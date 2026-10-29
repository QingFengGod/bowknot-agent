package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestRegistry(t *testing.T) (*Registry, string) {
	t.Helper()
	root := t.TempDir()
	r := NewRegistry(root)
	if err := r.Load(); err != nil {
		t.Fatal(err)
	}
	return r, root
}

func TestRegistryLoadCreatesMainWorkspace(t *testing.T) {
	r, root := newTestRegistry(t)
	list := r.List()
	if len(list) != 1 {
		t.Fatalf("agents = %+v, want only the main agent", list)
	}
	main := list[0]
	if main.ID != MainAgentID || main.Name != DefaultMainAgentName || !main.Enabled {
		t.Errorf("main agent = %+v", main)
	}
	if !ValidAgentID(main.ID) {
		t.Errorf("main agent ID %q is invalid", main.ID)
	}
	ws := NewWorkspace(root, MainAgentID)
	for _, path := range []string{
		ws.Path(AgentsFileName),
		ws.Path(PersonaFileName),
		ws.Path(UserFileName),
		ws.Path(PluginsFileName),
		ws.Path(AgentMetaFileName),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("main workspace file %s is missing: %v", path, err)
		}
	}
	for _, dir := range []string{ws.SessionsDir(), ws.MemoriesDir()} {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("main workspace directory %s is missing: %v", dir, err)
		}
	}
}

