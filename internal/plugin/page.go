package plugin

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo"
	mygoui "github.com/egoist/mygo/ui"

	"github.com/QingFengGod/bowknot-agent/internal/ui"
)

// AgentView is one agent row of the enable section.
type AgentView struct {
	ID   string
	Name string
	// Main marks the fixed main agent.
	Main bool
}

// Agents supplies the agent list and the per-agent enable lists the page
// manages. The app injects an adapter over the agent registry.
type Agents interface {
	Views() []AgentView
	EnabledPlugins(agentID string) []string
	SetEnabledPlugins(agentID string, ids []string) error
}

// Page is the plugin module: the installed list, the local import, the
// per-agent enable lists and the uninstall confirmation. The app holds one
// Page for the life of the process, so the selected agent and the notices
// survive switching modules.
type Page struct {
	Store  *Store
	Agents Agents
	// Repaint repaints the main window after a state change.
	Repaint func()

	// AgentChoice is the agent name selected in the enable section; the ID is
	// resolved from the views on use.
	AgentChoice string
	// ImportOpen shows the import confirmation for a validated source.
	ImportOpen bool
	// ImportSource is the path of the pending import.
	ImportSource string
	// ImportDirectory installs a folder instead of a .bkplugin package.
	ImportDirectory bool
	// ImportManifest is the validated manifest of the pending import.
	ImportManifest *Manifest
	// ConfirmUninstall is the plugin ID awaiting the uninstall confirmation.
	ConfirmUninstall string
	// Notice is the last successful action, Error the last failure.
	Notice string
	Error  string
}

// NewPage creates the plugin page. At startup the enable lists are reconciled
// with the installed plugins: entries that no longer exist or fail validation
// are removed from every agent (FR-PLG-04) and reported.
func NewPage(store *Store, agents Agents) *Page {
	p := &Page{Store: store, Agents: agents}
	p.reconcile()
	return p
}

// View returns the view of the plugin module.
func (p *Page) View() func(c *mygoui.Context) {
	return func(c *mygoui.Context) {
		t := c.Theme()
		col := mygoui.Column(c).Fill().Padding(20).Gap(12)
		col.Children(func() {
			p.header(c, t)
			p.message(c, t)
			items := p.Store.List()
			if len(items) == 0 {
				p.emptyState(c, t)
			} else {
				p.installedList(c, t, items)
			}
			p.enableSection(c, t, items)
		})
		p.importModal(c, t)
		p.uninstallModal(c, t)
	}
}

// reconcile removes plugins that no longer validate from every agent enable
// list and keeps the result as the startup notice.
func (p *Page) reconcile() {
	if p.Agents == nil {
		return
	}
	valid := map[string]bool{}
	for _, item := range p.Store.List() {
		if item.Valid {
			valid[item.Entry.ID] = true
		}
	}
	for _, av := range p.Agents.Views() {
		ids := p.Agents.EnabledPlugins(av.ID)
		kept := make([]string, 0, len(ids))
		removed := make([]string, 0, 2)
		for _, id := range ids {
			if valid[id] {
				kept = append(kept, id)
			} else {
				removed = append(removed, id)
			}
		}
		if len(removed) == 0 {
			continue
		}
		if err := p.Agents.SetEnabledPlugins(av.ID, kept); err != nil {
			p.Error = err.Error()
			continue
		}
		p.Notice += fmt.Sprintf("已从 %s 移除失效插件 %s，请重新导入；", av.Name, strings.Join(removed, "、"))
	}
}

