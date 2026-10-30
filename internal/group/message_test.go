package group

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func openMessageStore(t *testing.T, dir string) *Store {
	t.Helper()
	store := openStore(t, dir)
	run := testRun(t, testTask("t-00000001"))
	if err := store.SaveRun(run); err != nil {
		t.Fatalf("SaveRun() error: %v", err)
	}
	return store
}

func groupRunID(t *testing.T, store *Store) string {
	t.Helper()
	runs := store.AllRuns()
	if len(runs) != 1 {
		t.Fatalf("AllRuns() = %d 个", len(runs))
	}
	return runs[0].ID
}

func TestAppendMessagePersistsAndOrders(t *testing.T) {
	dir := t.TempDir()
	store := openMessageStore(t, dir)
	runID := groupRunID(t, store)

	first, err := store.AppendMessage(Message{
		RunID:   runID,
		TaskID:  "t-00000001",
		FromID:  "a-00000001",
		Type:    MessageProgress,
		Content: "开始整理",
	})
	if err != nil {
		t.Fatalf("AppendMessage() error: %v", err)
	}
	if first.ID == "" || first.Seq != 1 {
		t.Fatalf("首条消息 = %+v", first)
	}
	second, err := store.AppendMessage(Message{
		RunID:   runID,
		FromID:  MainAgentID,
		ToID:    "a-00000001",
		Type:    MessageRequest,
		Content: "补充来源",
	})
	if err != nil {
		t.Fatalf("AppendMessage() error: %v", err)
	}
	if second.Seq != 2 {
		t.Fatalf("第二条序号 = %d, want 2", second.Seq)
	}

	path := filepath.Join(dir, "groups", "gp-00000001", messageDirName, runID+messageExt)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("消息文件缺失: %v", err)
	}

	reloaded := openStore(t, dir)
	got := reloaded.Messages(runID)
	if len(got) != 2 || got[0].ID != first.ID || got[1].ID != second.ID {
		t.Fatalf("重载消息 = %+v", got)
	}
	if got[0].Seq != 1 || got[1].Seq != 2 {
		t.Fatalf("重载序号 = %d/%d", got[0].Seq, got[1].Seq)
	}
	if reloaded.LastSeq(runID) != 2 {
		t.Errorf("LastSeq() = %d", reloaded.LastSeq(runID))
	}
}

func TestAppendMessageValidates(t *testing.T) {
	dir := t.TempDir()
	store := openMessageStore(t, dir)
	runID := groupRunID(t, store)

	cases := []struct {
		name string
		msg  Message
		want string
	}{
		{
			name: "未知类型",
			msg:  Message{RunID: runID, FromID: "a-00000001", Type: "chat", Content: "x"},
			want: "类型",
		},
		{
			name: "请求缺少接收者",
			msg:  Message{RunID: runID, FromID: "a-00000001", Type: MessageRequest, Content: "x"},
			want: "接收者",
		},
		{
			name: "答复缺少原请求",
			msg:  Message{RunID: runID, FromID: "a-00000001", ToID: MainAgentID, Type: MessageResponse, Content: "x"},
			want: "答复",
		},
		{
			name: "正文为空",
			msg:  Message{RunID: runID, FromID: "a-00000001", Type: MessageProgress, Content: " "},
			want: "正文",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := store.AppendMessage(tc.msg); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("AppendMessage() error = %v, want 包含 %q", err, tc.want)
			}
		})
	}
	if _, err := store.AppendMessage(Message{RunID: "r-00000009", FromID: "a-00000001", Type: MessageProgress, Content: "x"}); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("未知 Run error = %v", err)
	}
}

func TestAppendMessageRespectsBudget(t *testing.T) {
	dir := t.TempDir()
	store := openMessageStore(t, dir)
	runID := groupRunID(t, store)
	run, ok := store.GetRun(runID)
	if !ok {
		t.Fatal("Run 缺失")
	}
	run.Budget.MaxMessages = 2
	if err := store.SaveRun(&run); err != nil {
		t.Fatalf("SaveRun() error: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := store.AppendMessage(Message{RunID: runID, FromID: "a-00000001", Type: MessageProgress, Content: "进展"}); err != nil {
			t.Fatalf("第 %d 条 error: %v", i+1, err)
		}
	}
	if _, err := store.AppendMessage(Message{RunID: runID, FromID: "a-00000001", Type: MessageProgress, Content: "进展"}); !errors.Is(err, ErrMessageLimit) {
		t.Fatalf("超限 error = %v, want ErrMessageLimit", err)
	}
	if got := store.MessageCount(runID); got != 2 {
		t.Fatalf("MessageCount() = %d", got)
	}
	if got := store.MessageLimit(runID); got != 2 {
		t.Errorf("MessageLimit() = %d", got)
	}
}

