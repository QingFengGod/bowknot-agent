// Package plugin 实现声明式插件的清单校验、本地导入、全局注册表、
// 每 Agent 启用清单的宿主侧支撑与内置工具。
package plugin

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

// ManifestFileName is the manifest file at the root of a plugin package and of
// an installed plugin directory.
const ManifestFileName = "manifest.json"

// Manifest limits. They bound what the store accepts before an install.
const (
	// ManifestSchema is the only manifest schema this shell understands.
	ManifestSchema = 1
	// MaxManifestSize is the largest accepted manifest file, in bytes.
	MaxManifestSize = 64 << 10
	// MaxSkillSize is the largest accepted skill file, in bytes.
	MaxSkillSize = 256 << 10
	// MaxIconSize is the largest accepted icon file, in bytes.
	MaxIconSize = 1 << 20
	// MaxSkills is the largest number of skills one plugin may declare.
	MaxSkills = 20
)

// Built-in tool names a manifest may declare. Permissions are granted by the
// host per tool name; a manifest cannot declare extra permissions.
const (
	ToolFSRead       = "fs.read"
	ToolFSWrite      = "fs.write"
	ToolMemorySearch = "memory.search"
	ToolGroupPost    = "group.post"
	ToolGroupRequest = "group.request"
	ToolDelegate     = "delegate"
)

// allowedTools is every tool name a manifest may declare.
var allowedTools = map[string]bool{
	ToolFSRead:       true,
	ToolFSWrite:      true,
	ToolMemorySearch: true,
	ToolGroupPost:    true,
	ToolGroupRequest: true,
	ToolDelegate:     true,
}

// hostTools are the tools this build injects and executes. The group and
// delegate tools belong to the group collaboration plans and are not injected
// until their host exists.
var hostTools = []string{ToolFSRead, ToolFSWrite, ToolMemorySearch}

// ToolSchema describes one built-in tool for the system prompt injection.
type ToolSchema struct {
	Name        string
	Description string
	Params      string
}

// toolSchemas are the descriptions of the tools the host executes.
var toolSchemas = map[string]ToolSchema{
	ToolFSRead: {
		Name:        ToolFSRead,
		Description: "读取本 Agent 工作区内的文本文件。",
		Params:      `{"path":"工作区内相对路径"}`,
	},
	ToolFSWrite: {
		Name:        ToolFSWrite,
		Description: "写入本 Agent 工作区内的文本文件，写入前需要用户批准。",
		Params:      `{"path":"工作区内相对路径","content":"UTF-8 文本内容"}`,
	},
	ToolMemorySearch: {
		Name:        ToolMemorySearch,
		Description: "检索本 Agent 的长期记忆，返回相关片段。",
		Params:      `{"query":"检索词","limit":"返回条数，1-10，默认 5"}`,
	},
}

// ToolSchemas returns the schemas of the given tool names in hostTools order.
// Unknown names and tools without a host implementation are skipped, so the
// injected list never promises a tool the shell cannot run.
func ToolSchemas(names []string) []ToolSchema {
	declared := make(map[string]bool, len(names))
	for _, name := range names {
		declared[name] = true
	}
	out := make([]ToolSchema, 0, len(names))
	for _, name := range hostTools {
		if declared[name] {
			if schema, ok := toolSchemas[name]; ok {
				out = append(out, schema)
			}
		}
	}
	return out
}

