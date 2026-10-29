package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/QingFengGod/bowknot-agent/internal/gateway"
	"github.com/QingFengGod/bowknot-agent/internal/task"
)

// Dispatcher errors.
var (
	ErrGroupNotFound = errors.New("任务组不存在")
	ErrGroupBusy     = errors.New("已有任务组在执行")
	ErrNotRunnable   = errors.New("该任务计划当前不可执行")
	ErrTaskNotFound  = errors.New("任务不存在")
)

const (
	// planMaxTokens bounds the planning and report calls.
	planMaxTokens = 1200
	// planTemperature keeps the structured calls deterministic.
	planTemperature = 0.2
	// unstructuredConclusionRunes caps a report that was not valid JSON.
	unstructuredConclusionRunes = 4000
)

const planRules = "你同时负责任务规划。请把用户的目标拆成 1–5 个可独立执行的子任务，并派给下面的子 Agent。" +
	"只输出 JSON，不要解释、不要 Markdown 代码块，格式为：" +
	`{"tasks":[{"goal":"一句话目标","input":"交给子 Agent 的完整输入","criteria":"完成标准","assignee":"子 Agent 的 ID"}]}。` +
	"assignee 必须严格取自候选列表中的 ID；无法合理拆解时输出 {\"tasks\":[]}。"

const reportRules = "你是执行子任务的助手。只输出 JSON，不要解释、不要 Markdown 代码块，格式为：" +
	`{"conclusion":"结论","artifacts":["产出物名称"],"open_issues":["未解决的问题"],"summary":"过程摘要"}。` +
	"artifacts 与 open_issues 没有内容时输出空数组；只能输出文本结论，不能调用工具。"

const summaryRules = "你是主 Agent，正在汇总一个任务计划的执行结果。" +
	"请用中文给出简明汇总：整体结论、关键产出、未解决的问题；如有失败任务，说明失败原因与影响。" +
	"不要输出 JSON，不要重复罗列每条任务的全文。"

// Dispatcher plans, runs and summarizes task groups. It owns the single
// dispatch slot: only one plan or execution runs at a time.
type Dispatcher struct {
	Chatter Chatter
	Convs   *Store
	Tasks   *task.Store
	Agents  *Registry
	Runner  *Runner

	// Dispatch marshals a UI update to the main thread; nil runs inline.
	Dispatch func(func())
	// Repaint asks the window to redraw; nil does nothing.
	Repaint func()
	// Notice reports a local failure to the page; it runs on the main thread.
	Notice func(string)

	mu         sync.Mutex
	run        *dispatchRun
	waitCh     chan struct{}
	planActive bool
	planCancel context.CancelFunc
}

type dispatchRun struct {
	groupID   string
	cancel    context.CancelFunc
	scheduler *task.Scheduler
}

// NewDispatcher wires a dispatcher over the conversation store, the task
// store and the agent registry.
func NewDispatcher(chatter Chatter, convs *Store, tasks *task.Store, agents *Registry, runner *Runner) *Dispatcher {
	return &Dispatcher{Chatter: chatter, Convs: convs, Tasks: tasks, Agents: agents, Runner: runner}
}

// Busy reports whether a plan or an execution is running.
func (d *Dispatcher) Busy() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.run != nil || d.planActive
}

// RunningGroup returns the group being executed, or "".
func (d *Dispatcher) RunningGroup() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.run == nil {
		return ""
	}
	return d.run.groupID
}

// AvailableAgents returns the enabled sub agents whose model is still a chat
// route target. These are the planning candidates.
func (d *Dispatcher) AvailableAgents() []Agent {
	if d.Agents == nil || d.Chatter == nil {
		return nil
	}
	targets := d.Chatter.ChatTargets()
	out := make([]Agent, 0, len(targets))
	for _, a := range d.Agents.List() {
		if a.IsMain() {
			continue
		}
		if a.Enabled && targetAvailable(targets, a.ProviderID, a.Model) {
			out = append(out, a)
		}
	}
	return out
}