func TestRegistryCreateUpdateDelete(t *testing.T) {
	r, root := newTestRegistry(t)
	created, err := r.Create(Agent{Name: "研究员", ProviderID: "p", Model: "m", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if !ValidSubAgentID(created.ID) || created.CreatedAt == 0 || created.UpdatedAt == 0 {
		t.Errorf("created agent = %+v", created)
	}
	if got := r.Count(); got != 1 {
		t.Errorf("Count = %d, want 1", got)
	}
	if _, ok := r.Get(created.ID); !ok {
		t.Error("created agent is not found")
	}
	if _, err := r.Create(Agent{Name: "研究员", Enabled: true}); !errors.Is(err, ErrAgentNameTaken) {
		t.Errorf("duplicate name error = %v, want ErrAgentNameTaken", err)
	}
	if _, err := r.Create(Agent{Name: DefaultMainAgentName, Enabled: true}); !errors.Is(err, ErrAgentNameTaken) {
		t.Errorf("main name error = %v, want ErrAgentNameTaken", err)
	}

	created.Name = "分析师"
	created.Model = "m2"
	updated, err := r.Update(created)
	if err != nil {
		t.Fatal(err)
	}
	if updated.CreatedAt != created.CreatedAt || updated.UpdatedAt < created.UpdatedAt {
		t.Errorf("updated timestamps = %+v", updated)
	}
	if err := r.SetEnabled(created.ID, false); err != nil {
		t.Fatal(err)
	}

	reloaded := NewRegistry(root)
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	again, ok := reloaded.Get(created.ID)
	if !ok {
		t.Fatal("reloaded agent is missing")
	}
	if again.Name != "分析师" || again.Model != "m2" || again.Enabled {
		t.Errorf("reloaded agent = %+v", again)
	}
	if list := reloaded.List(); len(list) != 2 || list[0].ID != MainAgentID {
		t.Errorf("reloaded list = %+v", list)
	}

	if err := r.Delete(created.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Get(created.ID); ok {
		t.Error("deleted agent is still in memory")
	}
	if _, err := os.Stat(NewWorkspace(root, created.ID).Dir()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("deleted workspace still exists: %v", err)
	}
}

func TestRegistryProtectsMainAgent(t *testing.T) {
	r, _ := newTestRegistry(t)
	if err := r.Delete(MainAgentID); !errors.Is(err, ErrMainAgentDelete) {
		t.Errorf("delete main error = %v, want ErrMainAgentDelete", err)
	}
	if err := r.SetEnabled(MainAgentID, false); !errors.Is(err, ErrMainAgentDisabled) {
		t.Errorf("disable main error = %v, want ErrMainAgentDisabled", err)
	}
	main := r.Main()
	main.Name = "我的助手"
	updated, err := r.Update(main)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Enabled {
		t.Error("main agent was disabled by Update")
	}
	if _, err := r.Create(Agent{Name: "我的助手"}); !errors.Is(err, ErrAgentNameTaken) {
		t.Errorf("renamed main name is not reserved: %v", err)
	}
}

func TestRegistrySubAgentLimit(t *testing.T) {
	r, _ := newTestRegistry(t)
	for i := 0; i < MaxSubAgents; i++ {
		name := fmt.Sprintf("子 Agent %d", i+1)
		if _, err := r.Create(Agent{Name: name, Enabled: true}); err != nil {
			t.Fatalf("create %d: %v", i+1, err)
		}
	}
	if got := r.Count(); got != MaxSubAgents {
		t.Errorf("Count = %d, want %d", got, MaxSubAgents)
	}
	if _, err := r.Create(Agent{Name: "多余", Enabled: true}); !errors.Is(err, ErrAgentLimit) {
		t.Errorf("over limit error = %v, want ErrAgentLimit", err)
	}
}

func TestRegistryValidAgentIDs(t *testing.T) {
	cases := []struct {
		id string
		ok bool
	}{
		{MainAgentID, true},
		{"a-12345678", true},
		{"a-1234567", false},
		{"a-123456789", false},
		{"b-12345678", false},
		{"main-extra", false},
	}
	for _, c := range cases {
		if got := ValidAgentID(c.id); got != c.ok {
			t.Errorf("ValidAgentID(%q) = %v, want %v", c.id, got, c.ok)
		}
	}
	if id := newAgentID(); !ValidSubAgentID(id) {
		t.Errorf("newAgentID() = %q, want a sub agent ID", id)
	}
}

func TestRegistryRebuildsMissingMain(t *testing.T) {
	_, root := newTestRegistry(t)
	ws := NewWorkspace(root, MainAgentID)
	if err := ws.Save(PersonaFileName, "# 保留的人设\n"); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(ws.Dir()); err != nil {
		t.Fatal(err)
	}

	reloaded := NewRegistry(root)
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	main := reloaded.Main()
	if main.ID != MainAgentID || main.Name != DefaultMainAgentName {
		t.Errorf("rebuilt main = %+v", main)
	}
	data, err := os.ReadFile(ws.Path(PersonaFileName))
	if err != nil {
		t.Fatalf("default persona was not recreated: %v", err)
	}
	if string(data) != DefaultPersonaMD {
		t.Errorf("recreated persona = %q", data)
	}

	// A corrupt metadata file is rebuilt while the workspace text is kept.
	if err := ws.Save(PersonaFileName, "# 保留的人设\n"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ws.Path(AgentMetaFileName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	reloaded = NewRegistry(root)
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Main(); got.ID != MainAgentID {
		t.Errorf("main after corrupt metadata = %+v", got)
	}
	data, err = os.ReadFile(ws.Path(PersonaFileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "# 保留的人设\n" {
		t.Errorf("main persona was overwritten: %q", data)
	}
}

func TestRegistrySkipsCorruptSubAgents(t *testing.T) {
	r, root := newTestRegistry(t)
	good, err := r.Create(Agent{Name: "可用", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	badID := "a-00000001"
	badDir := NewWorkspace(root, badID)
	if err := os.MkdirAll(badDir.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(badDir.Path(AgentMetaFileName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	otherID := "a-00000002"
	otherDir := NewWorkspace(root, otherID)
	if err := os.MkdirAll(otherDir.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(otherDir.Path(AgentMetaFileName),
		[]byte(`{"schema":1,"agent":{"id":"a-00000002","name":"","created_at":1,"updated_at":1}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	reloaded := NewRegistry(root)
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	list := reloaded.List()
	if len(list) != 2 || list[0].ID != MainAgentID || list[1].ID != good.ID {
		t.Errorf("list = %+v, want main and the valid sub agent", list)
	}
	if _, ok := reloaded.Get(badID); ok {
		t.Error("corrupt sub agent was loaded")
	}
	if _, ok := reloaded.Get(otherID); ok {
		t.Error("sub agent with an empty name was loaded")
	}
	for _, ws := range []*Workspace{badDir, otherDir} {
		if _, err := os.Stat(ws.Path(AgentMetaFileName)); err != nil {
			t.Errorf("skipped metadata was deleted: %v", err)
		}
	}
}

func TestRegistryIgnoresLegacyAgentData(t *testing.T) {
	parent := t.TempDir()
	legacy := filepath.Join(parent, "agent")
	if err := os.MkdirAll(filepath.Join(legacy, "conversations"), 0o700); err != nil {
		t.Fatal(err)
	}
	legacySubs := filepath.Join(legacy, "subagents.json")
	if err := os.WriteFile(legacySubs, []byte(`{"schema":1,"agents":[{"id":"a-12345678"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(parent, "agents")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	// A stale subagents.json directly in the agents root must be ignored too.
	if err := os.WriteFile(filepath.Join(root, "subagents.json"), []byte(`{"schema":1}`), 0o600); err != nil {
		t.Fatal(err)
	}

	r := NewRegistry(root)
	if err := r.Load(); err != nil {
		t.Fatal(err)
	}
	if list := r.List(); len(list) != 1 || list[0].ID != MainAgentID {
		t.Errorf("list = %+v, want only the main agent", list)
	}
	if _, err := os.Stat(legacySubs); err != nil {
		t.Errorf("legacy subagents.json was touched: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "subagents.json")); err != nil {
		t.Errorf("stale agents/subagents.json was touched: %v", err)
	}
}

func TestRegistryMainAndSubOrder(t *testing.T) {
	r, _ := newTestRegistry(t)
	base := time.Unix(1000, 0)
	r.now = func() time.Time {
		base = base.Add(time.Second)
		return base
	}
	first, err := r.Create(Agent{Name: "先创建", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.Create(Agent{Name: "后创建", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	list := r.List()
	if len(list) != 3 {
		t.Fatalf("list = %+v", list)
	}
	if list[0].ID != MainAgentID {
		t.Error("main agent is not first")
	}
	if list[1].ID != first.ID || list[2].ID != second.ID {
		t.Errorf("sub agent order = %s, %s; want creation order", list[1].ID, list[2].ID)
	}
}
