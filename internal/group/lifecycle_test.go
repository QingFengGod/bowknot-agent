package group

import (
	"testing"
	"time"
)

func TestRunSetPlanAndConfirm(t *testing.T) {
	now := testTime()
	run := NewRun(now, "gp-00000001", "资料组", "", "整理资料")
	plan := Plan{ProviderID: "p-00000001", Model: "m", Tasks: []Task{
		testTask("t-00000001"),
		testTask("t-00000002", "t-00000001"),
	}}
	if err := run.SetPlan(now, plan); err != nil {
		t.Fatalf("SetPlan() error: %v", err)
	}
	if run.Status != RunPendingConfirm {
		t.Fatalf("状态 = %s, want pending_confirm", run.Status)
	}
	if run.Plan.Tasks[0].Status != TaskWaitingDependency {
		t.Errorf("新任务状态 = %s, want waiting_dependency", run.Plan.Tasks[0].Status)
	}
	if !run.Confirm(now) || run.Status != RunRunning {
		t.Errorf("Confirm() 后状态 = %s, want running", run.Status)
	}
	if run.Confirm(now) {
		t.Error("重复 Confirm 应返回 false")
	}
}

func TestRunSetPlanRejectsInvalid(t *testing.T) {
	now := testTime()
	run := NewRun(now, "gp-00000001", "资料组", "", "整理资料")
	plan := Plan{ProviderID: "p", Model: "m", Tasks: []Task{
		testTask("t-00000001", "t-00000009"),
	}}
	if err := run.SetPlan(now, plan); err == nil {
		t.Fatal("缺失依赖的计划被接受")
	}
	if run.Status != RunPlanning {
		t.Errorf("非法计划改变了状态: %s", run.Status)
	}
}

func TestAbandon(t *testing.T) {
	now := testTime()
	run := testRun(t, testTask("t-00000001"))
	if !run.Abandon(now) {
		t.Fatal("Abandon() = false")
	}
	if run.Status != RunCanceled {
		t.Errorf("状态 = %s, want canceled", run.Status)
	}
	if run.Plan.Tasks[0].Status != TaskCanceled || run.Plan.Tasks[0].ErrorCode != "abandoned" {
		t.Errorf("任务 = %+v", run.Plan.Tasks[0])
	}
}

func TestCancelRunningRun(t *testing.T) {
	now := testTime()
	run := testRun(t, testTask("t-00000001"), testTask("t-00000002"), testTask("t-00000003"))
	run.Status = RunRunning
	run.Plan.Tasks[0].Status = TaskDone
	run.Plan.Tasks[0].Report = &Report{Conclusion: "已产出"}
	run.Plan.Tasks[1].Status = TaskRunning
	run.Plan.Tasks[2].Status = TaskWaitingDependency
	if !run.Cancel(now) {
		t.Fatal("Cancel() = false")
	}
	if run.Status != RunCanceled {
		t.Errorf("状态 = %s, want canceled", run.Status)
	}
	if run.Plan.Tasks[0].Status != TaskDone || run.Plan.Tasks[0].Report == nil {
		t.Errorf("已完成任务被改动: %+v", run.Plan.Tasks[0])
	}
	for i := 1; i < len(run.Plan.Tasks); i++ {
		if run.Plan.Tasks[i].Status != TaskCanceled || run.Plan.Tasks[i].ErrorCode != "canceled" {
			t.Errorf("任务 %d = %+v", i, run.Plan.Tasks[i])
		}
	}
}

