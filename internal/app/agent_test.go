package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/QingFengGod/bowknot-agent/internal/agent"
	"github.com/QingFengGod/bowknot-agent/internal/gateway"
)

type stubChatter struct{ snap gateway.Snapshot }

func (s stubChatter) Chat(context.Context, gateway.ChatRequest, string) (gateway.ChatResponse, error) {
	return gateway.ChatResponse{}, nil
}

func (s stubChatter) Snapshot() gateway.Snapshot { return s.snap }

func (s stubChatter) ChatTargets() []gateway.TargetView { return nil }

func TestConversationsDir(t *testing.T) {
	if got := conversationsDir(""); got != "" {
		t.Errorf("conversationsDir(\"\") = %q, want empty", got)
	}
	want := filepath.Join("home", ".bowknot-agent", "agent", "conversations")
	if got := conversationsDir(filepath.Join("home", ".bowknot-agent", "agent")); got != want {
		t.Errorf("conversationsDir() = %q, want %q", got, want)
	}
}

func TestNewAgentPageSelectsRecentConversation(t *testing.T) {
	dir := t.TempDir()
	older := agent.Conversation{
		Schema: agent.SchemaVersion, ID: "c-11111111", Title: "旧会话",
		CreatedAt: 10, UpdatedAt: 10,
		Messages: []agent.Message{{Role: agent.RoleUser, Content: "旧", Status: agent.StatusOK, At: 10}},
	}
	newer := older
	newer.ID = "c-22222222"
	newer.Title = "新会话"
	newer.UpdatedAt = 20
	newer.Messages[0].At = 20
	for _, conv := range []agent.Conversation{older, newer} {
		data, err := json.Marshal(conv)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, conv.ID+".json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	page := newAgentPage(stubChatter{snap: gateway.Snapshot{State: gateway.StateOnline, Model: "m1"}}, dir, "", "")
	if page.CurrentID != newer.ID {
		t.Errorf("current = %q, want the most recent conversation %q", page.CurrentID, newer.ID)
	}
	conv, ok := page.Store.Get(page.CurrentID)
	if !ok || conv.Title != "新会话" {
		t.Errorf("selected conversation = %+v, ok = %v", conv, ok)
	}
}

func TestNewAgentPageWithoutConversations(t *testing.T) {
	page := newAgentPage(stubChatter{}, t.TempDir(), "", "")
	if page.CurrentID != "" {
		t.Errorf("current = %q, want empty", page.CurrentID)
	}
	if got := page.Blocker(); got != "网关未配置，配置后可启用对话" {
		t.Errorf("blocker = %q", got)
	}
}