// AgentUsable reports whether one sub agent can be dispatched now.
func (d *Dispatcher) AgentUsable(a Agent) bool {
	if !a.Enabled || d.Chatter == nil {
		return false
	}
	return targetAvailable(d.Chatter.ChatTargets(), a.ProviderID, a.Model)
}

func targetAvailable(targets []gateway.TargetView, providerID, model string) bool {
	for _, t := range targets {
		if t.ProviderID == providerID && t.Model == model {
			return true
		}
	}
	return false
}

// PendingGroup returns the group of a conversation still awaiting
// confirmation, or false.
func (d *Dispatcher) PendingGroup(conversationID string) (task.Group, bool) {
	for _, s := range d.Tasks.List() {
		if s.ConversationID != conversationID || s.Status != task.GroupPending {
			continue
		}
		if g, ok := d.Tasks.Get(s.ID); ok {
			return g, true
		}
	}
	return task.Group{}, false
}

// Plan reserves the dispatch slot and plans in the background. It returns an
// immediate error when the slot is taken or the conversation is unknown; the
// planning outcome reaches the page through the store and Notice.
func (d *Dispatcher) Plan(ctx context.Context, conversationID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	conv, ok := d.Convs.Get(conversationID)
	if !ok {
		return ErrConversationNotFound
	}
	userText := lastUserText(conv)
	if userText == "" {
		return errors.New("没有可拆解的用户消息")
	}
	providerID, model := d.Runner.Target()
	if providerID == "" || model == "" {
		return errors.New("模型未配置，无法规划任务")
	}
	if !d.Runner.ReserveExternal() {
		return ErrBusy
	}

	planCtx, cancel := context.WithCancel(ctx)
	d.mu.Lock()
	d.planActive = true
	d.planCancel = cancel
	done := d.beginWaitLocked()
	d.mu.Unlock()

	go func() {
		defer func() {
			cancel()
			d.mu.Lock()
			d.planActive = false
			d.planCancel = nil
			d.endWaitLocked(done)
			d.mu.Unlock()
			d.Runner.ReleaseExternal()
			d.repaint()
		}()
		d.plan(planCtx, conversationID, userText, providerID, model)
	}()
	return nil
}

// Shutdown stops planning and the running group before the app quits. The
// interrupted state is persisted, so the next start shows it as interrupted.
func (d *Dispatcher) Shutdown() {
	d.mu.Lock()
	run := d.run
	planCancel := d.planCancel
	d.mu.Unlock()
	if planCancel != nil {
		planCancel()
	}
	if run != nil {
		run.cancel()
	}
}

func (d *Dispatcher) plan(ctx context.Context, conversationID, userText, providerID, model string) {
	planCtx, cancel := context.WithTimeout(ctx, task.PlanTimeout)
	defer cancel()

	candidates := d.AvailableAgents()
	prompt := d.planningPrompt(userText, candidates)
	msgs := []gateway.ChatMessage{
		{Role: "system", Content: d.promptOf(MainAgentID) + "\n\n" + planRules},
		{Role: "user", Content: prompt},
	}

	resp, err := d.chatOnce(planCtx, conversationID, "", providerID, model, msgs)
	if err != nil {
		if planCtx.Err() == nil {
			d.planFailed(conversationID, gateway.KindOf(err), err.Error())
		}
		return
	}
	items, parseErr := parsePlan(resp.Content, candidates)
	if parseErr != nil {
		msgs = append(msgs,
			gateway.ChatMessage{Role: "assistant", Content: resp.Content},
			gateway.ChatMessage{Role: "user", Content: "上次输出不是合法 JSON 或字段不合规，请只输出 JSON。"})
		resp, err = d.chatOnce(planCtx, conversationID, "", providerID, model, msgs)
		if err != nil {
			if planCtx.Err() == nil {
				d.planFailed(conversationID, gateway.KindOf(err), err.Error())
			}
			return
		}
		items, parseErr = parsePlan(resp.Content, candidates)
	}
	if parseErr != nil {
		if planCtx.Err() == nil {
			d.planFailed(conversationID, gateway.KindModel, "模型没有返回可用的任务计划")
		}
		return
	}
	if planCtx.Err() != nil {
		return
	}
	d.createGroup(conversationID, providerID, model, items, candidates)
}

