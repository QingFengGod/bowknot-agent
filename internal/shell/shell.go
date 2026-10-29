// Package shell 提供主界面框架：自绘标题栏下的四模块导航与模块容器。
package shell

import (
	mygoui "github.com/egoist/mygo/ui"

	"github.com/QingFengGod/bowknot-agent/internal/ui"
)

// ModuleID identifies a module of the shell.
type ModuleID string

// Modules of the shell.
const (
	ModuleStore    ModuleID = "store"
	ModuleAgent    ModuleID = "agent"
	ModuleTask     ModuleID = "task"
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
	{ModuleStore, "应用", "已安装应用与商店", ui.StoreIcon},
	{ModuleAgent, "助手 / Agent", "与 Agent 对话并编排任务", ui.AgentIcon},
	{ModuleTask, "任务", "任务计划、运行与结果", ui.TaskIcon},
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

// Page is a module's content view, injected by the app. A page keeps its own
// state for as long as the app holds it, so switching modules preserves the
// search, filter and selection of the module.
type Page interface {
	View() func(c *mygoui.Context)
}

// GatewayState is the gateway reachability shown in the sidebar.
type GatewayState int

// Gateway states.
const (
	GatewayNotConfigured GatewayState = iota
	GatewayOnline
	GatewayOffline
)

// Status is the global status shown at the bottom of the sidebar.
type Status struct {
	Gateway GatewayState
	Model   string
}

// Available reports whether features that need a model can run.
func (s Status) Available() bool {
	return s.Gateway == GatewayOnline
}

// GatewayText returns the label of the gateway state.
func (s Status) GatewayText() string {
	switch s.Gateway {
	case GatewayOnline:
		return "网关在线"
	case GatewayOffline:
		return "网关离线"
	default:
		return "网关未配置"
	}
}

// ModelText returns the current model, or that none is configured.
func (s Status) ModelText() string {
	if s.Model == "" {
		return "模型未配置"
	}
	return s.Model
}

// DisabledReason explains why model features are disabled.
func (s Status) DisabledReason() string {
	switch s.Gateway {
	case GatewayOnline:
		return ""
	case GatewayOffline:
		return "网关离线，恢复连接后可启用对话与创作"
	default:
		return "网关未配置，配置后可启用对话与创作"
	}
}

// Shell is the main view: the title bar above the navigation and the content
// of the current module.
type Shell struct {
	// Theme is the appearance the shell draws, resolved from Preference.
	Theme ui.Mode
	// Preference is the appearance the user chose.
	Preference ui.Preference
	// Focused is whether the window is focused; the title bar dims its
	// traffic lights while it is not.
	Focused bool
	Current ModuleID
	Actions Actions
	// Pages are the content views injected by the app, keyed by module.
	Pages map[ModuleID]Page
	// Status is the global status shown at the bottom of the sidebar.
	Status Status
}

// New creates the shell showing the appearance resolved from pref, where
// systemDark is the desktop appearance, and store as the module.
func New(pref ui.Preference, systemDark bool, actions Actions) *Shell {
	return &Shell{
		Theme:      ui.Resolve(pref, systemDark),
		Preference: pref,
		Focused:    true,
		Current:    ModuleStore,
		Actions:    actions,
	}
}

// View returns the view of the shell.
func (s *Shell) View() func(c *mygoui.Context) {
	return func(c *mygoui.Context) {
		t := ui.Theme(s.Theme)
		c.SetTheme(t)
		mygoui.Column(c).Fill().Background(t.Background).Children(func() {
			ui.TitleBar(c, ui.TitleBarActions{
				Preference:    s.Preference,
				Focused:       s.Focused,
				OnToggleTheme: s.Actions.ToggleTheme,
				OnMinimize:    s.Actions.Minimize,
				OnMaximize:    s.Actions.Maximize,
				OnClose:       s.Actions.Close,
			})
			mygoui.Row(c).Grow(1).Children(func() {
				s.sidebar(c, t)
				s.content(c, t)
			})
		})
	}
}

func (s *Shell) sidebar(c *mygoui.Context, t *mygoui.Theme) {
	nav := mygoui.Column(c).Width(220).Shrink(0).FillHeight().Padding(8).Gap(4).
		Background(t.Surface).BorderWidth(0, 1, 0, 0).BorderColor(t.Border).
		FocusGroup(mygoui.Vertical)
	nav.Children(func() {
		for _, m := range Modules {
			s.navItem(c, t, m)
		}
		mygoui.Spacer(c)
		s.statusFooter(c, t)
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
	page, hasPage := s.Pages[m.ID]
	if hasPage {
		page.View()(c)
		return
	}
	area := mygoui.Column(c).Grow(1).FillHeight().Padding(32).Gap(8).
		Background(t.Background)
	area.Children(func() {
		mygoui.Text(c, m.Name).FontSize(24).Bold().TextColor(t.Text)
		mygoui.Text(c, m.Description).TextColor(t.TextMuted)
		s.disabledNotice(c, t)
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

// disabledNotice explains why the assistant and creation modules are not
// usable until the gateway is online.
func (s *Shell) disabledNotice(c *mygoui.Context, t *mygoui.Theme) {
	if s.Current != ModuleAgent && s.Current != ModuleCreation {
		return
	}
	if reason := s.Status.DisabledReason(); reason != "" {
		mygoui.Text(c, reason).FontSize(12).TextColor(t.TextMuted)
	}
}

// statusFooter shows the gateway, model and usage entry at the bottom right
// of the sidebar.
func (s *Shell) statusFooter(c *mygoui.Context, t *mygoui.Theme) {
	mygoui.Box(c).FillWidth().Height(1).Shrink(0).Background(t.Border)
	footer := mygoui.Column(c).FillWidth().Shrink(0).Padding(4).Gap(6).
		AlignItems(mygoui.End)
	footer.Children(func() {
		gateway := mygoui.Row(c).Gap(6).AlignItems(mygoui.Center)
		gateway.Children(func() {
			mygoui.Box(c).Size(8, 8).Radius(4).Background(s.gatewayColor(t))
			mygoui.Text(c, s.Status.GatewayText()).FontSize(12).TextColor(t.Text)
		})
		mygoui.Text(c, s.Status.ModelText()).FontSize(12).TextColor(t.TextMuted).SingleLine()
		disabled := s.Status.Gateway != GatewayOnline
		usage := mygoui.ButtonBase(c).Label("用量").Shrink(0).Disabled(disabled).
			Padding(4, 8).Radius(t.Radius)
		usage.TextColor(t.TextMuted)
		if !disabled {
			if usage.Pressed() {
				usage.Background(t.SurfacePressed)
			} else if usage.Hovered() {
				usage.Background(t.SurfaceHover)
			}
		}
		usage.Children(func() {
			mygoui.Text(c, "用量").FontSize(12).TextColor(t.TextMuted)
		})
		if disabled {
			mygoui.Text(c, "用量面板将在后续计划开放").FontSize(10).TextColor(t.TextMuted).SingleLine()
		}
	})
}

func (s *Shell) gatewayColor(t *mygoui.Theme) mygoui.Color {
	switch s.Status.Gateway {
	case GatewayOnline:
		return ui.SuccessColor(s.Theme)
	case GatewayOffline:
		return ui.DangerColor(s.Theme)
	default:
		return t.TextMuted
	}
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
