package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QingFengGod/bowknot-agent/internal/gateway"
	"github.com/QingFengGod/bowknot-agent/internal/task"
)

const (
	testProvider = "p-00000001"
	testModel    = "m1"
)

type dispatchFixture struct {
	dispatcher *Dispatcher
	convs      *Store
	tasks      *task.Store
	agents     *Registry
	runner     *Runner
	convID     string
	agent      Agent
	chatter    *fakeChatter
}

// newDispatchFixture builds a dispatcher whose gateway answers plans, reports
// and summaries through planReply, reportReply and summaryReply. A nil reply
// returns valid output.
func newDispatchFixture(t *testing.T, planReply, reportReply, summaryReply func(req gateway.ChatRequest) (string, error)) *dispatchFixture {
	t.Helper()
	f := &fakeChatter{
		snapshot: onlineSnapshot(testModel),
		targets:  []gateway.TargetView{{ProviderID: testProvider, ProviderName: "A", Model: testModel}},
	}
	convs := NewStore("")
	agents := NewRegistry(t.TempDir())
	if err := agents.Load(); err != nil {
		t.Fatalf("load agents: %v", err)
	}
	tasks := task.NewStore("")
	agent, err := agents.Create(Agent{Name: "资料整理", Enabled: true,
		ProviderID: testProvider, Model: testModel})
	if err != nil {
		t.Fatalf("save sub agent: %v", err)
	}
	runner := NewRunner(f, convs)
	runner.SetTarget(testProvider, testModel)
	d := NewDispatcher(f, convs, tasks, agents, runner)

	f.handler = func(ctx context.Context, req gateway.ChatRequest) (gateway.ChatResponse, error) {
		var text string
		var err error
		switch {
		case strings.HasPrefix(req.TaskID, "t-"):
			if reportReply != nil {
				text, err = reportReply(req)
			} else {
				text = `{"conclusion":"已完成","artifacts":["清单"],"open_issues":[],"summary":"整理完毕"}`
			}
		case strings.HasPrefix(req.TaskID, "g-"):
			if summaryReply != nil {
				text, err = summaryReply(req)
			} else {
				text = "任务已完成，产出见各任务结论。"
			}
		default:
			if planReply != nil {
				text, err = planReply(req)
			} else {
				text = `{"tasks":[{"goal":"整理要点","input":"材料","criteria":"按主题分组","assignee":"` + agent.ID + `"}]}`
			}
		}
		if err != nil {
			return gateway.ChatResponse{}, err
		}
		return gateway.ChatResponse{
			Content:      text,
			Model:        req.Model,
			InputTokens:  8,
			OutputTokens: 16,
			HasUsage:     true,
			Duration:     5 * time.Millisecond,
		}, nil
	}

	conv := convs.Create()
	if _, err := convs.Append(conv.ID, Message{Role: RoleUser, Content: "帮我把会议纪要拆成待办",
		Status: StatusOK, At: 1}); err != nil {
		t.Fatalf("append user: %v", err)
	}
	if err := convs.Save(conv.ID); err != nil {
		t.Fatalf("save conversation: %v", err)
	}

	return &dispatchFixture{dispatcher: d, convs: convs, tasks: tasks, agents: agents,
		runner: runner, convID: conv.ID, agent: agent, chatter: f}
}

func (f *dispatchFixture) plan(t *testing.T) task.Group {
	t.Helper()
	if err := f.dispatcher.Plan(context.Background(), f.convID); err != nil {
		t.Fatalf("Plan() error: %v", err)
	}
	f.dispatcher.Wait()
	list := f.tasks.List()
	if len(list) != 1 {
		t.Fatalf("groups = %d, want 1", len(list))
	}
	g, ok := f.tasks.Get(list[0].ID)
	if !ok {
		t.Fatal("group missing after plan")
	}
	return g
}

func TestPlanCreatesPendingCard(t *testing.T) {
	f := newDispatchFixture(t, nil, nil, nil)
	g := f.plan(t)

	if g.Status != task.GroupPending {
		t.Errorf("status = %s, want pending", g.Status)
	}
	if len(g.Tasks) != 1 || g.Tasks[0].AssigneeName != "资料整理" {
		t.Fatalf("tasks = %+v", g.Tasks)
	}
	conv, _ := f.convs.Get(f.convID)
	if len(conv.Messages) != 2 {
		t.Fatalf("messages = %d, want user + card", len(conv.Messages))
	}
	card := conv.Messages[1]
	if card.TaskGroupID != g.ID || card.Role != RoleAssistant {
		t.Fatalf("card = %+v", card)
	}
	if g.MessageIndex != 1 {
		t.Errorf("message index = %d, want 1", g.MessageIndex)
	}
	planReq := f.chatter.lastRequest()
	if planReq.TaskID != "" || planReq.Stream || planReq.ConversationID != f.convID {
		t.Errorf("plan request = %+v", planReq)
	}
	if f.runner.Busy() {
		t.Error("dispatch slot still held after plan")
	}
}

