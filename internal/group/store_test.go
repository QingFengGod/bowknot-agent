package group

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func openStore(t *testing.T, dir string) *Store {
	t.Helper()
	store := NewStore(filepath.Join(dir, "groups"), filepath.Join(dir, "runs"))
	if err := store.Load(); err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	return store
}

func TestStoreSaveLoad(t *testing.T) {
	dir := t.TempDir()
	store := openStore(t, dir)

	g := testGroup()
	if err := store.SaveGroup(g); err != nil {
		t.Fatalf("SaveGroup() error: %v", err)
	}
	groupPath := filepath.Join(dir, "groups", g.ID, groupFileName)
	if _, err := os.Stat(groupPath); err != nil {
		t.Fatalf("群文件缺失: %v", err)
	}

	run := testRun(t, testTask("t-00000001"), testTask("t-00000002", "t-00000001"))
	if err := store.SaveRun(run); err != nil {
		t.Fatalf("SaveRun() error: %v", err)
	}
	runPath := filepath.Join(dir, "runs", run.ID+runExt)
	if _, err := os.Stat(runPath); err != nil {
		t.Fatalf("Run 文件缺失: %v", err)
	}

	reloaded := openStore(t, dir)
	gotGroup, ok := reloaded.GetGroup(g.ID)
	if !ok || gotGroup.Name != g.Name || len(gotGroup.Members) != 2 {
		t.Fatalf("群未正确重载: %+v", gotGroup)
	}
	gotRun, ok := reloaded.GetRun(run.ID)
	if !ok || len(gotRun.Plan.Tasks) != 2 || gotRun.Plan.Tasks[1].DependsOn[0] != "t-00000001" {
		t.Fatalf("Run 未正确重载: %+v", gotRun)
	}
	if gotRun.GroupName != g.Name {
		t.Errorf("Run 群名快照 = %q, want %q", gotRun.GroupName, g.Name)
	}
}

func TestStoreLoadsAssistantRun(t *testing.T) {
	dir := t.TempDir()
	store := openStore(t, dir)
	run := NewRun(testTime(), "", "", "c-00000001", "助手目标")
	if err := run.SetPlan(testTime(), Plan{ProviderID: "p-00000001", Model: "m", Tasks: []Task{testTask("t-00000001")}}); err != nil {
		t.Fatalf("SetPlan() error: %v", err)
	}
	if err := store.SaveRun(run); err != nil {
		t.Fatalf("SaveRun() error: %v", err)
	}
	reloaded := openStore(t, dir)
	if _, ok := reloaded.GetRun(run.ID); !ok {
		t.Fatal("助手 Run 未重载")
	}
	if runs := reloaded.Runs(""); len(runs) != 1 || runs[0].ID != run.ID {
		t.Fatalf("Runs(\"\") = %+v", runs)
	}
}