func (p *Page) header(c *mygoui.Context, t *mygoui.Theme) {
	row := mygoui.Row(c).FillWidth().Shrink(0).Gap(8).AlignItems(mygoui.Center)
	row.Children(func() {
		head := mygoui.Column(c).Grow(1).Gap(2)
		head.Children(func() {
			mygoui.Text(c, "插件").FontSize(17).Bold().TextColor(t.Text).SingleLine()
			items := p.Store.List()
			mygoui.Text(c, fmt.Sprintf("共 %d 个插件，导入后可为每个 Agent 启用", len(items))).
				FontSize(11).TextColor(t.TextMuted).SingleLine()
		})
		drawButton(c, t, "导入目录", false, false, func() { p.importFromPicker(true) })
		drawButton(c, t, "导入 .bkplugin", true, false, func() { p.importFromPicker(false) })
	})
}

func (p *Page) message(c *mygoui.Context, t *mygoui.Theme) {
	if p.Error != "" {
		mygoui.Text(c, p.Error).FontSize(11).TextColor(ui.DangerColor(modeOf(t))).Wrap()
	}
	if p.Notice != "" {
		mygoui.Text(c, p.Notice).FontSize(11).TextColor(t.TextMuted).Wrap()
	}
}

func (p *Page) emptyState(c *mygoui.Context, t *mygoui.Theme) {
	box := mygoui.Box(c).FillWidth().Grow(1).Center()
	box.Children(func() {
		col := mygoui.Column(c).Gap(8).AlignItems(mygoui.Center).MaxWidth(380)
		col.Children(func() {
			mygoui.Icon(c, ui.PluginIcon).Size(32, 32)
			mygoui.Text(c, "还没有已安装的插件").FontSize(15).Bold().TextColor(t.Text)
			mygoui.Text(c, "插件是声明式的技能与工具组合：选择插件目录或 .bkplugin 安装包导入，安装后按 Agent 启用。").
				FontSize(11).TextColor(t.TextMuted).Wrap()
		})
	})
}

func (p *Page) installedList(c *mygoui.Context, t *mygoui.Theme, items []Item) {
	scroll := mygoui.Scroll(c).FillWidth().Grow(1)
	scroll.Children(func() {
		col := mygoui.Column(c).FillWidth().Gap(8)
		col.Children(func() {
			for _, item := range items {
				p.pluginCard(c, t, item)
			}
		})
	})
}

func (p *Page) pluginCard(c *mygoui.Context, t *mygoui.Theme, item Item) {
	e := item.Entry
	card := mygoui.Column(c).FillWidth().Shrink(0).Padding(12).Gap(6).Radius(t.Radius).
		Background(t.Surface).BorderWidth(1).BorderColor(t.Border)
	card.Children(func() {
		top := mygoui.Row(c).FillWidth().Gap(8).AlignItems(mygoui.Center)
		top.Children(func() {
			mygoui.Text(c, e.Name).FontSize(13).Bold().TextColor(t.Text).SingleLine()
			mygoui.Text(c, "v"+e.Version).FontSize(10).TextColor(t.TextMuted).Shrink(0)
			if !item.Valid {
				mygoui.Text(c, "已损坏，请重新导入").FontSize(9).Shrink(0).
					TextColor(ui.DangerColor(modeOf(t)))
			}
			mygoui.Spacer(c)
			drawButton(c, t, "卸载", false, false, func() { p.RequestUninstall(e.ID) })
		})
		mygoui.Text(c, e.Description).FontSize(11).TextColor(t.Text).Wrap().MaxLines(2)
		meta := fmt.Sprintf("%s · %d 个技能 · 安装于 %s", e.Author, len(e.Skills),
			time.Unix(e.InstalledAt, 0).Format("2006-01-02"))
		if len(e.Tools) > 0 {
			meta += " · 工具：" + strings.Join(e.Tools, "、")
		}
		mygoui.Text(c, meta).FontSize(10).TextColor(t.TextMuted).Wrap()
	})
}

