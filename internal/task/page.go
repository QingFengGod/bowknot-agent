package task

import (
	"fmt"
	"strings"
	"time"

	mygoui "github.com/egoist/mygo/ui"

	"github.com/QingFengGod/bowknot-agent/internal/ui"
)

// Actions is the task lifecycle the assistant module provides to the task
// center. The dispatcher implements it, so the page stays independent of the
// agent package.
type Actions interface {
	Confirm(groupID string) error
	Cancel(groupID string)
	Retry(groupID, taskID string) error
	Resume(groupID string) error
	Terminate(groupID string) error
	Delete(groupID string) error
	CleanTerminal() (int, error)
	Busy() bool
}

const (
	listColumnW = 320
	// filterRowGap is the spacing between filter chips.
	filterRowGap = 4
)

// Page is the task center: the group list with status filters and the details
// of the selected group.
type Page struct {
	Tasks   *Store
	Actions Actions
	// Titles returns the title of a conversation; an empty result marks the
	// conversation as deleted.
	Titles func(conversationID string) string
	// OpenConversation switches to the assistant module and selects the
	// conversation of a group.
	OpenConversation func(conversationID string)
	// Repaint repaints the main window after a state change.
	Repaint func()

	// Filter is the group status shown; empty means all statuses.
	Filter GroupStatus
	// Selected is the group whose detail is shown.
	Selected string
	// ConfirmDeleteID is the group awaiting deletion confirmation.
	ConfirmDeleteID string
	// ConfirmClean asks before removing every settled group.
	ConfirmClean bool
	// ActionError is the last local failure, such as a save error.
	ActionError string
	// ListScroll and DetailScroll keep the two panes' positions.
	ListScroll   mygoui.ScrollState
	DetailScroll mygoui.ScrollState
}

// NewPage returns a task center page over the task store.
func NewPage(tasks *Store, actions Actions) *Page {
	if tasks == nil {
		tasks = NewStore("")
	}
	return &Page{Tasks: tasks, Actions: actions}
}

// Select shows the detail of one group.
func (p *Page) Select(id string) {
	p.Selected = id
	p.ConfirmDeleteID = ""
	p.ActionError = ""
	p.DetailScroll.Y = 0
	p.repaint()
}

// SetFilter limits the list to one status; an empty status shows all groups.
func (p *Page) SetFilter(status GroupStatus) {
	p.Filter = status
	p.ListScroll.Y = 0
	p.repaint()
}

// ConfirmGroup runs a pending plan.
func (p *Page) ConfirmGroup(id string) {
	if p.Actions == nil {
		return
	}
	p.run(func() error { return p.Actions.Confirm(id) })
}

// AbandonGroup cancels a pending plan or the running group.
func (p *Page) AbandonGroup(id string) {
	if p.Actions == nil {
		return
	}
	p.Actions.Cancel(id)
	p.ActionError = ""
	p.repaint()
}

// ResumeGroup runs the unfinished tasks of an interrupted group again.
func (p *Page) ResumeGroup(id string) {
	if p.Actions == nil {
		return
	}
	p.run(func() error { return p.Actions.Resume(id) })
}

// TerminateGroup closes an interrupted group without finishing it.
func (p *Page) TerminateGroup(id string) {
	if p.Actions == nil {
		return
	}
	p.run(func() error { return p.Actions.Terminate(id) })
}

// RetryTask queues a new task for a failed one.
func (p *Page) RetryTask(groupID, taskID string) {
	if p.Actions == nil {
		return
	}
	p.run(func() error { return p.Actions.Retry(groupID, taskID) })
}

// RequestDelete asks for confirmation before removing a settled group.
func (p *Page) RequestDelete(id string) {
	p.ConfirmDeleteID = id
	p.ActionError = ""
	p.repaint()
}

// DeleteGroup removes a settled group.
func (p *Page) DeleteGroup(id string) {
	p.ConfirmDeleteID = ""
	if p.Actions == nil {
		return
	}
	p.run(func() error { return p.Actions.Delete(id) })
	if _, ok := p.Tasks.Get(id); !ok && p.Selected == id {
		p.Selected = ""
	}
}

// RequestClean asks for confirmation before removing every settled group.
func (p *Page) RequestClean() {
	p.ConfirmClean = true
	p.ActionError = ""
	p.repaint()
}

// Clean removes every settled group.
func (p *Page) Clean() {
	p.ConfirmClean = false
	if p.Actions == nil {
		return
	}
	var removed int
	var err error
	p.run(func() error {
		removed, err = p.Actions.CleanTerminal()
		return err
	})
	if err == nil && removed > 0 && p.Selected != "" {
		if _, ok := p.Tasks.Get(p.Selected); !ok {
			p.Selected = ""
		}
	}
}

