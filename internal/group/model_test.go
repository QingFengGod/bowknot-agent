package group

import (
	"strings"
	"testing"
	"time"
)

func testTime() time.Time { return time.Unix(1700000000, 0) }

func testGroup() *Group {
	g := NewGroup(testTime(), "资料组", []string{"a-00000001", "a-00000002"})
	g.ID = "gp-00000001"
	return g
}

func testTask(id string, dependsOn ...string) Task {
	t := NewTask(testTime(), "目标 "+id, "", "", "a-00000001", "资料整理", dependsOn)
	t.ID = id
	return t
}

func testRun(t *testing.T, tasks ...Task) *Run {
	t.Helper()
	run := NewRun(testTime(), "gp-00000001", "资料组", "", "整理资料")
	if err := run.SetPlan(testTime(), Plan{ProviderID: "p-00000001", Model: "gpt-4o-mini", Tasks: tasks}); err != nil {
		t.Fatalf("SetPlan() error: %v", err)
	}
	return run
}

func TestNewIDs(t *testing.T) {
	if id := NewGroupID(); !ValidGroupID(id) {
		t.Errorf("group ID %q 非法", id)
	}
	if id := NewRunID(); !ValidRunID(id) {
		t.Errorf("run ID %q 非法", id)
	}
	if id := NewTaskID(); !ValidTaskID(id) {
		t.Errorf("task ID %q 非法", id)
	}
	if NewGroupID() == NewGroupID() {
		t.Error("group ID 重复")
	}
}

func TestValidIDs(t *testing.T) {
	cases := []struct {
		id   string
		kind string
		ok   bool
	}{
		{"gp-1a2b3c4d", "group", true},
		{"gp-1A2B3C4D", "group", false},
		{"gp-1a2b3c4", "group", false},
		{"g-1a2b3c4d", "group", false},
		{"r-1a2b3c4d", "run", true},
		{"r-1a2b3c4d.json", "run", false},
		{"t-1a2b3c4d", "task", true},
		{"a-1a2b3c4d", "agent", true},
		{"main", "agent", false},
	}
	for _, c := range cases {
		var got bool
		switch c.kind {
		case "group":
			got = ValidGroupID(c.id)
		case "run":
			got = ValidRunID(c.id)
		case "task":
			got = ValidTaskID(c.id)
		case "agent":
			got = ValidSubAgentID(c.id)
		}
		if got != c.ok {
			t.Errorf("valid(%q, %s) = %v, want %v", c.id, c.kind, got, c.ok)
		}
	}
}

func TestStatusTextAndTerminal(t *testing.T) {
	runCases := []struct {
		status   RunStatus
		text     string
		terminal bool
	}{
		{RunPlanning, "规划中", false},
		{RunPendingConfirm, "待确认", false},
		{RunRunning, "执行中", false},
		{RunSummarizing, "汇总中", false},
		{RunDone, "已完成", true},
		{RunFailed, "已失败", true},
		{RunCanceled, "已取消", true},
		{RunInterrupted, "已中断", false},
	}
	for _, c := range runCases {
		if got := c.status.Text(); got != c.text {
			t.Errorf("RunStatus(%s).Text() = %q, want %q", c.status, got, c.text)
		}
		if got := c.status.Terminal(); got != c.terminal {
			t.Errorf("RunStatus(%s).Terminal() = %v, want %v", c.status, got, c.terminal)
		}
	}
	taskCases := []struct {
		status   TaskStatus
		text     string
		terminal bool
		finished bool
	}{
		{TaskWaitingDependency, "等待依赖", false, false},
		{TaskPending, "待开始", false, false},
		{TaskRunning, "执行中", false, false},
		{TaskWaitingReply, "等待回复", false, false},
		{TaskWaitingApproval, "等待审批", false, false},
		{TaskPaused, "已暂停", false, false},
		{TaskDone, "已完成", true, true},
		{TaskFailed, "已失败", false, true},
		{TaskCanceled, "已取消", true, true},
		{TaskInterrupted, "已中断", false, false},
	}
	for _, c := range taskCases {
		if got := c.status.Text(); got != c.text {
			t.Errorf("TaskStatus(%s).Text() = %q, want %q", c.status, got, c.text)
		}
		if got := c.status.Terminal(); got != c.terminal {
			t.Errorf("TaskStatus(%s).Terminal() = %v, want %v", c.status, got, c.terminal)
		}
		if got := c.status.Finished(); got != c.finished {
			t.Errorf("TaskStatus(%s).Finished() = %v, want %v", c.status, got, c.finished)
		}
	}
}

