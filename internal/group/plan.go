package group

import (
	"errors"
	"fmt"
	"sort"
)

// ValidatePlan 检查计划的执行者字段之外的完整结构：模型、任务数量、任务字段、
// 任务 ID 唯一、依赖存在且不成环。执行者是否可用由 ValidateAssignees 检查。
func ValidatePlan(plan Plan, budget Budget) error {
	if plan.ProviderID == "" || plan.Model == "" {
		return errors.New("规划模型为空")
	}
	limit := budget.MaxTasks
	if limit <= 0 || limit > MaxTasksPerRun {
		limit = MaxTasksPerRun
	}
	if len(plan.Tasks) == 0 || len(plan.Tasks) > limit {
		return fmt.Errorf("任务 %d 条，超出 1–%d", len(plan.Tasks), limit)
	}
	known := make(map[string]bool, len(plan.Tasks))
	for i := range plan.Tasks {
		t := plan.Tasks[i]
		if err := t.Validate(); err != nil {
			return fmt.Errorf("任务 %d：%w", i+1, err)
		}
		if known[t.ID] {
			return fmt.Errorf("任务 ID %s 重复", t.ID)
		}
		known[t.ID] = true
	}
	for i := range plan.Tasks {
		t := plan.Tasks[i]
		seen := map[string]bool{}
		for _, dep := range t.DependsOn {
			if dep == t.ID {
				return fmt.Errorf("任务 %s 依赖自身", t.ID)
			}
			if !known[dep] {
				return fmt.Errorf("任务 %s 依赖的任务 %s 不存在", t.ID, dep)
			}
			if seen[dep] {
				return fmt.Errorf("任务 %s 重复依赖 %s", t.ID, dep)
			}
			seen[dep] = true
		}
	}
	if cycle := detectCycle(plan.Tasks); len(cycle) > 0 {
		return fmt.Errorf("任务依赖存在环：%v", cycle)
	}
	return nil
}

// ValidateAssignees 检查每个任务的执行者是否在可用集合中。available 通常为
// 已启用的子 Agent，主 Agent 不作为任务执行者。
func ValidateAssignees(tasks []Task, available map[string]bool) error {
	for i := range tasks {
		id := tasks[i].AssigneeID
		if !available[id] {
			return fmt.Errorf("任务 %s 的执行者 %s 不可用", tasks[i].ID, id)
		}
	}
	return nil
}

// detectCycle 用 Kahn 算法找出参与环的任务 ID；无环时返回空。
func detectCycle(tasks []Task) []string {
	indegree := make(map[string]int, len(tasks))
	next := make(map[string][]string, len(tasks))
	for i := range tasks {
		indegree[tasks[i].ID] += 0
	}
	for i := range tasks {
		for _, dep := range tasks[i].DependsOn {
			next[dep] = append(next[dep], tasks[i].ID)
			indegree[tasks[i].ID]++
		}
	}
	queue := make([]string, 0, len(tasks))
	for id, n := range indegree {
		if n == 0 {
			queue = append(queue, id)
		}
	}
	sort.Strings(queue)
	processed := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		processed++
		for _, child := range next[id] {
			indegree[child]--
			if indegree[child] == 0 {
				queue = append(queue, child)
			}
		}
	}
	if processed == len(indegree) {
		return nil
	}
	cycle := make([]string, 0, len(indegree)-processed)
	for id, n := range indegree {
		if n > 0 {
			cycle = append(cycle, id)
		}
	}
	sort.Strings(cycle)
	return cycle
}

// ReadyTasks 返回可以开始的任务下标：状态为待开始或等待依赖，且依赖全部完成。
// 依赖失败或取消的任务不在其中，由调度器标记为已取消。
func ReadyTasks(run *Run) []int {
	if run == nil {
		return nil
	}
	byID := make(map[string]*Task, len(run.Plan.Tasks))
	for i := range run.Plan.Tasks {
		byID[run.Plan.Tasks[i].ID] = &run.Plan.Tasks[i]
	}
	ready := make([]int, 0, len(run.Plan.Tasks))
	for i := range run.Plan.Tasks {
		t := &run.Plan.Tasks[i]
		if t.Status != TaskPending && t.Status != TaskWaitingDependency {
			continue
		}
		ok := true
		for _, dep := range t.DependsOn {
			d, found := byID[dep]
			if !found || d.Status != TaskDone {
				ok = false
				break
			}
		}
		if ok {
			ready = append(ready, i)
		}
	}
	return ready
}

// BlockedReason 检查任务是否因依赖失败或取消而无法执行，返回依赖任务的状态。
func BlockedReason(run *Run, id string) (TaskStatus, bool) {
	t, ok := run.Task(id)
	if !ok {
		return "", false
	}
	for _, dep := range t.DependsOn {
		d, found := run.Task(dep)
		if !found {
			return TaskCanceled, true
		}
		if d.Status == TaskFailed || d.Status == TaskCanceled {
			return d.Status, true
		}
	}
	return "", false
}