func TestMarkInterruptedResumeAndTerminate(t *testing.T) {
	now := testTime()
	run := testRun(t, testTask("t-00000001"), testTask("t-00000002"), testTask("t-00000003"))
	run.Status = RunRunning
	run.Plan.Tasks[0].Status = TaskDone
	run.Plan.Tasks[1].Status = TaskRunning
	run.Plan.Tasks[2].Status = TaskWaitingDependency

	if !run.MarkInterrupted(now) {
		t.Fatal("MarkInterrupted() = false")
	}
	if run.Status != RunInterrupted {
		t.Fatalf("状态 = %s, want interrupted", run.Status)
	}
	if run.Plan.Tasks[0].Status != TaskDone {
		t.Errorf("已完成任务被改动: %s", run.Plan.Tasks[0].Status)
	}
	for i := 1; i < len(run.Plan.Tasks); i++ {
		if run.Plan.Tasks[i].Status != TaskInterrupted {
			t.Errorf("任务 %d = %s, want interrupted", i, run.Plan.Tasks[i].Status)
		}
	}
	if run.MarkInterrupted(now) {
		t.Error("重复 MarkInterrupted 应返回 false")
	}

	if !run.Resume(now) {
		t.Fatal("Resume() = false")
	}
	if run.Status != RunRunning {
		t.Errorf("恢复后状态 = %s, want running", run.Status)
	}
	for i := 1; i < len(run.Plan.Tasks); i++ {
		if run.Plan.Tasks[i].Status != TaskWaitingDependency {
			t.Errorf("恢复后任务 %d = %s, want waiting_dependency", i, run.Plan.Tasks[i].Status)
		}
	}

	if !run.MarkInterrupted(now) || !run.Terminate(now) {
		t.Fatal("Terminate() = false")
	}
	if run.Status != RunCanceled {
		t.Errorf("终止后状态 = %s, want canceled", run.Status)
	}
	for i := 1; i < len(run.Plan.Tasks); i++ {
		if run.Plan.Tasks[i].Status != TaskCanceled {
			t.Errorf("终止后任务 %d = %s, want canceled", i, run.Plan.Tasks[i].Status)
		}
	}
}

func TestFail(t *testing.T) {
	now := testTime()
	run := testRun(t, testTask("t-00000001"), testTask("t-00000002"))
	run.Status = RunRunning
	run.Plan.Tasks[0].Status = TaskDone
	if !run.Fail(now, "model_error", "模型不可用") {
		t.Fatal("Fail() = false")
	}
	if run.Status != RunFailed || run.ErrorCode != "model_error" {
		t.Errorf("Run = %s/%s", run.Status, run.ErrorCode)
	}
	if run.Plan.Tasks[0].Status != TaskDone {
		t.Errorf("已完成任务被改动: %s", run.Plan.Tasks[0].Status)
	}
	if run.Plan.Tasks[1].Status != TaskFailed {
		t.Errorf("未完成任务 = %s, want failed", run.Plan.Tasks[1].Status)
	}
}

func TestSummarizeAndFinish(t *testing.T) {
	now := testTime()
	run := testRun(t, testTask("t-00000001"))
	run.Status = RunRunning
	run.Plan.Tasks[0].Status = TaskDone
	if !run.BeginSummary(now) || run.Status != RunSummarizing {
		t.Fatalf("BeginSummary() 后状态 = %s", run.Status)
	}
	if !run.Finish(now, "全部完成") || run.Status != RunDone {
		t.Fatalf("Finish() 后状态 = %s", run.Status)
	}
	if run.Summary != "全部完成" {
		t.Errorf("汇总 = %q", run.Summary)
	}
}

func TestBeginSummaryRequiresFinishedTasks(t *testing.T) {
	now := testTime()
	run := testRun(t, testTask("t-00000001"))
	run.Status = RunRunning
	if run.BeginSummary(now) {
		t.Error("任务未结束时应拒绝进入汇总")
	}
}

func TestTerminalRunDoesNotChange(t *testing.T) {
	now := testTime()
	run := testRun(t, testTask("t-00000001"))
	run.Status = RunDone
	run.Plan.Tasks[0].Status = TaskDone
	if run.Cancel(now) || run.Abandon(now) || run.Fail(now, "x", "x") ||
		run.MarkInterrupted(now) || run.Resume(now) || run.Terminate(now) {
		t.Error("终态 Run 不应再改变")
	}
	if run.Status != RunDone {
		t.Errorf("终态被改为 %s", run.Status)
	}
}

func TestLifecycleTimestamps(t *testing.T) {
	now := testTime()
	run := testRun(t, testTask("t-00000001"))
	before := run.UpdatedAt
	run.Confirm(now.Add(time.Minute))
	if run.UpdatedAt != now.Add(time.Minute).Unix() {
		t.Errorf("Confirm 未更新时间戳: %d, want %d", run.UpdatedAt, now.Add(time.Minute).Unix())
	}
	if run.UpdatedAt == before {
		t.Error("更新时间未变化")
	}
}
