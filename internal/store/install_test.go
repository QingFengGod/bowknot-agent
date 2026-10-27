package store

import (
	"archive/tar"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestInstaller(t *testing.T) *Installer {
	t.Helper()
	root := t.TempDir()
	apps := filepath.Join(root, "apps")
	appdata := filepath.Join(root, "appdata")
	registryPath := filepath.Join(apps, "registry.json")
	if err := os.MkdirAll(apps, 0o755); err != nil {
		t.Fatal(err)
	}
	registry, err := LoadRegistry(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	return &Installer{
		AppsDir:      apps,
		AppDataDir:   appdata,
		RegistryPath: registryPath,
		Registry:     registry,
		ShellVersion: "0.1.0",
	}
}

func writePackageFile(t *testing.T, m Manifest) string {
	t.Helper()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "app.bkapp")
	content := buildTar(t, []tarEntry{
		{name: "manifest.json", body: string(data)},
		{name: "index.html", body: "<html></html>"},
		{name: "icon.svg", body: "<svg/>"},
		{name: "assets/", typeflag: tar.TypeDir},
		{name: "assets/app.js", body: "console.log(1)"},
	})
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeAppSourceDir(t *testing.T, m Manifest) string {
	t.Helper()
	dir := t.TempDir()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestFileName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "icon.svg"), []byte("<svg/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func stagingEntries(t *testing.T, in *Installer) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(in.AppsDir, ".staging"))
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatal(err)
	}
	return len(entries)
}

func TestInstallPackage(t *testing.T) {
	in := newTestInstaller(t)
	m := validManifest()
	app, err := in.Install(context.Background(), InstallRequest{Path: writePackageFile(t, m)})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if app.ID != "docs" || app.Status != StatusInstalled || app.Source != SourcePackage {
		t.Errorf("app = %+v", app)
	}
	if app.Size == "" {
		t.Error("Size is empty")
	}
	if _, err := os.Stat(filepath.Join(in.AppsDir, "docs", "assets", "app.js")); err != nil {
		t.Errorf("installed file missing: %v", err)
	}
	if got := stagingEntries(t, in); got != 0 {
		t.Errorf("staging entries = %d, want 0", got)
	}
	loaded, err := LoadRegistry(in.RegistryPath)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	if got, ok := loaded.Get("docs"); !ok || got.Name != m.Name {
		t.Errorf("saved registry = %+v, ok = %v", got, ok)
	}
}

func TestInstallDirectory(t *testing.T) {
	in := newTestInstaller(t)
	src := writeAppSourceDir(t, validManifest())
	app, err := in.Install(context.Background(), InstallRequest{Path: src, Directory: true})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if app.Source != SourceDirectory {
		t.Errorf("Source = %q, want %q", app.Source, SourceDirectory)
	}
	if err := os.RemoveAll(src); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(in.AppsDir, "docs", "index.html")); err != nil {
		t.Errorf("installed copy missing after source removal: %v", err)
	}
}

func TestInstallRejectsWrongExtension(t *testing.T) {
	in := newTestInstaller(t)
	path := filepath.Join(t.TempDir(), "app.zip")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := in.Install(context.Background(), InstallRequest{Path: path}); err == nil {
		t.Fatal("expected a non-.bkapp file to be rejected")
	}
}

func TestInstallRejectsNewerShellVersion(t *testing.T) {
	in := newTestInstaller(t)
	m := validManifest()
	m.MinShellVersion = "0.2.0"
	_, err := in.Install(context.Background(), InstallRequest{Path: writePackageFile(t, m)})
	assertInstallCode(t, err, InstallVersion)
	if got := stagingEntries(t, in); got != 0 {
		t.Errorf("staging entries = %d, want 0", got)
	}
}

func TestInstallRejectsInvalidPackage(t *testing.T) {
	in := newTestInstaller(t)
	path := filepath.Join(t.TempDir(), "broken.bkapp")
	content := buildTar(t, []tarEntry{{name: "index.html", body: "<html></html>"}})
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := in.Install(context.Background(), InstallRequest{Path: path})
	assertInstallCode(t, err, InstallInvalid)
	if got := stagingEntries(t, in); got != 0 {
		t.Errorf("staging entries = %d, want 0", got)
	}
	if in.Registry.Len() != 0 {
		t.Errorf("registry len = %d, want 0", in.Registry.Len())
	}
}

