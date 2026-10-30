package group

import (
	"errors"
	"time"
)

// SetPlan 给规划中的 Run 写入计划并进入待确认。计划结构不合法时保持规划中。
func (r *Run) SetPlan(now time.Time, plan Plan) error {
	if r.Status != RunPlanning {
		return errors.New("只有规划中的 Run 可以设置计划")
	}
	if err := ValidatePlan(plan, r.Budget); err != nil {
		return err
	}
	plan.Tasks = cloneTasks(plan.Tasks)
	r.Plan = plan
	r.Status = RunPendingConfirm
	r.UpdatedAt = now.Unix()
	return nil
}

// Confirm 把待确认的 Run 转为执行中。用户确认是执行计划的前提。
func (r *Run) Confirm(now time.Time) bool {
	if r.Status != RunPendingConfirm {
		return false
	}
	r.Status = RunRunning
	r.UpdatedAt = now.Unix()
	return true
}

// Abandon 放弃待确认或规划中的 Run，未结束的任务标记为已取消。
func (r *Run) Abandon(now time.Time) bool {
	switch r.Status {
	case RunPlanning, RunPendingConfirm:
	default:
		return false
	}
	r.closeTasks(now, TaskCanceled, "abandoned", "用户放弃 Run")
	r.Status = RunCanceled
	r.UpdatedAt = now.Unix()
	return true
}

// Cancel 取消未到终态的 Run，未结束的任务标记为已取消。已中断的 Run 由
// Terminate 处理，已终态的 Run 不再变化。
func (r *Run) Cancel(now time.Time) bool {
	switch r.Status {
	case RunPlanning, RunPendingConfirm, RunRunning, RunSummarizing:
	default:
		return false
	}
	r.closeTasks(now, TaskCanceled, "canceled", "Run 已取消")
	r.Status = RunCanceled
	r.UpdatedAt = now.Unix()
	return true
}

// MarkInterrupted 把崩溃遗留的执行中 Run 标记为已中断，未结束的任务同样标记
// 为已中断，等待用户决定继续或终止（FR-AGT-07）。
func (r *Run) MarkInterrupted(now time.Time) bool {
	if r.Status != RunRunning && r.Status != RunSummarizing {
		return false
	}
	at := now.Unix()
	for i := range r.Plan.Tasks {
		if !r.Plan.Tasks[i].Status.Finished() {
			r.Plan.Tasks[i].Status = TaskInterrupted
			r.Plan.Tasks[i].UpdatedAt = at
		}
	}
	r.Status = RunInterrupted
	r.UpdatedAt = at
	return true
}

// Resume 继续已中断的 Run：已中断的任务回到等待依赖，由调度器重新计算就绪。
func (r *Run) Resume(now time.Time) bool {
	if r.Status != RunInterrupted {
		return false
	}
	at := now.Unix()
	for i := range r.Plan.Tasks {
		if r.Plan.Tasks[i].Status == TaskInterrupted {
			r.Plan.Tasks[i].Status = TaskWaitingDependency
			r.Plan.Tasks[i].ErrorCode = ""
			r.Plan.Tasks[i].ErrorMessage = ""
			r.Plan.Tasks[i].UpdatedAt = at
		}
	}
	r.Status = RunRunning
	r.Summary = ""
	r.ErrorCode = ""
	r.ErrorMessage = ""
	r.UpdatedAt = at
	return true
}

// Terminate 终止已中断的 Run，未结束的任务标记为已取消，已有产出保留。
func (r *Run) Terminate(now time.Time) bool {
	if r.Status != RunInterrupted {
		return false
	}
	r.closeTasks(now, TaskCanceled, "terminated", "用户终止了已中断的 Run")
	r.Status = RunCanceled
	r.UpdatedAt = now.Unix()
	return true
}

// Fail 把规划中、执行中或汇总中的 Run 标记为已失败，未结束的任务一并失败。
func (r *Run) Fail(now time.Time, code, message string) bool {
	switch r.Status {
	case RunPlanning, RunRunning, RunSummarizing:
	default:
		return false
	}
	r.closeTasks(now, TaskFailed, code, message)
	r.Status = RunFailed
	r.ErrorCode = code
	r.ErrorMessage = message
	r.UpdatedAt = now.Unix()
	return true
}

// BeginSummary 把执行中且任务全部结束的 Run 转为汇总中，由主 Agent 汇总。
func (r *Run) BeginSummary(now time.Time) bool {
	if r.Status != RunRunning || !r.AllFinished() {
		return false
	}
	r.Status = RunSummarizing
	r.UpdatedAt = now.Unix()
	return true
}

// Finish 汇总完成后把 Run 置为已完成。
func (r *Run) Finish(now time.Time, summary string) bool {
	if r.Status != RunSummarizing {
		return false
	}
	r.Summary = summary
	r.Status = RunDone
	r.UpdatedAt = now.Unix()
	return true
}

// closeTasks 把未结束（含已中断）的任务统一改为 status，并保留已有产出。
func (r *Run) closeTasks(now time.Time, status TaskStatus, code, message string) {
	at := now.Unix()
	for i := range r.Plan.Tasks {
		t := &r.Plan.Tasks[i]
		if t.Status.Finished() {
			continue
		}
		t.Status = status
		if status == TaskFailed || status == TaskCanceled {
			t.ErrorCode = code
			t.ErrorMessage = message
		}
		t.UpdatedAt = at
	}
}

func cloneTasks(tasks []Task) []Task {
	out := make([]Task, len(tasks))
	for i := range tasks {
		out[i] = tasks[i].Clone()
	}
	return out
}
