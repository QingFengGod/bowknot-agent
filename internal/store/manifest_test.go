package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validManifest() Manifest {
	return Manifest{
		Schema:          ManifestSchema,
		ID:              "docs",
		Name:            "文档工作台",
		Version:         "1.4.0",
		Entry:           "index.html",
		Icon:            "icon.svg",
		MinShellVersion: "0.1.0",
		Summary:         "笔记与文档",
		Category:        "efficiency",
		ReleaseNotes:    "首个本地安装版本。",
		Window: ManifestWindow{
			Width:     1080,
			Height:    720,
			MinWidth:  640,
			MinHeight: 480,
		},
	}
}

func marshalManifest(t *testing.T, m Manifest) []byte {
	t.Helper()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	return data
}

func TestParseManifestValid(t *testing.T) {
	m, err := ParseManifest(marshalManifest(t, validManifest()), "0.1.0")
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if m.ID != "docs" || m.Entry != "index.html" {
		t.Errorf("manifest = %+v", m)
	}
}

func TestParseManifestDefaultsWindow(t *testing.T) {
	m := validManifest()
	m.Window = ManifestWindow{}
	parsed, err := ParseManifest(marshalManifest(t, m), "0.1.0")
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	want := ManifestWindow{
		Width: DefaultWindowWidth, Height: DefaultWindowHeight,
		MinWidth: DefaultWindowMinWidth, MinHeight: DefaultWindowMinHeight,
	}
	if parsed.Window != want {
		t.Errorf("window = %+v, want %+v", parsed.Window, want)
	}
}

func TestParseManifestIgnoresUnknownFields(t *testing.T) {
	data := append(marshalManifest(t, validManifest())[:len(marshalManifest(t, validManifest()))-1], []byte(`,"future_field":true}`)...)
	if _, err := ParseManifest(data, "0.1.0"); err != nil {
		t.Fatalf("ParseManifest with unknown field: %v", err)
	}
}

func TestParseManifestInvalid(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Manifest)
		field  string
	}{
		{"schema", func(m *Manifest) { m.Schema = 2 }, "schema"},
		{"id uppercase", func(m *Manifest) { m.ID = "Docs" }, "id"},
		{"id short", func(m *Manifest) { m.ID = "a" }, "id"},
		{"id bad chars", func(m *Manifest) { m.ID = "docs_app" }, "id"},
		{"name blank", func(m *Manifest) { m.Name = "   " }, "name"},
		{"name long", func(m *Manifest) { m.Name = strings.Repeat("名", 65) }, "name"},
		{"version", func(m *Manifest) { m.Version = "1.0" }, "version"},
		{"version pre", func(m *Manifest) { m.Version = "1.0.0-beta" }, "version"},
		{"entry extension", func(m *Manifest) { m.Entry = "index.js" }, "entry"},
		{"entry absent", func(m *Manifest) { m.Entry = "" }, "entry"},
		{"entry traversal", func(m *Manifest) { m.Entry = "../index.html" }, "entry"},
		{"entry absolute", func(m *Manifest) { m.Entry = "/index.html" }, "entry"},
		{"entry backslash", func(m *Manifest) { m.Entry = `assets\index.html` }, "entry"},
		{"entry drive", func(m *Manifest) { m.Entry = `C:/index.html` }, "entry"},
		{"entry reserved", func(m *Manifest) { m.Entry = "CON.html" }, "entry"},
		{"icon extension", func(m *Manifest) { m.Icon = "icon.gif" }, "icon"},
		{"icon traversal", func(m *Manifest) { m.Icon = "../icon.svg" }, "icon"},
		{"min shell format", func(m *Manifest) { m.MinShellVersion = "1" }, "min_shell_version"},
		{"summary blank", func(m *Manifest) { m.Summary = "" }, "summary"},
		{"summary long", func(m *Manifest) { m.Summary = strings.Repeat("摘", 201) }, "summary"},
		{"category", func(m *Manifest) { m.Category = "other" }, "category"},
		{"release notes long", func(m *Manifest) { m.ReleaseNotes = strings.Repeat("注", 501) }, "release_notes"},
		{"window width small", func(m *Manifest) { m.Window.Width = 200 }, "window.width"},
		{"window width large", func(m *Manifest) { m.Window.Width = 5000 }, "window.width"},
		{"window min width", func(m *Manifest) { m.Window.MinWidth = 1200 }, "window.min_width"},
		{"window min height", func(m *Manifest) { m.Window.MinHeight = 100 }, "window.min_height"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := validManifest()
			tt.mutate(&m)
			_, err := ParseManifest(marshalManifest(t, m), "0.1.0")
			if err == nil {
				t.Fatal("expected error")
			}
			var me *ManifestError
			if !asManifestError(err, &me) || me.Field != tt.field {
				t.Fatalf("error = %v, want field %q", err, tt.field)
			}
		})
	}
}

