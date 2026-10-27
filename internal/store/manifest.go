package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ManifestFileName is the name of the app manifest at the root of an app
// package and of an installed app directory.
const ManifestFileName = "manifest.json"

// Manifest limits. They bound what the installer accepts before it touches
// the disk.
const (
	// ManifestSchema is the only manifest schema this shell understands.
	ManifestSchema = 1
	// MaxManifestSize is the largest accepted manifest file, in bytes.
	MaxManifestSize = 64 << 10
	// MaxIconSize is the largest accepted icon file, in bytes.
	MaxIconSize = 1 << 20

	// DefaultWindowWidth and DefaultWindowHeight size an app window when the
	// manifest leaves the window out.
	DefaultWindowWidth     = 1080
	DefaultWindowHeight    = 720
	DefaultWindowMinWidth  = 640
	DefaultWindowMinHeight = 480

	// MinWindowWidth and MaxWindowWidth bound a window dimension.
	MinWindowWidth  = 320
	MaxWindowWidth  = 4096
	MinWindowHeight = 320
	MaxWindowHeight = 4096
)

var (
	appIDPattern   = regexp.MustCompile(`^[a-z][a-z0-9-]{1,63}$`)
	versionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

	entryExtensions = []string{".html", ".htm"}
	iconExtensions  = []string{".svg", ".png", ".jpg", ".jpeg", ".webp"}
	// reservedNames are the Windows device names that cannot be a file or
	// directory, with or without an extension.
	reservedNames = map[string]bool{
		"CON": true, "PRN": true, "AUX": true, "NUL": true,
		"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
		"COM6": true, "COM7": true, "COM8": true, "COM9": true,
		"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
		"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
	}
	knownCategories = map[string]bool{
		"efficiency": true,
		"creation":   true,
		"developer":  true,
	}
)

// ManifestError describes why a manifest is invalid. Field is the manifest
// field at fault, empty when the whole document is at fault.
type ManifestError struct {
	Field  string
	Reason string
}

func (e *ManifestError) Error() string {
	if e.Field == "" {
		return "清单无效：" + e.Reason
	}
	return "清单无效：" + e.Field + " " + e.Reason
}

func manifestErrorf(field, format string, args ...any) error {
	return &ManifestError{Field: field, Reason: fmt.Sprintf(format, args...)}
}

// ManifestWindow is the window size an app asks for. Zero fields fall back to
// the defaults.
type ManifestWindow struct {
	Width     int `json:"width"`
	Height    int `json:"height"`
	MinWidth  int `json:"min_width"`
	MinHeight int `json:"min_height"`
}

// Manifest is app manifest v1. Unknown fields are ignored so that newer
// packages stay installable on older shells.
type Manifest struct {
	Schema          int            `json:"schema"`
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	Version         string         `json:"version"`
	Entry           string         `json:"entry"`
	Icon            string         `json:"icon"`
	MinShellVersion string         `json:"min_shell_version"`
	Summary         string         `json:"summary"`
	Category        string         `json:"category"`
	ReleaseNotes    string         `json:"release_notes"`
	Window          ManifestWindow `json:"window"`
}

// ParseManifest parses and validates a manifest. shellVersion is the running
// shell version; a package that needs a newer shell is rejected. An empty
// shellVersion skips the compatibility check.
func ParseManifest(data []byte, shellVersion string) (*Manifest, error) {
	if len(data) > MaxManifestSize {
		return nil, manifestErrorf("", "文件超过 64 KiB")
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, manifestErrorf("", "JSON 解析失败")
	}
	if err := m.validate(shellVersion); err != nil {
		return nil, err
	}
	return &m, nil
}

