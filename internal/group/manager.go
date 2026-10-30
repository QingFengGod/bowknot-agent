package group

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QingFengGod/bowknot-agent/internal/gateway"
)

// SummaryTimeout 限制一次主 Agent 汇总调用。
const (
	SummaryTimeout = 60 * time.Second
	summaryTokens  = 1600
	// summaryTranscriptRunes caps the group message excerpt in the summary
	// prompt; the full message log stays available through the store.
	summaryTranscriptRunes = 6000
	// shutdownWait bounds how long Shutdown waits for cancelled runs to unwind.
	shutdownWait = time.Second
)

// Manager 串联计划确认、依赖调度、子 Agent 执行、汇报收集与主 Agent 汇总。
// 所有状态变化都会保存到 Store，并通过回调交给界面；界面未接入时回调可空。
type Manager struct {
	Store     *Store
	Agents    AgentProvider
	Chat      ChatFunc
	Tools     Toolbox
	Scheduler *Scheduler
	Now       func() time.Time

	// OnRunCreated 在自动派发创建待确认 Run 后调用。
	OnRunCreated func(Run)
	// OnRunUpdate 在 Run 状态变化后调用。
	OnRunUpdate func(Run)
	// OnAssistantReply 把主 Agent 的直接回答或 Run 汇总写回会话。
	OnAssistantReply func(conversationID, runID, text string)

	mu       sync.Mutex
	sessions map[string]*session
	buses    map[string]*Bus
	wg       sync.WaitGroup
}

type session struct {
	cancel    context.CancelFunc
	scheduler *Scheduler
}

// NewManager 返回一个 Run 管理器。
func NewManager(store *Store, agents AgentProvider, chat ChatFunc) *Manager {
	return &Manager{
		Store:     store,
		Agents:    agents,
		Chat:      chat,
		Scheduler: NewScheduler(),
		Now:       time.Now,
		sessions:  map[string]*session{},
		buses:     map[string]*Bus{},
	}
}

// Shutdown 取消全部执行，把执行中的 Run 标记为已中断并关闭总线。进程退出
// 前调用，重启后用户可以选择继续或终止（FR-AGT-07）。
func (m *Manager) Shutdown() {
	m.mu.Lock()
	sessions := m.sessions
	buses := m.buses
	m.sessions = map[string]*session{}
	m.buses = map[string]*Bus{}
	m.mu.Unlock()
	for _, sess := range sessions {
		sess.scheduler.Interrupt()
		sess.cancel()
	}
	for _, bus := range buses {
		bus.Close()
	}
	m.waitRuns()
	now := m.now()
	for _, summary := range m.Store.AllRuns() {
		run, ok := m.Store.GetRun(summary.ID)
		if ok && run.MarkInterrupted(now) {
			_ = m.Store.SaveRun(&run)
		}
	}
}

// waitRuns waits for cancelled executions to unwind before the caller marks
// them interrupted, so that a late scheduler write cannot overwrite the state.
func (m *Manager) waitRuns() {
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()
	timer := time.NewTimer(shutdownWait)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
}

// Bus 返回 Run 的消息总线，首次调用时创建。总线随 Run 生命周期保留，供
// 界面读取与订阅。
func (m *Manager) Bus(runID string) (*Bus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if b, ok := m.buses[runID]; ok {
		return b, nil
	}
	b, err := NewBus(m.Store, runID)
	if err != nil {
		return nil, err
	}
	m.buses[runID] = b
	return b, nil
}

// Messages 返回 Run 的消息快照。
func (m *Manager) Messages(runID string) []Message { return m.Store.Messages(runID) }

// Confirm 确认待确认的 Run 并异步执行。只有确认后的 Run 才会调用模型。
func (m *Manager) Confirm(runID string) error {
	run, ok := m.Store.GetRun(runID)
	if !ok {
		return ErrRunNotFound
	}
	if !run.Confirm(m.now()) {
		return fmt.Errorf("group: Run %s 当前不是待确认状态", runID)
	}
	if err := m.Store.SaveRun(&run); err != nil {
		return err
	}
	m.notify(run)
	m.start(runID, run)
	return nil
}

