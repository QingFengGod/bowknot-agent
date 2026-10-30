package group

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func newTestBus(t *testing.T) (*Bus, *Store, string) {
	t.Helper()
	store := openMessageStore(t, t.TempDir())
	runID := groupRunID(t, store)
	bus, err := NewBus(store, runID)
	if err != nil {
		t.Fatalf("NewBus() error: %v", err)
	}
	return bus, store, runID
}

func TestBusPostBroadcastsAndNotifies(t *testing.T) {
	bus, store, runID := newTestBus(t)
	defer bus.Close()

	var mu sync.Mutex
	var seen []Message
	bus.OnMessage(func(m Message) {
		mu.Lock()
		seen = append(seen, m)
		mu.Unlock()
	})

	msg, err := bus.Post("a-00000001", "t-00000001", MessageProgress, "", "开始处理")
	if err != nil {
		t.Fatalf("Post() error: %v", err)
	}
	if msg.RunID != runID || msg.Seq != 1 {
		t.Fatalf("消息 = %+v", msg)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 || seen[0].ID != msg.ID {
		t.Fatalf("监听器收到 %+v", seen)
	}
	if got := store.Messages(runID); len(got) != 1 {
		t.Fatalf("Store 消息 = %+v", got)
	}
}

func TestBusRequestRespondWait(t *testing.T) {
	bus, store, runID := newTestBus(t)
	defer bus.Close()

	const asker, replier = "a-00000001", "a-00000002"
	req, err := bus.Request(asker, "t-00000001", replier, "请补充资料")
	if err != nil {
		t.Fatalf("Request() error: %v", err)
	}
	if _, err := bus.Respond(replier, "t-00000002", asker, req.ID, "答复内容"); err != nil {
		t.Fatalf("Respond() error: %v", err)
	}
	// 已经写入的答复必须能被 past 命中，覆盖请求后才注册等待的情况。
	w := &waiter{from: asker, replyTo: req.ID, types: map[MessageType]bool{MessageResponse: true}}
	msg, ok := bus.past(w)
	if !ok || msg.Content != "答复内容" {
		t.Fatalf("past() = %+v, ok=%v", msg, ok)
	}

	// 定向消息仍然全量可见。
	msgs := store.Messages(runID)
	if len(msgs) != 2 || len(msgs) == 0 {
		t.Fatalf("消息数量 = %d", len(msgs))
	}
	if msgs[0].Type != MessageRequest || msgs[0].ToID != replier {
		t.Fatalf("请求消息 = %+v", msgs[0])
	}
	if msgs[1].Type != MessageResponse || msgs[1].ReplyTo != req.ID {
		t.Fatalf("答复消息 = %+v", msgs[1])
	}
}

func TestBusWaitReply(t *testing.T) {
	bus, _, _ := newTestBus(t)
	defer bus.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, err := bus.Request("a-00000001", "t-00000001", "a-00000002", "请求")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(10 * time.Millisecond)
		_, _ = bus.Respond("a-00000002", "t-00000002", "a-00000001", req.ID, "答复")
	}()
	msg, err := bus.WaitReply(ctx, "a-00000001", req.ID, 500*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitReply() error: %v", err)
	}
	if msg.Content != "答复" || msg.ReplyTo != req.ID {
		t.Fatalf("WaitReply() = %+v", msg)
	}
}

func TestBusWaitUsesCursor(t *testing.T) {
	bus, _, _ := newTestBus(t)
	defer bus.Close()

	// 游标之前的答复不应被消费。
	if _, err := bus.Respond("a-00000002", "", "a-00000001", "m-00000001", "旧答复"); err != nil {
		t.Fatal(err)
	}
	after := bus.Cursor()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := bus.Wait(ctx, "a-00000001", nil, after, 100*time.Millisecond); !errors.Is(err, ErrWaitTimeout) {
		t.Fatalf("旧答复不应返回: %v", err)
	}
}

func TestBusWaitTimeout(t *testing.T) {
	bus, _, _ := newTestBus(t)
	defer bus.Close()
	start := time.Now()
	_, err := bus.WaitReply(context.Background(), "a-00000001", "", 20*time.Millisecond)
	if !errors.Is(err, ErrWaitTimeout) {
		t.Fatalf("WaitReply() error = %v, want ErrWaitTimeout", err)
	}
	if time.Since(start) < 15*time.Millisecond {
		t.Errorf("超时过早返回: %v", time.Since(start))
	}
}

func TestBusCloseWakesWaiters(t *testing.T) {
	bus, _, _ := newTestBus(t)
	done := make(chan error, 1)
	go func() {
		_, err := bus.WaitReply(context.Background(), "a-00000001", "", time.Second)
		done <- err
	}()
	time.Sleep(10 * time.Millisecond)
	bus.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("关闭后等待应失败")
		}
	case <-time.After(time.Second):
		t.Fatal("Close() 未唤醒等待者")
	}
	if _, err := bus.Post("a-00000001", "", MessageProgress, "", "x"); err == nil {
		t.Fatal("关闭后发送应失败")
	}
}

func TestBusRejectsInvalidDirection(t *testing.T) {
	bus, _, _ := newTestBus(t)
	defer bus.Close()
	if _, err := bus.Request("a-00000001", "", "", "x"); !errors.Is(err, ErrDirRequest) {
		t.Fatalf("Request() error = %v", err)
	}
	if _, err := bus.Respond("a-00000001", "", "a-00000002", "", "x"); !errors.Is(err, ErrDirRequest) {
		t.Fatalf("Respond() error = %v", err)
	}
}

func TestBusBroadcastsWhenMessageWriteFails(t *testing.T) {
	bus, store, runID := newTestBus(t)
	defer bus.Close()
	messagesDir := filepath.Join(store.GroupsDir(), "gp-00000001", messageDirName)
	if err := os.MkdirAll(filepath.Dir(messagesDir), 0o700); err != nil {
		t.Fatal(err)
	}
	// A file where the messages directory belongs makes every append fail.
	if err := os.WriteFile(messagesDir, []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var seen []Message
	bus.OnMessage(func(m Message) {
		mu.Lock()
		seen = append(seen, m)
		mu.Unlock()
	})

	msg, err := bus.Post("a-00000001", "t-00000001", MessageProgress, "", "进展")
	if !errors.Is(err, ErrMessageWrite) {
		t.Fatalf("Post() error = %v, want ErrMessageWrite", err)
	}
	if msg.Seq != 1 {
		t.Fatalf("消息序号 = %d", msg.Seq)
	}
	if got := store.Messages(runID); len(got) != 1 || got[0].ID != msg.ID {
		t.Fatalf("内存消息 = %+v", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 || seen[0].ID != msg.ID {
		t.Fatalf("监听器收到 %+v", seen)
	}
}