// ReadManifest reads and validates apps/<dir>/manifest.json, then checks that
// the entry and icon exist inside the directory.
func ReadManifest(dir, shellVersion string) (*Manifest, error) {
	path := filepath.Join(dir, ManifestFileName)
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, manifestErrorf("", "缺少 manifest.json")
		}
		return nil, fmt.Errorf("store: stat manifest: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, manifestErrorf("", "manifest.json 不是普通文件")
	}
	if info.Size() > MaxManifestSize {
		return nil, manifestErrorf("", "文件超过 64 KiB")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("store: read manifest: %w", err)
	}
	m, err := ParseManifest(data, shellVersion)
	if err != nil {
		return nil, err
	}
	if err := m.verifyFiles(dir); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manifest) validate(shellVersion string) error {
	if m.Schema != ManifestSchema {
		return manifestErrorf("schema", "必须为 %d", ManifestSchema)
	}
	if !appIDPattern.MatchString(m.ID) {
		return manifestErrorf("id", "必须由小写字母、数字和连字符组成，以字母开头，长度 2–64")
	}
	m.Name = strings.TrimSpace(m.Name)
	if n := utf8.RuneCountInString(m.Name); n < 1 || n > 64 {
		return manifestErrorf("name", "长度必须为 1–64 字符")
	}
	if !versionPattern.MatchString(m.Version) {
		return manifestErrorf("version", "必须为三段数字版本，如 1.0.0")
	}
	if err := validateAssetPath(m.Entry, entryExtensions); err != nil {
		return manifestErrorf("entry", "%s", err.Error())
	}
	if err := validateAssetPath(m.Icon, iconExtensions); err != nil {
		return manifestErrorf("icon", "%s", err.Error())
	}
	if !versionPattern.MatchString(m.MinShellVersion) {
		return manifestErrorf("min_shell_version", "必须为三段数字版本，如 1.0.0")
	}
	if shellVersion != "" && CompareVersions(m.MinShellVersion, shellVersion) > 0 {
		return manifestErrorf("min_shell_version", "需要壳版本 %s，当前为 %s", m.MinShellVersion, shellVersion)
	}
	m.Summary = strings.TrimSpace(m.Summary)
	if n := utf8.RuneCountInString(m.Summary); n < 1 || n > 200 {
		return manifestErrorf("summary", "长度必须为 1–200 字符")
	}
	m.Category = strings.TrimSpace(m.Category)
	if !knownCategories[m.Category] {
		return manifestErrorf("category", "必须为 efficiency、creation 或 developer")
	}
	m.ReleaseNotes = strings.TrimSpace(m.ReleaseNotes)
	if utf8.RuneCountInString(m.ReleaseNotes) > 500 {
		return manifestErrorf("release_notes", "长度不能超过 500 字符")
	}
	window, err := normalizeWindow(m.Window)
	if err != nil {
		return err
	}
	m.Window = window
	return nil
}

// verifyFiles checks that the entry and icon named by the manifest exist as
// regular files inside dir.
func (m *Manifest) verifyFiles(dir string) error {
	entry, err := resolveInRoot(dir, m.Entry)
	if err != nil {
		return manifestErrorf("entry", "%s", err.Error())
	}
	info, err := os.Lstat(entry)
	if err != nil || !info.Mode().IsRegular() {
		return manifestErrorf("entry", "文件不存在")
	}
	icon, err := resolveInRoot(dir, m.Icon)
	if err != nil {
		return manifestErrorf("icon", "%s", err.Error())
	}
	info, err = os.Lstat(icon)
	if err != nil || !info.Mode().IsRegular() {
		return manifestErrorf("icon", "文件不存在")
	}
	if info.Size() > MaxIconSize {
		return manifestErrorf("icon", "文件超过 1 MiB")
	}
	return nil
}

// normalizeWindow fills in the defaults and validates the bounds.
func normalizeWindow(w ManifestWindow) (ManifestWindow, error) {
	if w.Width == 0 {
		w.Width = DefaultWindowWidth
	}
	if w.Height == 0 {
		w.Height = DefaultWindowHeight
	}
	if w.MinWidth == 0 {
		w.MinWidth = DefaultWindowMinWidth
	}
	if w.MinHeight == 0 {
		w.MinHeight = DefaultWindowMinHeight
	}
	if w.Width < MinWindowWidth || w.Width > MaxWindowWidth {
		return ManifestWindow{}, manifestErrorf("window.width", "必须在 %d–%d 之间", MinWindowWidth, MaxWindowWidth)
	}
	if w.Height < MinWindowHeight || w.Height > MaxWindowHeight {
		return ManifestWindow{}, manifestErrorf("window.height", "必须在 %d–%d 之间", MinWindowHeight, MaxWindowHeight)
	}
	if w.MinWidth < MinWindowWidth || w.MinWidth > w.Width {
		return ManifestWindow{}, manifestErrorf("window.min_width", "必须在 %d–默认宽度之间", MinWindowWidth)
	}
	if w.MinHeight < MinWindowHeight || w.MinHeight > w.Height {
		return ManifestWindow{}, manifestErrorf("window.min_height", "必须在 %d–默认高度之间", MinWindowHeight)
	}
	return w, nil
}

