package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	mygoui "github.com/egoist/mygo/ui"
)

// fakeAgents is a stand-in Agents over in-memory enable lists.
type fakeAgents struct {
	views  []AgentView
	lists  map[string][]string
	setErr error
}

func newFakeAgents() *fakeAgents {
	return &fakeAgents{
		views: []AgentView{
			{ID: "main", Name: "主 Agent", Main: true},
			{ID: "a-00000001", Name: "整理员"},
		},
		lists: map[string][]string{},
	}
}

func (f *fakeAgents) Views() []AgentView { return f.views }

func (f *fakeAgents) EnabledPlugins(agentID string) []string { return f.lists[agentID] }

func (f *fakeAgents) SetEnabledPlugins(agentID string, ids []string) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.lists[agentID] = append([]string(nil), ids...)
	return nil
}

func newPage(t *testing.T) (*Page, *Store, *fakeAgents) {
	t.Helper()
	s := NewStore(filepath.Join(t.TempDir(), "plugins"), "0.1.0")
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	agents := newFakeAgents()
	p := NewPage(s, agents)
	return p, s, agents
}

func newPageTester(t *testing.T, p *Page) *mygoui.Tester {
	t.Helper()
	return mygoui.NewTester(p.View(), 1000, 720)
}

func clickAndFrame(t *testing.T, tester *mygoui.Tester, label string) {
	t.Helper()
	if err := tester.Click(label); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
}

func TestPageEmptyState(t *testing.T) {
	p, _, _ := newPage(t)
	tester := newPageTester(t, p)
	for _, text := range []string{"插件", "还没有已安装的插件", "导入 .bkplugin", "导入目录", "按 Agent 启用"} {
		if !tester.HasText(text) {
			t.Errorf("view does not show %q", text)
		}
	}
}

func TestPageInstalledList(t *testing.T) {
	p, s, _ := newPage(t)
	if _, err := s.Install(installSource(t, true, "1.2.0"), true); err != nil {
		t.Fatal(err)
	}
	tester := newPageTester(t, p)
	for _, text := range []string{
		"共 1 个插件", "周报助手", "v1.2.0", "Bowknot", "fs.read", "memory.search",
		"按 Agent 启用", "主 Agent", "未启用", "卸载",
	} {
		if !tester.HasText(text) {
			t.Errorf("view does not show %q", text)
		}
	}
}

func TestPageToggleEnable(t *testing.T) {
	p, s, agents := newPage(t)
	if _, err := s.Install(installSource(t, true, "1.2.0"), true); err != nil {
		t.Fatal(err)
	}
	tester := newPageTester(t, p)
	clickAndFrame(t, tester, "切换启用 周报助手")
	if got := agents.lists["main"]; len(got) != 1 || got[0] != "weekly-report" {
		t.Fatalf("main enable list = %v, want weekly-report", got)
	}
	tester.Frame()
	if !tester.HasText("已启用") {
		t.Error("view does not show the enabled state")
	}
	clickAndFrame(t, tester, "切换启用 周报助手")
	if got := agents.lists["main"]; len(got) != 0 {
		t.Fatalf("main enable list = %v, want empty", got)
	}
}

func TestPageToggleKeepsOrder(t *testing.T) {
	p, s, agents := newPage(t)
	if _, err := s.Install(installSource(t, true, "1.2.0"), true); err != nil {
		t.Fatal(err)
	}
	second := strings.Replace(validManifestJSON(), `"id": "weekly-report"`, `"id": "note-keeper"`, 1)
	if _, err := s.Install(writeManifestDir(t, second, map[string]string{"skills/report.md": "# n\n"}), true); err != nil {
		t.Fatal(err)
	}
	agents.lists["main"] = []string{"note-keeper", "weekly-report"}
	tester := newPageTester(t, p)
	// weekly-report is enabled; the first click disables it and keeps the
	// other entry, the second click re-enables it at the end.
	clickAndFrame(t, tester, "切换启用 周报助手")
	got := agents.lists["main"]
	if len(got) != 1 || got[0] != "note-keeper" {
		t.Fatalf("enable list = %v, want note-keeper only", got)
	}
	clickAndFrame(t, tester, "切换启用 周报助手")
	got = agents.lists["main"]
	if len(got) != 2 || got[0] != "note-keeper" || got[1] != "weekly-report" {
		t.Fatalf("enable list = %v, want both entries", got)
	}
}

