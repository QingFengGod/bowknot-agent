package agent

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	mygoui "github.com/egoist/mygo/ui"

	"github.com/QingFengGod/bowknot-agent/internal/gateway"
	"github.com/QingFengGod/bowknot-agent/internal/task"
	"github.com/QingFengGod/bowknot-agent/internal/ui"
)

// PersonaSummary is the one-line persona shown under the agent name.
const PersonaSummary = "本地优先的中文助手，当前不调用工具、不访问文件。"

// AgentName is the display name of the only agent of this build.
const AgentName = "Bowknot 助手"

const (
	userBubbleMax  = 520
	replyBubbleMax = 600
	maxListRows    = 20
	backToEndSlack = 24
	// agentColumnW is the width of the agent list column.
	agentColumnW = 300
	// conversationPanelW is the width of the conversation panel.
	conversationPanelW = 280
	// menuButtonSize is the square of a row's three-dot menu button.
	menuButtonSize = 22
	// menuPanelW is the width of a row menu panel.
	menuPanelW = 180
)

// Page is the assistant module: the agent list, the message stream with its
// composer, and the conversation panel on the right.
type Page struct {
	Chatter    Chatter
	Store      *Store
	Tasks      *task.Store
	Subs       *SubAgentStore
	Runner     *Runner
	Dispatcher *Dispatcher

	// Repaint repaints the main window after an async update.
	Repaint func()
	// Dispatch runs a UI update on the main thread; nil runs inline.
	Dispatch func(func())

	// CurrentID is the selected conversation.
	CurrentID string
	// CurrentAgent is the selected agent; empty is the main agent, and a sub
	// agent ID switches the conversation list and the composer to it.
	CurrentAgent string
	// Input is the unsent message.
	Input string
	// ModelChoice is the label of the chosen chat target.
	ModelChoice string
	// ActionError is the last local failure, such as a save error.
	ActionError string
	// ConfirmDeleteID is the conversation awaiting deletion confirmation.
	ConfirmDeleteID string
	// ConvPanelHidden hides the right conversation panel. The zero value
	// keeps it visible.
	ConvPanelHidden bool
	// ConvMenus holds one open state per conversation row menu.
	ConvMenus map[string]*bool
	// RenamingID is the conversation whose title is being edited inline.
	RenamingID string
	// RenameDraft is the title being typed in the inline editor.
	RenameDraft string
	// ChatScroll is where the message stream is scrolled to.
	ChatScroll mygoui.ScrollState

	// TaskMode sends the next message as a task plan instead of a reply.
	// The state is memory only.
	TaskMode bool
	// SubMenus holds one open state per sub agent row menu.
	SubMenus map[string]*bool
	// SubFormOpen shows the sub agent form.
	SubFormOpen bool
	// SubFormID is the edited sub agent; empty creates one.
	SubFormID string
	// SubFormName, SubFormPersona and SubFormTarget are the form fields.
	SubFormName    string
	SubFormPersona string
	SubFormTarget  string
	// SubFormEnabled is the form's dispatch switch.
	SubFormEnabled bool
	// SubFormError explains why the form was rejected.
	SubFormError string
	// ConfirmSubDeleteID is the sub agent awaiting deletion confirmation.
	ConfirmSubDeleteID string
}

// NewPage creates the assistant page over the chatter, the conversation
// store, the task store and the sub agent store. Nil stores keep the page in
// memory, which the tests use.
func NewPage(chatter Chatter, store *Store, tasks *task.Store, subs *SubAgentStore) *Page {
	if tasks == nil {
		tasks = task.NewStore("")
	}
	if subs == nil {
		subs = NewSubAgentStore("")
	}
	p := &Page{
		Chatter:   chatter,
		Store:     store,
		Tasks:     tasks,
		Subs:      subs,
		ConvMenus: map[string]*bool{},
		SubMenus:  map[string]*bool{},
	}
	p.Runner = NewRunner(chatter, store)
	p.Runner.Dispatch = p.dispatch
	p.Runner.Repaint = p.repaint
	p.Dispatcher = NewDispatcher(chatter, store, tasks, subs, p.Runner)
	p.Dispatcher.Dispatch = p.dispatch
	p.Dispatcher.Repaint = p.repaint
	p.Dispatcher.Notice = func(text string) { p.ActionError = text }
	return p
}

// SelectRecent selects the most recently updated conversation of the
// selected agent when none is selected yet. The app calls it after loading
// the store.
func (p *Page) SelectRecent() {
	if p.CurrentID != "" {
		if conv, ok := p.Store.Get(p.CurrentID); ok && conv.AgentID == p.CurrentAgent {
			return
		}
	}
	list := p.Store.ListByAgent(p.CurrentAgent)
	if len(list) == 0 {
		p.CurrentID = ""
		return
	}
	p.CurrentID = list[0].ID
}

// SelectAgent switches the agent column to the main agent (an empty ID) or
// one sub agent, selecting the most recent conversation of that agent.
func (p *Page) SelectAgent(id string) {
	if id != "" {
		if p.Subs == nil {
			return
		}
		if _, ok := p.Subs.Get(id); !ok {
			return
		}
	}
	if p.CurrentAgent != id {
		p.CurrentAgent = id
		p.CurrentID = ""
		p.ConfirmDeleteID = ""
		p.TaskMode = false
		p.cancelRename()
		p.SelectRecent()
	}
	p.closeMenus()
	p.repaint()
}

// View returns the view of the assistant module.
func (p *Page) View() func(c *mygoui.Context) {
	return func(c *mygoui.Context) {
		t := c.Theme()
		p.syncTarget()
		mygoui.Row(c).Fill().Background(t.Background).Children(func() {
			p.agentColumn(c, t)
			p.conversationColumn(c, t)
			if !p.ConvPanelHidden {
				p.conversationPanel(c, t)
			}
		})
		p.subAgentModal(c, t)
		p.subDeleteModal(c, t)
	}
}

// Send stores the input as a user message, then either starts a reply or, in
// task mode, asks the dispatcher to plan a task group.
func (p *Page) Send() {
	if p.Runner.Blocked() || p.Blocker() != "" || strings.TrimSpace(p.Input) == "" {
		return
	}
	taskMode := p.TaskMode && p.CurrentAgent == ""
	if taskMode && p.dispatchBlocker() != "" {
		return
	}
	providerID, model, persona, ok := p.agentTarget()
	if !ok {
		return
	}
	convID := p.CurrentID
	if convID == "" {
		conv := p.Store.CreateFor(p.CurrentAgent)
		if err := p.Store.Save(conv.ID); err != nil {
			p.ActionError = "会话保存失败：" + err.Error()
		}
		p.CurrentID = conv.ID
		convID = conv.ID
	}
	msg := Message{Role: RoleUser, Content: strings.TrimSpace(p.Input), Status: StatusOK, At: time.Now().Unix()}
	if _, err := p.Store.Append(convID, msg); err != nil {
		p.ActionError = err.Error()
		p.repaint()
		return
	}
	if err := p.Store.Save(convID); err != nil {
		p.ActionError = "会话保存失败：" + err.Error()
	}
	p.Input = ""
	if taskMode {
		if err := p.Dispatcher.Plan(context.Background(), convID); err != nil {
			p.ActionError = err.Error()
		}
		p.repaint()
		return
	}
	if err := p.Runner.StartWith(context.Background(), convID, providerID, model, persona); err != nil {
		p.ActionError = err.Error()
	}
	p.repaint()
}

