package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	mygoui "github.com/egoist/mygo/ui"

	"github.com/QingFengGod/bowknot-agent/internal/gateway"
)

func newPageTester(t *testing.T, p *Page) *mygoui.Tester {
	t.Helper()
	p.Dispatch = func(fn func()) { fn() }
	return mygoui.NewTester(p.View(), 1200, 800)
}

func newTestPage(t *testing.T, f *fakeChatter) (*Page, *Store) {
	t.Helper()
	store := NewStore(t.TempDir())
	if err := store.Load(); err != nil {
		t.Fatal(err)
	}
	return NewPage(f, store), store
}

func clickAndFrame(t *testing.T, tester *mygoui.Tester, label string) {
	t.Helper()
	if err := tester.Click(label); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
}

func TestPagePanelHeaderCreatesConversation(t *testing.T) {
	p, store := newTestPage(t, &fakeChatter{snapshot: onlineSnapshot("m1")})
	tester := newPageTester(t, p)
	if tester.HasText("Agent 菜单") {
		t.Error("the agent row still has a menu button")
	}
	clickAndFrame(t, tester, "新会话")
	if len(store.List()) != 1 || p.CurrentID == "" {
		t.Fatalf("conversations = %d, current = %q", len(store.List()), p.CurrentID)
	}
}

func TestPagePanelToggleRoundTrip(t *testing.T) {
	p, store := newTestPage(t, &fakeChatter{snapshot: onlineSnapshot("m1")})
	conv := store.Create()
	if _, err := store.Append(conv.ID, Message{Role: RoleUser, Content: "可见的会话",
		Status: StatusOK, At: 1}); err != nil {
		t.Fatal(err)
	}
	p.CurrentID = conv.ID
	tester := newPageTester(t, p)
	if !tester.HasText("可见的会话") {
		t.Fatal("conversation row missing")
	}
	clickAndFrame(t, tester, "收起会话列表")
	if !p.ConvPanelHidden {
		t.Error("conversation panel not hidden")
	}
	if tester.HasText("会话菜单：可见的会话") {
		t.Error("conversation row still shown after hiding the panel")
	}
	if !tester.HasText("打开会话列表") {
		t.Error("header does not offer the panel toggle")
	}
	clickAndFrame(t, tester, "打开会话列表")
	if p.ConvPanelHidden {
		t.Error("conversation panel not shown again")
	}
	if !tester.HasText("会话菜单：可见的会话") {
		t.Error("conversation row missing after showing the panel again")
	}
}

