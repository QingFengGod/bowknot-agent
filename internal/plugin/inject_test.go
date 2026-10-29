package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillSectionValid(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Install(installSource(t, true, "1.2.0"), true); err != nil {
		t.Fatal(err)
	}
	section, notice, invalid := s.SkillSection([]string{"weekly-report"})
	if notice != "" || len(invalid) != 0 {
		t.Fatalf("notice = %q, invalid = %v", notice, invalid)
	}
	for _, want := range []string{
		"## 插件技能", "### 周报助手（weekly-report）", "# 周报 v1.2.0",
		"### 可用工具", "`fs.read`", "`memory.search`",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("section missing %q:\n%s", want, section)
		}
	}
	// The manifest declares fs.read and memory.search only, so fs.write is
	// not injected.
	if strings.Contains(section, "`fs.write`") {
		t.Errorf("section should not inject undeclared tools:\n%s", section)
	}
}

func TestSkillSectionFollowsEnableOrder(t *testing.T) {
	s := newTestStore(t)
	// Two plugins: the section lists them in the order of the enable list.
	if _, err := s.Install(installSource(t, true, "1.0.0"), true); err != nil {
		t.Fatal(err)
	}
	other := strings.Replace(validManifestJSON(), `"id": "weekly-report"`, `"id": "other-plugin"`, 1)
	otherDir := writeManifestDir(t, other, map[string]string{"skills/report.md": "# 另一个技能\n"})
	if _, err := s.Install(otherDir, true); err != nil {
		t.Fatal(err)
	}
	section, _, _ := s.SkillSection([]string{"other-plugin", "weekly-report"})
	otherAt := strings.Index(section, "other-plugin")
	weeklyAt := strings.Index(section, "weekly-report")
	if otherAt < 0 || weeklyAt < 0 || otherAt > weeklyAt {
		t.Errorf("section order wrong:\n%s", section)
	}
}

func TestSkillSectionSkipsMissingAndInvalid(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Install(installSource(t, true, "1.0.0"), true); err != nil {
		t.Fatal(err)
	}
	// Corrupt the installed skill file so the manifest check fails.
	if err := os.Remove(filepath.Join(s.PluginDir("weekly-report"), "skills", "report.md")); err != nil {
		t.Fatal(err)
	}
	section, notice, invalid := s.SkillSection([]string{"no-such-plugin", "weekly-report"})
	if len(invalid) != 2 {
		t.Fatalf("invalid = %v, want both plugins", invalid)
	}
	if !strings.Contains(notice, "no-such-plugin") || !strings.Contains(notice, "周报助手") {
		t.Errorf("notice = %q", notice)
	}
	if strings.Contains(section, "插件技能") {
		t.Errorf("section should be empty:\n%s", section)
	}
}

func TestSkillSectionBudget(t *testing.T) {
	s := newTestStore(t)
	huge := strings.Repeat("很长的技能内容。", 2000) // ≈14000 runes
	manifest := strings.Replace(validManifestJSON(), `"skills": ["skills/report.md"]`, `"skills": ["skills/report.md", "skills/extra.md"]`, 1)
	src := writeManifestDir(t, manifest, map[string]string{
		"skills/report.md": huge,
		"skills/extra.md":  "# extra\n",
	})
	if _, err := s.Install(src, true); err != nil {
		t.Fatal(err)
	}
	section, notice, invalid := s.SkillSection([]string{"weekly-report"})
	if notice != "" || len(invalid) != 0 {
		t.Fatalf("notice = %q, invalid = %v", notice, invalid)
	}
	if runes := len([]rune(section)); runes > InjectionBudgetRunes+60 {
		t.Errorf("section = %d runes, want ≈ budget %d", runes, InjectionBudgetRunes)
	}
	if !strings.Contains(section, "超出预算，已截断") {
		t.Errorf("section missing the truncation marker:\n%s", section[len(section)-120:])
	}
}

func TestSkillSectionEmptyList(t *testing.T) {
	s := newTestStore(t)
	section, notice, invalid := s.SkillSection(nil)
	if section != "" || notice != "" || len(invalid) != 0 {
		t.Fatalf("unexpected result: %q %q %v", section, notice, invalid)
	}
}