// agentTarget returns the route target and the system persona of the selected
// agent. A sub agent keeps the model bound in its settings.
func (p *Page) agentTarget() (providerID, model, persona string, ok bool) {
	if p.CurrentAgent == "" {
		providerID, model = p.Runner.Target()
		if providerID == "" || model == "" {
			return "", "", "", false
		}
		return providerID, model, SystemPrompt, true
	}
	if p.Subs == nil {
		return "", "", "", false
	}
	a, found := p.Subs.Get(p.CurrentAgent)
	if !found || !a.Enabled || !targetAvailable(p.chatTargets(), a.ProviderID, a.Model) {
		return "", "", "", false
	}
	return a.ProviderID, a.Model, a.Persona, true
}

// currentSubAgent returns the selected sub agent, or false for the main agent.
func (p *Page) currentSubAgent() (SubAgent, bool) {
	if p.CurrentAgent == "" || p.Subs == nil {
		return SubAgent{}, false
	}
	return p.Subs.Get(p.CurrentAgent)
}

// subAgentTargetLabel returns the display label of the model bound to a sub
// agent, falling back to the raw model name when the target is unavailable.
func (p *Page) subAgentTargetLabel(a SubAgent) string {
	for _, target := range p.chatTargets() {
		if target.ProviderID == a.ProviderID && target.Model == a.Model {
			return target.Label()
		}
	}
	return a.Model
}

// NewConversation creates an empty conversation of the selected agent and
// selects it. A running reply is not aborted; it keeps writing to its own
// conversation.
func (p *Page) NewConversation() {
	conv := p.Store.CreateFor(p.CurrentAgent)
	if err := p.Store.Save(conv.ID); err != nil {
		p.ActionError = "会话保存失败：" + err.Error()
	}
	p.CurrentID = conv.ID
	p.ConfirmDeleteID = ""
	p.ConvPanelHidden = false
	p.closeMenus()
	p.cancelRename()
	p.repaint()
}

// SelectConversation switches to a conversation without touching the running
// generation. The agent column follows the agent of the conversation.
func (p *Page) SelectConversation(id string) {
	conv, ok := p.Store.Get(id)
	if !ok {
		return
	}
	p.CurrentAgent = conv.AgentID
	p.CurrentID = id
	p.ConfirmDeleteID = ""
	p.ChatScroll.Y = math.MaxFloat32
	p.closeMenus()
	p.cancelRename()
}

// DeleteConversation removes the selected conversation after confirmation.
func (p *Page) DeleteConversation() {
	if p.CurrentID == "" {
		return
	}
	if p.Runner.RunningConversation() == p.CurrentID {
		p.Runner.Stop()
	}
	if err := p.Store.Delete(p.CurrentID); err != nil {
		p.ActionError = err.Error()
	}
	p.CurrentID = ""
	p.ConfirmDeleteID = ""
	p.closeMenus()
	p.cancelRename()
	p.SelectRecent()
	p.repaint()
}

// Regenerate replaces the last assistant reply of the selected conversation.
func (p *Page) Regenerate() {
	if p.CurrentID == "" {
		return
	}
	providerID, model, persona, ok := p.agentTarget()
	if !ok {
		return
	}
	if err := p.Runner.RegenerateWith(context.Background(), p.CurrentID, providerID, model, persona); err != nil {
		p.ActionError = err.Error()
	}
	p.repaint()
}

// StartRename opens the inline title editor of one conversation.
func (p *Page) StartRename(id string) {
	conv, ok := p.Store.Get(id)
	if !ok {
		return
	}
	p.closeMenus()
	p.RenamingID = id
	p.RenameDraft = conv.TitleText()
	p.repaint()
}

// CommitRename saves the inline title editor.
func (p *Page) CommitRename() {
	id := p.RenamingID
	if id == "" {
		return
	}
	if err := p.Store.Rename(id, p.RenameDraft); err != nil {
		p.ActionError = err.Error()
		p.repaint()
		return
	}
	if err := p.Store.Save(id); err != nil {
		p.ActionError = "会话保存失败：" + err.Error()
	}
	p.cancelRename()
	p.repaint()
}

// CancelRename closes the inline title editor without saving.
func (p *Page) CancelRename() {
	p.cancelRename()
	p.repaint()
}

func (p *Page) cancelRename() {
	p.RenamingID = ""
	p.RenameDraft = ""
}

// RequestDelete selects a conversation and asks for the delete confirmation.
func (p *Page) RequestDelete(id string) {
	if _, ok := p.Store.Get(id); !ok {
		return
	}
	p.SelectConversation(id)
	p.ConfirmDeleteID = id
	p.repaint()
}

// menuOpen returns the stable open state of one conversation row menu.
func (p *Page) menuOpen(id string) *bool {
	if p.ConvMenus == nil {
		p.ConvMenus = map[string]*bool{}
	}
	open, ok := p.ConvMenus[id]
	if !ok {
		open = new(bool)
		p.ConvMenus[id] = open
	}
	return open
}

// closeMenus closes every row menu before another one opens.
func (p *Page) closeMenus() {
	for _, open := range p.ConvMenus {
		*open = false
	}
	p.closeSubMenus()
}

// Blocker explains why the input is disabled, or returns "" when it works.
func (p *Page) Blocker() string {
	switch p.snapshot().State {
	case gateway.StateNotConfigured:
		return "网关未配置，配置后可启用对话"
	case gateway.StateOffline:
		return "网关离线，恢复连接后可启用对话"
	}
	if p.CurrentAgent != "" {
		if p.Subs == nil {
			return "子 Agent 不存在，无法继续对话"
		}
		a, found := p.Subs.Get(p.CurrentAgent)
		switch {
		case !found:
			return "该子 Agent 已删除，无法继续对话"
		case !a.Enabled:
			return "该子 Agent 已停用，启用后可对话"
		case !targetAvailable(p.chatTargets(), a.ProviderID, a.Model):
			return "该子 Agent 的模型不可用，请在编辑中重新选择"
		}
	}
	if len(p.chatTargets()) == 0 {
		return "模型未配置，请先在设置中配置对话模型"
	}
	if conv, ok := p.Store.Get(p.CurrentID); ok && len(conv.Messages) >= MaxMessages {
		return fmt.Sprintf("会话已满 %d 条消息，请新建会话后继续", MaxMessages)
	}
	return ""
}

