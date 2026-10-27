package shell

import (
	"fmt"
	"testing"

	mygoui "github.com/egoist/mygo/ui"

	"github.com/bowknot/bowknot-agent/internal/ui"
)

func newTester(t *testing.T, s *Shell) *mygoui.Tester {
	t.Helper()
	return mygoui.NewTester(s.View(), 1200, 800)
}

func TestDefaultModuleIsStore(t *testing.T) {
	s := New(ui.PreferenceLight, false, Actions{})
	tester := newTester(t, s)
	if s.Current != ModuleStore {
		t.Errorf("Current = %q, want store", s.Current)
	}
	for _, text := range []string{"Bowknot Agent", "应用", "已安装应用与商店", "内容将在后续计划中实现"} {
		if !tester.HasText(text) {
			t.Errorf("view does not show %q", text)
		}
	}
}

func TestNewResolvesSystemPreference(t *testing.T) {
	tests := []struct {
		name       string
		preference ui.Preference
		systemDark bool
		want       ui.Mode
	}{
		{"system light", ui.PreferenceSystem, false, ui.ModeLight},
		{"system dark", ui.PreferenceSystem, true, ui.ModeDark},
		{"explicit light ignores dark system", ui.PreferenceLight, true, ui.ModeLight},
		{"explicit dark ignores light system", ui.PreferenceDark, false, ui.ModeDark},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := New(tt.preference, tt.systemDark, Actions{})
			if s.Theme != tt.want {
				t.Errorf("Theme = %q, want %q", s.Theme, tt.want)
			}
			if s.Preference != tt.preference {
				t.Errorf("Preference = %q, want %q", s.Preference, tt.preference)
			}
		})
	}
}

func TestClickSwitchesModule(t *testing.T) {
	tests := []struct {
		label string
		want  ModuleID
		text  string
	}{
		{"助手 / Agent", ModuleAgent, "与 Agent 对话并编排任务"},
		{"创作", ModuleCreation, "图片、视频与语音创作"},
		{"设置", ModuleSettings, "应用、网关与数据设置"},
		{"应用", ModuleStore, "已安装应用与商店"},
	}
	for _, tt := range tests {
		t.Run(string(tt.want), func(t *testing.T) {
			s := New(ui.PreferenceLight, false, Actions{})
			tester := newTester(t, s)
			if err := tester.Click(tt.label); err != nil {
				t.Fatal(err)
			}
			tester.Frame()
			if s.Current != tt.want {
				t.Errorf("Current = %q, want %q", s.Current, tt.want)
			}
			if !tester.HasText(tt.text) {
				t.Errorf("content of %q not shown", tt.want)
			}
		})
	}
}

func TestUnknownModuleFallsBackToStore(t *testing.T) {
	s := New(ui.PreferenceLight, false, Actions{})
	s.Current = ModuleID("unknown")
	if got := s.module().ID; got != ModuleStore {
		t.Errorf("module() = %q, want store", got)
	}
}

func TestThemeButtonTogglesTheme(t *testing.T) {
	s := New(ui.PreferenceLight, false, Actions{})
	s.Actions.ToggleTheme = func() {
		s.Preference = s.Preference.Next()
		s.Theme = ui.Resolve(s.Preference, false)
	}
	tester := newTester(t, s)
	if err := tester.Click("切换主题"); err != nil {
		t.Fatal(err)
	}
	if s.Preference != ui.PreferenceDark {
		t.Errorf("Preference = %q, want dark", s.Preference)
	}
	if s.Theme != ui.ModeDark {
		t.Errorf("Theme = %q, want dark", s.Theme)
	}
}

func TestTrafficLightsDimWhenUnfocused(t *testing.T) {
	tests := []struct {
		name    string
		focused bool
		r, g, b uint8
	}{
		{"focused", true, 0xfe, 0xbc, 0x2e},
		{"unfocused", false, 0xe4, 0xe4, 0xe7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := New(ui.PreferenceLight, false, Actions{})
			s.Focused = tt.focused
			tester := newTester(t, s)
			r, g, b := centerPixel(t, tester, "最小化")
			if abs(int(r), int(tt.r)) > 2 || abs(int(g), int(tt.g)) > 2 || abs(int(b), int(tt.b)) > 2 {
				t.Errorf("minimize light = #%02x%02x%02x, want #%02x%02x%02x", r, g, b, tt.r, tt.g, tt.b)
			}
		})
	}
}

func centerPixel(t *testing.T, tester *mygoui.Tester, label string) (uint8, uint8, uint8) {
	t.Helper()
	rect, ok := tester.Find(label)
	if !ok {
		t.Fatalf("no element labeled %q", label)
	}
	img := tester.Image()
	r, g, b, _ := img.At(int(rect.X+rect.W/2), int(rect.Y+rect.H/2)).RGBA()
	return uint8(r >> 8), uint8(g >> 8), uint8(b >> 8)
}

