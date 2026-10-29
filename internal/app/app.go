// Package app 负责进程生命周期与主窗口装配。
package app

import (
	"log"

	"github.com/egoist/mygo"
	mygoui "github.com/egoist/mygo/ui"

	"github.com/QingFengGod/bowknot-agent/internal/agent"
	"github.com/QingFengGod/bowknot-agent/internal/capability"
	"github.com/QingFengGod/bowknot-agent/internal/gateway"
	"github.com/QingFengGod/bowknot-agent/internal/platform"
	pluginpage "github.com/QingFengGod/bowknot-agent/internal/plugin"
	settingspage "github.com/QingFengGod/bowknot-agent/internal/settings"
	"github.com/QingFengGod/bowknot-agent/internal/shell"
	"github.com/QingFengGod/bowknot-agent/internal/store"
	"github.com/QingFengGod/bowknot-agent/internal/task"
	"github.com/QingFengGod/bowknot-agent/internal/ui"
	"github.com/QingFengGod/bowknot-agent/resources"
)

// Title is the main window title, also the app name the tray shows.
const Title = "Bowknot Agent"

// ShellVersion is the running shell version, compared with the
// min_shell_version of an app package.
const ShellVersion = "0.1.0"

// 主窗口的最小尺寸，以及工作区不可用时的默认尺寸。
const (
	MinWidth  = 960
	MinHeight = 640
)

// App 是主应用的装配入口。
type App struct {
	window       *mygo.Window
	tray         *mygo.Tray
	shell        *shell.Shell
	preference   ui.Preference
	theme        ui.Mode
	settingsPath string
	// appsDir holds installed app programs, appDataDir their private data and
	// registryPath the installed app list.
	appsDir       string
	appDataDir    string
	registryPath  string
	registry      *store.Registry
	page          *store.Page
	installer     *store.Installer
	host          *store.WebAppHost
	capabilities  *capability.Service
	gateway       *gateway.Service
	settingsPage  *settingspage.Page
	assistantPage *agent.Page
	agentPage     *agent.AgentPage
	taskPage      *task.Page
	// agents is the agent registry shared by the assistant, the agent page
	// and the plugin skill injection.
	agents      *agent.Registry
	pluginStore *pluginpage.Store
	pluginPage  *pluginpage.Page
	// quitting is set before the windows close for a quit, so that the close
	// handler lets the window go instead of hiding it to the tray.
	quitting bool
}

// New 读取上次的外观偏好并装配主界面。系统外观只能在应用就绪后读取，因此
// 有效外观在 Run 中解析。
func New() *App {
	a := &App{preference: ui.PreferenceSystem}
	path, err := platform.SettingsPath()
	if err != nil {
		log.Printf("app: settings path: %v; following the system theme", err)
	} else {
		a.settingsPath = path
		a.preference = loadTheme(path)
	}
	a.shell = shell.New(a.preference, false, shell.Actions{
		ToggleTheme: a.toggleTheme,
		Minimize:    func() { a.window.Minimize() },
		Maximize:    a.toggleMaximize,
		Close:       func() { a.window.Close() },
	})
	// 004 装配应用模块：从本地注册表读取已安装应用，缺省为空启动器。
	a.setUpStore()
	// 006 装配网关与设置模块：侧栏状态由网关快照注入。
	a.setUpGateway()
	// 007 装配助手模块：会话本地持久化，对话走网关。
	// 011 拆出独立的 Agent 管理页，两者共享会话与 Agent 工作区。
	a.setUpAgent()
	// 013 装配插件模块：本地导入、注册表与按 Agent 启用。
	a.setUpPlugins()
	a.shell.Pages = map[shell.ModuleID]shell.Page{
		shell.ModuleStore:     a.page,
		shell.ModuleAssistant: a.assistantPage,
		shell.ModuleAgent:     a.agentPage,
		shell.ModulePlugins:   a.pluginPage,
		shell.ModuleTask:      a.taskPage,
		shell.ModuleSettings:  a.settingsPage,
	}
	return a
}

