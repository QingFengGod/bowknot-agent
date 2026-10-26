package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bowknot/bowknot-agent/internal/ui"
)

func TestLoadTheme(t *testing.T) {
	tests := []struct {
		name    string
		content string
		missing bool
		want    ui.Mode
	}{
		{name: "missing file", missing: true, want: ui.ModeLight},
		{name: "light", content: `{"theme":"light"}`, want: ui.ModeLight},
		{name: "dark", content: `{"theme":"dark"}`, want: ui.ModeDark},
		{name: "malformed", content: `{"theme":`, want: ui.ModeLight},
		{name: "unknown value", content: `{"theme":"solarized"}`, want: ui.ModeLight},
		{name: "missing field", content: `{}`, want: ui.ModeLight},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			if !tt.missing {
				if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if got := loadTheme(path); got != tt.want {
				t.Errorf("loadTheme() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLoadThemeUnreadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := loadTheme(path); got != ui.ModeLight {
		t.Errorf("loadTheme() = %q, want light", got)
	}
}

func TestSaveAndLoadTheme(t *testing.T) {
	for _, mode := range []ui.Mode{ui.ModeLight, ui.ModeDark} {
		path := filepath.Join(t.TempDir(), "nested", "settings.json")
		if err := saveTheme(path, mode); err != nil {
			t.Fatalf("saveTheme(%q): %v", mode, err)
		}
		if got := loadTheme(path); got != mode {
			t.Errorf("loadTheme() = %q, want %q", got, mode)
		}
		if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
			t.Errorf("temporary file still present after saveTheme")
		}
	}
}

func TestSaveThemeFailure(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(blocker, "settings.json")
	if err := saveTheme(path, ui.ModeDark); err == nil {
		t.Fatal("saveTheme() = nil, want an error")
	}
}
