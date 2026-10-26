// Package app 负责进程生命周期与主窗口装配。
package app

import (
	"github.com/egoist/mygo"
	mygoui "github.com/egoist/mygo/ui"

	"github.com/bowknot/bowknot-agent/internal/platform"
	"github.com/bowknot/bowknot-agent/internal/ui"
)

// Title 是主窗口标题，也是用户看到的应用名。
const Title = "Bowknot Agent"

// App 是主应用的装配入口。
type App struct{}

// New 创建应用。
func New() *App {
	return &App{}
}

// Run 装配主窗口并启动事件循环，直到应用退出。
func (a *App) Run() error {
	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{
			Title:     Title,
			Width:     960,
			Height:    640,
			MinWidth:  720,
			MinHeight: 480,
			StateKey:  "main",
			Content:   mygoui.View(ui.Home(platform.Name())),
		})
	})
	return mygo.App.Run()
}
