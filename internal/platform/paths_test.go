package platform

import (
	"path/filepath"
	"testing"
)

func TestDataDir(t *testing.T) {
	home := filepath.Join("home", "user")
	if got, want := dataDir(home), filepath.Join(home, ".bowknot-agent"); got != want {
		t.Errorf("dataDir(%q) = %q, want %q", home, got, want)
	}
}

func TestSettingsPath(t *testing.T) {
	dir := filepath.Join("home", "user", ".bowknot-agent")
	if got, want := settingsPath(dir), filepath.Join(dir, "settings.json"); got != want {
		t.Errorf("settingsPath(%q) = %q, want %q", dir, got, want)
	}
}
