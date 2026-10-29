package agent

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceEnsureCreatesDefaults(t *testing.T) {
	root := t.TempDir()
	ws := NewWorkspace(root, MainAgentID)
	if err := ws.Ensure(); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{ws.Dir(), ws.SessionsDir(), ws.MemoriesDir()} {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Errorf("directory %s was not created: %v", dir, err)
		}
	}
	defaults := map[string]string{
		AgentsFileName:  DefaultAgentsMD,
		PersonaFileName: DefaultPersonaMD,
		UserFileName:    DefaultUserMD,
		PluginsFileName: DefaultPluginsJSON,
	}
	for name, want := range defaults {
		data, err := os.ReadFile(ws.Path(name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if string(data) != want {
			t.Errorf("%s = %q, want the default text", name, data)
		}
	}

	// A second Ensure must not overwrite an edited file.
	if err := ws.Save(AgentsFileName, "# 自定义入口\n"); err != nil {
		t.Fatal(err)
	}
	if err := ws.Ensure(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(ws.Path(AgentsFileName))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "# 自定义入口\n" {
		t.Errorf("edited AGENTS.md = %q, want it preserved", got)
	}
}

func TestWorkspaceLoadAssemblesPromptInOrder(t *testing.T) {
	ws := NewWorkspace(t.TempDir(), "a-12345678")
	if err := ws.Ensure(); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		AgentsFileName:  "# 入口\n\n入口标记",
		PersonaFileName: "# 人设\n\n人设标记",
		UserFileName:    "# 用户\n\n用户标记",
	}
	for name, content := range files {
		if err := ws.Save(name, content); err != nil {
			t.Fatal(err)
		}
	}
	content, err := ws.Load()
	if err != nil {
		t.Fatal(err)
	}
	prompt := content.Prompt()
	agentsAt := strings.Index(prompt, "入口标记")
	personaAt := strings.Index(prompt, "人设标记")
	userAt := strings.Index(prompt, "用户标记")
	if agentsAt < 0 || personaAt < 0 || userAt < 0 {
		t.Fatalf("prompt misses a section: %q", prompt)
	}
	if !(agentsAt < personaAt && personaAt < userAt) {
		t.Errorf("prompt order is %d/%d/%d, want AGENTS → PERSONA → USER", agentsAt, personaAt, userAt)
	}
}

func TestWorkspaceLoadMissingFilesFallBack(t *testing.T) {
	ws := NewWorkspace(t.TempDir(), "a-12345678")
	content, err := ws.Load()
	if err != nil {
		t.Fatal(err)
	}
	if content.Agents != DefaultAgentsMD || content.Persona != DefaultPersonaMD || content.User != DefaultUserMD {
		t.Errorf("missing files did not fall back to defaults: %+v", content)
	}
	if _, err := os.Stat(ws.Path(AgentsFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Load created AGENTS.md, want it read-only: %v", err)
	}
}

func TestWorkspaceLoadCorruptKeepsOriginal(t *testing.T) {
	ws := NewWorkspace(t.TempDir(), MainAgentID)
	if err := ws.Ensure(); err != nil {
		t.Fatal(err)
	}
	bad := []byte{0xff, 0xfe, 0x00, 0x01}
	path := ws.Path(PersonaFileName)
	if err := os.WriteFile(path, bad, 0o600); err != nil {
		t.Fatal(err)
	}

	content, err := ws.Load()
	if !errors.Is(err, ErrWorkspaceCorrupt) {
		t.Fatalf("Load error = %v, want ErrWorkspaceCorrupt", err)
	}
	if content.Persona != "" {
		t.Errorf("corrupt persona leaked into content: %q", content.Persona)
	}
	if content.Agents != DefaultAgentsMD || content.User != DefaultUserMD {
		t.Errorf("healthy files were not loaded: %+v", content)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, bad) {
		t.Errorf("corrupt file was overwritten: %q", data)
	}
}

func TestWorkspaceSaveRejectsBadInput(t *testing.T) {
	ws := NewWorkspace(t.TempDir(), "a-12345678")
	if err := ws.Save("../AGENTS.md", "x"); err == nil {
		t.Error("a path outside the workspace is accepted")
	}
	if err := ws.Save("notes.txt", "x"); err == nil {
		t.Error("an unknown workspace file is accepted")
	}
	if err := ws.Save(AgentsFileName, string([]byte{0xff})); err == nil {
		t.Error("invalid UTF-8 is accepted")
	}

	if err := ws.Save(AgentsFileName, "# 新入口\n"); err != nil {
		t.Fatal(err)
	}
	content, err := ws.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content.Prompt(), "新入口") {
		t.Errorf("saved text is not loaded: %q", content.Prompt())
	}
	path := ws.Path(AgentsFileName)
	if filepath.Dir(path) != ws.Dir() {
		t.Errorf("workspace file path %q is outside %q", path, ws.Dir())
	}
}
