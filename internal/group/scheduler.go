package group

import (
	"context"
	"sync"
	"time"
)

// Outcome 是注入执行函数返回的单任务结果。
type Outcome struct {
	Report         *Report
	InputTokens    int
	OutputTokens   int
	UsageEstimated bool
	DurationMS     int64
	ErrorCode      string
	ErrorMessage   string
	// Err 是调用失败；非 nil 或 ErrorCode 非空即标记任务失败。
	Err error
}

// Failed 报告结果是否标记任务失败。
func (o Outcome) Failed() bool { return o.Err != nil || o.ErrorCode != "" }

// ExecFunc 执行一个任务。调度器传入任务副本，上下文在取消时结束。
type ExecFunc func(ctx context.Context, t *Task) Outcome

// NotifyFunc 在每次状态变化后收到 Run 快照。
type NotifyFunc func(Run)

// Scheduler 按依赖图调度一个 Run 的任务，限制并发、等待超时并支持取消。
// 调度器自身不执行模型调用，执行由 ExecFunc 注入。
type Scheduler struct {
	// Concurrency 限制同时执行的任务数；零值使用 DefaultConcurrency。
	Concurrency int
	// WaitTimeout 限制任务等待依赖的时间；零值使用 Run 预算的等待超时。
	WaitTimeout time.Duration
	// Now 替换写入状态的时间戳；等待超时按真实时间计算。
	Now func() time.Time

	mu          sync.Mutex
	cancel      context.CancelFunc
	interrupted bool
}

// NewScheduler 返回使用默认并发上限的调度器。等待超时零值表示使用 Run 预算。
func NewScheduler() *Scheduler {
	return &Scheduler{Concurrency: DefaultConcurrency}
}

// Cancel 停止运行中的 Run，执行中的任务上下文随之结束。
func (s *Scheduler) Cancel() {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Interrupt 停止运行中的 Run 并标记为中断，供进程退出时保留可恢复状态
// （FR-AGT-07）。与 Cancel 不同，未结束的任务标记为已中断而不是已取消。
func (s *Scheduler) Interrupt() {
	s.mu.Lock()
	s.interrupted = true
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *Scheduler) stoppingAsInterrupt() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.interrupted
}

// Run 执行 run 中就绪的任务，直到全部任务结束或上下文结束。上下文结束时
// 未结束的任务标记为已取消；若调用过 Interrupt，则改为已中断的收尾。
func (s *Scheduler) Run(ctx context.Context, run *Run, exec ExecFunc, notify NotifyFunc) {
	if run == nil || exec == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.cancel = nil
		s.mu.Unlock()
		cancel()
	}()

	run.Status = RunRunning
	run.UpdatedAt = s.now().Unix()

	concurrency := s.concurrency()
	wait := s.WaitTimeout
	if wait <= 0 {
		wait = run.Budget.WaitTimeout()
	}
	results := make(chan taskResult, len(run.Plan.Tasks))
	waitSince := map[string]time.Time{}
	running := 0

	for {
		if runCtx.Err() != nil {
			s.finishStopped(run, running, results, notify)
			return
		}
		s.expireWaits(run, waitSince, wait, notify)
		s.prepare(run, waitSince, notify)

		for running < concurrency {
			idx := nextReady(run)
			if idx < 0 {
				break
			}
			t := &run.Plan.Tasks[idx]
			t.Status = TaskRunning
			t.UpdatedAt = s.now().Unix()
			run.UpdatedAt = t.UpdatedAt
			delete(waitSince, t.ID)
			running++
			s.notify(notify, run)
			go execOne(runCtx, idx, *t, exec, results)
		}
		if running == 0 {
			s.settle(run, notify)
			return
		}

		var timer *time.Timer
		var timerC <-chan time.Time
		if d, ok := nextWait(waitSince, wait); ok {
			if d < 0 {
				d = 0
			}
			timer = time.NewTimer(d)
			timerC = timer.C
		}
		select {
		case res := <-results:
			running--
			s.apply(run, res)
			s.notify(notify, run)
		case <-runCtx.Done():
			if timer != nil {
				timer.Stop()
			}
			s.finishStopped(run, running, results, notify)
			return
		case <-timerC:
		}
		if timer != nil {
			timer.Stop()
		}
	}
}

