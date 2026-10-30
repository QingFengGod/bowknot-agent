package group

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QingFengGod/bowknot-agent/internal/gateway"
)

type fakeAgents struct {
	specs   map[string]AgentSpec
	order   []string
	prompts map[string]string
}

func newFakeAgents(ids ...string) *fakeAgents {
	f := &fakeAgents{specs: map[string]AgentSpec{}, prompts: map[string]string{}}
	f.specs[MainAgentID] = AgentSpec{ID: MainAgentID, Name: "主 Agent", ProviderID: "p-00000001", Model: "m", Enabled: true}
	f.order = append(f.order, MainAgentID)
	for _, id := range ids {
		f.specs[id] = AgentSpec{ID: id, Name: "成员 " + id, ProviderID: "p-00000001", Model: "m", Enabled: true}
		f.order = append(f.order, id)
	}
	return f
}

func (f *fakeAgents) Agent(id string) (AgentSpec, bool) { s, ok := f.specs[id]; return s, ok }
func (f *fakeAgents) AgentIDs() []string                { return f.order }
func (f *fakeAgents) Prompt(id, _ string) (string, string) {
	text, ok := f.prompts[id]
	if !ok {
		text = "你是 " + id
	}
	return text, ""
}

type scriptChat struct {
	mu    sync.Mutex
	steps []func(gateway.ChatRequest) (gateway.ChatResponse, error)
	calls []gateway.ChatRequest
}

func (s *scriptChat) chat(_ context.Context, req gateway.ChatRequest) (gateway.ChatResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, req)
	if len(s.steps) == 0 {
		return gateway.ChatResponse{}, errors.New("脚本已用完")
	}
	step := s.steps[0]
	s.steps = s.steps[1:]
	return step(req)
}

func reply(content string) func(gateway.ChatRequest) (gateway.ChatResponse, error) {
	return func(gateway.ChatRequest) (gateway.ChatResponse, error) {
		return gateway.ChatResponse{Content: content, InputTokens: 10, OutputTokens: 5, HasUsage: true}, nil
	}
}

func failStep(err error) func(gateway.ChatRequest) (gateway.ChatResponse, error) {
	return func(gateway.ChatRequest) (gateway.ChatResponse, error) { return gateway.ChatResponse{}, err }
}

type fakeTools struct {
	tools map[string][]ToolSpec
	calls []string
	err   error
	out   string
}

func (f *fakeTools) Tools(id string) []ToolSpec { return f.tools[id] }
func (f *fakeTools) Call(_ context.Context, id, name, _ string) (string, error) {
	f.calls = append(f.calls, id+":"+name)
	if f.err != nil {
		return "", f.err
	}
	if f.out == "" {
		return "ok", nil
	}
	return f.out, nil
}

func executorFixture(t *testing.T) (*Executor, *Store, *Bus, *Run, *Task, *fakeAgents, *scriptChat) {
	t.Helper()
	store := openStore(t, t.TempDir())
	g := testGroup()
	if err := store.SaveGroup(g); err != nil {
		t.Fatalf("SaveGroup() error: %v", err)
	}
	run := testRun(t, testTask("t-00000001"))
	if err := store.SaveRun(run); err != nil {
		t.Fatalf("SaveRun() error: %v", err)
	}
	bus, err := NewBus(store, run.ID)
	if err != nil {
		t.Fatalf("NewBus() error: %v", err)
	}
	agents := newFakeAgents("a-00000001", "a-00000002")
	chat := &scriptChat{}
	exec := NewExecutor(store, bus, chat.chat)
	exec.Agents = agents
	exec.WaitTimeout = 200 * time.Millisecond
	task := &run.Plan.Tasks[0]
	return exec, store, bus, run, task, agents, chat
}

func TestExecutorPostsProgressAndReports(t *testing.T) {
	exec, store, bus, run, task, _, chat := executorFixture(t)
	defer bus.Close()
	chat.steps = []func(gateway.ChatRequest) (gateway.ChatResponse, error){
		reply(`{"action":"tool","tool":"group.post","args":{"content":"开始处理"}}`),
		reply(`{"action":"final","report":{"conclusion":"整理完成","artifacts":["摘要.md"],"open_issues":["缺少脚注"]}}`),
	}
	out := exec.RunTask(context.Background(), run, task)
	if out.Failed() {
		t.Fatalf("RunTask() failed: %+v", out)
	}
	if out.Report == nil || out.Report.Conclusion != "整理完成" || len(out.Report.Artifacts) != 1 {
		t.Fatalf("汇报 = %+v", out.Report)
	}
	msgs := store.Messages(run.ID)
	if len(msgs) != 2 || msgs[0].Type != MessageProgress || msgs[1].Type != MessageReport {
		t.Fatalf("消息 = %+v", msgs)
	}
	if msgs[1].Content != "整理完成" {
		t.Errorf("汇报内容 = %q", msgs[1].Content)
	}
	if len(chat.calls) != 2 || !strings.Contains(chat.calls[1].Messages[len(chat.calls[1].Messages)-1].Content, "进展已广播") {
		t.Fatalf("第二轮回填 = %+v", chat.calls[1].Messages)
	}
}