// enableSection is the per-agent enable panel: one selected agent, one toggle
// per installed plugin.
func (p *Page) enableSection(c *mygoui.Context, t *mygoui.Theme, items []Item) {
	panel := mygoui.Column(c).FillWidth().Shrink(0).Padding(12).Gap(8).Radius(t.Radius).
		Background(t.Surface).BorderWidth(1).BorderColor(t.Border)
	panel.Children(func() {
		head := mygoui.Row(c).FillWidth().Gap(8).AlignItems(mygoui.Center)
		head.Children(func() {
			mygoui.Text(c, "按 Agent 启用").FontSize(13).Bold().TextColor(t.Text).Grow(1)
			mygoui.Text(c, "变更在下一轮对话生效").FontSize(10).TextColor(t.TextMuted).Shrink(0)
		})
		views := p.agentViews()
		if len(views) == 0 {
			mygoui.Text(c, "没有可管理的 Agent").FontSize(11).TextColor(t.TextMuted).Wrap()
			return
		}
		if p.AgentChoice == "" || p.indexOfAgent(views, p.AgentChoice) < 0 {
			p.AgentChoice = views[0].Name
		}
		mygoui.Select(c, &p.AgentChoice, p.agentNames(views)).FillWidth()
		agentID := views[p.indexOfAgent(views, p.AgentChoice)].ID
		enabled := map[string]bool{}
		for _, id := range p.Agents.EnabledPlugins(agentID) {
			enabled[id] = true
		}
		if len(items) == 0 {
			mygoui.Text(c, "还没有可启用的插件").FontSize(11).TextColor(t.TextMuted).Wrap()
			return
		}
		for _, item := range items {
			p.enableRow(c, t, agentID, item, enabled[item.Entry.ID])
		}
	})
}

// enableRow is one plugin row of the selected agent; clicking toggles it.
func (p *Page) enableRow(c *mygoui.Context, t *mygoui.Theme, agentID string, item Item, enabled bool) {
	state := "未启用"
	stateColor := t.TextMuted
	if enabled {
		state = "已启用"
		stateColor = t.Accent
	}
	b := mygoui.ButtonBase(c).Label("切换启用 "+item.Entry.Name).FillWidth().Shrink(0).Padding(6, 8).Radius(t.Radius).
		AlignItems(mygoui.Center).Justify(mygoui.Start)
	if b.Hovered() {
		b.Background(t.SurfaceHover)
	}
	b.Children(func() {
		col := mygoui.Column(c).Grow(1).Gap(1)
		col.Children(func() {
			mygoui.Text(c, item.Entry.Name+" · v"+item.Entry.Version).FontSize(12).
				TextColor(t.Text).SingleLine()
			if !item.Valid {
				mygoui.Text(c, "已损坏，重新导入后才能启用").FontSize(9).
					TextColor(ui.DangerColor(modeOf(t))).SingleLine()
			}
		})
		mygoui.Text(c, state).FontSize(11).TextColor(stateColor).Shrink(0)
	})
	if b.Clicked() {
		p.TogglePlugin(agentID, item.Entry.ID, !enabled)
	}
}

// TogglePlugin enables or disables one plugin for one agent. The list order is
// preserved so injected skills keep their order.
func (p *Page) TogglePlugin(agentID, pluginID string, enable bool) {
	ids := p.Agents.EnabledPlugins(agentID)
	next := make([]string, 0, len(ids)+1)
	has := false
	for _, id := range ids {
		if id == pluginID {
			has = true
			if enable {
				next = append(next, id)
			}
			continue
		}
		next = append(next, id)
	}
	if enable && !has {
		next = append(next, pluginID)
	}
	if err := p.Agents.SetEnabledPlugins(agentID, next); err != nil {
		p.Error = err.Error()
		p.repaint()
		return
	}
	p.Error = ""
	p.Notice = ""
	p.repaint()
}

// importFromPicker asks for a .bkplugin package or a plugin directory and
// validates it into the pending import.
func (p *Page) importFromPicker(directory bool) {
	path, err := pickImportSource(directory)
	if err != nil {
		p.Error = "打开选择器失败：" + err.Error()
		p.repaint()
		return
	}
	if path == "" {
		return
	}
	p.beginImport(path, directory)
}