// prepare 把依赖已完成的任务转待开始、依赖失败或取消的任务转已取消，并记录
// 等待开始时间。依赖链上的取消会重复扫描直到稳定。
func (s *Scheduler) prepare(run *Run, waitSince map[string]time.Time, notify NotifyFunc) {
	for {
		changed := false
		for i := range run.Plan.Tasks {
			t := &run.Plan.Tasks[i]
			if t.Status != TaskPending && t.Status != TaskWaitingDependency {
				delete(waitSince, t.ID)
				continue
			}
			if blocked, depStatus := blockedDependency(run, t); blocked {
				t.Status = TaskCanceled
				if depStatus == TaskFailed {
					t.ErrorCode = "dependency_failed"
					t.ErrorMessage = "依赖任务失败，任务已取消"
				} else {
					t.ErrorCode = "dependency_canceled"
					t.ErrorMessage = "依赖任务已取消，任务已取消"
				}
				t.UpdatedAt = s.now().Unix()
				run.UpdatedAt = t.UpdatedAt
				delete(waitSince, t.ID)
				changed = true
				continue
			}
			if depsDone(run, t) {
				if t.Status == TaskWaitingDependency {
					t.Status = TaskPending
					t.UpdatedAt = s.now().Unix()
					run.UpdatedAt = t.UpdatedAt
					changed = true
				}
				delete(waitSince, t.ID)
				continue
			}
			if _, ok := waitSince[t.ID]; !ok {
				waitSince[t.ID] = time.Now()
			}
			if t.Status == TaskPending {
				t.Status = TaskWaitingDependency
				t.UpdatedAt = s.now().Unix()
				run.UpdatedAt = t.UpdatedAt
				changed = true
			}
		}
		if !changed {
			return
		}
		s.notify(notify, run)
	}
}

// expireWaits 把等待依赖超时的任务标记为失败，随后由 prepare 级联取消依赖
// 它的任务。
func (s *Scheduler) expireWaits(run *Run, waitSince map[string]time.Time, wait time.Duration, notify NotifyFunc) {
	now := time.Now()
	changed := false
	for i := range run.Plan.Tasks {
		t := &run.Plan.Tasks[i]
		if t.Status != TaskWaitingDependency {
			continue
		}
		start, ok := waitSince[t.ID]
		if !ok || now.Sub(start) < wait {
			continue
		}
		t.Status = TaskFailed
		t.ErrorCode = "wait_timeout"
		t.ErrorMessage = "等待依赖超时"
		t.UpdatedAt = s.now().Unix()
		run.UpdatedAt = t.UpdatedAt
		delete(waitSince, t.ID)
		changed = true
	}
	if changed {
		s.notify(notify, run)
	}
}

// settle 在没有任务可运行时收尾：仍阻塞的任务标记为失败，Run 状态按任务
// 状态推导。
func (s *Scheduler) settle(run *Run, notify NotifyFunc) {
	now := s.now()
	for i := range run.Plan.Tasks {
		t := &run.Plan.Tasks[i]
		if t.Status == TaskPending || t.Status == TaskWaitingDependency {
			t.Status = TaskFailed
			t.ErrorCode = "dependency_blocked"
			t.ErrorMessage = "依赖无法完成，任务未能执行"
			t.UpdatedAt = now.Unix()
		}
	}
	run.Status = run.DerivedStatus()
	run.UpdatedAt = now.Unix()
	s.notify(notify, run)
}

// finishStopped 标记未结束任务后，等待执行中的任务返回并保留用量。进程退出
// 时按已中断收尾，用户取消时按已取消收尾（FR-AGT-07）。
func (s *Scheduler) finishStopped(run *Run, running int, results <-chan taskResult, notify NotifyFunc) {
	status, taskStatus := RunCanceled, TaskCanceled
	code, message := "canceled", "Run 已取消"
	if s.stoppingAsInterrupt() {
		status, taskStatus = RunInterrupted, TaskInterrupted
		code, message = "interrupted", "应用退出，Run 已中断"
	}
	now := s.now()
	for i := range run.Plan.Tasks {
		t := &run.Plan.Tasks[i]
		if t.Status.Finished() || t.Status == TaskInterrupted {
			continue
		}
		t.Status = taskStatus
		t.ErrorCode = code
		t.ErrorMessage = message
		t.UpdatedAt = now.Unix()
	}
	run.Status = status
	run.UpdatedAt = now.Unix()
	s.notify(notify, run)
	for running > 0 {
		res := <-results
		running--
		t := &run.Plan.Tasks[res.index]
		t.Status = taskStatus
		t.ErrorCode = code
		t.ErrorMessage = message
		t.UpdatedAt = now.Unix()
		s.applyUsage(t, res.out)
	}
	s.recomputeUsage(run)
	s.notify(notify, run)
}