func TestExecutorRequestWaitResponse(t *testing.T) {
	exec, store, bus, run, task, _, chat := executorFixture(t)
	defer bus.Close()
	bus.OnMessage(func(m Message) {
		if m.Type == MessageRequest {
			go func() {
				_, _ = bus.Respond("a-00000002", "t-00000002", m.FromID, m.ID, "补充完成")
			}()
		}
	})
	chat.steps = []func(gateway.ChatRequest) (gateway.ChatResponse, error){
		reply(`{"action":"tool","tool":"group.request","args":{"to":"a-00000002","content":"请补充来源"}}`),
		reply(`{"action":"tool","tool":"group.wait","args":{"timeout_seconds":1}}`),
		reply(`{"action":"final","report":{"conclusion":"已收到来源"}}`),
	}
	out := exec.RunTask(context.Background(), run, task)
	if out.Failed() {
		t.Fatalf("RunTask() failed: %+v", out)
	}
	msgs := store.Messages(run.ID)
	var reqID, respID string
	for _, m := range msgs {
		switch m.Type {
		case MessageRequest:
			reqID = m.ID
		case MessageResponse:
			respID = m.ReplyTo
		}
	}
	if reqID == "" || respID != reqID {
		t.Fatalf("请求/答复关联失败: req=%q resp=%q msgs=%+v", reqID, respID, msgs)
	}
}

func TestExecutorStepLimit(t *testing.T) {
	exec, store, bus, run, task, _, chat := executorFixture(t)
	defer bus.Close()
	exec.MaxStep = 2
	chat.steps = []func(gateway.ChatRequest) (gateway.ChatResponse, error){
		reply(`{"action":"tool","tool":"group.post","args":{"content":"一"}}`),
		reply(`{"action":"tool","tool":"group.post","args":{"content":"二"}}`),
	}
	out := exec.RunTask(context.Background(), run, task)
	if !out.Failed() || out.ErrorCode != "step_limit" || !errors.Is(out.Err, ErrStepLimit) {
		t.Fatalf("RunTask() = %+v", out)
	}
	last := store.Messages(run.ID)
	if last[len(last)-1].Type != MessageBlocked || !strings.Contains(last[len(last)-1].Content, "步数上限") {
		t.Fatalf("阻塞消息 = %+v", last[len(last)-1])
	}
}

func TestExecutorCanceled(t *testing.T) {
	exec, store, bus, run, task, _, _ := executorFixture(t)
	defer bus.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out := exec.RunTask(ctx, run, task)
	if !out.Failed() || out.ErrorCode != "canceled" {
		t.Fatalf("RunTask() = %+v", out)
	}
	msgs := store.Messages(run.ID)
	if len(msgs) != 1 || msgs[0].Type != MessageBlocked {
		t.Fatalf("消息 = %+v", msgs)
	}
}

func TestExecutorModelErrorFails(t *testing.T) {
	exec, store, bus, run, task, _, chat := executorFixture(t)
	defer bus.Close()
	chat.steps = []func(gateway.ChatRequest) (gateway.ChatResponse, error){
		failStep(&gateway.Error{Kind: gateway.KindQuota, Message: "额度不足"}),
	}
	out := exec.RunTask(context.Background(), run, task)
	if !out.Failed() || out.ErrorCode != string(gateway.KindQuota) {
		t.Fatalf("RunTask() = %+v", out)
	}
	msgs := store.Messages(run.ID)
	if len(msgs) != 1 || !strings.Contains(msgs[0].Content, "额度不足") {
		t.Fatalf("消息 = %+v", msgs)
	}
}

