package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Tool limits and output bounds.
const (
	// MaxFSToolWrite caps one fs.write payload, in bytes.
	MaxFSToolWrite = 256 << 10
	// MaxFSToolRead caps one fs.read source file, in bytes.
	MaxFSToolRead = 1 << 20
	// fsToolOutputRunes bounds the text one fs.read returns to the model.
	fsToolOutputRunes = 8000
	// maxMemoryToolHits bounds one memory.search result set.
	maxMemoryToolHits = 10
)

// ToolHost executes the built-in fs and memory tools for one agent. It never
// grants raw file system access: fs paths must stay inside the agent
// workspace, fs.write asks the approval callback first (FR-AGT-06), and
// memory.search only reads through the injected searcher. The group and
// delegate tools are executed by the group collaboration plans, not here.
type ToolHost struct {
	// Root is the agent workspace directory; an empty root disables the fs
	// tools.
	Root string
	// Memory backs memory.search; nil disables the tool.
	Memory MemorySearcher
	// Approve grants one high-risk write. It receives the tool name and a
	// short detail; nil denies every write.
	Approve func(tool, detail string) bool
	// Audit records a tool call summary; nil skips recording.
	Audit func(tool, summary string)
}

// MemorySearcher backs the memory.search tool with one agent's own memory.
type MemorySearcher interface {
	SearchMemory(ctx context.Context, query string, limit int) ([]MemoryHit, error)
}

// MemoryHit is one retrieved memory snippet.
type MemoryHit struct {
	File    string
	Snippet string
}

// Tool errors.
var (
	ErrToolUnknown    = errors.New("未知工具")
	ErrToolDisabled   = errors.New("工具不可用")
	ErrToolUnapproved = errors.New("写入未获批准")
)

// Call runs one built-in tool with its JSON arguments and returns the text
// the model receives. argsJSON is empty for tools without arguments.
func (h *ToolHost) Call(ctx context.Context, name, argsJSON string) (string, error) {
	out, err := h.call(ctx, name, argsJSON)
	if h.Audit != nil {
		h.Audit(name, toolSummary(name, argsJSON, err))
	}
	return out, err
}

func (h *ToolHost) call(ctx context.Context, name, argsJSON string) (string, error) {
	switch name {
	case ToolFSRead:
		return h.fsRead(argsJSON)
	case ToolFSWrite:
		return h.fsWrite(argsJSON)
	case ToolMemorySearch:
		return h.memorySearch(ctx, argsJSON)
	case ToolGroupPost, ToolGroupRequest, ToolDelegate:
		return "", fmt.Errorf("%w：%s 属于群聊协作能力，当前不可调用", ErrToolDisabled, name)
	case "":
		return "", fmt.Errorf("%w：工具名为空", ErrToolUnknown)
	default:
		return "", fmt.Errorf("%w：%s", ErrToolUnknown, name)
	}
}

type fsReadArgs struct {
	Path string `json:"path"`
}

func (h *ToolHost) fsRead(argsJSON string) (string, error) {
	if h.Root == "" {
		return "", fmt.Errorf("%w：fs.read 需要工作区", ErrToolDisabled)
	}
	var args fsReadArgs
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	path, err := h.resolve(args.Path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("文件不存在：%s", args.Path)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("不是普通文件：%s", args.Path)
	}
	if info.Size() > MaxFSToolRead {
		return "", fmt.Errorf("文件超过 %d KiB：%s", MaxFSToolRead>>10, args.Path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("读取文件失败：%w", err)
	}
	if !utf8.Valid(data) {
		return "", fmt.Errorf("不是 UTF-8 文本文件：%s", args.Path)
	}
	text := string(data)
	if runes := utf8.RuneCountInString(text); runes > fsToolOutputRunes {
		return truncateRunes(text, fsToolOutputRunes) + "\n（内容过长，已截断）", nil
	}
	return text, nil
}

type fsWriteArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (h *ToolHost) fsWrite(argsJSON string) (string, error) {
	if h.Root == "" {
		return "", fmt.Errorf("%w：fs.write 需要工作区", ErrToolDisabled)
	}
	var args fsWriteArgs
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	path, err := h.resolve(args.Path)
	if err != nil {
		return "", err
	}
	if !utf8.ValidString(args.Content) {
		return "", errors.New("内容必须是 UTF-8 文本")
	}
	if len(args.Content) > MaxFSToolWrite {
		return "", fmt.Errorf("内容超过 %d KiB", MaxFSToolWrite>>10)
	}
	detail := fmt.Sprintf("写入 %s（%d 字节）", args.Path, len(args.Content))
	if h.Approve == nil || !h.Approve(ToolFSWrite, detail) {
		return "", fmt.Errorf("%w：%s", ErrToolUnapproved, detail)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("创建目录失败：%w", err)
	}
	if err := writeFileAtomic(path, []byte(args.Content), 0o600); err != nil {
		return "", fmt.Errorf("写入文件失败：%w", err)
	}
	return "已写入 " + args.Path, nil
}

type memorySearchArgs struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

func (h *ToolHost) memorySearch(ctx context.Context, argsJSON string) (string, error) {
	if h.Memory == nil {
		return "", fmt.Errorf("%w：memory.search 需要记忆后端", ErrToolDisabled)
	}
	var args memorySearchArgs
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	limit := args.Limit
	if limit <= 0 {
		limit = 5
	}
	if limit > maxMemoryToolHits {
		limit = maxMemoryToolHits
	}
	hits, err := h.Memory.SearchMemory(ctx, args.Query, limit)
	if err != nil {
		return "", fmt.Errorf("检索记忆失败：%w", err)
	}
	if len(hits) == 0 {
		return "没有找到相关记忆。", nil
	}
	var b strings.Builder
	b.WriteString("相关记忆片段：\n")
	for _, hit := range hits {
		b.WriteString("- 来源 " + hit.File + "：" + hit.Snippet + "\n")
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// resolve confines one tool path to the workspace root.
func (h *ToolHost) resolve(p string) (string, error) {
	cleaned, err := CleanRelativePath(strings.TrimSpace(p))
	if err != nil {
		return "", fmt.Errorf("路径无效：%s", err.Error())
	}
	return resolveInRoot(h.Root, cleaned)
}

func decodeArgs(argsJSON string, args any) error {
	trimmed := strings.TrimSpace(argsJSON)
	if trimmed == "" {
		trimmed = "{}"
	}
	if err := json.Unmarshal([]byte(trimmed), args); err != nil {
		return fmt.Errorf("参数无效：%w", err)
	}
	return nil
}

// toolSummary builds the audit line of one call: the arguments shortened, plus
// the failure reason when the call failed.
func toolSummary(name, argsJSON string, err error) string {
	summary := strings.TrimSpace(argsJSON)
	if summary == "" {
		summary = "{}"
	}
	if len(summary) > 120 {
		summary = truncateRunes(summary, 120) + "…"
	}
	summary = strings.Join(strings.Fields(summary), " ")
	if err != nil {
		return summary + " → 失败：" + err.Error()
	}
	return summary
}