// planFailed records why planning fell back instead of creating a card.
func (d *Dispatcher) planFailed(conversationID string, kind gateway.ErrorKind, detail string) {
	msg := Message{
		Role:    RoleAssistant,
		Content: "无法拆解任务，已按普通回答处理。",
		Status:  StatusError,
		// A structured parse failure is a model output problem.
		ErrorCode:    string(kind),
		ErrorMessage: detail,
		At:           time.Now().Unix(),
	}
	if _, err := d.Convs.Append(conversationID, msg); err != nil {
		d.notice(err.Error())
		return
	}
	if err := d.Convs.Save(conversationID); err != nil {
		d.notice("会话保存失败：" + err.Error())
	}
	d.repaint()
}

// createGroup stores the confirmed plan and appends its task card message.
func (d *Dispatcher) createGroup(conversationID, providerID, model string, items []planTask, candidates []Agent) {
	now := time.Now()
	// Refuse before appending the card, otherwise the conversation would keep
	// a card that points at no stored group.
	if d.Tasks.Count() >= task.MaxGroups {
		msg := Message{
			Role:         RoleAssistant,
			Content:      "任务记录已达上限，请先在任务中心清理已结束的任务，再重新派发。",
			Status:       StatusError,
			ErrorCode:    string(gateway.KindInternal),
			ErrorMessage: task.ErrGroupLimit.Error(),
			At:           now.Unix(),
		}
		if _, err := d.Convs.Append(conversationID, msg); err != nil {
			d.notice(err.Error())
			return
		}
		if err := d.Convs.Save(conversationID); err != nil {
			d.notice("会话保存失败：" + err.Error())
		}
		d.repaint()
		return
	}
	g := task.NewGroup(now, conversationID, 0, providerID, model)
	byID := map[string]Agent{}
	for _, a := range candidates {
		byID[a.ID] = a
	}
	for _, item := range items {
		a := byID[item.Assignee]
		g.Tasks = append(g.Tasks, task.NewTask(now, item.Goal, item.Input, item.Criteria, a.ID, a.Name))
	}

	msg := Message{Role: RoleAssistant, Status: StatusOK, TaskGroupID: g.ID, At: now.Unix()}
	index, err := d.Convs.Append(conversationID, msg)
	if err != nil {
		d.notice(err.Error())
		return
	}
	g.MessageIndex = index
	if err := d.Tasks.Save(g); err != nil {
		d.notice("任务保存失败：" + err.Error())
		return
	}
	if err := d.Convs.Save(conversationID); err != nil {
		d.notice("会话保存失败：" + err.Error())
	}
	d.repaint()
}

// Confirm starts a pending group. The slot is reserved before the state
// changes, so a concurrent send cannot slip in.
func (d *Dispatcher) Confirm(groupID string) error {
	g, ok := d.Tasks.Get(groupID)
	if !ok {
		return ErrGroupNotFound
	}
	if g.Status != task.GroupPending {
		return ErrNotRunnable
	}
	if !d.Runner.ReserveExternal() {
		return ErrBusy
	}
	if err := d.startReserved(&g); err != nil {
		d.Runner.ReleaseExternal()
		return err
	}
	return nil
}

