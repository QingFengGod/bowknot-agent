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

func managerFixture(t *testing.T, tasks ...Task) (*Manager, *Store, *fakeAgents, *scriptChat, *Run) {
	t.Helper()
	store := openStore(t, t.TempDir())
	g := testGroup()
	if err := store.SaveGroup(g); err != nil {
		t.Fatalf("SaveGroup() error: %v", err)
	}
	var run *Run
	if len(tasks) == 0 {
		run = NewRun(testTime(), "gp-00000001", "资料组", "c-00000001", "整理资料")
	} else {
		run = testRun(t, tasks...)
		run.ConversationID = "c-00000001"
	}
	if err := store.SaveRun(run); err != nil {
		t.Fatalf("SaveRun() error: %v", err)
	}
	agents := newFakeAgents("a-00000001", "a-00000002")
	chat := &scriptChat{}
	manager := NewManager(store, agents, chat.chat)
	manager.Now = testTime
	return manager, store, agents, chat, run
}

func waitRunStatus(t *testing.T, store *Store, runID string, want RunStatus) Run {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		run, ok := store.GetRun(runID)
		if ok && run.Status == want {
			// done/failed 由调度器先行写入，汇总随后补齐，避免取到中间态。
			if (want == RunDone || want == RunFailed) && run.Summary == "" {
				time.Sleep(5 * time.Millisecond)
				continue
			}
			return run
		}
		time.Sleep(5 * time.Millisecond)
	}
	run, _ := store.GetRun(runID)
	t.Fatalf("Run 状态 = %s, want %s（%+v）", run.Status, want, run)
	return Run{}
}

func taskFinal(conclusion string) func(gateway.ChatRequest) (gateway.ChatResponse, error) {
	return reply(`{"action":"final","report":{"conclusion":"` + conclusion + `","artifacts":["` + conclusion + `.md"]}}`)
}

func TestManagerConfirmExecutesAndSummarizes(t *testing.T) {
	manager, store, _, chat, run := managerFixture(t, testTask("t-00000001"))
	replyCh := make(chan string, 1)
	manager.OnAssistantReply = func(conversationID, runID, text string) {
		select {
		case replyCh <- text:
		default:
		}
	}
	chat.steps = []func(gateway.ChatRequest) (gateway.ChatResponse, error){
		taskFinal("资料已整理"),
		reply("整体汇总：资料已整理"),
	}
	if err := manager.Confirm(run.ID); err != nil {
		t.Fatalf("Confirm() error: %v", err)
	}
	got := waitRunStatus(t, store, run.ID, RunDone)
	if got.Summary != "整体汇总：资料已整理" {
		t.Fatalf("Summary = %q", got.Summary)
	}
	if got.Plan.Tasks[0].Report == nil || got.Plan.Tasks[0].Report.Conclusion != "资料已整理" {
		t.Fatalf("任务汇报 = %+v", got.Plan.Tasks[0])
	}
	if msgs := store.Messages(run.ID); len(msgs) != 1 || msgs[0].Type != MessageReport {
		t.Fatalf("消息 = %+v", msgs)
	}
	select {
	case text := <-replyCh:
		if !strings.Contains(text, "整体汇总") {
			t.Fatalf("会话回复 = %q", text)
		}
	case <-time.After(time.Second):
		t.Fatal("会话未收到汇总回复")
	}
}

func TestManagerFailureSummary(t *testing.T) {
	manager, store, _, chat, run := managerFixture(t, testTask("t-00000001"))
	chat.steps = []func(gateway.ChatRequest) (gateway.ChatResponse, error){
		failStep(&gateway.Error{Kind: gateway.KindQuota, Message: "额度不足"}),
		reply("汇总：任务因额度不足失败"),
	}
	if err := manager.Confirm(run.ID); err != nil {
		t.Fatalf("Confirm() error: %v", err)
	}
	got := waitRunStatus(t, store, run.ID, RunFailed)
	if got.Plan.Tasks[0].Status != TaskFailed || got.ErrorCode != "task_failed" {
		t.Fatalf("Run = %+v", got)
	}
	if !strings.Contains(got.ErrorMessage, "额度不足") {
		t.Errorf("失败说明 = %q", got.ErrorMessage)
	}
	if got.Summary == "" {
		t.Error("失败 Run 也应保留汇总")
	}
}

func TestManagerSummaryFallback(t *testing.T) {
	manager, store, _, chat, run := managerFixture(t, testTask("t-00000001"))
	chat.steps = []func(gateway.ChatRequest) (gateway.ChatResponse, error){
		taskFinal("完成"),
		failStep(errors.New("汇总网关不可用")),
	}
	if err := manager.Confirm(run.ID); err != nil {
		t.Fatalf("Confirm() error: %v", err)
	}
	got := waitRunStatus(t, store, run.ID, RunDone)
	if !strings.Contains(got.Summary, "完成") {
		t.Fatalf("兜底汇总 = %q", got.Summary)
	}
}