// start 为一次执行建立可取消的上下文与独立调度器。
func (m *Manager) start(runID string, run Run) {
	ctx, cancel := context.WithCancel(context.Background())
	sched := m.schedulerFor(&run)
	m.mu.Lock()
	if old, exists := m.sessions[runID]; exists {
		old.cancel()
	}
	m.sessions[runID] = &session{cancel: cancel, scheduler: sched}
	m.mu.Unlock()

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer cancel()
		m.execute(ctx, runID, run, sched)
	}()
}

// Cancel 取消执行中的 Run；已确认且未结束的 Run 才处理。消息与产出保留。
func (m *Manager) Cancel(runID string) error {
	run, ok := m.Store.GetRun(runID)
	if !ok {
		return ErrRunNotFound
	}
	m.mu.Lock()
	sess := m.sessions[runID]
	m.mu.Unlock()
	if sess != nil {
		// 先取消上下文，调度器尚未启动时也能生效，再取消当前调度。
		sess.cancel()
		sess.scheduler.Cancel()
	}
	if !run.Cancel(m.now()) {
		return fmt.Errorf("group: Run %s 当前状态不可取消", runID)
	}
	if err := m.Store.SaveRun(&run); err != nil {
		return err
	}
	if b, err := m.Bus(runID); err == nil {
		b.Close()
	}
	m.notify(run)
	return nil
}

// Resume 继续已中断的 Run。旧执行上下文不再使用，重新调度。
func (m *Manager) Resume(runID string) error {
	run, ok := m.Store.GetRun(runID)
	if !ok {
		return ErrRunNotFound
	}
	if !run.Resume(m.now()) {
		return fmt.Errorf("group: Run %s 当前状态不可继续", runID)
	}
	if err := m.Store.SaveRun(&run); err != nil {
		return err
	}
	m.notify(run)
	m.start(runID, run)
	return nil
}

// Terminate 终止已中断的 Run。
func (m *Manager) Terminate(runID string) error {
	run, ok := m.Store.GetRun(runID)
	if !ok {
		return ErrRunNotFound
	}
	if !run.Terminate(m.now()) {
		return fmt.Errorf("group: Run %s 当前状态不可终止", runID)
	}
	if err := m.Store.SaveRun(&run); err != nil {
		return err
	}
	m.notify(run)
	return nil
}

// Abandon 放弃待确认的 Run。
func (m *Manager) Abandon(runID string) error {
	run, ok := m.Store.GetRun(runID)
	if !ok {
		return ErrRunNotFound
	}
	if !run.Abandon(m.now()) {
		return fmt.Errorf("group: Run %s 当前状态不可放弃", runID)
	}
	if err := m.Store.SaveRun(&run); err != nil {
		return err
	}
	m.notify(run)
	return nil
}

func (m *Manager) execute(ctx context.Context, runID string, run Run, sched *Scheduler) {
	bus, err := m.Bus(runID)
	if err != nil {
		m.failRun(runID, run, "internal", err.Error())
		return
	}
	executor := &Executor{
		Store:       m.Store,
		Bus:         bus,
		Chat:        m.Chat,
		Agents:      m.Agents,
		Tools:       m.Tools,
		WaitTimeout: run.Budget.WaitTimeout(),
	}
	var used int64
	exec := func(ctx context.Context, t *Task) Outcome {
		if run.Budget.TokenBudget > 0 && atomic.LoadInt64(&used) >= int64(run.Budget.TokenBudget) {
			return Outcome{ErrorCode: "token_budget", ErrorMessage: "已达 token 预算，协作已暂停，请用户决定继续或终止"}
		}
		out := executor.RunTask(ctx, &run, t)
		atomic.AddInt64(&used, int64(out.InputTokens+out.OutputTokens))
		return out
	}

	snapshot := run.Clone()
	sched.Run(ctx, &snapshot, exec, func(next Run) {
		_ = m.Store.SaveRun(&next)
		m.notify(next)
	})

	m.mu.Lock()
	delete(m.sessions, runID)
	m.mu.Unlock()

	switch snapshot.Status {
	case RunCanceled, RunInterrupted:
		_ = m.Store.SaveRun(&snapshot)
		m.notify(snapshot)
		bus.Close()
	default:
		m.summarize(ctx, &snapshot, bus)
	}
}

