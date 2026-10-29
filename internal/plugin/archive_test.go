package plugin

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tarEntry is one entry of a synthesized .bkplugin archive.
type tarEntry struct {
	name     string
	content  string
	dir      bool
	typeflag byte
	linkname string
}

func buildTar(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Typeflag: tar.TypeReg, Size: int64(len(e.content))}
		switch {
		case e.dir:
			hdr = &tar.Header{Name: e.name, Typeflag: tar.TypeDir}
		case e.typeflag != 0:
			hdr = &tar.Header{Name: e.name, Typeflag: e.typeflag, Linkname: e.linkname}
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if hdr.Typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(e.content)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func validTarEntries() []tarEntry {
	return []tarEntry{
		{name: "skills", dir: true},
		{name: "manifest.json", content: validManifestJSON()},
		{name: "skills/report.md", content: "# 周报\n"},
	}
}

func TestExtractPackageValid(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "staging")
	if err := ExtractPackage(bytes.NewReader(buildTar(t, validTarEntries())), dest); err != nil {
		t.Fatalf("ExtractPackage() error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dest, "skills", "report.md"))
	if err != nil || string(data) != "# 周报\n" {
		t.Fatalf("extracted skill = %q, err %v", data, err)
	}
}

func TestExtractPackageRejections(t *testing.T) {
	cases := []struct {
		name    string
		entries []tarEntry
		reason  string
	}{
		{"symlink", append(validTarEntries(), tarEntry{name: "link", typeflag: tar.TypeSymlink, linkname: "/etc/passwd"}), "不允许符号链接"},
		{"hardlink", append(validTarEntries(), tarEntry{name: "hard", typeflag: tar.TypeLink, linkname: "manifest.json"}), "不允许硬链接"},
		{"device", append(validTarEntries(), tarEntry{name: "dev", typeflag: tar.TypeChar}), "不允许设备文件"},
		{"absolute path", append(validTarEntries(), tarEntry{name: "/abs.md", content: "x"}), "相对路径"},
		{"drive prefix", append(validTarEntries(), tarEntry{name: "C:evil.md", content: "x"}), "相对路径"},
		{"dot dot", append(validTarEntries(), tarEntry{name: "../escape.md", content: "x"}), "路径不能包含 . 或 .."},
		{"backslash", append(validTarEntries(), tarEntry{name: `skills\a.md`, content: "x"}), "反斜杠"},
		{"duplicate case", append(validTarEntries(), tarEntry{name: "MANIFEST.json", content: "{}"}), "重复"},
		{"file too large", append(validTarEntries(), tarEntry{name: "skills/big.md", content: strings.Repeat("a", MaxPackageFileSize+1)}), "单个文件超过"},
		{"too many entries", manyEntries(MaxPackageEntries+1, 0), "条目数超过"},
		{"total too large", manyEntries(12, MaxPackageFileSize), "总体积超过"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "staging")
			err := ExtractPackage(bytes.NewReader(buildTar(t, tc.entries)), dest)
			if err == nil {
				t.Fatal("ExtractPackage() expected an error")
			}
			if tc.reason != "" && !strings.Contains(err.Error(), tc.reason) {
				t.Errorf("error = %v, want reason %q", err, tc.reason)
			}
		})
	}
}

// manyEntries returns a package with n files of the given size each, on top of
// the valid entries.
func manyEntries(n int, size int) []tarEntry {
	entries := validTarEntries()
	content := strings.Repeat("a", size)
	for i := 0; len(entries) < n+3; i++ {
		entries = append(entries, tarEntry{
			name:    "skills/x" + string(rune('a'+i%26)) + string(rune('a'+i/26%26)) + ".md",
			content: content,
		})
	}
	return entries
}

func TestCopyDirectoryValid(t *testing.T) {
	src := writeManifestDir(t, validManifestJSON(), map[string]string{"skills/report.md": "# 周报\n"})
	dest := filepath.Join(t.TempDir(), "staging")
	if err := CopyDirectory(src, dest); err != nil {
		t.Fatalf("CopyDirectory() error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "skills", "report.md")); err != nil {
		t.Fatalf("copied skill missing: %v", err)
	}
}

func TestCopyDirectoryRejections(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		src := writeManifestDir(t, validManifestJSON(), map[string]string{"skills/report.md": "# 周报\n"})
		if err := os.Symlink("/etc/passwd", filepath.Join(src, "link")); err != nil {
			t.Skipf("cannot create symlink: %v", err)
		}
		err := CopyDirectory(src, filepath.Join(t.TempDir(), "staging"))
		if err == nil || !strings.Contains(err.Error(), "不允许符号链接") {
			t.Fatalf("CopyDirectory() error = %v, want symlink rejection", err)
		}
	})
	t.Run("not a directory", func(t *testing.T) {
		src := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(src, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := CopyDirectory(src, filepath.Join(t.TempDir(), "staging")); err == nil {
			t.Fatal("CopyDirectory() expected an error")
		}
	})
}

func TestValidateLayout(t *testing.T) {
	m := &Manifest{
		ID:     "demo",
		Skills: []string{"skills/a.md"},
		Icon:   "icon.png",
	}
	t.Run("valid", func(t *testing.T) {
		dir := writeManifestDir(t, validManifestJSON(), map[string]string{
			"skills/a.md": "# 技能\n",
			"icon.png":    "png",
		})
		if err := validateLayout(dir, m); err != nil {
			t.Fatalf("validateLayout() error: %v", err)
		}
	})
	t.Run("unknown file", func(t *testing.T) {
		dir := writeManifestDir(t, validManifestJSON(), map[string]string{"skills/a.md": "# 技能\n"})
		if err := os.WriteFile(filepath.Join(dir, "extra.txt"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		err := validateLayout(dir, m)
		if err == nil || !strings.Contains(err.Error(), "不允许包含该文件") {
			t.Fatalf("validateLayout() error = %v, want unknown file rejection", err)
		}
	})
	t.Run("executable", func(t *testing.T) {
		dir := writeManifestDir(t, validManifestJSON(), map[string]string{"skills/a.md": "# 技能\n"})
		if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		err := validateLayout(dir, m)
		if err == nil || !strings.Contains(err.Error(), "可执行文件或脚本") {
			t.Fatalf("validateLayout() error = %v, want executable rejection", err)
		}
	})
}

func TestHashTree(t *testing.T) {
	dir := writeManifestDir(t, validManifestJSON(), map[string]string{"skills/report.md": "# 周报\n"})
	a, err := hashTree(dir)
	if err != nil {
		t.Fatalf("hashTree() error: %v", err)
	}
	b, err := hashTree(dir)
	if err != nil || a != b {
		t.Fatalf("hashTree() not deterministic: %q vs %q (err %v)", a, b, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skills", "report.md"), []byte("# 变了\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := hashTree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if a == c {
		t.Error("hashTree() did not change after a content edit")
	}
}