// taskModeBlocker explains why task mode cannot start a plan, or returns "".
func (p *Page) taskModeBlocker() string {
	if p.CurrentAgent != "" {
		return "任务模式仅在主 Agent 会话可用"
	}
	switch p.snapshot().State {
	case gateway.StateNotConfigured, gateway.StateOffline:
		return "网关不可用，无法派发任务"
	}
	if len(p.chatTargets()) == 0 {
		return "模型未配置，无法派发任务"
	}
	if p.Dispatcher == nil || len(p.Dispatcher.AvailableAgents()) == 0 {
		return "先创建并启用子 Agent"
	}
	return ""
}

// dispatchBlocker adds the pending plan rule to taskModeBlocker.
func (p *Page) dispatchBlocker() string {
	if b := p.taskModeBlocker(); b != "" {
		return b
	}
	if p.Dispatcher.Busy() {
		return "正在派发任务，请先等待结束"
	}
	if p.CurrentID != "" {
		if _, ok := p.Dispatcher.PendingGroup(p.CurrentID); ok {
			return "已有待确认的任务计划，请先执行或放弃"
		}
	}
	return ""
}

// ToggleTaskMode flips task mode and redraws.
func (p *Page) ToggleTaskMode() {
	if p.Dispatcher != nil && p.Dispatcher.Busy() {
		return
	}
	if p.CurrentAgent != "" {
		return
	}
	if !p.TaskMode && p.taskModeBlocker() != "" {
		return
	}
	p.TaskMode = !p.TaskMode
	p.repaint()
}

// ConfirmTaskGroup runs a pending plan.
func (p *Page) ConfirmTaskGroup(id string) {
	if p.Dispatcher == nil {
		return
	}
	if err := p.Dispatcher.Confirm(id); err != nil {
		p.ActionError = err.Error()
	}
	p.repaint()
}

// AbandonTaskGroup cancels a pending or running plan.
func (p *Page) AbandonTaskGroup(id string) {
	if p.Dispatcher == nil {
		return
	}
	p.Dispatcher.Cancel(id)
	p.repaint()
}

// RetryTask queues a new task for a failed one.
func (p *Page) RetryTask(groupID, taskID string) {
	if p.Dispatcher == nil {
		return
	}
	if err := p.Dispatcher.Retry(groupID, taskID); err != nil {
		p.ActionError = err.Error()
	}
	p.repaint()
}

// syncTarget keeps the pinned route target aligned with the model picker.
func (p *Page) syncTarget() {
	targets := p.chatTargets()
	if len(targets) == 0 {
		p.ModelChoice = ""
		p.Runner.SetTarget("", "")
		return
	}
	chosen, ok := targetByLabel(targets, p.ModelChoice)
	if !ok {
		chosen = targets[0]
		p.ModelChoice = chosen.Label()
	}
	p.Runner.SetTarget(chosen.ProviderID, chosen.Model)
}

func (p *Page) chatTargets() []gateway.TargetView {
	if p.Chatter == nil {
		return nil
	}
	return p.Chatter.ChatTargets()
}

func targetByLabel(targets []gateway.TargetView, label string) (gateway.TargetView, bool) {
	for _, t := range targets {
		if t.Label() == label {
			return t, true
		}
	}
	return gateway.TargetView{}, false
}

func (p *Page) snapshot() gateway.Snapshot {
	if p.Chatter == nil {
		return gateway.Snapshot{}
	}
	return p.Chatter.Snapshot()
}

func (p *Page) dispatch(fn func()) {
	if p.Dispatch != nil {
		p.Dispatch(fn)
		return
	}
	fn()
}

func (p *Page) repaint() {
	if p.Repaint != nil {
		p.Repaint()
	}
}

// agentColumn is the left column: the main agent, the sub agents and the
// create entry.
func (p *Page) agentColumn(c *mygoui.Context, t *mygoui.Theme) {
	col := mygoui.Column(c).Width(agentColumnW).Shrink(0).FillHeight().Padding(12).Gap(6).
		Background(t.Surface).BorderWidth(0, 1, 0, 0).BorderColor(t.Border)
	col.Children(func() {
		mygoui.Text(c, "助手").FontSize(11).TextColor(t.TextMuted).Shrink(0)
		p.mainAgentRow(c, t)
		p.subAgentList(c, t)
	})
}

// subAgentList shows the defined sub agents and the create entry.
func (p *Page) subAgentList(c *mygoui.Context, t *mygoui.Theme) {
	if p.Subs != nil && p.Subs.Corrupt() {
		mygoui.Text(c, "子 Agent 配置损坏，已跳过加载").FontSize(10).
			TextColor(ui.DangerColor(modeOf(t))).Wrap().Shrink(0)
	}
	agents := []SubAgent(nil)
	if p.Subs != nil {
		agents = p.Subs.List()
	}
	scroll := mygoui.Scroll(c).FillWidth().Grow(1)
	scroll.Children(func() {
		col := mygoui.Column(c).FillWidth().Gap(1)
		col.Children(func() {
			if len(agents) == 0 {
				mygoui.Text(c, "还没有子 Agent，创建后可直接对话或派发任务").FontSize(10).
					TextColor(t.TextMuted).Wrap()
				return
			}
			for _, a := range agents {
				p.subAgentRow(c, t, a)
			}
		})
	})
	disabled := p.Subs == nil || p.Subs.Count() >= MaxSubAgents
	label := "+ 新建子 Agent"
	if disabled {
		label = fmt.Sprintf("+ 新建子 Agent（已达上限 %d）", MaxSubAgents)
	}
	b := mygoui.ButtonBase(c).FillWidth().Shrink(0).Label(label).
		Padding(5, 6).Radius(t.Radius).Justify(mygoui.Start).Disabled(disabled)
	if !disabled && b.Hovered() {
		b.Background(t.SurfaceHover)
	}
	b.Children(func() {
		color := t.Accent
		if disabled {
			color = t.TextMuted
		}
		mygoui.Text(c, label).FontSize(11).TextColor(color).SingleLine()
	})
	if b.Clicked() && !disabled {
		p.OpenSubForm("")
	}
}

// subAgentRow is one sub agent: the shared agent row with its row menu.
func (p *Page) subAgentRow(c *mygoui.Context, t *mygoui.Theme, a SubAgent) {
	persona := a.PersonaLine()
	if persona == "" {
		persona = "（没有人设）"
	}
	badge, danger := "", false
	if !a.Enabled {
		badge = "已停用"
	} else if !p.Dispatcher.AgentUsable(a) {
		badge, danger = "模型不可用", true
	}
	open := p.subMenuOpen(a.ID)
	p.agentRow(c, t, a.Name, avatarInitial(a.Name), persona, badge, danger,
		p.CurrentAgent == a.ID, func() { p.SelectAgent(a.ID) }, func() {
			anchor := p.menuButton(c, t, "子 Agent 菜单："+a.Name, open)
			p.popoverMenu(c, t, anchor, open, p.subAgentMenuItems(a))
		})
}

