// Package shell 提供主界面框架：自绘标题栏下的四模块导航与模块容器。
package shell

import (
	mygoui "github.com/egoist/mygo/ui"

	"github.com/bowknot/bowknot-agent/internal/ui"
)

// ModuleID identifies a module of the shell.
type ModuleID string

// Modules of the shell.
const (
	ModuleStore    ModuleID = "store"
	ModuleAgent    ModuleID = "agent"
	ModuleCreation ModuleID = "creation"
	ModuleSettings ModuleID = "settings"
)

// Module is one entry of the navigation.
type Module struct {
	ID          ModuleID
	Name        string
	Description string
	Icon        *mygoui.SVG
}

// Modules are the navigation entries, in order.
var Modules = []Module{
	{ModuleStore, "App Store", "浏览与安装 WebApp", ui.StoreIcon},
	{ModuleAgent, "助手 / Agent", "与 Agent 对话并编排任务", ui.AgentIcon},
	{ModuleCreation, "创作", "图片、视频与语音创作", ui.CreationIcon},
	{ModuleSettings, "设置", "应用、网关与数据设置", ui.SettingsIcon},
}

// Actions are the window commands the title bar buttons ask the app to run.
type Actions struct {
	ToggleTheme func()
	Minimize    func()
	Maximize    func()
	Close       func()
}

// Shell is the main view: the title bar above the navigation and the content
// of the current module.
type Shell struct {
	Theme   ui.Mode
	Current ModuleID
	Actions Actions
}

// New creates the shell showing mode as the appearance and store as the
// module.
func New(mode ui.Mode, actions Actions) *Shell {
	return &Shell{Theme: mode, Current: ModuleStore, Actions: actions}
}

// View returns the view of the shell.
func (s *Shell) View() func(c *mygoui.Context) {
	return func(c *mygoui.Context) {
		t := ui.Theme(s.Theme)
		c.SetTheme(t)
		mygoui.Column(c).Fill().Background(t.Background).Children(func() {
			ui.TitleBar(c, ui.TitleBarActions{
				OnToggleTheme: s.Actions.ToggleTheme,
				OnMinimize:    s.Actions.Minimize,
				OnMaximize:    s.Actions.Maximize,
				OnClose:       s.Actions.Close,
			})
			mygoui.Row(c).Grow(1).Children(func() {
				s.nav(c, t)
				s.content(c, t)
			})
		})
	}
}

func (s *Shell) nav(c *mygoui.Context, t *mygoui.Theme) {
	nav := mygoui.Column(c).Width(220).Shrink(0).FillHeight().Padding(8).Gap(4).
		Background(t.Surface).BorderWidth(0, 1, 0, 0).BorderColor(t.Border).
		FocusGroup(mygoui.Vertical)
	nav.Children(func() {
		for _, m := range Modules {
			s.navItem(c, t, m)
		}
	})
}

func (s *Shell) navItem(c *mygoui.Context, t *mygoui.Theme, m Module) {
	selected := s.Current == m.ID
	b := mygoui.ButtonBase(c).Label(m.Name).Selected(selected).FillWidth().
		Padding(8, 10).Gap(8).Radius(t.Radius).AlignItems(mygoui.Center).
		Justify(mygoui.Start)
	if selected {
		b.Background(ui.AccentTint()).TextColor(t.Accent)
	} else {
		b.TextColor(t.Text)
		if b.Pressed() {
			b.Background(t.SurfacePressed)
		} else if b.Hovered() {
			b.Background(t.SurfaceHover)
		}
	}
	b.Children(func() {
		mygoui.Icon(c, m.Icon).Size(16, 16)
		mygoui.Text(c, m.Name).FontSize(14)
	})
	if b.Clicked() {
		s.Current = m.ID
	}
}

func (s *Shell) content(c *mygoui.Context, t *mygoui.Theme) {
	m := s.module()
	area := mygoui.Column(c).Grow(1).FillHeight().Padding(32).Gap(8).
		Background(t.Background)
	area.Children(func() {
		mygoui.Text(c, m.Name).FontSize(24).Bold().TextColor(t.Text)
		mygoui.Text(c, m.Description).TextColor(t.TextMuted)
		centered := mygoui.Box(c).Grow(1).FillWidth().Center()
		centered.Children(func() {
			card := mygoui.Column(c).Width(380).Padding(24).Gap(8).Radius(t.Radius).
				Background(t.Surface).BorderWidth(1).BorderColor(t.Border).
				AlignItems(mygoui.Center).TextColor(t.TextMuted)
			card.Children(func() {
				mygoui.Icon(c, m.Icon).Size(32, 32)
				mygoui.Text(c, m.Name).FontSize(16).Bold().TextColor(t.Text)
				mygoui.Text(c, "内容将在后续计划中实现").FontSize(13)
			})
		})
	})
}

// module returns the module of the current ID, store when the ID is unknown.
func (s *Shell) module() Module {
	for _, m := range Modules {
		if m.ID == s.Current {
			return m
		}
	}
	return Modules[0]
}