func TestManagerCancelRunningRun(t *testing.T) {
	manager, store, _, _, run := managerFixture(t, testTask("t-00000001"))
	chat := &blockingChat{done: make(chan struct{})}
	manager.Chat = chat.chat
	if err := manager.Confirm(run.ID); err != nil {
		t.Fatalf("Confirm() error: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := manager.Cancel(run.ID); err != nil {
		t.Fatalf("Cancel() error: %v", err)
	}
	got := waitRunStatus(t, store, run.ID, RunCanceled)
	if got.Plan.Tasks[0].Status != TaskCanceled {
		t.Fatalf("任务状态 = %s", got.Plan.Tasks[0].Status)
	}
	// Wait for the cancelled scheduler to finish writing before the test's
	// temporary directory is removed.
	manager.Shutdown()
}

func TestManagerShutdownInterruptsRunningRun(t *testing.T) {
	manager, store, _, _, run := managerFixture(t, testTask("t-00000001"))
	chat := &blockingChat{done: make(chan struct{})}
	manager.Chat = chat.chat
	if err := manager.Confirm(run.ID); err != nil {
		t.Fatalf("Confirm() error: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	manager.Shutdown()
	got := waitRunStatus(t, store, run.ID, RunInterrupted)
	if got.Plan.Tasks[0].Status != TaskInterrupted {
		t.Fatalf("任务状态 = %s, want %s", got.Plan.Tasks[0].Status, TaskInterrupted)
	}
}

type blockingChat struct {
	done chan struct{}
}

func (b *blockingChat) chat(ctx context.Context, _ gateway.ChatRequest) (gateway.ChatResponse, error) {
	<-ctx.Done()
	return gateway.ChatResponse{}, ctx.Err()
}

func TestManagerTokenBudgetStopsScheduling(t *testing.T) {
	tasks := []Task{testTask("t-00000001"), testTask("t-00000002")}
	manager, store, _, chat, run := managerFixture(t, tasks...)
	run.Budget.TokenBudget = 1
	run.Budget.MaxConcurrency = 1
	if err := store.SaveRun(run); err != nil {
		t.Fatal(err)
	}
	manager.Scheduler.Concurrency = 1
	chat.steps = []func(gateway.ChatRequest) (gateway.ChatResponse, error){
		reply(`{"action":"final","report":{"conclusion":"第一个任务完成"}}`),
		reply("汇总：预算不足"),
	}
	if err := manager.Confirm(run.ID); err != nil {
		t.Fatalf("Confirm() error: %v", err)
	}
	got := waitRunStatus(t, store, run.ID, RunFailed)
	if got.Plan.Tasks[0].Status != TaskDone {
		t.Fatalf("首个任务 = %s", got.Plan.Tasks[0].Status)
	}
	if got.Plan.Tasks[1].Status != TaskFailed || got.Plan.Tasks[1].ErrorCode != "token_budget" {
		t.Fatalf("第二个任务 = %+v", got.Plan.Tasks[1])
	}
}

func TestManagerPlanDelegates(t *testing.T) {
	manager, store, _, chat, _ := managerFixture(t)
	var created []Run
	var mu sync.Mutex
	manager.OnRunCreated = func(run Run) {
		mu.Lock()
		created = append(created, run)
		mu.Unlock()
	}
	chat.steps = []func(gateway.ChatRequest) (gateway.ChatResponse, error){
		reply(`{"action":"delegate","tasks":[{"goal":"整理资料","input":"来源 A","criteria":"三条结论","assignee_id":"a-00000001"},{"goal":"撰写摘要","assignee_id":"a-00000002","depends_on_indexes":[0]}]}`),
	}
	res, err := manager.Plan(context.Background(), PlanRequest{
		ConversationID: "c-00000001",
		Goal:           "整理资料并写摘要",
		ProviderID:     "p-00000001",
		Model:          "m",
	})
	if err != nil {
		t.Fatalf("Plan() error: %v", err)
	}
	if res.Run == nil || res.Run.Status != RunPendingConfirm || len(res.Run.Plan.Tasks) != 2 {
		t.Fatalf("Plan() = %+v", res)
	}
	if res.Run.Plan.Tasks[0].AssigneeName != "成员 a-00000001" {
		t.Fatalf("执行者名 = %q", res.Run.Plan.Tasks[0].AssigneeName)
	}
	if len(res.Run.Plan.Tasks[1].DependsOn) != 1 || res.Run.Plan.Tasks[1].DependsOn[0] != res.Run.Plan.Tasks[0].ID {
		t.Fatalf("依赖 = %+v", res.Run.Plan.Tasks[1].DependsOn)
	}
	if _, ok := store.GetRun(res.Run.ID); !ok {
		t.Fatal("Run 未保存")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(created) != 1 || created[0].ID != res.Run.ID {
		t.Fatalf("OnRunCreated = %+v", created)
	}
}

func TestManagerPlanAnswersDirectly(t *testing.T) {
	manager, store, _, chat, _ := managerFixture(t)
	chat.steps = []func(gateway.ChatRequest) (gateway.ChatResponse, error){
		reply(`{"action":"answer","content":"直接回答"}`),
	}
	res, err := manager.Plan(context.Background(), PlanRequest{ConversationID: "c-00000001", Goal: "你好"})
	if err != nil {
		t.Fatalf("Plan() error: %v", err)
	}
	if res.Run != nil || res.Answer != "直接回答" {
		t.Fatalf("Plan() = %+v", res)
	}
	if len(store.AllRuns()) != 1 {
		t.Fatalf("不应创建新 Run: %+v", store.AllRuns())
	}
}

func TestManagerPlanPlainTextAnswers(t *testing.T) {
	manager, _, _, chat, _ := managerFixture(t)
	chat.steps = []func(gateway.ChatRequest) (gateway.ChatResponse, error){reply("这不是 JSON")}
	res, err := manager.Plan(context.Background(), PlanRequest{ConversationID: "c-00000001", Goal: "解释一下"})
	if err != nil {
		t.Fatalf("Plan() error: %v", err)
	}
	if res.Answer != "这不是 JSON" {
		t.Fatalf("Answer = %q", res.Answer)
	}
}

func TestManagerPlanRejectsUnknownAssignee(t *testing.T) {
	manager, _, _, chat, _ := managerFixture(t)
	chat.steps = []func(gateway.ChatRequest) (gateway.ChatResponse, error){
		reply(`{"action":"delegate","tasks":[{"goal":"x","assignee_id":"a-00000009"}]}`),
	}
	_, err := manager.Plan(context.Background(), PlanRequest{ConversationID: "c-00000001", Goal: "x"})
	if err == nil || !strings.Contains(err.Error(), "不可用") {
		t.Fatalf("Plan() error = %v", err)
	}
}

func TestManagerPlanUsesGroupMembers(t *testing.T) {
	manager, store, agents, chat, _ := managerFixture(t)
	g := testGroup()
	if err := store.SaveGroup(g); err != nil {
		t.Fatal(err)
	}
	// a-00000002 被禁用，群 Run 只能看到 a-00000001。
	spec := agents.specs["a-00000002"]
	spec.Enabled = false
	agents.specs["a-00000002"] = spec
	chat.steps = []func(gateway.ChatRequest) (gateway.ChatResponse, error){
		reply(`{"action":"delegate","tasks":[{"goal":"x","assignee_id":"a-00000001"}]}`),
	}
	res, err := manager.Plan(context.Background(), PlanRequest{GroupID: g.ID, GroupName: g.Name, Goal: "群目标"})
	if err != nil {
		t.Fatalf("Plan() error: %v", err)
	}
	if res.Run == nil || res.Run.GroupID != g.ID {
		t.Fatalf("群 Run = %+v", res.Run)
	}
}

func TestManagerPlanRequiresSubAgent(t *testing.T) {
	manager, _, agents, _, _ := managerFixture(t)
	for _, id := range []string{"a-00000001", "a-00000002"} {
		spec := agents.specs[id]
		spec.Enabled = false
		agents.specs[id] = spec
	}
	_, err := manager.Plan(context.Background(), PlanRequest{ConversationID: "c-00000001", Goal: "x"})
	if err == nil || !strings.Contains(err.Error(), "没有可用") {
		t.Fatalf("Plan() error = %v", err)
	}
}

func TestManagerAbandonPendingRun(t *testing.T) {
	manager, store, _, _, run := managerFixture(t, testTask("t-00000001"))
	if err := manager.Abandon(run.ID); err != nil {
		t.Fatalf("Abandon() error: %v", err)
	}
	got := waitRunStatus(t, store, run.ID, RunCanceled)
	if got.Plan.Tasks[0].Status != TaskCanceled {
		t.Fatalf("任务状态 = %s", got.Plan.Tasks[0].Status)
	}
}

func TestSummaryPromptIncludesGroupMessages(t *testing.T) {
	run := testRun(t, testTask("t-00000001"))
	msgs := []Message{
		{Type: MessageProgress, FromID: "a-00000001", Content: "开始整理"},
		{Type: MessageRequest, FromID: "a-00000001", ToID: "a-00000002", Content: "补充来源"},
	}
	prompt := summaryPrompt(run, msgs)
	for _, want := range []string{"[进展]", "[请求]", "开始整理", "补充来源", "a-00000002"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("汇总提示缺少 %q: %s", want, prompt)
		}
	}
	if transcript := messageTranscript(nil); transcript != "" {
		t.Errorf("空消息记录 = %q", transcript)
	}
}

func TestManagerResumeInterrupted(t *testing.T) {
	manager, store, _, chat, run := managerFixture(t, testTask("t-00000001"))
	run.Status = RunInterrupted
	run.Plan.Tasks[0].Status = TaskInterrupted
	if err := store.SaveRun(run); err != nil {
		t.Fatal(err)
	}
	chat.steps = []func(gateway.ChatRequest) (gateway.ChatResponse, error){
		taskFinal("继续完成"),
		reply("汇总：继续完成"),
	}
	if err := manager.Resume(run.ID); err != nil {
		t.Fatalf("Resume() error: %v", err)
	}
	got := waitRunStatus(t, store, run.ID, RunDone)
	if got.Plan.Tasks[0].Status != TaskDone {
		t.Fatalf("任务状态 = %s", got.Plan.Tasks[0].Status)
	}
}
