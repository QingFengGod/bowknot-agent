package capability

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakePlatform struct {
	appID       string
	identified  bool
	pickFile    string
	pickDir     string
	clip        string
	notifyTitle string
	notifyBody  string
	supported   bool
	opened      string
}

func newTestService(t *testing.T, fp *fakePlatform) *Service {
	t.Helper()
	if fp.appID == "" {
		fp.appID = "docs"
	}
	svc := &Service{
		AppDataDir: filepath.Join(t.TempDir(), "appdata"),
		Identify: func(context.Context) (string, bool) {
			return fp.appID, fp.identified
		},
		Platform: Platform{
			PickFile: func(context.Context) (string, error) { return fp.pickFile, nil },
			PickDirectory: func(context.Context) (string, error) {
				return fp.pickDir, nil
			},
			ReadClipboard:  func() (string, error) { return fp.clip, nil },
			WriteClipboard: func(text string) error { fp.clip = text; return nil },
			Notify: func(title, body string) error {
				fp.notifyTitle, fp.notifyBody = title, body
				return nil
			},
			SupportsNotify: func() bool { return fp.supported },
			OpenExternal:   func(target string) error { fp.opened = target; return nil },
		},
	}
	return svc
}

func assertReply(t *testing.T, r Reply, code string) {
	t.Helper()
	if code == "" {
		if !r.OK {
			t.Fatalf("reply failed: %+v", r)
		}
		return
	}
	if r.OK || r.Code != code {
		t.Fatalf("reply = %+v, want code %q", r, code)
	}
	if r.Message == "" {
		t.Error("error reply has no message")
	}
}

func TestUnauthorized(t *testing.T) {
	svc := newTestService(t, &fakePlatform{identified: false, supported: true})
	assertReply(t, svc.AppDataRead(context.Background(), "a.txt"), CodeUnauthorized)
	if got := (&Service{}).AppDataRead(context.Background(), "a.txt"); got.Code != CodeUnauthorized {
		t.Errorf("no identity = %+v", got)
	}
}

func TestAppDataRoundTrip(t *testing.T) {
	svc := newTestService(t, &fakePlatform{identified: true, supported: true})
	ctx := context.Background()
	assertReply(t, svc.AppDataWrite(ctx, "notes/a.txt", "hello"), "")
	r := svc.AppDataRead(ctx, "notes/a.txt")
	assertReply(t, r, "")
	if got := r.Data.(readResult).Content; got != "hello" {
		t.Errorf("content = %q", got)
	}
	list := svc.AppDataList(ctx, "")
	assertReply(t, list, "")
	entries := list.Data.(listResult).Entries
	if len(entries) != 1 || entries[0].Name != "notes" || !entries[0].IsDirectory {
		t.Errorf("root list = %+v", entries)
	}
	list = svc.AppDataList(ctx, "notes")
	entries = list.Data.(listResult).Entries
	if len(entries) != 1 || entries[0].Name != "a.txt" || entries[0].Size != 5 {
		t.Errorf("notes list = %+v", entries)
	}
	assertReply(t, svc.AppDataRemove(ctx, "notes/a.txt"), "")
	assertReply(t, svc.AppDataRead(ctx, "notes/a.txt"), CodeNotFound)
	assertReply(t, svc.AppDataRemove(ctx, "notes/missing.txt"), CodeNotFound)
}

func TestAppDataPathEscape(t *testing.T) {
	fp := &fakePlatform{identified: true, supported: true}
	svc := newTestService(t, fp)
	ctx := context.Background()
	for _, path := range []string{"../evil.txt", "/evil.txt", `a\b.txt`, "C:/evil.txt", "a/../../evil.txt"} {
		assertReply(t, svc.AppDataWrite(ctx, path, "x"), CodeInvalidArgument)
	}
	if _, err := os.Stat(filepath.Join(svc.AppDataDir, "..", "evil.txt")); err == nil {
		t.Error("a traversal write escaped the app data root")
	}
	assertReply(t, svc.AppDataList(ctx, "../"), CodeInvalidArgument)
}

