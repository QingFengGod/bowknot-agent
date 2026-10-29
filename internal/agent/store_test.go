package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s := NewStore(t.TempDir())
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStoreAgentConversationsStaySeparate(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	main := s.Create()
	sub := s.CreateFor("a-12345678")
	if _, err := s.Append(main.ID, Message{Role: RoleUser, Content: "主导", Status: StatusOK,
		At: 20}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(sub.ID, Message{Role: RoleUser, Content: "子", Status: StatusOK,
		At: 10}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{main.ID, sub.ID} {
		if err := s.Save(id); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{
		filepath.Join(dir, MainAgentID, SessionsDirName, main.ID+".json"),
		filepath.Join(dir, "a-12345678", SessionsDirName, sub.ID+".json"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("conversation file %s is missing: %v", path, err)
		}
	}

	if list := s.List(); len(list) != 1 || list[0].AgentID != "" {
		t.Errorf("main list = %+v", list)
	}
	subList := s.ListByAgent("a-12345678")
	if len(subList) != 1 || subList[0].ID != sub.ID || subList[0].AgentID != "a-12345678" {
		t.Errorf("sub list = %+v", subList)
	}

	reloaded := NewStore(dir)
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	again, ok := reloaded.Get(sub.ID)
	if !ok || again.AgentID != "a-12345678" {
		t.Errorf("reloaded sub conversation = %+v", again)
	}
	if list := reloaded.ListByAgent("a-12345678"); len(list) != 1 {
		t.Errorf("reloaded sub list = %+v", list)
	}
}

func TestConversationRejectsBadAgentID(t *testing.T) {
	conv := NewConversation(time.Unix(100, 0))
	conv.AgentID = "main"
	if err := conv.Validate(); err == nil {
		t.Error("a non sub agent ID is accepted")
	}
}

func TestStoreRenameKeepsTitleAndPersists(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	conv := s.Create()
	if err := s.Rename(conv.ID, "  我的   会话  "); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(conv.ID, Message{Role: RoleUser, Content: "第一条消息",
		Status: StatusOK, At: 50}); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(conv.ID); err != nil {
		t.Fatal(err)
	}
	got, ok := s.Get(conv.ID)
	if !ok {
		t.Fatal("conversation not found")
	}
	if got.Title != "我的 会话" || !got.TitleEdited {
		t.Errorf("title = %q, edited = %v", got.Title, got.TitleEdited)
	}

	reloaded := NewStore(dir)
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	again, ok := reloaded.Get(conv.ID)
	if !ok {
		t.Fatal("conversation not reloaded")
	}
	if again.Title != "我的 会话" {
		t.Errorf("reloaded title = %q", again.Title)
	}
}

func TestStoreRenameFallbacks(t *testing.T) {
	s := newTestStore(t)
	conv := s.Create()
	if err := s.Rename(conv.ID, "   "); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(conv.ID); got.Title != DefaultTitle {
		t.Errorf("empty title = %q, want %q", got.Title, DefaultTitle)
	}
	if err := s.Rename("c-ffffffff", "不存在"); err != ErrConversationNotFound {
		t.Errorf("missing conversation = %v, want not found", err)
	}
}

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	conv := s.Create()
	if _, err := s.Append(conv.ID, Message{Role: RoleUser, Content: "帮我总结这段文字",
		Status: StatusOK, At: 100}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(conv.ID, Message{Role: RoleAssistant, Content: "好的",
		Model: "m1", InputTokens: 3, OutputTokens: 4, Status: StatusOK, At: 101}); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(conv.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, MainAgentID, SessionsDirName, conv.ID+".json")); err != nil {
		t.Fatalf("conversation file missing: %v", err)
	}

	reloaded := NewStore(dir)
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	got, ok := reloaded.Get(conv.ID)
	if !ok {
		t.Fatal("conversation not reloaded")
	}
	if got.Title != "帮我总结这段文字" || len(got.Messages) != 2 {
		t.Errorf("reloaded = %+v", got)
	}
	if got.Model != "m1" || got.UpdatedAt != 101 {
		t.Errorf("model/time = %q,%d", got.Model, got.UpdatedAt)
	}
	if got.Messages[1].InputTokens != 3 || got.Messages[1].OutputTokens != 4 {
		t.Errorf("usage = %+v", got.Messages[1])
	}
}

func TestStoreListOrder(t *testing.T) {
	s := newTestStore(t)
	first := s.Create()
	second := s.Create()
	if _, err := s.Append(first.ID, Message{Role: RoleUser, Content: "old", Status: StatusOK, At: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(second.ID, Message{Role: RoleUser, Content: "new", Status: StatusOK, At: 20}); err != nil {
		t.Fatal(err)
	}
	list := s.List()
	if len(list) != 2 {
		t.Fatalf("list = %d, want 2", len(list))
	}
	if list[0].ID != second.ID || list[1].ID != first.ID {
		t.Errorf("order = %s,%s, want newest first", list[0].ID, list[1].ID)
	}
	if list[0].Messages != 1 || list[0].Title != "new" {
		t.Errorf("summary = %+v", list[0])
	}
}

func TestStoreDeleteRemovesFile(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	conv := s.Create()
	if err := s.Save(conv.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(conv.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get(conv.ID); ok {
		t.Error("deleted conversation still readable")
	}
	if _, err := os.Stat(filepath.Join(dir, MainAgentID, SessionsDirName, conv.ID+".json")); !os.IsNotExist(err) {
		t.Errorf("file still exists: %v", err)
	}
	if err := s.Delete(conv.ID); err != ErrConversationNotFound {
		t.Errorf("second delete = %v", err)
	}
}

func TestStoreSkipsBadFiles(t *testing.T) {
	dir := t.TempDir()
	sessions := filepath.Join(dir, MainAgentID, SessionsDirName)
	if err := os.MkdirAll(sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	valid := Conversation{
		Schema: SchemaVersion, ID: "c-11111111", Title: "好的",
		CreatedAt: 1, UpdatedAt: 2,
		Messages: []Message{{Role: RoleUser, Content: "hi", Status: StatusOK, At: 1}},
	}
	writeJSON(t, filepath.Join(sessions, "c-11111111.json"), valid)
	writeJSON(t, filepath.Join(sessions, "c-22222222.json"), Conversation{
		Schema: 99, ID: "c-22222222", CreatedAt: 1, UpdatedAt: 1,
	})
	writeJSON(t, filepath.Join(sessions, "c-33333333.json"), Conversation{
		Schema: SchemaVersion, ID: "bad", CreatedAt: 1, UpdatedAt: 1,
	})
	if err := os.WriteFile(filepath.Join(sessions, "c-44444444.json"), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	huge := filepath.Join(sessions, "c-55555555.json")
	if err := os.WriteFile(huge, []byte(strings.Repeat("x", MaxConversationBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	// A sub agent conversation stored under the main agent directory is not
	// merged into the main list.
	writeJSON(t, filepath.Join(sessions, "c-66666666.json"), Conversation{
		Schema: SchemaVersion, ID: "c-66666666", AgentID: "a-12345678", CreatedAt: 1, UpdatedAt: 1,
	})
	if err := os.WriteFile(filepath.Join(sessions, "notes.txt"), []byte("ignored"), 0o600); err != nil {
		t.Fatal(err)
	}

	s := NewStore(dir)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	list := s.List()
	if len(list) != 1 || list[0].ID != valid.ID {
		t.Fatalf("list = %+v, want only the valid conversation", list)
	}
	for _, name := range []string{"c-22222222.json", "c-33333333.json", "c-44444444.json",
		"c-55555555.json", "c-66666666.json"} {
		if _, err := os.Stat(filepath.Join(sessions, name)); err != nil {
			t.Errorf("skipped file %s was removed: %v", name, err)
		}
	}
}

func TestStoreAppendEnforcesLimit(t *testing.T) {
	s := newTestStore(t)
	conv := s.Create()
	for i := 0; i < MaxMessages; i++ {
		if _, err := s.Append(conv.ID, Message{Role: RoleUser, Status: StatusOK, At: int64(i + 1)}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	if _, err := s.Append(conv.ID, Message{Role: RoleUser, Status: StatusOK, At: 999}); err != ErrConversationFull {
		t.Errorf("append past the limit = %v, want ErrConversationFull", err)
	}
}

func TestStoreDropLastAssistant(t *testing.T) {
	s := newTestStore(t)
	conv := s.Create()
	if dropped, err := s.DropLastAssistant(conv.ID); err != nil || dropped {
		t.Fatalf("drop on empty = %v,%v", dropped, err)
	}
	if _, err := s.Append(conv.ID, Message{Role: RoleUser, Content: "hi", Status: StatusOK, At: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(conv.ID, Message{Role: RoleAssistant, Content: "hey", Status: StatusOK, At: 2}); err != nil {
		t.Fatal(err)
	}
	dropped, err := s.DropLastAssistant(conv.ID)
	if err != nil || !dropped {
		t.Fatalf("drop = %v,%v", dropped, err)
	}
	conv, _ = s.Get(conv.ID)
	if len(conv.Messages) != 1 || conv.Messages[0].Role != RoleUser {
		t.Errorf("messages = %+v", conv.Messages)
	}
}

func TestStoreFinishMessage(t *testing.T) {
	s := newTestStore(t)
	conv := s.Create()
	index, err := s.Append(conv.ID, Message{Role: RoleAssistant, Status: StatusStreaming, At: 5})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetContent(conv.ID, index, "部分"); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishMessage(conv.ID, index, Message{
		Role: RoleAssistant, Content: "部分回复", Status: StatusAborted, At: 6,
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(conv.ID)
	if got.Messages[0].Content != "部分回复" || got.Messages[0].Status != StatusAborted {
		t.Errorf("message = %+v", got.Messages[0])
	}
	if got.UpdatedAt != 6 {
		t.Errorf("updated_at = %d, want 6", got.UpdatedAt)
	}
}

func TestStoreInMemory(t *testing.T) {
	s := NewStore("")
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	conv := s.Create()
	if _, err := s.Append(conv.ID, Message{Role: RoleUser, Content: "hi", Status: StatusOK, At: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(conv.ID); err != nil {
		t.Errorf("in-memory save = %v", err)
	}
	if err := s.Delete(conv.ID); err != nil {
		t.Errorf("in-memory delete = %v", err)
	}
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestStoreCreateUsesClock(t *testing.T) {
	s := NewStore("")
	s.now = func() time.Time { return time.Unix(1790000000, 0) }
	conv := s.Create()
	if conv.CreatedAt != 1790000000 {
		t.Errorf("created_at = %d", conv.CreatedAt)
	}
	if !strings.HasPrefix(conv.ID, "c-") || len(conv.ID) != 10 {
		t.Errorf("ID = %q", conv.ID)
	}
	if _, err := s.Append(conv.ID, Message{Role: RoleUser, Content: "hi", Status: StatusOK}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(conv.ID)
	if got.Messages[0].At != 1790000000 {
		t.Errorf("message at = %d", got.Messages[0].At)
	}
	if err := s.Delete(conv.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := s.Save("missing"); err != ErrConversationNotFound {
		t.Errorf("save missing = %v", err)
	}
}