func TestStoreSkipsInvalidFiles(t *testing.T) {
	dir := t.TempDir()
	store := openStore(t, dir)
	g := testGroup()
	if err := store.SaveGroup(g); err != nil {
		t.Fatalf("SaveGroup() error: %v", err)
	}
	run := testRun(t, testTask("t-00000001"))
	if err := store.SaveRun(run); err != nil {
		t.Fatalf("SaveRun() error: %v", err)
	}
	groupsDir := filepath.Join(dir, "groups")
	runsDir := filepath.Join(dir, "runs")
	for _, path := range []string{"gp-00000002", "gp-00000003"} {
		if err := os.MkdirAll(filepath.Join(groupsDir, path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	write(filepath.Join(groupsDir, "gp-00000002", groupFileName), "{not json")
	write(filepath.Join(groupsDir, "gp-00000003", groupFileName), `{"schema":9}`)
	write(filepath.Join(runsDir, "r-00000002.json"), "{not json")
	write(filepath.Join(runsDir, "r-00000003.json"), strings.Repeat("x", MaxRunFileBytes+1))
	write(filepath.Join(runsDir, "notes.txt"), "ignore")

	reloaded := openStore(t, dir)
	if reloaded.GroupCount() != 1 {
		t.Errorf("GroupCount() = %d, want 1", reloaded.GroupCount())
	}
	if runs := reloaded.AllRuns(); len(runs) != 1 || runs[0].ID != run.ID {
		t.Errorf("AllRuns() = %+v, want only %s", runs, run.ID)
	}
	if _, err := os.Stat(filepath.Join(groupsDir, "gp-00000002", groupFileName)); err != nil {
		t.Errorf("损坏文件被删除: %v", err)
	}
}

func TestStoreDeleteGroupKeepsRuns(t *testing.T) {
	dir := t.TempDir()
	store := openStore(t, dir)
	g := testGroup()
	if err := store.SaveGroup(g); err != nil {
		t.Fatalf("SaveGroup() error: %v", err)
	}
	run := testRun(t, testTask("t-00000001"))
	if err := store.SaveRun(run); err != nil {
		t.Fatalf("SaveRun() error: %v", err)
	}

	if err := store.DeleteGroup(g.ID); err != nil {
		t.Fatalf("DeleteGroup() error: %v", err)
	}
	if _, ok := store.GetGroup(g.ID); ok {
		t.Fatal("群删除后仍可读取")
	}
	if _, err := os.Stat(filepath.Join(dir, "groups", g.ID)); !os.IsNotExist(err) {
		t.Errorf("群目录未删除: %v", err)
	}
	gotRun, ok := store.GetRun(run.ID)
	if !ok {
		t.Fatal("群删除后 Run 记录应保留")
	}
	if !gotRun.GroupDeleted || gotRun.GroupName != g.Name {
		t.Fatalf("Run 未标记群已删除: %+v", gotRun)
	}
	if gotRun.Status != RunCanceled {
		t.Errorf("群删除时运行中的 Run 状态 = %s, want canceled", gotRun.Status)
	}
	for i := range gotRun.Plan.Tasks {
		if gotRun.Plan.Tasks[i].Status != TaskCanceled {
			t.Errorf("群删除后任务 %d = %s, want canceled", i, gotRun.Plan.Tasks[i].Status)
		}
	}
	if runs := store.AllRuns(); len(runs) != 1 || !runs[0].GroupDeleted {
		t.Fatalf("AllRuns() = %+v", runs)
	}

	reloaded := openStore(t, dir)
	gotRun, ok = reloaded.GetRun(run.ID)
	if !ok || !gotRun.GroupDeleted {
		t.Fatalf("重载后 Run 应保留并标记群已删除: %+v, ok=%v", gotRun, ok)
	}
	if err := reloaded.DeleteRun(run.ID); err != nil {
		t.Fatalf("DeleteRun() error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "runs", run.ID+runExt)); !os.IsNotExist(err) {
		t.Errorf("Run 文件未删除: %v", err)
	}
}

func TestStoreRecoverInterrupted(t *testing.T) {
	dir := t.TempDir()
	store := openStore(t, dir)
	g := testGroup()
	if err := store.SaveGroup(g); err != nil {
		t.Fatalf("SaveGroup() error: %v", err)
	}
	run := testRun(t, testTask("t-00000001"), testTask("t-00000002"))
	run.Status = RunRunning
	run.Plan.Tasks[0].Status = TaskRunning
	run.Plan.Tasks[1].Status = TaskPending
	if err := store.SaveRun(run); err != nil {
		t.Fatalf("SaveRun() error: %v", err)
	}
	pending := testRun(t, testTask("t-00000003"))
	if err := store.SaveRun(pending); err != nil {
		t.Fatalf("SaveRun() error: %v", err)
	}

	reloaded := openStore(t, dir)
	got, ok := reloaded.GetRun(run.ID)
	if !ok {
		t.Fatal("Run 未重载")
	}
	if got.Status != RunInterrupted {
		t.Errorf("Run 状态 = %s, want interrupted", got.Status)
	}
	for i := range got.Plan.Tasks {
		if got.Plan.Tasks[i].Status != TaskInterrupted {
			t.Errorf("任务 %d = %s, want interrupted", i, got.Plan.Tasks[i].Status)
		}
	}
	gotPending, _ := reloaded.GetRun(pending.ID)
	if gotPending.Status != RunPendingConfirm {
		t.Errorf("待确认 Run 被改动: %s", gotPending.Status)
	}

	var onDisk Run
	data, err := os.ReadFile(filepath.Join(dir, "runs", run.ID+runExt))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &onDisk); err != nil {
		t.Fatal(err)
	}
	if onDisk.Status != RunInterrupted {
		t.Errorf("磁盘上的 Run 未被标记为已中断: %s", onDisk.Status)
	}
}

func TestStoreDeleteGroupTerminatesInterrupted(t *testing.T) {
	store := NewStore("", "")
	g := testGroup()
	if err := store.SaveGroup(g); err != nil {
		t.Fatalf("SaveGroup() error: %v", err)
	}
	run := testRun(t, testTask("t-00000001"), testTask("t-00000002"))
	run.Status = RunRunning
	run.Plan.Tasks[0].Status = TaskDone
	run.Plan.Tasks[1].Status = TaskInterrupted
	run.Status = RunInterrupted
	if err := store.SaveRun(run); err != nil {
		t.Fatalf("SaveRun() error: %v", err)
	}
	if err := store.DeleteGroup(g.ID); err != nil {
		t.Fatalf("DeleteGroup() error: %v", err)
	}
	got, ok := store.GetRun(run.ID)
	if !ok {
		t.Fatal("Run 记录应保留")
	}
	if got.Status != RunCanceled || !got.GroupDeleted {
		t.Fatalf("已中断 Run = %s, deleted=%v", got.Status, got.GroupDeleted)
	}
	if got.Plan.Tasks[0].Status != TaskDone {
		t.Errorf("已完成任务被改动: %s", got.Plan.Tasks[0].Status)
	}
	if got.Plan.Tasks[1].Status != TaskCanceled {
		t.Errorf("已中断任务 = %s, want canceled", got.Plan.Tasks[1].Status)
	}
}

func TestStoreSummaryOrder(t *testing.T) {
	store := NewStore("", "")
	now := testTime()
	for i, id := range []string{"r-00000001", "r-00000002", "r-00000003"} {
		run := NewRun(now, "", "", "c-00000001", "目标")
		run.ID = id
		run.UpdatedAt = int64(1000 + i)
		if err := run.SetPlan(now, Plan{ProviderID: "p", Model: "m", Tasks: []Task{testTask("t-00000001")}}); err != nil {
			t.Fatalf("SetPlan() error: %v", err)
		}
		run.UpdatedAt = int64(1000 + i)
		if err := store.SaveRun(run); err != nil {
			t.Fatalf("SaveRun() error: %v", err)
		}
	}
	runs := store.AllRuns()
	if len(runs) != 3 || runs[0].ID != "r-00000003" || runs[2].ID != "r-00000001" {
		t.Fatalf("AllRuns() = %+v", runs)
	}
}
