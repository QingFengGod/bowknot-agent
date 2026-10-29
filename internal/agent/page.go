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

const (
	userBubbleMax  = 520
	replyBubbleMax = 600
	maxListRows    = 20
	backToEndSlack = 24
	// conversationPanelW is the width of the conversation panel.
	conversationPanelW = 280
	// menuButtonSize is the square of a row's three-dot menu button.
	menuButtonSize = 22
	// menuPanelW is the width of a row menu panel.
	menuPanelW = 180
)

// Page is the assistant module. It serves the main agent only: the message
// stream with its composer, the conversation panel, and the task cards of
// dispatched plans. Sub agent management and 1:1 conversations live in the
// agent module.
type Page struct {
	Chatter    Chatter
	Store      *Store
	Tasks      *task.Store
	Agents     *Registry
	Runner     *Runner
	Dispatcher *Dispatcher

	// Repaint repaints the main window after an async update.
	Repaint func()
	// Dispatch runs a UI update on the main thread; nil runs inline.
	Dispatch func(func())

	// CurrentID is the selected conversation.
	CurrentID string
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
}

// NewPage creates the assistant page over the chatter, the conversation store,
// the task store and the agent registry. Nil stores keep the page in memory,
// which the tests use.
func NewPage(chatter Chatter, store *Store, tasks *task.Store, agents *Registry) *Page {
	if store == nil {
		store = NewStore("")
	}
	if tasks == nil {
		tasks = task.NewStore("")
	}
	if agents == nil {
		agents = NewRegistry("")
	}
	p := &Page{
		Chatter:   chatter,
		Store:     store,
		Tasks:     tasks,
		Agents:    agents,
		ConvMenus: map[string]*bool{},
	}
	p.Runner = NewRunner(chatter, store)
	p.Runner.Dispatch = p.dispatch
	p.Runner.Repaint = p.repaint
	p.Dispatcher = NewDispatcher(chatter, store, tasks, agents, p.Runner)
	p.Dispatcher.Dispatch = p.dispatch
	p.Dispatcher.Repaint = p.repaint
	p.Dispatcher.Notice = func(text string) { p.ActionError = text }
	return p
}

// SelectRecent selects the most recently updated main agent conversation when
// none is selected. The app calls it after loading the store.
func (p *Page) SelectRecent() {
	if p.CurrentID != "" {
		if conv, ok := p.Store.Get(p.CurrentID); ok && conv.AgentID == "" {
			return
		}
	}
	list := p.Store.List()
	if len(list) == 0 {
		p.CurrentID = ""
		return
	}
	p.CurrentID = list[0].ID
}

// View returns the view of the assistant module.
func (p *Page) View() func(c *mygoui.Context) {
	return func(c *mygoui.Context) {
		t := c.Theme()
		p.syncTarget()
		mygoui.Row(c).Fill().Background(t.Background).Children(func() {
			p.conversationColumn(c, t)
			if !p.ConvPanelHidden {
				p.conversationPanel(c, t)
			}
		})
	}
}

// Send stores the input as a user message, then either starts a reply or, in
// task mode, asks the dispatcher to plan a task group.
func (p *Page) Send() {
	if p.Runner.Blocked() || p.Blocker() != "" || strings.TrimSpace(p.Input) == "" {
		return
	}
	taskMode := p.TaskMode
	if taskMode && p.dispatchBlocker() != "" {
		return
	}
	providerID, model, persona, ok := p.agentTarget()
	if !ok {
		return
	}
	convID := p.CurrentID
	if convID == "" {
		conv := p.Store.Create()
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

// agentTarget returns the route target and the system prompt of the main
// agent.
func (p *Page) agentTarget() (providerID, model, persona string, ok bool) {
	providerID, model = p.Runner.Target()
	if providerID == "" || model == "" {
		return "", "", "", false
	}
	return providerID, model, agentPrompt(p.Agents, MainAgentID), true
}

// NewConversation creates an empty main agent conversation and selects it. A
// running reply is not aborted; it keeps writing to its own conversation.
func (p *Page) NewConversation() {
	conv := p.Store.Create()
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

// SelectConversation switches to a main agent conversation without touching
// the running generation.
func (p *Page) SelectConversation(id string) {
	conv, ok := p.Store.Get(id)
	if !ok || conv.AgentID != "" {
		return
	}
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
}

// Blocker explains why the input is disabled, or returns "" when it works.
func (p *Page) Blocker() string {
	switch p.snapshot().State {
	case gateway.StateNotConfigured:
		return "网关未配置，配置后可启用对话"
	case gateway.StateOffline:
		return "网关离线，恢复连接后可启用对话"
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
	if !p.TaskMode && p.taskModeBlocker() != "" {
		return
	}
	p.TaskMode = !p.TaskMode
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

// ToggleConversationPanel shows or hides the conversation panel.
func (p *Page) ToggleConversationPanel() {
	p.ConvPanelHidden = !p.ConvPanelHidden
	p.closeMenus()
	p.repaint()
}

// conversationPanel is the right column: the main agent conversations and
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
			drawTextButton(c, t, "新会话", p.NewConversation)
		})
		p.conversationList(c, t)
	})
}

