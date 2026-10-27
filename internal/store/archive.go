package store

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Package size limits. They bound both archive extraction and directory
// copying before anything lands in the app directory.
const (
	// MaxPackageEntries is the largest number of files and folders accepted.
	MaxPackageEntries = 2000
	// MaxPackageSize is the largest total size after extraction, in bytes.
	MaxPackageSize = 200 << 20
	// MaxPackageFileSize is the largest single file, in bytes.
	MaxPackageFileSize = 64 << 20
)

// ArchiveLimits bounds one extraction or directory copy. The zero value is
// replaced by DefaultArchiveLimits.
type ArchiveLimits struct {
	MaxEntries   int
	MaxTotalSize int64
	MaxFileSize  int64
}

// DefaultLimits returns the package limits the installer uses.
func DefaultLimits() ArchiveLimits {
	return ArchiveLimits{
		MaxEntries:   MaxPackageEntries,
		MaxTotalSize: MaxPackageSize,
		MaxFileSize:  MaxPackageFileSize,
	}
}

// PackageError describes why a package entry is rejected. Entry is the
// package-relative path at fault, or empty when the whole package is at
// fault; it never contains a host path.
type PackageError struct {
	Entry  string
	Reason string
}

func (e *PackageError) Error() string {
	if e.Entry == "" {
		return "安装内容无效：" + e.Reason
	}
	return "安装内容无效：" + e.Entry + " " + e.Reason
}

func packageErrorf(entry, format string, args ...any) error {
	return &PackageError{Entry: entry, Reason: fmt.Sprintf(format, args...)}
}

// ExtractPackage unpacks an uncompressed .bkapp tar stream into dest. Only
// regular files and directories are accepted; the manifest must sit at the
// package root.
func ExtractPackage(r io.Reader, dest string) error {
	return extractPackage(r, dest, DefaultLimits())
}

func extractPackage(r io.Reader, dest string, limits ArchiveLimits) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return fmt.Errorf("store: create staging: %w", err)
	}
	counter := &packageCounter{limits: limits.withDefaults()}
	seen := make(map[string]bool)
	hasManifest := false
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return packageErrorf("", "tar 读取失败")
		}
		name, isDir, err := archiveEntryName(hdr)
		if err != nil {
			return err
		}
		if name == "" {
			continue
		}
		if err := checkEntryType(hdr); err != nil {
			return packageErrorf(name, "%s", err.Error())
		}
		if err := checkDuplicate(seen, name); err != nil {
			return err
		}
		if err := counter.add(name, hdr.Size); err != nil {
			return err
		}
		target, err := resolveInRoot(dest, name)
		if err != nil {
			return packageErrorf(name, "%s", err.Error())
		}
		if isDir {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("store: create package directory %s: %w", name, err)
			}
			continue
		}
		if strings.EqualFold(name, ManifestFileName) {
			hasManifest = true
		}
		if err := writeArchiveFile(tr, target, hdr.Size); err != nil {
			return fmt.Errorf("store: write package file %s: %w", name, err)
		}
	}
	if !hasManifest {
		return packageErrorf("", "缺少根目录 manifest.json")
	}
	return nil
}

// archiveEntryName normalizes a tar header name and validates it. It returns
// an empty name for the package root, which callers skip.
func archiveEntryName(hdr *tar.Header) (name string, isDir bool, err error) {
	raw := hdr.Name
	if raw == "" {
		return "", false, packageErrorf("", "条目路径为空")
	}
	if strings.ContainsAny(raw, "\\\x00") {
		return "", false, packageErrorf(raw, "路径不能包含反斜杠或空字符")
	}
	if strings.HasPrefix(raw, "/") || hasDrivePrefix(raw) {
		return "", false, packageErrorf(raw, "路径必须是相对路径")
	}
	for strings.HasPrefix(raw, "./") {
		raw = raw[2:]
	}
	isDir = hdr.Typeflag == tar.TypeDir
	if isDir {
		raw = strings.TrimRight(raw, "/")
	} else if strings.HasSuffix(raw, "/") {
		return "", false, packageErrorf(raw, "文件路径不能以斜杠结尾")
	}
	if raw == "" || raw == "." {
		return "", false, nil
	}
	if _, err := CleanRelativePath(raw); err != nil {
		return "", false, packageErrorf(raw, "%s", err.Error())
	}
	return raw, isDir, nil
}