// Retry queues a new task for a failed one and starts the group again.
func (d *Dispatcher) Retry(groupID, taskID string) error {
	if !d.Runner.ReserveExternal() {
		return ErrBusy
	}
	g, ok := d.Tasks.Get(groupID)
	if !ok {
		d.Runner.ReleaseExternal()
		return ErrGroupNotFound
	}
	orig, ok := g.Task(taskID)
	if !ok {
		d.Runner.ReleaseExternal()
		return ErrTaskNotFound
	}
	if orig.Status != task.TaskFailed {
		d.Runner.ReleaseExternal()
		return errors.New("只有失败的任务可以重试")
	}
	if len(g.Tasks) >= task.MaxTasksPerGroup {
		d.Runner.ReleaseExternal()
		return fmt.Errorf("任务数已达上限 %d，无法继续重试", task.MaxTasksPerGroup)
	}
	now := time.Now()
	retry := task.NewTask(now, orig.Goal, orig.Input, orig.Criteria, orig.AssigneeID, orig.AssigneeName)
	retry.RetryOf = orig.ID
	g.Tasks = append(g.Tasks, retry)
	g.Summary = ""
	if err := d.startReserved(&g); err != nil {
		d.Runner.ReleaseExternal()
		return err
	}
	return nil
}

// Resume reopens an interrupted group and runs its unfinished tasks again.
// Done tasks keep their outcome; the summary is rebuilt when the group settles.
func (d *Dispatcher) Resume(groupID string) error {
	if !d.Runner.ReserveExternal() {
		return ErrBusy
	}
	g, ok := d.Tasks.Get(groupID)
	if !ok {
		d.Runner.ReleaseExternal()
		return ErrGroupNotFound
	}
	if g.Status != task.GroupInterrupted {
		d.Runner.ReleaseExternal()
		return errors.New("只有已中断的任务计划可以继续")
	}
	if !g.Resume(time.Now()) {
		d.Runner.ReleaseExternal()
		return ErrNotRunnable
	}
	if err := d.startReserved(&g); err != nil {
		d.Runner.ReleaseExternal()
		return err
	}
	return nil
}

// Terminate closes an interrupted group without running the remaining tasks.
func (d *Dispatcher) Terminate(groupID string) error {
	g, ok := d.Tasks.Get(groupID)
	if !ok {
		return ErrGroupNotFound
	}
	if !g.Terminate(time.Now()) {
		return errors.New("只有已中断的任务计划可以终止")
	}
	if err := d.Tasks.Save(&g); err != nil {
		return fmt.Errorf("任务保存失败：%w", err)
	}
	d.repaint()
	return nil
}

// Delete removes a settled group from the task center. A group that is still
// pending, running or summarizing must be abandoned first.
func (d *Dispatcher) Delete(groupID string) error {
	g, ok := d.Tasks.Get(groupID)
	if !ok {
		return ErrGroupNotFound
	}
	if !g.Status.Terminal() {
		return errors.New("任务计划尚未结束，请先放弃或取消")
	}
	if d.RunningGroup() == groupID {
		return ErrGroupBusy
	}
	if err := d.Tasks.Delete(groupID); err != nil {
		return fmt.Errorf("任务删除失败：%w", err)
	}
	d.repaint()
	return nil
}

// CleanTerminal removes every settled group and reports how many were removed.
// It refuses while a plan or execution is running, so no card disappears
// underneath the conversation view.
func (d *Dispatcher) CleanTerminal() (int, error) {
	if d.Busy() {
		return 0, ErrBusy
	}
	removed, err := d.Tasks.DeleteTerminal()
	d.repaint()
	return removed, err
}