func TestInstallRejectsInstalledApp(t *testing.T) {
	in := newTestInstaller(t)
	if _, err := in.Install(context.Background(), InstallRequest{Path: writePackageFile(t, validManifest())}); err != nil {
		t.Fatalf("first install: %v", err)
	}
	_, err := in.Install(context.Background(), InstallRequest{Path: writePackageFile(t, validManifest())})
	assertInstallCode(t, err, InstallConflict)
}

func TestInstallRepairsCorruptOrDisabledApp(t *testing.T) {
	for _, status := range []Status{StatusCorrupt, StatusDisabled} {
		t.Run(string(status), func(t *testing.T) {
			in := newTestInstaller(t)
			in.Registry.Upsert(AppInfo{ID: "docs", Name: "旧版本", Status: status})
			if err := SaveRegistry(in.RegistryPath, in.Registry); err != nil {
				t.Fatal(err)
			}
			app, err := in.Install(context.Background(), InstallRequest{Path: writePackageFile(t, validManifest())})
			if err != nil {
				t.Fatalf("Install: %v", err)
			}
			if app.Status != StatusInstalled {
				t.Errorf("Status = %q, want installed", app.Status)
			}
		})
	}
}

func TestUninstallKeepsData(t *testing.T) {
	in := newTestInstaller(t)
	if _, err := in.Install(context.Background(), InstallRequest{Path: writePackageFile(t, validManifest())}); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(in.AppDataDir, "docs")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "note.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := in.Uninstall(context.Background(), "docs", false); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(in.AppsDir, "docs")); !os.IsNotExist(err) {
		t.Errorf("program directory still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "note.json")); err != nil {
		t.Errorf("app data should be kept: %v", err)
	}
	if in.Registry.Len() != 0 {
		t.Errorf("registry len = %d, want 0", in.Registry.Len())
	}
}

func TestUninstallRemovesData(t *testing.T) {
	in := newTestInstaller(t)
	if _, err := in.Install(context.Background(), InstallRequest{Path: writePackageFile(t, validManifest())}); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(in.AppDataDir, "docs")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := in.Uninstall(context.Background(), "docs", true); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Errorf("app data still exists: %v", err)
	}
}

func TestUninstallUnknownApp(t *testing.T) {
	in := newTestInstaller(t)
	if err := in.Uninstall(context.Background(), "missing", false); err == nil {
		t.Fatal("expected an unknown app to fail")
	}
}

func TestSetStatusAndMarkOpened(t *testing.T) {
	in := newTestInstaller(t)
	if _, err := in.Install(context.Background(), InstallRequest{Path: writePackageFile(t, validManifest())}); err != nil {
		t.Fatal(err)
	}
	if err := in.SetStatus("docs", StatusDisabled); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if app, _ := in.Registry.Get("docs"); app.Status != StatusDisabled {
		t.Errorf("Status = %q, want disabled", app.Status)
	}
	app, err := in.MarkOpened("docs")
	if err != nil {
		t.Fatalf("MarkOpened: %v", err)
	}
	if app.LastOpened == 0 {
		t.Error("LastOpened was not recorded")
	}
	loaded, err := LoadRegistry(in.RegistryPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := loaded.Get("docs"); got.LastOpened == 0 || got.Status != StatusDisabled {
		t.Errorf("saved app = %+v", got)
	}
}

func TestCleanStaging(t *testing.T) {
	in := newTestInstaller(t)
	staging := filepath.Join(in.AppsDir, ".staging", "leftover")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	in.CleanStaging()
	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Errorf("staging leftover still exists: %v", err)
	}
}

func TestFormatSize(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{2048, "2 KB"},
		{3 * 1024 * 1024, "3.0 MB"},
	}
	for _, tt := range tests {
		if got := FormatSize(tt.bytes); got != tt.want {
			t.Errorf("FormatSize(%d) = %q, want %q", tt.bytes, got, tt.want)
		}
	}
}

func assertInstallCode(t *testing.T, err error, want InstallErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	installErr, ok := err.(*InstallError)
	if !ok {
		t.Fatalf("error = %T %v, want *InstallError", err, err)
	}
	if installErr.Code != want {
		t.Errorf("code = %q, want %q", installErr.Code, want)
	}
	if strings.TrimSpace(installErr.Message) == "" {
		t.Error("message is empty")
	}
}
