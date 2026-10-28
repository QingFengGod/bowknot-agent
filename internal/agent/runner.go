package agent

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/QingFengGod/bowknot-agent/internal/gateway"
)

// OriginAgent marks usage records written for assistant calls.
const OriginAgent = "agent"

// SystemPrompt is the fixed persona sent with every request. Editing the
// persona is left to a later agent management plan.
const SystemPrompt = "你是 Bowknot 助手，一个本地桌面工作台上的中文 AI 助手。" +
	"回答要简洁、准确，先给结论再补充必要细节。" +
	"你当前无法调用工具、执行命令、访问本地文件或联网检索；" +
	"遇到这类请求时请明确说明限制，并给出可行的替代建议。"

// ErrBusy is returned when a generation is already running.
var ErrBusy = errors.New("正在生成回复，请先停止")

// deltaThrottle is the interval at which streamed text reaches the view.
const deltaThrottle = 50 * time.Millisecond

// Chatter is the part of the gateway the assistant uses.
type Chatter interface {
	Chat(ctx context.Context, req gateway.ChatRequest, origin string) (gateway.ChatResponse, error)
	Snapshot() gateway.Snapshot
	ChatTargets() []gateway.TargetView
}

// Runner drives one generation at a time across every conversation.
type Runner struct {
	chatter Chatter
	store   *Store

	// Dispatch marshals a function to the UI thread; nil runs inline.
	Dispatch func(func())
	// Repaint asks the window to redraw; nil does nothing.
	Repaint func()

	mu         sync.Mutex
	run        *runState
	notice     string
	providerID string
	model      string
}

// NewRunner returns a runner over the chatter and the store.
func NewRunner(chatter Chatter, store *Store) *Runner {
	return &Runner{chatter: chatter, store: store}
}

// Notice returns the last save or local failure shown to the user.
func (r *Runner) Notice() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.notice
}

// Busy reports whether a generation is running.
func (r *Runner) Busy() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.run != nil
}

// RunningConversation returns the conversation receiving the current reply.
func (r *Runner) RunningConversation() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.run == nil {
		return ""
	}
	return r.run.conversationID
}

// SetTarget pins the next generations to one route target. Empty values let
// the gateway use its route order.
func (r *Runner) SetTarget(providerID, model string) {
	r.mu.Lock()
	r.providerID, r.model = providerID, model
	r.mu.Unlock()
}

// Target returns the pinned route target.
func (r *Runner) Target() (providerID, model string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.providerID, r.model
}

// Start appends an assistant placeholder and streams a reply for it. The
// context is cancelled by Stop or Shutdown; a nil context runs detached.
func (r *Runner) Start(ctx context.Context, conversationID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	busy := r.run != nil
	providerID, model := r.providerID, r.model
	r.mu.Unlock()
	if busy {
		return ErrBusy
	}
	conv, ok := r.store.Get(conversationID)
	if !ok {
		return ErrConversationNotFound
	}
	if len(conv.Messages) >= MaxMessages {
		return ErrConversationFull
	}
	req := gateway.ChatRequest{
		ProviderID:     providerID,
		Model:          model,
		ConversationID: conversationID,
		Messages:       buildMessages(conv),
		Stream:         true,
	}
	index, err := r.store.Append(conversationID, Message{
		Role:   RoleAssistant,
		Status: StatusStreaming,
		At:     time.Now().Unix(),
	})
	if err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(ctx)
	state := &runState{conversationID: conversationID, index: index, start: time.Now(), cancel: cancel}
	req.OnDelta = func(text string) {
		if state.add(text) {
			r.flush(state)
		}
	}

	r.mu.Lock()
	if r.run != nil {
		r.mu.Unlock()
		cancel()
		_, _ = r.store.DropLastAssistant(conversationID)
		r.repaint()
		return ErrBusy
	}
	r.run = state
	r.notice = ""
	r.mu.Unlock()

	go r.execute(runCtx, state, req)
	return nil
}

// Stop cancels the running generation; the partial reply is kept.
func (r *Runner) Stop() {
	r.mu.Lock()
	state := r.run
	r.mu.Unlock()
	if state != nil {
		state.cancel()
	}
}

// Shutdown cancels the running generation and persists what streamed so far.
func (r *Runner) Shutdown() {
	r.mu.Lock()
	state := r.run
	r.mu.Unlock()
	if state == nil {
		return
	}
	state.cancel()
	r.finalize(state, true, gateway.ChatResponse{}, context.Canceled)
}