// startReserved marks the group running, saves it and launches execution. The
// caller holds the dispatch slot.
func (d *Dispatcher) startReserved(g *task.Group) error {
	now := time.Now()
	g.Status = task.GroupRunning
	g.UpdatedAt = now.Unix()
	if err := d.Tasks.Save(g); err != nil {
		return fmt.Errorf("任务保存失败：%w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	run := &dispatchRun{groupID: g.ID, cancel: cancel, scheduler: task.NewScheduler()}
	d.mu.Lock()
	d.run = run
	done := d.beginWaitLocked()
	d.mu.Unlock()
	d.repaint()

	go d.execute(ctx, run, done)
	return nil
}

func (d *Dispatcher) execute(ctx context.Context, run *dispatchRun, done chan struct{}) {
	defer func() {
		d.mu.Lock()
		if d.run == run {
			d.run = nil
		}
		d.endWaitLocked(done)
		d.mu.Unlock()
		run.cancel()
		d.Runner.ReleaseExternal()
		d.repaint()
	}()

	g, ok := d.Tasks.Get(run.groupID)
	if !ok {
		return
	}
	g.Status = task.GroupRunning
	run.scheduler.Run(ctx, &g, func(ctx context.Context, t *task.Task) task.Outcome {
		return d.runTask(ctx, &g, t)
	}, func(snapshot task.Group) {
		d.saveGroup(&snapshot)
		d.repaint()
	})

	g, ok = d.Tasks.Get(run.groupID)
	if !ok {
		return
	}
	if g.Status == task.GroupCanceled || ctx.Err() != nil {
		if g.Status != task.GroupCanceled {
			g.Status = task.GroupCanceled
			g.UpdatedAt = time.Now().Unix()
			d.saveGroup(&g)
		}
		d.repaint()
		return
	}

	g.Status = task.GroupSummarizing
	g.UpdatedAt = time.Now().Unix()
	d.saveGroup(&g)
	d.repaint()
	d.summarize(ctx, &g)
}

// runTask resolves the assignee and turns its structured report into a task
// outcome. Each task may spend one retry when its output is not valid JSON.
func (d *Dispatcher) runTask(ctx context.Context, g *task.Group, t *task.Task) task.Outcome {
	if d.Agents == nil {
		return task.Outcome{ErrorCode: string(gateway.KindModel), ErrorMessage: "子 Agent 不存在，无法执行"}
	}
	a, ok := d.Agents.Get(t.AssigneeID)
	if !ok || a.IsMain() {
		return task.Outcome{ErrorCode: string(gateway.KindModel), ErrorMessage: "子 Agent 已删除，无法执行"}
	}
	if !a.Enabled {
		return task.Outcome{ErrorCode: string(gateway.KindModel), ErrorMessage: "子 Agent 已停用，无法执行"}
	}
	if d.Chatter == nil || !targetAvailable(d.Chatter.ChatTargets(), a.ProviderID, a.Model) {
		return task.Outcome{ErrorCode: string(gateway.KindModel), ErrorMessage: "子 Agent 的模型不可用，请重新配置"}
	}

	msgs := []gateway.ChatMessage{
		{Role: "system", Content: d.promptOf(a.ID) + "\n\n" + reportRules},
		{Role: "user", Content: taskPrompt(t)},
	}
	resp, err := d.chatOnce(ctx, g.ConversationID, t.ID, a.ProviderID, a.Model, msgs)
	if err != nil {
		return failureOutcome(err)
	}
	if strings.TrimSpace(resp.Content) == "" {
		return task.Outcome{ErrorCode: string(gateway.KindModel), ErrorMessage: "子 Agent 没有返回内容"}
	}
	report, parseErr := parseReport(resp.Content)
	if parseErr != nil {
		msgs = append(msgs,
			gateway.ChatMessage{Role: "assistant", Content: resp.Content},
			gateway.ChatMessage{Role: "user", Content: "上次输出不是合法 JSON，请只输出 JSON。"})
		retryResp, retryErr := d.chatOnce(ctx, g.ConversationID, t.ID, a.ProviderID, a.Model, msgs)
		if retryErr != nil {
			return failureOutcome(retryErr)
		}
		if strings.TrimSpace(retryResp.Content) == "" {
			return task.Outcome{ErrorCode: string(gateway.KindModel), ErrorMessage: "子 Agent 没有返回内容"}
		}
		report, parseErr = parseReport(retryResp.Content)
		if parseErr != nil {
			report = fallbackReport(retryResp.Content)
		}
		resp = retryResp
	}
	out := task.Outcome{
		Report:         &report,
		InputTokens:    resp.InputTokens,
		OutputTokens:   resp.OutputTokens,
		UsageEstimated: !resp.HasUsage,
		DurationMS:     resp.Duration.Milliseconds(),
	}
	return out
}

func failureOutcome(err error) task.Outcome {
	return task.Outcome{
		Err:          err,
		ErrorCode:    string(gateway.KindOf(err)),
		ErrorMessage: err.Error(),
	}
}

// summarize appends one assistant summary after the tasks settle.
func (d *Dispatcher) summarize(ctx context.Context, g *task.Group) {
	sumCtx, cancel := context.WithTimeout(ctx, task.SummaryTimeout)
	defer cancel()

	msgs := []gateway.ChatMessage{
		{Role: "system", Content: d.promptOf(MainAgentID) + "\n\n" + summaryRules},
		{Role: "user", Content: summaryPrompt(g)},
	}
	resp, err := d.chatOnce(sumCtx, g.ConversationID, g.ID, g.ProviderID, g.Model, msgs)
	if err != nil {
		if ctx.Err() != nil {
			g.Status = task.GroupCanceled
			g.UpdatedAt = time.Now().Unix()
			d.saveGroup(g)
			return
		}
		g.Status = task.GroupFailed
		g.UpdatedAt = time.Now().Unix()
		g.Summary = clipSummary("汇总失败：" + err.Error())
		d.saveGroup(g)
		msg := Message{
			Role:         RoleAssistant,
			Status:       StatusError,
			ErrorCode:    string(gateway.KindOf(err)),
			ErrorMessage: "汇总失败：" + err.Error(),
			At:           time.Now().Unix(),
		}
		d.appendSummary(g.ConversationID, msg)
		return
	}

	msg := Message{
		Role:           RoleAssistant,
		Content:        strings.TrimSpace(resp.Content),
		Model:          resp.Model,
		InputTokens:    resp.InputTokens,
		OutputTokens:   resp.OutputTokens,
		UsageEstimated: !resp.HasUsage,
		DurationMS:     resp.Duration.Milliseconds(),
		Status:         StatusOK,
		At:             time.Now().Unix(),
	}
	if msg.Content == "" {
		msg.Content = "任务已完成，但汇总没有返回内容。"
	}
	g.Status = task.GroupDone
	g.UpdatedAt = time.Now().Unix()
	g.Summary = clipSummary(msg.Content)
	d.saveGroup(g)
	d.appendSummary(g.ConversationID, msg)
}

// clipSummary keeps a summary inside the schema limit so the group still
// validates and saves.
func clipSummary(text string) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) > task.MaxSummaryRunes {
		return string(runes[:task.MaxSummaryRunes])
	}
	return text
}