func pickImportSource(directory bool) (string, error) {
	opts := mygo.OpenDialogOptions{
		Title:       "选择要导入的插件目录",
		ButtonLabel: "导入",
		Directory:   directory,
	}
	if !directory {
		opts.Title = "选择 .bkplugin 安装包"
		opts.Filters = []mygo.FileFilter{{Name: "Bowknot 插件", Extensions: []string{"bkplugin"}}}
	}
	paths, err := mygo.Dialog.Open(opts)
	if err != nil {
		return "", err
	}
	if len(paths) == 0 {
		return "", nil
	}
	return paths[0], nil
}

// beginImport validates the source and opens the confirmation. A downgrade of
// an installed plugin is rejected here with the reason (FR-PLG-11).
func (p *Page) beginImport(path string, directory bool) {
	manifest, err := p.Store.Inspect(path, directory)
	if err != nil {
		p.Error = err.Error()
		p.repaint()
		return
	}
	if existing, ok := p.Store.Get(manifest.ID); ok {
		if CompareVersions(manifest.Version, existing.Version) < 0 {
			p.Error = fmt.Sprintf("插件 %s 已安装 v%s，不能降级导入 v%s", existing.Name, existing.Version, manifest.Version)
			p.repaint()
			return
		}
	}
	p.ImportOpen = true
	p.ImportSource = path
	p.ImportDirectory = directory
	p.ImportManifest = manifest
	p.Error = ""
	p.repaint()
}

// CancelImport closes the confirmation without installing.
func (p *Page) CancelImport() {
	p.ImportOpen = false
	p.ImportSource = ""
	p.ImportDirectory = false
	p.ImportManifest = nil
	p.repaint()
}

// ConfirmImport installs the pending source.
func (p *Page) ConfirmImport() {
	path, directory, manifest := p.ImportSource, p.ImportDirectory, p.ImportManifest
	p.CancelImport()
	if manifest == nil {
		return
	}
	entry, err := p.Store.Install(path, directory)
	if err != nil {
		p.Error = err.Error()
		p.repaint()
		return
	}
	kind := "已导入"
	if existing, ok := p.Store.Get(entry.ID); ok && existing.Version != entry.Version {
		kind = "已升级"
	}
	p.Notice = kind + " " + entry.Name + " v" + entry.Version
	p.Error = ""
	p.repaint()
}

// RequestUninstall asks for the uninstall confirmation of one plugin.
func (p *Page) RequestUninstall(id string) {
	if _, ok := p.Store.Get(id); !ok {
		return
	}
	p.ConfirmUninstall = id
	p.repaint()
}

// enabledAgents returns the agents that still enable the plugin.
func (p *Page) enabledAgents(pluginID string) []AgentView {
	var out []AgentView
	for _, av := range p.agentViews() {
		for _, id := range p.Agents.EnabledPlugins(av.ID) {
			if id == pluginID {
				out = append(out, av)
				break
			}
		}
	}
	return out
}

// CancelUninstall closes the confirmation without uninstalling.
func (p *Page) CancelUninstall() {
	p.ConfirmUninstall = ""
	p.repaint()
}

// ConfirmUninstallAction removes the plugin, its registry entry and its ID
// from every agent enable list. The audit log is kept.
func (p *Page) ConfirmUninstallAction() {
	id := p.ConfirmUninstall
	p.ConfirmUninstall = ""
	if id == "" {
		p.repaint()
		return
	}
	entry, ok := p.Store.Get(id)
	if !ok {
		p.repaint()
		return
	}
	for _, av := range p.enabledAgents(id) {
		ids := p.Agents.EnabledPlugins(av.ID)
		kept := make([]string, 0, len(ids))
		for _, pid := range ids {
			if pid != id {
				kept = append(kept, pid)
			}
		}
		if err := p.Agents.SetEnabledPlugins(av.ID, kept); err != nil {
			p.Error = err.Error()
			p.repaint()
			return
		}
	}
	if err := p.Store.Uninstall(id); err != nil {
		p.Error = err.Error()
		p.repaint()
		return
	}
	p.Notice = "已卸载 " + entry.Name + " v" + entry.Version
	p.Error = ""
	p.repaint()
}

