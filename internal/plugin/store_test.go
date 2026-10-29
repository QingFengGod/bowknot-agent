package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// installSource writes a valid plugin source (a directory or a .bkplugin tar)
// and returns its path.
func installSource(t *testing.T, directory bool, version string) string {
	t.Helper()
	manifest := strings.Replace(validManifestJSON(), `"version": "1.2.0"`, `"version": "`+version+`"`, 1)
	if directory {
		return writeManifestDir(t, manifest, map[string]string{"skills/report.md": "# 周报 v" + version + "\n"})
	}
	data := buildTar(t, []tarEntry{
		{name: "skills", dir: true},
		{name: "manifest.json", content: manifest},
		{name: "skills/report.md", content: "# 周报 v" + version + "\n"},
	})
	path := filepath.Join(t.TempDir(), "weekly-report.bkplugin")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s := NewStore(filepath.Join(t.TempDir(), "plugins"), "0.1.0")
	if err := s.Load(); err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	return s
}

func TestInstallFromDirectory(t *testing.T) {
	s := newTestStore(t)
	src := installSource(t, true, "1.0.0")
	entry, err := s.Install(src, true)
	if err != nil {
		t.Fatalf("Install() error: %v", err)
	}
	if entry.ID != "weekly-report" || entry.Version != "1.0.0" || entry.SHA256 == "" {
		t.Fatalf("unexpected entry: %+v", entry)
	}
	if _, err := os.Stat(filepath.Join(s.PluginDir("weekly-report"), "skills", "report.md")); err != nil {
		t.Fatalf("installed skill missing: %v", err)
	}
	if leftovers, _ := os.ReadDir(filepath.Join(s.dir, stagingDirName)); len(leftovers) != 0 {
		t.Errorf("staging should hold no leftovers, got %d entries", len(leftovers))
	}
	items := s.List()
	if len(items) != 1 || !items[0].Valid {
		t.Fatalf("unexpected list: %+v", items)
	}
}

func TestInstallFromArchive(t *testing.T) {
	s := newTestStore(t)
	entry, err := s.Install(installSource(t, false, "1.0.0"), false)
	if err != nil {
		t.Fatalf("Install() error: %v", err)
	}
	if entry.ID != "weekly-report" {
		t.Fatalf("unexpected entry: %+v", entry)
	}
}

func TestInspectDoesNotInstall(t *testing.T) {
	s := newTestStore(t)
	m, err := s.Inspect(installSource(t, true, "1.0.0"), true)
	if err != nil {
		t.Fatalf("Inspect() error: %v", err)
	}
	if m.ID != "weekly-report" {
		t.Fatalf("unexpected manifest: %+v", m)
	}
	if _, ok := s.Get("weekly-report"); ok {
		t.Error("Inspect() installed the plugin")
	}
}

func TestInspectRejectsBadPackage(t *testing.T) {
	s := newTestStore(t)
	bad := strings.Replace(validManifestJSON(), `"tools": ["fs.read", "memory.search"]`, `"tools": ["shell.exec"]`, 1)
	src := writeManifestDir(t, bad, map[string]string{"skills/report.md": "# 周报\n"})
	if _, err := s.Inspect(src, true); err == nil {
		t.Fatal("Inspect() expected an error for an unknown tool")
	}
}

func TestInstallUpgradeAndDowngrade(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Install(installSource(t, true, "1.0.0"), true); err != nil {
		t.Fatalf("Install() error: %v", err)
	}
	first, _ := s.Get("weekly-report")

	// Upgrade: same or newer version replaces the files and the entry.
	if _, err := s.Install(installSource(t, true, "1.2.0"), true); err != nil {
		t.Fatalf("Install() upgrade error: %v", err)
	}
	entry, ok := s.Get("weekly-report")
	if !ok || entry.Version != "1.2.0" {
		t.Fatalf("unexpected entry after upgrade: %+v", entry)
	}
	if entry.InstalledAt != first.InstalledAt {
		t.Error("upgrade should keep the original install time")
	}
	data, err := os.ReadFile(filepath.Join(s.PluginDir("weekly-report"), "skills", "report.md"))
	if err != nil || !strings.Contains(string(data), "1.2.0") {
		t.Fatalf("upgraded skill = %q, err %v", data, err)
	}
	if _, err := os.Stat(firstDir(t, s) + ".backup"); !os.IsNotExist(err) {
		t.Errorf("backup should be removed after a successful install, err %v", err)
	}

	// Downgrade: rejected, the installed version stays.
	if _, err := s.Install(installSource(t, true, "1.0.0"), true); err == nil {
		t.Fatal("Install() expected a downgrade rejection")
	}
	if entry, _ := s.Get("weekly-report"); entry.Version != "1.2.0" {
		t.Errorf("version after rejected downgrade = %q, want 1.2.0", entry.Version)
	}
}

