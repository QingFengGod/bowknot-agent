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
	// agentPanelW is the width of the conversation panel in a detail view.
	agentPanelW = 260
	// agentCardW and agentCardH size one sub agent card in the grid.
	agentCardW = 260.0
	agentCardH = 128.0
)

// AgentPage is the agent module. It lists the sub agents as cards, and opens
// one sub agent into a detail view shaped like the assistant page: the
// conversation panel, the message stream and the composer of a 1:1 chat. The
// main agent is owned by the assistant page and not shown here.
type AgentPage struct {
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

	// CurrentAgent is the open sub agent; empty shows the card list.
	CurrentAgent string
	// CurrentID is the selected conversation of the open sub agent.
	CurrentID string
	// Input is the unsent message.
	Input string
	// ActionError is the last local failure.
	ActionError string
	// ChatScroll is where the message stream is scrolled to.
	ChatScroll mygoui.ScrollState
	// AgentMenus holds one open state per card menu.
	AgentMenus map[string]*bool

	// FormOpen shows the create or edit form.
	FormOpen bool
	// FormID is the edited sub agent; empty creates one.
	FormID string
	// FormName, FormAvatar, FormPersona and FormTarget are the form fields.
	FormName    string
	FormAvatar  string
	FormPersona string
	FormTarget  string
	// FormEnabled is the form's dispatch switch.
	FormEnabled bool
	// FormCopyMain copies the main agent USER.md and PERSONA.md into a new
	// sub agent as its initial template.
	FormCopyMain bool
	// FormError explains why the form was rejected.
	FormError string
	// ConfirmDeleteID is the sub agent awaiting deletion confirmation.
	ConfirmDeleteID string
}

// NewAgentPage creates the agent page over the shared stores and runner. The
// runner is shared with the assistant page so that a 1:1 reply keeps its slot
// and streaming state.
func NewAgentPage(chatter Chatter, store *Store, tasks *task.Store, agents *Registry, runner *Runner) *AgentPage {
	if store == nil {
		store = NewStore("")
	}
	if tasks == nil {
		tasks = task.NewStore("")
	}
	if agents == nil {
		agents = NewRegistry("")
	}
	if runner == nil {
		runner = NewRunner(chatter, store)
	}
	return &AgentPage{
		Chatter:    chatter,
		Store:      store,
		Tasks:      tasks,
		Agents:     agents,
		Runner:     runner,
		AgentMenus: map[string]*bool{},
	}
}

// View returns the view of the agent module.
func (p *AgentPage) View() func(c *mygoui.Context) {
	return func(c *mygoui.Context) {
		t := c.Theme()
		p.validateCurrent()
		if p.CurrentAgent == "" {
			p.listView(c, t)
		} else {
			p.detailView(c, t)
		}
		p.agentFormModal(c, t)
		p.deleteModal(c, t)
	}
}

// validateCurrent falls back to the card list when the open sub agent is gone.
func (p *AgentPage) validateCurrent() {
	if p.CurrentAgent == "" {
		return
	}
	if _, ok := p.Agents.Get(p.CurrentAgent); !ok {
		p.CurrentAgent = ""
		p.CurrentID = ""
	}
}

// SelectAgent opens one sub agent and selects its most recent conversation.
// An empty ID returns to the card list; the main agent is not part of this
// page.
func (p *AgentPage) SelectAgent(id string) {
	if id == "" {
		p.BackToList()
		return
	}
	if _, ok := p.Agents.Get(id); !ok {
		return
	}
	if p.CurrentAgent != id {
		p.CurrentAgent = id
		p.CurrentID = ""
		p.SelectRecent()
	}
	p.closeMenus()
	p.repaint()
}

// BackToList closes the detail view and returns to the card list.
func (p *AgentPage) BackToList() {
	p.CurrentAgent = ""
	p.CurrentID = ""
	p.closeMenus()
	p.repaint()
}