func (d *Dispatcher) appendSummary(conversationID string, msg Message) {
	if _, err := d.Convs.Append(conversationID, msg); err != nil {
		d.notice(err.Error())
		return
	}
	if err := d.Convs.Save(conversationID); err != nil {
		d.notice("会话保存失败：" + err.Error())
	}
	d.repaint()
}

func (d *Dispatcher) saveGroup(g *task.Group) {
	if err := d.Tasks.Save(g); err != nil {
		log.Printf("agent: save task group %s: %v", g.ID, err)
		d.notice("任务保存失败：" + err.Error())
	}
}

// Cancel abandons a pending group or cancels the running one.
func (d *Dispatcher) Cancel(groupID string) {
	d.mu.Lock()
	run := d.run
	d.mu.Unlock()
	if run != nil && run.groupID == groupID {
		run.cancel()
		return
	}
	g, ok := d.Tasks.Get(groupID)
	if !ok || g.Status != task.GroupPending {
		return
	}
	g.CancelPending(time.Now())
	g.Status = task.GroupCanceled
	g.UpdatedAt = time.Now().Unix()
	d.saveGroup(&g)
	d.repaint()
}

func (d *Dispatcher) chatOnce(ctx context.Context, conversationID, taskID, providerID, model string, msgs []gateway.ChatMessage) (gateway.ChatResponse, error) {
	if d.Chatter == nil {
		return gateway.ChatResponse{}, errors.New("网关不可用")
	}
	temp := planTemperature
	return d.Chatter.Chat(ctx, gateway.ChatRequest{
		ProviderID:     providerID,
		Model:          model,
		ConversationID: conversationID,
		TaskID:         taskID,
		Messages:       msgs,
		MaxTokens:      planMaxTokens,
		Temperature:    &temp,
		Stream:         false,
	}, OriginAgent)
}