func TestBudget(t *testing.T) {
	b := DefaultBudget()
	if err := b.Validate(); err != nil {
		t.Fatalf("DefaultBudget().Validate() error: %v", err)
	}
	if b.Concurrency() != 3 || b.WaitTimeout() != 120*time.Second {
		t.Errorf("default budget = %+v", b)
	}
	cases := []struct {
		name string
		edit func(*Budget)
		ok   bool
	}{
		{"ok", func(*Budget) {}, true},
		{"tasks high", func(b *Budget) { b.MaxTasks = MaxTasksPerRun + 1 }, false},
		{"tasks zero", func(b *Budget) { b.MaxTasks = 0 }, false},
		{"concurrency high", func(b *Budget) { b.MaxConcurrency = MaxConcurrency + 1 }, false},
		{"messages high", func(b *Budget) { b.MaxMessages = DefaultMaxMessages + 1 }, false},
		{"steps high", func(b *Budget) { b.MaxTaskSteps = DefaultMaxTaskSteps + 1 }, false},
		{"wait zero", func(b *Budget) { b.WaitTimeoutSeconds = 0 }, false},
		{"token negative", func(b *Budget) { b.TokenBudget = -1 }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := DefaultBudget()
			c.edit(&b)
			if (b.Validate() == nil) != c.ok {
				t.Errorf("Validate() = %v, want ok %v", b.Validate(), c.ok)
			}
		})
	}
}

func TestGroupValidate(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Group)
		ok   bool
	}{
		{"valid", func(*Group) {}, true},
		{"schema too new", func(g *Group) { g.Schema = SchemaVersion + 1 }, false},
		{"schema missing", func(g *Group) { g.Schema = 0 }, false},
		{"bad id", func(g *Group) { g.ID = "g-00000001" }, false},
		{"empty name", func(g *Group) { g.Name = " " }, false},
		{"long name", func(g *Group) { g.Name = strings.Repeat("群", MaxGroupNameRunes+1) }, false},
		{"bad owner", func(g *Group) { g.OwnerID = "a-00000001" }, false},
		{"too many members", func(g *Group) {
			for i := 0; i < MaxGroupMembers; i++ {
				g.Members = append(g.Members, "a-1000000"+string(rune('0'+i%10)))
			}
		}, false},
		{"main as member", func(g *Group) { g.Members = append(g.Members, MainAgentID) }, false},
		{"duplicate member", func(g *Group) { g.Members = append(g.Members, g.Members[0]) }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := testGroup()
			c.edit(g)
			if (g.Validate() == nil) != c.ok {
				t.Errorf("Validate() = %v, want ok %v", g.Validate(), c.ok)
			}
		})
	}
}

