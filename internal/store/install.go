package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Install sources shown in the launcher.
const (
	SourcePackage   = "本地安装"
	SourceDirectory = "目录安装"
)

// InstallErrorCode classifies an install or uninstall failure.
type InstallErrorCode string

// Install failure codes.
const (
	InstallInvalid  InstallErrorCode = "invalid"
	InstallConflict InstallErrorCode = "conflict"
	InstallVersion  InstallErrorCode = "version"
	InstallIO       InstallErrorCode = "io"
)

// InstallError is a failure the launcher can show to the user. Message is
// safe to display; Err keeps the underlying cause for the log.
type InstallError struct {
	Code    InstallErrorCode
	Message string
	Err     error
}

func (e *InstallError) Error() string { return e.Message }

func (e *InstallError) Unwrap() error { return e.Err }

func installErrorf(code InstallErrorCode, format string, args ...any) error {
	return &InstallError{Code: code, Message: fmt.Sprintf(format, args...)}
}

func installIOError(message string, err error) error {
	return &InstallError{Code: InstallIO, Message: message, Err: err}
}

// InstallRequest says where an app comes from.
type InstallRequest struct {
	// Path is a .bkapp file or a directory, depending on Directory.
	Path string
	// Directory installs a local folder instead of a package file.
	Directory bool
}

// Installer installs, enables and removes local apps. It owns no window: the
// app closes the window before calling Uninstall.
type Installer struct {
	AppsDir      string
	AppDataDir   string
	RegistryPath string
	Registry     *Registry
	ShellVersion string

	// saveMu serializes registry mutations with their save, so a status
	// update cannot be overwritten by an install that commits at the same
	// time. It is never held during copying.
	saveMu sync.Mutex
}