// Wait blocks until the running plan or execution finishes. It is for tests
// and shutdown; the UI never waits on it.
func (d *Dispatcher) Wait() {
	for {
		d.mu.Lock()
		ch := d.waitCh
		d.mu.Unlock()
		if ch == nil {
			return
		}
		<-ch
	}
}

func (d *Dispatcher) beginWaitLocked() chan struct{} {
	ch := make(chan struct{})
	d.waitCh = ch
	return ch
}

func (d *Dispatcher) endWaitLocked(ch chan struct{}) {
	if d.waitCh == ch {
		d.waitCh = nil
		close(ch)
	}
}

func (d *Dispatcher) notice(text string) {
	if d.Notice == nil {
		return
	}
	notice := d.Notice
	d.dispatch(func() { notice(text) })
}

func (d *Dispatcher) dispatch(fn func()) {
	if d.Dispatch != nil {
		d.Dispatch(fn)
		return
	}
	fn()
}

func (d *Dispatcher) repaint() {
	if d.Repaint == nil {
		return
	}
	repaint := d.Repaint
	d.dispatch(repaint)
}

func lastUserText(c Conversation) string {
	for i := len(c.Messages) - 1; i >= 0; i-- {
		if c.Messages[i].Role == RoleUser {
			return strings.TrimSpace(c.Messages[i].Content)
		}
	}
	return ""
}

