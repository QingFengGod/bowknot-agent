package agent

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Agent workspace layout and file limits.
const (
	// MainAgentID is the ID and directory of the only main agent. It is fixed
	// and cannot be deleted.
	MainAgentID = "main"
	// AgentsFileName is the entry file that defines the prompt assembly order.
	AgentsFileName = "AGENTS.md"
	// PersonaFileName holds the persona text of the agent.
	PersonaFileName = "PERSONA.md"
	// UserFileName holds what one agent knows about the user.
	UserFileName = "USER.md"
	// PluginsFileName is the per-agent plugin enable list.
	PluginsFileName = "plugins.json"
	// SessionsDirName holds the 1:1 conversations of one agent.
	SessionsDirName = "sessions"
	// MemoriesDirName holds the weekly memory files of one agent.
	MemoriesDirName = "memories"
	// MaxWorkspaceFileBytes caps one text file the workspace loads.
	MaxWorkspaceFileBytes = 1 << 20
)

// Default workspace files. They are written only when a file is missing, so a
// user edit is never overwritten.
const (
	DefaultAgentsMD = `# AGENTS.md

本文件是 Agent 工作区的入口，系统提示按以下顺序装配：

1. PERSONA.md
2. USER.md
3. 记忆索引与相关周记忆
4. 已启用插件的技能文件

## 行为准则

- 回答简洁、准确，先给结论，再补充必要细节。
- 不虚构工具、文件访问或联网能力；无法完成时说明限制并给出替代建议。
`

	DefaultPersonaMD = `# PERSONA.md

你是 Bowknot 工作台上的中文 AI 助手，友善、务实，优先给出可执行的建议。
`

	DefaultUserMD = `# USER.md

（暂无用户信息。此处只记录该 Agent 所知道的用户信息。）
`

	DefaultPluginsJSON = `{"schema":1,"plugins":[]}
`
)

// ErrWorkspaceCorrupt reports a workspace text file that could not be used.
// The original file is kept; callers should warn instead of rewriting it.
var ErrWorkspaceCorrupt = errors.New("agent: 工作区文件损坏")

// WorkspaceContent is the loaded text of one agent workspace.
type WorkspaceContent struct {
	Agents  string
	Persona string
	User    string
}

