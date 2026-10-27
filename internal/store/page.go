package store

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo"
	mygoui "github.com/egoist/mygo/ui"

	"github.com/QingFengGod/bowknot-agent/internal/ui"
)

// Tab is the page shown in the app module.
type Tab int

// Page tabs.
const (
	TabInstalled Tab = iota
	TabDiscover
)

// Page is the state and view of the app module: the installed app launcher
// and the App Store. The app holds one Page for the life of the process, so
// switching modules keeps its state.
type Page struct {
	Tab      Tab
	Registry *Registry

	// Installer installs, enables and removes local apps; Host opens their
	// windows. The app injects both. Without them the launcher still shows
	// the installed apps, but its actions do nothing.
	Installer *Installer
	Host      *WebAppHost
	// Repaint repaints the main window after an action changed the registry
	// or opened a window.
	Repaint func()

	// Launcher state.
	InstalledSearch string
	StatusFilter    Status
	Sort            SortOrder

	// Discover state.
	Search   string
	Category string
	Selected string

	// installing is set while an install runs; notice is the last result,
	// shown above the grid.
	installing bool
	notice     string
}

// NewPage creates the app module page, defaulting to the installed launcher.
func NewPage(registry *Registry) *Page {
	if registry == nil {
		registry = NewRegistry()
	}
	p := &Page{
		Tab:      TabInstalled,
		Registry: registry,
		Sort:     SortRecent,
		Category: AllCategories,
	}
	p.Filtered()
	return p
}

// Installed returns the launcher's apps, filtered and sorted. Apps with an
// open window show as running without writing that transient status to the
// registry.
func (p *Page) Installed() []AppInfo {
	apps := FilterInstalled(p.Registry.Apps(), p.InstalledSearch, p.StatusFilter)
	if p.Host != nil {
		for i := range apps {
			if apps[i].Status == StatusInstalled && p.Host.IsOpen(apps[i].ID) {
				apps[i].Status = StatusRunning
			}
		}
	}
	return SortApps(apps, p.Sort)
}

// Filtered returns the catalog apps matching the current search and category,
// and keeps the selection on a visible app.
func (p *Page) Filtered() []AppInfo {
	apps := Filter(Catalog, p.Search, p.Category)
	p.ensureSelection(apps)
	return apps
}

func (p *Page) ensureSelection(apps []AppInfo) {
	if len(apps) == 0 {
		p.Selected = ""
		return
	}
	for _, a := range apps {
		if a.ID == p.Selected {
			return
		}
	}
	p.Selected = apps[0].ID
}

// SelectedApp returns the selected catalog entry, or false when no app is
// visible.
func (p *Page) SelectedApp() (AppInfo, bool) {
	for _, a := range Catalog {
		if a.ID == p.Selected {
			return a, true
		}
	}
	return AppInfo{}, false
}

// View returns the view of the app module.
func (p *Page) View() func(c *mygoui.Context) {
	return func(c *mygoui.Context) {
		t := c.Theme()
		area := mygoui.Column(c).Fill().Padding(32).Gap(16).Background(t.Background)
		area.Children(func() {
			p.header(c, t)
			if p.Tab == TabInstalled {
				p.launcher(c, t)
			} else {
				p.discover(c, t)
			}
		})
	}
}

func (p *Page) header(c *mygoui.Context, t *mygoui.Theme) {
	title, subtitle := "应用", "启动已安装的 WebApp"
	if p.Tab == TabDiscover {
		title, subtitle = "App Store", "浏览与安装 WebApp"
	}
	head := mygoui.Column(c).Gap(8).Shrink(0)
	head.Children(func() {
		text := mygoui.Column(c).Gap(4)
		text.Children(func() {
			mygoui.Text(c, title).FontSize(24).Bold().TextColor(t.Text)
			mygoui.Text(c, subtitle).FontSize(13).TextColor(t.TextMuted)
		})
		tabs := mygoui.Row(c).Gap(4).AlignItems(mygoui.Center)
		tabs.Children(func() {
			p.tabButton(c, t, "已安装", TabInstalled)
			p.tabButton(c, t, "发现", TabDiscover)
		})
	})
}