func TestPlanRetriesInvalidJSON(t *testing.T) {
	f := newDispatchFixture(t, nil, nil, nil)
	var mu sync.Mutex
	calls := 0
	f.chatter.handler = func(ctx context.Context, req gateway.ChatRequest) (gateway.ChatResponse, error) {
		mu.Lock()
		calls++
		first := calls == 1
		mu.Unlock()
		if first {
			return gateway.ChatResponse{Content: "先解释一下，然后给出任务", Model: req.Model}, nil
		}
		return gateway.ChatResponse{
			Content: `{"tasks":[{"goal":"整理要点","input":"材料","criteria":"","assignee":"` +
				f.agent.ID + `"}]}`,
			Model: req.Model, HasUsage: true,
		}, nil
	}

	g := f.plan(t)
	if g.Status != task.GroupPending || len(g.Tasks) != 1 {
		t.Fatalf("group = %+v", g)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Errorf("plan calls = %d, want 2", calls)
	}
}

func TestPlanFallsBackWithoutGroup(t *testing.T) {
	f := newDispatchFixture(t, func(req gateway.ChatRequest) (string, error) {
		return "我无法按要求输出 JSON", nil
	}, nil, nil)

	if err := f.dispatcher.Plan(context.Background(), f.convID); err != nil {
		t.Fatalf("Plan() error: %v", err)
	}
	f.dispatcher.Wait()

	if len(f.tasks.List()) != 0 {
		t.Fatal("a group was created for an unparsable plan")
	}
	conv, _ := f.convs.Get(f.convID)
	last := conv.Messages[len(conv.Messages)-1]
	if last.Status != StatusError || last.ErrorCode != string(gateway.KindModel) {
		t.Fatalf("fallback message = %+v", last)
	}
	if !strings.Contains(last.Content, "无法拆解任务") {
		t.Errorf("fallback text = %q", last.Content)
	}
	if f.runner.Busy() {
		t.Error("dispatch slot still held after failure")
	}
}

func TestPlanRejectsInvalidAssignee(t *testing.T) {
	f := newDispatchFixture(t, func(req gateway.ChatRequest) (string, error) {
		return `{"tasks":[{"goal":"整理","input":"材料","criteria":"","assignee":"a-deadbeef"}]}`, nil
	}, nil, nil)
	if err := f.dispatcher.Plan(context.Background(), f.convID); err != nil {
		t.Fatal(err)
	}
	f.dispatcher.Wait()
	if len(f.tasks.List()) != 0 {
		t.Fatal("invalid assignee was accepted")
	}
}

func TestConfirmRunsTasksAndSummarizes(t *testing.T) {
	f := newDispatchFixture(t, nil, nil, nil)
	g := f.plan(t)

	if err := f.dispatcher.Confirm(g.ID); err != nil {
		t.Fatalf("Confirm() error: %v", err)
	}
	f.dispatcher.Wait()

	g, ok := f.tasks.Get(g.ID)
	if !ok {
		t.Fatal("group missing")
	}
	if g.Status != task.GroupDone {
		t.Fatalf("group = %s, want done", g.Status)
	}
	if g.Tasks[0].Status != task.TaskDone || g.Tasks[0].Report == nil {
		t.Fatalf("task = %+v", g.Tasks[0])
	}
	if g.Tasks[0].Report.Conclusion != "已完成" || g.Tasks[0].InputTokens != 8 {
		t.Errorf("report = %+v tokens = %d", g.Tasks[0].Report, g.Tasks[0].InputTokens)
	}
	conv, _ := f.convs.Get(f.convID)
	last := conv.Messages[len(conv.Messages)-1]
	if last.TaskGroupID != "" || !strings.Contains(last.Content, "任务已完成") {
		t.Fatalf("summary message = %+v", last)
	}
	if f.runner.Busy() {
		t.Error("dispatch slot still held")
	}
}

