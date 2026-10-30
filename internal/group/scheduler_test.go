package group

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSchedulerRunsInDependencyOrder(t *testing.T) {
	run := testRun(t,
		testTask("t-00000001"),
		testTask("t-00000002", "t-00000001"),
	)
	s := NewScheduler()
	s.Concurrency = 1

	var mu sync.Mutex
	var order []string
	s.Run(context.Background(), run, func(ctx context.Context, tl *Task) Outcome {
		mu.Lock()
		order = append(order, tl.ID)
		mu.Unlock()
		return Outcome{Report: &Report{Conclusion: "完成"}}
	}, nil)

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 || order[0] != "t-00000001" || order[1] != "t-00000002" {
		t.Fatalf("执行顺序 = %v, want [t-00000001 t-00000002]", order)
	}
	for i := range run.Plan.Tasks {
		if run.Plan.Tasks[i].Status != TaskDone {
			t.Errorf("任务 %d = %s, want done", i, run.Plan.Tasks[i].Status)
		}
	}
	if run.Status != RunDone {
		t.Errorf("Run 状态 = %s, want done", run.Status)
	}
}

func TestSchedulerConcurrencyLimit(t *testing.T) {
	run := testRun(t,
		testTask("t-00000001"), testTask("t-00000002"),
		testTask("t-00000003"), testTask("t-00000004"),
	)
	s := NewScheduler()
	s.Concurrency = 2

	var inFlight, peak int32
	s.Run(context.Background(), run, func(ctx context.Context, tl *Task) Outcome {
		cur := atomic.AddInt32(&inFlight, 1)
		for {
			old := atomic.LoadInt32(&peak)
			if cur <= old || atomic.CompareAndSwapInt32(&peak, old, cur) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		atomic.AddInt32(&inFlight, -1)
		return Outcome{Report: &Report{Conclusion: "完成"}, InputTokens: 3, OutputTokens: 4}
	}, nil)

	if peak > 2 {
		t.Errorf("峰值并发 = %d, want <= 2", peak)
	}
	if run.InputTokens != 12 || run.OutputTokens != 16 {
		t.Errorf("Run 用量 = %d/%d, want 12/16", run.InputTokens, run.OutputTokens)
	}
	if run.Status != RunDone {
		t.Errorf("Run 状态 = %s, want done", run.Status)
	}
}

func TestSchedulerWaitTimeout(t *testing.T) {
	run := testRun(t,
		testTask("t-00000001"),
		testTask("t-00000002", "t-00000001"),
	)
	s := NewScheduler()
	s.Concurrency = 1
	s.WaitTimeout = 15 * time.Millisecond

	s.Run(context.Background(), run, func(ctx context.Context, tl *Task) Outcome {
		if tl.ID == "t-00000001" {
			time.Sleep(60 * time.Millisecond)
		}
		return Outcome{Report: &Report{Conclusion: "完成"}}
	}, nil)

	if run.Plan.Tasks[1].Status != TaskFailed || run.Plan.Tasks[1].ErrorCode != "wait_timeout" {
		t.Fatalf("等待超时任务 = %+v", run.Plan.Tasks[1])
	}
	if run.Plan.Tasks[0].Status != TaskDone {
		t.Errorf("前置任务 = %s, want done", run.Plan.Tasks[0].Status)
	}
}

func TestSchedulerFailureCancelsDependents(t *testing.T) {
	run := testRun(t,
		testTask("t-00000001"),
		testTask("t-00000002", "t-00000001"),
		testTask("t-00000003", "t-00000002"),
	)
	s := NewScheduler()
	s.Concurrency = 1

	s.Run(context.Background(), run, func(ctx context.Context, tl *Task) Outcome {
		if tl.ID == "t-00000001" {
			return Outcome{ErrorCode: "tool_failed", ErrorMessage: "工具失败"}
		}
		return Outcome{Report: &Report{Conclusion: "完成"}}
	}, nil)

	wantCodes := []string{"", "dependency_failed", "dependency_canceled"}
	if run.Plan.Tasks[0].Status != TaskFailed || run.Plan.Tasks[0].ErrorCode != "tool_failed" {
		t.Fatalf("失败任务 = %+v", run.Plan.Tasks[0])
	}
	for i := 1; i < len(run.Plan.Tasks); i++ {
		if run.Plan.Tasks[i].Status != TaskCanceled || run.Plan.Tasks[i].ErrorCode != wantCodes[i] {
			t.Errorf("依赖任务 %d = %+v, want canceled %s", i, run.Plan.Tasks[i], wantCodes[i])
		}
	}
	if run.Status != RunFailed {
		t.Errorf("Run 状态 = %s, want failed", run.Status)
	}
}

func TestSchedulerCancel(t *testing.T) {
	run := testRun(t,
		testTask("t-00000001"), testTask("t-00000002"), testTask("t-00000003"),
	)
	s := NewScheduler()
	s.Concurrency = 1
	started := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Run(context.Background(), run, func(ctx context.Context, tl *Task) Outcome {
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
		t.Fatal("Cancel 后 Run 未停止")
	}

	if run.Status != RunCanceled {
		t.Errorf("Run 状态 = %s, want canceled", run.Status)
	}
	for i := range run.Plan.Tasks {
		if run.Plan.Tasks[i].Status != TaskCanceled {
			t.Errorf("任务 %d = %s, want canceled", i, run.Plan.Tasks[i].Status)
		}
	}
}

func TestSchedulerDerivesFailedWhenAllFail(t *testing.T) {
	run := testRun(t, testTask("t-00000001"), testTask("t-00000002"))
	s := NewScheduler()
	s.Concurrency = 2

	s.Run(context.Background(), run, func(ctx context.Context, tl *Task) Outcome {
		return Outcome{ErrorCode: "model_error", ErrorMessage: "模型错误"}
	}, nil)

	if run.Status != RunFailed {
		t.Errorf("Run 状态 = %s, want failed", run.Status)
	}
	for i := range run.Plan.Tasks {
		if run.Plan.Tasks[i].Status != TaskFailed {
			t.Errorf("任务 %d = %s, want failed", i, run.Plan.Tasks[i].Status)
		}
	}
}

func TestSchedulerNotifySnapshots(t *testing.T) {
	run := testRun(t, testTask("t-00000001"))
	s := NewScheduler()

	var mu sync.Mutex
	var snapshots int
	s.Run(context.Background(), run, func(ctx context.Context, tl *Task) Outcome {
		return Outcome{Report: &Report{Conclusion: "完成"}}
	}, func(snapshot Run) {
		mu.Lock()
		snapshots++
		mu.Unlock()
	})

	mu.Lock()
	defer mu.Unlock()
	if snapshots < 2 {
		t.Errorf("通知次数 = %d, want >= 2", snapshots)
	}
}