// checkEntryType accepts regular files and directories only.
func checkEntryType(hdr *tar.Header) error {
	switch hdr.Typeflag {
	case tar.TypeReg, tar.TypeRegA, tar.TypeDir:
		return nil
	case tar.TypeSymlink:
		return fmt.Errorf("不允许符号链接")
	case tar.TypeLink:
		return fmt.Errorf("不允许硬链接")
	case tar.TypeChar, tar.TypeBlock:
		return fmt.Errorf("不允许设备文件")
	case tar.TypeFifo:
		return fmt.Errorf("不允许命名管道")
	default:
		return fmt.Errorf("不支持的条目类型")
	}
}

// checkDuplicate rejects a path that differs only in case from an earlier
// entry, which would overwrite it on a case-insensitive file system.
func checkDuplicate(seen map[string]bool, name string) error {
	key := strings.ToLower(filepath.ToSlash(name))
	if seen[key] {
		return packageErrorf(name, "与已有条目重复")
	}
	seen[key] = true
	return nil
}

// writeArchiveFile writes exactly size bytes from tr to target with 0644.
func writeArchiveFile(tr io.Reader, target string, size int64) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(f, tr)
	closeErr := f.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written != size {
		return fmt.Errorf("条目大小不一致")
	}
	return os.Chmod(target, 0o644)
}

// CopyDirectory copies a local app directory to dest without following links.
// The source stays untouched so that uninstalling never deletes user files.
func CopyDirectory(src, dest string) error {
	return copyDirectory(src, dest, DefaultLimits())
}

func copyDirectory(src, dest string, limits ArchiveLimits) error {
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("store: stat app directory: %w", err)
	}
	if !info.IsDir() {
		return packageErrorf("", "选择的路径不是目录")
	}
	absSrc, err := filepath.Abs(src)
	if err != nil {
		return fmt.Errorf("store: resolve app directory: %w", err)
	}
	absDst, err := filepath.Abs(dest)
	if err != nil {
		return fmt.Errorf("store: resolve staging: %w", err)
	}
	if absDst == absSrc || strings.HasPrefix(absDst, absSrc+string(filepath.Separator)) {
		return packageErrorf("", "不能把安装目录复制到自身内部")
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return fmt.Errorf("store: create staging: %w", err)
	}
	counter := &packageCounter{limits: limits.withDefaults()}
	seen := make(map[string]bool)
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("store: walk app directory: %w", walkErr)
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return fmt.Errorf("store: resolve app entry: %w", err)
		}
		if rel == "." {
			return nil
		}
		name := filepath.ToSlash(rel)
		if _, err := CleanRelativePath(name); err != nil {
			return packageErrorf(name, "%s", err.Error())
		}
		if d.Type()&os.ModeSymlink != 0 {
			return packageErrorf(name, "不允许符号链接")
		}
		if err := checkDuplicate(seen, name); err != nil {
			return err
		}
		target, err := resolveInRoot(dest, name)
		if err != nil {
			return packageErrorf(name, "%s", err.Error())
		}
		if d.IsDir() {
			if err := counter.add(name, 0); err != nil {
				return err
			}
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("store: create app directory %s: %w", name, err)
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return packageErrorf(name, "不是普通文件")
		}
		entryInfo, err := d.Info()
		if err != nil {
			return fmt.Errorf("store: stat app entry %s: %w", name, err)
		}
		if err := counter.add(name, entryInfo.Size()); err != nil {
			return err
		}
		if err := copyFile(path, target, entryInfo.Size()); err != nil {
			return fmt.Errorf("store: copy app file %s: %w", name, err)
		}
		return nil
	})
}

// copyFile copies exactly size bytes preserving nothing but the content.
func copyFile(src, dst string, size int64) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written != size {
		return fmt.Errorf("条目大小不一致")
	}
	return os.Chmod(dst, 0o644)
}

type packageCounter struct {
	limits  ArchiveLimits
	entries int
	total   int64
}

func (l ArchiveLimits) withDefaults() ArchiveLimits {
	def := DefaultLimits()
	if l.MaxEntries <= 0 {
		l.MaxEntries = def.MaxEntries
	}
	if l.MaxTotalSize <= 0 {
		l.MaxTotalSize = def.MaxTotalSize
	}
	if l.MaxFileSize <= 0 {
		l.MaxFileSize = def.MaxFileSize
	}
	return l
}

func (c *packageCounter) add(name string, size int64) error {
	if size < 0 || size > c.limits.MaxFileSize {
		return packageErrorf(name, "单个文件超过 %d MiB", c.limits.MaxFileSize>>20)
	}
	c.entries++
	if c.entries > c.limits.MaxEntries {
		return packageErrorf("", "条目数超过 %d", c.limits.MaxEntries)
	}
	c.total += size
	if c.total > c.limits.MaxTotalSize {
		return packageErrorf("", "解压后体积超过 %d MiB", c.limits.MaxTotalSize>>20)
	}
	return nil
}