func abs(a, b int) int {
	if a < b {
		return b - a
	}
	return a - b
}

func TestTitleBarButtonsRunActions(t *testing.T) {
	var minimized, maximized, closed bool
	s := New(ui.PreferenceLight, false, Actions{
		Minimize: func() { minimized = true },
		Maximize: func() { maximized = true },
		Close:    func() { closed = true },
	})
	tester := newTester(t, s)
	for _, tt := range []struct {
		label string
		flag  *bool
	}{
		{"最小化", &minimized},
		{"最大化", &maximized},
		{"关闭", &closed},
	} {
		if err := tester.Click(tt.label); err != nil {
			t.Fatal(err)
		}
		if !*tt.flag {
			t.Errorf("clicking %q did not run its action", tt.label)
		}
	}
}

func TestStatusLabels(t *testing.T) {
	tests := []struct {
		name        string
		status      Status
		wantGateway string
		wantModel   string
		wantReason  string
	}{
		{"not configured", Status{}, "网关未配置", "模型未配置", "网关未配置，配置后可启用对话与创作"},
		{"online", Status{Gateway: GatewayOnline, Model: "glm-4"}, "网关在线", "glm-4", ""},
		{"offline", Status{Gateway: GatewayOffline, Model: "glm-4"}, "网关离线", "glm-4", "网关离线，恢复连接后可启用对话与创作"},
		{"online without model", Status{Gateway: GatewayOnline}, "网关在线", "模型未配置", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.status.GatewayText(); got != tt.wantGateway {
				t.Errorf("GatewayText() = %q, want %q", got, tt.wantGateway)
			}
			if got := tt.status.ModelText(); got != tt.wantModel {
				t.Errorf("ModelText() = %q, want %q", got, tt.wantModel)
			}
			if got := tt.status.DisabledReason(); got != tt.wantReason {
				t.Errorf("DisabledReason() = %q, want %q", got, tt.wantReason)
			}
			if got := tt.status.Available(); got != (tt.status.Gateway == GatewayOnline) {
				t.Errorf("Available() = %v for gateway %v", got, tt.status.Gateway)
			}
		})
	}
}

func TestSidebarFooterShowsGatewayStatus(t *testing.T) {
	s := New(ui.PreferenceLight, false, Actions{})
	s.Status = Status{Gateway: GatewayOnline, Model: "glm-4"}
	tester := newTester(t, s)
	for _, text := range []string{"网关在线", "glm-4", "用量"} {
		if !tester.HasText(text) {
			t.Errorf("footer does not show %q", text)
		}
	}
	if tester.HasText("用量将随网关接入开放") {
		t.Error("online footer shows the disabled usage hint")
	}
}

func TestDisabledNoticeFollowsCurrentModule(t *testing.T) {
	tests := []struct {
		module  ModuleID
		status  Status
		visible bool
	}{
		{ModuleAgent, Status{}, true},
		{ModuleCreation, Status{Gateway: GatewayOffline}, true},
		{ModuleSettings, Status{}, false},
		{ModuleAgent, Status{Gateway: GatewayOnline}, false},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s-%v", tt.module, tt.status.Gateway), func(t *testing.T) {
			s := New(ui.PreferenceLight, false, Actions{})
			s.Current = tt.module
			s.Status = tt.status
			tester := newTester(t, s)
			reason := tt.status.DisabledReason()
			if got := tester.HasText(reason) && reason != ""; got != tt.visible {
				t.Errorf("disabled notice visible = %v, want %v", got, tt.visible)
			}
		})
	}
}

// countingPage is a stateful module page used to verify that the shell keeps
// page state while switching modules.
type countingPage struct{ clicks int }

func (p *countingPage) View() func(c *mygoui.Context) {
	return func(c *mygoui.Context) {
		column := mygoui.Column(c).Fill().Padding(16).Gap(8)
		column.Children(func() {
			mygoui.Text(c, fmt.Sprintf("页面点击数 %d", p.clicks))
			b := mygoui.ButtonBase(c).Label("增加计数")
			b.Children(func() { mygoui.Text(c, "增加计数") })
			if b.Clicked() {
				p.clicks++
			}
		})
	}
}

func TestPageReplacesFallbackAndKeepsState(t *testing.T) {
	s := New(ui.PreferenceLight, false, Actions{})
	page := &countingPage{}
	s.Pages = map[ModuleID]Page{ModuleStore: page}
	tester := newTester(t, s)
	if tester.HasText("内容将在后续计划中实现") {
		t.Error("injected page did not replace the placeholder")
	}
	if err := tester.Click("增加计数"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !tester.HasText("页面点击数 1") {
		t.Fatal("page state did not update")
	}
	if err := tester.Click("助手 / Agent"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if tester.HasText("页面点击数 1") {
		t.Error("store page still visible after switching away")
	}
	if err := tester.Click("应用"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !tester.HasText("页面点击数 1") {
		t.Error("page state was not kept after switching back")
	}
}