func TestPageReconcileRemovesInvalid(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "plugins"), "0.1.0")
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Install(installSource(t, true, "1.0.0"), true); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(s.PluginDir("weekly-report"), ManifestFileName)); err != nil {
		t.Fatal(err)
	}
	agents := newFakeAgents()
	agents.lists["main"] = []string{"weekly-report", "gone-plugin"}
	p := NewPage(s, agents)
	if got := agents.lists["main"]; len(got) != 0 {
		t.Fatalf("enable list after reconcile = %v, want empty", got)
	}
	tester := newPageTester(t, p)
	if !tester.HasText("移除失效插件 gone-plugin、weekly-report") &&
		!tester.HasText("移除失效插件 weekly-report、gone-plugin") {
		t.Error("view does not show the reconcile notice")
	}
}

func TestPageImportFlow(t *testing.T) {
	p, s, _ := newPage(t)
	src := installSource(t, true, "1.2.3")
	p.beginImport(src, true)
	if !p.ImportOpen || p.ImportManifest == nil {
		t.Fatal("beginImport did not open the confirmation")
	}
	tester := newPageTester(t, p)
	if !tester.HasText("将导入插件 周报助手 v1.2.3") {
		t.Error("view does not show the import confirmation")
	}
	clickAndFrame(t, tester, "确认导入")
	if _, ok := s.Get("weekly-report"); !ok {
		t.Fatal("plugin was not installed")
	}
	if !tester.HasText("已导入 周报助手 v1.2.3") {
		t.Error("view does not show the import notice")
	}
}

func TestPageImportOverwriteAndDowngrade(t *testing.T) {
	p, s, _ := newPage(t)
	if _, err := s.Install(installSource(t, true, "1.2.0"), true); err != nil {
		t.Fatal(err)
	}
	// Same version: the confirmation says overwrite.
	p.beginImport(installSource(t, true, "1.2.0"), true)
	if !p.ImportOpen {
		t.Fatal("beginImport did not open the overwrite confirmation")
	}
	tester := newPageTester(t, p)
	if !tester.HasText("将覆盖为 v1.2.0") {
		t.Error("view does not show the overwrite confirmation")
	}
	clickAndFrame(t, tester, "取消")
	if _, ok := s.Get("weekly-report"); !ok {
		t.Fatal("cancel should not uninstall")
	}

	// Downgrade: rejected before the confirmation.
	p.beginImport(installSource(t, true, "1.0.0"), true)
	if p.ImportOpen {
		t.Fatal("downgrade should not open the confirmation")
	}
	if !strings.Contains(p.Error, "不能降级导入") {
		t.Errorf("error = %q, want a downgrade rejection", p.Error)
	}
}

func TestPageUninstallFlow(t *testing.T) {
	p, s, agents := newPage(t)
	if _, err := s.Install(installSource(t, true, "1.2.0"), true); err != nil {
		t.Fatal(err)
	}
	agents.lists["main"] = []string{"weekly-report"}
	agents.lists["a-00000001"] = []string{"weekly-report"}
	tester := newPageTester(t, p)
	clickAndFrame(t, tester, "卸载")
	if !tester.HasText("仍被 主 Agent、整理员 启用") {
		t.Error("view does not warn about enabled agents")
	}
	clickAndFrame(t, tester, "确认卸载")
	if _, ok := s.Get("weekly-report"); ok {
		t.Fatal("plugin still installed after uninstall")
	}
	for _, id := range []string{"main", "a-00000001"} {
		if got := agents.lists[id]; len(got) != 0 {
			t.Errorf("enable list of %s = %v, want empty", id, got)
		}
	}
	if _, err := os.Stat(s.PluginDir("weekly-report")); !os.IsNotExist(err) {
		t.Errorf("plugin directory should be gone, err %v", err)
	}
	if !tester.HasText("已卸载 周报助手 v1.2.0") {
		t.Error("view does not show the uninstall notice")
	}
}

func TestPageEnableError(t *testing.T) {
	p, s, agents := newPage(t)
	if _, err := s.Install(installSource(t, true, "1.2.0"), true); err != nil {
		t.Fatal(err)
	}
	agents.setErr = os.ErrPermission
	tester := newPageTester(t, p)
	clickAndFrame(t, tester, "切换启用 周报助手")
	if !tester.HasText(os.ErrPermission.Error()) {
		t.Error("view does not show the enable error")
	}
}
