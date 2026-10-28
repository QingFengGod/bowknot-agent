package task

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreSaveLoad(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	g := newGroupForTest(t, TaskDone, TaskFailed)
	g.Tasks[0].Report = &Report{Conclusion: "结论", Artifacts: []string{"清单"}}
	if err := store.Save(g); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	path := filepath.Join(dir, g.ID+taskExt)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("saved file missing: %v", err)
	}

	reloaded := NewStore(dir)
	if err := reloaded.Load(); err != nil {
		t.Fatalf("reload error: %v", err)
	}
	got, ok := reloaded.Get(g.ID)
	if !ok {
		t.Fatal("group not reloaded")
	}
	if len(got.Tasks) != 2 || got.Tasks[0].Report == nil || got.Tasks[0].Report.Conclusion != "结论" {
		t.Fatalf("round trip lost data: %+v", got)
	}
}

func TestStoreListOrder(t *testing.T) {
	store := NewStore("")
	for i, id := range []string{"g-00000001", "g-00000002", "g-00000003"} {
		g := newGroupForTest(t, TaskPending)
		g.ID = id
		g.UpdatedAt = int64(1000 + i)
		if err := store.Save(g); err != nil {
			t.Fatalf("Save() error: %v", err)
		}
	}
	list := store.List()
	if len(list) != 3 || list[0].ID != "g-00000003" || list[2].ID != "g-00000001" {
		t.Fatalf("List() = %+v", list)
	}
}

func TestStoreSkipsInvalidFiles(t *testing.T) {
	dir := t.TempDir()
	valid := newGroupForTest(t, TaskPending)
	if err := NewStore(dir).Save(valid); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("g-00000001.json", "{not json")
	write("g-00000002.json", `{"schema":9}`)
	write("g-00000003.json", strings.Repeat("x", MaxTaskFileBytes+1))
	write("notes.txt", "ignore")

	store := NewStore(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	list := store.List()
	if len(list) != 1 || list[0].ID != valid.ID {
		t.Fatalf("List() = %+v", list)
	}
	for _, name := range []string{"g-00000001.json", "g-00000002.json", "g-00000003.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("invalid file %s was deleted", name)
		}
	}
}

func TestStoreRecoversInterrupted(t *testing.T) {
	dir := t.TempDir()
	g := newGroupForTest(t, TaskRunning, TaskPending, TaskDone)
	g.Status = GroupRunning
	if err := NewStore(dir).Save(g); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	store := NewStore(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	got, ok := store.Get(g.ID)
	if !ok {
		t.Fatal("group missing")
	}
	if got.Status != GroupInterrupted {
		t.Fatalf("status = %s, want interrupted", got.Status)
	}
	if got.Tasks[0].Status != TaskInterrupted || got.Tasks[1].Status != TaskInterrupted {
		t.Fatalf("tasks = %s, %s", got.Tasks[0].Status, got.Tasks[1].Status)
	}
	if got.Tasks[2].Status != TaskDone {
		t.Fatalf("done task = %s", got.Tasks[2].Status)
	}

	again := NewStore(dir)
	if err := again.Load(); err != nil {
		t.Fatalf("reload error: %v", err)
	}
	if got, _ := again.Get(g.ID); got.Status != GroupInterrupted {
		t.Fatal("interrupted status was not persisted")
	}

	pending := newGroupForTest(t, TaskPending)
	pending.ID = "g-0000000a"
	if err := store.Save(pending); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	reloaded := NewStore(dir)
	if err := reloaded.Load(); err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if got, _ := reloaded.Get(pending.ID); got.Status != GroupPending {
		t.Fatalf("confirmable group = %s, want pending", got.Status)
	}
}

func TestStoreDelete(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	g := newGroupForTest(t, TaskPending)
	if err := store.Save(g); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	if err := store.Delete(g.ID); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}
	if _, ok := store.Get(g.ID); ok {
		t.Fatal("group still in memory")
	}
	if _, err := os.Stat(filepath.Join(dir, g.ID+taskExt)); !os.IsNotExist(err) {
		t.Fatal("group file still exists")
	}
	if err := store.Delete(g.ID); err != ErrGroupNotFound {
		t.Fatalf("Delete() = %v, want ErrGroupNotFound", err)
	}
}
