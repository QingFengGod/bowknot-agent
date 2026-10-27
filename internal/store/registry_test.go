package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestNewRegistry(t *testing.T) {
	r := NewRegistry(
		AppInfo{ID: " docs ", Name: " 文档 "},
		AppInfo{ID: "broken", Name: "损坏", Status: "unknown"},
		AppInfo{ID: "", Name: "无ID"},
		AppInfo{ID: "noname", Name: "  "},
		AppInfo{ID: "fresh", Name: "未安装", Status: StatusNotInstalled},
	)
	if r.Len() != 2 {
		t.Fatalf("Len() = %d, want 2", r.Len())
	}
	apps := r.Apps()
	if apps[0].ID != "docs" || apps[0].Name != "文档" {
		t.Error("registry did not trim the first app")
	}
	if apps[1].Status != StatusCorrupt {
		t.Errorf("unknown status = %q, want corrupt", apps[1].Status)
	}
}

func TestLoadRegistryMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	r, err := LoadRegistry(path)
	if err != nil {
		t.Fatalf("LoadRegistry() error: %v", err)
	}
	if r.Len() != 0 {
		t.Errorf("Len() = %d, want 0", r.Len())
	}
}

func TestLoadRegistryInvalidReturnsErrorAndEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	if err := os.WriteFile(path, []byte("{ nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := LoadRegistry(path)
	if err == nil {
		t.Fatal("LoadRegistry() returned no error for invalid JSON")
	}
	if r.Len() != 0 {
		t.Errorf("Len() = %d, want 0", r.Len())
	}
}

func TestLoadRegistryRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.json")
	input := registryFile{
		Apps: []AppInfo{
			{ID: "docs", Name: "文档工作台", Version: "1.0", Status: StatusInstalled, LastOpened: 7},
			{ID: "audio", Name: "音频剪辑", Status: StatusRunning},
		},
	}
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := LoadRegistry(path)
	if err != nil {
		t.Fatalf("LoadRegistry() error: %v", err)
	}
	apps := r.Apps()
	if len(apps) != 2 {
		t.Fatalf("Len() = %d, want 2", len(apps))
	}
	if apps[0].LastOpened != 7 {
		t.Errorf("LastOpened = %d, want 7", apps[0].LastOpened)
	}
	if apps[1].Status != StatusRunning {
		t.Errorf("Status = %q, want running", apps[1].Status)
	}
}
