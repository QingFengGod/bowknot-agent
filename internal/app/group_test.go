package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QingFengGod/bowknot-agent/internal/agent"
	"github.com/QingFengGod/bowknot-agent/internal/gateway"
	"github.com/QingFengGod/bowknot-agent/internal/plugin"
)

// writePluginRegistry stores one installed plugin declaring the given tools.
func writePluginRegistry(t *testing.T, dir string, tools []string) {
	t.Helper()
	body := `{"schema":1,"plugins":[{"id":"demo","name":"演示","version":"1.0.0","skills":["demo"],"tools":["` +
		strings.Join(tools, `","`) + `"]}]}`
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, plugin.RegistryFileName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestGroupToolsOffersEnabledPluginTools(t *testing.T) {
	pluginsDir := t.TempDir()
	writePluginRegistry(t, pluginsDir, []string{plugin.ToolFSRead, plugin.ToolFSWrite})
	plugins := plugin.NewStore(pluginsDir, ShellVersion)
	if err := plugins.Load(); err != nil {
		t.Fatal(err)
	}

	agents := agent.NewRegistry(t.TempDir())
	a, err := agents.Create(agent.Agent{Name: "子 Agent", Enabled: true, ProviderID: "p-00000001", Model: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := agents.SetEnabledPlugins(a.ID, []string{"demo"}); err != nil {
		t.Fatal(err)
	}

	tools := groupTools{agents: agents, plugins: plugins}
	specs := tools.Tools(a.ID)
	if len(specs) != 2 {
		t.Fatalf("工具数 = %d, want 2: %+v", len(specs), specs)
	}
	for _, spec := range specs {
		if spec.Name != plugin.ToolFSRead && spec.Name != plugin.ToolFSWrite {
			t.Errorf("未启用的工具 %q 被暴露", spec.Name)
		}
	}
	if specs := tools.Tools(agent.MainAgentID); len(specs) != 0 {
		t.Errorf("主 Agent 未启用插件却拿到工具: %+v", specs)
	}

	ws := agents.Workspace(a.ID)
	if err := os.MkdirAll(ws.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws.Dir(), "note.txt"), []byte("你好"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := tools.Call(context.Background(), a.ID, plugin.ToolFSRead, `{"path":"note.txt"}`)
	if err != nil || out != "你好" {
		t.Fatalf("fs.read = %q, %v", out, err)
	}
	if _, err := tools.Call(context.Background(), a.ID, plugin.ToolFSWrite, `{"path":"out.txt","content":"x"}`); !errors.Is(err, plugin.ErrToolUnapproved) {
		t.Fatalf("fs.write error = %v, want ErrToolUnapproved", err)
	}
	if _, err := tools.Call(context.Background(), agent.MainAgentID, plugin.ToolFSRead, `{"path":"note.txt"}`); !errors.Is(err, plugin.ErrToolDisabled) {
		t.Fatalf("未启用插件的工具调用 error = %v, want ErrToolDisabled", err)
	}
}

func TestAppendRunSummaryWritesAssistantMessage(t *testing.T) {
	page := agent.NewPage(stubChatter{}, agent.NewStore(""), nil, nil)
	conv := page.Store.Create()
	a := &App{assistantPage: page}
	a.appendRunSummary(conv.ID, "Run 汇总")

	got, ok := page.Store.Get(conv.ID)
	if !ok || len(got.Messages) != 1 {
		t.Fatalf("会话消息 = %+v", got.Messages)
	}
	last := got.Messages[len(got.Messages)-1]
	if last.Role != agent.RoleAssistant || last.Content != "Run 汇总" || last.Status != agent.StatusOK {
		t.Fatalf("汇总消息 = %+v", last)
	}

	// A group run has no assistant conversation and must not be written there.
	a.appendRunSummary("c-00000000", "群聊汇总")
	if got, _ := page.Store.Get("c-00000000"); len(got.Messages) != 0 {
		t.Errorf("未知会话被写入: %+v", got.Messages)
	}
}

func TestGroupChatRequiresGateway(t *testing.T) {
	a := &App{}
	if _, err := a.groupChat(context.Background(), gateway.ChatRequest{}); err == nil {
		t.Fatal("网关缺失时应返回错误")
	}
}
