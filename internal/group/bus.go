package group

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// 消息总线错误。
var (
	// ErrDirRequest 表示定向消息的目标不合法或不可见。
	ErrDirRequest = errors.New("定向消息的接收者无效")
	// ErrWaitTimeout 表示等待答复超时（FR-GRP-04）。
	ErrWaitTimeout = errors.New("等待答复超时")
	// ErrInvalidMessage 表示消息字段不合法。
	ErrInvalidMessage = errors.New("消息不合法")
)

// Bus 是一个 Run 的群消息总线：所有消息先写入 Store 并广播给主 Agent 与
// 用户，再唤醒匹配的定向等待者。不存在私聊旁路：定向请求与答复同样进入
// 公开消息流，主 Agent 全量可见（FR-GRP-02、FR-GRP-05）。
type Bus struct {
	store *Store
	runID string
	// WaitLimit 限制同一 Run 同时等待的成员数，防止等待者无限增长。
	WaitLimit int
	// Now 替换时间戳与超时计算，测试使用。
	Now func() time.Time

	mu        sync.Mutex
	waiters   []*waiter
	listeners []func(Message)
	closed    bool
	closedCh  chan struct{}
}

// waiter 是一次定向等待。
type waiter struct {
	from    string
	to      string
	replyTo string
	types   map[MessageType]bool
	cursor  int
	ch      chan Message
	done    bool
}

// NewBus 返回一个绑定到 Run 的消息总线。Run 必须已经保存到 store。
func NewBus(store *Store, runID string) (*Bus, error) {
	if store == nil {
		return nil, errors.New("group: 消息总线缺少存储")
	}
	if _, ok := store.GetRun(runID); !ok {
		return nil, ErrRunNotFound
	}
	return &Bus{
		store:     store,
		runID:     runID,
		WaitLimit: DefaultMaxMessages,
		Now:       time.Now,
		closedCh:  make(chan struct{}),
	}, nil
}

// RunID 返回总线所属的 Run。
func (b *Bus) RunID() string { return b.runID }

// OnMessage 注册一个同步监听器，每次成功追加消息后按顺序调用。监听器在
// 追加锁之外调用，避免界面刷新阻塞其他成员发送。
func (b *Bus) OnMessage(fn func(Message)) {
	if fn == nil {
		return
	}
	b.mu.Lock()
	b.listeners = append(b.listeners, fn)
	b.mu.Unlock()
}

// Close 结束所有等待者，之后的发送返回错误。
func (b *Bus) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	close(b.closedCh)
	waiters := b.waiters
	b.waiters = nil
	for _, w := range waiters {
		w.done = true
	}
	b.mu.Unlock()
}

// Cursor 返回当前已保存消息的最大序号。调用方在发起请求前记录游标，再
// 用 WaitFrom 等待，避免请求与等待之间到达的答复被漏掉。
func (b *Bus) Cursor() int {
	return b.store.LastSeq(b.runID)
}

// Post 广播一条进展、交接或阻塞消息。toID 为空即广播；非空时同时作为定向
// 消息记录，但仍在公开消息流中。
func (b *Bus) Post(from, taskID string, typ MessageType, toID, content string) (Message, error) {
	return b.emit(Message{TaskID: taskID, FromID: from, ToID: toID, Type: typ, Content: content})
}

// Request 发送一条定向请求并返回消息 ID，供答复的 ReplyTo 引用。
func (b *Bus) Request(from, taskID, toID, content string) (Message, error) {
	if toID == "" {
		return Message{}, ErrDirRequest
	}
	return b.emit(Message{TaskID: taskID, FromID: from, ToID: toID, Type: MessageRequest, Content: content})
}

// Respond 回复一条定向请求。答复会广播给全部成员。
func (b *Bus) Respond(from, taskID, toID, replyTo, content string) (Message, error) {
	if toID == "" || replyTo == "" {
		return Message{}, ErrDirRequest
	}
	return b.emit(Message{
		TaskID: taskID, FromID: from, ToID: toID, Type: MessageResponse,
		Content: content, ReplyTo: replyTo,
	})
}