func (p *Page) run(action func() error) {
	if err := action(); err != nil {
		p.ActionError = err.Error()
	} else {
		p.ActionError = ""
	}
	p.repaint()
}

func (p *Page) busy() bool {
	return p.Actions != nil && p.Actions.Busy()
}

func (p *Page) repaint() {
	if p.Repaint != nil {
		p.Repaint()
	}
}

// View returns the view of the task module.
func (p *Page) View() func(c *mygoui.Context) {
	return func(c *mygoui.Context) {
		t := c.Theme()
		mygoui.Row(c).Fill().Background(t.Background).Children(func() {
			p.listColumn(c, t)
			p.detailColumn(c, t)
		})
	}
}

func (p *Page) listColumn(c *mygoui.Context, t *mygoui.Theme) {
	col := mygoui.Column(c).Width(listColumnW).Shrink(0).FillHeight().Padding(12).Gap(8).
		Background(t.Surface).BorderWidth(0, 0, 0, 1).BorderColor(t.Border)
	col.Children(func() {
		head := mygoui.Row(c).FillWidth().Shrink(0).AlignItems(mygoui.Center)
		head.Children(func() {
			mygoui.Text(c, "任务中心").FontSize(14).Bold().TextColor(t.Text).Grow(1)
			mygoui.Text(c, fmt.Sprintf("%d / %d", p.Tasks.Count(), MaxGroups)).
				FontSize(10).TextColor(t.TextMuted).Shrink(0)
		})
		p.filters(c, t)
		p.cleanRow(c, t)
		if p.ConfirmClean {
			p.cleanConfirm(c, t)
		}
		p.groupList(c, t)
	})
}

func (p *Page) filters(c *mygoui.Context, t *mygoui.Theme) {
	rows := [][]struct {
		status GroupStatus
		text   string
	}{
		{{"", "全部"}, {GroupPending, "待确认"}, {GroupRunning, "执行中"}, {GroupSummarizing, "汇总中"}},
		{{GroupDone, "已完成"}, {GroupFailed, "已失败"}, {GroupInterrupted, "已中断"}, {GroupCanceled, "已取消"}},
	}
	for _, row := range rows {
		r := mygoui.Row(c).FillWidth().Shrink(0).Gap(filterRowGap).AlignItems(mygoui.Center)
		r.Children(func() {
			for _, item := range row {
				p.filterChip(c, t, item.status, item.text)
			}
		})
	}
}

func (p *Page) filterChip(c *mygoui.Context, t *mygoui.Theme, status GroupStatus, text string) {
	selected := p.Filter == status
	b := mygoui.ButtonBase(c).Label("筛选："+text).Shrink(0).Padding(3, 7).
		Radius(t.Radius).Selected(selected)
	if selected {
		b.Background(ui.AccentTint())
	} else if b.Hovered() {
		b.Background(t.SurfaceHover)
	}
	b.Children(func() {
		color := t.TextMuted
		if selected {
			color = t.Accent
		}
		mygoui.Text(c, text).FontSize(10).TextColor(color)
	})
	if b.Clicked() {
		p.SetFilter(status)
	}
}

func (p *Page) cleanRow(c *mygoui.Context, t *mygoui.Theme) {
	row := mygoui.Row(c).FillWidth().Shrink(0).AlignItems(mygoui.Center)
	row.Children(func() {
		mygoui.Spacer(c).Grow(1)
		p.textButton(c, t, "清理已结束任务", p.RequestClean)
	})
}

func (p *Page) cleanConfirm(c *mygoui.Context, t *mygoui.Theme) {
	box := mygoui.Column(c).FillWidth().Shrink(0).Padding(8).Gap(6).Radius(t.Radius).
		Background(t.Background).BorderWidth(1).BorderColor(t.Border)
	box.Children(func() {
		mygoui.Text(c, "将删除所有已完成、已失败、已取消与已中断的任务计划，不可恢复。").
			FontSize(10).TextColor(t.Text).Wrap()
		actions := mygoui.Row(c).Gap(8).AlignItems(mygoui.Center)
		actions.Children(func() {
			p.button(c, t, "取消", false, false, func() {
				p.ConfirmClean = false
				p.repaint()
			})
			p.button(c, t, "确认清理", true, p.busy(), p.Clean)
		})
	})
}

