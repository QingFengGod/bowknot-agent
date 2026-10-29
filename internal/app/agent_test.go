package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/QingFengGod/bowknot-agent/internal/agent"
	"github.com/QingFengGod/bowknot-agent/internal/gateway"
	"github.com/QingFengGod/bowknot-agent/internal/shell"
	"github.com/QingFengGod/bowknot-agent/internal/ui"
)

type stubChatter struct{ snap gateway.Snapshot }

func (s stubChatter) Chat(context.Context, gateway.ChatRequest, string) (gateway.ChatResponse, error) {
	return gateway.ChatResponse{}, nil
}

func (s stubChatter) Embed(context.Context, gateway.EmbedRequest, string) (gateway.EmbedResponse, error) {
	return gateway.EmbedResponse{}, nil
}

func (s stubChatter) Snapshot() gateway.Snapshot { return s.snap }

func (s stubChatter) ChatTargets() []gateway.TargetView { return nil }

func TestNewAgentPagesSelectsRecentConversation(t *testing.T) {
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
	sessions := filepath.Join(dir, agent.MainAgentID, agent.SessionsDirName)
	if err := os.MkdirAll(sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, conv := range []agent.Conversation{older, newer} {
		data, err := json.Marshal(conv)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sessions, conv.ID+".json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	page, agentPage := newAgentPages(stubChatter{snap: gateway.Snapshot{State: gateway.StateOnline, Model: "m1"}}, dir, "")
	if page.CurrentID != newer.ID {
		t.Errorf("current = %q, want the most recent conversation %q", page.CurrentID, newer.ID)
	}
	conv, ok := page.Store.Get(page.CurrentID)
	if !ok || conv.Title != "新会话" {
		t.Errorf("selected conversation = %+v, ok = %v", conv, ok)
	}
	if agentPage.CurrentAgent != "" {
		t.Errorf("agent page current agent = %q, want the main agent", agentPage.CurrentAgent)
	}
	if got := agentPage.Agents.Main().Name; got != agent.DefaultMainAgentName {
		t.Errorf("main agent name = %q", got)
	}
}

func TestNewAgentPagesWithoutData(t *testing.T) {
	page, agentPage := newAgentPages(stubChatter{}, "", "")
	if page.CurrentID != "" {
		t.Errorf("current = %q, want empty", page.CurrentID)
	}
	if got := page.Blocker(); got != "网关未配置，配置后可启用对话" {
		t.Errorf("blocker = %q", got)
	}
	if agentPage.Store != page.Store || agentPage.Agents != page.Agents {
		t.Error("assistant and agent pages do not share the stores")
	}
}

func TestOpenConversationSwitchesModule(t *testing.T) {
	dir := t.TempDir()
	page, agentPage := newAgentPages(stubChatter{snap: gateway.Snapshot{State: gateway.StateOnline, Model: "m1"}}, dir, "")
	a, err := agentPage.Agents.Create(agent.Agent{Name: "短剧", Enabled: true,
		ProviderID: "p-00000001", Model: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	subConv := page.Store.CreateFor(a.ID)
	if _, err := page.Store.Append(subConv.ID, agent.Message{Role: agent.RoleUser,
		Content: "子会话", Status: agent.StatusOK, At: 1}); err != nil {
		t.Fatal(err)
	}
	mainConv := page.Store.Create()
	a2 := &App{
		assistantPage: page,
		agentPage:     agentPage,
		shell:         shell.New(ui.PreferenceSystem, false, shell.Actions{}),
	}
	a2.openConversation(subConv.ID)
	if agentPage.CurrentAgent != a.ID || agentPage.CurrentID != subConv.ID {
		t.Errorf("sub agent selection = %q/%q", agentPage.CurrentAgent, agentPage.CurrentID)
	}
	if a2.shell.Current != shell.ModuleAgent {
		t.Errorf("module = %q, want the agent module", a2.shell.Current)
	}
	a2.openConversation(mainConv.ID)
	if page.CurrentID != mainConv.ID {
		t.Errorf("main conversation = %q, want %q", page.CurrentID, mainConv.ID)
	}
	if a2.shell.Current != shell.ModuleAssistant {
		t.Errorf("module = %q, want the assistant module", a2.shell.Current)
	}
}
