package shell

import (
	"testing"

	mygoui "github.com/egoist/mygo/ui"

	"github.com/bowknot/bowknot-agent/internal/ui"
)

func newTester(t *testing.T, s *Shell) *mygoui.Tester {
	t.Helper()
	return mygoui.NewTester(s.View(), 1200, 800)
}

func TestDefaultModuleIsStore(t *testing.T) {
	s := New(ui.ModeLight, Actions{})
	tester := newTester(t, s)
	if s.Current != ModuleStore {
		t.Errorf("Current = %q, want store", s.Current)
	}
	for _, text := range []string{"Bowknot Agent", "App Store", "浏览与安装 WebApp", "内容将在后续计划中实现"} {
		if !tester.HasText(text) {
			t.Errorf("view does not show %q", text)
		}
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
		{"App Store", ModuleStore, "浏览与安装 WebApp"},
	}
	for _, tt := range tests {
		t.Run(string(tt.want), func(t *testing.T) {
			s := New(ui.ModeLight, Actions{})
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
	s := New(ui.ModeLight, Actions{})
	s.Current = ModuleID("unknown")
	if got := s.module().ID; got != ModuleStore {
		t.Errorf("module() = %q, want store", got)
	}
}

func TestThemeButtonTogglesTheme(t *testing.T) {
	s := New(ui.ModeLight, Actions{})
	s.Actions.ToggleTheme = func() { s.Theme = s.Theme.Toggle() }
	tester := newTester(t, s)
	if err := tester.Click("切换主题"); err != nil {
		t.Fatal(err)
	}
	if s.Theme != ui.ModeDark {
		t.Errorf("Theme = %q, want dark", s.Theme)
	}
}

func TestTitleBarButtonsRunActions(t *testing.T) {
	var minimized, maximized, closed bool
	s := New(ui.ModeLight, Actions{
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
