package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newPluginRegistry(t *testing.T) *Registry {
	t.Helper()
	r := NewRegistry(t.TempDir())
	if err := r.Load(); err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	return r
}

func TestEnabledPluginsDefault(t *testing.T) {
	r := newPluginRegistry(t)
	if got := r.EnabledPlugins(MainAgentID); len(got) != 0 {
		t.Errorf("EnabledPlugins() = %v, want empty", got)
	}
}

func TestSetEnabledPluginsRoundTrip(t *testing.T) {
	r := newPluginRegistry(t)
	if err := r.SetEnabledPlugins(MainAgentID, []string{"weekly-report", " note-keeper ", "", "weekly-report"}); err != nil {
		t.Fatalf("SetEnabledPlugins() error: %v", err)
	}
	got := r.EnabledPlugins(MainAgentID)
	if len(got) != 2 || got[0] != "weekly-report" || got[1] != "note-keeper" {
		t.Errorf("EnabledPlugins() = %v, want order-preserving dedupe", got)
	}
	// The file keeps the schema and the default workspace files stay.
	data, err := os.ReadFile(r.Workspace(MainAgentID).Path(PluginsFileName))
	if err != nil || !strings.Contains(string(data), `"schema":1`) {
		t.Fatalf("plugins.json = %q, err %v", data, err)
	}
}

func TestEnabledPluginsCorruptFile(t *testing.T) {
	r := newPluginRegistry(t)
	path := r.Workspace(MainAgentID).Path(PluginsFileName)
	if err := os.WriteFile(path, []byte("{corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := r.EnabledPlugins(MainAgentID); len(got) != 0 {
		t.Errorf("EnabledPlugins() = %v, want empty", got)
	}
	// The corrupt file is kept, never silently rewritten.
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "{corrupt" {
		t.Fatalf("corrupt file changed: %q, err %v", data, err)
	}
}

func TestSetEnabledPluginsSubAgent(t *testing.T) {
	r := newPluginRegistry(t)
	a, err := r.Create(Agent{Name: "整理员", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SetEnabledPlugins(a.ID, []string{"weekly-report"}); err != nil {
		t.Fatalf("SetEnabledPlugins() error: %v", err)
	}
	if got := r.EnabledPlugins(a.ID); len(got) != 1 || got[0] != "weekly-report" {
		t.Errorf("EnabledPlugins() = %v", got)
	}
	// The main agent list stays isolated.
	if got := r.EnabledPlugins(MainAgentID); len(got) != 0 {
		t.Errorf("main agent list = %v, want empty", got)
	}
}

type fakeSkills struct {
	section string
	notice  string
	agents  map[string]string
}

func (f fakeSkills) SkillSection(agentID string) (string, string) {
	if f.agents != nil {
		return f.agents[agentID], f.notice
	}
	return f.section, f.notice
}

func TestAgentMemoryPromptIncludesSkills(t *testing.T) {
	r := newPluginRegistry(t)
	r.Skills = fakeSkills{section: "## 插件技能\n技能内容", notice: "插件 已失效 已从启用清单移除"}
	prompt, notice := agentMemoryPrompt(r, nil, MainAgentID, "", "", "查询")
	if !strings.Contains(prompt, "## 插件技能") || !strings.Contains(prompt, "技能内容") {
		t.Errorf("prompt missing the skill section:\n%s", prompt)
	}
	if notice != "插件 已失效 已从启用清单移除" {
		t.Errorf("notice = %q", notice)
	}
}

func TestAgentMemoryPromptWithoutSkills(t *testing.T) {
	r := newPluginRegistry(t)
	prompt, notice := agentMemoryPrompt(r, nil, MainAgentID, "", "", "查询")
	if strings.Contains(prompt, "插件技能") {
		t.Errorf("prompt should not contain a skill section:\n%s", prompt)
	}
	if notice != "" {
		t.Errorf("notice = %q, want empty", notice)
	}
}

func TestAgentMemoryPromptPerAgentSkills(t *testing.T) {
	r := newPluginRegistry(t)
	a, err := r.Create(Agent{Name: "整理员", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	r.Skills = fakeSkills{agents: map[string]string{
		MainAgentID: "主 Agent 技能",
		a.ID:        "子 Agent 技能",
	}}
	mainPrompt, _ := agentMemoryPrompt(r, nil, MainAgentID, "", "", "")
	subPrompt, _ := agentMemoryPrompt(r, nil, a.ID, "", "", "")
	if !strings.Contains(mainPrompt, "主 Agent 技能") || strings.Contains(mainPrompt, "子 Agent 技能") {
		t.Errorf("main prompt wrong:\n%s", mainPrompt)
	}
	if !strings.Contains(subPrompt, "子 Agent 技能") || strings.Contains(subPrompt, "主 Agent 技能") {
		t.Errorf("sub prompt wrong:\n%s", subPrompt)
	}
	_ = filepath.Join // keep the import when the test shrinks
}