func TestPageConversationMenuRename(t *testing.T) {
	p, store := newTestPage(t, &fakeChatter{snapshot: onlineSnapshot("m1")})
	conv := store.Create()
	if _, err := store.Append(conv.ID, Message{Role: RoleUser, Content: "旧标题",
		Status: StatusOK, At: 1}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(conv.ID); err != nil {
		t.Fatal(err)
	}
	p.CurrentID = conv.ID
	tester := newPageTester(t, p)
	clickAndFrame(t, tester, "会话菜单：旧标题")
	for _, text := range []string{"重命名会话", "删除会话"} {
		if !tester.HasText(text) {
			t.Errorf("conversation menu does not show %q", text)
		}
	}
	clickAndFrame(t, tester, "重命名会话")
	if p.RenamingID != conv.ID || p.RenameDraft != "旧标题" {
		t.Fatalf("rename state = %q/%q", p.RenamingID, p.RenameDraft)
	}
	p.RenameDraft = "新标题"
	clickAndFrame(t, tester, "保存")
	if p.RenamingID != "" {
		t.Errorf("rename editor still open on %q", p.RenamingID)
	}
	got, _ := store.Get(conv.ID)
	if got.Title != "新标题" {
		t.Errorf("title = %q, want the renamed one", got.Title)
	}
	reloaded := NewStore(store.Dir())
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	if again, _ := reloaded.Get(conv.ID); again.Title != "新标题" {
		t.Errorf("reloaded title = %q", again.Title)
	}
	if !tester.HasText("新标题") {
		t.Error("view does not show the new title")
	}
}

func TestPageConversationMenuDelete(t *testing.T) {
	p, store := newTestPage(t, &fakeChatter{snapshot: onlineSnapshot("m1")})
	conv := store.Create()
	if _, err := store.Append(conv.ID, Message{Role: RoleUser, Content: "要删除的",
		Status: StatusOK, At: 1}); err != nil {
		t.Fatal(err)
	}
	p.CurrentID = conv.ID
	tester := newPageTester(t, p)
	clickAndFrame(t, tester, "会话菜单：要删除的")
	clickAndFrame(t, tester, "删除会话")
	if p.ConfirmDeleteID != conv.ID {
		t.Fatalf("confirmation = %q, want %q", p.ConfirmDeleteID, conv.ID)
	}
	if !tester.HasText("删除该会话后不可恢复，确认删除？") {
		t.Error("confirmation text missing")
	}
	clickAndFrame(t, tester, "确认删除")
	if len(store.List()) != 0 || p.CurrentID != "" {
		t.Errorf("conversation kept: list = %d, current = %q", len(store.List()), p.CurrentID)
	}
}

func TestPageEmptyState(t *testing.T) {
	p, _ := newTestPage(t, &fakeChatter{snapshot: onlineSnapshot("m1")})
	tester := newPageTester(t, p)
	tester.SetSize(960, 640)
	tester.Frame()
	for _, text := range []string{
		"Bowknot 助手", "新会话", "开始一段新对话", "会话", "还没有会话", "网关在线", "A · m1",
	} {
		if !tester.HasText(text) {
			t.Errorf("view does not show %q", text)
		}
	}
	if p.Blocker() != "" {
		t.Errorf("blocker = %q, want none", p.Blocker())
	}
	provider, model := p.Runner.Target()
	if provider != "p-00000001" || model != "m1" {
		t.Errorf("target = %q/%q, want the only chat target", provider, model)
	}
}

func TestPageGatewayStatesBlockInput(t *testing.T) {
	cases := []struct {
		name   string
		snap   gateway.Snapshot
		reason string
	}{
		{"not configured", gateway.Snapshot{State: gateway.StateNotConfigured},
			"网关未配置，配置后可启用对话"},
		{"offline", gateway.Snapshot{State: gateway.StateOffline},
			"网关离线，恢复连接后可启用对话"},
		{"no model", onlineSnapshot(""),
			"模型未配置，请先在设置中配置对话模型"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, store := newTestPage(t, &fakeChatter{snapshot: tc.snap})
			tester := newPageTester(t, p)
			p.Input = "你好"
			p.Send()
			if len(store.List()) != 0 {
				t.Error("send created a conversation while blocked")
			}
			if got := p.Blocker(); got != tc.reason {
				t.Errorf("blocker = %q, want %q", got, tc.reason)
			}
			tester.Frame()
			if !tester.HasText(tc.reason) {
				t.Errorf("view does not show %q", tc.reason)
			}
		})
	}
}

func TestPageSendCreatesConversationAndReply(t *testing.T) {
	f := &fakeChatter{
		snapshot: onlineSnapshot("m1"),
		handler: func(ctx context.Context, req gateway.ChatRequest) (gateway.ChatResponse, error) {
			req.OnDelta("回复")
			return gateway.ChatResponse{Content: "回复", Model: "m1",
				InputTokens: 7, OutputTokens: 2, HasUsage: true}, nil
		},
	}
	p, store := newTestPage(t, f)
	tester := newPageTester(t, p)
	p.Input = "你好"
	p.Send()
	waitFor(t, func() bool { return !p.Runner.Busy() })

	list := store.List()
	if len(list) != 1 {
		t.Fatalf("conversations = %d, want 1", len(list))
	}
	if list[0].Title != "你好" || list[0].Messages != 2 {
		t.Errorf("summary = %+v", list[0])
	}
	conv, _ := store.Get(list[0].ID)
	reply := conv.Messages[1]
	if reply.Content != "回复" || reply.Status != StatusOK {
		t.Errorf("reply = %+v", reply)
	}
	tester.Frame()
	for _, text := range []string{"你好", "回复", "输入 7 · 输出 2 token"} {
		if !tester.HasText(text) {
			t.Errorf("view does not show %q", text)
		}
	}
	if strings.TrimSpace(p.Input) != "" {
		t.Errorf("input = %q, want cleared", p.Input)
	}
}