// subAgentMenuItems are the actions of one sub agent row menu.
func (p *Page) subAgentMenuItems(a SubAgent) []menuEntry {
	toggle := "停用"
	if !a.Enabled {
		toggle = "启用"
	}
	return []menuEntry{
		{label: "编辑", action: func() { p.OpenSubForm(a.ID) }},
		{label: toggle, action: func() { p.ToggleSubAgent(a.ID) }},
		{label: "删除", disabled: p.subAgentInUse(a.ID), action: func() { p.RequestSubDelete(a.ID) }},
	}
}

// subAgentInUse reports whether a running task uses the sub agent, which
// blocks deletion.
func (p *Page) subAgentInUse(id string) bool {
	for _, s := range p.Tasks.List() {
		if s.Status != task.GroupRunning && s.Status != task.GroupSummarizing {
			continue
		}
		g, ok := p.Tasks.Get(s.ID)
		if !ok {
			continue
		}
		for i := range g.Tasks {
			if g.Tasks[i].AssigneeID == id && !g.Tasks[i].Status.Terminal() {
				return true
			}
		}
	}
	return false
}

// subMenuOpen returns the stable open state of one sub agent row menu.
func (p *Page) subMenuOpen(id string) *bool {
	if p.SubMenus == nil {
		p.SubMenus = map[string]*bool{}
	}
	open, ok := p.SubMenus[id]
	if !ok {
		open = new(bool)
		p.SubMenus[id] = open
	}
	return open
}

// closeSubMenus closes every sub agent row menu.
func (p *Page) closeSubMenus() {
	for _, open := range p.SubMenus {
		*open = false
	}
}

// ToggleSubAgent enables or disables one sub agent.
func (p *Page) ToggleSubAgent(id string) {
	if p.Subs == nil {
		return
	}
	a, ok := p.Subs.Get(id)
	if !ok {
		return
	}
	if err := p.Subs.SetEnabled(id, !a.Enabled); err != nil {
		p.ActionError = err.Error()
	}
	p.closeSubMenus()
	p.repaint()
}

// OpenSubForm opens the create or edit form of a sub agent.
func (p *Page) OpenSubForm(id string) {
	p.SubFormOpen = true
	p.SubFormID = id
	p.SubFormError = ""
	p.closeSubMenus()
	p.closeMenus()
	if id == "" {
		p.SubFormName = ""
		p.SubFormPersona = ""
		p.SubFormEnabled = true
		p.SubFormTarget = p.defaultTargetLabel()
		if p.SubFormTarget == "" && len(p.chatTargets()) == 0 {
			p.SubFormError = "模型未配置，请先在设置中配置对话模型"
		}
		p.repaint()
		return
	}
	if p.Subs == nil {
		return
	}
	a, ok := p.Subs.Get(id)
	if !ok {
		return
	}
	p.SubFormName = a.Name
	p.SubFormPersona = a.Persona
	p.SubFormEnabled = a.Enabled
	p.SubFormTarget = ""
	for _, target := range p.chatTargets() {
		if target.ProviderID == a.ProviderID && target.Model == a.Model {
			p.SubFormTarget = target.Label()
			break
		}
	}
	p.repaint()
}

// CloseSubForm hides the sub agent form without saving.
func (p *Page) CloseSubForm() {
	p.SubFormOpen = false
	p.SubFormID = ""
	p.SubFormError = ""
	p.repaint()
}

// SaveSubForm validates and stores the form.
func (p *Page) SaveSubForm() {
	if p.Subs == nil {
		return
	}
	chosen, ok := targetByLabel(p.chatTargets(), p.SubFormTarget)
	if !ok {
		p.SubFormError = "请选择子 Agent 的模型"
		p.repaint()
		return
	}
	in := SubAgent{
		ID:         p.SubFormID,
		Name:       p.SubFormName,
		Persona:    p.SubFormPersona,
		ProviderID: chosen.ProviderID,
		Model:      chosen.Model,
		Enabled:    p.SubFormEnabled,
	}
	if _, err := p.Subs.Save(in); err != nil {
		p.SubFormError = err.Error()
		p.repaint()
		return
	}
	p.SubFormOpen = false
	p.SubFormID = ""
	p.SubFormError = ""
	p.repaint()
}

// RequestSubDelete asks for the deletion confirmation of one sub agent.
func (p *Page) RequestSubDelete(id string) {
	if p.Subs == nil {
		return
	}
	if _, ok := p.Subs.Get(id); !ok {
		return
	}
	if p.subAgentInUse(id) {
		p.ActionError = "该子 Agent 有运行中的任务，暂不能删除"
		p.closeSubMenus()
		p.repaint()
		return
	}
	p.ConfirmSubDeleteID = id
	p.closeSubMenus()
	p.repaint()
}

// DeleteSubAgent removes the sub agent awaiting confirmation.
func (p *Page) DeleteSubAgent() {
	id := p.ConfirmSubDeleteID
	p.ConfirmSubDeleteID = ""
	if id == "" || p.Subs == nil {
		p.repaint()
		return
	}
	if err := p.Subs.Delete(id); err != nil {
		p.ActionError = err.Error()
	}
	if p.CurrentAgent == id {
		p.CurrentAgent = ""
		p.CurrentID = ""
		p.SelectRecent()
	}
	p.repaint()
}

// subAgentModal draws the create or edit form of a sub agent.
func (p *Page) subAgentModal(c *mygoui.Context, t *mygoui.Theme) {
	mygoui.Modal(c, &p.SubFormOpen, func() {
		title := "新建子 Agent"
		if p.SubFormID != "" {
			title = "编辑子 Agent"
		}
		mygoui.Text(c, title).FontSize(15).Bold().TextColor(t.Text).Shrink(0)
		mygoui.Text(c, "子 Agent 只能输出文本结论，不会调用工具。").
			FontSize(10).TextColor(t.TextMuted).Wrap()

		mygoui.Text(c, "名称").FontSize(10).TextColor(t.TextMuted).Shrink(0)
		mygoui.TextInput(c, &p.SubFormName).FillWidth().Placeholder("例如：资料整理")

		mygoui.Text(c, "人设").FontSize(10).TextColor(t.TextMuted).Shrink(0)
		mygoui.TextArea(c, &p.SubFormPersona).FillWidth().Lines(2, 8).
			Placeholder("你负责把用户提供的材料整理成要点清单…")

		mygoui.Text(c, "模型").FontSize(10).TextColor(t.TextMuted).Shrink(0)
		labels := p.targetLabels()
		if len(labels) == 0 {
			mygoui.Text(c, "模型未配置，请先在设置中配置对话模型").
				FontSize(11).TextColor(ui.DangerColor(modeOf(t))).Wrap()
		} else {
			mygoui.Select(c, &p.SubFormTarget, labels).FillWidth()
		}

		mygoui.Checkbox(c, &p.SubFormEnabled, "启用（可被派发任务）")
		if p.SubFormError != "" {
			mygoui.Text(c, p.SubFormError).FontSize(10).
				TextColor(ui.DangerColor(modeOf(t))).Wrap()
		}
		actions := mygoui.Row(c).FillWidth().Gap(8).Justify(mygoui.End)
		actions.Children(func() {
			p.button(c, t, "取消", false, false, p.CloseSubForm)
			p.button(c, t, "保存", true, false, p.SaveSubForm)
		})
	})
}

