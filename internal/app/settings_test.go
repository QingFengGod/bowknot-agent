package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/QingFengGod/bowknot-agent/internal/ui"
)

func TestLoadTheme(t *testing.T) {
	tests := []struct {
		name    string
		content string
		missing bool
		want    ui.Preference
	}{
		{name: "missing file", missing: true, want: ui.PreferenceSystem},
		{name: "light", content: `{"theme":"light"}`, want: ui.PreferenceLight},
		{name: "dark", content: `{"theme":"dark"}`, want: ui.PreferenceDark},
		{name: "system", content: `{"theme":"system"}`, want: ui.PreferenceSystem},
		{name: "malformed", content: `{"theme":`, want: ui.PreferenceSystem},
		{name: "unknown value", content: `{"theme":"solarized"}`, want: ui.PreferenceSystem},
		{name: "missing field", content: `{}`, want: ui.PreferenceSystem},
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
	if got := loadTheme(path); got != ui.PreferenceSystem {
		t.Errorf("loadTheme() = %q, want system", got)
	}
}

func TestSaveAndLoadTheme(t *testing.T) {
	for _, preference := range []ui.Preference{ui.PreferenceLight, ui.PreferenceDark, ui.PreferenceSystem} {
		path := filepath.Join(t.TempDir(), "nested", "settings.json")
		if err := saveTheme(path, preference); err != nil {
			t.Fatalf("saveTheme(%q): %v", preference, err)
		}
		if got := loadTheme(path); got != preference {
			t.Errorf("loadTheme() = %q, want %q", got, preference)
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
	if err := saveTheme(path, ui.PreferenceDark); err == nil {
		t.Fatal("saveTheme() = nil, want an error")
	}
}