func TestAllMessagesVisibleIncludingDirected(t *testing.T) {
	dir := t.TempDir()
	store := openMessageStore(t, dir)
	runID := groupRunID(t, store)

	req, err := store.AppendMessage(Message{
		RunID: runID, FromID: "a-00000001", ToID: "a-00000002",
		Type: MessageRequest, Content: "能补充来源吗",
	})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := store.AppendMessage(Message{
		RunID: runID, FromID: "a-00000002", ToID: "a-00000001",
		Type: MessageResponse, Content: "来源在附录", ReplyTo: req.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	msgs := store.Messages(runID)
	if len(msgs) != 2 {
		t.Fatalf("消息数量 = %d", len(msgs))
	}
	if msgs[0].ToID != "a-00000002" || msgs[1].ReplyTo != req.ID {
		t.Fatalf("定向消息未完整保留: %+v", msgs)
	}
	if resp.Seq != 2 {
		t.Errorf("答复序号 = %d", resp.Seq)
	}
}

func TestDeleteRunDeletesMessages(t *testing.T) {
	dir := t.TempDir()
	store := openMessageStore(t, dir)
	runID := groupRunID(t, store)
	if _, err := store.AppendMessage(Message{RunID: runID, FromID: "a-00000001", Type: MessageProgress, Content: "进展"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "groups", "gp-00000001", messageDirName, runID+messageExt)
	if err := store.DeleteRun(runID); err != nil {
		t.Fatalf("DeleteRun() error: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("消息文件未删除: %v", err)
	}
	if got := store.Messages(runID); len(got) != 0 {
		t.Errorf("内存消息未删除: %+v", got)
	}
}

func TestAssistantRunMessageFile(t *testing.T) {
	dir := t.TempDir()
	store := openStore(t, dir)
	run := NewRun(testTime(), "", "", "c-00000001", "助手目标")
	if err := run.SetPlan(testTime(), Plan{ProviderID: "p-00000001", Model: "m", Tasks: []Task{testTask("t-00000001")}}); err != nil {
		t.Fatalf("SetPlan() error: %v", err)
	}
	if err := store.SaveRun(run); err != nil {
		t.Fatalf("SaveRun() error: %v", err)
	}
	if _, err := store.AppendMessage(Message{RunID: run.ID, FromID: "a-00000001", Type: MessageProgress, Content: "进展"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "runs", run.ID+messageExt)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("助手消息文件缺失: %v", err)
	}
	reloaded := openStore(t, dir)
	if len(reloaded.Messages(run.ID)) != 1 {
		t.Fatalf("重载消息 = %+v", reloaded.Messages(run.ID))
	}
}

func TestMessageWriteFailureKeepsMemory(t *testing.T) {
	dir := t.TempDir()
	store := openMessageStore(t, dir)
	runID := groupRunID(t, store)
	// A file where the messages directory belongs makes every append fail.
	messagesDir := filepath.Join(dir, "groups", "gp-00000001", messageDirName)
	if err := os.MkdirAll(filepath.Dir(messagesDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(messagesDir, []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}

	msg, err := store.AppendMessage(Message{RunID: runID, FromID: "a-00000001", Type: MessageProgress, Content: "进展"})
	if !errors.Is(err, ErrMessageWrite) {
		t.Fatalf("AppendMessage() error = %v, want ErrMessageWrite", err)
	}
	if msg.ID == "" || msg.Seq != 1 {
		t.Fatalf("失败后返回的消息 = %+v", msg)
	}
	if got := store.MessageCount(runID); got != 1 {
		t.Fatalf("内存消息数量 = %d, want 1", got)
	}
	if got := store.Messages(runID); len(got) != 1 || got[0].ID != msg.ID {
		t.Fatalf("内存消息 = %+v", got)
	}
}

func TestMessageFileSkipsCorruptLines(t *testing.T) {
	dir := t.TempDir()
	store := openMessageStore(t, dir)
	runID := groupRunID(t, store)
	path := filepath.Join(dir, "groups", "gp-00000001", messageDirName, runID+messageExt)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"id":"m-00000001","run_id":"` + runID + `","from_id":"a-00000001","type":"progress","content":"ok","seq":1,"at":1700000000}` + "\n" +
		"{not json\n" +
		`{"id":"m-00000002","run_id":"` + runID + `","from_id":"a-00000001","type":"unknown","content":"x","seq":2,"at":1700000000}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	reloaded := openStore(t, dir)
	if got := reloaded.Messages(runID); len(got) != 1 || got[0].ID != "m-00000001" {
		t.Fatalf("Messages() = %+v", got)
	}
	if reloaded.LastSeq(runID) != 1 {
		t.Errorf("LastSeq() = %d", reloaded.LastSeq(runID))
	}
}