// subDeleteModal confirms deleting one sub agent.
func (p *Page) subDeleteModal(c *mygoui.Context, t *mygoui.Theme) {
	if p.ConfirmSubDeleteID == "" {
		return
	}
	open := true
	name := p.ConfirmSubDeleteID
	if p.Subs != nil {
		if a, ok := p.Subs.Get(p.ConfirmSubDeleteID); ok {
			name = a.Name
		}
	}
	mygoui.Modal(c, &open, func() {
		mygoui.Text(c, "删除子 Agent「"+name+"」后不可恢复，历史任务仍保留其名称。确认删除？").
			FontSize(12).TextColor(t.Text).Wrap()
		actions := mygoui.Row(c).FillWidth().Gap(8).Justify(mygoui.End)
		actions.Children(func() {
			p.button(c, t, "取消", false, false, func() {
				p.ConfirmSubDeleteID = ""
				open = false
				p.repaint()
			})
			p.button(c, t, "确认删除", true, false, func() {
				p.DeleteSubAgent()
				open = false
			})
		})
	})
	if !open {
		p.ConfirmSubDeleteID = ""
	}
}

func (p *Page) targetLabels() []string {
	targets := p.chatTargets()
	labels := make([]string, 0, len(targets))
	for _, target := range targets {
		labels = append(labels, target.Label())
	}
	return labels
}

// defaultTargetLabel returns the label of the current chat target for a new
// sub agent.
func (p *Page) defaultTargetLabel() string {
	targets := p.chatTargets()
	if len(targets) == 0 {
		return ""
	}
	chosen, ok := targetByLabel(targets, p.ModelChoice)
	if !ok {
		chosen = targets[0]
	}
	return chosen.Label()
}

func avatarInitial(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "?"
	}
	for _, r := range trimmed {
		return string(r)
	}
	return "?"
}

// mainAgentRow is the main agent at the top of the list.
func (p *Page) mainAgentRow(c *mygoui.Context, t *mygoui.Theme) {
	p.agentRow(c, t, AgentName, "Bow", PersonaSummary, "", false,
		p.CurrentAgent == "", func() { p.SelectAgent("") }, nil)
}

// agentRow draws one agent entry: the avatar, the name, the persona and an
// optional row menu. The main agent and the sub agents share this layout.
func (p *Page) agentRow(c *mygoui.Context, t *mygoui.Theme, name, initial, persona,
	badge string, badgeDanger, selected bool, onClick, menu func()) {
	row := mygoui.Row(c).FillWidth().Shrink(0).Gap(2).AlignItems(mygoui.Center)
	row.Children(func() {
		b := mygoui.ButtonBase(c).Grow(1).Padding(6).Radius(t.Radius).
			AlignItems(mygoui.Center).Justify(mygoui.Start)
		switch {
		case selected:
			b.Background(ui.AccentTint())
		case b.Hovered():
			b.Background(t.SurfaceHover)
		}
		b.Children(func() {
			mygoui.Avatar(c, initial, nil).Size(36, 36).MarginEnd(5)
			text := mygoui.Column(c).Grow(1).Gap(1)
			text.Children(func() {
				nameRow := mygoui.Row(c).Gap(4).AlignItems(mygoui.Center)
				nameRow.Children(func() {
					mygoui.Text(c, name).FontSize(13).Bold().TextColor(t.Text).SingleLine()
					if badge != "" {
						color := t.TextMuted
						if badgeDanger {
							color = ui.DangerColor(modeOf(t))
						}
						mygoui.Text(c, badge).FontSize(9).TextColor(color).Shrink(0)
					}
				})
				mygoui.Text(c, persona).FontSize(10).TextColor(t.TextMuted).MaxLines(2)
			})
		})
		if b.Clicked() && onClick != nil {
			onClick()
		}
		if menu != nil {
			menu()
		}
	})
}

// ToggleConversationPanel shows or hides the conversation panel.
func (p *Page) ToggleConversationPanel() {
	p.ConvPanelHidden = !p.ConvPanelHidden
	p.closeMenus()
	p.repaint()
}

// conversationPanel is the right column: the conversations of the agent and
// their management. It hides as a whole, and the middle header brings it
// back.
func (p *Page) conversationPanel(c *mygoui.Context, t *mygoui.Theme) {
	col := mygoui.Column(c).
		Width(conversationPanelW).Shrink(0).FillHeight().Padding(12).Gap(6).
		Background(t.Surface).BorderWidth(0, 0, 0, 1).BorderColor(t.Border)
	col.Children(func() {
		head := mygoui.Row(c).FillWidth().Shrink(0).Gap(4).AlignItems(mygoui.Center)
		head.Children(func() {
			mygoui.Text(c, "会话").FontSize(13).Bold().TextColor(t.Text).Grow(1)
			p.textButton(c, t, "新会话", p.NewConversation)
		})
		p.conversationList(c, t)
	})
}

// conversationList lists the conversations of the selected agent, newest
// first.
func (p *Page) conversationList(c *mygoui.Context, t *mygoui.Theme) {
	list := p.Store.ListByAgent(p.CurrentAgent)
	if len(list) > maxListRows {
		list = list[:maxListRows]
	}
	scroll := mygoui.Scroll(c).FillWidth().Grow(1)
	scroll.Children(func() {
		col := mygoui.Column(c).FillWidth().Gap(1)
		col.Children(func() {
			if len(list) == 0 {
				mygoui.Text(c, "还没有会话").FontSize(11).TextColor(t.TextMuted).Wrap()
				return
			}
			for _, item := range list {
				p.conversationRow(c, t, item)
			}
		})
	})
}