func TestExecutorPluginToolErrorIsFedBack(t *testing.T) {
	exec, _, bus, run, task, _, chat := executorFixture(t)
	defer bus.Close()
	tools := &fakeTools{err: errors.New("插件拒绝执行")}
	exec.Tools = tools
	chat.steps = []func(gateway.ChatRequest) (gateway.ChatResponse, error){
		reply(`{"action":"tool","tool":"web.fetch","args":{"url":"https://example.com"}}`),
		reply(`{"action":"final","report":{"conclusion":"改用其他来源"}}`),
	}
	out := exec.RunTask(context.Background(), run, task)
	if out.Failed() {
		t.Fatalf("RunTask() = %+v", out)
	}
	if len(tools.calls) != 1 || tools.calls[0] != "a-00000001:web.fetch" {
		t.Fatalf("工具调用 = %+v", tools.calls)
	}
	last := chat.calls[1].Messages[len(chat.calls[1].Messages)-1].Content
	if !strings.Contains(last, "工具失败：插件拒绝执行") {
		t.Fatalf("错误未回填: %q", last)
	}
}

func TestExecutorPluginToolSuccess(t *testing.T) {
	exec, _, bus, run, task, _, chat := executorFixture(t)
	defer bus.Close()
	tools := &fakeTools{
		tools: map[string][]ToolSpec{"a-00000001": {{Name: "web.fetch", Description: "抓取网页", Params: "{}"}}},
		out:   "页面正文",
	}
	exec.Tools = tools
	chat.steps = []func(gateway.ChatRequest) (gateway.ChatResponse, error){
		reply(`{"action":"tool","tool":"web.fetch","args":{"url":"https://example.com"}}`),
		reply(`{"action":"final","report":{"conclusion":"抓取完成"}}`),
	}
	out := exec.RunTask(context.Background(), run, task)
	if out.Failed() {
		t.Fatalf("RunTask() = %+v", out)
	}
	if !strings.Contains(chat.calls[0].Messages[0].Content, "web.fetch") {
		t.Fatalf("系统提示缺少插件工具: %q", chat.calls[0].Messages[0].Content)
	}
}

func TestExecutorPlainTextFallsBackToUnstructuredReport(t *testing.T) {
	exec, _, bus, run, task, _, chat := executorFixture(t)
	defer bus.Close()
	chat.steps = []func(gateway.ChatRequest) (gateway.ChatResponse, error){reply("完成了，但没有按格式返回")}
	out := exec.RunTask(context.Background(), run, task)
	if out.Failed() || out.Report == nil || !out.Report.Unstructured {
		t.Fatalf("RunTask() = %+v", out)
	}
	if !strings.Contains(out.Report.Conclusion, "完成") {
		t.Fatalf("兜底汇报 = %+v", out.Report)
	}
}

func TestExecutorUnknownToolIsFedBack(t *testing.T) {
	exec, _, bus, run, task, _, chat := executorFixture(t)
	defer bus.Close()
	chat.steps = []func(gateway.ChatRequest) (gateway.ChatResponse, error){
		reply(`{"action":"tool","tool":"group.delete","args":{}}`),
		reply(`{"action":"final","report":{"conclusion":"忽略未知工具"}}`),
	}
	out := exec.RunTask(context.Background(), run, task)
	if out.Failed() {
		t.Fatalf("RunTask() = %+v", out)
	}
	last := chat.calls[1].Messages[len(chat.calls[1].Messages)-1].Content
	if !strings.Contains(last, "未知工具") {
		t.Fatalf("错误未回填: %q", last)
	}
}

func TestExecutorEmptyReplyFails(t *testing.T) {
	exec, store, bus, run, task, _, chat := executorFixture(t)
	defer bus.Close()
	chat.steps = []func(gateway.ChatRequest) (gateway.ChatResponse, error){reply("   ")}
	out := exec.RunTask(context.Background(), run, task)
	if !out.Failed() || out.ErrorCode != "model" {
		t.Fatalf("RunTask() = %+v", out)
	}
	if msgs := store.Messages(run.ID); len(msgs) != 1 || msgs[0].Type != MessageBlocked {
		t.Fatalf("消息 = %+v", msgs)
	}
}

func TestExecutorDisabledAgentFails(t *testing.T) {
	exec, _, bus, run, task, agents, _ := executorFixture(t)
	defer bus.Close()
	spec := agents.specs["a-00000001"]
	spec.Enabled = false
	agents.specs["a-00000001"] = spec
	out := exec.RunTask(context.Background(), run, task)
	if !out.Failed() || out.ErrorCode != "model" {
		t.Fatalf("RunTask() = %+v", out)
	}
}
