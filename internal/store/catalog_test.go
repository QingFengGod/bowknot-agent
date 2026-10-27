package store

import "testing"

func TestFilter(t *testing.T) {
	apps := []AppInfo{
		{ID: "notes", Name: "Notes", Summary: "Local notes", Category: "efficiency"},
		{ID: "canvas", Name: "设计画板", Summary: "A WebApp canvas", Category: "creation"},
		{ID: "code", Name: "Code", Summary: "Browse repository history", Category: "developer"},
	}
	tests := []struct {
		name     string
		query    string
		category string
		want     []string
	}{
		{"all", "", AllCategories, []string{"notes", "canvas", "code"}},
		{"name case insensitive", "NOTES", AllCategories, []string{"notes"}},
		{"summary case insensitive", "webapp", AllCategories, []string{"canvas"}},
		{"query trimmed", "  code  ", AllCategories, []string{"code"}},
		{"category", "", "creation", []string{"canvas"}},
		{"category and query", "canvas", "creation", []string{"canvas"}},
		{"no match", "missing", AllCategories, nil},
		{"unknown category", "", "unknown", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Filter(apps, tt.query, tt.category)
			if len(got) != len(tt.want) {
				t.Fatalf("Filter() returned %d apps, want %d: %+v", len(got), len(tt.want), got)
			}
			for i, id := range tt.want {
				if got[i].ID != id {
					t.Errorf("Filter()[%d].ID = %q, want %q", i, got[i].ID, id)
				}
			}
		})
	}
}

func TestCategoryLabel(t *testing.T) {
	if got := CategoryLabel("creation"); got != "创作" {
		t.Errorf("CategoryLabel(creation) = %q, want 创作", got)
	}
	if got := CategoryLabel("unknown"); got != "unknown" {
		t.Errorf("CategoryLabel(unknown) = %q, want unknown", got)
	}
}

func TestNewPageSelectsFirstApp(t *testing.T) {
	p := NewPage(NewRegistry())
	if p.Category != AllCategories {
		t.Errorf("Category = %q, want %q", p.Category, AllCategories)
	}
	if p.Selected != Catalog[0].ID {
		t.Errorf("Selected = %q, want %q", p.Selected, Catalog[0].ID)
	}
	if got := len(p.Filtered()); got != len(Catalog) {
		t.Errorf("Filtered() = %d apps, want %d", got, len(Catalog))
	}
}

func TestPageSelectionFollowsFilter(t *testing.T) {
	p := NewPage(NewRegistry())
	p.Category = "developer"
	apps := p.Filtered()
	if len(apps) == 0 {
		t.Fatal("developer category has no sample apps")
	}
	if p.Selected != apps[0].ID {
		t.Errorf("Selected = %q, want first developer app %q", p.Selected, apps[0].ID)
	}
	p.Category = AllCategories
	p.Search = "文档"
	p.Filtered()
	if p.Selected != "docs" {
		t.Errorf("Selected = %q, want docs", p.Selected)
	}
	p.Search = "missing"
	apps = p.Filtered()
	if len(apps) != 0 {
		t.Fatalf("Filtered() = %d apps, want 0", len(apps))
	}
	if p.Selected != "" {
		t.Errorf("Selected = %q, want empty", p.Selected)
	}
}

func TestStatusMatrix(t *testing.T) {
	tests := []struct {
		status    Status
		installed bool
		action    string
	}{
		{StatusNotInstalled, false, "打开"},
		{StatusInstalling, true, "取消安装"},
		{StatusInstalled, true, "打开"},
		{StatusRunning, true, "打开"},
		{StatusUpdatable, true, "更新"},
		{StatusUpdating, true, "更新中"},
		{StatusDisabled, true, "启用"},
		{StatusCorrupt, true, "修复"},
	}
	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if !tt.status.Valid() {
				t.Errorf("%q is not valid", tt.status)
			}
			if got := tt.status.Installed(); got != tt.installed {
				t.Errorf("Installed() = %v, want %v", got, tt.installed)
			}
			if got := tt.status.PrimaryAction(); got != tt.action {
				t.Errorf("PrimaryAction() = %q, want %q", got, tt.action)
			}
		})
	}
	if Status("unknown").Valid() {
		t.Error("unknown status is valid")
	}
}

