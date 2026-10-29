package plugin

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Package limits for one plugin import. They bound both archive extraction and
// directory copying before anything lands in the plugins directory.
const (
	// MaxPackageEntries is the largest number of files and folders accepted.
	MaxPackageEntries = 200
	// MaxPackageSize is the largest total size after extraction, in bytes.
	MaxPackageSize = 10 << 20
	// MaxPackageFileSize is the largest single file, in bytes. It is the icon
	// limit; the manifest and skill limits are tighter.
	MaxPackageFileSize = MaxIconSize
)

// executableExtensions are the file extensions rejected as executable code or
// scripts. They only sharpen the message; the package layout allowlist rejects
// them either way.
var executableExtensions = map[string]bool{
	".exe": true, ".dll": true, ".so": true, ".dylib": true, ".bin": true,
	".com": true, ".msi": true, ".bat": true, ".cmd": true, ".ps1": true,
	".sh": true, ".bash": true, ".zsh": true, ".vbs": true, ".wsf": true,
	".jar": true, ".class": true, ".py": true, ".pyc": true, ".rb": true,
	".pl": true, ".php": true, ".js": true, ".mjs": true, ".cjs": true,
	".ts": true, ".go": true, ".rs": true, ".c": true, ".cpp": true,
	".h": true, ".java": true, ".wasm": true,
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

// ExtractPackage unpacks an uncompressed .bkplugin tar stream into dest. Only
// regular files and directories are accepted; layout and manifest validation
// happen after extraction.
func ExtractPackage(r io.Reader, dest string) error {
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return fmt.Errorf("plugin: create staging: %w", err)
	}
	counter := &packageCounter{}
	seen := make(map[string]bool)
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
			if err := os.MkdirAll(target, 0o700); err != nil {
				return fmt.Errorf("plugin: create package directory %s: %w", name, err)
			}
			continue
		}
		if err := writeArchiveFile(tr, target, hdr.Size); err != nil {
			return fmt.Errorf("plugin: write package file %s: %w", name, err)
		}
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

// writeArchiveFile writes exactly size bytes from r to target.
func writeArchiveFile(r io.Reader, target string, size int64) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(f, r)
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
	return nil
}

// CopyDirectory copies a local plugin directory to dest without following
// links. The source stays untouched so that a failed import never deletes
// user files.
func CopyDirectory(src, dest string) error {
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("plugin: stat plugin directory: %w", err)
	}
	if !info.IsDir() {
		return packageErrorf("", "选择的路径不是目录")
	}
	absSrc, err := filepath.Abs(src)
	if err != nil {
		return fmt.Errorf("plugin: resolve plugin directory: %w", err)
	}
	absDst, err := filepath.Abs(dest)
	if err != nil {
		return fmt.Errorf("plugin: resolve staging: %w", err)
	}
	if absDst == absSrc || strings.HasPrefix(absDst, absSrc+string(filepath.Separator)) {
		return packageErrorf("", "不能把插件目录复制到自身内部")
	}
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return fmt.Errorf("plugin: create staging: %w", err)
	}
	counter := &packageCounter{}
	seen := make(map[string]bool)
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("plugin: walk plugin directory: %w", walkErr)
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return fmt.Errorf("plugin: resolve plugin entry: %w", err)
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
			if err := os.MkdirAll(target, 0o700); err != nil {
				return fmt.Errorf("plugin: create plugin directory %s: %w", name, err)
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return packageErrorf(name, "不是普通文件")
		}
		entryInfo, err := d.Info()
		if err != nil {
			return fmt.Errorf("plugin: stat plugin entry %s: %w", name, err)
		}
		if err := counter.add(name, entryInfo.Size()); err != nil {
			return err
		}
		if err := copyFile(path, target, entryInfo.Size()); err != nil {
			return fmt.Errorf("plugin: copy plugin file %s: %w", name, err)
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
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
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
	return nil
}

// validateLayout checks that the extracted package only holds the manifest,
// the declared skills and the optional icon. Anything else — executables,
// scripts or unknown files — is rejected, so a plugin never carries code.
func validateLayout(dir string, m *Manifest) error {
	allowed := make(map[string]bool, len(m.Skills)+2)
	allowed[ManifestFileName] = true
	for _, skill := range m.Skills {
		allowed[filepath.ToSlash(skill)] = true
	}
	if m.Icon != "" {
		allowed[filepath.ToSlash(m.Icon)] = true
	}
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("plugin: walk package: %w", walkErr)
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return fmt.Errorf("plugin: resolve package entry: %w", err)
		}
		if rel == "." {
			return nil
		}
		name := filepath.ToSlash(rel)
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return packageErrorf(name, "不是普通文件")
		}
		if allowed[name] {
			return nil
		}
		if executableExtensions[strings.ToLower(filepath.Ext(name))] {
			return packageErrorf(name, "插件不允许包含可执行文件或脚本")
		}
		return packageErrorf(name, "插件包不允许包含该文件")
	})
}

// hashTree returns the SHA-256 of one directory: the sorted relative paths and
// file contents are hashed in order, so any content change changes the digest.
func hashTree(dir string) (string, error) {
	var names []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		names = append(names, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("plugin: walk plugin files: %w", err)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			return "", fmt.Errorf("plugin: read plugin file %s: %w", name, err)
		}
		io.WriteString(h, name)
		h.Write([]byte{0})
		h.Write(data)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// packageCounter bounds the entry count, the total size and the single file
// size of one package.
type packageCounter struct {
	entries int
	total   int64
}

func (c *packageCounter) add(name string, size int64) error {
	if size < 0 || size > MaxPackageFileSize {
		return packageErrorf(name, "单个文件超过 %d MiB", MaxPackageFileSize>>20)
	}
	c.entries++
	if c.entries > MaxPackageEntries {
		return packageErrorf("", "条目数超过 %d", MaxPackageEntries)
	}
	c.total += size
	if c.total > MaxPackageSize {
		return packageErrorf("", "总体积超过 %d MiB", MaxPackageSize>>20)
	}
	return nil
}
