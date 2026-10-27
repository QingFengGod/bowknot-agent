package store

import (
	"testing"

	mygoui "github.com/egoist/mygo/ui"
)

func newTester(t *testing.T, p *Page) *mygoui.Tester {
	t.Helper()
	return mygoui.NewTester(p.View(), 900, 720)
}

func newDiscoverPage(t *testing.T) *Page {
	t.Helper()
	p := NewPage(NewRegistry())
	p.Tab = TabDiscover
	return p
}

func sampleRegistry() *Registry {
	return NewRegistry(
		AppInfo{
			ID: "docs", Name: "文档工作台", Version: "1.4.0", Source: "本地",
			Category: "efficiency", Summary: "笔记与文档", Status: StatusInstalled,
			LastOpened: 100,
		},
		AppInfo{
			ID: "code", Name: "代码浏览器", Version: "3.2.0", Source: "本地",
			Category: "developer", Summary: "浏览仓库", Status: StatusUpdatable,
			LastOpened: 300,
		},
		AppInfo{
			ID: "audio", Name: "音频剪辑", Version: "1.8.0", Source: "本地",
			Category: "creation", Summary: "裁剪音频", Status: StatusDisabled,
		},
	)
}

func TestStoreShowsCatalog(t *testing.T) {
	p := newDiscoverPage(t)
	tester := newTester(t, p)
	for _, text := range []string{"App Store", "搜索应用", "全部", "分类：效率", "文档工作台", "共 8 个应用"} {
		if !tester.HasText(text) {
			t.Errorf("view does not show %q", text)
		}
	}
}

func TestStoreSearchFilters(t *testing.T) {
	p := newDiscoverPage(t)
	tester := newTester(t, p)
	if err := tester.Click("搜索应用"); err != nil {
		t.Fatal(err)
	}
	tester.Type("白板")
	tester.Frame()
	if p.Search != "白板" {
		t.Errorf("Search = %q, want 白板", p.Search)
	}
	if !tester.HasText("灵感白板") {
		t.Error("search results do not show 灵感白板")
	}
	if tester.HasText("共 8 个应用") {
		t.Error("count still shows all apps after filtering")
	}
}

func TestStoreCategoryFilters(t *testing.T) {
	p := newDiscoverPage(t)
	tester := newTester(t, p)
	if err := tester.Click("分类：创作"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if p.Category != "creation" {
		t.Errorf("Category = %q, want creation", p.Category)
	}
	if !tester.HasText("设计画板") {
		t.Error("creation category does not show 设计画板")
	}
	if tester.HasText("文档工作台") {
		t.Error("creation category still shows efficiency app")
	}
}

func TestStoreSelectShowsDetail(t *testing.T) {
	p := newDiscoverPage(t)
	tester := newTester(t, p)
	if err := tester.Click("打开代码浏览器"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if p.Selected != "code" {
		t.Errorf("Selected = %q, want code", p.Selected)
	}
	for _, text := range []string{"代码浏览器", "v3.2.0", "浏览仓库、查看差异与提交记录"} {
		if !tester.HasText(text) {
			t.Errorf("detail does not show %q", text)
		}
	}
}

func TestStoreClearFilterRestoresList(t *testing.T) {
	p := newDiscoverPage(t)
	p.Search = "missing-query"
	p.Filtered()
	tester := newTester(t, p)
	if !tester.HasText("未找到匹配应用") {
		t.Fatal("empty state not shown")
	}
	if err := tester.Click("清除筛选"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if p.Search != "" || p.Category != AllCategories {
		t.Errorf("after clear = (%q, %q), want (\"\", all)", p.Search, p.Category)
	}
	if !tester.HasText("文档工作台") {
		t.Error("catalog not restored after clearing")
	}
}

func TestLauncherEmptyState(t *testing.T) {
	p := NewPage(NewRegistry())
	if p.Tab != TabInstalled {
		t.Fatalf("default tab = %v, want installed", p.Tab)
	}
	tester := newTester(t, p)
	if !tester.HasText("还没有安装应用") {
		t.Error("empty launcher state not shown")
	}
	if tester.HasText("App Store") {
		t.Error("installed tab shows the store header")
	}
	if err := tester.Click("浏览商店"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if p.Tab != TabDiscover {
		t.Errorf("Tab = %v after browsing, want discover", p.Tab)
	}
	if !tester.HasText("App Store") {
		t.Error("store not shown after browsing")
	}
}

func TestLauncherShowsInstalledApps(t *testing.T) {
	p := NewPage(sampleRegistry())
	tester := newTester(t, p)
	for _, text := range []string{"应用", "文档工作台", "代码浏览器", "音频剪辑", "共 3 个应用"} {
		if !tester.HasText(text) {
			t.Errorf("launcher does not show %q", text)
		}
	}
	if tester.HasText("设计画板") {
		t.Error("launcher shows an app that is not installed")
	}
}

func TestLauncherFilterAndSearch(t *testing.T) {
	p := NewPage(sampleRegistry())
	tester := newTester(t, p)
	if err := tester.Click("状态：可更新"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if p.StatusFilter != StatusUpdatable {
		t.Errorf("StatusFilter = %q, want updatable", p.StatusFilter)
	}
	if !tester.HasText("代码浏览器") || tester.HasText("音频剪辑") {
		t.Error("updatable filter did not narrow the launcher")
	}
	if err := tester.Click("状态：全部"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if err := tester.Click("搜索已安装应用"); err != nil {
		t.Fatal(err)
	}
	tester.Type("音频")
	tester.Frame()
	if p.InstalledSearch != "音频" {
		t.Errorf("InstalledSearch = %q, want 音频", p.InstalledSearch)
	}
	if !tester.HasText("音频剪辑") || tester.HasText("文档工作台") {
		t.Error("launcher search did not narrow the grid")
	}
}

func TestLauncherRecentOrder(t *testing.T) {
	p := NewPage(sampleRegistry())
	apps := p.Installed()
	if len(apps) != 3 {
		t.Fatalf("Installed() = %d apps, want 3", len(apps))
	}
	if apps[0].ID != "code" || apps[1].ID != "docs" {
		t.Errorf("recent order = [%s %s], want [code docs]", apps[0].ID, apps[1].ID)
	}
	p.Sort = SortName
	apps = p.Installed()
	if apps[0].Name != "代码浏览器" {
		t.Errorf("name order starts with %q, want 代码浏览器", apps[0].Name)
	}
}

func TestLauncherEmptyResultClearsFilter(t *testing.T) {
	p := NewPage(sampleRegistry())
	p.InstalledSearch = "missing"
	tester := newTester(t, p)
	if !tester.HasText("没有符合条件的应用") {
		t.Fatal("no-result state not shown")
	}
	if err := tester.Click("清除筛选"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if p.InstalledSearch != "" || p.StatusFilter != "" {
		t.Errorf("after clear = (%q, %q), want empty", p.InstalledSearch, p.StatusFilter)
	}
	if !tester.HasText("文档工作台") {
		t.Error("launcher not restored after clearing")
	}
}

func TestTabSwitchingKeepsBothSearches(t *testing.T) {
	p := NewPage(sampleRegistry())
	p.InstalledSearch = "文档"
	p.Search = "白板"
	tester := newTester(t, p)
	if !tester.HasText("文档工作台") {
		t.Error("launcher search not applied")
	}
	if err := tester.Click("发现"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !tester.HasText("灵感白板") {
		t.Error("store search not applied after switching tab")
	}
	if err := tester.Click("已安装"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !tester.HasText("文档工作台") {
		t.Error("launcher search was lost after switching tab")
	}
}