func TestStatusMatchesFilter(t *testing.T) {
	tests := []struct {
		status Status
		filter Status
		want   bool
	}{
		{StatusNotInstalled, "", false},
		{StatusInstalled, "", true},
		{StatusRunning, StatusInstalled, true},
		{StatusUpdating, StatusInstalled, true},
		{StatusUpdatable, StatusInstalled, true},
		{StatusDisabled, StatusInstalled, false},
		{StatusUpdatable, StatusUpdatable, true},
		{StatusInstalled, StatusUpdatable, false},
		{StatusDisabled, StatusDisabled, true},
		{StatusCorrupt, StatusCorrupt, true},
	}
	for _, tt := range tests {
		if got := tt.status.MatchesFilter(tt.filter); got != tt.want {
			t.Errorf("%q.MatchesFilter(%q) = %v, want %v", tt.status, tt.filter, got, tt.want)
		}
	}
}

func TestFilterInstalled(t *testing.T) {
	apps := []AppInfo{
		{ID: "docs", Name: "文档工作台", Summary: "笔记", Status: StatusInstalled, LastOpened: 10},
		{ID: "code", Name: "代码浏览器", Summary: "仓库", Status: StatusUpdatable, LastOpened: 30},
		{ID: "audio", Name: "音频剪辑", Summary: "声音", Status: StatusDisabled},
		{ID: "broken", Name: "损坏应用", Summary: "无法启动", Status: StatusCorrupt},
	}
	tests := []struct {
		name   string
		query  string
		filter Status
		want   []string
	}{
		{"all", "", "", []string{"docs", "code", "audio", "broken"}},
		{"installed group", "", StatusInstalled, []string{"docs", "code"}},
		{"updatable", "", StatusUpdatable, []string{"code"}},
		{"disabled", "", StatusDisabled, []string{"audio"}},
		{"corrupt", "", StatusCorrupt, []string{"broken"}},
		{"search name", "代码", "", []string{"code"}},
		{"search summary", "声音", "", []string{"audio"}},
		{"search and filter", "代码", StatusInstalled, []string{"code"}},
		{"no match", "missing", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FilterInstalled(apps, tt.query, tt.filter)
			if len(got) != len(tt.want) {
				t.Fatalf("FilterInstalled() returned %d apps, want %d: %+v", len(got), len(tt.want), got)
			}
			for i, id := range tt.want {
				if got[i].ID != id {
					t.Errorf("FilterInstalled()[%d].ID = %q, want %q", i, got[i].ID, id)
				}
			}
		})
	}
}

func TestSortApps(t *testing.T) {
	apps := []AppInfo{
		{ID: "b", Name: "Beta", LastOpened: 10},
		{ID: "a", Name: "Alpha", LastOpened: 30},
		{ID: "c", Name: "Gamma"},
	}
	recent := SortApps(apps, SortRecent)
	if got := []string{recent[0].ID, recent[1].ID, recent[2].ID}; got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("SortRecent IDs = %v, want [a b c]", got)
	}
	byName := SortApps(apps, SortName)
	if byName[0].ID != "a" || byName[1].ID != "b" || byName[2].ID != "c" {
		t.Errorf("SortName IDs = %v, want [a b c]", []string{byName[0].ID, byName[1].ID, byName[2].ID})
	}
	if apps[0].ID != "b" {
		t.Error("SortApps modified the input slice")
	}
	if SortRecent.Next() != SortName || SortName.Next() != SortRecent {
		t.Error("SortOrder.Next() does not toggle")
	}
}

func TestSelectedApp(t *testing.T) {
	p := NewPage(NewRegistry())
	app, ok := p.SelectedApp()
	if !ok {
		t.Fatal("SelectedApp() not found")
	}
	if app.ID != p.Selected {
		t.Errorf("SelectedApp().ID = %q, want %q", app.ID, p.Selected)
	}
	p.Selected = ""
	if _, ok := p.SelectedApp(); ok {
		t.Error("SelectedApp() found an app with no selection")
	}
}
