package task

import (
	"fmt"
	"testing"
	"time"

	mygoui "github.com/egoist/mygo/ui"
)

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

type fakeActions struct {
	store    *Store
	busy     bool
	resumed  []string
	terminat []string
	deleted  []string
}

func (f *fakeActions) Confirm(string) error { return nil }

func (f *fakeActions) Cancel(string) {}

func (f *fakeActions) Retry(string, string) error { return nil }

func (f *fakeActions) Resume(groupID string) error {
	f.resumed = append(f.resumed, groupID)
	return nil
}

func (f *fakeActions) Terminate(groupID string) error {
	f.terminat = append(f.terminat, groupID)
	return nil
}

func (f *fakeActions) Delete(groupID string) error {
	f.deleted = append(f.deleted, groupID)
	return f.store.Delete(groupID)
}

func (f *fakeActions) CleanTerminal() (int, error) {
	return f.store.DeleteTerminal()
}

func (f *fakeActions) Busy() bool { return f.busy }

func saveGroup(t *testing.T, store *Store, g *Group) {
	t.Helper()
	if err := store.Save(g); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
}

func TestPageEmptyState(t *testing.T) {
	p := NewPage(NewStore(""), nil)
	tester := newPageTester(t, p)
	for _, text := range []string{"任务中心", "选择一个任务计划查看详情", "清理已结束任务"} {
		if !tester.HasText(text) {
			t.Errorf("view does not show %q", text)
		}
	}
}

func TestPageGroupDetail(t *testing.T) {
	store := NewStore("")
	g := newGroupForTest(t, TaskDone)
	g.ID = "g-00000001"
	g.Status = GroupDone
	g.Summary = "整体完成，产出清单已交付。"
	g.Tasks[0].Report = &Report{
		Conclusion: "清单已整理",
		Artifacts:  []string{"任务清单.md"},
		OpenIssues: []string{"缺少负责人"},
	}
	saveGroup(t, store, g)

	opened := ""
	p := NewPage(store, &fakeActions{store: store})
	p.Titles = func(string) string { return "季度整理" }
	p.OpenConversation = func(id string) { opened = id }
	p.Selected = g.ID
	tester := newPageTester(t, p)

	for _, text := range []string{
		"任务计划 1 项", "已完成", "季度整理", "主 Agent 汇总", g.Summary,
		"任务 1 项，已完成 1 项", "结论：清单已整理", "产出物：任务清单.md", "未解决：缺少负责人",
	} {
		if !tester.HasText(text) {
			t.Errorf("detail does not show %q", text)
		}
	}
	clickAndFrame(t, tester, "打开会话")
	if opened != g.ConversationID {
		t.Errorf("opened conversation = %q, want %q", opened, g.ConversationID)
	}
}

func TestPageFilterHidesOtherGroups(t *testing.T) {
	store := NewStore("")
	pending := newGroupForTest(t, TaskPending, TaskPending)
	pending.ID = "g-00000001"
	done := newGroupForTest(t, TaskDone)
	done.ID = "g-00000002"
	done.Status = GroupDone
	saveGroup(t, store, pending)
	saveGroup(t, store, done)

	p := NewPage(store, &fakeActions{store: store})
	tester := newPageTester(t, p)
	if !tester.HasText("任务计划 2 项") || !tester.HasText("任务计划 1 项") {
		t.Fatal("both groups missing from the list")
	}
	clickAndFrame(t, tester, "筛选：已完成")
	if p.Filter != GroupDone {
		t.Fatalf("filter = %q, want %q", p.Filter, GroupDone)
	}
	if tester.HasText("任务计划 2 项") {
		t.Error("pending group still listed")
	}
	if !tester.HasText("任务计划 1 项") {
		t.Error("done group not listed")
	}
}