func (p *Page) tabButton(c *mygoui.Context, t *mygoui.Theme, label string, tab Tab) {
	selected := p.Tab == tab
	textColor := t.TextMuted
	if selected {
		textColor = t.Accent
	}
	b := mygoui.ButtonBase(c).Label(label).Shrink(0).Padding(6, 12).Radius(t.Radius).
		AlignItems(mygoui.Center).Justify(mygoui.Start)
	if selected {
		b.Background(ui.AccentTint())
	} else {
		b.Background(t.Surface)
		if b.Pressed() {
			b.Background(t.SurfacePressed)
		} else if b.Hovered() {
			b.Background(t.SurfaceHover)
		}
	}
	b.Children(func() {
		mygoui.Text(c, label).FontSize(13).TextColor(textColor)
	})
	if b.Clicked() {
		p.Tab = tab
	}
}

func (p *Page) launcher(c *mygoui.Context, t *mygoui.Theme) {
	apps := p.Installed()
	p.launcherToolbar(c, t, len(apps))
	if p.Registry.Len() == 0 {
		p.emptyLauncher(c, t)
		return
	}
	if len(apps) == 0 {
		p.emptyResult(c, t)
		return
	}
	scroll := mygoui.Scroll(c).Grow(1).FillHeight().Padding(4)
	scroll.Children(func() {
		grid := mygoui.Grid(c).Columns(3).Gap(12).FillWidth()
		grid.Children(func() {
			for _, app := range apps {
				p.appCard(c, t, app)
			}
		})
	})
}

func (p *Page) launcherToolbar(c *mygoui.Context, t *mygoui.Theme, count int) {
	bar := mygoui.Column(c).FillWidth().Shrink(0).Gap(8)
	bar.Children(func() {
		top := mygoui.Row(c).FillWidth().Gap(8).AlignItems(mygoui.Center)
		top.Children(func() {
			mygoui.TextInput(c, &p.InstalledSearch).Width(260).Label("搜索已安装应用").
				Placeholder("搜索已安装应用")
			p.installButton(c, t, "安装包…", false)
			p.installButton(c, t, "从目录安装", true)
			mygoui.Spacer(c)
			p.sortButton(c, t)
		})
		filters := mygoui.Row(c).FillWidth().Gap(8).AlignItems(mygoui.Center)
		filters.Children(func() {
			for _, status := range StatusFilters {
				p.statusButton(c, t, status)
			}
			mygoui.Spacer(c)
			mygoui.Text(c, fmt.Sprintf("共 %d 个应用", count)).
				FontSize(12).TextColor(t.TextMuted).SingleLine()
		})
		if p.installing {
			mygoui.Text(c, "正在安装…").FontSize(11).TextColor(t.TextMuted).SingleLine()
		} else if p.notice != "" {
			mygoui.Text(c, p.notice).FontSize(11).TextColor(t.TextMuted).SingleLine()
		}
	})
}

func (p *Page) installButton(c *mygoui.Context, t *mygoui.Theme, label string, directory bool) {
	disabled := p.Installer == nil || p.installing
	b := mygoui.ButtonBase(c).Label(label).Shrink(0).Disabled(disabled).
		Padding(6, 10).Radius(t.Radius).AlignItems(mygoui.Center).Justify(mygoui.Start).
		Background(t.Surface)
	if !disabled {
		if b.Pressed() {
			b.Background(t.SurfacePressed)
		} else if b.Hovered() {
			b.Background(t.SurfaceHover)
		}
	}
	b.Children(func() {
		textColor := t.Text
		if disabled {
			textColor = t.TextMuted
		}
		mygoui.Text(c, label).FontSize(12).TextColor(textColor)
	})
	if b.Clicked() && !disabled {
		p.installFromPicker(directory)
	}
}

// installFromPicker asks for a .bkapp package or a local folder and starts
// the install.
func (p *Page) installFromPicker(directory bool) {
	path, err := pickInstallSource(directory)
	if err != nil {
		p.notice = "打开选择器失败：" + err.Error()
		return
	}
	if path == "" {
		return
	}
	p.startInstall(InstallRequest{Path: path, Directory: directory})
}