// Prompt assembles the fixed order AGENTS.md → PERSONA.md → USER.md. Memory
// and plugin skill sections are appended by later plans.
func (c WorkspaceContent) Prompt() string {
	sections := []string{c.Agents, c.Persona, c.User}
	parts := make([]string, 0, len(sections))
	for _, section := range sections {
		if text := strings.TrimSpace(section); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// Workspace is one agent directory under the agents root.
type Workspace struct {
	dir string
}

// NewWorkspace returns the workspace of id under root. It does not touch the
// disk and does not validate id.
func NewWorkspace(root, id string) *Workspace {
	return &Workspace{dir: filepath.Join(root, id)}
}

// Dir returns the workspace directory.
func (w *Workspace) Dir() string { return w.dir }

// SessionsDir returns the directory of the agent conversations.
func (w *Workspace) SessionsDir() string { return filepath.Join(w.dir, SessionsDirName) }

// MemoriesDir returns the directory of the agent memory files.
func (w *Workspace) MemoriesDir() string { return filepath.Join(w.dir, MemoriesDirName) }

// Path returns the path of one workspace file.
func (w *Workspace) Path(name string) string { return filepath.Join(w.dir, name) }

// Ensure creates the workspace directories and writes every missing default
// file. Existing files are left untouched, including corrupt ones.
func (w *Workspace) Ensure() error {
	if w.dir == "" {
		return nil
	}
	for _, dir := range []string{w.dir, w.SessionsDir(), w.MemoriesDir()} {
		if err := os.MkdirAll(dir, storeDirMode); err != nil {
			return fmt.Errorf("agent: create workspace directory: %w", err)
		}
	}
	defaults := []struct {
		name    string
		content string
	}{
		{AgentsFileName, DefaultAgentsMD},
		{PersonaFileName, DefaultPersonaMD},
		{UserFileName, DefaultUserMD},
		{PluginsFileName, DefaultPluginsJSON},
	}
	for _, file := range defaults {
		path := w.Path(file.name)
		switch _, err := os.Stat(path); {
		case err == nil:
			continue
		case errors.Is(err, os.ErrNotExist):
			if err := writeFileAtomic(path, []byte(file.content), storeFileMode); err != nil {
				return fmt.Errorf("agent: write default %s: %w", file.name, err)
			}
		default:
			return fmt.Errorf("agent: stat workspace file: %w", err)
		}
	}
	return nil
}

// Load reads AGENTS.md, PERSONA.md and USER.md. A missing file falls back to
// its default text without writing; an unreadable, oversized or non UTF-8
// file is reported through ErrWorkspaceCorrupt and left on disk.
func (w *Workspace) Load() (WorkspaceContent, error) {
	if w.dir == "" {
		return WorkspaceContent{}, nil
	}
	var corrupt []string
	read := func(name, fallback string) string {
		path := w.Path(name)
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			return fallback
		}
		if err != nil {
			log.Printf("agent: read workspace %s: %v", name, err)
			corrupt = append(corrupt, name)
			return ""
		}
		if info.Size() > MaxWorkspaceFileBytes {
			log.Printf("agent: workspace %s exceeds %d bytes", name, MaxWorkspaceFileBytes)
			corrupt = append(corrupt, name)
			return ""
		}
		data, err := os.ReadFile(path)
		if err != nil {
			log.Printf("agent: read workspace %s: %v", name, err)
			corrupt = append(corrupt, name)
			return ""
		}
		if !utf8.Valid(data) {
			log.Printf("agent: workspace %s is not valid UTF-8", name)
			corrupt = append(corrupt, name)
			return ""
		}
		return string(data)
	}
	content := WorkspaceContent{
		Agents:  read(AgentsFileName, DefaultAgentsMD),
		Persona: read(PersonaFileName, DefaultPersonaMD),
		User:    read(UserFileName, DefaultUserMD),
	}
	if len(corrupt) > 0 {
		return content, fmt.Errorf("%w: %s", ErrWorkspaceCorrupt, strings.Join(corrupt, ", "))
	}
	return content, nil
}

// Save replaces one workspace text file atomically. Only the workspace text
// files and plugins.json are accepted.
func (w *Workspace) Save(name, content string) error {
	if w.dir == "" {
		return nil
	}
	if !workspaceTextFile(name) {
		return fmt.Errorf("agent: %q is not a workspace text file", name)
	}
	if !utf8.ValidString(content) {
		return errors.New("工作区文件必须是 UTF-8 文本")
	}
	if len(content) > MaxWorkspaceFileBytes {
		return fmt.Errorf("工作区文件超过 %d 字节", MaxWorkspaceFileBytes)
	}
	if err := os.MkdirAll(w.dir, storeDirMode); err != nil {
		return fmt.Errorf("agent: create workspace directory: %w", err)
	}
	if err := writeFileAtomic(w.Path(name), []byte(content), storeFileMode); err != nil {
		return fmt.Errorf("agent: save workspace %s: %w", name, err)
	}
	return nil
}

func workspaceTextFile(name string) bool {
	switch name {
	case AgentsFileName, PersonaFileName, UserFileName, PluginsFileName:
		return true
	default:
		return false
	}
}

// writeFileAtomic writes data to a temporary file in the target directory and
// replaces the target with it.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "tmp-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	temp := f.Name()
	defer func() {
		if temp != "" {
			_ = os.Remove(temp)
		}
	}()
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		return fmt.Errorf("set file permissions: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("write file: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("sync file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close file: %w", err)
	}
	if err := os.Rename(temp, path); err != nil {
		return fmt.Errorf("replace file: %w", err)
	}
	temp = ""
	return nil
}