// conversationList lists the conversations of the main agent, newest first.
func (p *Page) conversationList(c *mygoui.Context, t *mygoui.Theme) {
	list := p.Store.List()
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
		anchor := drawMenuButton(c, t, "会话菜单："+item.Title, open, func() {
			next := !*open
			p.closeMenus()
			*open = next
			p.repaint()
		})
		drawPopoverMenu(c, t, anchor, open, []menuEntry{
			{label: "重命名会话", action: func() { p.StartRename(item.ID) }},
			{label: "删除会话", action: func() { p.RequestDelete(item.ID) }},
		})
	})
}

// renameRow is the inline title editor of a conversation row.
func (p *Page) renameRow(c *mygoui.Context, t *mygoui.Theme) {
	input := mygoui.TextInput(c, &p.RenameDraft).Grow(1).Shrink(1).
		Placeholder("会话名称").AutoFocus()
	input.OnSubmit(p.CommitRename)
	drawTextButton(c, t, "保存", func() { p.CommitRename() })
	drawTextButton(c, t, "取消", func() { p.CancelRename() })
}

// conversationColumn is the middle column: the messages and the composer.
func (p *Page) conversationColumn(c *mygoui.Context, t *mygoui.Theme) {
	col := mygoui.Column(c).Grow(1).FillHeight().Padding(16).Gap(8)
	col.Children(func() {
		p.headerRow(c, t)
		if p.ConfirmDeleteID != "" {
			p.deleteConfirm(c, t)
		}
		conv, ok := p.Store.Get(p.CurrentID)
		drawMessages(c, t, conv, ok, &p.ChatScroll, messageActions{
			Tasks:           p.Tasks,
			Dispatcher:      p.Dispatcher,
			Repaint:         p.repaint,
			AllowRegenerate: ok && !p.Runner.Blocked(),
			Regenerate:      p.Regenerate,
		})
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
		}
		mygoui.Text(c, title).FontSize(15).Bold().TextColor(t.Text).SingleLine().Grow(1)
		label := "收起会话列表"
		if p.ConvPanelHidden {
			label = "打开会话列表"
		}
		drawButton(c, t, label, false, false, p.ToggleConversationPanel)
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
			drawButton(c, t, "取消", false, false, func() { p.ConfirmDeleteID = "" })
			drawButton(c, t, "确认删除", true, false, p.DeleteConversation)
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
		drawButton(c, t, "↓ 回到底部", false, false, func() {
			p.ChatScroll.ScrollTo(0, math.MaxFloat32, mygoui.Smooth)
			p.repaint()
		})
	})
}