func (d *Dispatcher) planningPrompt(userText string, candidates []Agent) string {
	var b strings.Builder
	b.WriteString("用户消息：\n")
	b.WriteString(userText)
	b.WriteString("\n\n可用子 Agent：\n")
	for _, a := range candidates {
		b.WriteString("- ")
		b.WriteString(a.ID)
		b.WriteString("：")
		b.WriteString(a.Name)
		if line := d.personaLine(a.ID); line != "" {
			b.WriteString(" — ")
			b.WriteString(line)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// promptOf returns the system prompt of one agent workspace. A workspace that
// cannot be read falls back to the built-in default.
func (d *Dispatcher) promptOf(agentID string) string {
	if d.Agents == nil {
		return SystemPrompt
	}
	return agentPrompt(d.Agents, agentID)
}

// personaLine returns the first content line of one agent persona for the
// planning prompt. Headings are skipped, so the default persona adds nothing.
func (d *Dispatcher) personaLine(agentID string) string {
	if d.Agents == nil {
		return ""
	}
	content, err := d.Agents.Workspace(agentID).Load()
	if err != nil {
		log.Printf("agent: load persona of %s: %v", agentID, err)
		return ""
	}
	return firstContentLine(content.Persona)
}

// firstContentLine returns the first non-empty line that is not a heading.
func firstContentLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return line
	}
	return ""
}

func taskPrompt(t *task.Task) string {
	var b strings.Builder
	b.WriteString("目标：")
	b.WriteString(t.Goal)
	b.WriteString("\n\n输入：\n")
	b.WriteString(t.Input)
	if strings.TrimSpace(t.Criteria) != "" {
		b.WriteString("\n\n完成标准：")
		b.WriteString(t.Criteria)
	}
	return b.String()
}

func summaryPrompt(g *task.Group) string {
	var b strings.Builder
	b.WriteString("任务计划共 ")
	fmt.Fprintf(&b, "%d 项，状态：%s。\n", len(g.Tasks), g.Status.Text())
	for i := range g.Tasks {
		t := &g.Tasks[i]
		fmt.Fprintf(&b, "%d. %s（%s，%s）", i+1, t.Goal, t.AssigneeName, t.Status.Text())
		if t.Status == task.TaskFailed {
			if t.ErrorMessage != "" {
				b.WriteString("：失败原因 ")
				b.WriteString(t.ErrorMessage)
			}
		}
		if t.Report != nil && strings.TrimSpace(t.Report.Conclusion) != "" {
			b.WriteString("；结论：")
			b.WriteString(t.Report.Conclusion)
		}
		b.WriteString("\n")
	}
	return b.String()
}

type planTask struct {
	Goal     string `json:"goal"`
	Input    string `json:"input"`
	Criteria string `json:"criteria"`
	Assignee string `json:"assignee"`
}

type planOutput struct {
	Tasks []planTask `json:"tasks"`
}

func parsePlan(content string, candidates []Agent) ([]planTask, error) {
	body := stripJSONFence(content)
	var out planOutput
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		return nil, err
	}
	if len(out.Tasks) == 0 || len(out.Tasks) > task.MaxTasksPerGroup {
		return nil, fmt.Errorf("任务数 %d 不在 1–%d 之间", len(out.Tasks), task.MaxTasksPerGroup)
	}
	known := map[string]bool{}
	for _, a := range candidates {
		known[a.ID] = true
	}
	for i, item := range out.Tasks {
		if err := checkRunes("目标", item.Goal, 1, task.MaxGoalRunes); err != nil {
			return nil, fmt.Errorf("任务 %d：%w", i+1, err)
		}
		if err := checkRunes("输入", item.Input, 0, task.MaxInputRunes); err != nil {
			return nil, fmt.Errorf("任务 %d：%w", i+1, err)
		}
		if err := checkRunes("完成标准", item.Criteria, 0, task.MaxCriteriaRunes); err != nil {
			return nil, fmt.Errorf("任务 %d：%w", i+1, err)
		}
		if !known[item.Assignee] {
			return nil, fmt.Errorf("任务 %d：执行者 %q 不在候选列表", i+1, item.Assignee)
		}
	}
	return out.Tasks, nil
}

type reportOutput struct {
	Conclusion string   `json:"conclusion"`
	Artifacts  []string `json:"artifacts"`
	OpenIssues []string `json:"open_issues"`
	Summary    string   `json:"summary"`
}

func parseReport(content string) (task.Report, error) {
	body := stripJSONFence(content)
	var out reportOutput
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		return task.Report{}, err
	}
	if strings.TrimSpace(out.Conclusion) == "" {
		return task.Report{}, errors.New("conclusion 为空")
	}
	return task.Report{
		Conclusion: strings.TrimSpace(out.Conclusion),
		Artifacts:  out.Artifacts,
		OpenIssues: out.OpenIssues,
		Summary:    out.Summary,
	}, nil
}

func fallbackReport(content string) task.Report {
	text := strings.TrimSpace(content)
	runes := []rune(text)
	if len(runes) > unstructuredConclusionRunes {
		text = string(runes[:unstructuredConclusionRunes])
	}
	return task.Report{
		Conclusion:   text,
		OpenIssues:   []string{"未按结构返回"},
		Unstructured: true,
	}
}

// stripJSONFence removes a surrounding Markdown code fence, which some models
// add despite the prompt.
func stripJSONFence(content string) string {
	body := strings.TrimSpace(content)
	if !strings.HasPrefix(body, "```") {
		return body
	}
	if i := strings.IndexByte(body, '\n'); i >= 0 {
		body = body[i+1:]
	}
	body = strings.TrimSpace(body)
	body = strings.TrimSuffix(body, "```")
	return strings.TrimSpace(body)
}