func TestReportFallsBackToPlainText(t *testing.T) {
	f := newDispatchFixture(t, nil, func(req gateway.ChatRequest) (string, error) {
		return "这里是纯文本结论，没有 JSON。", nil
	}, nil)
	g := f.plan(t)
	if err := f.dispatcher.Confirm(g.ID); err != nil {
		t.Fatal(err)
	}
	f.dispatcher.Wait()

	g, _ = f.tasks.Get(g.ID)
	if g.Tasks[0].Status != task.TaskDone {
		t.Fatalf("task = %s", g.Tasks[0].Status)
	}
	report := g.Tasks[0].Report
	if report == nil || !report.Unstructured {
		t.Fatalf("report = %+v", report)
	}
	if !strings.Contains(report.Conclusion, "纯文本结论") {
		t.Errorf("conclusion = %q", report.Conclusion)
	}
}

func TestFailedTaskCanBeRetried(t *testing.T) {
	var mu sync.Mutex
	reportCalls := 0
	f := newDispatchFixture(t, nil, func(req gateway.ChatRequest) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		reportCalls++
		if reportCalls == 1 {
			return "", &gateway.Error{Kind: gateway.KindAuth, Message: "密钥无效"}
		}
		return `{"conclusion":"重试成功","artifacts":[],"open_issues":[],"summary":""}`, nil
	}, nil)

	g := f.plan(t)
	if err := f.dispatcher.Confirm(g.ID); err != nil {
		t.Fatal(err)
	}
	f.dispatcher.Wait()

	g, _ = f.tasks.Get(g.ID)
	if g.Tasks[0].Status != task.TaskFailed || g.Tasks[0].ErrorCode != string(gateway.KindAuth) {
		t.Fatalf("first task = %+v", g.Tasks[0])
	}

	if err := f.dispatcher.Retry(g.ID, g.Tasks[0].ID); err != nil {
		t.Fatalf("Retry() error: %v", err)
	}
	f.dispatcher.Wait()

	g, _ = f.tasks.Get(g.ID)
	if len(g.Tasks) != 2 {
		t.Fatalf("tasks = %d, want 2", len(g.Tasks))
	}
	if g.Tasks[1].Status != task.TaskDone || g.Tasks[1].RetryOf != g.Tasks[0].ID {
		t.Fatalf("retry task = %+v", g.Tasks[1])
	}
	if g.Tasks[0].Status != task.TaskFailed {
		t.Errorf("original task changed to %s", g.Tasks[0].Status)
	}
}

func TestCancelPendingGroup(t *testing.T) {
	f := newDispatchFixture(t, nil, nil, nil)
	g := f.plan(t)

	f.dispatcher.Cancel(g.ID)
	g, _ = f.tasks.Get(g.ID)
	if g.Status != task.GroupCanceled || g.Tasks[0].Status != task.TaskCanceled {
		t.Fatalf("group = %s, task = %s", g.Status, g.Tasks[0].Status)
	}
}

func TestCancelRunningGroup(t *testing.T) {
	started := make(chan struct{}, 1)
	var once sync.Once
	f := newDispatchFixture(t, nil, nil, nil)
	f.chatter.handler = func(ctx context.Context, req gateway.ChatRequest) (gateway.ChatResponse, error) {
		if strings.HasPrefix(req.TaskID, "t-") {
			once.Do(func() { started <- struct{}{} })
			<-ctx.Done()
			return gateway.ChatResponse{}, ctx.Err()
		}
		return gateway.ChatResponse{Content: `{"tasks":[{"goal":"整理","input":"材料","criteria":"","assignee":"` +
			f.agent.ID + `"}]}`, Model: req.Model}, nil
	}

	g := f.plan(t)
	if err := f.dispatcher.Confirm(g.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("task never started")
	}
	f.dispatcher.Cancel(g.ID)
	f.dispatcher.Wait()

	g, _ = f.tasks.Get(g.ID)
	if g.Status != task.GroupCanceled {
		t.Fatalf("group = %s, want canceled", g.Status)
	}
	for i := range g.Tasks {
		if g.Tasks[i].Status != task.TaskCanceled {
			t.Errorf("task %d = %s, want canceled", i, g.Tasks[i].Status)
		}
	}
}

// interruptedGroup stores a group as it would look after an app restart:
// one task settled, one left interrupted.
func interruptedGroup(t *testing.T, f *dispatchFixture, id string) task.Group {
	t.Helper()
	now := time.Now()
	g := task.NewGroup(now, f.convID, 0, testProvider, testModel)
	g.ID = id
	done := task.NewTask(now, "整理要点", "材料", "", f.agent.ID, "资料整理")
	done.Status = task.TaskDone
	done.DurationMS = 12
	done.Report = &task.Report{Conclusion: "旧结论"}
	left := task.NewTask(now, "补充来源", "材料", "", f.agent.ID, "资料整理")
	left.Status = task.TaskInterrupted
	g.Tasks = []task.Task{done, left}
	g.Status = task.GroupInterrupted
	if err := f.tasks.Save(g); err != nil {
		t.Fatalf("save interrupted group: %v", err)
	}
	return *g
}