// Wait 等待 after 之后到达的定向消息（ToID 等于 from），直到上下文结束或
// 超时。允许的消息类型为空表示任意类型。超时按 timeout<=0 回退到 Run 预算。
func (b *Bus) Wait(ctx context.Context, from string, types []MessageType, after int, timeout time.Duration) (Message, error) {
	return b.wait(ctx, from, "", types, after, timeout)
}

// WaitReply 等待对 replyTo 这条请求的答复。replyTo 为空时等待任意定向消息。
func (b *Bus) WaitReply(ctx context.Context, from, replyTo string, timeout time.Duration) (Message, error) {
	return b.wait(ctx, from, replyTo, []MessageType{MessageResponse}, 0, timeout)
}

func (b *Bus) wait(parent context.Context, from, replyTo string, types []MessageType, after int, timeout time.Duration) (Message, error) {
	if !ValidAuthorID(from) {
		return Message{}, ErrInvalidMessage
	}
	if parent == nil {
		parent = context.Background()
	}
	if timeout <= 0 {
		timeout = DefaultWaitTimeout
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	w := &waiter{
		from:    from,
		replyTo: replyTo,
		cursor:  after,
		ch:      make(chan Message, 1),
	}
	if len(types) > 0 {
		w.types = make(map[MessageType]bool, len(types))
		for _, t := range types {
			w.types[t] = true
		}
	}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return Message{}, ErrWaitTimeout
	}
	if b.WaitLimit > 0 && len(b.waiters) >= b.WaitLimit {
		b.mu.Unlock()
		return Message{}, fmt.Errorf("%w：等待者已达上限 %d", ErrWaitTimeout, b.WaitLimit)
	}
	b.waiters = append(b.waiters, w)
	b.mu.Unlock()

	// Register 之后复查已经写在游标之后的消息，避免错过快速答复。
	if msg, ok := b.past(w); ok {
		b.removeWaiter(w)
		return msg, nil
	}

	select {
	case msg := <-w.ch:
		return msg, nil
	case <-b.closedCh:
		return Message{}, errors.New("group: 消息总线已关闭")
	case <-ctx.Done():
		b.removeWaiter(w)
		if parent.Err() != nil {
			return Message{}, parent.Err()
		}
		return Message{}, fmt.Errorf("%w（%s）", ErrWaitTimeout, timeout)
	}
}

// past 查找游标之后第一个匹配的已保存消息。
func (b *Bus) past(w *waiter) (Message, bool) {
	msgs := b.store.Messages(b.runID)
	for _, m := range msgs {
		if m.Seq <= w.cursor {
			continue
		}
		if w.match(m) {
			return m, true
		}
	}
	return Message{}, false
}

func (w *waiter) match(m Message) bool {
	if m.ToID != w.from {
		return false
	}
	if w.replyTo != "" && m.ReplyTo != w.replyTo {
		return false
	}
	if len(w.types) > 0 && !w.types[m.Type] {
		return false
	}
	return true
}

func (b *Bus) removeWaiter(target *waiter) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.removeWaiterLocked(target)
}

func (b *Bus) removeWaiterLocked(target *waiter) {
	for i, w := range b.waiters {
		if w != target {
			continue
		}
		b.waiters = append(b.waiters[:i], b.waiters[i+1:]...)
		return
	}
}

// emit 追加消息、通知监听器并唤醒匹配的等待者。
func (b *Bus) emit(m Message) (Message, error) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return Message{}, errors.New("group: 消息总线已关闭")
	}
	b.mu.Unlock()

	m.RunID = b.runID
	saved, err := b.store.AppendMessage(m)
	if err != nil && !errors.Is(err, ErrMessageWrite) {
		return Message{}, err
	}
	// A failed disk write keeps the message in memory (FR-GRP-02): still
	// broadcast it and wake the matching waiters, then report the notice.

	b.mu.Lock()
	listeners := append([]func(Message){}, b.listeners...)
	var hit []*waiter
	for _, w := range b.waiters {
		if w.match(saved) {
			w.done = true
			hit = append(hit, w)
		}
	}
	if len(hit) > 0 {
		kept := b.waiters[:0]
		for _, w := range b.waiters {
			if !w.done {
				kept = append(kept, w)
			}
		}
		b.waiters = kept
	}
	b.mu.Unlock()

	for _, w := range hit {
		w.ch <- saved
	}
	for _, fn := range listeners {
		fn(saved)
	}
	return saved, err
}
