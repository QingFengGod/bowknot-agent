// Package app 负责进程生命周期与主窗口装配。
package app

import (
	"log"

	"github.com/egoist/mygo"
	mygoui "github.com/egoist/mygo/ui"

	"github.com/bowknot/bowknot-agent/internal/platform"
	"github.com/bowknot/bowknot-agent/internal/shell"
	"github.com/bowknot/bowknot-agent/internal/ui"
)

// Title 是主窗口标题，也是用户看到的应用名。
const Title = "Bowknot Agent"

// 主窗口的最小尺寸，以及工作区不可用时的默认尺寸。
const (
	MinWidth  = 960
	MinHeight = 640
)

// App 是主应用的装配入口。
type App struct {
	window       *mygo.Window
	shell        *shell.Shell
	theme        ui.Mode
	settingsPath string
}

// New 读取上次的外观并装配主界面。
func New() *App {
	a := &App{theme: ui.ModeLight}
	path, err := platform.SettingsPath()
	if err != nil {
		log.Printf("app: settings path: %v; using light theme", err)
	} else {
		a.settingsPath = path
		a.theme = loadTheme(path)
	}
	a.shell = shell.New(a.theme, shell.Actions{
		ToggleTheme: a.toggleTheme,
		Minimize:    func() { a.window.Minimize() },
		Maximize:    a.toggleMaximize,
		Close:       func() { a.window.Close() },
	})
	return a
}

// Run 装配主窗口并启动事件循环，直到应用退出。
func (a *App) Run() error {
	mygo.App.WhenReady(func() {
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
	})
	return mygo.App.Run()
}

// toggleTheme switches the appearance, keeps the window background in step
// and saves the choice. A failed save keeps the new theme and tries again on
// the next toggle.
func (a *App) toggleTheme() {
	a.theme = a.theme.Toggle()
	a.shell.Theme = a.theme
	if a.window != nil {
		_ = a.window.SetBackgroundColor(ui.BackgroundHex(a.theme))
	}
	if a.settingsPath == "" {
		return
	}
	if err := saveTheme(a.settingsPath, a.theme); err != nil {
		log.Printf("app: save settings: %v", err)
	}
}

func (a *App) toggleMaximize() {
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