// setUpStore wires the installed app registry, the installer and the app
// window host. Missing data paths leave the launcher usable but idle: nothing
// is installed and installing is disabled.
func (a *App) setUpStore() {
	registry := store.NewRegistry()
	appsDir, err := platform.AppsDir()
	if err != nil {
		log.Printf("app: apps directory: %v; installing is disabled", err)
	}
	appDataDir, err := platform.AppDataDir()
	if err != nil {
		log.Printf("app: app data directory: %v", err)
	}
	registryPath, err := platform.RegistryPath()
	if err != nil {
		log.Printf("app: apps registry path: %v", err)
	}
	a.appsDir, a.appDataDir, a.registryPath = appsDir, appDataDir, registryPath

	if registryPath != "" {
		loaded, loadErr := store.LoadRegistry(registryPath)
		if loadErr != nil {
			log.Printf("app: load apps registry: %v", loadErr)
		} else {
			registry = loaded
		}
		// A crash leaves transient statuses behind, and a deleted or damaged
		// program directory must show as corrupt rather than as openable.
		changed := registry.Normalize()
		if appsDir != "" && registry.VerifyPrograms(appsDir, ShellVersion) {
			changed = true
		}
		if changed {
			if saveErr := store.SaveRegistry(registryPath, registry); saveErr != nil {
				log.Printf("app: save normalized apps registry: %v", saveErr)
			}
		}
	}
	a.registry = registry

	if appsDir != "" && registryPath != "" {
		a.installer = &store.Installer{
			AppsDir:      appsDir,
			AppDataDir:   appDataDir,
			RegistryPath: registryPath,
			Registry:     registry,
			ShellVersion: ShellVersion,
		}
	}
	if appsDir != "" {
		a.host = store.NewWebAppHost(appsDir, ShellVersion, capability.PreloadScript, ui.ModeLight)
		a.host.Repaint = a.invalidateMain
		a.host.OnOpened = func(id string) {
			if a.installer == nil {
				return
			}
			if _, err := a.installer.MarkOpened(id); err != nil {
				log.Printf("app: record app launch: %v", err)
			}
		}
		a.host.OnClosed = func(id string) {
			a.forgetAppGrants(id)
			a.invalidateMain()
		}
	}

	a.page = store.NewPage(registry)
	a.page.Installer = a.installer
	a.page.Host = a.host
	a.page.Repaint = a.invalidateMain
}

// invalidateMain repaints the main window when it exists.
func (a *App) invalidateMain() {
	if a.window != nil {
		a.window.Invalidate()
	}
}

// Run 取得单实例锁、装配主窗口与托盘并启动事件循环，直到应用退出。
func (a *App) Run() error {
	if !mygo.App.RequestSingleInstanceLock() {
		return nil
	}
	mygo.App.OnSecondInstance(func([]string, string) { a.showMainWindow() })
	mygo.App.OnActivate(func(hasVisibleWindows bool) {
		if !hasVisibleWindows {
			a.showMainWindow()
		}
	})
	// Quitting also closes the windows, so the close handler must know that
	// this close is the real exit rather than a request to hide.
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) {
		a.quitting = true
		// 007: cancel a running reply and persist what streamed before exit.
		// 008: cancel a running task group or plan.
		if a.assistantPage != nil {
			if a.assistantPage.Dispatcher != nil {
				a.assistantPage.Dispatcher.Shutdown()
			}
			a.assistantPage.Runner.Shutdown()
		}
		if a.host != nil {
			a.host.CloseAll()
		}
	})
	mygo.Theme.SetSource(a.preference.Source())
	mygo.Theme.OnUpdated(a.systemThemeChanged)
	mygo.App.WhenReady(func() {
		// The bkapp scheme must be registered before an app window is created;
		// the capability service must be bound before a page calls it.
		a.registerAppProtocol()
		a.bindCapabilities()
		if a.installer != nil {
			a.installer.CleanStaging()
		}
		if a.pluginStore != nil {
			a.pluginStore.CleanStaging()
		}
		a.applyTheme()
		a.createWindow()
		a.createTray()
	})
	return mygo.App.Run()
}

// registerAppProtocol serves installed app files under bkapp://localhost/. A
// failed registration leaves the launcher usable and reports the reason.
func (a *App) registerAppProtocol() {
	if a.appsDir == "" {
		return
	}
	if err := mygo.Protocol.Handle(store.ProtocolScheme, store.NewProtocolHandler(a.appsDir)); err != nil {
		log.Printf("app: register %s protocol: %v", store.ProtocolScheme, err)
	}
}

// createWindow creates the main window and wires its close and focus events.
func (a *App) createWindow() {
	d := mygo.Screen.PrimaryDisplay()
	width, height := windowSize(d.WorkArea.Width, d.WorkArea.Height)
	minWidth, minHeight := minWindowSize(d.WorkArea.Width, d.WorkArea.Height)
	a.window = mygo.NewWindow(mygo.WindowOptions{
		Title:             Title,
		Width:             width,
		Height:            height,
		MinWidth:          minWidth,
		MinHeight:         minHeight,
		StateKey:          "main",
		TitleBarStyle:     mygo.TitleBarHidden,
		HideWindowButtons: true,
		BackgroundColor:   ui.BackgroundHex(a.theme),
		Content:           mygoui.View(a.shell.View()),
	})
	a.window.OnClose(func(e *mygo.CloseEvent) {
		if !shouldHideOnClose(a.quitting, a.tray != nil) {
			return
		}
		e.PreventDefault()
		a.window.Hide()
	})
	a.window.OnFocus(func() { a.setFocused(true) })
	a.window.OnBlur(func() { a.setFocused(false) })
}