func (s *Scheduler) apply(run *Run, res taskResult) {
	t := &run.Plan.Tasks[res.index]
	t.UpdatedAt = s.now().Unix()
	run.UpdatedAt = t.UpdatedAt
	s.applyUsage(t, res.out)
	switch {
	case res.out.Failed():
		t.Status = TaskFailed
		t.ErrorCode = res.out.ErrorCode
		t.ErrorMessage = res.out.ErrorMessage
		if t.ErrorCode == "" && res.out.Err != nil {
			t.ErrorCode = "unknown"
		}
		if t.ErrorMessage == "" && res.out.Err != nil {
			t.ErrorMessage = res.out.Err.Error()
		}
	default:
		t.Status = TaskDone
		t.Report = res.out.Report
	}
	s.recomputeUsage(run)
}

func (s *Scheduler) applyUsage(t *Task, out Outcome) {
	t.InputTokens = out.InputTokens
	t.OutputTokens = out.OutputTokens
	t.UsageEstimated = out.UsageEstimated
	t.DurationMS = out.DurationMS
}

// recomputeUsage 以任务累计值刷新 Run 用量，避免恢复后重复累计。
func (s *Scheduler) recomputeUsage(run *Run) {
	run.InputTokens, run.OutputTokens, run.UsageEstimated = 0, 0, false
	for i := range run.Plan.Tasks {
		t := &run.Plan.Tasks[i]
		run.InputTokens += t.InputTokens
		run.OutputTokens += t.OutputTokens
		if t.UsageEstimated {
			run.UsageEstimated = true
		}
	}
}

func (s *Scheduler) concurrency() int {
	n := s.Concurrency
	if n <= 0 {
		n = DefaultConcurrency
	}
	return n
}

func (s *Scheduler) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Scheduler) notify(notify NotifyFunc, run *Run) {
	if notify != nil {
		notify(run.Clone())
	}
}

func execOne(parent context.Context, index int, t Task, exec ExecFunc, results chan<- taskResult) {
	started := time.Now()
	out := exec(parent, &t)
	if out.DurationMS <= 0 {
		out.DurationMS = time.Since(started).Milliseconds()
	}
	results <- taskResult{index: index, out: out}
}

// nextReady 返回第一个依赖已完成的待开始任务下标。
func nextReady(run *Run) int {
	for i := range run.Plan.Tasks {
		if run.Plan.Tasks[i].Status == TaskPending && depsDone(run, &run.Plan.Tasks[i]) {
			return i
		}
	}
	return -1
}

func depsDone(run *Run, t *Task) bool {
	for _, dep := range t.DependsOn {
		d, ok := run.Task(dep)
		if !ok || d.Status != TaskDone {
			return false
		}
	}
	return true
}

func blockedDependency(run *Run, t *Task) (bool, TaskStatus) {
	for _, dep := range t.DependsOn {
		d, ok := run.Task(dep)
		if !ok {
			return true, TaskCanceled
		}
		if d.Status == TaskFailed {
			return true, TaskFailed
		}
		if d.Status == TaskCanceled {
			return true, TaskCanceled
		}
	}
	return false, ""
}

// nextWait 返回最早的等待截止剩余时间。
func nextWait(waitSince map[string]time.Time, wait time.Duration) (time.Duration, bool) {
	var best time.Duration
	found := false
	now := time.Now()
	for _, start := range waitSince {
		d := start.Add(wait).Sub(now)
		if !found || d < best {
			best = d
			found = true
		}
	}
	return best, found
}

type taskResult struct {
	index int
	out   Outcome
}
