package ui

import (
	"testing"

	mygoui "github.com/egoist/mygo/ui"
)

func layoutTitleBar(t *testing.T, view func(c *mygoui.Context)) *mygoui.Tester {
	t.Helper()
	return mygoui.NewTester(view, 900, 44)
}

func TestWindowTitleBarShowsAppAndButtons(t *testing.T) {
	closed := false
	view := func(c *mygoui.Context) {
		mygoui.Row(c).Fill().Children(func() {
			WindowTitleBar(c, WindowTitleBarActions{
				Title:   "文档工作台",
				Focused: true,
				OnClose: func() { closed = true },
			})
		})
	}
	tester := layoutTitleBar(t, view)
	if !tester.HasText("文档工作台") || !tester.HasText("文") {
		t.Errorf("title bar texts = %v", tester.Texts())
	}
	for _, label := range []string{"最小化", "最大化", "关闭"} {
		if _, ok := tester.Find(label); !ok {
			t.Errorf("title bar does not show the %q button", label)
		}
	}
	if err := tester.Click("关闭"); err != nil {
		t.Fatalf("click close: %v", err)
	}
	if !closed {
		t.Error("close button did not call its action")
	}
}

func TestWindowTitleBarInitial(t *testing.T) {
	if got := firstRune("Sales"); got != "S" {
		t.Errorf("firstRune = %q, want S", got)
	}
	if got := firstRune(""); got != "?" {
		t.Errorf("firstRune of empty = %q, want ?", got)
	}
}

func TestWindowTitleBarUnfocused(t *testing.T) {
	tester := layoutTitleBar(t, func(c *mygoui.Context) {
		mygoui.Row(c).Fill().Children(func() {
			WindowTitleBar(c, WindowTitleBarActions{Title: "App", Focused: false})
		})
	})
	if _, ok := tester.Find("关闭"); !ok {
		t.Error("unfocused title bar lost its window buttons")
	}
}