// Regenerate drops the last assistant reply and streams it again with the
// same preceding context.
func (r *Runner) Regenerate(ctx context.Context, conversationID string) error {
	r.mu.Lock()
	busy := r.run != nil
	r.mu.Unlock()
	if busy {
		return ErrBusy
	}
	dropped, err := r.store.DropLastAssistant(conversationID)
	if err != nil {
		return err
	}
	if !dropped {
		return ErrNoAssistantReply
	}
	if err := r.store.Save(conversationID); err != nil {
		r.setNotice(fmt.Sprintf("会话保存失败：%v", err))
	}
	return r.Start(ctx, conversationID)
}

func (r *Runner) execute(ctx context.Context, state *runState, req gateway.ChatRequest) {
	resp, err := r.chatter.Chat(ctx, req, OriginAgent)
	canceled := errors.Is(ctx.Err(), context.Canceled)
	r.dispatch(func() { r.finalize(state, canceled, resp, err) })
}

// flush copies the buffered text into the conversation on the UI thread.
func (r *Runner) flush(state *runState) {
	content := state.snapshot()
	r.dispatch(func() {
		if state.closed() {
			return
		}
		if err := r.store.SetContent(state.conversationID, state.index, content); err != nil {
			log.Printf("agent: update streaming reply of %s: %v", state.conversationID, err)
			return
		}
		r.repaint()
	})
}

// finalize stores the outcome of one generation. It runs at most once per
// generation, on the UI thread.
func (r *Runner) finalize(state *runState, canceled bool, resp gateway.ChatResponse, err error) {
	content, alreadyClosed := state.close()
	if alreadyClosed {
		return
	}
	if strings.TrimSpace(content) == "" {
		content = resp.Content
	}
	duration := resp.Duration
	if duration <= 0 {
		duration = time.Since(state.start)
	}
	msg := Message{
		Role:       RoleAssistant,
		Content:    content,
		Model:      resp.Model,
		DurationMS: duration.Milliseconds(),
		At:         time.Now().Unix(),
	}
	switch {
	case canceled:
		msg.Status = StatusAborted
	case err != nil:
		msg.Status = StatusError
		msg.ErrorCode = string(gateway.KindOf(err))
		msg.ErrorMessage = err.Error()
	case strings.TrimSpace(content) == "":
		msg.Status = StatusError
		msg.ErrorCode = string(gateway.KindModel)
		msg.ErrorMessage = "模型没有返回内容"
	default:
		msg.Status = StatusOK
		msg.InputTokens = resp.InputTokens
		msg.OutputTokens = resp.OutputTokens
		msg.UsageEstimated = !resp.HasUsage
	}
	if err := r.store.FinishMessage(state.conversationID, state.index, msg); err != nil {
		log.Printf("agent: finish reply of %s: %v", state.conversationID, err)
	} else if err := r.store.Save(state.conversationID); err != nil {
		r.setNotice(fmt.Sprintf("会话保存失败：%v", err))
	}
	r.mu.Lock()
	if r.run == state {
		r.run = nil
	}
	r.mu.Unlock()
	r.repaint()
}

func (r *Runner) setNotice(text string) {
	r.mu.Lock()
	r.notice = text
	r.mu.Unlock()
}

func (r *Runner) dispatch(fn func()) {
	if r.Dispatch != nil {
		r.Dispatch(fn)
		return
	}
	fn()
}

func (r *Runner) repaint() {
	if r.Repaint != nil {
		r.Repaint()
	}
}

// buildMessages turns a conversation into the request context. The system
// persona always comes first, and empty assistant turns are dropped so a
// failed call does not pollute the next one.
func buildMessages(c Conversation) []gateway.ChatMessage {
	out := make([]gateway.ChatMessage, 0, len(c.Messages)+1)
	out = append(out, gateway.ChatMessage{Role: "system", Content: SystemPrompt})
	for _, m := range c.Messages {
		if m.Role == RoleAssistant && strings.TrimSpace(m.Content) == "" {
			continue
		}
		out = append(out, gateway.ChatMessage{Role: string(m.Role), Content: m.Content})
	}
	return out
}

// runState buffers one streamed reply between UI flushes.
type runState struct {
	conversationID string
	index          int
	start          time.Time
	cancel         context.CancelFunc

	mu       sync.Mutex
	builder  strings.Builder
	last     time.Time
	isClosed bool
}

// add appends streamed text and reports whether the throttle window elapsed.
func (a *runState) add(text string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.builder.WriteString(text)
	now := time.Now()
	if now.Sub(a.last) < deltaThrottle {
		return false
	}
	a.last = now
	return true
}

// snapshot returns the buffered text so far.
func (a *runState) snapshot() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.builder.String()
}

// close marks the reply finished and returns the buffered text.
func (a *runState) close() (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.isClosed {
		return a.builder.String(), true
	}
	a.isClosed = true
	return a.builder.String(), false
}

func (a *runState) closed() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.isClosed
}