func TestAppDataTextLimits(t *testing.T) {
	svc := newTestService(t, &fakePlatform{identified: true, supported: true})
	big := strings.Repeat("a", MaxTextSize+1)
	assertReply(t, svc.AppDataWrite(context.Background(), "big.txt", big), CodeTooLarge)
	raw := filepath.Join(svc.AppDataDir, "docs", "raw.bin")
	if err := os.MkdirAll(filepath.Dir(raw), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(raw, []byte{0xff, 0xfe}, 0o644); err != nil {
		t.Fatal(err)
	}
	assertReply(t, svc.AppDataRead(context.Background(), "raw.bin"), CodeInvalidArgument)
}

func TestPickFileAndGrant(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	file := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(file, []byte("picked"), 0o644); err != nil {
		t.Fatal(err)
	}
	fp := &fakePlatform{identified: true, supported: true, pickFile: file}
	svc := newTestService(t, fp)
	picked := svc.PickFile(ctx)
	assertReply(t, picked, "")
	pick := picked.Data.(pickResult)
	if pick.IsDirectory || pick.Name != "note.txt" || pick.Token == "" {
		t.Fatalf("pick = %+v", pick)
	}
	read := svc.ReadGranted(ctx, pick.Token, "")
	assertReply(t, read, "")
	if read.Data.(readResult).Content != "picked" {
		t.Errorf("content = %q", read.Data.(readResult).Content)
	}
	assertReply(t, svc.WriteGranted(ctx, pick.Token, "", "updated"), "")
	data, _ := os.ReadFile(file)
	if string(data) != "updated" {
		t.Errorf("file content = %q", data)
	}
	assertReply(t, svc.ReadGranted(ctx, pick.Token, "other.txt"), CodeOutOfRoot)
	assertReply(t, svc.ListGranted(ctx, pick.Token, ""), CodeInvalidArgument)
	assertReply(t, svc.ReadGranted(ctx, "bad-token", ""), CodeNotFound)

	fp.appID = "audio"
	assertReply(t, svc.ReadGranted(ctx, pick.Token, ""), CodeNotFound)

	fp.appID = "docs"
	svc.ForgetWindow("docs")
	assertReply(t, svc.ReadGranted(ctx, pick.Token, ""), CodeNotFound)
}

func TestPickDirectoryGrants(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "note.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	fp := &fakePlatform{identified: true, supported: true, pickDir: dir}
	svc := newTestService(t, fp)
	picked := svc.PickDirectory(ctx)
	assertReply(t, picked, "")
	token := picked.Data.(pickResult).Token

	list := svc.ListGranted(ctx, token, "")
	assertReply(t, list, "")
	if entries := list.Data.(listResult).Entries; len(entries) != 1 || entries[0].Name != "sub" {
		t.Errorf("root list = %+v", entries)
	}
	read := svc.ReadGranted(ctx, token, "sub/note.txt")
	assertReply(t, read, "")
	if read.Data.(readResult).Content != "hello" {
		t.Errorf("content = %q", read.Data.(readResult).Content)
	}
	assertReply(t, svc.WriteGranted(ctx, token, "sub/new.txt", "new"), "")
	data, err := os.ReadFile(filepath.Join(dir, "sub", "new.txt"))
	if err != nil || string(data) != "new" {
		t.Errorf("written file = %q, err = %v", data, err)
	}
	assertReply(t, svc.WriteGranted(ctx, token, "", "x"), CodeInvalidArgument)
	assertReply(t, svc.ReadGranted(ctx, token, "../outside.txt"), CodeInvalidArgument)
	if _, err := os.Stat(filepath.Join(dir, "..", "outside.txt")); err == nil {
		t.Error("a grant write escaped the picked directory")
	}
}

func TestGrantLimit(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := newTestService(t, &fakePlatform{identified: true, supported: true, pickFile: file})
	for i := 0; i < MaxGrantsPerApp; i++ {
		assertReply(t, svc.PickFile(context.Background()), "")
	}
	assertReply(t, svc.PickFile(context.Background()), CodeTooLarge)
}

func TestPickCancelled(t *testing.T) {
	svc := newTestService(t, &fakePlatform{identified: true, supported: true})
	r := svc.PickFile(context.Background())
	assertReply(t, r, "")
	if r.Data != nil {
		t.Errorf("cancelled pick data = %+v, want nil", r.Data)
	}
}

func TestClipboard(t *testing.T) {
	fp := &fakePlatform{identified: true, supported: true, clip: "before"}
	svc := newTestService(t, fp)
	ctx := context.Background()
	r := svc.ReadClipboardText(ctx)
	assertReply(t, r, "")
	if r.Data.(readResult).Content != "before" {
		t.Errorf("clipboard = %+v", r.Data)
	}
	assertReply(t, svc.WriteClipboardText(ctx, "after"), "")
	if fp.clip != "after" {
		t.Errorf("clipboard written = %q", fp.clip)
	}
	assertReply(t, svc.WriteClipboardText(ctx, strings.Repeat("a", MaxTextSize+1)), CodeTooLarge)
}

func TestNotify(t *testing.T) {
	fp := &fakePlatform{identified: true, supported: true}
	svc := newTestService(t, fp)
	ctx := context.Background()
	assertReply(t, svc.Notify(ctx, "标题", "正文"), "")
	if fp.notifyTitle != "标题" || fp.notifyBody != "正文" {
		t.Errorf("notify = %q / %q", fp.notifyTitle, fp.notifyBody)
	}
	assertReply(t, svc.Notify(ctx, strings.Repeat("标", MaxNotifyTitle+1), ""), CodeInvalidArgument)
	assertReply(t, svc.Notify(ctx, "标题", strings.Repeat("文", MaxNotifyBody+1)), CodeInvalidArgument)
	fp.supported = false
	assertReply(t, svc.Notify(ctx, "标题", "正文"), CodeUnsupported)
}

func TestOpenExternal(t *testing.T) {
	fp := &fakePlatform{identified: true, supported: true}
	svc := newTestService(t, fp)
	ctx := context.Background()
	assertReply(t, svc.OpenExternal(ctx, "https://example.com/a"), "")
	if fp.opened != "https://example.com/a" {
		t.Errorf("opened = %q", fp.opened)
	}
	assertReply(t, svc.OpenExternal(ctx, "mailto:hi@example.com"), "")
	for _, target := range []string{"file:///etc/passwd", "javascript:alert(1)", "ftp://example.com", "http://"} {
		got := svc.OpenExternal(ctx, target)
		if got.OK || (got.Code != CodeDenied && got.Code != CodeInvalidArgument) {
			t.Errorf("OpenExternal(%q) = %+v, want denied or invalid", target, got)
		}
	}
}

func TestPreloadScriptWiring(t *testing.T) {
	for _, want := range []string{
		"window.bowknot",
		`"BowknotCapability."`,
		`invoke("AppDataRead"`,
		`invoke("AppDataWrite"`,
		`invoke("AppDataList"`,
		`invoke("AppDataRemove"`,
		`invoke("PickFile")`,
		`invoke("PickDirectory")`,
		`invoke("ReadGranted"`,
		`invoke("WriteGranted"`,
		`invoke("ListGranted"`,
		`invoke("ReadClipboardText")`,
		`invoke("WriteClipboardText"`,
		`invoke("Notify"`,
		`invoke("OpenExternal"`,
		"error.code",
	} {
		if !strings.Contains(PreloadScript, want) {
			t.Errorf("preload script is missing %q", want)
		}
	}
}