// composer holds the input, the model picker and the send / stop controls.
func (p *Page) composer(c *mygoui.Context, t *mygoui.Theme) {
	labels := make([]string, 0, 2)
	for _, target := range p.chatTargets() {
		labels = append(labels, target.Label())
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

// agentPrompt returns the assembled system prompt of one agent workspace. A
// workspace that cannot be read falls back to the built-in default.
func agentPrompt(agents *Registry, agentID string) string {
	if agents == nil {
		return SystemPrompt
	}
	content, err := agents.Workspace(agentID).Load()
	if err != nil {
		// The corrupt file is kept; the remaining sections are still used.
		_ = err
	}
	if prompt := strings.TrimSpace(content.Prompt()); prompt != "" {
		return prompt
	}
	return SystemPrompt
}

// menuEntry is one action of a row menu.
type menuEntry struct {
	label    string
	disabled bool
	action   func()
}

// messageActions carries what the shared message stream needs to render task
// cards and the regenerate action.
type messageActions struct {
	Tasks           *task.Store
	Dispatcher      *Dispatcher
	Repaint         func()
	AllowRegenerate bool
	Regenerate      func()
}

// drawMessages follows the end unless the user scrolled up, and draws the
// stream of one conversation.
func drawMessages(c *mygoui.Context, t *mygoui.Theme, conv Conversation, ok bool,
	scrollState *mygoui.ScrollState, actions messageActions) {
	if scrollState.Y >= scrollState.MaxY {
		scrollState.Y = math.MaxFloat32
	}
	scroll := mygoui.Scroll(c).Grow(1).FillWidth().FillHeight().TrackScroll(scrollState)
	scroll.Children(func() {
		col := mygoui.Column(c).FillWidth().Gap(10)
		col.Children(func() {
			if !ok {
				drawEmptyState(c, t)
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
					if actions.Tasks == nil {
						continue
					}
					g, found := actions.Tasks.Get(m.TaskGroupID)
					// A card hidden by a deleted message is not rendered; the
					// group itself stays on disk.
					if !found || g.ConversationID != conv.ID || g.MessageIndex != i {
						continue
					}
					drawTaskCard(c, t, g, actions)
					continue
				}
				last := lastIsAssistant && i == len(conv.Messages)-1
				drawMessage(c, t, m, last && actions.AllowRegenerate, actions.Regenerate)
			}
		})
	})
}

func drawEmptyState(c *mygoui.Context, t *mygoui.Theme) {
	box := mygoui.Box(c).FillWidth().Height(240).Center()
	box.Children(func() {
		inner := mygoui.Column(c).Gap(8).AlignItems(mygoui.Center).MaxWidth(360)
		inner.Children(func() {
			mygoui.Icon(c, ui.AssistantIcon).Size(32, 32)
			mygoui.Text(c, "开始一段新对话").FontSize(15).Bold().TextColor(t.Text)
			mygoui.Text(c, "在下方输入消息，回复会逐字显示在这里。").
				FontSize(12).TextColor(t.TextMuted).Wrap()
		})
	})
}

func drawMessage(c *mygoui.Context, t *mygoui.Theme, m Message, showRegenerate bool, regenerate func()) {
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
			mygoui.Text(c, messageMeta(m)).FontSize(10).TextColor(t.TextMuted).SingleLine()
			if m.Status == StatusError || m.Status == StatusAborted {
				mygoui.Text(c, messageStatus(m)).FontSize(10).
					TextColor(ui.DangerColor(modeOf(t))).Wrap()
			}
			if showRegenerate && m.Status != StatusStreaming && regenerate != nil {
				drawButton(c, t, "重新生成", false, false, regenerate)
			}
		})
	})
}

