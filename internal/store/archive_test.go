package store

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type tarEntry struct {
	name     string
	typeflag byte
	body     string
	linkname string
}

func buildTar(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		typeflag := e.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		hdr := &tar.Header{
			Name:     e.name,
			Typeflag: typeflag,
			Linkname: e.linkname,
			Mode:     0o644,
			Size:     int64(len(e.body)),
		}
		if typeflag == tar.TypeDir {
			hdr.Mode = 0o755
			hdr.Size = 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write tar header %q: %v", e.name, err)
		}
		if typeflag == tar.TypeReg && len(e.body) > 0 {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatalf("write tar body %q: %v", e.name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	return buf.Bytes()
}

func validPackageEntries() []tarEntry {
	return []tarEntry{
		{name: "manifest.json", body: `{"schema":1}`},
		{name: "index.html", body: "<html></html>"},
		{name: "assets/", typeflag: tar.TypeDir},
		{name: "assets/app.js", body: "console.log(1)"},
	}
}

func extract(t *testing.T, entries []tarEntry) (string, error) {
	t.Helper()
	dest := t.TempDir()
	err := ExtractPackage(bytes.NewReader(buildTar(t, entries)), dest)
	return dest, err
}

func TestExtractPackageValid(t *testing.T) {
	dest, err := extract(t, validPackageEntries())
	if err != nil {
		t.Fatalf("ExtractPackage: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dest, "assets", "app.js"))
	if err != nil {
		t.Fatalf("read extracted file: %v", err)
	}
	if string(data) != "console.log(1)" {
		t.Errorf("content = %q", data)
	}
	info, err := os.Stat(filepath.Join(dest, "index.html"))
	if err != nil {
		t.Fatalf("stat extracted file: %v", err)
	}
	if info.Mode().Perm()&0o111 != 0 {
		t.Errorf("file kept an executable bit: %v", info.Mode())
	}
}

func TestExtractPackageRootAndDotPrefix(t *testing.T) {
	entries := []tarEntry{
		{name: "./", typeflag: tar.TypeDir},
		{name: "./manifest.json", body: `{}`},
		{name: "./index.html", body: "<html></html>"},
	}
	if _, err := extract(t, entries); err != nil {
		t.Fatalf("ExtractPackage: %v", err)
	}
}

func TestExtractPackageRejectsMissingRootManifest(t *testing.T) {
	entries := []tarEntry{
		{name: "app/manifest.json", body: `{}`},
		{name: "app/index.html", body: "<html></html>"},
	}
	if _, err := extract(t, entries); err == nil {
		t.Fatal("expected a nested manifest to be rejected")
	}
}

func TestExtractPackageRejectsUnsafeEntries(t *testing.T) {
	tests := []struct {
		name  string
		entry tarEntry
	}{
		{"traversal", tarEntry{name: "../evil.html", body: "x"}},
		{"absolute", tarEntry{name: "/evil.html", body: "x"}},
		{"backslash", tarEntry{name: `assets\evil.html`, body: "x"}},
		{"drive", tarEntry{name: "C:/evil.html", body: "x"}},
		{"reserved", tarEntry{name: "CON.html", body: "x"}},
		{"symlink", tarEntry{name: "link.html", typeflag: tar.TypeSymlink, linkname: "index.html"}},
		{"hardlink", tarEntry{name: "link.html", typeflag: tar.TypeLink, linkname: "index.html"}},
		{"fifo", tarEntry{name: "pipe", typeflag: tar.TypeFifo}},
		{"device", tarEntry{name: "dev", typeflag: tar.TypeChar}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries := append(validPackageEntries(), tt.entry)
			dest, err := extract(t, entries)
			if err == nil {
				t.Fatalf("expected %q to be rejected", tt.entry.name)
			}
			if _, statErr := os.Stat(filepath.Join(dest, "evil.html")); statErr == nil {
				t.Error("unsafe entry escaped the staging root")
			}
		})
	}
}

func TestArchiveEntryNameRejectsEmpty(t *testing.T) {
	if _, _, err := archiveEntryName(&tar.Header{Name: ""}); err == nil {
		t.Fatal("expected an empty entry name to be rejected")
	}
	if _, _, err := archiveEntryName(&tar.Header{Name: "dir/", Typeflag: tar.TypeReg}); err == nil {
		t.Fatal("expected a file name with a trailing slash to be rejected")
	}
}

func TestExtractPackageRejectsCaseDuplicate(t *testing.T) {
	entries := append(validPackageEntries(),
		tarEntry{name: "Assets/app.js", body: "dup"},
	)
	if _, err := extract(t, entries); err == nil {
		t.Fatal("expected a case-insensitive duplicate to be rejected")
	}
}

func TestExtractPackageLimits(t *testing.T) {
	limits := ArchiveLimits{MaxEntries: 2, MaxTotalSize: 16, MaxFileSize: 16}
	entries := validPackageEntries()
	dest := t.TempDir()
	if err := extractPackage(bytes.NewReader(buildTar(t, entries)), dest, limits); err == nil {
		t.Fatal("expected the entry count limit to reject the package")
	}

	dest = t.TempDir()
	smallLimits := ArchiveLimits{MaxEntries: 100, MaxTotalSize: 5, MaxFileSize: 16}
	if err := extractPackage(bytes.NewReader(buildTar(t, entries)), dest, smallLimits); err == nil {
		t.Fatal("expected the total size limit to reject the package")
	}

	dest = t.TempDir()
	fileLimits := ArchiveLimits{MaxEntries: 100, MaxTotalSize: 100, MaxFileSize: 4}
	if err := extractPackage(bytes.NewReader(buildTar(t, entries)), dest, fileLimits); err == nil {
		t.Fatal("expected the file size limit to reject the package")
	}
}

func writeSourceApp(t *testing.T) string {
	t.Helper()
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, ManifestFileName), []byte(`{"schema":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "index.html"), []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "assets", "app.js"), []byte("console.log(1)"), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

func TestCopyDirectory(t *testing.T) {
	src := writeSourceApp(t)
	dest := filepath.Join(t.TempDir(), "staging")
	if err := CopyDirectory(src, dest); err != nil {
		t.Fatalf("CopyDirectory: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dest, "assets", "app.js"))
	if err != nil {
		t.Fatalf("read copied file: %v", err)
	}
	if string(data) != "console.log(1)" {
		t.Errorf("content = %q", data)
	}
	if err := os.RemoveAll(src); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "index.html")); err != nil {
		t.Errorf("copy should not depend on the source: %v", err)
	}
}

func TestCopyDirectoryRejectsMissingSource(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "staging")
	if err := CopyDirectory(filepath.Join(t.TempDir(), "missing"), dest); err == nil {
		t.Fatal("expected a missing source to be rejected")
	}
}

func TestCopyDirectoryRejectsDestInsideSource(t *testing.T) {
	src := writeSourceApp(t)
	dest := filepath.Join(src, "nested", "staging")
	if err := CopyDirectory(src, dest); err == nil {
		t.Fatal("expected a destination inside the source to be rejected")
	}
}

func TestCopyDirectoryRejectsSymlink(t *testing.T) {
	src := writeSourceApp(t)
	link := filepath.Join(src, "link.html")
	if err := os.Symlink(filepath.Join(src, "index.html"), link); err != nil {
		t.Skipf("symlinks are not available: %v", err)
	}
	dest := filepath.Join(t.TempDir(), "staging")
	if err := CopyDirectory(src, dest); err == nil {
		t.Fatal("expected a symlink to be rejected")
	}
}

func TestCopyDirectoryLimits(t *testing.T) {
	src := writeSourceApp(t)
	dest := filepath.Join(t.TempDir(), "staging")
	limits := ArchiveLimits{MaxEntries: 100, MaxTotalSize: 100, MaxFileSize: 4}
	if err := copyDirectory(src, dest, limits); err == nil {
		t.Fatal("expected the file size limit to reject the directory")
	}
}

func TestPackageErrorDoesNotLeakHostPath(t *testing.T) {
	err := packageErrorf("assets/app.js", "过大")
	if strings.Contains(err.Error(), filepath.Join("home", "user")) {
		t.Errorf("error leaked a host path: %v", err)
	}
}