func asManifestError(err error, target **ManifestError) bool {
	me, ok := err.(*ManifestError)
	if ok {
		*target = me
	}
	return ok
}

func TestParseManifestNewerShellRejected(t *testing.T) {
	m := validManifest()
	m.MinShellVersion = "0.2.0"
	if _, err := ParseManifest(marshalManifest(t, m), "0.1.0"); err == nil {
		t.Fatal("expected a newer shell version to be rejected")
	}
	if _, err := ParseManifest(marshalManifest(t, m), "0.2.0"); err != nil {
		t.Fatalf("same shell version: %v", err)
	}
}

func TestParseManifestTooLarge(t *testing.T) {
	if _, err := ParseManifest(make([]byte, MaxManifestSize+1), "0.1.0"); err == nil {
		t.Fatal("expected oversized manifest to be rejected")
	}
}

func TestCleanRelativePath(t *testing.T) {
	valid := []string{"index.html", "assets/app.js", "a/b/c.png", "icon.svg"}
	for _, p := range valid {
		if _, err := CleanRelativePath(p); err != nil {
			t.Errorf("CleanRelativePath(%q) = %v, want nil", p, err)
		}
	}
	invalid := []string{
		"", "/index.html", "../index.html", "a/../b", "a//b", "a/", `a\b`,
		"./index.html", "C:/index.html", "CON", "con.txt", "a /b", "a./b",
		strings.Repeat("a/", 17) + "b",
	}
	for _, p := range invalid {
		if _, err := CleanRelativePath(p); err == nil {
			t.Errorf("CleanRelativePath(%q) = nil, want error", p)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"0.1.0", "0.1.0", 0},
		{"0.1.1", "0.1.0", 1},
		{"0.1.0", "0.2.0", -1},
		{"1.0.0", "0.9.9", 1},
		{"10.0.0", "9.9.9", 1},
		{"bad", "0.0.0", 0},
	}
	for _, tt := range tests {
		if got := CompareVersions(tt.a, tt.b); got != tt.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
	if !VersionAtLeast("0.1.0", "0.1.0") || VersionAtLeast("0.0.9", "0.1.0") {
		t.Error("VersionAtLeast returned the wrong result")
	}
}

func writeAppFiles(t *testing.T, dir string, m Manifest, iconSize int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestFileName), marshalManifest(t, m), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "icon.svg"), make([]byte, iconSize), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadManifest(t *testing.T) {
	dir := t.TempDir()
	writeAppFiles(t, dir, validManifest(), 100)
	m, err := ReadManifest(dir, "0.1.0")
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if m.Name != "文档工作台" {
		t.Errorf("name = %q", m.Name)
	}
}

func TestReadManifestMissingManifest(t *testing.T) {
	dir := t.TempDir()
	if _, err := ReadManifest(dir, "0.1.0"); err == nil {
		t.Fatal("expected an error for a missing manifest")
	}
}

func TestReadManifestMissingEntry(t *testing.T) {
	dir := t.TempDir()
	writeAppFiles(t, dir, validManifest(), 100)
	if err := os.Remove(filepath.Join(dir, "index.html")); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadManifest(dir, "0.1.0"); err == nil {
		t.Fatal("expected an error for a missing entry")
	}
}

func TestReadManifestIconTooLarge(t *testing.T) {
	dir := t.TempDir()
	writeAppFiles(t, dir, validManifest(), MaxIconSize+1)
	if _, err := ReadManifest(dir, "0.1.0"); err == nil {
		t.Fatal("expected an error for an oversized icon")
	}
}