// summarize 在主 Agent 汇总任务汇报与失败说明，然后把 Run 置为完成或失败。
func (m *Manager) summarize(ctx context.Context, run *Run, bus *Bus) {
	now := m.now()
	run.Status = RunSummarizing
	run.UpdatedAt = now.Unix()
	_ = m.Store.SaveRun(run)
	m.notify(*run)

	summary, err := m.summaryText(ctx, run)
	if ctx != nil && ctx.Err() != nil {
		// 进程退出打断了汇总：保留可恢复状态，不写入完成或失败。
		run.MarkInterrupted(m.now())
		_ = m.Store.SaveRun(run)
		m.notify(*run)
		bus.Close()
		return
	}
	if err != nil {
		log.Printf("group: 主 Agent 汇总失败，使用结构化兜底: %v", err)
	}
	failed := false
	for i := range run.Plan.Tasks {
		if run.Plan.Tasks[i].Status == TaskFailed {
			failed = true
			break
		}
	}
	if failed {
		code := "task_failed"
		message := failedMessage(run)
		run.Summary = summary
		run.Fail(now, code, message)
	} else if !run.Finish(now, summary) {
		run.Summary = summary
		run.Status = RunDone
		run.UpdatedAt = now.Unix()
	}
	_ = m.Store.SaveRun(run)
	m.notify(*run)
	bus.Close()
	if m.OnAssistantReply != nil && run.ConversationID != "" {
		m.OnAssistantReply(run.ConversationID, run.ID, run.Summary)
	}
}

func (m *Manager) summaryText(parent context.Context, run *Run) (string, error) {
	if m.Chat == nil || m.Agents == nil {
		return fallbackSummary(run), nil
	}
	spec, ok := m.Agents.Agent(MainAgentID)
	if !ok || !spec.Enabled {
		return fallbackSummary(run), nil
	}
	persona, _ := m.Agents.Prompt(MainAgentID, run.Goal)
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, SummaryTimeout)
	defer cancel()
	resp, err := m.Chat(ctx, gateway.ChatRequest{
		ProviderID: spec.ProviderID,
		Model:      spec.Model,
		Messages: []gateway.ChatMessage{
			{Role: "system", Content: persona + "\n\n你是群主，负责汇总群聊 Run 的最终结果。只输出汇总正文，不要编造未出现的事实。"},
			{Role: "user", Content: summaryPrompt(run, m.Store.Messages(run.ID))},
		},
		MaxTokens: summaryTokens,
	})
	if err != nil {
		return fallbackSummary(run), err
	}
	text := clipText(strings.TrimSpace(resp.Content), MaxSummaryRunes)
	if text == "" {
		return fallbackSummary(run), errors.New("汇总为空")
	}
	return text, nil
}