func TestPageNewConversation(t *testing.T) {
	p, store := newTestPage(t, &fakeChatter{snapshot: onlineSnapshot("m1")})
	tester := newPageTester(t, p)
	if err := tester.Click("新会话"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if p.CurrentID == "" || len(store.List()) != 1 {
		t.Fatalf("conversation = %q, list = %d", p.CurrentID, len(store.List()))
	}
	if !tester.HasText("这个会话还没有消息，输入内容开始对话。") {
		t.Error("empty conversation hint missing")
	}
}

func TestPageModelPickerPinsTarget(t *testing.T) {
	f := &fakeChatter{
		snapshot: onlineSnapshot("m1"),
		targets: []gateway.TargetView{
			{ProviderID: "p-00000001", ProviderName: "A", Model: "m1"},
			{ProviderID: "p-00000002", ProviderName: "B", Model: "m2"},
		},
		handler: func(ctx context.Context, req gateway.ChatRequest) (gateway.ChatResponse, error) {
			return gateway.ChatResponse{Content: "ok", Model: req.Model}, nil
		},
	}
	p, _ := newTestPage(t, f)
	tester := newPageTester(t, p)
	tester.Frame()
	if err := tester.Click("A · m1"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if err := tester.Click("B · m2"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if provider, model := p.Runner.Target(); provider != "p-00000002" || model != "m2" {
		t.Errorf("target = %q/%q, want the picked model", provider, model)
	}
}

func TestPageDeleteNeedsConfirmation(t *testing.T) {
	p, store := newTestPage(t, &fakeChatter{snapshot: onlineSnapshot("m1")})
	conv := store.Create()
	if _, err := store.Append(conv.ID, Message{Role: RoleUser, Content: "要删除的会话",
		Status: StatusOK, At: 1}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(conv.ID); err != nil {
		t.Fatal(err)
	}
	p.CurrentID = conv.ID
	tester := newPageTester(t, p)
	clickAndFrame(t, tester, "会话菜单：要删除的会话")
	clickAndFrame(t, tester, "删除会话")
	if p.ConfirmDeleteID != conv.ID {
		t.Errorf("confirmation = %q, want %q", p.ConfirmDeleteID, conv.ID)
	}
	if !tester.HasText("删除该会话后不可恢复，确认删除？") {
		t.Error("confirmation text missing")
	}
	if len(store.List()) != 1 {
		t.Error("conversation deleted before confirmation")
	}
	if err := tester.Click("确认删除"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if len(store.List()) != 0 || p.CurrentID != "" {
		t.Errorf("conversation kept: list = %d, current = %q", len(store.List()), p.CurrentID)
	}
	if !tester.HasText("开始一段新对话") {
		t.Error("empty state missing after deleting the last conversation")
	}
}

func TestPageDeleteCancelKeepsConversation(t *testing.T) {
	p, store := newTestPage(t, &fakeChatter{snapshot: onlineSnapshot("m1")})
	conv := store.Create()
	p.CurrentID = conv.ID
	tester := newPageTester(t, p)
	clickAndFrame(t, tester, "会话菜单："+conv.TitleText())
	clickAndFrame(t, tester, "删除会话")
	if err := tester.Click("取消"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if len(store.List()) != 1 || p.ConfirmDeleteID != "" {
		t.Error("cancel deleted the conversation")
	}
}

func TestPageRegenerateReplacesLastReply(t *testing.T) {
	f := &fakeChatter{
		snapshot: onlineSnapshot("m1"),
		handler: func(ctx context.Context, req gateway.ChatRequest) (gateway.ChatResponse, error) {
			return gateway.ChatResponse{Content: "新的回复", Model: "m1", HasUsage: true}, nil
		},
	}
	p, store := newTestPage(t, f)
	conv := store.Create()
	if _, err := store.Append(conv.ID, Message{Role: RoleUser, Content: "问题", Status: StatusOK, At: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(conv.ID, Message{Role: RoleAssistant, Content: "旧回复", Status: StatusOK, At: 2}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(conv.ID); err != nil {
		t.Fatal(err)
	}
	p.CurrentID = conv.ID
	tester := newPageTester(t, p)
	if !tester.HasText("重新生成") {
		t.Fatal("regenerate button missing")
	}
	if err := tester.Click("重新生成"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !p.Runner.Busy() })
	conv, _ = store.Get(conv.ID)
	if len(conv.Messages) != 2 || conv.Messages[1].Content != "新的回复" {
		t.Errorf("messages = %+v", conv.Messages)
	}
}

func TestPageSwitchConversation(t *testing.T) {
	p, store := newTestPage(t, &fakeChatter{snapshot: onlineSnapshot("m1")})
	first := store.Create()
	second := store.Create()
	if _, err := store.Append(first.ID, Message{Role: RoleUser, Content: "第一个会话",
		Status: StatusOK, At: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(second.ID, Message{Role: RoleUser, Content: "第二个会话",
		Status: StatusOK, At: 20}); err != nil {
		t.Fatal(err)
	}
	p.CurrentID = second.ID
	tester := newPageTester(t, p)
	if !tester.HasText("第二个会话") {
		t.Fatal("selected conversation is not shown")
	}
	if err := tester.Click("第一个会话"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if p.CurrentID != first.ID {
		t.Errorf("current = %q, want %q", p.CurrentID, first.ID)
	}
	if !tester.HasText("第一个会话") {
		t.Error("switched conversation is not shown")
	}
}

func TestPageBusyShowsStop(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	f := &fakeChatter{
		snapshot: onlineSnapshot("m1"),
		handler: func(ctx context.Context, req gateway.ChatRequest) (gateway.ChatResponse, error) {
			close(started)
			<-release
			return gateway.ChatResponse{Content: "ok", Model: "m1"}, nil
		},
	}
	p, _ := newTestPage(t, f)
	tester := newPageTester(t, p)
	p.Input = "你好"
	p.Send()
	<-started
	tester.Frame()
	for _, text := range []string{"停止", "正在生成…"} {
		if !tester.HasText(text) {
			t.Errorf("view does not show %q while generating", text)
		}
	}
	if err := tester.Click("停止"); err != nil {
		t.Fatal(err)
	}
	close(release)
	waitFor(t, func() bool { return !p.Runner.Busy() })
	tester.Frame()
	if !tester.HasText("已中止") {
		t.Error("aborted state missing")
	}
}

func TestPageSwitchingKeepsGeneration(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	f := &fakeChatter{
		snapshot: onlineSnapshot("m1"),
		handler: func(ctx context.Context, req gateway.ChatRequest) (gateway.ChatResponse, error) {
			close(started)
			<-release
			return gateway.ChatResponse{Content: "后台回复", Model: "m1"}, nil
		},
	}
	p, store := newTestPage(t, f)
	running := store.Create()
	if _, err := store.Append(running.ID, Message{Role: RoleUser, Content: "长任务",
		Status: StatusOK, At: 1}); err != nil {
		t.Fatal(err)
	}
	p.CurrentID = running.ID
	if err := p.Runner.Start(context.Background(), running.ID); err != nil {
		t.Fatal(err)
	}
	<-started
	p.NewConversation()
	if p.Runner.RunningConversation() != running.ID {
		t.Errorf("running conversation = %q, want %q", p.Runner.RunningConversation(), running.ID)
	}
	close(release)
	waitFor(t, func() bool { return !p.Runner.Busy() })
	conv, _ := store.Get(running.ID)
	if got := conv.Messages[len(conv.Messages)-1].Content; got != "后台回复" {
		t.Errorf("reply = %q, want the result in its own conversation", got)
	}
}

func TestPageMessageMetaWithoutDetailPanel(t *testing.T) {
	f := &fakeChatter{
		snapshot: onlineSnapshot("m1"),
		handler: func(ctx context.Context, req gateway.ChatRequest) (gateway.ChatResponse, error) {
			return gateway.ChatResponse{Content: "回复", Model: "m1", InputTokens: 3,
				OutputTokens: 9, HasUsage: true, Duration: 1500 * time.Millisecond}, nil
		},
	}
	p, store := newTestPage(t, f)
	conv := store.Create()
	if _, err := store.Append(conv.ID, Message{Role: RoleUser, Content: "问题", Status: StatusOK, At: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(conv.ID, Message{Role: RoleAssistant, Content: "回复",
		Model: "m1", InputTokens: 3, OutputTokens: 9, DurationMS: 1500, Status: StatusOK,
		At: 2}); err != nil {
		t.Fatal(err)
	}
	p.CurrentID = conv.ID
	tester := newPageTester(t, p)
	for _, text := range []string{"1.5s", "输入 3 · 输出 9 token"} {
		if !tester.HasText(text) {
			t.Errorf("message does not show %q", text)
		}
	}
	if tester.HasText("运行详情") || tester.HasText("任务派发、子 Agent 与审批将在后续计划接入。") {
		t.Error("run details are still shown")
	}
}
