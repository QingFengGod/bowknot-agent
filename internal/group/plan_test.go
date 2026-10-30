package group

import (
	"strings"
	"testing"
)

func TestValidatePlan(t *testing.T) {
	cases := []struct {
		name string
		plan func() Plan
		ok   bool
	}{
		{"valid", func() Plan {
			return Plan{ProviderID: "p-00000001", Model: "m", Tasks: []Task{
				testTask("t-00000001"),
				testTask("t-00000002", "t-00000001"),
			}}
		}, true},
		{"no model", func() Plan {
			return Plan{Tasks: []Task{testTask("t-00000001")}}
		}, false},
		{"no tasks", func() Plan {
			return Plan{ProviderID: "p", Model: "m"}
		}, false},
		{"too many tasks", func() Plan {
			plan := Plan{ProviderID: "p", Model: "m"}
			for i := 0; i < MaxTasksPerRun+1; i++ {
				plan.Tasks = append(plan.Tasks, testTask("t-0000000"+string(rune('0'+i))))
			}
			return plan
		}, false},
		{"duplicate id", func() Plan {
			return Plan{ProviderID: "p", Model: "m", Tasks: []Task{
				testTask("t-00000001"),
				testTask("t-00000001"),
			}}
		}, false},
		{"missing dependency", func() Plan {
			return Plan{ProviderID: "p", Model: "m", Tasks: []Task{
				testTask("t-00000001", "t-00000009"),
			}}
		}, false},
		{"self dependency", func() Plan {
			return Plan{ProviderID: "p", Model: "m", Tasks: []Task{
				testTask("t-00000001", "t-00000001"),
			}}
		}, false},
		{"duplicate dependency", func() Plan {
			return Plan{ProviderID: "p", Model: "m", Tasks: []Task{
				testTask("t-00000001"),
				testTask("t-00000002", "t-00000001", "t-00000001"),
			}}
		}, false},
		{"cycle", func() Plan {
			return Plan{ProviderID: "p", Model: "m", Tasks: []Task{
				testTask("t-00000001", "t-00000003"),
				testTask("t-00000002", "t-00000001"),
				testTask("t-00000003", "t-00000002"),
			}}
		}, false},
		{"bad task", func() Plan {
			task := testTask("t-00000001")
			task.Goal = " "
			return Plan{ProviderID: "p", Model: "m", Tasks: []Task{task}}
		}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := ValidatePlan(c.plan(), DefaultBudget()); (err == nil) != c.ok {
				t.Errorf("ValidatePlan() = %v, want ok %v", err, c.ok)
			}
		})
	}
}

func TestValidatePlanHonorsBudgetTaskLimit(t *testing.T) {
	budget := DefaultBudget()
	budget.MaxTasks = 2
	plan := Plan{ProviderID: "p", Model: "m", Tasks: []Task{
		testTask("t-00000001"), testTask("t-00000002"), testTask("t-00000003"),
	}}
	if err := ValidatePlan(plan, budget); err == nil {
		t.Fatal("超出预算任务数应被拒绝")
	}
}

func TestValidateAssignees(t *testing.T) {
	tasks := []Task{testTask("t-00000001"), testTask("t-00000002")}
	available := map[string]bool{"a-00000001": true}
	if err := ValidateAssignees(tasks, available); err != nil {
		t.Fatalf("可用的执行者被拒绝: %v", err)
	}
	tasks[1].AssigneeID = "a-00000002"
	err := ValidateAssignees(tasks, available)
	if err == nil || !strings.Contains(err.Error(), "不可用") {
		t.Fatalf("不可用的执行者未被拒绝: %v", err)
	}
	if err := ValidateAssignees(tasks, map[string]bool{MainAgentID: true}); err == nil {
		t.Fatal("主 Agent 不应被接受为任务执行者")
	}
}

func TestReadyTasks(t *testing.T) {
	run := testRun(t,
		testTask("t-00000001"),
		testTask("t-00000002", "t-00000001"),
		testTask("t-00000003", "t-00000002"),
	)
	run.Plan.Tasks[0].Status = TaskDone
	run.Plan.Tasks[1].Status = TaskWaitingDependency
	run.Plan.Tasks[2].Status = TaskWaitingDependency
	ready := ReadyTasks(run)
	if len(ready) != 1 || ready[0] != 1 {
		t.Fatalf("ReadyTasks() = %v, want [1]", ready)
	}

	run.Plan.Tasks[1].Status = TaskDone
	ready = ReadyTasks(run)
	if len(ready) != 1 || ready[0] != 2 {
		t.Fatalf("ReadyTasks() = %v, want [2]", ready)
	}

	run.Plan.Tasks[2].Status = TaskRunning
	if ready := ReadyTasks(run); len(ready) != 0 {
		t.Fatalf("运行中的任务不应进入就绪集合: %v", ready)
	}
}

func TestBlockedReason(t *testing.T) {
	run := testRun(t, testTask("t-00000001"), testTask("t-00000002", "t-00000001"))
	run.Plan.Tasks[0].Status = TaskFailed
	if status, blocked := BlockedReason(run, "t-00000002"); !blocked || status != TaskFailed {
		t.Errorf("BlockedReason() = %v, %v, want failed, true", status, blocked)
	}
	run.Plan.Tasks[0].Status = TaskCanceled
	if status, blocked := BlockedReason(run, "t-00000002"); !blocked || status != TaskCanceled {
		t.Errorf("BlockedReason() = %v, %v, want canceled, true", status, blocked)
	}
	run.Plan.Tasks[0].Status = TaskDone
	if _, blocked := BlockedReason(run, "t-00000002"); blocked {
		t.Error("依赖完成的任务不应被阻塞")
	}
}

func TestDetectCycle(t *testing.T) {
	if cycle := detectCycle([]Task{testTask("t-00000001"), testTask("t-00000002", "t-00000001")}); len(cycle) != 0 {
		t.Errorf("无环计划返回环: %v", cycle)
	}
	cycle := detectCycle([]Task{
		testTask("t-00000001", "t-00000003"),
		testTask("t-00000002", "t-00000001"),
		testTask("t-00000003", "t-00000002"),
	})
	if len(cycle) != 3 {
		t.Errorf("环任务 = %v, want 3 个", cycle)
	}
}