var (
	pluginIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,63}$`)
	versionPattern  = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

	iconExtensions = []string{".svg", ".png", ".jpg", ".jpeg", ".webp"}
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

// Manifest is a plugin manifest v1. Unknown fields are ignored so that newer
// packages stay importable on older shells.
type Manifest struct {
	Schema          int      `json:"schema"`
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Version         string   `json:"version"`
	MinShellVersion string   `json:"min_shell_version"`
	Description     string   `json:"description"`
	Author          string   `json:"author"`
	Skills          []string `json:"skills"`
	Tools           []string `json:"tools"`
	Icon            string   `json:"icon,omitempty"`
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

// ReadManifest reads and validates <dir>/manifest.json, then checks that every
// declared skill and the optional icon exist inside dir with their size limits.
func ReadManifest(dir, shellVersion string) (*Manifest, error) {
	path := filepath.Join(dir, ManifestFileName)
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, manifestErrorf("", "缺少 manifest.json")
		}
		return nil, fmt.Errorf("plugin: stat manifest: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, manifestErrorf("", "manifest.json 不是普通文件")
	}
	if info.Size() > MaxManifestSize {
		return nil, manifestErrorf("", "文件超过 64 KiB")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("plugin: read manifest: %w", err)
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
	if !pluginIDPattern.MatchString(m.ID) {
		return manifestErrorf("id", "必须由小写字母开头的 2–64 位小写字母、数字或连字符组成")
	}
	m.Name = strings.TrimSpace(m.Name)
	if n := utf8.RuneCountInString(m.Name); n < 1 || n > 64 {
		return manifestErrorf("name", "长度必须为 1–64 字符")
	}
	if !versionPattern.MatchString(m.Version) {
		return manifestErrorf("version", "必须为三段数字版本，如 1.0.0")
	}
	if !versionPattern.MatchString(m.MinShellVersion) {
		return manifestErrorf("min_shell_version", "必须为三段数字版本，如 1.0.0")
	}
	if shellVersion != "" && CompareVersions(m.MinShellVersion, shellVersion) > 0 {
		return manifestErrorf("min_shell_version", "需要壳版本 %s，当前为 %s", m.MinShellVersion, shellVersion)
	}
	m.Description = strings.TrimSpace(m.Description)
	if n := utf8.RuneCountInString(m.Description); n < 1 || n > 500 {
		return manifestErrorf("description", "长度必须为 1–500 字符")
	}
	m.Author = strings.TrimSpace(m.Author)
	if n := utf8.RuneCountInString(m.Author); n < 1 || n > 64 {
		return manifestErrorf("author", "长度必须为 1–64 字符")
	}
	if err := m.validateSkills(); err != nil {
		return err
	}
	if err := m.validateTools(); err != nil {
		return err
	}
	if err := validateAssetPath(m.Icon, iconExtensions); err != nil {
		return manifestErrorf("icon", "%s", err.Error())
	}
	return nil
}

// validateSkills checks the skill path list: 1–20 entries, package-relative
// markdown paths under skills/, unique regardless of case.
func (m *Manifest) validateSkills() error {
	if n := len(m.Skills); n < 1 || n > MaxSkills {
		return manifestErrorf("skills", "必须声明 1–%d 个技能文件", MaxSkills)
	}
	seen := make(map[string]bool, len(m.Skills))
	for _, skill := range m.Skills {
		cleaned, err := CleanRelativePath(skill)
		if err != nil {
			return manifestErrorf("skills", "%s：%s", skill, err.Error())
		}
		if !strings.HasPrefix(cleaned, "skills/") {
			return manifestErrorf("skills", "%s 必须位于 skills/ 目录内", skill)
		}
		if ext := strings.ToLower(filepath.Ext(cleaned)); ext != ".md" {
			return manifestErrorf("skills", "%s 必须是 .md 文件", skill)
		}
		key := strings.ToLower(cleaned)
		if seen[key] {
			return manifestErrorf("skills", "%s 重复声明", skill)
		}
		seen[key] = true
	}
	return nil
}

// validateTools checks that every declared tool is a known built-in tool.
func (m *Manifest) validateTools() error {
	seen := make(map[string]bool, len(m.Tools))
	for _, tool := range m.Tools {
		if !allowedTools[tool] {
			return manifestErrorf("tools", "未知工具 %q，只能声明内置工具", tool)
		}
		if seen[tool] {
			return manifestErrorf("tools", "%s 重复声明", tool)
		}
		seen[tool] = true
	}
	return nil
}

// verifyFiles checks that every declared skill and the optional icon exist as
// regular files inside dir, within their size limits.
func (m *Manifest) verifyFiles(dir string) error {
	for _, skill := range m.Skills {
		path, err := resolveInRoot(dir, skill)
		if err != nil {
			return manifestErrorf("skills", "%s：%s", skill, err.Error())
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return manifestErrorf("skills", "%s 文件不存在", skill)
		}
		if info.Size() > MaxSkillSize {
			return manifestErrorf("skills", "%s 超过 256 KiB", skill)
		}
	}
	if m.Icon == "" {
		return nil
	}
	path, err := resolveInRoot(dir, m.Icon)
	if err != nil {
		return manifestErrorf("icon", "%s", err.Error())
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return manifestErrorf("icon", "文件不存在")
	}
	if info.Size() > MaxIconSize {
		return manifestErrorf("icon", "文件超过 1 MiB")
	}
	return nil
}

// validateAssetPath reports whether p is a safe relative path with one of the
// accepted extensions. An empty path is only valid when the asset is optional.
func validateAssetPath(p string, extensions []string) error {
	if p == "" {
		return nil
	}
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

// CleanRelativePath validates a package-relative path and returns it unchanged.
// It rejects absolute paths, drive prefixes, backslashes, empty segments,
// ".", ".." and over-long or control-character paths.
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
		switch {
		case segment == "":
			return "", fmt.Errorf("路径不能包含空段")
		case segment == "." || segment == "..":
			return "", fmt.Errorf("路径不能包含 . 或 ..")
		}
		for _, r := range segment {
			if r < 0x20 {
				return "", fmt.Errorf("路径不能包含控制字符")
			}
		}
	}
	return p, nil
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
		return "", fmt.Errorf("路径超出插件目录")
	}
	return joined, nil
}

func hasDrivePrefix(p string) bool {
	return len(p) >= 2 && p[1] == ':' &&
		((p[0] >= 'a' && p[0] <= 'z') || (p[0] >= 'A' && p[0] <= 'Z'))
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