func TestRunValidate(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Run)
		ok   bool
	}{
		{"valid", func(*Run) {}, true},
		{"schema too new", func(r *Run) { r.Schema = SchemaVersion + 1 }, false},
		{"bad run id", func(r *Run) { r.ID = "t-00000001" }, false},
		{"bad group id", func(r *Run) { r.GroupID = "gp-0000001" }, false},
		{"no owner", func(r *Run) { r.GroupID = "" }, false},
		{"empty goal", func(r *Run) { r.Goal = " " }, false},
		{"long goal", func(r *Run) { r.Goal = strings.Repeat("目", MaxRunGoalRunes+1) }, false},
		{"bad status", func(r *Run) { r.Status = "weird" }, false},
		{"bad budget", func(r *Run) { r.Budget.MaxTasks = 0 }, false},
		{"long summary", func(r *Run) { r.Summary = strings.Repeat("汇", MaxSummaryRunes+1) }, false},
		{"no plan while pending", func(r *Run) { r.Plan.Tasks = nil }, false},
		{"bad task", func(r *Run) { r.Plan.Tasks[0].Goal = " " }, false},
		{"missing dependency", func(r *Run) { r.Plan.Tasks[1].DependsOn = []string{"t-00000009"} }, false},
		{"duplicate task", func(r *Run) { r.Plan.Tasks[1].ID = r.Plan.Tasks[0].ID }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := testRun(t, testTask("t-00000001"), testTask("t-00000002", "t-00000001"))
			c.edit(r)
			if (r.Validate() == nil) != c.ok {
				t.Errorf("Validate() = %v, want ok %v", r.Validate(), c.ok)
			}
		})
	}
}

func TestPlanningRunValidatesWithoutPlan(t *testing.T) {
	r := NewRun(testTime(), "gp-00000001", "资料组", "", "整理资料")
	if err := r.Validate(); err != nil {
		t.Fatalf("规划中的 Run 不应要求计划: %v", err)
	}
	r.Status = RunPendingConfirm
	if r.Validate() == nil {
		t.Fatal("待确认的 Run 缺失计划应校验失败")
	}
}

func TestDerivedStatus(t *testing.T) {
	cases := []struct {
		name     string
		status   RunStatus
		statuses []TaskStatus
		want     RunStatus
	}{
		{"planning", RunPlanning, []TaskStatus{TaskWaitingDependency}, RunPlanning},
		{"pending confirm", RunPendingConfirm, []TaskStatus{TaskWaitingDependency}, RunPendingConfirm},
		{"active", RunRunning, []TaskStatus{TaskDone, TaskRunning}, RunRunning},
		{"all done", RunRunning, []TaskStatus{TaskDone, TaskDone}, RunDone},
		{"done with failure", RunRunning, []TaskStatus{TaskDone, TaskFailed}, RunDone},
		{"all failed", RunRunning, []TaskStatus{TaskFailed, TaskFailed}, RunFailed},
		{"all canceled", RunRunning, []TaskStatus{TaskCanceled, TaskCanceled}, RunCanceled},
		{"all interrupted", RunRunning, []TaskStatus{TaskInterrupted, TaskInterrupted}, RunInterrupted},
		{"done and interrupted", RunRunning, []TaskStatus{TaskDone, TaskInterrupted}, RunInterrupted},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			run := testRun(t, testTask("t-00000001"), testTask("t-00000002"))
			run.Status = c.status
			for i, status := range c.statuses {
				run.Plan.Tasks[i].Status = status
			}
			if got := run.DerivedStatus(); got != c.want {
				t.Errorf("DerivedStatus() = %s, want %s", got, c.want)
			}
		})
	}
}

func TestCloneIsDeep(t *testing.T) {
	run := testRun(t, testTask("t-00000001"))
	run.Plan.Tasks[0].Report = &Report{Conclusion: "结论", Artifacts: []string{"清单"}, OpenIssues: []string{"遗留"}}
	run.Plan.Tasks[0].DependsOn = []string{"t-00000002"}
	copy := run.Clone()
	copy.Plan.Tasks[0].Report.Artifacts[0] = "改后"
	copy.Plan.Tasks[0].DependsOn[0] = "t-00000003"
	if run.Plan.Tasks[0].Report.Artifacts[0] != "清单" {
		t.Error("Clone() 共享了汇报产物切片")
	}
	if run.Plan.Tasks[0].DependsOn[0] != "t-00000002" {
		t.Error("Clone() 共享了依赖切片")
	}
}