func firstDir(t *testing.T, s *Store) string {
	t.Helper()
	return filepath.Join(s.dir, "weekly-report")
}

func TestInstallRollsBackOnRegistryFailure(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Install(installSource(t, true, "1.0.0"), true); err != nil {
		t.Fatalf("Install() error: %v", err)
	}
	// Make the registry unwritable by replacing the file with a directory.
	if err := os.Remove(filepath.Join(s.dir, RegistryFileName)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(s.dir, RegistryFileName), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Install(installSource(t, true, "2.0.0"), true); err == nil {
		t.Fatal("Install() expected a registry failure")
	}
	data, err := os.ReadFile(filepath.Join(s.PluginDir("weekly-report"), "skills", "report.md"))
	if err != nil || !strings.Contains(string(data), "1.0.0") {
		t.Fatalf("rollback failed, skill = %q, err %v", data, err)
	}
}

func TestUninstall(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Install(installSource(t, false, "1.0.0"), false); err != nil {
		t.Fatalf("Install() error: %v", err)
	}
	if err := s.Uninstall("weekly-report"); err != nil {
		t.Fatalf("Uninstall() error: %v", err)
	}
	if _, err := os.Stat(s.PluginDir("weekly-report")); !os.IsNotExist(err) {
		t.Errorf("plugin directory should be gone, err %v", err)
	}
	if _, ok := s.Get("weekly-report"); ok {
		t.Error("registry entry should be gone")
	}
	if items := s.List(); len(items) != 0 {
		t.Fatalf("unexpected list: %+v", items)
	}
	if err := s.Uninstall("weekly-report"); err != ErrNotFound {
		t.Errorf("Uninstall() = %v, want ErrNotFound", err)
	}
	// A plugin whose directory is already gone still uninstalls cleanly.
	if _, err := s.Install(installSource(t, true, "1.0.0"), true); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(s.PluginDir("weekly-report")); err != nil {
		t.Fatal(err)
	}
	if err := s.Uninstall("weekly-report"); err != nil {
		t.Fatalf("Uninstall() with missing directory error: %v", err)
	}
}

func TestListMarksInvalid(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Install(installSource(t, true, "1.0.0"), true); err != nil {
		t.Fatalf("Install() error: %v", err)
	}
	if err := os.Remove(filepath.Join(s.PluginDir("weekly-report"), ManifestFileName)); err != nil {
		t.Fatal(err)
	}
	items := s.List()
	if len(items) != 1 || items[0].Valid {
		t.Fatalf("unexpected list: %+v", items)
	}
}

func TestLoadCorruptRegistry(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, RegistryFileName), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewStore(dir, "0.1.0")
	if err := s.Load(); err == nil {
		t.Fatal("Load() expected an error for corrupt JSON")
	}
	if items := s.List(); len(items) != 0 {
		t.Errorf("List() = %+v, want empty", items)
	}
}

func TestLoadRejectsInvalidEntries(t *testing.T) {
	dir := t.TempDir()
	file := `{"schema":1,"plugins":[
		{"id":"good","name":"好","version":"1.0.0","skills":["skills/a.md"]},
		{"id":"","name":"坏","version":"1.0.0","skills":["skills/a.md"]},
		{"id":"bad-version","name":"坏","version":"x","skills":["skills/a.md"]}
	]}`
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, RegistryFileName), []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewStore(dir, "0.1.0")
	if err := s.Load(); err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if _, ok := s.Get("good"); !ok {
		t.Error("valid entry should be loaded")
	}
	if _, ok := s.Get("bad-version"); ok {
		t.Error("invalid entry should be skipped")
	}
}

func TestAuditRecordsActions(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Install(installSource(t, false, "1.0.0"), false); err != nil {
		t.Fatal(err)
	}
	s.Record("enable", "weekly-report", "Agent main 启用")
	if err := s.Uninstall("weekly-report"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(s.dir, AuditFileName))
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	text := string(data)
	for _, want := range []string{`"action":"install"`, `"action":"enable"`, `"action":"uninstall"`, "weekly-report"} {
		if !strings.Contains(text, want) {
			t.Errorf("audit log missing %s:\n%s", want, text)
		}
	}
}

func TestAuditRotation(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 2000; i++ {
		s.Record("enable", "weekly-report", strings.Repeat("x", 300))
	}
	info, err := os.Stat(filepath.Join(s.dir, AuditFileName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > MaxAuditBytes+4096 {
		t.Errorf("audit log size = %d, want rotation below %d", info.Size(), MaxAuditBytes)
	}
}

func TestCleanStaging(t *testing.T) {
	s := newTestStore(t)
	staging := filepath.Join(s.dir, stagingDirName, "install-leftover")
	if err := os.MkdirAll(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	s.CleanStaging()
	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Errorf("staging leftover should be removed, err %v", err)
	}
}