func pickInstallSource(directory bool) (string, error) {
	opts := mygo.OpenDialogOptions{
		Title:       "选择要安装的应用目录",
		ButtonLabel: "安装",
		Directory:   directory,
	}
	if !directory {
		opts.Title = "选择 .bkapp 安装包"
		opts.Filters = []mygo.FileFilter{{Name: "Bowknot 应用", Extensions: []string{"bkapp"}}}
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

func (p *Page) sortButton(c *mygoui.Context, t *mygoui.Theme) {
	b := mygoui.ButtonBase(c).Label("切换排序").Shrink(0).Padding(6, 10).
		Radius(t.Radius).AlignItems(mygoui.Center).Justify(mygoui.Start).
		Background(t.Surface)
	if b.Pressed() {
		b.Background(t.SurfacePressed)
	} else if b.Hovered() {
		b.Background(t.SurfaceHover)
	}
	b.Children(func() {
		mygoui.Text(c, "排序："+string(p.Sort)).FontSize(12).TextColor(t.TextMuted)
	})
	if b.Clicked() {
		p.Sort = p.Sort.Next()
	}
}

func (p *Page) statusButton(c *mygoui.Context, t *mygoui.Theme, status Status) {
	selected := p.StatusFilter == status
	label := StatusFilterLabel(status)
	textColor := t.TextMuted
	if selected {
		textColor = t.Accent
	}
	b := mygoui.ButtonBase(c).Label("状态："+label).Shrink(0).Padding(6, 10).
		Radius(t.Radius).AlignItems(mygoui.Center).Justify(mygoui.Start)
	if selected {
		b.Background(ui.AccentTint())
	} else {
		b.Background(t.Surface)
		if b.Pressed() {
			b.Background(t.SurfacePressed)
		} else if b.Hovered() {
			b.Background(t.SurfaceHover)
		}
	}
	b.Children(func() {
		mygoui.Text(c, label).FontSize(12).TextColor(textColor)
	})
	if b.Clicked() {
		p.StatusFilter = status
	}
}

func (p *Page) appCard(c *mygoui.Context, t *mygoui.Theme, app AppInfo) {
	card := mygoui.Column(c).FillWidth().Padding(16).Gap(10).Radius(t.Radius).
		Background(t.Surface).BorderWidth(1).BorderColor(t.Border)
	card.Children(func() {
		head := mygoui.Row(c).FillWidth().Gap(12).AlignItems(mygoui.Center)
		head.Children(func() {
			iconTile(c, t, app.Name, 40)
			info := mygoui.Column(c).Grow(1).Gap(2)
			info.Children(func() {
				mygoui.Text(c, app.Name).FontSize(14).Bold().TextColor(t.Text).SingleLine()
				mygoui.Text(c, "v"+app.Version+" · "+app.Source).
					FontSize(11).TextColor(t.TextMuted).SingleLine()
			})
		})
		mygoui.Text(c, app.Summary).FontSize(12).TextColor(t.TextMuted).SingleLine()
		mygoui.Spacer(c)
		footer := mygoui.Row(c).FillWidth().Gap(6).AlignItems(mygoui.Center)
		footer.Children(func() {
			mygoui.Text(c, string(app.Status)).FontSize(11).
				TextColor(statusColor(app.Status, t)).SingleLine()
			mygoui.Spacer(c)
			if app.Status.Removable() {
				p.cardButton(c, t, "卸载", false, func() { p.uninstallApp(app) })
			}
			p.cardButton(c, t, p.primaryLabel(app), true, func() { p.primary(app) })
		})
	})
}

// primaryLabel is the card's main action for the app status.
func (p *Page) primaryLabel(app AppInfo) string {
	if app.Status == StatusCorrupt {
		return "修复"
	}
	if app.Status == StatusDisabled {
		return "启用"
	}
	return app.Status.PrimaryAction()
}

// primary runs the card's main action.
func (p *Page) primary(app AppInfo) {
	switch app.Status {
	case StatusInstalling, StatusUpdating:
		p.notice = string(app.Status) + "，请稍候"
	case StatusCorrupt:
		p.installFromPicker(app.Source == SourceDirectory)
	case StatusDisabled:
		p.enableApp(app)
	default:
		p.openApp(app)
	}
}

func (p *Page) cardButton(c *mygoui.Context, t *mygoui.Theme, label string, primary bool, action func()) {
	b := mygoui.ButtonBase(c).Label(label+"应用").Shrink(0).
		Padding(5, 10).Radius(t.Radius)
	if primary {
		b.Background(t.Accent)
	} else {
		b.Background(t.SurfacePressed)
	}
	if b.Pressed() {
		b.Background(t.SurfaceHover)
	}
	b.Children(func() {
		color := t.Text
		if primary {
			color = t.AccentText
		}
		mygoui.Text(c, label).FontSize(11).TextColor(color)
	})
	if b.Clicked() {
		action()
	}
}

// openApp opens the app window, focusing it when it is already open.
func (p *Page) openApp(app AppInfo) {
	if p.Host == nil {
		p.notice = "应用窗口不可用"
		return
	}
	if err := p.Host.Open(app.ID); err != nil {
		p.notice = "打开失败：" + err.Error()
		var manifestErr *ManifestError
		if errors.As(err, &manifestErr) && p.Installer != nil {
			_ = p.Installer.SetStatus(app.ID, StatusCorrupt)
		}
		p.showMessage("无法打开应用", err.Error())
	}
	p.repaint()
}

// enableApp turns a disabled app back on.
func (p *Page) enableApp(app AppInfo) {
	if p.Installer == nil {
		return
	}
	if err := p.Installer.SetStatus(app.ID, StatusInstalled); err != nil {
		p.notice = "启用失败：" + err.Error()
		p.showMessage("启用失败", err.Error())
	}
	p.repaint()
}

// startInstall runs the install on a worker and applies the outcome on the
// main thread.
func (p *Page) startInstall(req InstallRequest) {
	if p.Installer == nil || p.installing {
		return
	}
	p.installing = true
	p.notice = ""
	go func() {
		app, err := p.runInstall(context.Background(), req)
		mygo.RunOnMain(func() {
			p.installing = false
			if err != nil {
				p.showMessage("安装失败", err.Error())
			} else {
				p.notice = "已安装 " + app.Name
			}
			p.repaint()
		})
	}()
}

// runInstall installs synchronously; it only touches the registry, so the
// caller applies the notice on the main thread.
func (p *Page) runInstall(ctx context.Context, req InstallRequest) (AppInfo, error) {
	if p.Installer == nil {
		return AppInfo{}, fmt.Errorf("store: installer is not configured")
	}
	return p.Installer.Install(ctx, req)
}

// uninstallApp confirms, closes the app window, asks about its data and
// removes the app.
func (p *Page) uninstallApp(app AppInfo) {
	if p.Installer == nil {
		return
	}
	confirm, err := mygo.Dialog.Message(mygo.MessageOptions{
		Type:          mygo.MessageWarning,
		Title:         "卸载应用",
		Message:       "确定卸载 " + app.Name + "？",
		Detail:        "应用文件会被删除。",
		Buttons:       []string{"取消", "卸载"},
		DefaultButton: 1,
		CancelButton:  0,
	})
	if err != nil || confirm.Button != 1 {
		return
	}
	if p.Host != nil && p.Host.IsOpen(app.ID) {
		p.Host.Close(app.ID)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = p.Host.WaitClosed(ctx, app.ID)
		cancel()
	}
	choice, err := mygo.Dialog.Message(mygo.MessageOptions{
		Type:          mygo.MessageQuestion,
		Title:         "应用数据",
		Message:       "要一并删除 " + app.Name + " 的私有数据吗？",
		Detail:        "保留数据会在重装后继续可用。",
		Buttons:       []string{"取消", "保留数据", "删除数据"},
		DefaultButton: 1,
		CancelButton:  0,
	})
	if err != nil || choice.Button == 0 {
		return
	}
	if err := p.Installer.Uninstall(context.Background(), app.ID, choice.Button == 2); err != nil {
		p.notice = "卸载失败：" + err.Error()
		p.showMessage("卸载失败", err.Error())
		p.repaint()
		return
	}
	p.notice = "已卸载 " + app.Name
	p.repaint()
}

func (p *Page) showMessage(title, message string) {
	_, _ = mygo.Dialog.Message(mygo.MessageOptions{
		Type:    mygo.MessageError,
		Title:   title,
		Message: message,
		Buttons: []string{"知道了"},
	})
}

func (p *Page) repaint() {
	if p.Repaint != nil {
		p.Repaint()
	}
}

func (p *Page) emptyLauncher(c *mygoui.Context, t *mygoui.Theme) {
	p.emptyState(c, t, "还没有安装应用", "安装后会在应用模块里直接启动", "浏览商店", func() {
		p.Tab = TabDiscover
	})
}

func (p *Page) emptyResult(c *mygoui.Context, t *mygoui.Theme) {
	p.emptyState(c, t, "没有符合条件的应用", "换个关键词或状态筛选试试", "清除筛选", func() {
		p.InstalledSearch = ""
		p.StatusFilter = ""
	})
}

func (p *Page) emptyState(c *mygoui.Context, t *mygoui.Theme, title, hint, actionLabel string, action func()) {
	box := mygoui.Box(c).Grow(1).FillWidth().Center()
	box.Children(func() {
		column := mygoui.Column(c).Gap(8).AlignItems(mygoui.Center)
		column.Children(func() {
			mygoui.Text(c, title).FontSize(14).TextColor(t.Text)
			mygoui.Text(c, hint).FontSize(12).TextColor(t.TextMuted)
			b := mygoui.ButtonBase(c).Label(actionLabel).Shrink(0).
				Padding(6, 12).Radius(t.Radius).Background(t.SurfacePressed)
			b.Children(func() {
				mygoui.Text(c, actionLabel).FontSize(12).TextColor(t.Text)
			})
			if b.Clicked() {
				action()
			}
		})
	})
}

func (p *Page) discover(c *mygoui.Context, t *mygoui.Theme) {
	apps := p.Filtered()
	p.discoverToolbar(c, t, len(apps))
	row := mygoui.Row(c).Grow(1).FillWidth().Gap(16)
	row.Children(func() {
		p.list(c, t, apps)
		p.detail(c, t)
	})
}

func (p *Page) discoverToolbar(c *mygoui.Context, t *mygoui.Theme, count int) {
	bar := mygoui.Row(c).FillWidth().Shrink(0).Gap(8).AlignItems(mygoui.Center)
	bar.Children(func() {
		mygoui.TextInput(c, &p.Search).Width(260).Label("搜索应用").Placeholder("搜索应用")
		for _, category := range Categories {
			p.categoryButton(c, t, category)
		}
		mygoui.Spacer(c)
		mygoui.Text(c, fmt.Sprintf("共 %d 个应用", count)).
			FontSize(12).TextColor(t.TextMuted).SingleLine()
	})
}

func (p *Page) categoryButton(c *mygoui.Context, t *mygoui.Theme, category Category) {
	selected := p.Category == category.ID
	labelColor := t.TextMuted
	if selected {
		labelColor = t.Accent
	}
	b := mygoui.ButtonBase(c).Label("分类："+category.Label).Shrink(0).
		Padding(6, 10).Radius(t.Radius).AlignItems(mygoui.Center).Justify(mygoui.Start)
	if selected {
		b.Background(ui.AccentTint())
	} else {
		b.Background(t.Surface)
		if b.Pressed() {
			b.Background(t.SurfacePressed)
		} else if b.Hovered() {
			b.Background(t.SurfaceHover)
		}
	}
	b.Children(func() {
		mygoui.Text(c, category.Label).FontSize(12).TextColor(labelColor)
	})
	if b.Clicked() {
		p.Category = category.ID
	}
}

func (p *Page) list(c *mygoui.Context, t *mygoui.Theme, apps []AppInfo) {
	list := mygoui.Scroll(c).Grow(1).FillHeight().Padding(6).Radius(t.Radius).
		Background(t.Surface).BorderWidth(1).BorderColor(t.Border)
	list.Children(func() {
		if len(apps) == 0 {
			p.emptyList(c, t)
			return
		}
		column := mygoui.Column(c).FillWidth().Gap(4)
		column.Children(func() {
			for _, app := range apps {
				p.appRow(c, t, app)
			}
		})
	})
}

func (p *Page) emptyList(c *mygoui.Context, t *mygoui.Theme) {
	box := mygoui.Box(c).FillWidth().Height(260).Center()
	box.Children(func() {
		column := mygoui.Column(c).Gap(8).AlignItems(mygoui.Center)
		column.Children(func() {
			mygoui.Text(c, "未找到匹配应用").FontSize(14).TextColor(t.Text)
			mygoui.Text(c, "换个关键词或分类试试").FontSize(12).TextColor(t.TextMuted)
			clear := mygoui.ButtonBase(c).Label("清除筛选").Shrink(0).
				Padding(6, 12).Radius(t.Radius).Background(t.SurfacePressed)
			clear.Children(func() {
				mygoui.Text(c, "清除筛选").FontSize(12).TextColor(t.Text)
			})
			if clear.Clicked() {
				p.Search = ""
				p.Category = AllCategories
			}
		})
	})
}

func (p *Page) appRow(c *mygoui.Context, t *mygoui.Theme, app AppInfo) {
	selected := p.Selected == app.ID
	nameColor := t.Text
	if selected {
		nameColor = t.Accent
	}
	row := mygoui.ButtonBase(c).Label("打开" + app.Name).FillWidth().Shrink(0).
		Padding(8).Gap(10).Radius(t.Radius).AlignItems(mygoui.Center).Justify(mygoui.Start)
	if selected {
		row.Background(ui.AccentTint())
	} else if row.Pressed() {
		row.Background(t.SurfacePressed)
	} else if row.Hovered() {
		row.Background(t.SurfaceHover)
	}
	row.Children(func() {
		iconTile(c, t, app.Name, 36)
		column := mygoui.Column(c).Grow(1).Gap(2)
		column.Children(func() {
			mygoui.Text(c, app.Name).FontSize(14).Bold().TextColor(nameColor).SingleLine()
			mygoui.Text(c, app.Summary).FontSize(12).TextColor(t.TextMuted).SingleLine()
		})
		mygoui.Text(c, string(app.Status)).FontSize(11).TextColor(t.TextMuted).
			Shrink(0).SingleLine()
	})
	if row.Clicked() {
		p.Selected = app.ID
	}
}

func (p *Page) detail(c *mygoui.Context, t *mygoui.Theme) {
	panel := mygoui.Column(c).Width(320).Shrink(0).FillHeight().Padding(20).Gap(10).
		Background(t.Surface).BorderWidth(1).BorderColor(t.Border).Radius(t.Radius)
	panel.Children(func() {
		app, ok := p.SelectedApp()
		if !ok {
			empty := mygoui.Box(c).Grow(1).FillWidth().Center()
			empty.Children(func() {
				mygoui.Text(c, "选择一个应用查看详情").FontSize(12).TextColor(t.TextMuted)
			})
			return
		}
		head := mygoui.Row(c).FillWidth().Gap(12).AlignItems(mygoui.Center)
		head.Children(func() {
			iconTile(c, t, app.Name, 56)
			column := mygoui.Column(c).Grow(1).Gap(2)
			column.Children(func() {
				mygoui.Text(c, app.Name).FontSize(18).Bold().TextColor(t.Text).SingleLine()
				mygoui.Text(c, "v"+app.Version+" · "+app.Size).
					FontSize(12).TextColor(t.TextMuted).SingleLine()
			})
		})
		mygoui.Text(c, "来源："+app.Source+" · "+CategoryLabel(app.Category)).
			FontSize(12).TextColor(t.TextMuted)
		divider(c, t)
		detailSection(c, t, "简介", app.Summary)
		detailSection(c, t, "更新说明", app.ReleaseNotes)
		mygoui.Spacer(c)
		status := mygoui.Row(c).FillWidth().Gap(6).AlignItems(mygoui.Center)
		status.Children(func() {
			mygoui.Text(c, string(app.Status)).FontSize(12).TextColor(t.Text)
			mygoui.Spacer(c)
			mygoui.Text(c, "示例应用不含安装包，请使用本地安装").
				FontSize(11).TextColor(t.TextMuted).SingleLine()
		})
		useLocal := mygoui.ButtonBase(c).Label("使用本地安装").Shrink(0).
			Padding(6, 12).Radius(t.Radius).Background(t.SurfacePressed)
		useLocal.Children(func() {
			mygoui.Text(c, "使用本地安装").FontSize(12).TextColor(t.Text)
		})
		if useLocal.Clicked() {
			p.Tab = TabInstalled
		}
	})
}

func statusColor(status Status, t *mygoui.Theme) mygoui.Color {
	mode := ui.ModeLight
	if t.Dark {
		mode = ui.ModeDark
	}
	switch status {
	case StatusRunning:
		return ui.SuccessColor(mode)
	case StatusUpdatable, StatusUpdating, StatusInstalling:
		return t.Accent
	case StatusDisabled, StatusCorrupt:
		return ui.DangerColor(mode)
	default:
		return t.TextMuted
	}
}

func detailSection(c *mygoui.Context, t *mygoui.Theme, label, body string) {
	mygoui.Text(c, label).FontSize(12).Bold().TextColor(t.Text)
	mygoui.Text(c, body).FontSize(12).TextColor(t.TextMuted)
}

func divider(c *mygoui.Context, t *mygoui.Theme) {
	mygoui.Box(c).FillWidth().Height(1).Shrink(0).Background(t.Border)
}

func iconTile(c *mygoui.Context, t *mygoui.Theme, name string, size float32) {
	tile := mygoui.Box(c).Size(size, size).Radius(size / 6).Shrink(0).Center().
		Background(t.SurfacePressed)
	tile.Children(func() {
		mygoui.Text(c, initial(name)).FontSize(size / 3).Bold().TextColor(t.Accent)
	})
}

func initial(name string) string {
	r, _ := utf8.DecodeRuneInString(name)
	if r == utf8.RuneError {
		return "?"
	}
	return string(r)
}