// conversationRow is one conversation: the title, the count and the menu.
func (p *Page) conversationRow(c *mygoui.Context, t *mygoui.Theme, item Summary) {
	selected := item.ID == p.CurrentID
	row := mygoui.Row(c).FillWidth().Shrink(0).Gap(2).AlignItems(mygoui.Center)
	row.Children(func() {
		if p.RenamingID == item.ID {
			p.renameRow(c, t)
			return
		}
		b := mygoui.ButtonBase(c).Grow(1).Padding(4, 6).Radius(t.Radius).
			AlignItems(mygoui.Center).Justify(mygoui.Start)
		if selected {
			b.Background(ui.AccentTint()).TextColor(t.Text)
		} else {
			b.TextColor(t.Text)
			if b.Pressed() {
				b.Background(t.SurfacePressed)
			} else if b.Hovered() {
				b.Background(t.SurfaceHover)
			}
		}
		b.Children(func() {
			mygoui.Text(c, item.Title).FontSize(12).TextColor(t.Text).SingleLine().Grow(1)
			mygoui.Text(c, fmt.Sprintf("%d", item.Messages)).FontSize(9).
				TextColor(t.TextMuted).Shrink(0)
		})
		if b.Clicked() {
			p.SelectConversation(item.ID)
			p.repaint()
		}
		open := p.menuOpen(item.ID)
		anchor := p.menuButton(c, t, "会话菜单："+item.Title, open)
		p.popoverMenu(c, t, anchor, open, p.conversationMenuItems(item))
	})
}

// conversationMenuItems are the actions of one conversation row menu.
func (p *Page) conversationMenuItems(item Summary) []menuEntry {
	return []menuEntry{
		{label: "重命名会话", action: func() { p.StartRename(item.ID) }},
		{label: "删除会话", action: func() { p.RequestDelete(item.ID) }},
	}
}

// renameRow is the inline title editor of a conversation row.
func (p *Page) renameRow(c *mygoui.Context, t *mygoui.Theme) {
	input := mygoui.TextInput(c, &p.RenameDraft).Grow(1).Shrink(1).
		Placeholder("会话名称").AutoFocus()
	input.OnSubmit(p.CommitRename)
	p.textButton(c, t, "保存", func() { p.CommitRename() })
	p.textButton(c, t, "取消", func() { p.CancelRename() })
}

// conversationColumn is the middle column: the messages and the composer.
func (p *Page) conversationColumn(c *mygoui.Context, t *mygoui.Theme) {
	col := mygoui.Column(c).Grow(1).FillHeight().Padding(16).Gap(8)
	col.Children(func() {
		p.headerRow(c, t)
		if p.ConfirmDeleteID != "" {
			p.deleteConfirm(c, t)
		}
		p.messages(c, t)
		if p.scrolledUp() {
			p.backToEndRow(c, t)
		}
		p.composer(c, t)
	})
}

func (p *Page) headerRow(c *mygoui.Context, t *mygoui.Theme) {
	row := mygoui.Row(c).FillWidth().Shrink(0).Gap(8).AlignItems(mygoui.Center)
	row.Children(func() {
		title := "助手"
		if conv, ok := p.Store.Get(p.CurrentID); ok {
			title = conv.TitleText()
		} else if a, ok := p.currentSubAgent(); ok {
			title = a.Name
		}
		mygoui.Text(c, title).FontSize(15).Bold().TextColor(t.Text).SingleLine().Grow(1)
		label := "收起会话列表"
		if p.ConvPanelHidden {
			label = "打开会话列表"
		}
		p.button(c, t, label, false, false, p.ToggleConversationPanel)
	})
}

func (p *Page) deleteConfirm(c *mygoui.Context, t *mygoui.Theme) {
	box := mygoui.Column(c).FillWidth().Shrink(0).Padding(8).Gap(6).Radius(t.Radius).
		Background(t.Surface).BorderWidth(1).BorderColor(t.Border)
	box.Children(func() {
		mygoui.Text(c, "删除该会话后不可恢复，确认删除？").FontSize(11).
			TextColor(t.Text).Shrink(0).SingleLine()
		actions := mygoui.Row(c).Gap(8).AlignItems(mygoui.Center)
		actions.Children(func() {
			p.button(c, t, "取消", false, false, func() { p.ConfirmDeleteID = "" })
			p.button(c, t, "确认删除", true, false, p.DeleteConversation)
		})
	})
}

// messages follows the end unless the user scrolled up, and draws the stream.
func (p *Page) messages(c *mygoui.Context, t *mygoui.Theme) {
	if p.ChatScroll.Y >= p.ChatScroll.MaxY {
		p.ChatScroll.Y = math.MaxFloat32
	}
	conv, ok := p.Store.Get(p.CurrentID)
	scroll := mygoui.Scroll(c).Grow(1).FillWidth().FillHeight().
		TrackScroll(&p.ChatScroll)
	scroll.Children(func() {
		col := mygoui.Column(c).FillWidth().Gap(10)
		col.Children(func() {
			if !ok {
				p.emptyState(c, t)
				return
			}
			if len(conv.Messages) == 0 {
				mygoui.Text(c, "这个会话还没有消息，输入内容开始对话。").
					FontSize(12).TextColor(t.TextMuted).Wrap()
				return
			}
			_, lastIsAssistant := conv.LastAssistant()
			for i := range conv.Messages {
				m := conv.Messages[i]
				if m.TaskGroupID != "" {
					g, ok := p.Tasks.Get(m.TaskGroupID)
					// A card hidden by a deleted message is not rendered; the
					// group itself stays on disk.
					if !ok || g.ConversationID != conv.ID || g.MessageIndex != i {
						continue
					}
					p.taskCard(c, t, g)
					continue
				}
				last := lastIsAssistant && i == len(conv.Messages)-1
				p.messageView(c, t, m, last)
			}
		})
	})
}

// scrolledUp reports whether the stream is away from its end.
func (p *Page) scrolledUp() bool {
	if p.ChatScroll.MaxY <= 0 {
		return false
	}
	return p.ChatScroll.MaxY-p.ChatScroll.Y > backToEndSlack
}

func (p *Page) backToEndRow(c *mygoui.Context, t *mygoui.Theme) {
	row := mygoui.Row(c).FillWidth().Shrink(0).Justify(mygoui.End)
	row.Children(func() {
		p.button(c, t, "↓ 回到底部", false, false, func() {
			p.ChatScroll.ScrollTo(0, math.MaxFloat32, mygoui.Smooth)
			p.repaint()
		})
	})
}

func (p *Page) emptyState(c *mygoui.Context, t *mygoui.Theme) {
	box := mygoui.Box(c).FillWidth().Height(240).Center()
	box.Children(func() {
		inner := mygoui.Column(c).Gap(8).AlignItems(mygoui.Center).MaxWidth(360)
		inner.Children(func() {
			mygoui.Icon(c, ui.AgentIcon).Size(32, 32)
			mygoui.Text(c, "开始一段新对话").FontSize(15).Bold().TextColor(t.Text)
			mygoui.Text(c, "在下方输入消息，回复会逐字显示在这里。").
				FontSize(12).TextColor(t.TextMuted).Wrap()
		})
	})
}