// validateAssetPath reports whether p is a safe relative path with one of the
// accepted extensions.
func validateAssetPath(p string, extensions []string) error {
	cleaned, err := CleanRelativePath(p)
	if err != nil {
		return err
	}
	ext := strings.ToLower(filepath.Ext(cleaned))
	for _, allowed := range extensions {
		if ext == allowed {
			return nil
		}
	}
	return fmt.Errorf("必须是以 %s 结尾的文件", strings.Join(extensions, "、"))
}

// CleanRelativePath validates a package-relative path and returns it with
// forward slashes. It rejects absolute paths, drive prefixes, backslashes,
// empty segments, ".", "..", Windows reserved names and over-deep paths.
func CleanRelativePath(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("路径不能为空")
	}
	if strings.ContainsAny(p, "\\\x00") {
		return "", fmt.Errorf("路径不能包含反斜杠或空字符")
	}
	if strings.HasPrefix(p, "/") || filepath.IsAbs(p) || hasDrivePrefix(p) {
		return "", fmt.Errorf("路径必须是相对路径")
	}
	if strings.HasSuffix(p, "/") {
		return "", fmt.Errorf("路径不能以斜杠结尾")
	}
	if len(p) > 255 {
		return "", fmt.Errorf("路径长度不能超过 255")
	}
	segments := strings.Split(p, "/")
	if len(segments) > 16 {
		return "", fmt.Errorf("路径层级不能超过 16")
	}
	for _, segment := range segments {
		if err := validateSegment(segment); err != nil {
			return "", err
		}
	}
	return p, nil
}

// ValidateRelativePath is CleanRelativePath for callers outside the store
// package that only need the check.
func ValidateRelativePath(p string) error {
	_, err := CleanRelativePath(p)
	return err
}

func hasDrivePrefix(p string) bool {
	return len(p) >= 2 && p[1] == ':' &&
		((p[0] >= 'a' && p[0] <= 'z') || (p[0] >= 'A' && p[0] <= 'Z'))
}

func validateSegment(segment string) error {
	if segment == "" {
		return fmt.Errorf("路径不能包含空段")
	}
	if segment == "." || segment == ".." {
		return fmt.Errorf("路径不能包含 . 或 ..")
	}
	if strings.HasSuffix(segment, ".") || strings.HasSuffix(segment, " ") {
		return fmt.Errorf("路径段不能以点或空格结尾")
	}
	base := segment
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	if reservedNames[strings.ToUpper(base)] {
		return fmt.Errorf("路径段使用了系统保留名 %s", segment)
	}
	for _, r := range segment {
		if r < 0x20 {
			return fmt.Errorf("路径不能包含控制字符")
		}
	}
	return nil
}

// resolveInRoot joins the validated relative path p to root and confirms the
// result stays inside root.
func resolveInRoot(root, p string) (string, error) {
	cleaned, err := CleanRelativePath(p)
	if err != nil {
		return "", err
	}
	joined := filepath.Join(root, filepath.FromSlash(cleaned))
	rel, err := filepath.Rel(root, joined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("路径超出应用目录")
	}
	return joined, nil
}

// CompareVersions compares two three-part versions and returns -1, 0 or 1.
// Invalid versions compare as 0.0.0.
func CompareVersions(a, b string) int {
	pa, pb := parseVersion(a), parseVersion(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// VersionAtLeast reports whether version is the same as or newer than minimum.
func VersionAtLeast(version, minimum string) bool {
	return CompareVersions(version, minimum) >= 0
}

func parseVersion(v string) [3]int {
	parts := strings.Split(v, ".")
	var out [3]int
	for i := 0; i < len(parts) && i < 3; i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil || n < 0 {
			return [3]int{}
		}
		out[i] = n
	}
	return out
}
