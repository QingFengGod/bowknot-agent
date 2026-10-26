// Package ui 提供主界面共用的布局、组件与主题。
//
// 001 只有占位首页；模块导航与模块容器见后续计划（FR-SHELL-01）。
package ui

import "github.com/egoist/mygo/ui"

// Home 返回占位首页的视图，后续由主界面框架替换。
func Home(platformName string) func(c *ui.Context) {
	return func(c *ui.Context) {
		t := c.Theme()
		ui.Column(c).Fill().Center().Gap(12).Padding(32).Children(func() {
			ui.Text(c, "Bowknot Agent").FontSize(28).Bold()
			ui.Text(c, "本地优先的桌面 AI 工作台").TextColor(t.TextMuted)
			ui.Text(c, "工程骨架已就绪，功能模块待后续计划实现。").FontSize(13).TextColor(t.TextMuted)
			ui.Textf(c, "运行平台：%s", platformName).FontSize(12).TextColor(t.TextMuted)
		})
	}
}