// SelectRecent selects the most recent conversation of the open agent when
// none is selected.
func (p *AgentPage) SelectRecent() {
	if p.CurrentAgent == "" {
		p.CurrentID = ""
		return
	}
	if p.CurrentID != "" {
		if conv, ok := p.Store.Get(p.CurrentID); ok && normalizeAgentID(conv.AgentID) == p.CurrentAgent {
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

// SelectConversation switches to a conversation of the open agent.
func (p *AgentPage) SelectConversation(id string) {
	conv, ok := p.Store.Get(id)
	if !ok || normalizeAgentID(conv.AgentID) != p.CurrentAgent {
		return
	}
	p.CurrentID = id
	p.ChatScroll.Y = math.MaxFloat32
	p.repaint()
}

// NewConversation creates an empty conversation of the open agent.
func (p *AgentPage) NewConversation() {
	conv := p.Store.CreateFor(p.CurrentAgent)
	if err := p.Store.Save(conv.ID); err != nil {
		p.ActionError = "会话保存失败：" + err.Error()
	}
	p.CurrentID = conv.ID
	p.repaint()
}

// Send stores the input as a user message and starts the reply of the open
// agent.
func (p *AgentPage) Send() {
	if p.Runner.Blocked() || p.Blocker() != "" || strings.TrimSpace(p.Input) == "" {
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
	if err := p.Runner.StartWith(context.Background(), convID, providerID, model, persona); err != nil {
		p.ActionError = err.Error()
	}
	p.repaint()
}

// Regenerate replaces the last assistant reply of the selected conversation.
func (p *AgentPage) Regenerate() {
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

// agentTarget returns the route target and prompt of the open sub agent.
func (p *AgentPage) agentTarget() (providerID, model, persona string, ok bool) {
	a, found := p.Agents.Get(p.CurrentAgent)
	if !found || !a.Enabled || !targetAvailable(p.chatTargets(), a.ProviderID, a.Model) {
		return "", "", "", false
	}
	return a.ProviderID, a.Model, agentPrompt(p.Agents, a.ID), true
}

// Blocker explains why the input is disabled, or returns "" when it works.
func (p *AgentPage) Blocker() string {
	switch p.snapshot().State {
	case gateway.StateNotConfigured:
		return "网关未配置，配置后可启用对话"
	case gateway.StateOffline:
		return "网关离线，恢复连接后可启用对话"
	}
	a, found := p.Agents.Get(p.CurrentAgent)
	switch {
	case !found:
		return "该子 Agent 已删除，无法继续对话"
	case !a.Enabled:
		return "该子 Agent 已停用，启用后可对话"
	case !targetAvailable(p.chatTargets(), a.ProviderID, a.Model):
		return "该子 Agent 的模型不可用，请在编辑中重新选择"
	}
	if conv, ok := p.Store.Get(p.CurrentID); ok && len(conv.Messages) >= MaxMessages {
		return fmt.Sprintf("会话已满 %d 条消息，请新建会话后继续", MaxMessages)
	}
	return ""
}

// listView is the card list of every sub agent.
func (p *AgentPage) listView(c *mygoui.Context, t *mygoui.Theme) {
	col := mygoui.Column(c).Fill().Padding(20).Gap(12)
	col.Children(func() {
		p.listHeader(c, t)
		agents := p.subAgents()
		if len(agents) == 0 {
			p.emptyList(c, t)
			return
		}
		mygoui.GridView(c, nil, len(agents), agentCardW, agentCardH, func(i int) {
			p.agentCard(c, t, agents[i])
		}).FillWidth().Grow(1)
	})
}

func (p *AgentPage) listHeader(c *mygoui.Context, t *mygoui.Theme) {
	row := mygoui.Row(c).FillWidth().Shrink(0).Gap(8).AlignItems(mygoui.Center)
	row.Children(func() {
		head := mygoui.Column(c).Grow(1).Gap(2)
		head.Children(func() {
			mygoui.Text(c, "Agent").FontSize(17).Bold().TextColor(t.Text).SingleLine()
			mygoui.Text(c, fmt.Sprintf("共 %d 个子 Agent，点击卡片进入 1:1 会话", len(p.subAgents()))).
				FontSize(11).TextColor(t.TextMuted).SingleLine()
		})
		disabled := len(p.subAgents()) >= MaxSubAgents
		label := "+ 新建子 Agent"
		if disabled {
			label = fmt.Sprintf("+ 新建子 Agent（已达上限 %d）", MaxSubAgents)
		}
		drawButton(c, t, label, true, disabled, func() { p.OpenAgentForm("") })
	})
}

func (p *AgentPage) emptyList(c *mygoui.Context, t *mygoui.Theme) {
	box := mygoui.Box(c).FillWidth().Grow(1).Center()
	box.Children(func() {
		col := mygoui.Column(c).Gap(8).AlignItems(mygoui.Center).MaxWidth(360)
		col.Children(func() {
			mygoui.Icon(c, ui.AgentIcon).Size(32, 32)
			mygoui.Text(c, "还没有子 Agent").FontSize(15).Bold().TextColor(t.Text)
			mygoui.Text(c, "创建子 Agent 后，可以在这里与它 1:1 对话，也可以交给主 Agent 派发任务。").
				FontSize(11).TextColor(t.TextMuted).Wrap()
			drawButton(c, t, "+ 新建子 Agent", true, false, func() { p.OpenAgentForm("") })
		})
	})
}

// agentCard is one sub agent in the list: the avatar, the name, the persona,
// the model and the enable / delete menu.
func (p *AgentPage) agentCard(c *mygoui.Context, t *mygoui.Theme, a Agent) {
	badge, danger := "", false
	if !a.Enabled {
		badge = "已停用"
	} else if !targetAvailable(p.chatTargets(), a.ProviderID, a.Model) {
		badge, danger = "模型不可用", true
	}
	card := mygoui.ButtonBase(c).FillWidth().FillHeight().Padding(12).Radius(t.Radius).
		Background(t.Surface).BorderWidth(1).BorderColor(t.Border).
		AlignItems(mygoui.Start).Justify(mygoui.Start)
	if card.Hovered() {
		card.Background(t.SurfaceHover)
	}
	card.Children(func() {
		col := mygoui.Column(c).FillWidth().Grow(1).Gap(6)
		col.Children(func() {
			top := mygoui.Row(c).FillWidth().Gap(8).AlignItems(mygoui.Center)
			top.Children(func() {
				mygoui.Avatar(c, agentAvatar(a, a.Name), nil).Size(36, 36)
				info := mygoui.Column(c).Grow(1).Gap(1)
				info.Children(func() {
					nameRow := mygoui.Row(c).Gap(4).AlignItems(mygoui.Center)
					nameRow.Children(func() {
						mygoui.Text(c, a.Name).FontSize(13).Bold().TextColor(t.Text).SingleLine()
						if badge != "" {
							color := t.TextMuted
							if danger {
								color = ui.DangerColor(modeOf(t))
							}
							mygoui.Text(c, badge).FontSize(9).TextColor(color).Shrink(0)
						}
					})
					mygoui.Text(c, p.agentModelLabel(a)).FontSize(10).
						TextColor(t.TextMuted).SingleLine()
				})
				open := p.menuOpen(a.ID)
				anchor := drawMenuButton(c, t, "子 Agent 菜单："+a.Name, open, func() {
					next := !*open
					p.closeMenus()
					*open = next
					p.repaint()
				})
				drawPopoverMenu(c, t, anchor, open, p.subAgentMenuItems(a))
			})
			mygoui.Text(c, p.personaSummary(a.ID)).FontSize(11).TextColor(t.TextMuted).
				Wrap().MaxLines(2).Grow(1)
			mygoui.Text(c, fmt.Sprintf("%d 个会话", len(p.Store.ListByAgent(a.ID)))).
				FontSize(9).TextColor(t.TextMuted).SingleLine().Shrink(0)
		})
	})
	if card.Clicked() {
		p.SelectAgent(a.ID)
		p.repaint()
	}
}

// detailView is one sub agent: the same shape as the assistant page, with the
// conversation panel on the right.
func (p *AgentPage) detailView(c *mygoui.Context, t *mygoui.Theme) {
	mygoui.Row(c).Fill().Background(t.Background).Children(func() {
		p.conversationColumn(c, t)
		p.conversationPanel(c, t)
	})
}

// conversationColumn is the middle column of the detail view.
func (p *AgentPage) conversationColumn(c *mygoui.Context, t *mygoui.Theme) {
	col := mygoui.Column(c).Grow(1).FillHeight().Padding(16).Gap(8)
	col.Children(func() {
		p.headerRow(c, t)
		conv, ok := p.Store.Get(p.CurrentID)
		drawMessages(c, t, conv, ok, &p.ChatScroll, messageActions{
			Tasks:           p.Tasks,
			Dispatcher:      p.Dispatcher,
			Repaint:         p.repaint,
			AllowRegenerate: ok && !p.Runner.Blocked(),
			Regenerate:      p.Regenerate,
		})
		p.composer(c, t)
	})
}

func (p *AgentPage) headerRow(c *mygoui.Context, t *mygoui.Theme) {
	row := mygoui.Row(c).FillWidth().Shrink(0).Gap(8).AlignItems(mygoui.Center)
	row.Children(func() {
		drawTextButton(c, t, "← 返回", p.BackToList)
		a, _ := p.Agents.Get(p.CurrentAgent)
		title := a.Name
		if conv, ok := p.Store.Get(p.CurrentID); ok {
			title += " · " + conv.TitleText()
		}
		col := mygoui.Column(c).Grow(1).Gap(1)
		col.Children(func() {
			mygoui.Text(c, title).FontSize(15).Bold().TextColor(t.Text).SingleLine()
			mygoui.Text(c, p.agentSubtitle(a)).FontSize(10).TextColor(t.TextMuted).SingleLine()
		})
		drawButton(c, t, "编辑", false, false, func() { p.OpenAgentForm(a.ID) })
	})
}

// conversationPanel lists the conversations of the open sub agent.
func (p *AgentPage) conversationPanel(c *mygoui.Context, t *mygoui.Theme) {
	col := mygoui.Column(c).
		Width(agentPanelW).Shrink(0).FillHeight().Padding(12).Gap(6).
		Background(t.Surface).BorderWidth(0, 0, 0, 1).BorderColor(t.Border)
	col.Children(func() {
		head := mygoui.Row(c).FillWidth().Shrink(0).Gap(4).AlignItems(mygoui.Center)
		head.Children(func() {
			mygoui.Text(c, "会话").FontSize(13).Bold().TextColor(t.Text).Grow(1)
			drawTextButton(c, t, "新会话", p.NewConversation)
		})
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
	})
}

func (p *AgentPage) conversationRow(c *mygoui.Context, t *mygoui.Theme, item Summary) {
	selected := item.ID == p.CurrentID
	b := mygoui.ButtonBase(c).FillWidth().Shrink(0).Padding(4, 6).Radius(t.Radius).
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
	}
}

// composer holds the input and the bound model of the open sub agent.
func (p *AgentPage) composer(c *mygoui.Context, t *mygoui.Theme) {
	modelFixed := ""
	if a, ok := p.Agents.Get(p.CurrentAgent); ok {
		modelFixed = p.agentModelLabel(a)
	}
	hint := p.Blocker()
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
		ModelFixed:  modelFixed,
		Hint:        hint,
		Busy:        p.Runner.Busy(),
		Disabled:    blocker != "" || p.Runner.Blocked(),
		OnSend:      p.Send,
		OnStop:      func() { p.Runner.Stop() },
		OnSubmit: func(text string) bool {
			p.Input = text
			p.Send()
			return true
		},
	})
}

// subAgents returns the sub agents in registry order.
func (p *AgentPage) subAgents() []Agent {
	out := make([]Agent, 0)
	for _, a := range p.Agents.List() {
		if !a.IsMain() {
			out = append(out, a)
		}
	}
	return out
}

func (p *AgentPage) subAgentMenuItems(a Agent) []menuEntry {
	toggle := "停用"
	if !a.Enabled {
		toggle = "启用"
	}
	return []menuEntry{
		{label: "打开", action: func() { p.SelectAgent(a.ID) }},
		{label: "编辑", action: func() { p.OpenAgentForm(a.ID) }},
		{label: toggle, action: func() { p.ToggleAgent(a.ID) }},
		{label: "删除", action: func() { p.RequestDeleteAgent(a.ID) }},
	}
}

func (p *AgentPage) agentSubtitle(a Agent) string {
	persona := p.personaSummary(a.ID)
	model := p.agentModelLabel(a)
	if model != "" {
		return persona + " · " + model
	}
	return persona
}

func (p *AgentPage) personaSummary(agentID string) string {
	content, err := p.Agents.Workspace(agentID).Load()
	if err != nil {
		_ = err
	}
	if line := firstContentLine(content.Persona); line != "" {
		return line
	}
	return "（未设置人设）"
}

func (p *AgentPage) agentModelLabel(a Agent) string {
	if a.ProviderID == "" || a.Model == "" {
		return "未配置模型"
	}
	for _, target := range p.chatTargets() {
		if target.ProviderID == a.ProviderID && target.Model == a.Model {
			return target.Label()
		}
	}
	return a.Model
}

func agentAvatar(a Agent, name string) string {
	if avatar := strings.TrimSpace(a.Avatar); avatar != "" {
		return avatar
	}
	return avatarInitial(name)
}

// terminateAgentTasks cancels every pending or running task group that still
// assigns work to the deleted sub agent.
func (p *AgentPage) terminateAgentTasks(id string) {
	if p.Dispatcher == nil || p.Tasks == nil {
		return
	}
	for _, s := range p.Tasks.List() {
		if s.Status.Terminal() || s.Status == task.GroupSummarizing {
			continue
		}
		g, ok := p.Tasks.Get(s.ID)
		if !ok {
			continue
		}
		for i := range g.Tasks {
			if g.Tasks[i].AssigneeID == id && !g.Tasks[i].Status.Terminal() {
				p.Dispatcher.Cancel(g.ID)
				break
			}
		}
	}
}

// menuOpen returns the stable open state of one card menu.
func (p *AgentPage) menuOpen(id string) *bool {
	if p.AgentMenus == nil {
		p.AgentMenus = map[string]*bool{}
	}
	open, ok := p.AgentMenus[id]
	if !ok {
		open = new(bool)
		p.AgentMenus[id] = open
	}
	return open
}

// closeMenus closes every card menu.
func (p *AgentPage) closeMenus() {
	for _, open := range p.AgentMenus {
		*open = false
	}
}

// ToggleAgent enables or disables one sub agent.
func (p *AgentPage) ToggleAgent(id string) {
	if err := p.Agents.SetEnabled(id, !p.subEnabled(id)); err != nil {
		p.ActionError = err.Error()
	}
	p.closeMenus()
	p.repaint()
}

func (p *AgentPage) subEnabled(id string) bool {
	if a, ok := p.Agents.Get(id); ok {
		return a.Enabled
	}
	return false
}

// OpenAgentForm opens the create or edit form of a sub agent. An empty ID
// creates one.
func (p *AgentPage) OpenAgentForm(id string) {
	p.FormOpen = true
	p.FormID = id
	p.FormError = ""
	p.closeMenus()
	if id == "" {
		p.FormName = ""
		p.FormAvatar = ""
		p.FormPersona = ""
		p.FormEnabled = true
		p.FormCopyMain = false
		p.FormTarget = p.defaultTargetLabel()
		if p.FormTarget == "" && len(p.chatTargets()) == 0 {
			p.FormError = "模型未配置，请先在设置中配置对话模型"
		}
		p.repaint()
		return
	}
	a, ok := p.Agents.Get(id)
	if !ok || a.IsMain() {
		p.FormOpen = false
		return
	}
	content, err := p.Agents.Workspace(id).Load()
	if err != nil {
		_ = err
	}
	p.FormName = a.Name
	p.FormAvatar = a.Avatar
	p.FormPersona = strings.TrimSpace(content.Persona)
	p.FormEnabled = a.Enabled
	p.FormCopyMain = false
	p.FormTarget = ""
	for _, target := range p.chatTargets() {
		if target.ProviderID == a.ProviderID && target.Model == a.Model {
			p.FormTarget = target.Label()
			break
		}
	}
	p.repaint()
}

// CloseAgentForm hides the form without saving.
func (p *AgentPage) CloseAgentForm() {
	p.FormOpen = false
	p.FormID = ""
	p.FormError = ""
	p.repaint()
}

// SaveAgentForm validates and stores the form, including PERSONA.md and the
// optional copy of the main agent template.
func (p *AgentPage) SaveAgentForm() {
	chosen, ok := targetByLabel(p.chatTargets(), p.FormTarget)
	if !ok {
		p.FormError = "请选择 Agent 的模型"
		p.repaint()
		return
	}
	persona := strings.TrimSpace(p.FormPersona)
	if err := checkRunes("人设", persona, 0, MaxAgentPersonaRunes); err != nil {
		p.FormError = err.Error()
		p.repaint()
		return
	}
	avatar := strings.TrimSpace(p.FormAvatar)
	if len([]rune(avatar)) > 8 {
		p.FormError = "头像最多 8 个字符"
		p.repaint()
		return
	}
	in := Agent{
		ID:         p.FormID,
		Name:       p.FormName,
		Avatar:     avatar,
		ProviderID: chosen.ProviderID,
		Model:      chosen.Model,
		Enabled:    p.FormEnabled,
	}

	if in.ID == "" {
		created, err := p.Agents.Create(in)
		if err != nil {
			p.FormError = err.Error()
			p.repaint()
			return
		}
		if err := p.writeNewWorkspace(created.ID, persona); err != nil {
			p.ActionError = err.Error()
		}
		p.FormOpen = false
		p.FormID = ""
		p.FormError = ""
		p.SelectAgent(created.ID)
		p.repaint()
		return
	}

	if _, err := p.Agents.Update(in); err != nil {
		p.FormError = err.Error()
		p.repaint()
		return
	}
	if err := p.Agents.Workspace(in.ID).Save(PersonaFileName, persona); err != nil {
		p.FormError = err.Error()
		p.repaint()
		return
	}
	p.FormOpen = false
	p.FormID = ""
	p.FormError = ""
	p.repaint()
}

// writeNewWorkspace seeds the created workspace: the typed persona, and the
// main agent USER.md and PERSONA.md when the copy option is on.
func (p *AgentPage) writeNewWorkspace(id, persona string) error {
	ws := p.Agents.Workspace(id)
	main := p.Agents.Workspace(MainAgentID)
	mainContent, _ := main.Load()

	if p.FormCopyMain {
		text := persona
		if text == "" {
			text = strings.TrimSpace(mainContent.Persona)
		}
		if err := ws.Save(PersonaFileName, text); err != nil {
			return err
		}
		return ws.Save(UserFileName, strings.TrimSpace(mainContent.User))
	}
	if persona != "" {
		return ws.Save(PersonaFileName, persona)
	}
	return nil
}

// RequestDeleteAgent asks for the deletion confirmation of one sub agent.
func (p *AgentPage) RequestDeleteAgent(id string) {
	if a, ok := p.Agents.Get(id); !ok || a.IsMain() {
		return
	}
	p.ConfirmDeleteID = id
	p.closeMenus()
	p.repaint()
}

// DeleteAgent removes the sub agent awaiting confirmation, including its
// sessions and whole workspace.
func (p *AgentPage) DeleteAgent() {
	id := p.ConfirmDeleteID
	p.ConfirmDeleteID = ""
	if id == "" {
		p.repaint()
		return
	}
	if a, ok := p.Agents.Get(id); !ok || a.IsMain() {
		p.repaint()
		return
	}
	p.terminateAgentTasks(id)
	if running := p.Runner.RunningConversation(); running != "" {
		if conv, ok := p.Store.Get(running); ok && normalizeAgentID(conv.AgentID) == id {
			p.Runner.Stop()
		}
	}
	if err := p.Store.DeleteAgent(id); err != nil {
		p.ActionError = err.Error()
		p.repaint()
		return
	}
	if err := p.Agents.Delete(id); err != nil {
		p.ActionError = err.Error()
		p.repaint()
		return
	}
	if p.CurrentAgent == id {
		p.CurrentAgent = ""
		p.CurrentID = ""
	}
	p.repaint()
}

// agentFormModal draws the create or edit form of a sub agent.
func (p *AgentPage) agentFormModal(c *mygoui.Context, t *mygoui.Theme) {
	mygoui.Modal(c, &p.FormOpen, func() {
		title := "新建子 Agent"
		if p.FormID != "" {
			title = "编辑子 Agent"
		}
		mygoui.Text(c, title).FontSize(15).Bold().TextColor(t.Text).Shrink(0)
		mygoui.Text(c, "子 Agent 只能输出文本结论，不会调用工具。").
			FontSize(10).TextColor(t.TextMuted).Wrap()

		mygoui.Text(c, "名称").FontSize(10).TextColor(t.TextMuted).Shrink(0)
		mygoui.TextInput(c, &p.FormName).FillWidth().Placeholder("例如：资料整理")

		mygoui.Text(c, "头像").FontSize(10).TextColor(t.TextMuted).Shrink(0)
		mygoui.TextInput(c, &p.FormAvatar).FillWidth().Placeholder("单字或短文本，留空取名称首字")

		mygoui.Text(c, "人设").FontSize(10).TextColor(t.TextMuted).Shrink(0)
		mygoui.TextArea(c, &p.FormPersona).FillWidth().Lines(2, 8).
			Placeholder("你负责把用户提供的材料整理成要点清单…")

		mygoui.Text(c, "模型").FontSize(10).TextColor(t.TextMuted).Shrink(0)
		labels := p.targetLabels()
		if len(labels) == 0 {
			mygoui.Text(c, "模型未配置，请先在设置中配置对话模型").
				FontSize(11).TextColor(ui.DangerColor(modeOf(t))).Wrap()
		} else {
			mygoui.Select(c, &p.FormTarget, labels).FillWidth()
		}

		if p.FormID == "" {
			mygoui.Checkbox(c, &p.FormCopyMain, "从主 Agent 复制 USER.md、PERSONA.md 作为初始内容")
		}
		mygoui.Checkbox(c, &p.FormEnabled, "启用（可被派发任务）")
		if p.FormError != "" {
			mygoui.Text(c, p.FormError).FontSize(10).
				TextColor(ui.DangerColor(modeOf(t))).Wrap()
		}
		actions := mygoui.Row(c).FillWidth().Gap(8).Justify(mygoui.End)
		actions.Children(func() {
			drawButton(c, t, "取消", false, false, p.CloseAgentForm)
			drawButton(c, t, "保存", true, false, p.SaveAgentForm)
		})
	})
	if !p.FormOpen {
		p.FormID = ""
		p.FormError = ""
	}
}

// deleteModal confirms deleting one sub agent and its whole workspace.
func (p *AgentPage) deleteModal(c *mygoui.Context, t *mygoui.Theme) {
	if p.ConfirmDeleteID == "" {
		return
	}
	open := true
	name := p.ConfirmDeleteID
	if a, ok := p.Agents.Get(p.ConfirmDeleteID); ok {
		name = a.Name
	}
	mygoui.Modal(c, &open, func() {
		mygoui.Text(c, "删除子 Agent「"+name+"」后不可恢复：将终止其运行中的任务，并删除其会话、记忆、USER.md、PERSONA.md 与整个工作区；历史任务保留名称快照。确认删除？").
			FontSize(12).TextColor(t.Text).Wrap()
		actions := mygoui.Row(c).FillWidth().Gap(8).Justify(mygoui.End)
		actions.Children(func() {
			drawButton(c, t, "取消", false, false, func() {
				p.ConfirmDeleteID = ""
				open = false
				p.repaint()
			})
			drawButton(c, t, "确认删除", true, false, func() {
				p.DeleteAgent()
				open = false
			})
		})
	})
	if !open {
		p.ConfirmDeleteID = ""
	}
}

func (p *AgentPage) targetLabels() []string {
	targets := p.chatTargets()
	labels := make([]string, 0, len(targets))
	for _, target := range targets {
		labels = append(labels, target.Label())
	}
	return labels
}

func (p *AgentPage) defaultTargetLabel() string {
	targets := p.chatTargets()
	if len(targets) == 0 {
		return ""
	}
	return targets[0].Label()
}

func (p *AgentPage) chatTargets() []gateway.TargetView {
	if p.Chatter == nil {
		return nil
	}
	return p.Chatter.ChatTargets()
}

func (p *AgentPage) snapshot() gateway.Snapshot {
	if p.Chatter == nil {
		return gateway.Snapshot{}
	}
	return p.Chatter.Snapshot()
}

func (p *AgentPage) repaint() {
	if p.Repaint != nil {
		p.Repaint()
	}
}
