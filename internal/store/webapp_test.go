package store

import (
	"testing"

	mygoui "github.com/egoist/mygo/ui"

	"github.com/QingFengGod/bowknot-agent/internal/ui"
)

func newWebAppTester(t *testing.T, v *webAppView) *mygoui.Tester {
	t.Helper()
	return mygoui.NewTester(v.View(), 900, 600)
}

func newTestWebAppView(state webAppState, detail string) *webAppView {
	v := &webAppView{title: "文档工作台", theme: ui.ModeLight, state: state, detail: detail, focused: true}
	v.onMinimize = func() {}
	v.onMaximize = func() {}
	v.onClose = func() {}
	v.onRetry = func() {}
	return v
}

func TestWebAppViewLoading(t *testing.T) {
	tester := newWebAppTester(t, newTestWebAppView(webAppLoading, ""))
	for _, want := range []string{"文档工作台", "正在加载应用…"} {
		if !tester.HasText(want) {
			t.Errorf("loading view does not show %q", want)
		}
	}
}

func TestWebAppViewFailed(t *testing.T) {
	retried, closed := false, false
	v := newTestWebAppView(webAppFailed, "无法访问文件（错误码 -2）")
	v.onRetry = func() { retried = true }
	v.onClose = func() { closed = true }
	tester := newWebAppTester(t, v)
	for _, want := range []string{"应用加载失败", "无法访问文件（错误码 -2）", "重新加载", "关闭窗口"} {
		if !tester.HasText(want) {
			t.Errorf("error view does not show %q", want)
		}
	}
	if err := tester.Click("重新加载"); err != nil {
		t.Fatalf("click retry: %v", err)
	}
	if !retried {
		t.Error("retry did not call its action")
	}
	if err := tester.Click("关闭窗口"); err != nil {
		t.Fatalf("click close: %v", err)
	}
	if !closed {
		t.Error("close did not call its action")
	}
}

func TestWebAppViewCrashed(t *testing.T) {
	v := newTestWebAppView(webAppCrashed, "应用已连续崩溃 3 次，建议关闭窗口并在应用页卸载后重新安装。")
	tester := newWebAppTester(t, v)
	for _, want := range []string{"应用无响应", "连续崩溃 3 次", "重新加载"} {
		if !tester.HasText(want) {
			t.Errorf("crash view does not show %q", want)
		}
	}
}

func TestWebAppSelection(t *testing.T) {
	if err := validateAppID("docs"); err != nil {
		t.Errorf("validateAppID(docs) = %v", err)
	}
	for _, id := range []string{"", "Docs", "../docs", "docs/app"} {
		if err := validateAppID(id); err == nil {
			t.Errorf("validateAppID(%q) = nil, want error", id)
		}
	}
}