func (p *Page) messageView(c *mygoui.Context, t *mygoui.Theme, m Message, last bool) {
	if m.Role == RoleUser {
		row := mygoui.Row(c).FillWidth().Justify(mygoui.End)
		row.Children(func() {
			bubble := mygoui.Column(c).MaxWidth(userBubbleMax).Padding(9, 12).Gap(2).
				Radius(t.Radius).Background(ui.AccentTint())
			bubble.Children(func() {
				mygoui.Text(c, m.Content).FontSize(13).TextColor(t.Text).Wrap()
			})
		})
		return
	}
	row := mygoui.Row(c).FillWidth().Justify(mygoui.Start)
	row.Children(func() {
		bubble := mygoui.Column(c).MaxWidth(replyBubbleMax).Padding(9, 12).Gap(4).
			Radius(t.Radius).Background(t.Surface).BorderWidth(1).BorderColor(t.Border)
		bubble.Children(func() {
			content := m.Content
			if m.Status == StatusStreaming {
				content += "▍"
			}
			if strings.TrimSpace(content) == "" {
				content = "（空回复）"
			}
			mygoui.Text(c, content).FontSize(13).TextColor(t.Text).Wrap()
			if m.Status == StatusStreaming {
				mygoui.Text(c, "正在生成…").FontSize(10).TextColor(t.TextMuted).SingleLine()
				return
			}
			mygoui.Text(c, p.messageMeta(m)).FontSize(10).TextColor(t.TextMuted).SingleLine()
			if m.Status == StatusError || m.Status == StatusAborted {
				mygoui.Text(c, p.messageStatus(m)).FontSize(10).
					TextColor(ui.DangerColor(modeOf(t))).Wrap()
			}
			if last && !p.Runner.Blocked() && m.Status != StatusStreaming {
				p.button(c, t, "重新生成", false, false, p.Regenerate)
			}
		})
	})
}

func (p *Page) messageMeta(m Message) string {
	parts := make([]string, 0, 3)
	if m.Model != "" {
		parts = append(parts, m.Model)
	}
	if m.DurationMS > 0 {
		parts = append(parts, formatDuration(m.DurationMS))
	}
	if m.InputTokens > 0 || m.OutputTokens > 0 {
		parts = append(parts, fmt.Sprintf("输入 %d · 输出 %d token", m.InputTokens, m.OutputTokens))
	} else {
		parts = append(parts, "token 用量未返回")
	}
	return strings.Join(parts, " · ")
}

func (p *Page) messageStatus(m Message) string {
	switch m.Status {
	case StatusAborted:
		return "已中止，已生成的内容已保留"
	case StatusError:
		text := "生成失败"
		if m.ErrorCode != "" {
			text += " · " + gateway.KindText(gateway.ErrorKind(m.ErrorCode))
		}
		if m.ErrorMessage != "" {
			text += " · " + m.ErrorMessage
		}
		return text
	default:
		return ""
	}
}

// taskCard shows one task group in the message stream. A pending card offers
// run and abandon; a running card offers cancel; terminal cards are read only.
func (p *Page) taskCard(c *mygoui.Context, t *mygoui.Theme, g task.Group) {
	card := mygoui.Column(c).FillWidth().Shrink(0).Padding(10, 12).Gap(6).
		Radius(t.Radius).Background(t.Surface).BorderWidth(1).BorderColor(t.Border)
	card.Children(func() {
		head := mygoui.Row(c).FillWidth().Shrink(0).Gap(6).AlignItems(mygoui.Center)
		head.Children(func() {
			mygoui.Text(c, fmt.Sprintf("任务计划 %d 项", len(g.Tasks))).FontSize(13).Bold().
				TextColor(t.Text).Shrink(0)
			mygoui.Text(c, g.Status.Text()).FontSize(10).
				TextColor(p.groupStatusColor(t, g.Status)).Shrink(0)
			mygoui.Spacer(c).Grow(1)
			switch g.Status {
			case task.GroupPending:
				p.button(c, t, "执行", true, false, func() { p.ConfirmTaskGroup(g.ID) })
				p.button(c, t, "放弃", false, false, func() { p.AbandonTaskGroup(g.ID) })
			case task.GroupRunning:
				p.button(c, t, "取消全部", false, false, func() { p.AbandonTaskGroup(g.ID) })
			case task.GroupSummarizing:
				mygoui.Text(c, "正在汇总…").FontSize(10).TextColor(t.TextMuted).Shrink(0)
			}
		})
		for i := range g.Tasks {
			p.taskRow(c, t, g, i)
		}
	})
}

// taskRow is one task of the card: the goal, the assignee, the live status
// and, once terminal, the duration and token usage.
func (p *Page) taskRow(c *mygoui.Context, t *mygoui.Theme, g task.Group, index int) {
	tk := g.Tasks[index]
	row := mygoui.Row(c).FillWidth().Shrink(0).Gap(6).AlignItems(mygoui.Start)
	row.Children(func() {
		mygoui.Text(c, fmt.Sprintf("%d", index+1)).FontSize(10).TextColor(t.TextMuted).
			Width(14).Shrink(0)
		col := mygoui.Column(c).Grow(1).Gap(1)
		col.Children(func() {
			top := mygoui.Row(c).FillWidth().Gap(6).AlignItems(mygoui.Center)
			top.Children(func() {
				mygoui.Text(c, tk.Goal).FontSize(12).TextColor(t.Text).Wrap().MaxLines(2).Grow(1)
				mygoui.Text(c, tk.Status.Text()).FontSize(10).
					TextColor(p.taskStatusColor(t, tk.Status)).Shrink(0)
			})
			mygoui.Text(c, p.taskMeta(tk)).FontSize(10).TextColor(t.TextMuted).SingleLine()
			if tk.Status == task.TaskFailed {
				mygoui.Text(c, taskErrorText(tk)).FontSize(10).
					TextColor(ui.DangerColor(modeOf(t))).Wrap()
			}
			if tk.Report != nil && tk.Report.Unstructured {
				mygoui.Text(c, "未按结构返回").FontSize(10).TextColor(t.TextMuted).SingleLine()
			}
		})
		if p.canRetry(g, tk) {
			p.textButton(c, t, "重试", func() { p.RetryTask(g.ID, tk.ID) })
		}
	})
}

// canRetry reports whether a failed task may be queued again: the group must
// not be canceled or interrupted, and no dispatch may be running.
func (p *Page) canRetry(g task.Group, tk task.Task) bool {
	if tk.Status != task.TaskFailed {
		return false
	}
	if g.Status == task.GroupCanceled || g.Status == task.GroupInterrupted {
		return false
	}
	return p.Dispatcher != nil && !p.Dispatcher.Busy()
}

func (p *Page) taskMeta(tk task.Task) string {
	parts := []string{tk.AssigneeName}
	if tk.Status.Terminal() {
		if tk.DurationMS > 0 {
			parts = append(parts, formatDuration(tk.DurationMS))
		}
		if tk.InputTokens > 0 || tk.OutputTokens > 0 {
			parts = append(parts, fmt.Sprintf("输入 %d · 输出 %d token", tk.InputTokens, tk.OutputTokens))
		}
	}
	return strings.Join(parts, " · ")
}

