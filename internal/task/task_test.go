package task

import (
	"strings"
	"testing"
	"time"
)

func TestNewIDs(t *testing.T) {
	for _, id := range []string{NewGroupID(), NewGroupID()} {
		if !ValidGroupID(id) {
			t.Errorf("group ID %q is invalid", id)
		}
		if !ValidTaskID("t-" + id[2:]) {
			t.Errorf("task ID derived from %q is invalid", id)
		}
	}
	if NewGroupID() == NewGroupID() {
		t.Error("group IDs repeat")
	}
}

func TestValidIDs(t *testing.T) {
	cases := []struct {
		id   string
		ok   bool
		kind string
	}{
		{"g-1a2b3c4d", true, "group"},
		{"g-1A2B3C4D", false, "group"},
		{"t-1a2b3c4d", true, "task"},
		{"t-1a2b3c4", false, "task"},
		{"a-1a2b3c4d", true, "task"},
		{"g-1a2b3c4z", false, "group"},
		{"g-1a2b3c4d.json", false, "group"},
	}
	for _, c := range cases {
		var got bool
		switch c.kind {
		case "group":
			got = ValidGroupID(c.id)
		case "task":
			got = ValidTaskID(c.id) || validAssigneeID(c.id)
		}
		if got != c.ok {
			t.Errorf("valid(%q, %s) = %v, want %v", c.id, c.kind, got, c.ok)
		}
	}
}

func newGroupForTest(t *testing.T, statuses ...TaskStatus) *Group {
	t.Helper()
	now := time.Now()
	g := NewGroup(now, "c-1a2b3c4d", 0, "p-1a2b3c4d", "gpt-4o-mini")
	for i, status := range statuses {
		task := NewTask(now, "目标"+strings.Repeat("x", i), "", "", "a-1a2b3c4d", "资料整理")
		task.Status = status
		g.Tasks = append(g.Tasks, task)
	}
	return g
}

func TestGroupValidate(t *testing.T) {
	cases := []struct {
		name    string
		edit    func(g *Group)
		wantErr bool
	}{
		{"valid", func(g *Group) {}, false},
		{"schema too new", func(g *Group) { g.Schema = SchemaVersion + 1 }, true},
		{"schema missing", func(g *Group) { g.Schema = 0 }, true},
		{"schema 1 is readable", func(g *Group) { g.Schema = 1 }, false},
		{"no tasks", func(g *Group) { g.Tasks = nil }, true},
		{"too many tasks", func(g *Group) {
			for i := 0; i < MaxTasksPerGroup; i++ {
				g.Tasks = append(g.Tasks, NewTask(time.Now(), "目标", "", "", "a-1a2b3c4d", "资料整理"))
			}
		}, true},
		{"empty goal", func(g *Group) { g.Tasks[0].Goal = " " }, true},
		{"long goal", func(g *Group) { g.Tasks[0].Goal = strings.Repeat("目", MaxGoalRunes+1) }, true},
		{"long input", func(g *Group) { g.Tasks[0].Input = strings.Repeat("输", MaxInputRunes+1) }, true},
		{"bad assignee", func(g *Group) { g.Tasks[0].AssigneeID = "a-xxxx" }, true},
		{"bad status", func(g *Group) { g.Tasks[0].Status = "weird" }, true},
		{"bad conversation", func(g *Group) { g.ConversationID = "" }, true},
		{"bad model", func(g *Group) { g.Model = "" }, true},
		{"duplicate task", func(g *Group) { g.Tasks = append(g.Tasks, g.Tasks[0]) }, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := newGroupForTest(t, TaskPending, TaskPending)
			c.edit(g)
			err := g.Validate()
			if (err != nil) != c.wantErr {
				t.Fatalf("Validate() = %v, wantErr %v", err, c.wantErr)
			}
		})
	}
}

func TestDerivedStatus(t *testing.T) {
	cases := []struct {
		name     string
		group    GroupStatus
		statuses []TaskStatus
		want     GroupStatus
	}{
		{"pending group", GroupPending, []TaskStatus{TaskPending, TaskPending}, GroupPending},
		{"running task", GroupRunning, []TaskStatus{TaskDone, TaskRunning}, GroupRunning},
		{"all done", GroupRunning, []TaskStatus{TaskDone, TaskDone}, GroupDone},
		{"one failed", GroupRunning, []TaskStatus{TaskDone, TaskFailed}, GroupDone},
		{"all failed", GroupRunning, []TaskStatus{TaskFailed, TaskFailed}, GroupFailed},
		{"all canceled", GroupRunning, []TaskStatus{TaskCanceled, TaskCanceled}, GroupCanceled},
		{"all interrupted", GroupRunning, []TaskStatus{TaskInterrupted}, GroupInterrupted},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := newGroupForTest(t, c.statuses...)
			g.Status = c.group
			if got := g.DerivedStatus(); got != c.want {
				t.Errorf("DerivedStatus() = %s, want %s", got, c.want)
			}
		})
	}
}

