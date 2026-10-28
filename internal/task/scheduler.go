package task

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Outcome is what one injected execution returns for a task.
type Outcome struct {
	Report         *Report
	InputTokens    int
	OutputTokens   int
	UsageEstimated bool
	DurationMS     int64
	ErrorCode      string
	ErrorMessage   string
	// Err is the call failure; a non-nil value or a non-empty ErrorCode marks
	// the task failed.
	Err error
}

// Failed reports whether the outcome marks the task failed.
func (o Outcome) Failed() bool { return o.Err != nil || o.ErrorCode != "" }

// ExecFunc runs one task. The scheduler owns the task copy, and the context
// ends on cancel or timeout.
type ExecFunc func(ctx context.Context, t *Task) Outcome

// NotifyFunc receives a snapshot after each state change.
type NotifyFunc func(Group)

// Scheduler runs the tasks of one group with a concurrency limit, per task
// timeout and cancellation. It is safe to cancel from another goroutine.
type Scheduler struct {
	// Concurrency caps the tasks in flight; zero uses MaxConcurrentTasks.
	Concurrency int
	// Timeout bounds one task; zero uses TaskTimeout.
	Timeout time.Duration
	// Now replaces the clock, for tests.
	Now func() time.Time

	mu      sync.Mutex
	cancel  context.CancelFunc
	cancels map[string]context.CancelFunc
}

// NewScheduler returns a scheduler with the default limits.
func NewScheduler() *Scheduler {
	return &Scheduler{Concurrency: MaxConcurrentTasks, Timeout: TaskTimeout}
}

// Cancel stops the running group; in-flight tasks see their context end.
func (s *Scheduler) Cancel() {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Run executes every pending task of g and updates g in place until all tasks
// reach a terminal status, or the context ends. When the context ends, the
// pending tasks become canceled and the group becomes canceled.
func (s *Scheduler) Run(ctx context.Context, g *Group, exec ExecFunc, notify NotifyFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	groupCtx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.cancel = cancel
	s.cancels = map[string]context.CancelFunc{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.cancel = nil
		s.cancels = nil
		s.mu.Unlock()
		cancel()
	}()

	results := make(chan taskResult, len(g.Tasks))
	running := 0
	next := 0
	for {
		if groupCtx.Err() != nil {
			s.finishCanceled(g, running, results, notify)
			return
		}
		for running < s.concurrency() {
			idx := nextPending(g, next)
			if idx < 0 {
				break
			}
			next = idx + 1
			t := g.Tasks[idx]
			t.Status = TaskRunning
			t.UpdatedAt = s.now().Unix()
			g.Tasks[idx] = t
			g.UpdatedAt = t.UpdatedAt
			running++
			s.notify(notify, g)
			go s.execOne(groupCtx, idx, t, exec, results)
		}
		if running == 0 {
			return
		}
		select {
		case res := <-results:
			running--
			s.apply(g, res)
			s.notify(notify, g)
		case <-groupCtx.Done():
			s.finishCanceled(g, running, results, notify)
			return
		}
	}
}

// finishCanceled marks the pending tasks canceled, waits for the tasks still
// in flight and closes the group as canceled.
func (s *Scheduler) finishCanceled(g *Group, running int, results <-chan taskResult, notify NotifyFunc) {
	now := s.now()
	g.CancelPending(now)
	s.notify(notify, g)
	for running > 0 {
		res := <-results
		running--
		t := &g.Tasks[res.index]
		t.Status = TaskCanceled
		t.UpdatedAt = now.Unix()
		s.applyUsage(t, res.out)
	}
	g.Status = GroupCanceled
	g.UpdatedAt = now.Unix()
	s.notify(notify, g)
}

func (s *Scheduler) concurrency() int {
	if s.Concurrency > 0 {
		return s.Concurrency
	}
	return MaxConcurrentTasks
}

func (s *Scheduler) timeout() time.Duration {
	if s.Timeout > 0 {
		return s.Timeout
	}
	return TaskTimeout
}

func (s *Scheduler) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Scheduler) notify(notify NotifyFunc, g *Group) {
	if notify != nil {
		notify(g.Clone())
	}
}

func (s *Scheduler) execOne(parent context.Context, index int, t Task, exec ExecFunc, results chan<- taskResult) {
	ctx, cancel := context.WithTimeout(parent, s.timeout())
	s.mu.Lock()
	if s.cancels != nil {
		s.cancels[t.ID] = cancel
	}
	s.mu.Unlock()

	started := time.Now()
	out := exec(ctx, &t)
	timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)

	cancel()
	s.mu.Lock()
	delete(s.cancels, t.ID)
	s.mu.Unlock()

	if out.DurationMS <= 0 {
		out.DurationMS = time.Since(started).Milliseconds()
	}
	if timedOut {
		out.ErrorCode = "timeout"
		out.ErrorMessage = "任务执行超时"
	}
	results <- taskResult{index: index, out: out, timedOut: timedOut}
}

func (s *Scheduler) apply(g *Group, res taskResult) {
	t := &g.Tasks[res.index]
	t.UpdatedAt = s.now().Unix()
	g.UpdatedAt = t.UpdatedAt
	s.applyUsage(t, res.out)
	switch {
	case res.timedOut:
		t.Status = TaskFailed
		t.ErrorCode = "timeout"
		t.ErrorMessage = "任务执行超时"
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
}

func (s *Scheduler) applyUsage(t *Task, out Outcome) {
	t.InputTokens = out.InputTokens
	t.OutputTokens = out.OutputTokens
	t.UsageEstimated = out.UsageEstimated
	t.DurationMS = out.DurationMS
}

func nextPending(g *Group, from int) int {
	for i := from; i < len(g.Tasks); i++ {
		if g.Tasks[i].Status == TaskPending {
			return i
		}
	}
	return -1
}

type taskResult struct {
	index    int
	out      Outcome
	timedOut bool
}