func TestPageDeleteConfirm(t *testing.T) {
	store := NewStore("")
	g := newGroupForTest(t, TaskDone)
	g.ID = "g-00000001"
	g.Status = GroupDone
	saveGroup(t, store, g)

	actions := &fakeActions{store: store}
	p := NewPage(store, actions)
	p.Selected = g.ID
	tester := newPageTester(t, p)
	clickAndFrame(t, tester, "删除")
	if p.ConfirmDeleteID != g.ID {
		t.Fatalf("confirmation = %q, want %q", p.ConfirmDeleteID, g.ID)
	}
	if !tester.HasText("删除该任务计划后不可恢复，确认删除？") {
		t.Fatal("confirmation text missing")
	}
	clickAndFrame(t, tester, "确认删除")
	if len(actions.deleted) != 1 || actions.deleted[0] != g.ID {
		t.Fatalf("deleted = %+v", actions.deleted)
	}
	if _, ok := store.Get(g.ID); ok {
		t.Error("group still stored")
	}
	if p.Selected != "" {
		t.Errorf("selection = %q, want empty", p.Selected)
	}
}

func TestPageCleanTerminal(t *testing.T) {
	store := NewStore("")
	pending := newGroupForTest(t, TaskPending)
	pending.ID = "g-00000001"
	done := newGroupForTest(t, TaskDone)
	done.ID = "g-00000002"
	done.Status = GroupDone
	saveGroup(t, store, pending)
	saveGroup(t, store, done)

	p := NewPage(store, &fakeActions{store: store})
	tester := newPageTester(t, p)
	clickAndFrame(t, tester, "清理已结束任务")
	if !tester.HasText("将删除所有已完成、已失败、已取消与已中断的任务计划，不可恢复。") {
		t.Fatal("clean confirmation missing")
	}
	clickAndFrame(t, tester, "确认清理")
	if p.ConfirmClean {
		t.Error("confirmation still open")
	}
	if _, ok := store.Get(done.ID); ok {
		t.Error("settled group was kept")
	}
	if _, ok := store.Get(pending.ID); !ok {
		t.Error("pending group was removed")
	}
}

func TestPageInterruptedActions(t *testing.T) {
	store := NewStore("")
	g := newGroupForTest(t, TaskDone, TaskInterrupted)
	g.ID = "g-00000001"
	g.Status = GroupInterrupted
	saveGroup(t, store, g)

	actions := &fakeActions{store: store}
	p := NewPage(store, actions)
	p.Selected = g.ID
	tester := newPageTester(t, p)
	if !tester.HasText("任务因应用退出而中断，可继续执行剩余任务或终止。") {
		t.Fatal("interrupted hint missing")
	}
	clickAndFrame(t, tester, "继续")
	if len(actions.resumed) != 1 || actions.resumed[0] != g.ID {
		t.Fatalf("resumed = %+v", actions.resumed)
	}
	clickAndFrame(t, tester, "终止")
	if len(actions.terminat) != 1 || actions.terminat[0] != g.ID {
		t.Fatalf("terminated = %+v", actions.terminat)
	}
}

func TestPageFailureShowsError(t *testing.T) {
	store := NewStore("")
	g := newGroupForTest(t, TaskFailed)
	g.ID = "g-00000001"
	g.Status = GroupFailed
	g.Tasks[0].ErrorCode = "timeout"
	g.Tasks[0].ErrorMessage = "请求超时"
	saveGroup(t, store, g)

	p := NewPage(store, &fakeActions{store: store})
	p.Selected = g.ID
	tester := newPageTester(t, p)
	if !tester.HasText("失败 · timeout · 请求超时") {
		t.Fatal("task error missing")
	}
	if !tester.HasText("重试") {
		t.Fatal("retry action missing")
	}
}

func TestPageLimitsShown(t *testing.T) {
	store := NewStore("")
	now := time.Now()
	for i := 0; i < 3; i++ {
		g := NewGroup(now, "c-1a2b3c4d", 0, "p-1a2b3c4d", "m1")
		g.ID = fmt.Sprintf("g-%08d", i)
		g.Status = GroupDone
		task := NewTask(now, "目标", "", "", "a-1a2b3c4d", "资料整理")
		task.Status = TaskDone
		g.Tasks = append(g.Tasks, task)
		saveGroup(t, store, g)
	}
	p := NewPage(store, &fakeActions{store: store})
	tester := newPageTester(t, p)
	if !tester.HasText(fmt.Sprintf("3 / %d", MaxGroups)) {
		t.Fatal("limit counter missing")
	}
}
