package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QingFengGod/bowknot-agent/internal/gateway"
)

type fakeChatter struct {
	snapshot gateway.Snapshot
	targets  []gateway.TargetView
	handler  func(ctx context.Context, req gateway.ChatRequest) (gateway.ChatResponse, error)

	mu     sync.Mutex
	last   gateway.ChatRequest
	origin string
}

func (f *fakeChatter) Snapshot() gateway.Snapshot { return f.snapshot }

// ChatTargets defaults to one target named after the snapshot model, as a
// configured gateway exposes.
func (f *fakeChatter) ChatTargets() []gateway.TargetView {
	if f.targets != nil {
		return f.targets
	}
	if f.snapshot.Model == "" {
		return nil
	}
	return []gateway.TargetView{{ProviderID: "p-00000001", ProviderName: "A", Model: f.snapshot.Model}}
}

func (f *fakeChatter) Chat(ctx context.Context, req gateway.ChatRequest, origin string) (gateway.ChatResponse, error) {
	f.mu.Lock()
	f.last = req
	f.origin = origin
	f.mu.Unlock()
	if f.handler == nil {
		return gateway.ChatResponse{Content: "回复"}, nil
	}
	return f.handler(ctx, req)
}

func (f *fakeChatter) lastRequest() gateway.ChatRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.last
}

func onlineSnapshot(model string) gateway.Snapshot {
	return gateway.Snapshot{State: gateway.StateOnline, Model: model, UpdatedAt: time.Now()}
}

