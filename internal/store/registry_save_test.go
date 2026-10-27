package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveRegistryRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.json")
	r := NewRegistry(
		AppInfo{ID: "docs", Name: "文档工作台", Status: StatusInstalled, LastOpened: 9},
	)
	if err := SaveRegistry(path, r); err != nil {
		t.Fatalf("SaveRegistry: %v", err)
	}
	loaded, err := LoadRegistry(path)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	app, ok := loaded.Get("docs")
	if !ok || app.LastOpened != 9 {
		t.Errorf("app = %+v, ok = %v", app, ok)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "registry.json" {
			t.Errorf("leftover file %q", entry.Name())
		}
	}
}

func TestSaveRegistryCreatesDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "registry.json")
	if err := SaveRegistry(path, NewRegistry()); err != nil {
		t.Fatalf("SaveRegistry: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("registry file missing: %v", err)
	}
}

func TestRegistrySaveWithoutPath(t *testing.T) {
	if err := NewRegistry().Save(); err == nil {
		t.Fatal("expected Save without a path to fail")
	}
}

func TestRegistryNormalize(t *testing.T) {
	r := NewRegistry(
		AppInfo{ID: "a", Name: "运行", Status: StatusRunning},
		AppInfo{ID: "b", Name: "更新", Status: StatusUpdating},
		AppInfo{ID: "c", Name: "安装", Status: StatusInstalling},
		AppInfo{ID: "d", Name: "损坏", Status: StatusCorrupt},
	)
	if !r.Normalize() {
		t.Fatal("Normalize reported no change")
	}
	want := map[string]Status{
		"a": StatusInstalled, "b": StatusInstalled,
		"c": StatusInstalled, "d": StatusCorrupt,
	}
	for _, app := range r.Apps() {
		if app.Status != want[app.ID] {
			t.Errorf("%s status = %q, want %q", app.ID, app.Status, want[app.ID])
		}
	}
	if r.Normalize() {
		t.Error("Normalize reported a change on the second call")
	}
}

func TestRegistryVerifyPrograms(t *testing.T) {
	appsDir := t.TempDir()
	r := NewRegistry(
		AppInfo{ID: "docs", Name: "文档", Status: StatusInstalled},
		AppInfo{ID: "audio", Name: "音频", Status: StatusInstalled},
	)
	writeAppFiles(t, filepath.Join(appsDir, "docs"), validManifest(), 16)
	if !r.VerifyPrograms(appsDir, "0.1.0") {
		t.Fatal("VerifyPrograms reported no change")
	}
	if app, _ := r.Get("docs"); app.Status != StatusInstalled {
		t.Errorf("docs status = %q, want installed", app.Status)
	}
	if app, _ := r.Get("audio"); app.Status != StatusCorrupt {
		t.Errorf("audio status = %q, want corrupt", app.Status)
	}
}

func TestRegistrySetStatusAndTouch(t *testing.T) {
	r := NewRegistry(AppInfo{ID: "docs", Name: "文档", Status: StatusInstalled})
	if !r.SetStatus("docs", StatusDisabled) {
		t.Fatal("SetStatus did not find the app")
	}
	if !r.Touch("docs", 42) {
		t.Fatal("Touch did not find the app")
	}
	app, _ := r.Get("docs")
	if app.Status != StatusDisabled || app.LastOpened != 42 {
		t.Errorf("app = %+v", app)
	}
	if r.SetStatus("missing", StatusInstalled) {
		t.Error("SetStatus found an unknown app")
	}
	if !r.Remove("docs") || r.Remove("docs") {
		t.Error("Remove did not report presence correctly")
	}
}