func taskErrorText(tk task.Task) string {
	text := "失败"
	if tk.ErrorCode != "" {
		text += " · " + gateway.KindText(gateway.ErrorKind(tk.ErrorCode))
	}
	if tk.ErrorMessage != "" {
		text += " · " + tk.ErrorMessage
	}
	return text
}

func (p *Page) taskStatusColor(t *mygoui.Theme, s task.TaskStatus) mygoui.Color {
	switch s {
	case task.TaskDone:
		return ui.SuccessColor(modeOf(t))
	case task.TaskFailed:
		return ui.DangerColor(modeOf(t))
	case task.TaskRunning:
		return t.Accent
	default:
		return t.TextMuted
	}
}

func (p *Page) groupStatusColor(t *mygoui.Theme, s task.GroupStatus) mygoui.Color {
	switch s {
	case task.GroupDone:
		return ui.SuccessColor(modeOf(t))
	case task.GroupFailed:
		return ui.DangerColor(modeOf(t))
	case task.GroupRunning, task.GroupSummarizing:
		return t.Accent
	default:
		return t.TextMuted
	}
}

// composer holds the input, the model picker and the send / stop controls.
func (p *Page) composer(c *mygoui.Context, t *mygoui.Theme) {
	labels := make([]string, 0, 2)
	modelFixed := ""
	if a, ok := p.currentSubAgent(); ok {
		modelFixed = p.subAgentTargetLabel(a)
	} else {
		for _, target := range p.chatTargets() {
			labels = append(labels, target.Label())
		}
	}
	hint := p.Blocker()
	if hint == "" && p.TaskMode {
		hint = p.dispatchBlocker()
	}
	if hint == "" {
		hint = p.ActionError
	}
	if hint == "" {
		hint = p.Runner.Notice()
	}
	blocker := p.Blocker()
	ui.ComposerView(c, t, ui.Composer{
		Value:       &p.Input,
		Placeholder: "输入消息",
		Models:      labels,
		Model:       &p.ModelChoice,
		ModelFixed:  modelFixed,
		Hint:        hint,
		Busy:        p.Runner.Busy(),
		Disabled:    blocker != "" || p.Runner.Blocked() || (p.TaskMode && p.dispatchBlocker() != ""),
		OnSend:      p.Send,
		OnStop:      func() { p.Runner.Stop() },
		OnSubmit: func(text string) bool {
			p.Input = text
			p.Send()
			return true
		},
		TaskMode:         p.TaskMode,
		TaskModeDisabled: p.taskModeBlocker() != "" || (p.Dispatcher != nil && p.Dispatcher.Busy()),
		OnToggleTaskMode: p.ToggleTaskMode,
	})
}

// menuEntry is one action of a row menu.
type menuEntry struct {
	label    string
	disabled bool
	action   func()
}

// menuButton draws the three-dot button of a row and toggles its menu. The
// open state belongs to the page so that the Popover keeps it across frames.
func (p *Page) menuButton(c *mygoui.Context, t *mygoui.Theme, label string, open *bool) mygoui.Element {
	b := mygoui.ButtonBase(c).Size(menuButtonSize, menuButtonSize).Shrink(0).
		Center().Radius(t.Radius).Label(label)
	if *open || b.Hovered() {
		b.Background(t.SurfaceHover)
	}
	b.Children(func() {
		mygoui.Text(c, "⋮").FontSize(13).TextColor(t.TextMuted)
	})
	if b.Clicked() {
		next := !*open
		p.closeMenus()
		*open = next
		p.repaint()
	}
	return b
}

// popoverMenu shows entries in a panel below anchor while open is true.
func (p *Page) popoverMenu(c *mygoui.Context, t *mygoui.Theme, anchor mygoui.Element, open *bool, entries []menuEntry) {
	mygoui.PopoverBase(c, anchor, open, func(panel mygoui.Element) {
		panel.MinWidth(menuPanelW).Padding(4).Gap(2).Radius(t.Radius).
			Background(t.Background).BorderWidth(1).BorderColor(t.Border).
			Shadow(0, 6, 20, 0, mygoui.RGBA(0, 0, 0, 0.18))
		for _, entry := range entries {
			p.menuItemView(c, t, open, entry)
		}
	})
}

func (p *Page) menuItemView(c *mygoui.Context, t *mygoui.Theme, open *bool, entry menuEntry) {
	b := mygoui.ButtonBase(c).FillWidth().Shrink(0).Label(entry.label).
		Padding(6, 10).Radius(t.Radius).Justify(mygoui.Start).Disabled(entry.disabled)
	if !entry.disabled && b.Hovered() {
		b.Background(t.SurfaceHover)
	}
	b.Children(func() {
		color := t.Text
		if entry.disabled {
			color = t.TextMuted
		}
		mygoui.Text(c, entry.label).FontSize(12).TextColor(color).SingleLine()
	})
	if b.Clicked() && !entry.disabled {
		*open = false
		entry.action()
	}
}

// textButton is a compact text action beside the inline title editor.
func (p *Page) textButton(c *mygoui.Context, t *mygoui.Theme, label string, action func()) {
	b := mygoui.ButtonBase(c).Shrink(0).Padding(3, 6).Radius(t.Radius).Label(label)
	if b.Hovered() {
		b.Background(t.SurfaceHover)
	}
	b.Children(func() {
		mygoui.Text(c, label).FontSize(11).TextColor(t.Accent)
	})
	if b.Clicked() {
		action()
	}
}

// button draws a text button and runs action on click.
func (p *Page) button(c *mygoui.Context, t *mygoui.Theme, label string, primary, disabled bool, action func()) {
	b := mygoui.ButtonBase(c).Label(label).Shrink(0).
		Padding(4, 10).Radius(t.Radius).Disabled(disabled)
	if disabled {
		b.Background(t.SurfacePressed)
	} else if primary {
		b.Background(t.Accent)
	} else {
		b.Background(t.SurfacePressed)
		if b.Hovered() {
			b.Background(t.SurfaceHover)
		}
	}
	b.Children(func() {
		color := t.Text
		if primary && !disabled {
			color = t.AccentText
		}
		if disabled {
			color = t.TextMuted
		}
		mygoui.Text(c, label).FontSize(11).TextColor(color)
	})
	if b.Clicked() && !disabled {
		action()
	}
}

func modeOf(t *mygoui.Theme) ui.Mode {
	if t.Dark {
		return ui.ModeDark
	}
	return ui.ModeLight
}

func formatDuration(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	if d < time.Second {
		return fmt.Sprintf("%dms", ms)
	}
	return d.Round(100 * time.Millisecond).String()
}
