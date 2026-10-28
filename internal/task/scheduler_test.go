package task

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSchedulerRunsWithConcurrencyLimit(t *testing.T) {
	g := newGroupForTest(t, TaskPending, TaskPending, TaskPending, TaskPending)
	g.Status = GroupRunning

	var inFlight, peak int32
	s := NewScheduler()
	s.Concurrency = 2
	s.Timeout = time.Second

	var mu sync.Mutex
	var snapshots []GroupStatus
	s.Run(context.Background(), g, func(ctx context.Context, tl *Task) Outcome {
		cur := atomic.AddInt32(&inFlight, 1)
		for {
			old := atomic.LoadInt32(&peak)
			if cur <= old || atomic.CompareAndSwapInt32(&peak, old, cur) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt32(&inFlight, -1)
		return Outcome{Report: &Report{Conclusion: "完成"}, InputTokens: 3, OutputTokens: 4}
	}, func(snapshot Group) {
		mu.Lock()
		snapshots = append(snapshots, snapshot.Status)
		mu.Unlock()
	})
	if peak > 2 {
		t.Errorf("peak concurrency = %d, want <= 2", peak)
	}
	for i := range g.Tasks {
		if g.Tasks[i].Status != TaskDone {
			t.Errorf("task %d = %s, want done", i, g.Tasks[i].Status)
		}
		if g.Tasks[i].Report == nil || g.Tasks[i].InputTokens != 3 {
			t.Errorf("task %d outcome lost: %+v", i, g.Tasks[i])
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(snapshots) == 0 {
		t.Error("notify was never called")
	}
}

func TestSchedulerTimeout(t *testing.T) {
	g := newGroupForTest(t, TaskPending)
	g.Status = GroupRunning

	s := NewScheduler()
	s.Timeout = 20 * time.Millisecond
	s.Run(context.Background(), g, func(ctx context.Context, tl *Task) Outcome {
		<-ctx.Done()
		return Outcome{Err: ctx.Err()}
	}, nil)

	if g.Tasks[0].Status != TaskFailed {
		t.Fatalf("status = %s, want failed", g.Tasks[0].Status)
	}
	if g.Tasks[0].ErrorCode != "timeout" {
		t.Fatalf("code = %q, want timeout", g.Tasks[0].ErrorCode)
	}
}

func TestSchedulerRecordsFailure(t *testing.T) {
	g := newGroupForTest(t, TaskPending, TaskPending)
	g.Status = GroupRunning

	s := NewScheduler()
	s.Run(context.Background(), g, func(ctx context.Context, tl *Task) Outcome {
		if tl.Goal == g.Tasks[0].Goal {
			return Outcome{ErrorCode: "auth", ErrorMessage: "密钥无效", Err: errors.New("401")}
		}
		return Outcome{Report: &Report{Conclusion: "完成"}}
	}, nil)

	if g.Tasks[0].Status != TaskFailed || g.Tasks[0].ErrorCode != "auth" {
		t.Fatalf("failed task = %+v", g.Tasks[0])
	}
	if g.Tasks[1].Status != TaskDone {
		t.Fatalf("second task = %s", g.Tasks[1].Status)
	}
}

func TestSchedulerCancel(t *testing.T) {
	g := newGroupForTest(t, TaskPending, TaskPending, TaskPending)
	g.Status = GroupRunning

	s := NewScheduler()
	s.Concurrency = 1
	started := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Run(context.Background(), g, func(ctx context.Context, tl *Task) Outcome {
			select {
			case started <- struct{}{}:
			default:
			}
			<-ctx.Done()
			return Outcome{Err: ctx.Err()}
		}, nil)
	}()

	<-started
	s.Cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop after Cancel")
	}

	if g.Status != GroupCanceled {
		t.Errorf("group = %s, want canceled", g.Status)
	}
	for i := range g.Tasks {
		if g.Tasks[i].Status != TaskCanceled {
			t.Errorf("task %d = %s, want canceled", i, g.Tasks[i].Status)
		}
	}
}
