// Package store 实现应用商店模块：内置示例目录、筛选与浏览页面。
package store

import (
	"sort"
	"strings"
)

// Status is the installation state of an app.
type Status string

// Installation states, matching requirements 4.4 and FR-APP-01.
const (
	StatusNotInstalled Status = "未安装"
	StatusInstalling   Status = "安装中"
	StatusInstalled    Status = "已安装"
	StatusRunning      Status = "运行中"
	StatusUpdatable    Status = "可更新"
	StatusUpdating     Status = "更新中"
	StatusDisabled     Status = "已禁用"
	StatusCorrupt      Status = "损坏"
)

// validStatuses are the statuses accepted from the registry.
var validStatuses = map[Status]bool{
	StatusNotInstalled: true,
	StatusInstalling:   true,
	StatusInstalled:    true,
	StatusRunning:      true,
	StatusUpdatable:    true,
	StatusUpdating:     true,
	StatusDisabled:     true,
	StatusCorrupt:      true,
}

// Valid reports whether the status is one the registry accepts.
func (s Status) Valid() bool {
	return validStatuses[s]
}

// Installed reports whether the app belongs in the launcher.
func (s Status) Installed() bool {
	return s != StatusNotInstalled
}

// MatchesFilter reports whether the status belongs to a launcher filter. An
// empty filter matches every installed app; the installed filter also covers
// running and updating apps.
func (s Status) MatchesFilter(filter Status) bool {
	if s == StatusNotInstalled {
		return false
	}
	switch filter {
	case "":
		return true
	case StatusInstalled:
		return s == StatusInstalled || s == StatusRunning ||
			s == StatusUpdatable || s == StatusUpdating
	default:
		return s == filter
	}
}

// PrimaryAction returns the label of the launcher's main button.
func (s Status) PrimaryAction() string {
	switch s {
	case StatusInstalling:
		return "取消安装"
	case StatusUpdatable:
		return "更新"
	case StatusUpdating:
		return "更新中"
	case StatusDisabled:
		return "启用"
	case StatusCorrupt:
		return "修复"
	default:
		return "打开"
	}
}

// Removable reports whether the app can be uninstalled, including a damaged
// one whose files are already gone but whose registry entry remains.
func (s Status) Removable() bool {
	switch s {
	case StatusInstalled, StatusRunning, StatusUpdatable, StatusDisabled, StatusCorrupt:
		return true
	default:
		return false
	}
}

// StatusFilters are the filter options of the launcher, the first showing
// every installed app.
var StatusFilters = []Status{"", StatusInstalled, StatusUpdatable, StatusDisabled, StatusCorrupt}

// StatusFilterLabel returns the label of a launcher filter.
func StatusFilterLabel(status Status) string {
	switch status {
	case StatusInstalled:
		return "已安装"
	case StatusUpdatable:
		return "可更新"
	case StatusDisabled:
		return "已禁用"
	case StatusCorrupt:
		return "损坏"
	default:
		return "全部"
	}
}

// SortOrder is how the launcher orders installed apps.
type SortOrder string

// Sort orders.
const (
	SortRecent SortOrder = "最近使用"
	SortName   SortOrder = "名称"
)

// Next returns the other sort order.
func (o SortOrder) Next() SortOrder {
	if o == SortName {
		return SortRecent
	}
	return SortName
}

// AllCategories is the category ID that shows every app.
const AllCategories = "all"

// Category is one filter of the catalog.
type Category struct {
	ID    string
	Label string
}

// Categories are the filters of the browse page, the first one showing all.
var Categories = []Category{
	{AllCategories, "全部"},
	{"efficiency", "效率"},
	{"creation", "创作"},
	{"developer", "开发"},
}

// AppInfo is the view data of one catalog entry. The full manifest format is
// still open (requirements FR-APP-04); this type carries only the fields the
// browse page shows.
type AppInfo struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Version      string `json:"version"`
	Size         string `json:"size"`
	Source       string `json:"source"`
	Category     string `json:"category"`
	Summary      string `json:"summary"`
	ReleaseNotes string `json:"release_notes"`
	Status       Status `json:"status"`
	// LastOpened is the Unix time of the last launch, used by the launcher's
	// recent order; zero means the app has not been opened yet.
	LastOpened int64 `json:"last_opened"`
}