// Install validates and installs the app in req, returning the registry entry.
// It runs on a worker goroutine; callers must not hold UI state across it.
func (in *Installer) Install(ctx context.Context, req InstallRequest) (AppInfo, error) {
	if err := in.validate(); err != nil {
		return AppInfo{}, err
	}
	if err := ctx.Err(); err != nil {
		return AppInfo{}, err
	}
	if strings.TrimSpace(req.Path) == "" {
		return AppInfo{}, installErrorf(InstallInvalid, "未选择安装来源")
	}
	if !req.Directory && !strings.EqualFold(filepath.Ext(req.Path), ".bkapp") {
		return AppInfo{}, installErrorf(InstallInvalid, "请选择 .bkapp 安装包")
	}
	stagingRoot := filepath.Join(in.AppsDir, ".staging")
	if err := os.MkdirAll(stagingRoot, 0o755); err != nil {
		return AppInfo{}, installIOError("无写入应用目录的权限", err)
	}
	staging, err := os.MkdirTemp(stagingRoot, "install-")
	if err != nil {
		return AppInfo{}, installIOError("创建安装临时目录失败", err)
	}
	defer os.RemoveAll(staging)

	if err := in.populate(req, staging); err != nil {
		return AppInfo{}, classifyInstallError(err)
	}
	if err := ctx.Err(); err != nil {
		return AppInfo{}, err
	}
	manifest, err := ReadManifest(staging, in.ShellVersion)
	if err != nil {
		return AppInfo{}, classifyInstallError(err)
	}
	if err := in.checkInstallable(manifest.ID); err != nil {
		return AppInfo{}, err
	}
	final := filepath.Join(in.AppsDir, manifest.ID)
	if _, err := os.Lstat(final); err == nil {
		if err := os.RemoveAll(final); err != nil {
			return AppInfo{}, installIOError("移除旧版本失败，请稍后重试", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return AppInfo{}, installIOError("读取应用目录失败", err)
	}
	if err := ctx.Err(); err != nil {
		return AppInfo{}, err
	}
	if err := os.Rename(staging, final); err != nil {
		return AppInfo{}, installIOError("写入应用目录失败", err)
	}
	size := directorySize(final)
	app := AppInfo{
		ID:           manifest.ID,
		Name:         manifest.Name,
		Version:      manifest.Version,
		Size:         FormatSize(size),
		Source:       SourcePackage,
		Category:     manifest.Category,
		Summary:      manifest.Summary,
		ReleaseNotes: manifest.ReleaseNotes,
		Status:       StatusInstalled,
	}
	if req.Directory {
		app.Source = SourceDirectory
	}
	if err := in.commit(app); err != nil {
		os.RemoveAll(final)
		return AppInfo{}, err
	}
	return app, nil
}

// Uninstall removes the app program, optionally its private data, and then the
// registry entry. The app window must be closed before the call.
func (in *Installer) Uninstall(ctx context.Context, id string, removeData bool) error {
	if err := in.validate(); err != nil {
		return err
	}
	if !appIDPattern.MatchString(id) {
		return installErrorf(InstallInvalid, "应用 ID 无效")
	}
	if _, ok := in.Registry.Get(id); !ok {
		return installErrorf(InstallInvalid, "应用未安装")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	program := filepath.Join(in.AppsDir, id)
	if err := os.RemoveAll(program); err != nil {
		return installIOError("删除应用文件失败，请关闭应用窗口后重试", err)
	}
	var dataErr error
	if removeData {
		if err := os.RemoveAll(filepath.Join(in.AppDataDir, id)); err != nil {
			dataErr = installIOError("程序已移除，但应用数据删除失败", err)
		}
	}
	in.saveMu.Lock()
	defer in.saveMu.Unlock()
	previous, hadPrevious := in.Registry.Get(id)
	in.Registry.Remove(id)
	if err := SaveRegistry(in.RegistryPath, in.Registry); err != nil {
		if hadPrevious {
			in.Registry.Upsert(previous)
		}
		return installIOError("更新应用索引失败", err)
	}
	return dataErr
}

// SetStatus updates an app status and saves the registry.
func (in *Installer) SetStatus(id string, status Status) error {
	if err := in.validate(); err != nil {
		return err
	}
	in.saveMu.Lock()
	defer in.saveMu.Unlock()
	previous, hadPrevious := in.Registry.Get(id)
	if !in.Registry.SetStatus(id, status) {
		return installErrorf(InstallInvalid, "应用未安装")
	}
	if err := SaveRegistry(in.RegistryPath, in.Registry); err != nil {
		if hadPrevious {
			in.Registry.Upsert(previous)
		}
		return installIOError("更新应用索引失败", err)
	}
	return nil
}

// MarkOpened records the last launch time and saves the registry.
func (in *Installer) MarkOpened(id string) (AppInfo, error) {
	if err := in.validate(); err != nil {
		return AppInfo{}, err
	}
	in.saveMu.Lock()
	defer in.saveMu.Unlock()
	previous, hadPrevious := in.Registry.Get(id)
	if !in.Registry.Touch(id, time.Now().Unix()) {
		return AppInfo{}, installErrorf(InstallInvalid, "应用未安装")
	}
	if err := SaveRegistry(in.RegistryPath, in.Registry); err != nil {
		if hadPrevious {
			in.Registry.Upsert(previous)
		}
		return AppInfo{}, installIOError("更新应用索引失败", err)
	}
	app, _ := in.Registry.Get(id)
	return app, nil
}

// CleanStaging removes leftovers of interrupted installs. Failures are left
// for the next start rather than blocking startup.
func (in *Installer) CleanStaging() {
	staging := filepath.Join(in.AppsDir, ".staging")
	entries, err := os.ReadDir(staging)
	if err != nil {
		return
	}
	for _, entry := range entries {
		os.RemoveAll(filepath.Join(staging, entry.Name()))
	}
}

func (in *Installer) validate() error {
	if in.AppsDir == "" || in.RegistryPath == "" || in.Registry == nil {
		return fmt.Errorf("store: installer is not configured")
	}
	return nil
}

// checkInstallable rejects a reinstall of an app that already works. Corrupt
// and disabled apps may be replaced to repair them.
func (in *Installer) checkInstallable(id string) error {
	app, ok := in.Registry.Get(id)
	if !ok {
		return nil
	}
	switch app.Status {
	case StatusInstalled, StatusRunning, StatusUpdatable, StatusUpdating:
		return installErrorf(InstallConflict, "该应用已安装，更新功能尚未开放")
	case StatusInstalling:
		return installErrorf(InstallConflict, "该应用正在安装")
	default:
		return nil
	}
}

func (in *Installer) populate(req InstallRequest, staging string) error {
	if req.Directory {
		return CopyDirectory(req.Path, staging)
	}
	f, err := os.Open(req.Path)
	if err != nil {
		return installIOError("无法读取安装包", err)
	}
	defer f.Close()
	return ExtractPackage(f, staging)
}

func (in *Installer) commit(app AppInfo) (err error) {
	in.saveMu.Lock()
	defer in.saveMu.Unlock()
	previous, hadPrevious := in.Registry.Get(app.ID)
	in.Registry.Upsert(app)
	if saveErr := SaveRegistry(in.RegistryPath, in.Registry); saveErr != nil {
		if hadPrevious {
			in.Registry.Upsert(previous)
		} else {
			in.Registry.Remove(app.ID)
		}
		return installIOError("写入应用索引失败", saveErr)
	}
	return nil
}

// classifyInstallError turns manifest and package errors into user-facing
// install errors.
func classifyInstallError(err error) error {
	var manifestErr *ManifestError
	if errors.As(err, &manifestErr) {
		if manifestErr.Field == "min_shell_version" {
			return &InstallError{Code: InstallVersion, Message: manifestErr.Error(), Err: err}
		}
		return &InstallError{Code: InstallInvalid, Message: manifestErr.Error(), Err: err}
	}
	var packageErr *PackageError
	if errors.As(err, &packageErr) {
		return &InstallError{Code: InstallInvalid, Message: packageErr.Error(), Err: err}
	}
	var installErr *InstallError
	if errors.As(err, &installErr) {
		return err
	}
	return installIOError("安装失败，请检查安装包或磁盘空间", err)
}

// FormatSize renders a byte count for the launcher.
func FormatSize(bytes int64) string {
	const (
		kb = int64(1024)
		mb = kb * 1024
	)
	switch {
	case bytes >= mb:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(mb))
	case bytes >= kb:
		return fmt.Sprintf("%.0f KB", float64(bytes)/float64(kb))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

func directorySize(root string) int64 {
	var total int64
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}