func TestMarkInterrupted(t *testing.T) {
	g := newGroupForTest(t, TaskDone, TaskRunning, TaskPending)
	g.Status = GroupSummarizing
	g.MarkInterrupted(time.Unix(1790000100, 0))

	if g.Status != GroupInterrupted {
		t.Errorf("group status = %s, want %s", g.Status, GroupInterrupted)
	}
	if g.Tasks[0].Status != TaskDone {
		t.Errorf("done task changed to %s", g.Tasks[0].Status)
	}
	if g.Tasks[1].Status != TaskInterrupted || g.Tasks[2].Status != TaskInterrupted {
		t.Errorf("in-flight tasks = %s, %s", g.Tasks[1].Status, g.Tasks[2].Status)
	}
	if g.UpdatedAt != 1790000100 {
		t.Errorf("updated at = %d", g.UpdatedAt)
	}

	pending := newGroupForTest(t, TaskPending)
	pending.MarkInterrupted(time.Unix(1790000100, 0))
	if pending.Status != GroupPending {
		t.Errorf("pending group changed to %s", pending.Status)
	}
}

func TestCancelPending(t *testing.T) {
	g := newGroupForTest(t, TaskRunning, TaskPending, TaskDone)
	g.CancelPending(time.Unix(1790000100, 0))
	if g.Tasks[0].Status != TaskRunning {
		t.Errorf("running task changed to %s", g.Tasks[0].Status)
	}
	if g.Tasks[1].Status != TaskCanceled {
		t.Errorf("pending task = %s, want canceled", g.Tasks[1].Status)
	}
	if g.Tasks[2].Status != TaskDone {
		t.Errorf("done task changed to %s", g.Tasks[2].Status)
	}
}

func TestResume(t *testing.T) {
	g := newGroupForTest(t, TaskDone, TaskInterrupted, TaskPending, TaskFailed)
	g.Status = GroupInterrupted
	g.Summary = "旧汇总"
	g.Tasks[1].ErrorCode = "timeout"
	g.Tasks[1].ErrorMessage = "请求超时"

	if !g.Resume(time.Unix(1790000200, 0)) {
		t.Fatal("Resume() = false")
	}
	if g.Status != GroupRunning {
		t.Errorf("group status = %s, want %s", g.Status, GroupRunning)
	}
	if g.Summary != "" {
		t.Errorf("summary = %q, want empty", g.Summary)
	}
	if g.Tasks[0].Status != TaskDone || g.Tasks[3].Status != TaskFailed {
		t.Errorf("settled tasks changed: %s, %s", g.Tasks[0].Status, g.Tasks[3].Status)
	}
	for _, i := range []int{1, 2} {
		if g.Tasks[i].Status != TaskPending {
			t.Errorf("task %d = %s, want pending", i, g.Tasks[i].Status)
		}
	}
	if g.Tasks[1].ErrorCode != "" || g.Tasks[1].ErrorMessage != "" {
		t.Errorf("task error kept: %q %q", g.Tasks[1].ErrorCode, g.Tasks[1].ErrorMessage)
	}
	if g.Resume(time.Unix(1790000200, 0)) {
		t.Fatal("running group resumed again")
	}
}

func TestTerminate(t *testing.T) {
	g := newGroupForTest(t, TaskDone, TaskInterrupted, TaskPending)
	g.Status = GroupInterrupted

	if !g.Terminate(time.Unix(1790000300, 0)) {
		t.Fatal("Terminate() = false")
	}
	if g.Status != GroupCanceled {
		t.Errorf("group status = %s, want %s", g.Status, GroupCanceled)
	}
	for _, i := range []int{1, 2} {
		if g.Tasks[i].Status != TaskCanceled {
			t.Errorf("task %d = %s, want canceled", i, g.Tasks[i].Status)
		}
	}
	if g.Tasks[0].Status != TaskDone {
		t.Errorf("done task changed to %s", g.Tasks[0].Status)
	}
	if g.UpdatedAt != 1790000300 {
		t.Errorf("updated at = %d", g.UpdatedAt)
	}

	if g.Terminate(time.Unix(1790000300, 0)) {
		t.Error("terminate twice")
	}
}

func TestCloneIsDeep(t *testing.T) {
	g := newGroupForTest(t, TaskDone)
	g.Tasks[0].Report = &Report{
		Conclusion: "结论",
		Artifacts:  []string{"清单"},
		OpenIssues: []string{"缺少负责人"},
	}
	copy := g.Clone()
	copy.Tasks[0].Goal = "改了"
	copy.Tasks[0].Report.Artifacts[0] = "改了"
	copy.Tasks[0].Report.OpenIssues[0] = "改了"

	if g.Tasks[0].Goal == "改了" {
		t.Error("goal shares memory")
	}
	if g.Tasks[0].Report.Artifacts[0] != "清单" || g.Tasks[0].Report.OpenIssues[0] != "缺少负责人" {
		t.Error("report shares memory")
	}
}