func (p *Page) groupList(c *mygoui.Context, t *mygoui.Theme) {
	list := p.visibleGroups()
	scroll := mygoui.Scroll(c).FillWidth().Grow(1).FillHeight().TrackScroll(&p.ListScroll)
	scroll.Children(func() {
		col := mygoui.Column(c).FillWidth().Gap(2)
		col.Children(func() {
			if len(list) == 0 {
				mygoui.Text(c, "没有符合条件的任务计划").FontSize(11).
					TextColor(t.TextMuted).Wrap()
				return
			}
			for _, item := range list {
				p.groupRow(c, t, item)
			}
		})
	})
}

// visibleGroups returns the summaries matching the current filter, newest
// first.
func (p *Page) visibleGroups() []Summary {
	list := p.Tasks.List()
	if p.Filter == "" {
		return list
	}
	out := make([]Summary, 0, len(list))
	for _, item := range list {
		if item.Status == p.Filter {
			out = append(out, item)
		}
	}
	return out
}

func (p *Page) groupRow(c *mygoui.Context, t *mygoui.Theme, item Summary) {
	selected := item.ID == p.Selected
	b := mygoui.ButtonBase(c).Label("任务计划："+item.ID).FillWidth().Shrink(0).
		Padding(6, 8).Radius(t.Radius).AlignItems(mygoui.Start)
	if selected {
		b.Background(ui.AccentTint())
	} else if b.Pressed() {
		b.Background(t.SurfacePressed)
	} else if b.Hovered() {
		b.Background(t.SurfaceHover)
	}
	b.Children(func() {
		col := mygoui.Column(c).Grow(1).Gap(1)
		col.Children(func() {
			top := mygoui.Row(c).FillWidth().Gap(6).AlignItems(mygoui.Center)
			top.Children(func() {
				mygoui.Text(c, fmt.Sprintf("任务计划 %d 项", item.Tasks)).FontSize(12).
					Bold().TextColor(t.Text).Grow(1).SingleLine()
				mygoui.Text(c, item.Status.Text()).FontSize(10).
					TextColor(p.groupStatusColor(t, item.Status)).Shrink(0)
			})
			mygoui.Text(c, p.conversationTitle(item.ConversationID)).FontSize(10).
				TextColor(t.TextMuted).SingleLine()
			mygoui.Text(c, formatTime(item.UpdatedAt)).FontSize(10).
				TextColor(t.TextMuted).SingleLine()
		})
	})
	if b.Clicked() {
		p.Select(item.ID)
	}
}

func (p *Page) detailColumn(c *mygoui.Context, t *mygoui.Theme) {
	col := mygoui.Column(c).Grow(1).FillHeight().Padding(16).Gap(8)
	col.Children(func() {
		g, ok := p.Tasks.Get(p.Selected)
		if !ok {
			p.emptyDetail(c, t)
			return
		}
		p.detailHeader(c, t, g)
		p.detailActions(c, t, g)
		if p.ConfirmDeleteID == g.ID {
			p.deleteConfirm(c, t, g)
		}
		if p.ActionError != "" {
			mygoui.Text(c, p.ActionError).FontSize(11).
				TextColor(ui.DangerColor(p.mode(t))).Wrap()
		}
		if g.Summary != "" {
			p.summaryCard(c, t, g)
		}
		p.detailTasks(c, t, g)
	})
}

func (p *Page) emptyDetail(c *mygoui.Context, t *mygoui.Theme) {
	box := mygoui.Box(c).FillWidth().Grow(1).Center()
	box.Children(func() {
		inner := mygoui.Column(c).Gap(8).AlignItems(mygoui.Center).MaxWidth(380)
		inner.Children(func() {
			mygoui.Icon(c, ui.TaskIcon).Size(32, 32)
			mygoui.Text(c, "选择一个任务计划查看详情").FontSize(15).Bold().TextColor(t.Text)
			mygoui.Text(c, "在助手模块开启任务模式并发送目标，任务计划会出现在左侧列表。").
				FontSize(12).TextColor(t.TextMuted).Wrap()
		})
	})
}