func TestResumeRunsUnfinishedTasks(t *testing.T) {
	var mu sync.Mutex
	reports := 0
	f := newDispatchFixture(t, nil, func(req gateway.ChatRequest) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		reports++
		return `{"conclusion":"新结论","artifacts":[],"open_issues":[],"summary":""}`, nil
	}, nil)
	g := interruptedGroup(t, f, "g-00000001")

	if err := f.dispatcher.Resume(g.ID); err != nil {
		t.Fatalf("Resume() error: %v", err)
	}
	f.dispatcher.Wait()

	got, _ := f.tasks.Get(g.ID)
	if got.Status != task.GroupDone {
		t.Fatalf("group = %s, want done", got.Status)
	}
	if got.Tasks[0].Status != task.TaskDone || got.Tasks[0].Report.Conclusion != "旧结论" {
		t.Errorf("settled task changed: %+v", got.Tasks[0])
	}
	if got.Tasks[1].Status != task.TaskDone || got.Tasks[1].Report == nil ||
		got.Tasks[1].Report.Conclusion != "新结论" {
		t.Errorf("unfinished task = %+v", got.Tasks[1])
	}
	if got.Summary == "" {
		t.Error("summary was not written back to the group")
	}
	mu.Lock()
	defer mu.Unlock()
	if reports != 1 {
		t.Errorf("report calls = %d, want 1", reports)
	}
	if f.runner.Busy() {
		t.Error("dispatch slot still held")
	}
}

func TestResumeRejectsSettledGroup(t *testing.T) {
	f := newDispatchFixture(t, nil, nil, nil)
	g := interruptedGroup(t, f, "g-00000001")
	if err := f.dispatcher.Terminate(g.ID); err != nil {
		t.Fatalf("Terminate() error: %v", err)
	}
	if err := f.dispatcher.Resume(g.ID); err == nil {
		t.Fatal("Resume() accepted a canceled group")
	}
}

func TestTerminateInterruptedGroup(t *testing.T) {
	f := newDispatchFixture(t, nil, nil, nil)
	g := interruptedGroup(t, f, "g-00000001")
	conv, _ := f.convs.Get(f.convID)
	messages := len(conv.Messages)

	if err := f.dispatcher.Terminate(g.ID); err != nil {
		t.Fatalf("Terminate() error: %v", err)
	}
	got, _ := f.tasks.Get(g.ID)
	if got.Status != task.GroupCanceled {
		t.Fatalf("group = %s, want canceled", got.Status)
	}
	if got.Tasks[0].Status != task.TaskDone {
		t.Errorf("settled task changed to %s", got.Tasks[0].Status)
	}
	if got.Tasks[1].Status != task.TaskCanceled {
		t.Errorf("unfinished task = %s, want canceled", got.Tasks[1].Status)
	}
	conv, _ = f.convs.Get(f.convID)
	if len(conv.Messages) != messages {
		t.Errorf("terminate appended a message: %d -> %d", messages, len(conv.Messages))
	}
}

