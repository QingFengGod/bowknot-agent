package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mygoui "github.com/egoist/mygo/ui"

	"github.com/QingFengGod/bowknot-agent/internal/gateway"
	"github.com/QingFengGod/bowknot-agent/internal/task"
)

func newTestAgentPage(t *testing.T, f *fakeChatter) (*AgentPage, *Store, *Registry) {
	t.Helper()
	root := t.TempDir()
	store := NewStore(root)
	if err := store.Load(); err != nil {
		t.Fatal(err)
	}
	agents := NewRegistry(root)
	if err := agents.Load(); err != nil {
		t.Fatal(err)
	}
	assistant := NewPage(f, store, nil, agents)
	assistant.Dispatch = func(fn func()) { fn() }
	p := NewAgentPage(f, store, nil, agents, assistant.Runner)
	p.Dispatcher = assistant.Dispatcher
	p.Dispatch = func(fn func()) { fn() }
	p.Repaint = func() {}
	return p, store, agents
}

func newAgentPageTester(t *testing.T, p *AgentPage) *mygoui.Tester {
	t.Helper()
	return mygoui.NewTester(p.View(), 1200, 800)
}

func TestAgentPageListsSubAgentCards(t *testing.T) {
	p, _, agents := newTestAgentPage(t, &fakeChatter{snapshot: onlineSnapshot("m1"),
		targets: []gateway.TargetView{{ProviderID: "p-00000001", ProviderName: "A", Model: "m1"}}})
	a, err := agents.Create(Agent{Name: "资料整理", Enabled: true,
		ProviderID: "p-00000001", Model: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := agents.Workspace(a.ID).Save(PersonaFileName, "整理材料"); err != nil {
		t.Fatal(err)
	}
	tester := newAgentPageTester(t, p)
	tester.Frame()
	for _, text := range []string{"Agent", "资料整理", "+ 新建子 Agent", "整理材料", "A · m1"} {
		if !tester.HasText(text) {
			t.Errorf("agent list does not show %q", text)
		}
	}
	if tester.HasText("主 Agent") {
		t.Error("agent page still shows the main agent")
	}

	clickAndFrame(t, tester, "资料整理")
	if p.CurrentAgent != a.ID {
		t.Fatalf("current agent = %q, want the opened card %q", p.CurrentAgent, a.ID)
	}
	if !tester.HasText("← 返回") || !tester.HasText("输入消息") {
		t.Error("detail view does not reuse the chat layout")
	}
	clickAndFrame(t, tester, "← 返回")
	if p.CurrentAgent != "" {
		t.Errorf("current agent = %q, want the card list", p.CurrentAgent)
	}
	if !tester.HasText("资料整理") {
		t.Error("card list is missing after going back")
	}
}

func TestAgentPageEmptyList(t *testing.T) {
	p, _, _ := newTestAgentPage(t, &fakeChatter{snapshot: onlineSnapshot("m1"),
		targets: []gateway.TargetView{{ProviderID: "p-00000001", ProviderName: "A", Model: "m1"}}})
	tester := newAgentPageTester(t, p)
	tester.Frame()
	if !tester.HasText("还没有子 Agent") {
		t.Error("empty list state missing")
	}
	clickAndFrame(t, tester, "+ 新建子 Agent")
	if !p.FormOpen {
		t.Error("empty state create button does not open the form")
	}
}

func TestAgentPageFormSavesAndDeletes(t *testing.T) {
	p, store, agents := newTestAgentPage(t, &fakeChatter{snapshot: onlineSnapshot("m1"),
		targets: []gateway.TargetView{{ProviderID: "p-00000001", ProviderName: "A", Model: "m1"}}})
	first, err := agents.Create(Agent{Name: "资料整理", Enabled: true,
		ProviderID: "p-00000001", Model: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	tester := newAgentPageTester(t, p)
	clickAndFrame(t, tester, "+ 新建子 Agent")
	if !p.FormOpen || p.FormID != "" {
		t.Fatalf("form = %v/%q", p.FormOpen, p.FormID)
	}
	if !tester.HasText("新建子 Agent") {
		t.Error("form title missing")
	}
	if p.FormTarget != "A · m1" || !p.FormEnabled {
		t.Errorf("form defaults = target %q, enabled %v", p.FormTarget, p.FormEnabled)
	}

	p.FormName = "资料整理"
	p.FormPersona = "重复的名字"
	clickAndFrame(t, tester, "保存")
	if p.FormError != ErrAgentNameTaken.Error() {
		t.Errorf("form error = %q", p.FormError)
	}
	if len(p.subAgents()) != 1 {
		t.Errorf("sub agents = %d, want the existing one", len(p.subAgents()))
	}

	p.FormName = "写作"
	p.FormPersona = "负责写作"
	clickAndFrame(t, tester, "保存")
	if p.FormOpen || len(p.subAgents()) != 2 {
		t.Fatalf("form = %v, count = %d", p.FormOpen, len(p.subAgents()))
	}
	if p.CurrentAgent == "" || p.CurrentAgent == first.ID {
		t.Errorf("current agent = %q, want the created agent", p.CurrentAgent)
	}
	if !tester.HasText("← 返回") {
		t.Error("a new sub agent should open in the detail view")
	}
	writer, ok := agents.Get(p.CurrentAgent)
	if !ok {
		t.Fatal("created agent missing")
	}
	content, err := agents.Workspace(writer.ID).Load()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(content.Persona) != "负责写作" {
		t.Errorf("PERSONA.md = %q", content.Persona)
	}

	clickAndFrame(t, tester, "← 返回")
	clickAndFrame(t, tester, "子 Agent 菜单：写作")
	clickAndFrame(t, tester, "停用")
	if a, _ := agents.Get(writer.ID); a.Enabled {
		t.Error("sub agent still enabled after the toggle")
	}
	if !tester.HasText("已停用") {
		t.Error("disabled badge missing")
	}

	p.RequestDeleteAgent(writer.ID)
	tester.Frame()
	if !tester.HasText("删除子 Agent「写作」后不可恢复") {
		t.Fatal("delete confirmation missing")
	}
	clickAndFrame(t, tester, "确认删除")
	if len(p.subAgents()) != 1 {
		t.Errorf("sub agents = %d, want 1 after deleting", len(p.subAgents()))
	}
	if _, err := os.Stat(agents.Workspace(writer.ID).Dir()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("workspace still exists: %v", err)
	}
	if len(store.ListByAgent(writer.ID)) != 0 {
		t.Error("sub agent conversations kept after deleting the agent")
	}
}

func TestAgentPageCreateCopiesMainTemplate(t *testing.T) {
	p, _, agents := newTestAgentPage(t, &fakeChatter{snapshot: onlineSnapshot("m1"),
		targets: []gateway.TargetView{{ProviderID: "p-00000001", ProviderName: "A", Model: "m1"}}})
	main := agents.Workspace(MainAgentID)
	if err := main.Save(PersonaFileName, "主 Agent 人设"); err != nil {
		t.Fatal(err)
	}
	if err := main.Save(UserFileName, "用户资料"); err != nil {
		t.Fatal(err)
	}
	p.OpenAgentForm("")
	p.FormName = "复制者"
	p.FormCopyMain = true
	p.SaveAgentForm()
	if !strings.HasPrefix(p.CurrentAgent, "a-") {
		t.Fatalf("current agent = %q, want the created agent", p.CurrentAgent)
	}
	content, err := agents.Workspace(p.CurrentAgent).Load()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(content.Persona) != "主 Agent 人设" {
		t.Errorf("copied persona = %q", content.Persona)
	}
	if strings.TrimSpace(content.User) != "用户资料" {
		t.Errorf("copied user = %q", content.User)
	}
}

func TestAgentPageEditPersona(t *testing.T) {
	p, _, agents := newTestAgentPage(t, &fakeChatter{snapshot: onlineSnapshot("m1"),
		targets: []gateway.TargetView{{ProviderID: "p-00000001", ProviderName: "A", Model: "m1"}}})
	a, err := agents.Create(Agent{Name: "短剧", Enabled: true,
		ProviderID: "p-00000001", Model: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	p.OpenAgentForm(a.ID)
	if !p.FormOpen || p.FormID != a.ID || p.FormName != "短剧" {
		t.Fatalf("form = %v/%q/%q", p.FormOpen, p.FormID, p.FormName)
	}
	p.FormPersona = "负责短剧"
	p.SaveAgentForm()
	content, err := agents.Workspace(a.ID).Load()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(content.Persona) != "负责短剧" {
		t.Errorf("PERSONA.md = %q", content.Persona)
	}
}

func TestAgentPageMainAgentFormRejected(t *testing.T) {
	p, _, _ := newTestAgentPage(t, &fakeChatter{snapshot: onlineSnapshot("m1"),
		targets: []gateway.TargetView{{ProviderID: "p-00000001", ProviderName: "A", Model: "m1"}}})
	p.OpenAgentForm(MainAgentID)
	if p.FormOpen {
		t.Error("the main agent must not open in the agent page form")
	}
}

func TestAgentPageDirectChat(t *testing.T) {
	f := &fakeChatter{
		snapshot: onlineSnapshot("m1"),
		targets:  []gateway.TargetView{{ProviderID: "p-00000001", ProviderName: "A", Model: "m1"}},
	}
	var got gateway.ChatRequest
	f.handler = func(ctx context.Context, req gateway.ChatRequest) (gateway.ChatResponse, error) {
		got = req
		return gateway.ChatResponse{Content: "收到", Model: req.Model, HasUsage: true}, nil
	}
	p, store, agents := newTestAgentPage(t, f)
	a, err := agents.Create(Agent{Name: "短剧", Enabled: true,
		ProviderID: "p-00000001", Model: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := agents.Workspace(a.ID).Save(PersonaFileName, "你是短剧剧本专家"); err != nil {
		t.Fatal(err)
	}
	main := store.Create()
	if _, err := store.Append(main.ID, Message{Role: RoleUser, Content: "主导会话",
		Status: StatusOK, At: 1}); err != nil {
		t.Fatal(err)
	}
	tester := newAgentPageTester(t, p)
	tester.Frame()
	if tester.HasText("主导会话") {
		t.Fatal("the card list leaks main agent conversations")
	}

	clickAndFrame(t, tester, "短剧")
	if p.CurrentAgent != a.ID {
		t.Fatalf("current agent = %q, want %q", p.CurrentAgent, a.ID)
	}
	if p.CurrentID != "" {
		t.Errorf("current conversation = %q, want none for a new agent", p.CurrentID)
	}
	if tester.HasText("主导会话") {
		t.Error("the conversation list still shows the main agent's conversation")
	}

	p.Input = "帮我写个短剧"
	p.Send()
	waitFor(t, func() bool { return !p.Runner.Busy() })

	if len(got.Messages) == 0 || !strings.Contains(got.Messages[0].Content, "你是短剧剧本专家") {
		t.Errorf("system message = %+v, want the sub agent persona", got.Messages)
	}
	if got.Model != "m1" {
		t.Errorf("model = %q, want the sub agent binding", got.Model)
	}
	list := store.ListByAgent(a.ID)
	if len(list) != 1 || list[0].AgentID != a.ID {
		t.Fatalf("sub agent conversations = %+v", list)
	}
	if len(store.List()) != 1 {
		t.Errorf("main conversations = %d, want the original one", len(store.List()))
	}
	conv, _ := store.Get(list[0].ID)
	if conv.Messages[1].Content != "收到" || conv.Messages[1].Status != StatusOK {
		t.Errorf("reply = %+v", conv.Messages)
	}
	tester.Frame()
	if !tester.HasText("收到") {
		t.Error("view does not show the sub agent reply")
	}

	if err := agents.SetEnabled(a.ID, false); err != nil {
		t.Fatal(err)
	}
	if got := p.Blocker(); got != "该子 Agent 已停用，启用后可对话" {
		t.Errorf("blocker = %q after disabling the sub agent", got)
	}
}

func TestAgentPageDeleteRequiresConfirmation(t *testing.T) {
	p, _, agents := newTestAgentPage(t, &fakeChatter{snapshot: onlineSnapshot("m1"),
		targets: []gateway.TargetView{{ProviderID: "p-00000001", ProviderName: "A", Model: "m1"}}})
	a, err := agents.Create(Agent{Name: "写作", Enabled: true,
		ProviderID: "p-00000001", Model: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	p.RequestDeleteAgent(a.ID)
	if p.ConfirmDeleteID != a.ID {
		t.Fatalf("confirmation = %q, want %q", p.ConfirmDeleteID, a.ID)
	}
	if len(p.subAgents()) != 1 {
		t.Error("agent deleted before confirmation")
	}
	p.ConfirmDeleteID = ""
	p.DeleteAgent()
	if len(p.subAgents()) != 1 {
		t.Error("cancel path deleted the agent")
	}
	if _, err := os.Stat(filepath.Join(agents.Workspace(a.ID).Dir(), AgentMetaFileName)); err != nil {
		t.Errorf("agent metadata missing after cancel: %v", err)
	}
}

func TestAgentPageDeleteTerminatesPendingTasks(t *testing.T) {
	root := t.TempDir()
	f := &fakeChatter{snapshot: onlineSnapshot("m1"),
		targets: []gateway.TargetView{{ProviderID: "p-00000001", ProviderName: "A", Model: "m1"}}}
	store := NewStore(root)
	if err := store.Load(); err != nil {
		t.Fatal(err)
	}
	agents := NewRegistry(root)
	if err := agents.Load(); err != nil {
		t.Fatal(err)
	}
	tasks := task.NewStore("")
	assistant := NewPage(f, store, tasks, agents)
	assistant.Dispatch = func(fn func()) { fn() }
	p := NewAgentPage(f, store, tasks, agents, assistant.Runner)
	p.Dispatcher = assistant.Dispatcher
	p.Dispatch = func(fn func()) { fn() }

	a, err := agents.Create(Agent{Name: "写作", Enabled: true,
		ProviderID: "p-00000001", Model: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	conv := store.Create()
	now := time.Now()
	g := task.NewGroup(now, conv.ID, 0, "p-00000001", "m1")
	g.Tasks = []task.Task{task.NewTask(now, "写一段文字", "", "完成", a.ID, a.Name)}
	if err := tasks.Save(g); err != nil {
		t.Fatal(err)
	}

	p.ConfirmDeleteID = a.ID
	p.DeleteAgent()

	got, ok := tasks.Get(g.ID)
	if !ok || got.Status != task.GroupCanceled {
		t.Errorf("group = %+v, ok = %v, want canceled", got, ok)
	}
	if len(p.subAgents()) != 0 {
		t.Errorf("sub agents = %d, want the agent deleted", len(p.subAgents()))
	}
}

func TestAgentPageBlockedByGateway(t *testing.T) {
	p, _, _ := newTestAgentPage(t, &fakeChatter{})
	if got := p.Blocker(); got != "网关未配置，配置后可启用对话" {
		t.Errorf("blocker = %q", got)
	}
}