// shouldHideOnClose reports whether closing the main window hides it to the
// tray instead of ending the process. Without a tray the process would have
// no way back, so closing quits as usual.
func shouldHideOnClose(quitting, hasTray bool) bool {
	return !quitting && hasTray
}

// createTray adds the menu bar or notification area icon. When the platform
// refuses the tray, closing the window quits as usual rather than leaving a
// process with no way back.
func (a *App) createTray() {
	tray, err := mygo.NewTray(mygo.TrayOptions{
		ID:             "main",
		Icon:           resources.TrayIcon,
		IconIsTemplate: true,
		ToolTip:        Title,
		Menu: mygo.NewMenu([]*mygo.MenuItem{
			{ID: "show", Label: "打开主界面", Click: func(*mygo.MenuItem, *mygo.Window) { a.showMainWindow() }},
			mygo.Separator(),
			{ID: "quit", Label: "退出", Click: func(*mygo.MenuItem, *mygo.Window) { mygo.App.Quit() }},
		}),
	})
	if err != nil {
		log.Printf("app: create tray: %v; closing the main window will quit", err)
		return
	}
	a.tray = tray
	// A left click opens the main window; the menu stays on the right click.
	// macOS shows the menu on either click, so the click there cannot open
	// the window (see 003 §3.2).
	tray.OnClick(a.showMainWindow)
	// The main window hides instead of closing while the tray is available,
	// so the last window may be invisible; keep the process running.
	mygo.App.OnWindowAllClosed(func() {})
}

// showMainWindow brings the hidden main window back, from the tray, a second
// launch or the macOS Dock.
func (a *App) showMainWindow() {
	if a.window == nil {
		return
	}
	mygo.App.Show()
	a.window.Show()
	a.window.Focus()
}

// setFocused stores whether the main window has focus and repaints its title
// bar.
func (a *App) setFocused(focused bool) {
	if a.shell.Focused == focused {
		return
	}
	a.shell.Focused = focused
	if a.window != nil {
		a.window.Invalidate()
	}
}

// systemThemeChanged follows the desktop appearance while the user chose
// "system"; an explicit light or dark choice stays.
func (a *App) systemThemeChanged() {
	if a.preference != ui.PreferenceSystem {
		return
	}
	a.applyTheme()
}

// applyTheme resolves the appearance from the preference and the desktop,
// updates the view, and keeps the window background in step so that no old
// color shows while it repaints.
func (a *App) applyTheme() {
	a.theme = ui.Resolve(a.preference, mygo.Theme.IsDark())
	a.shell.Preference = a.preference
	a.shell.Theme = a.theme
	if a.host != nil {
		a.host.SetTheme(a.theme)
	}
	if a.window != nil {
		_ = a.window.SetBackgroundColor(ui.BackgroundHex(a.theme))
		a.window.Invalidate()
	}
}

// toggleTheme moves the preference light → dark → system, applies it and
// saves it. A failed save keeps the new appearance and tries again on the
// next toggle.
func (a *App) toggleTheme() {
	a.preference = a.preference.Next()
	mygo.Theme.SetSource(a.preference.Source())
	a.applyTheme()
	if a.settingsPath == "" {
		return
	}
	if err := saveTheme(a.settingsPath, a.preference); err != nil {
		log.Printf("app: save settings: %v", err)
	}
}

func (a *App) toggleMaximize() {
	if a.window == nil {
		return
	}
	if platform.UsesFullScreenZoom() {
		a.window.ToggleFullScreen()
		return
	}
	if a.window.IsMaximized() {
		a.window.Unmaximize()
	} else {
		a.window.Maximize()
	}
}

// windowSize returns the initial window size: 80% of the work area, but no
// smaller than the minimum, and never larger than the work area itself.
func windowSize(workWidth, workHeight int) (width, height int) {
	if workWidth <= 0 || workHeight <= 0 {
		return MinWidth, MinHeight
	}
	width = max(workWidth*4/5, min(MinWidth, workWidth))
	height = max(workHeight*4/5, min(MinHeight, workHeight))
	return width, height
}

// minWindowSize returns the minimum window size, capped by the work area so
// that the window cannot exceed it.
func minWindowSize(workWidth, workHeight int) (width, height int) {
	if workWidth <= 0 || workHeight <= 0 {
		return MinWidth, MinHeight
	}
	return min(MinWidth, workWidth), min(MinHeight, workHeight)
}