func TestDeleteAndCleanTerminal(t *testing.T) {
	f := newDispatchFixture(t, nil, nil, nil)
	g := f.plan(t)
	f.dispatcher.Cancel(g.ID)

	if err := f.dispatcher.Delete(g.ID); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}
	if _, ok := f.tasks.Get(g.ID); ok {
		t.Fatal("deleted group still stored")
	}
	if err := f.dispatcher.Delete(g.ID); !errors.Is(err, ErrGroupNotFound) {
		t.Fatalf("Delete() = %v, want ErrGroupNotFound", err)
	}

	settled := interruptedGroup(t, f, "g-00000002")
	if err := f.dispatcher.Terminate(settled.ID); err != nil {
		t.Fatalf("Terminate() error: %v", err)
	}
	now := time.Now()
	pending := task.NewGroup(now, f.convID, 0, testProvider, testModel)
	pending.ID = "g-00000003"
	pending.Status = task.GroupPending
	pending.Tasks = []task.Task{task.NewTask(now, "待确认", "", "", f.agent.ID, "资料整理")}
	if err := f.tasks.Save(pending); err != nil {
		t.Fatalf("save pending: %v", err)
	}
	removed, err := f.dispatcher.CleanTerminal()
	if err != nil {
		t.Fatalf("CleanTerminal() error: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
	if _, ok := f.tasks.Get(pending.ID); !ok {
		t.Error("pending group was cleaned")
	}
}

func TestPlanRefusesAtGroupLimit(t *testing.T) {
	f := newDispatchFixture(t, nil, nil, nil)
	now := time.Now()
	for i := 0; i < task.MaxGroups; i++ {
		g := task.NewGroup(now, f.convID, 0, testProvider, testModel)
		g.ID = fmt.Sprintf("g-%08d", i)
		g.Status = task.GroupDone
		g.Tasks = []task.Task{task.NewTask(now, "旧任务", "", "", f.agent.ID, "资料整理")}
		g.Tasks[0].Status = task.TaskDone
		if err := f.tasks.Save(g); err != nil {
			t.Fatalf("save group %d: %v", i, err)
		}
	}

	if err := f.dispatcher.Plan(context.Background(), f.convID); err != nil {
		t.Fatalf("Plan() error: %v", err)
	}
	f.dispatcher.Wait()

	if got := len(f.tasks.List()); got != task.MaxGroups {
		t.Errorf("groups = %d, want %d", got, task.MaxGroups)
	}
	conv, _ := f.convs.Get(f.convID)
	last := conv.Messages[len(conv.Messages)-1]
	if !strings.Contains(last.Content, "上限") {
		t.Errorf("limit message = %q", last.Content)
	}
	if last.TaskGroupID != "" {
		t.Errorf("limit message carries a task card: %+v", last)
	}
}

func TestDeleteRefusesUnsettledGroup(t *testing.T) {
	f := newDispatchFixture(t, nil, nil, nil)
	g := f.plan(t)
	if err := f.dispatcher.Delete(g.ID); err == nil {
		t.Fatal("Delete() accepted a pending group")
	}
}

func TestParsePlan(t *testing.T) {
	candidates := []Agent{{ID: "a-00000001", Name: "A"}}
	cases := []struct {
		name    string
		content string
		wantErr bool
	}{
		{"valid", `{"tasks":[{"goal":"整理","input":"材料","criteria":"","assignee":"a-00000001"}]}`, false},
		{"fenced", "```json\n{\"tasks\":[{\"goal\":\"整理\",\"input\":\"\",\"criteria\":\"\",\"assignee\":\"a-00000001\"}]}\n```", false},
		{"empty", `{"tasks":[]}`, true},
		{"bad json", `not json`, true},
		{"unknown assignee", `{"tasks":[{"goal":"整理","input":"","criteria":"","assignee":"a-00000002"}]}`, true},
		{"no goal", `{"tasks":[{"goal":"","input":"","criteria":"","assignee":"a-00000001"}]}`, true},
		{"too many", `{"tasks":[` + strings.Repeat(`{"goal":"a","input":"","criteria":"","assignee":"a-00000001"},`, task.MaxTasksPerGroup) +
			`{"goal":"a","input":"","criteria":"","assignee":"a-00000001"}]}`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parsePlan(c.content, candidates)
			if (err != nil) != c.wantErr {
				t.Fatalf("parsePlan() error = %v, wantErr %v", err, c.wantErr)
			}
		})
	}
}

func TestParseReport(t *testing.T) {
	report, err := parseReport("```json\n{\"conclusion\":\"结论\",\"artifacts\":[\"清单\"],\"open_issues\":[\"缺人\"],\"summary\":\"过程\"}\n```")
	if err != nil {
		t.Fatalf("parseReport() error: %v", err)
	}
	if report.Conclusion != "结论" || len(report.Artifacts) != 1 || len(report.OpenIssues) != 1 {
		t.Fatalf("report = %+v", report)
	}
	if report.Unstructured {
		t.Error("structured report marked unstructured")
	}
	if _, err := parseReport(`{"conclusion":""}`); err == nil {
		t.Error("empty conclusion accepted")
	}
	if _, err := parseReport("文本"); err == nil {
		t.Error("plain text accepted")
	}
}

func TestPlanRejectsBusySlot(t *testing.T) {
	f := newDispatchFixture(t, nil, nil, nil)
	if err := f.dispatcher.Plan(context.Background(), f.convID); err != nil {
		t.Fatal(err)
	}
	f.dispatcher.Wait()

	f.runner.ReserveExternal()
	if err := f.dispatcher.Plan(context.Background(), f.convID); !errors.Is(err, ErrBusy) {
		t.Fatalf("Plan() = %v, want ErrBusy", err)
	}
	f.runner.ReleaseExternal()
}