func (p *Page) detailHeader(c *mygoui.Context, t *mygoui.Theme, g Group) {
	head := mygoui.Column(c).FillWidth().Shrink(0).Gap(3)
	head.Children(func() {
		titleRow := mygoui.Row(c).FillWidth().Gap(8).AlignItems(mygoui.Center)
		titleRow.Children(func() {
			mygoui.Text(c, fmt.Sprintf("任务计划 %d 项", len(g.Tasks))).FontSize(16).
				Bold().TextColor(t.Text).Shrink(0)
			mygoui.Text(c, g.Status.Text()).FontSize(11).
				TextColor(p.groupStatusColor(t, g.Status)).Shrink(0)
			mygoui.Spacer(c).Grow(1)
			if p.OpenConversation != nil {
				p.textButton(c, t, "打开会话", func() { p.OpenConversation(g.ConversationID) })
			}
		})
		mygoui.Text(c, p.conversationTitle(g.ConversationID)).FontSize(11).
			TextColor(t.TextMuted).SingleLine()
		mygoui.Text(c, fmt.Sprintf("创建 %s · 更新 %s · %s / %s",
			formatTime(g.CreatedAt), formatTime(g.UpdatedAt), g.ProviderID, g.Model)).
			FontSize(10).TextColor(t.TextMuted).SingleLine()
	})
}

func (p *Page) detailActions(c *mygoui.Context, t *mygoui.Theme, g Group) {
	row := mygoui.Row(c).FillWidth().Shrink(0).Gap(8).AlignItems(mygoui.Center)
	row.Children(func() {
		switch g.Status {
		case GroupPending:
			p.button(c, t, "执行", true, p.busy(), func() { p.ConfirmGroup(g.ID) })
			p.button(c, t, "放弃", false, false, func() { p.AbandonGroup(g.ID) })
		case GroupRunning:
			p.button(c, t, "取消全部", false, false, func() { p.AbandonGroup(g.ID) })
		case GroupSummarizing:
			mygoui.Text(c, "正在汇总…").FontSize(11).TextColor(t.TextMuted).Shrink(0)
		case GroupInterrupted:
			p.button(c, t, "继续", true, p.busy(), func() { p.ResumeGroup(g.ID) })
			p.button(c, t, "终止", false, false, func() { p.TerminateGroup(g.ID) })
			p.button(c, t, "删除", false, false, func() { p.RequestDelete(g.ID) })
		default:
			p.button(c, t, "删除", false, false, func() { p.RequestDelete(g.ID) })
		}
	})
	if g.Status == GroupInterrupted {
		mygoui.Text(c, "任务因应用退出而中断，可继续执行剩余任务或终止。").
			FontSize(10).TextColor(t.TextMuted).Wrap()
	}
}

func (p *Page) deleteConfirm(c *mygoui.Context, t *mygoui.Theme, g Group) {
	box := mygoui.Column(c).FillWidth().Shrink(0).Padding(8).Gap(6).Radius(t.Radius).
		Background(t.Surface).BorderWidth(1).BorderColor(t.Border)
	box.Children(func() {
		mygoui.Text(c, "删除该任务计划后不可恢复，确认删除？").FontSize(11).
			TextColor(t.Text).Shrink(0).SingleLine()
		actions := mygoui.Row(c).Gap(8).AlignItems(mygoui.Center)
		actions.Children(func() {
			p.button(c, t, "取消", false, false, func() {
				p.ConfirmDeleteID = ""
				p.repaint()
			})
			p.button(c, t, "确认删除", true, false, func() { p.DeleteGroup(g.ID) })
		})
	})
}

func (p *Page) summaryCard(c *mygoui.Context, t *mygoui.Theme, g Group) {
	card := mygoui.Column(c).FillWidth().Shrink(0).Padding(10, 12).Gap(4).
		Radius(t.Radius).Background(t.Surface).BorderWidth(1).BorderColor(t.Border)
	card.Children(func() {
		mygoui.Text(c, "主 Agent 汇总").FontSize(11).Bold().TextColor(t.TextMuted)
		mygoui.Text(c, g.Summary).FontSize(13).TextColor(t.Text).Wrap()
	})
}

func (p *Page) detailTasks(c *mygoui.Context, t *mygoui.Theme, g Group) {
	done := 0
	for i := range g.Tasks {
		if g.Tasks[i].Status == TaskDone {
			done++
		}
	}
	mygoui.Text(c, fmt.Sprintf("任务 %d 项，已完成 %d 项", len(g.Tasks), done)).
		FontSize(11).Bold().TextColor(t.TextMuted).Shrink(0)
	scroll := mygoui.Scroll(c).FillWidth().Grow(1).FillHeight().TrackScroll(&p.DetailScroll)
	scroll.Children(func() {
		col := mygoui.Column(c).FillWidth().Gap(6)
		col.Children(func() {
			for i := range g.Tasks {
				p.taskCard(c, t, g, i)
			}
		})
	})
}

