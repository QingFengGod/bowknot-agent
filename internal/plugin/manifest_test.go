package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validManifestJSON() string {
	return `{
		"schema": 1,
		"id": "weekly-report",
		"name": "周报助手",
		"version": "1.2.0",
		"min_shell_version": "0.1.0",
		"description": "按周整理工作产出并生成周报。",
		"author": "Bowknot",
		"skills": ["skills/report.md"],
		"tools": ["fs.read", "memory.search"]
	}`
}

func writeManifestDir(t *testing.T, manifest string, skills map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if manifest != "" {
		if err := os.WriteFile(filepath.Join(dir, ManifestFileName), []byte(manifest), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range skills {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestParseManifestValid(t *testing.T) {
	m, err := ParseManifest([]byte(validManifestJSON()), "0.1.0")
	if err != nil {
		t.Fatalf("ParseManifest() error: %v", err)
	}
	if m.ID != "weekly-report" || m.Name != "周报助手" || m.Version != "1.2.0" {
		t.Errorf("unexpected manifest: %+v", m)
	}
	if len(m.Skills) != 1 || m.Skills[0] != "skills/report.md" {
		t.Errorf("unexpected skills: %v", m.Skills)
	}
	if len(m.Tools) != 2 {
		t.Errorf("unexpected tools: %v", m.Tools)
	}
}

func TestParseManifestRejections(t *testing.T) {
	cases := []struct {
		name  string
		json  string
		shell string
		field string
	}{
		{"schema", strings.Replace(validManifestJSON(), `"schema": 1`, `"schema": 2`, 1), "0.1.0", "schema"},
		{"id uppercase", strings.Replace(validManifestJSON(), `"id": "weekly-report"`, `"id": "Weekly-report"`, 1), "0.1.0", "id"},
		{"id short", strings.Replace(validManifestJSON(), `"id": "weekly-report"`, `"id": "w"`, 1), "0.1.0", "id"},
		{"id leading digit", strings.Replace(validManifestJSON(), `"id": "weekly-report"`, `"id": "1weekly"`, 1), "0.1.0", "id"},
		{"id too long", strings.Replace(validManifestJSON(), `"id": "weekly-report"`, `"id": "`+strings.Repeat("a", 65)+`"`, 1), "0.1.0", "id"},
		{"name empty", strings.Replace(validManifestJSON(), `"name": "周报助手"`, `"name": ""`, 1), "0.1.0", "name"},
		{"name too long", strings.Replace(validManifestJSON(), `"name": "周报助手"`, `"name": "`+strings.Repeat("名", 65)+`"`, 1), "0.1.0", "name"},
		{"version", strings.Replace(validManifestJSON(), `"version": "1.2.0"`, `"version": "1.2"`, 1), "0.1.0", "version"},
		{"min shell version", strings.Replace(validManifestJSON(), `"min_shell_version": "0.1.0"`, `"min_shell_version": "0.1"`, 1), "0.1.0", "min_shell_version"},
		{"shell too old", validManifestJSON(), "0.0.9", "min_shell_version"},
		{"description empty", strings.Replace(validManifestJSON(), `"description": "按周整理工作产出并生成周报。"`, `"description": ""`, 1), "0.1.0", "description"},
		{"description too long", strings.Replace(validManifestJSON(), `"description": "按周整理工作产出并生成周报。"`, `"description": "`+strings.Repeat("述", 501)+`"`, 1), "0.1.0", "description"},
		{"author empty", strings.Replace(validManifestJSON(), `"author": "Bowknot"`, `"author": ""`, 1), "0.1.0", "author"},
		{"skills empty", strings.Replace(validManifestJSON(), `"skills": ["skills/report.md"]`, `"skills": []`, 1), "0.1.0", "skills"},
		{"skills too many", strings.Replace(validManifestJSON(), `"skills": ["skills/report.md"]`, `"skills": [`+strings.Join(skillNames(MaxSkills+1), ",")+`]`, 1), "0.1.0", "skills"},
		{"skill outside dir", strings.Replace(validManifestJSON(), `"skills": ["skills/report.md"]`, `"skills": ["report.md"]`, 1), "0.1.0", "skills"},
		{"skill not markdown", strings.Replace(validManifestJSON(), `"skills": ["skills/report.md"]`, `"skills": ["skills/report.txt"]`, 1), "0.1.0", "skills"},
		{"skill escapes", strings.Replace(validManifestJSON(), `"skills": ["skills/report.md"]`, `"skills": ["skills/../report.md"]`, 1), "0.1.0", "skills"},
		{"skill duplicate", strings.Replace(validManifestJSON(), `"skills": ["skills/report.md"]`, `"skills": ["skills/report.md", "skills/REPORT.md"]`, 1), "0.1.0", "skills"},
		{"tool unknown", strings.Replace(validManifestJSON(), `"tools": ["fs.read", "memory.search"]`, `"tools": ["fs.read", "shell.exec"]`, 1), "0.1.0", "tools"},
		{"tool duplicate", strings.Replace(validManifestJSON(), `"tools": ["fs.read", "memory.search"]`, `"tools": ["fs.read", "fs.read"]`, 1), "0.1.0", "tools"},
		{"icon extension", strings.Replace(validManifestJSON(), `"tools": ["fs.read", "memory.search"]`, `"tools": ["fs.read"], "icon": "icon.gif"`, 1), "0.1.0", "icon"},
		{"icon absolute", strings.Replace(validManifestJSON(), `"tools": ["fs.read", "memory.search"]`, `"tools": ["fs.read"], "icon": "/etc/icon.png"`, 1), "0.1.0", "icon"},
		{"invalid json", `{`, "0.1.0", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseManifest([]byte(tc.json), tc.shell)
			if err == nil {
				t.Fatal("ParseManifest() expected an error")
			}
			me, ok := err.(*ManifestError)
			if !ok {
				t.Fatalf("ParseManifest() error type %T, want *ManifestError", err)
			}
			if me.Field != tc.field {
				t.Errorf("field = %q, want %q (reason %s)", me.Field, tc.field, me.Reason)
			}
		})
	}
}

func skillNames(n int) []string {
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, `"skills/s`+strings.Repeat("0", 2)+string(rune('a'+i%26))+`.md"`)
	}
	return out
}

func TestParseManifestEmptyToolsAllowed(t *testing.T) {
	json := strings.Replace(validManifestJSON(), `"tools": ["fs.read", "memory.search"]`, `"tools": []`, 1)
	m, err := ParseManifest([]byte(json), "0.1.0")
	if err != nil {
		t.Fatalf("ParseManifest() error: %v", err)
	}
	if len(m.Tools) != 0 {
		t.Errorf("tools = %v, want empty", m.Tools)
	}
}

func TestReadManifestVerifiesFiles(t *testing.T) {
	t.Run("missing skill file", func(t *testing.T) {
		dir := writeManifestDir(t, validManifestJSON(), nil)
		_, err := ReadManifest(dir, "0.1.0")
		if err == nil {
			t.Fatal("ReadManifest() expected an error")
		}
	})
	t.Run("valid", func(t *testing.T) {
		dir := writeManifestDir(t, validManifestJSON(), map[string]string{
			"skills/report.md": "# 周报\n",
		})
		if _, err := ReadManifest(dir, "0.1.0"); err != nil {
			t.Fatalf("ReadManifest() error: %v", err)
		}
	})
	t.Run("skill too large", func(t *testing.T) {
		dir := writeManifestDir(t, validManifestJSON(), map[string]string{
			"skills/report.md": strings.Repeat("a", MaxSkillSize+1),
		})
		_, err := ReadManifest(dir, "0.1.0")
		if err == nil || !strings.Contains(err.Error(), "256 KiB") {
			t.Fatalf("ReadManifest() error = %v, want size limit", err)
		}
	})
	t.Run("icon missing", func(t *testing.T) {
		json := strings.Replace(validManifestJSON(), `"tools": ["fs.read", "memory.search"]`, `"tools": [], "icon": "icon.png"`, 1)
		dir := writeManifestDir(t, json, map[string]string{"skills/report.md": "# 周报\n"})
		if _, err := ReadManifest(dir, "0.1.0"); err == nil {
			t.Fatal("ReadManifest() expected an error")
		}
	})
	t.Run("icon valid", func(t *testing.T) {
		json := strings.Replace(validManifestJSON(), `"tools": ["fs.read", "memory.search"]`, `"tools": [], "icon": "icon.png"`, 1)
		dir := writeManifestDir(t, json, map[string]string{
			"skills/report.md": "# 周报\n",
			"icon.png":         "png",
		})
		if _, err := ReadManifest(dir, "0.1.0"); err != nil {
			t.Fatalf("ReadManifest() error: %v", err)
		}
	})
	t.Run("missing manifest", func(t *testing.T) {
		dir := writeManifestDir(t, "", nil)
		_, err := ReadManifest(dir, "0.1.0")
		if err == nil || !strings.Contains(err.Error(), "缺少 manifest.json") {
			t.Fatalf("ReadManifest() error = %v, want missing manifest", err)
		}
	})
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.1", "1.0.0", 1},
		{"1.0.0", "1.0.1", -1},
		{"1.2.0", "1.10.0", -1},
		{"2.0.0", "1.9.9", 1},
		{"bad", "1.0.0", -1},
	}
	for _, tc := range cases {
		if got := CompareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestCleanRelativePath(t *testing.T) {
	valid := []string{"skills/a.md", "skills/dir/b.md", "icon.png", "skills/中文技能.md"}
	for _, p := range valid {
		if _, err := CleanRelativePath(p); err != nil {
			t.Errorf("CleanRelativePath(%q) error: %v", p, err)
		}
	}
	invalid := []string{
		"", "/abs/path", "C:/path", `skills\a.md`, "../escape", "a/./b", "a/../b",
		"trailing/", strings.Repeat("a", 256), "a/" + strings.Repeat("b/", 16) + "c",
		"ctrl\x01path",
	}
	for _, p := range invalid {
		if _, err := CleanRelativePath(p); err == nil {
			t.Errorf("CleanRelativePath(%q) expected an error", p)
		}
	}
}

func TestResolveInRootContains(t *testing.T) {
	dir := t.TempDir()
	if _, err := resolveInRoot(dir, "skills/a.md"); err != nil {
		t.Fatalf("resolveInRoot() error: %v", err)
	}
	if _, err := resolveInRoot(dir, "../outside.md"); err == nil {
		t.Error("resolveInRoot() expected an error for an escaping path")
	}
}

func TestToolSchemas(t *testing.T) {
	schemas := ToolSchemas([]string{ToolFSRead, ToolFSWrite, ToolMemorySearch, ToolDelegate, ToolGroupPost, "unknown"})
	got := make([]string, 0, len(schemas))
	for _, s := range schemas {
		got = append(got, s.Name)
	}
	// hostTools order, without the group and delegate tools.
	want := []string{ToolFSRead, ToolFSWrite, ToolMemorySearch}
	if len(got) != len(want) {
		t.Fatalf("ToolSchemas() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ToolSchemas()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
