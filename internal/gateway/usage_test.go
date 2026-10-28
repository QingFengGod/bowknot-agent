package gateway

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUsageAppendRecentCount(t *testing.T) {
	log := NewUsageLog(t.TempDir())
	for i := 0; i < 3; i++ {
		rec := UsageRecord{Time: int64(100 + i), Origin: "settings", Capability: CapabilityChat,
			Status: UsageOK}
		if err := log.Append(rec); err != nil {
			t.Fatal(err)
		}
	}
	count, err := log.Count()
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Errorf("count = %d, want 3", count)
	}
	recent, err := log.Recent(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 2 {
		t.Fatalf("recent = %d, want 2", len(recent))
	}
	if recent[0].Time != 102 || recent[1].Time != 101 {
		t.Errorf("recent times = %d,%d, want 102,101", recent[0].Time, recent[1].Time)
	}
}

func TestUsageRecordsConversationID(t *testing.T) {
	log := NewUsageLog(t.TempDir())
	if err := log.Append(UsageRecord{Time: 1, Origin: "agent", ConversationID: "c-1a2b3c4d",
		TaskID: "t-1a2b3c4d", Capability: CapabilityChat, Status: UsageOK}); err != nil {
		t.Fatal(err)
	}
	// A record written before 007 has no conversation_id and no task_id, and
	// must still read.
	legacy := fmt.Sprintf("{\"time\":2,\"origin\":\"settings\",\"status\":\"ok\"}\n")
	f, err := os.OpenFile(log.Path(), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(legacy); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	recent, err := log.Recent(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 2 {
		t.Fatalf("recent = %d, want 2", len(recent))
	}
	if recent[0].ConversationID != "" || recent[0].TaskID != "" {
		t.Errorf("legacy record = %+v, want empty IDs", recent[0])
	}
	if recent[1].ConversationID != "c-1a2b3c4d" || recent[1].TaskID != "t-1a2b3c4d" {
		t.Errorf("record = %+v", recent[1])
	}
}

func TestUsageRotatesAndPrunes(t *testing.T) {
	dir := t.TempDir()
	log := NewUsageLog(dir)
	log.maxBytes = 256
	log.keep = 2
	var tick int64 = 1000
	log.now = func() time.Time {
		tick += 10
		return time.Unix(tick, 0)
	}
	for i := 0; i < 20; i++ {
		rec := UsageRecord{Time: int64(1000 + i), Origin: "settings", Model: "m"}
		if err := log.Append(rec); err != nil {
			t.Fatal(err)
		}
	}
	files, err := filepath.Glob(filepath.Join(dir, "usage-*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) > log.keep {
		t.Errorf("rotated files = %d, want at most %d", len(files), log.keep)
	}
	if _, err := os.Stat(filepath.Join(dir, UsageFileName)); err != nil {
		t.Errorf("current usage file missing: %v", err)
	}
}

func TestUsageRecentMissingFile(t *testing.T) {
	log := NewUsageLog(t.TempDir())
	recent, err := log.Recent(5)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 0 {
		t.Errorf("recent = %+v, want none", recent)
	}
	count, err := log.Count()
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
}

func TestUsageSkipsDamagedLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, UsageFileName)
	content := fmt.Sprintf("{\"time\":1,\"origin\":\"settings\"}\nnot json\n{\"time\":2,\"origin\":\"settings\"}\n")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	log := NewUsageLog(dir)
	count, err := log.Count()
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
	recent, err := log.Recent(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 2 || recent[0].Time != 2 {
		t.Errorf("recent = %+v", recent)
	}
}
