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

func TestAppsPaths(t *testing.T) {
	dir := filepath.Join("home", "user", ".bowknot-agent")
	apps := filepath.Join(dir, "apps")
	if got, want := appsDir(dir), apps; got != want {
		t.Errorf("appsDir(%q) = %q, want %q", dir, got, want)
	}
	if got, want := registryPath(apps), filepath.Join(apps, "registry.json"); got != want {
		t.Errorf("registryPath(%q) = %q, want %q", apps, got, want)
	}
}

func TestAppDataDir(t *testing.T) {
	dir := filepath.Join("home", "user", ".bowknot-agent")
	if got, want := appDataDir(dir), filepath.Join(dir, "appdata"); got != want {
		t.Errorf("appDataDir(%q) = %q, want %q", dir, got, want)
	}
}