// Catalog is the built-in sample catalog. It is not fetched from a store
// server, and every entry is marked as sample data rather than a real app.
var Catalog = []AppInfo{
	{
		ID: "docs", Name: "文档工作台", Version: "1.4.0", Size: "12.4 MB",
		Source: "内置示例", Category: "efficiency",
		Summary:      "把笔记、文档与任务放在一个工作区里。",
		ReleaseNotes: "示例版本，用于验证商店列表与详情布局。",
		Status:       StatusNotInstalled,
	},
	{
		ID: "design", Name: "设计画板", Version: "2.1.3", Size: "38.7 MB",
		Source: "内置示例", Category: "creation",
		Summary:      "在浏览器里画界面、做原型和导出切图。",
		ReleaseNotes: "示例版本，包含画板、图层与导出入口。",
		Status:       StatusNotInstalled,
	},
	{
		ID: "tasks", Name: "任务看板", Version: "0.9.2", Size: "8.2 MB",
		Source: "内置示例", Category: "efficiency",
		Summary:      "用看板整理待办、迭代与负责人。",
		ReleaseNotes: "示例版本，用于验证分类筛选。",
		Status:       StatusNotInstalled,
	},
	{
		ID: "whiteboard", Name: "灵感白板", Version: "1.0.6", Size: "21.5 MB",
		Source: "内置示例", Category: "creation",
		Summary:      "像纸一样自由书写，适合头脑风暴与草图。",
		ReleaseNotes: "示例版本，包含便签与手绘线条。",
		Status:       StatusNotInstalled,
	},
	{
		ID: "code", Name: "代码浏览器", Version: "3.2.0", Size: "16.8 MB",
		Source: "内置示例", Category: "developer",
		Summary:      "在桌面里浏览仓库、查看差异与提交记录。",
		ReleaseNotes: "示例版本，用于验证开发分类与详情。",
		Status:       StatusNotInstalled,
	},
	{
		ID: "playground", Name: "模型调试台", Version: "0.4.1", Size: "5.6 MB",
		Source: "内置示例", Category: "developer",
		Summary:      "对比提示词、参数与不同模型的输出。",
		ReleaseNotes: "示例版本，暂不发起真实模型请求。",
		Status:       StatusNotInstalled,
	},
	{
		ID: "audio", Name: "音频剪辑", Version: "1.8.0", Size: "27.3 MB",
		Source: "内置示例", Category: "creation",
		Summary:      "裁剪、拼接与导出短音频。",
		ReleaseNotes: "示例版本，用于验证创作分类。",
		Status:       StatusNotInstalled,
	},
	{
		ID: "focus", Name: "专注计时器", Version: "2.0.4", Size: "3.1 MB",
		Source: "内置示例", Category: "efficiency",
		Summary:      "用番茄钟安排专注与休息。",
		ReleaseNotes: "示例版本，包含计时与统计占位。",
		Status:       StatusNotInstalled,
	},
}

// CategoryLabel returns the label of the category ID, or the ID itself when
// the category is unknown.
func CategoryLabel(id string) string {
	for _, c := range Categories {
		if c.ID == id {
			return c.Label
		}
	}
	return id
}

// Filter returns the catalog apps matching the search query and category. The
// query matches the name or the summary, ignoring case and surrounding
// spaces; the all category matches every app.
func Filter(apps []AppInfo, query, category string) []AppInfo {
	q := strings.ToLower(strings.TrimSpace(query))
	out := make([]AppInfo, 0, len(apps))
	for _, a := range apps {
		if category != "" && category != AllCategories && a.Category != category {
			continue
		}
		if q != "" &&
			!strings.Contains(strings.ToLower(a.Name), q) &&
			!strings.Contains(strings.ToLower(a.Summary), q) {
			continue
		}
		out = append(out, a)
	}
	return out
}

// FilterInstalled returns the installed apps matching the search query and
// status filter. The query matches the name or summary, ignoring case and
// surrounding spaces.
func FilterInstalled(apps []AppInfo, query string, filter Status) []AppInfo {
	q := strings.ToLower(strings.TrimSpace(query))
	out := make([]AppInfo, 0, len(apps))
	for _, a := range apps {
		if !a.Status.MatchesFilter(filter) {
			continue
		}
		if q != "" &&
			!strings.Contains(strings.ToLower(a.Name), q) &&
			!strings.Contains(strings.ToLower(a.Summary), q) {
			continue
		}
		out = append(out, a)
	}
	return out
}

// SortApps returns a sorted copy of apps. The recent order puts apps opened
// last first, with never-opened apps in name order; the name order sorts by
// name. Equal keys keep catalog order.
func SortApps(apps []AppInfo, order SortOrder) []AppInfo {
	out := append([]AppInfo(nil), apps...)
	if order == SortName {
		sort.SliceStable(out, func(i, j int) bool {
			return out[i].Name < out[j].Name
		})
		return out
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].LastOpened != out[j].LastOpened {
			return out[i].LastOpened > out[j].LastOpened
		}
		return out[i].Name < out[j].Name
	})
	return out
}
