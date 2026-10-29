package plugin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeMemory is a stand-in MemorySearcher.
type fakeMemory struct {
	hits []MemoryHit
	err  error
}

func (f *fakeMemory) SearchMemory(_ context.Context, _ string, _ int) ([]MemoryHit, error) {
	return f.hits, f.err
}

func newToolHost(t *testing.T) (*ToolHost, string) {
	t.Helper()
	root := t.TempDir()
	return &ToolHost{Root: root, Memory: &fakeMemory{}}, root
}

func TestToolFSRead(t *testing.T) {
	h, root := newToolHost(t)
	if err := os.WriteFile(filepath.Join(root, "notes.md"), []byte("# 笔记\n内容"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := h.Call(context.Background(), ToolFSRead, `{"path":"notes.md"}`)
	if err != nil {
		t.Fatalf("Call() error: %v", err)
	}
	if !strings.Contains(out, "# 笔记") {
		t.Errorf("output = %q", out)
	}

	if _, err := h.Call(context.Background(), ToolFSRead, `{"path":"missing.md"}`); err == nil {
		t.Error("Call() expected an error for a missing file")
	}
	// The path must stay inside the workspace.
	if _, err := h.Call(context.Background(), ToolFSRead, `{"path":"../outside.md"}`); err == nil {
		t.Error("Call() expected an error for an escaping path")
	}
	if _, err := h.Call(context.Background(), ToolFSRead, `{"path":"/etc/hosts"}`); err == nil {
		t.Error("Call() expected an error for an absolute path")
	}
}

func TestToolFSReadTruncates(t *testing.T) {
	h, _ := newToolHost(t)
	h.Root = t.TempDir()
	big := strings.Repeat("长", fsToolOutputRunes+100)
	if err := os.WriteFile(filepath.Join(h.Root, "big.md"), []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := h.Call(context.Background(), ToolFSRead, `{"path":"big.md"}`)
	if err != nil {
		t.Fatalf("Call() error: %v", err)
	}
	if !strings.HasSuffix(out, "已截断）") {
		t.Errorf("output should be truncated, got %d runes", len([]rune(out)))
	}
}

func TestToolFSWriteApproval(t *testing.T) {
	h, root := newToolHost(t)
	var approved []string
	h.Approve = func(tool, detail string) bool {
		approved = append(approved, tool+"|"+detail)
		return true
	}
	out, err := h.Call(context.Background(), ToolFSWrite, `{"path":"sub/out.md","content":"# 产出"}`)
	if err != nil {
		t.Fatalf("Call() error: %v", err)
	}
	if out != "已写入 sub/out.md" {
		t.Errorf("output = %q", out)
	}
	if len(approved) != 1 || !strings.HasPrefix(approved[0], ToolFSWrite+"|") {
		t.Errorf("approve calls = %v", approved)
	}
	data, err := os.ReadFile(filepath.Join(root, "sub", "out.md"))
	if err != nil || string(data) != "# 产出" {
		t.Fatalf("written file = %q, err %v", data, err)
	}

	// A denied approval blocks the write.
	h.Approve = func(string, string) bool { return false }
	if _, err := h.Call(context.Background(), ToolFSWrite, `{"path":"denied.md","content":"x"}`); !errors.Is(err, ErrToolUnapproved) {
		t.Errorf("error = %v, want ErrToolUnapproved", err)
	}
	if _, err := os.Stat(filepath.Join(root, "denied.md")); !os.IsNotExist(err) {
		t.Errorf("denied write created a file, err %v", err)
	}

	// No approval callback denies every write.
	h.Approve = nil
	if _, err := h.Call(context.Background(), ToolFSWrite, `{"path":"a.md","content":"x"}`); !errors.Is(err, ErrToolUnapproved) {
		t.Errorf("error = %v, want ErrToolUnapproved", err)
	}
}

func TestToolFSWriteLimits(t *testing.T) {
	h, _ := newToolHost(t)
	h.Approve = func(string, string) bool { return true }
	if _, err := h.Call(context.Background(), ToolFSWrite, `{"path":"big.md","content":"`+strings.Repeat("a", MaxFSToolWrite+1)+`"}`); err == nil {
		t.Error("Call() expected a size limit error")
	}
	// Non-UTF-8 content is rejected before the approval is asked.
	h.Approve = nil
	if _, err := h.Call(context.Background(), ToolFSWrite, `{"path":"a.md","content":"\xff"}`); err == nil || errors.Is(err, ErrToolUnapproved) {
		t.Errorf("error = %v, want an encoding error", err)
	}
}

func TestToolMemorySearch(t *testing.T) {
	h, _ := newToolHost(t)
	h.Memory = &fakeMemory{hits: []MemoryHit{{File: "2026-10-27_2026-11-02.md", Snippet: "用户偏好深色主题"}}}
	out, err := h.Call(context.Background(), ToolMemorySearch, `{"query":"主题"}`)
	if err != nil {
		t.Fatalf("Call() error: %v", err)
	}
	if !strings.Contains(out, "2026-10-27_2026-11-02.md") || !strings.Contains(out, "深色主题") {
		t.Errorf("output = %q", out)
	}
	h.Memory = &fakeMemory{}
	out, err = h.Call(context.Background(), ToolMemorySearch, `{"query":"无结果"}`)
	if err != nil || out != "没有找到相关记忆。" {
		t.Errorf("empty result output = %q, err %v", out, err)
	}
	h.Memory = &fakeMemory{err: errors.New("网关离线")}
	if _, err := h.Call(context.Background(), ToolMemorySearch, `{"query":"x"}`); err == nil {
		t.Error("Call() expected the backend error to surface")
	}
	h.Memory = nil
	if _, err := h.Call(context.Background(), ToolMemorySearch, `{}`); !errors.Is(err, ErrToolDisabled) {
		t.Errorf("error = %v, want ErrToolDisabled", err)
	}
}

func TestToolHostGroupAndDelegateDisabled(t *testing.T) {
	h, _ := newToolHost(t)
	for _, name := range []string{ToolGroupPost, ToolGroupRequest, ToolDelegate} {
		if _, err := h.Call(context.Background(), name, "{}"); !errors.Is(err, ErrToolDisabled) {
			t.Errorf("%s error = %v, want ErrToolDisabled", name, err)
		}
	}
	if _, err := h.Call(context.Background(), "shell.exec", "{}"); !errors.Is(err, ErrToolUnknown) {
		t.Errorf("error = %v, want ErrToolUnknown", err)
	}
	if _, err := h.Call(context.Background(), "", "{}"); err == nil {
		t.Error("Call() expected an error for an empty tool name")
	}
}

func TestToolHostWithoutWorkspace(t *testing.T) {
	h := &ToolHost{}
	if _, err := h.Call(context.Background(), ToolFSRead, `{"path":"a.md"}`); !errors.Is(err, ErrToolDisabled) {
		t.Errorf("error = %v, want ErrToolDisabled", err)
	}
}

func TestToolHostAudit(t *testing.T) {
	h, root := newToolHost(t)
	var calls []string
	h.Audit = func(tool, summary string) { calls = append(calls, tool+" "+summary) }
	h.Approve = func(string, string) bool { return true }
	if _, err := h.Call(context.Background(), ToolFSRead, `{"path":"a.md"}`); err == nil {
		t.Error("Call() expected an error for a missing file")
	}
	if _, err := h.Call(context.Background(), ToolFSWrite, `{"path":"a.md","content":"x"}`); err != nil {
		t.Fatal(err)
	}
	_ = root
	if len(calls) != 2 {
		t.Fatalf("audit calls = %v", calls)
	}
	if !strings.Contains(calls[0], "失败") {
		t.Errorf("failed call audit = %q", calls[0])
	}
	if !strings.Contains(calls[1], "a.md") {
		t.Errorf("successful call audit = %q", calls[1])
	}
}

func TestDecodeArgsInvalid(t *testing.T) {
	h, _ := newToolHost(t)
	if _, err := h.Call(context.Background(), ToolFSRead, `{bad`); err == nil || !strings.Contains(err.Error(), "参数无效") {
		t.Errorf("error = %v, want invalid args", err)
	}
	// Empty arguments are treated as an empty object.
	if _, err := h.Call(context.Background(), ToolFSRead, ""); err == nil {
		t.Error("Call() expected a missing path error, not an args error")
	}
}