func messageMeta(m Message) string {
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

func messageStatus(m Message) string {
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

// drawTaskCard shows one task group in the message stream. A pending card
// offers run and abandon; a running card offers cancel; terminal cards are
// read only.
func drawTaskCard(c *mygoui.Context, t *mygoui.Theme, g task.Group, actions messageActions) {
	card := mygoui.Column(c).FillWidth().Shrink(0).Padding(10, 12).Gap(6).
		Radius(t.Radius).Background(t.Surface).BorderWidth(1).BorderColor(t.Border)
	card.Children(func() {
		head := mygoui.Row(c).FillWidth().Shrink(0).Gap(6).AlignItems(mygoui.Center)
		head.Children(func() {
			mygoui.Text(c, fmt.Sprintf("任务计划 %d 项", len(g.Tasks))).FontSize(13).Bold().
				TextColor(t.Text).Shrink(0)
			mygoui.Text(c, g.Status.Text()).FontSize(10).
				TextColor(groupStatusColor(t, g.Status)).Shrink(0)
			mygoui.Spacer(c).Grow(1)
			switch g.Status {
			case task.GroupPending:
				drawButton(c, t, "执行", true, false, func() {
					if actions.Dispatcher != nil {
						if err := actions.Dispatcher.Confirm(g.ID); err != nil {
							notifyRepaint(actions)
						}
					}
					notifyRepaint(actions)
				})
				drawButton(c, t, "放弃", false, false, func() {
					if actions.Dispatcher != nil {
						actions.Dispatcher.Cancel(g.ID)
					}
					notifyRepaint(actions)
				})
			case task.GroupRunning:
				drawButton(c, t, "取消全部", false, false, func() {
					if actions.Dispatcher != nil {
						actions.Dispatcher.Cancel(g.ID)
					}
					notifyRepaint(actions)
				})
			case task.GroupSummarizing:
				mygoui.Text(c, "正在汇总…").FontSize(10).TextColor(t.TextMuted).Shrink(0)
			}
		})
		for i := range g.Tasks {
			drawTaskRow(c, t, g, i, actions)
		}
	})
}

// drawTaskRow is one task of the card: the goal, the assignee, the live status
// and, once terminal, the duration and token usage.
func drawTaskRow(c *mygoui.Context, t *mygoui.Theme, g task.Group, index int, actions messageActions) {
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
					TextColor(taskStatusColor(t, tk.Status)).Shrink(0)
			})
			mygoui.Text(c, taskMeta(tk)).FontSize(10).TextColor(t.TextMuted).SingleLine()
			if tk.Status == task.TaskFailed {
				mygoui.Text(c, taskErrorText(tk)).FontSize(10).
					TextColor(ui.DangerColor(modeOf(t))).Wrap()
			}
			if tk.Report != nil && tk.Report.Unstructured {
				mygoui.Text(c, "未按结构返回").FontSize(10).TextColor(t.TextMuted).SingleLine()
			}
		})
		if canRetry(g, tk, actions.Dispatcher) {
			drawTextButton(c, t, "重试", func() {
				if actions.Dispatcher != nil {
					if err := actions.Dispatcher.Retry(g.ID, tk.ID); err != nil {
						notifyRepaint(actions)
					}
				}
				notifyRepaint(actions)
			})
		}
	})
}

// canRetry reports whether a failed task may be queued again: the group must
// not be canceled or interrupted, and no dispatch may be running.
func canRetry(g task.Group, tk task.Task, dispatcher *Dispatcher) bool {
	if tk.Status != task.TaskFailed {
		return false
	}
	if g.Status == task.GroupCanceled || g.Status == task.GroupInterrupted {
		return false
	}
	return dispatcher != nil && !dispatcher.Busy()
}

func notifyRepaint(actions messageActions) {
	if actions.Repaint != nil {
		actions.Repaint()
	}
}

func taskMeta(tk task.Task) string {
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

func taskStatusColor(t *mygoui.Theme, s task.TaskStatus) mygoui.Color {
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

func groupStatusColor(t *mygoui.Theme, s task.GroupStatus) mygoui.Color {
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

// avatarInitial returns the first rune of a name, or "?" for an empty name.
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

// drawMenuButton draws the three-dot button of a row and toggles its menu. The
// open state belongs to the page so that the Popover keeps it across frames.
func drawMenuButton(c *mygoui.Context, t *mygoui.Theme, label string, open *bool, onClick func()) mygoui.Element {
	b := mygoui.ButtonBase(c).Size(menuButtonSize, menuButtonSize).Shrink(0).
		Center().Radius(t.Radius).Label(label)
	if *open || b.Hovered() {
		b.Background(t.SurfaceHover)
	}
	b.Children(func() {
		mygoui.Text(c, "⋮").FontSize(13).TextColor(t.TextMuted)
	})
	if b.Clicked() {
		onClick()
	}
	return b
}

// drawPopoverMenu shows entries in a panel below anchor while open is true.
func drawPopoverMenu(c *mygoui.Context, t *mygoui.Theme, anchor mygoui.Element, open *bool, entries []menuEntry) {
	mygoui.PopoverBase(c, anchor, open, func(panel mygoui.Element) {
		panel.MinWidth(menuPanelW).Padding(4).Gap(2).Radius(t.Radius).
			Background(t.Background).BorderWidth(1).BorderColor(t.Border).
			Shadow(0, 6, 20, 0, mygoui.RGBA(0, 0, 0, 0.18))
		for _, entry := range entries {
			drawMenuItemView(c, t, open, entry)
		}
	})
}

func drawMenuItemView(c *mygoui.Context, t *mygoui.Theme, open *bool, entry menuEntry) {
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

// drawTextButton is a compact text action beside an inline editor.
func drawTextButton(c *mygoui.Context, t *mygoui.Theme, label string, action func()) {
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

// drawButton draws a text button and runs action on click.
func drawButton(c *mygoui.Context, t *mygoui.Theme, label string, primary, disabled bool, action func()) {
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