func waitFor(t *testing.T, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatal("async action did not finish")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func newRunner(t *testing.T, f *fakeChatter) (*Runner, *Store, string) {
	t.Helper()
	store := NewStore(t.TempDir())
	if err := store.Load(); err != nil {
		t.Fatal(err)
	}
	conv := store.Create()
	if _, err := store.Append(conv.ID, Message{Role: RoleUser, Content: "你好",
		Status: StatusOK, At: 1}); err != nil {
		t.Fatal(err)
	}
	runner := NewRunner(f, store)
	return runner, store, conv.ID
}

func TestRunnerStreamsAndPersists(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	f := &fakeChatter{
		snapshot: onlineSnapshot("m1"),
		handler: func(ctx context.Context, req gateway.ChatRequest) (gateway.ChatResponse, error) {
			if !req.Stream {
				t.Error("request is not streaming")
			}
			req.OnDelta("你")
			close(started)
			<-release
			req.OnDelta("好")
			return gateway.ChatResponse{
				Content: "你好", Model: "m1", InputTokens: 5, OutputTokens: 2,
				HasUsage: true, Duration: 120 * time.Millisecond,
			}, nil
		},
	}
	runner, store, id := newRunner(t, f)
	if err := runner.Start(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	<-started
	conv, _ := store.Get(id)
	if len(conv.Messages) != 2 || conv.Messages[1].Status != StatusStreaming {
		t.Fatalf("streaming placeholder = %+v", conv.Messages)
	}
	if conv.Messages[1].Content != "你" {
		t.Errorf("streamed content = %q, want 你", conv.Messages[1].Content)
	}
	close(release)
	waitFor(t, func() bool {
		conv, _ := store.Get(id)
		return !runner.Busy() &&
			conv.Messages[len(conv.Messages)-1].Status == StatusOK
	})
	conv, _ = store.Get(id)
	reply := conv.Messages[1]
	if reply.Content != "你好" || reply.Model != "m1" {
		t.Errorf("reply = %+v", reply)
	}
	if reply.InputTokens != 5 || reply.OutputTokens != 2 || reply.UsageEstimated {
		t.Errorf("usage = %+v", reply)
	}
	if reply.DurationMS != 120 {
		t.Errorf("duration = %d", reply.DurationMS)
	}
	if conv.Model != "m1" || conv.UpdatedAt == 0 {
		t.Errorf("conversation = %+v", conv)
	}
	if f.origin != OriginAgent {
		t.Errorf("origin = %q, want %q", f.origin, OriginAgent)
	}
	if req := f.lastRequest(); req.ConversationID != id {
		t.Errorf("conversation ID = %q, want %q", req.ConversationID, id)
	}
	if runner.Busy() {
		t.Error("runner still busy")
	}
}

func TestRunnerStopKeepsPartial(t *testing.T) {
	started := make(chan struct{})
	f := &fakeChatter{snapshot: onlineSnapshot("m1")}
	f.handler = func(ctx context.Context, req gateway.ChatRequest) (gateway.ChatResponse, error) {
		req.OnDelta("部分内容")
		close(started)
		<-ctx.Done()
		return gateway.ChatResponse{Content: "部分内容", Model: "m1"}, ctx.Err()
	}
	runner, store, id := newRunner(t, f)
	if err := runner.Start(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	<-started
	runner.Stop()
	waitFor(t, func() bool { return !runner.Busy() })
	conv, _ := store.Get(id)
	reply := conv.Messages[len(conv.Messages)-1]
	if reply.Status != StatusAborted || reply.Content != "部分内容" {
		t.Errorf("reply = %+v, want aborted with partial content", reply)
	}
	if reply.ErrorCode != "" {
		t.Errorf("error code = %q, want none on abort", reply.ErrorCode)
	}
}

func TestRunnerErrorKeepsPartial(t *testing.T) {
	f := &fakeChatter{
		snapshot: onlineSnapshot("m1"),
		handler: func(ctx context.Context, req gateway.ChatRequest) (gateway.ChatResponse, error) {
			req.OnDelta("半")
			return gateway.ChatResponse{Model: "m1"},
				&gateway.Error{Kind: gateway.KindAuth, Message: "密钥无效"}
		},
	}
	runner, store, id := newRunner(t, f)
	if err := runner.Start(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !runner.Busy() })
	conv, _ := store.Get(id)
	reply := conv.Messages[len(conv.Messages)-1]
	if reply.Status != StatusError || reply.Content != "半" {
		t.Errorf("reply = %+v", reply)
	}
	if reply.ErrorCode != string(gateway.KindAuth) || reply.ErrorMessage != "密钥无效" {
		t.Errorf("error = %q / %q", reply.ErrorCode, reply.ErrorMessage)
	}
}

func TestRunnerEmptyReplyIsModelError(t *testing.T) {
	f := &fakeChatter{
		snapshot: onlineSnapshot("m1"),
		handler: func(ctx context.Context, req gateway.ChatRequest) (gateway.ChatResponse, error) {
			return gateway.ChatResponse{Model: "m1"}, nil
		},
	}
	runner, store, id := newRunner(t, f)
	if err := runner.Start(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !runner.Busy() })
	conv, _ := store.Get(id)
	reply := conv.Messages[len(conv.Messages)-1]
	if reply.Status != StatusError || reply.ErrorCode != string(gateway.KindModel) {
		t.Errorf("reply = %+v, want a model error", reply)
	}
	if !strings.Contains(reply.ErrorMessage, "没有返回内容") {
		t.Errorf("message = %q", reply.ErrorMessage)
	}
}

func TestRunnerRegenerateReplacesReply(t *testing.T) {
	f := &fakeChatter{
		snapshot: onlineSnapshot("m1"),
		handler: func(ctx context.Context, req gateway.ChatRequest) (gateway.ChatResponse, error) {
			return gateway.ChatResponse{Content: "新回复", Model: "m1", HasUsage: true}, nil
		},
	}
	runner, store, id := newRunner(t, f)
	if _, err := store.Append(id, Message{Role: RoleAssistant, Content: "旧回复",
		Status: StatusOK, At: 2}); err != nil {
		t.Fatal(err)
	}
	if err := runner.Regenerate(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !runner.Busy() })
	conv, _ := store.Get(id)
	if len(conv.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(conv.Messages))
	}
	if conv.Messages[1].Content != "新回复" {
		t.Errorf("reply = %q, want 新回复", conv.Messages[1].Content)
	}
	messages := f.lastRequest().Messages
	if len(messages) != 2 {
		t.Fatalf("request messages = %d, want system + user", len(messages))
	}
	if messages[0].Role != "system" || messages[0].Content != SystemPrompt {
		t.Errorf("first message = %+v", messages[0])
	}
	if messages[1].Role != "user" || messages[1].Content != "你好" {
		t.Errorf("second message = %+v", messages[1])
	}
}