func summaryPrompt(run *Run, messages []Message) string {
	var b strings.Builder
	b.WriteString("Run 目标：")
	b.WriteString(run.Goal)
	b.WriteString("\n\n任务与汇报：\n")
	for i := range run.Plan.Tasks {
		t := &run.Plan.Tasks[i]
		b.WriteString(fmt.Sprintf("- %s（%s，%s）", t.Goal, t.AssigneeName, t.Status.Text()))
		switch {
		case t.Report != nil:
			b.WriteString("：")
			b.WriteString(t.Report.Conclusion)
			if len(t.Report.Artifacts) > 0 {
				b.WriteString("；产出：")
				b.WriteString(strings.Join(t.Report.Artifacts, "、"))
			}
			if len(t.Report.OpenIssues) > 0 {
				b.WriteString("；未解决：")
				b.WriteString(strings.Join(t.Report.OpenIssues, "、"))
			}
		case t.ErrorMessage != "":
			b.WriteString("：失败，")
			b.WriteString(t.ErrorMessage)
		}
		b.WriteString("。\n")
	}
	if transcript := messageTranscript(messages); transcript != "" {
		b.WriteString("\n群消息记录：\n")
		b.WriteString(transcript)
	}
	b.WriteString("\n请输出 4000 字以内的整体汇总，先给结论，再列产出与未解决事项。")
	return b.String()
}

// messageTranscript renders the run message log for the summary, bounded so a
// large run cannot overflow the prompt. Every message is visible to the main
// agent through the store; this is the excerpt it summarizes from.
func messageTranscript(messages []Message) string {
	var b strings.Builder
	used := 0
	for _, m := range messages {
		var line string
		if m.ToID != "" {
			line = fmt.Sprintf("- [%s] %s → %s：%s", m.Type.Text(), m.FromID, m.ToID, m.Content)
		} else {
			line = fmt.Sprintf("- [%s] %s：%s", m.Type.Text(), m.FromID, m.Content)
		}
		line = clipText(line, MaxMessageRunes) + "\n"
		if used+len([]rune(line)) > summaryTranscriptRunes {
			b.WriteString("（消息较多，后面省略）\n")
			break
		}
		b.WriteString(line)
		used += len([]rune(line))
	}
	return b.String()
}

func fallbackSummary(run *Run) string {
	var b strings.Builder
	b.WriteString("Run 结束：")
	b.WriteString(run.Goal)
	b.WriteString("\n\n")
	for i := range run.Plan.Tasks {
		t := &run.Plan.Tasks[i]
		b.WriteString(fmt.Sprintf("· %s（%s，%s）", t.Goal, t.AssigneeName, t.Status.Text()))
		if t.Report != nil && strings.TrimSpace(t.Report.Conclusion) != "" {
			b.WriteString("：")
			b.WriteString(t.Report.Conclusion)
		}
		if t.ErrorMessage != "" {
			b.WriteString("：")
			b.WriteString(t.ErrorMessage)
		}
		b.WriteString("\n")
	}
	if run.Status == RunFailed {
		b.WriteString("\n有任务失败，详见失败说明。")
	}
	return clipText(b.String(), MaxSummaryRunes)
}

func failedMessage(run *Run) string {
	var parts []string
	for i := range run.Plan.Tasks {
		t := &run.Plan.Tasks[i]
		if t.Status != TaskFailed {
			continue
		}
		if t.ErrorMessage != "" {
			parts = append(parts, t.Goal+"："+t.ErrorMessage)
		} else {
			parts = append(parts, t.Goal+"：执行失败")
		}
	}
	if len(parts) == 0 {
		return "部分任务未能完成"
	}
	return clipText(strings.Join(parts, "；"), MaxSummaryRunes)
}

func (m *Manager) failRun(runID string, run Run, code, message string) {
	if run.Fail(m.now(), code, message) {
		_ = m.Store.SaveRun(&run)
	}
	m.notify(run)
}

func (m *Manager) schedulerFor(run *Run) *Scheduler {
	s := &Scheduler{
		Concurrency: run.Budget.Concurrency(),
		WaitTimeout: run.Budget.WaitTimeout(),
		Now:         m.Now,
	}
	if m.Scheduler != nil && m.Scheduler.Concurrency > 0 {
		s.Concurrency = m.Scheduler.Concurrency
	}
	return s
}

func (m *Manager) notify(run Run) {
	if m.OnRunUpdate != nil {
		m.OnRunUpdate(run.Clone())
	}
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}
