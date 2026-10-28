package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSubAgentStoreSaveAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), SubAgentsFileName)
	store := NewSubAgentStore(path)
	if err := store.Load(); err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	a, err := store.Save(SubAgent{Name: " 资料整理 ", Persona: " 整理材料\n输出要点 ", Enabled: true,
		ProviderID: "p-00000001", Model: "m1"})
	if err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	if !ValidSubAgentID(a.ID) || a.Name != "资料整理" || a.Persona != "整理材料\n输出要点" {
		t.Fatalf("saved = %+v", a)
	}
	if a.PersonaLine() != "整理材料" {
		t.Errorf("persona line = %q", a.PersonaLine())
	}

	reloaded := NewSubAgentStore(path)
	if err := reloaded.Load(); err != nil {
		t.Fatalf("reload error: %v", err)
	}
	got, ok := reloaded.Get(a.ID)
	if !ok || got.Name != "资料整理" || !got.Enabled {
		t.Fatalf("reloaded = %+v, ok = %v", got, ok)
	}
	if filepath.Base(path) != SubAgentsFileName {
		t.Errorf("file name = %q", filepath.Base(path))
	}
}

func TestSubAgentStoreUniqueName(t *testing.T) {
	store := NewSubAgentStore("")
	if _, err := store.Save(SubAgent{Name: "Writer", Persona: "写", Enabled: true,
		ProviderID: "p-00000001", Model: "m1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(SubAgent{Name: "writer", Persona: "写", Enabled: true,
		ProviderID: "p-00000001", Model: "m1"}); err != ErrSubAgentNameTaken {
		t.Fatalf("duplicate name error = %v", err)
	}
	existing := store.List()[0]
	updated, err := store.Save(SubAgent{ID: existing.ID, Name: "Writer", Persona: "改写",
		Enabled: false, ProviderID: "p-00000001", Model: "m1"})
	if err != nil {
		t.Fatalf("update error: %v", err)
	}
	if updated.CreatedAt != existing.CreatedAt || updated.Persona != "改写" {
		t.Fatalf("update = %+v", updated)
	}
}

func TestSubAgentStoreValidation(t *testing.T) {
	cases := []struct {
		name  string
		agent SubAgent
	}{
		{"empty name", SubAgent{Name: " ", Persona: "写", ProviderID: "p", Model: "m"}},
		{"long name", SubAgent{Name: strings.Repeat("名", MaxSubAgentNameRunes+1), Persona: "写",
			ProviderID: "p", Model: "m"}},
		{"empty persona", SubAgent{Name: "写手", Persona: " ", ProviderID: "p", Model: "m"}},
		{"long persona", SubAgent{Name: "写手", Persona: strings.Repeat("人", MaxPersonaRunes+1),
			ProviderID: "p", Model: "m"}},
		{"no model", SubAgent{Name: "写手", Persona: "人设", ProviderID: "p"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := NewSubAgentStore("")
			if _, err := store.Save(c.agent); err == nil {
				t.Fatal("invalid sub agent accepted")
			}
		})
	}
}

func TestSubAgentStoreLimit(t *testing.T) {
	store := NewSubAgentStore("")
	for i := 0; i < MaxSubAgents; i++ {
		name := strings.Repeat("x", i+1)
		if _, err := store.Save(SubAgent{Name: name, Persona: "人设", Enabled: true,
			ProviderID: "p-00000001", Model: "m1"}); err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
	}
	if _, err := store.Save(SubAgent{Name: strings.Repeat("y", MaxSubAgents+1), Persona: "人设",
		ProviderID: "p", Model: "m"}); err != ErrSubAgentLimit {
		t.Fatalf("limit error = %v", err)
	}
}

func TestSubAgentStoreEnableAndDelete(t *testing.T) {
	store := NewSubAgentStore("")
	a, err := store.Save(SubAgent{Name: "写手", Persona: "人设", ProviderID: "p", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetEnabled(a.ID, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.Get(a.ID); !got.Enabled {
		t.Fatal("SetEnabled did not persist the flag")
	}
	if err := store.Delete(a.ID); err != nil {
		t.Fatal(err)
	}
	if store.Count() != 0 {
		t.Fatal("Count after delete = ", store.Count())
	}
	if err := store.Delete(a.ID); err != ErrSubAgentNotFound {
		t.Fatalf("second delete = %v", err)
	}
	if err := store.SetEnabled(a.ID, true); err != ErrSubAgentNotFound {
		t.Fatalf("enable of deleted = %v", err)
	}
}

func TestSubAgentStoreCorruptFileIsKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), SubAgentsFileName)
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewSubAgentStore(path)
	if err := store.Load(); err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if !store.Corrupt() {
		t.Error("corrupt flag not set")
	}
	if store.Count() != 0 {
		t.Errorf("entries = %d, want 0", store.Count())
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("corrupt file was deleted: %v", err)
	}
}