func TestRunnerRegenerateNeedsAssistant(t *testing.T) {
	runner, _, id := newRunner(t, &fakeChatter{snapshot: onlineSnapshot("m1")})
	if err := runner.Regenerate(context.Background(), id); !errors.Is(err, ErrNoAssistantReply) {
		t.Errorf("regenerate = %v, want ErrNoAssistantReply", err)
	}
}

func TestRunnerOneGenerationAtATime(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	f := &fakeChatter{
		snapshot: onlineSnapshot("m1"),
		handler: func(ctx context.Context, req gateway.ChatRequest) (gateway.ChatResponse, error) {
			close(started)
			<-release
			return gateway.ChatResponse{Content: "ok"}, nil
		},
	}
	runner, store, id := newRunner(t, f)
	if err := runner.Start(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := runner.Start(context.Background(), id); !errors.Is(err, ErrBusy) {
		t.Errorf("second start = %v, want ErrBusy", err)
	}
	conv, _ := store.Get(id)
	if len(conv.Messages) != 2 {
		t.Errorf("messages = %d, want no extra placeholder", len(conv.Messages))
	}
	close(release)
	waitFor(t, func() bool { return !runner.Busy() })
}

func TestRunnerShutdownPersistsPartial(t *testing.T) {
	started := make(chan struct{})
	f := &fakeChatter{snapshot: onlineSnapshot("m1")}
	f.handler = func(ctx context.Context, req gateway.ChatRequest) (gateway.ChatResponse, error) {
		req.OnDelta("退出前")
		close(started)
		<-ctx.Done()
		return gateway.ChatResponse{}, ctx.Err()
	}
	runner, store, id := newRunner(t, f)
	if err := runner.Start(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	<-started
	runner.Shutdown()
	conv, _ := store.Get(id)
	reply := conv.Messages[len(conv.Messages)-1]
	if reply.Status != StatusAborted || reply.Content != "退出前" {
		t.Errorf("reply = %+v", reply)
	}
}

func TestRunnerPinsSelectedTarget(t *testing.T) {
	f := &fakeChatter{
		snapshot: onlineSnapshot("m1"),
		handler: func(ctx context.Context, req gateway.ChatRequest) (gateway.ChatResponse, error) {
			return gateway.ChatResponse{Content: "ok", Model: "m2"}, nil
		},
	}
	runner, _, id := newRunner(t, f)
	runner.SetTarget("p-00000002", "m2")
	if err := runner.Start(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !runner.Busy() })
	req := f.lastRequest()
	if req.ProviderID != "p-00000002" || req.Model != "m2" {
		t.Errorf("request target = %q/%q, want p-00000002/m2", req.ProviderID, req.Model)
	}
}

func TestRunnerUnknownConversation(t *testing.T) {
	runner := NewRunner(&fakeChatter{}, NewStore(""))
	if err := runner.Start(context.Background(), "c-00000000"); !errors.Is(err, ErrConversationNotFound) {
		t.Errorf("start = %v, want ErrConversationNotFound", err)
	}
}

func TestBuildMessagesSkipsEmptyReplies(t *testing.T) {
	conv := Conversation{Messages: []Message{
		{Role: RoleUser, Content: "问题", Status: StatusOK, At: 1},
		{Role: RoleAssistant, Content: "  ", Status: StatusError, At: 2},
		{Role: RoleUser, Content: "追问", Status: StatusOK, At: 3},
		{Role: RoleAssistant, Content: "回答", Status: StatusOK, At: 4},
	}}
	messages := buildMessages(conv)
	if len(messages) != 4 {
		t.Fatalf("messages = %d, want system + 3", len(messages))
	}
	if messages[1].Content != "问题" || messages[2].Content != "追问" || messages[3].Content != "回答" {
		t.Errorf("messages = %+v", messages)
	}
}