func (p *Page) taskCard(c *mygoui.Context, t *mygoui.Theme, g Group, index int) {
	tk := g.Tasks[index]
	card := mygoui.Column(c).FillWidth().Shrink(0).Padding(8, 10).Gap(3).
		Radius(t.Radius).Background(t.Surface).BorderWidth(1).BorderColor(t.Border)
	card.Children(func() {
		top := mygoui.Row(c).FillWidth().Gap(6).AlignItems(mygoui.Center)
		top.Children(func() {
			mygoui.Text(c, fmt.Sprintf("%d. %s", index+1, tk.Goal)).FontSize(12).
				TextColor(t.Text).Wrap().Grow(1)
			mygoui.Text(c, tk.Status.Text()).FontSize(10).
				TextColor(p.taskStatusColor(t, tk.Status)).Shrink(0)
			if p.canRetry(g, tk) {
				p.textButton(c, t, "重试", func() { p.RetryTask(g.ID, tk.ID) })
			}
		})
		mygoui.Text(c, p.taskMeta(tk)).FontSize(10).TextColor(t.TextMuted).SingleLine()
		if tk.Input != "" {
			mygoui.Text(c, "输入："+tk.Input).FontSize(10).TextColor(t.TextMuted).MaxLines(3)
		}
		if tk.Criteria != "" {
			mygoui.Text(c, "完成标准："+tk.Criteria).FontSize(10).
				TextColor(t.TextMuted).MaxLines(2)
		}
		if tk.Status == TaskFailed {
			mygoui.Text(c, taskErrorText(tk)).FontSize(10).
				TextColor(ui.DangerColor(p.mode(t))).Wrap()
		}
		if tk.Report != nil {
			p.reportView(c, t, *tk.Report)
		}
	})
}

func (p *Page) reportView(c *mygoui.Context, t *mygoui.Theme, r Report) {
	mygoui.Text(c, "结论："+r.Conclusion).FontSize(11).TextColor(t.Text).Wrap()
	for _, artifact := range r.Artifacts {
		mygoui.Text(c, "产出物："+artifact).FontSize(10).TextColor(t.TextMuted).Wrap()
	}
	for _, issue := range r.OpenIssues {
		mygoui.Text(c, "未解决："+issue).FontSize(10).
			TextColor(ui.DangerColor(p.mode(t))).Wrap()
	}
	if r.Summary != "" {
		mygoui.Text(c, "过程："+r.Summary).FontSize(10).TextColor(t.TextMuted).MaxLines(3)
	}
	if r.Unstructured {
		mygoui.Text(c, "未按结构返回，已按纯文本收录").FontSize(10).
			TextColor(t.TextMuted).SingleLine()
	}
}

// canRetry reports whether a failed task may be queued again: the group must
// not be canceled or interrupted, and no dispatch may be running.
func (p *Page) canRetry(g Group, tk Task) bool {
	if tk.Status != TaskFailed {
		return false
	}
	if g.Status == GroupCanceled || g.Status == GroupInterrupted {
		return false
	}
	return !p.busy()
}

func (p *Page) taskMeta(tk Task) string {
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

func taskErrorText(tk Task) string {
	text := "失败"
	if tk.ErrorCode != "" {
		text += " · " + tk.ErrorCode
	}
	if tk.ErrorMessage != "" {
		text += " · " + tk.ErrorMessage
	}
	return text
}

func (p *Page) conversationTitle(id string) string {
	if id == "" {
		return "未知会话"
	}
	if p.Titles == nil {
		return id
	}
	if title := p.Titles(id); title != "" {
		return title
	}
	return "原会话已删除（" + id + "）"
}

func (p *Page) groupStatusColor(t *mygoui.Theme, s GroupStatus) mygoui.Color {
	switch s {
	case GroupDone:
		return ui.SuccessColor(p.mode(t))
	case GroupFailed:
		return ui.DangerColor(p.mode(t))
	case GroupRunning, GroupSummarizing:
		return t.Accent
	default:
		return t.TextMuted
	}
}

func (p *Page) taskStatusColor(t *mygoui.Theme, s TaskStatus) mygoui.Color {
	switch s {
	case TaskDone:
		return ui.SuccessColor(p.mode(t))
	case TaskFailed:
		return ui.DangerColor(p.mode(t))
	case TaskRunning:
		return t.Accent
	default:
		return t.TextMuted
	}
}

func (p *Page) mode(t *mygoui.Theme) ui.Mode {
	if t.Dark {
		return ui.ModeDark
	}
	return ui.ModeLight
}

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

func formatDuration(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	if d < time.Second {
		return fmt.Sprintf("%dms", ms)
	}
	return d.Round(100 * time.Millisecond).String()
}

func formatTime(unix int64) string {
	if unix <= 0 {
		return "—"
	}
	return time.Unix(unix, 0).Format("01-02 15:04")
}