func (p *Page) importModal(c *mygoui.Context, t *mygoui.Theme) {
	if !p.ImportOpen {
		return
	}
	manifest := p.ImportManifest
	mygoui.Modal(c, &p.ImportOpen, func() {
		if manifest == nil {
			return
		}
		existing, hasExisting := p.Store.Get(manifest.ID)
		var text string
		if hasExisting {
			switch CompareVersions(manifest.Version, existing.Version) {
			case 0:
				text = fmt.Sprintf("插件 %s 已安装 v%s，将覆盖为 v%s。", existing.Name, existing.Version, manifest.Version)
			default:
				text = fmt.Sprintf("插件 %s 将从 v%s 升级到 v%s。", existing.Name, existing.Version, manifest.Version)
			}
		} else {
			text = fmt.Sprintf("将导入插件 %s v%s，可为每个 Agent 启用。", manifest.Name, manifest.Version)
		}
		mygoui.Text(c, text).FontSize(12).TextColor(t.Text).Wrap()
		mygoui.Text(c, fmt.Sprintf("%d 个技能 · %s", len(manifest.Skills), manifest.Description)).
			FontSize(10).TextColor(t.TextMuted).Wrap()
		actions := mygoui.Row(c).FillWidth().Gap(8).Justify(mygoui.End)
		actions.Children(func() {
			drawButton(c, t, "取消", false, false, p.CancelImport)
			drawButton(c, t, "确认导入", true, false, p.ConfirmImport)
		})
	})
	if !p.ImportOpen {
		p.ImportSource = ""
		p.ImportDirectory = false
		p.ImportManifest = nil
	}
}

func (p *Page) uninstallModal(c *mygoui.Context, t *mygoui.Theme) {
	if p.ConfirmUninstall == "" {
		return
	}
	open := true
	id := p.ConfirmUninstall
	entry, ok := p.Store.Get(id)
	if !ok {
		p.ConfirmUninstall = ""
		return
	}
	mygoui.Modal(c, &open, func() {
		text := fmt.Sprintf("卸载 %s v%s 将删除插件文件与注册记录，历史审计与调用记录保留。", entry.Name, entry.Version)
		if users := p.enabledAgents(id); len(users) > 0 {
			names := make([]string, 0, len(users))
			for _, av := range users {
				names = append(names, av.Name)
			}
			text += fmt.Sprintf("该插件仍被 %s 启用，卸载后将从这些 Agent 移除。", strings.Join(names, "、"))
		}
		mygoui.Text(c, text).FontSize(12).TextColor(t.Text).Wrap()
		actions := mygoui.Row(c).FillWidth().Gap(8).Justify(mygoui.End)
		actions.Children(func() {
			drawButton(c, t, "取消", false, false, p.CancelUninstall)
			drawButton(c, t, "确认卸载", true, false, p.ConfirmUninstallAction)
		})
	})
	if !open {
		p.ConfirmUninstall = ""
	}
}

func (p *Page) agentViews() []AgentView {
	if p.Agents == nil {
		return nil
	}
	return p.Agents.Views()
}

func (p *Page) agentNames(views []AgentView) []string {
	out := make([]string, 0, len(views))
	for _, av := range views {
		out = append(out, av.Name)
	}
	return out
}

func (p *Page) indexOfAgent(views []AgentView, name string) int {
	for i, av := range views {
		if av.Name == name {
			return i
		}
	}
	return -1
}

func (p *Page) repaint() {
	if p.Repaint != nil {
		p.Repaint()
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
