package store

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func newProtocolFixture(t *testing.T) *ProtocolHandler {
	t.Helper()
	appsDir := t.TempDir()
	writeAppFiles(t, filepath.Join(appsDir, "docs"), validManifest(), 16)
	if err := os.WriteFile(filepath.Join(appsDir, "docs", "assets", "app.js"), []byte("console.log(1)"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appsDir, "docs", "style.css"), []byte("body{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	return NewProtocolHandler(appsDir)
}

func serve(h http.Handler, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func TestProtocolServesFiles(t *testing.T) {
	h := newProtocolFixture(t)
	tests := []struct {
		path        string
		contentType string
		body        string
	}{
		{"/docs/index.html", "text/html; charset=utf-8", "<html></html>"},
		{"/docs/assets/app.js", "text/javascript; charset=utf-8", "console.log(1)"},
		{"/docs/style.css", "text/css; charset=utf-8", "body{}"},
		{"/docs/icon.svg", "image/svg+xml", ""},
	}
	for _, tt := range tests {
		rec := serve(h, http.MethodGet, ProtocolScheme+"://localhost"+tt.path)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", tt.path, rec.Code)
		}
		if got := rec.Header().Get("Content-Type"); got != tt.contentType {
			t.Errorf("GET %s content type = %q, want %q", tt.path, got, tt.contentType)
		}
		if tt.body != "" && rec.Body.String() != tt.body {
			t.Errorf("GET %s body = %q, want %q", tt.path, rec.Body.String(), tt.body)
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("GET %s cache control = %q", tt.path, got)
		}
	}
}

func TestProtocolHead(t *testing.T) {
	h := newProtocolFixture(t)
	rec := serve(h, http.MethodHead, ProtocolScheme+"://localhost/docs/index.html")
	if rec.Code != http.StatusOK {
		t.Fatalf("HEAD = %d, want 200", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("HEAD body = %q, want empty", rec.Body.String())
	}
	if rec.Header().Get("Content-Length") == "" {
		t.Error("HEAD is missing Content-Length")
	}
}

func TestProtocolNotFound(t *testing.T) {
	h := newProtocolFixture(t)
	paths := []string{
		"/docs/",
		"/docs",
		"/docs/missing.html",
		"/unknown/index.html",
		"/DOCS/index.html",
		"/docs/../docs/index.html",
		"/docs//index.html",
		"/docs/%2e%2e/index.html",
		"/docs/assets",
	}
	for _, path := range paths {
		rec := serve(h, http.MethodGet, ProtocolScheme+"://localhost"+path)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, rec.Code)
		}
	}
	rec := serve(h, http.MethodGet, ProtocolScheme+"://localhost/"+ManifestFileName)
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET root manifest = %d, want 404", rec.Code)
	}
}

func TestProtocolMethodNotAllowed(t *testing.T) {
	h := newProtocolFixture(t)
	rec := serve(h, http.MethodPost, ProtocolScheme+"://localhost/docs/index.html")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST = %d, want 405", rec.Code)
	}
	if rec.Header().Get("Allow") != "GET, HEAD" {
		t.Errorf("Allow = %q", rec.Header().Get("Allow"))
	}
}

func TestProtocolRejectsUnknownApp(t *testing.T) {
	appsDir := t.TempDir()
	h := NewProtocolHandler(appsDir)
	rec := serve(h, http.MethodGet, ProtocolScheme+"://localhost/docs/index.html")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown app = %d, want 404", rec.Code)
	}
}

func TestAppURLs(t *testing.T) {
	if got, want := AppRootURL("docs"), "bkapp://localhost/docs/"; got != want {
		t.Errorf("AppRootURL = %q, want %q", got, want)
	}
	if got, want := AppURL("docs", "index.html"), "bkapp://localhost/docs/index.html"; got != want {
		t.Errorf("AppURL = %q, want %q", got, want)
	}
	if got, want := AppURL("docs", "assets/app.js"), "bkapp://localhost/docs/assets/app.js"; got != want {
		t.Errorf("AppURL = %q, want %q", got, want)
	}
	if got, want := AppURL("docs", "/index.html"), "bkapp://localhost/docs/index.html"; got != want {
		t.Errorf("AppURL with a leading slash = %q, want %q", got, want)
	}
}

func TestContentTypeFor(t *testing.T) {
	tests := map[string]string{
		"index.html": "text/html; charset=utf-8",
		"app.js":     "text/javascript; charset=utf-8",
		"f.woff2":    "font/woff2",
		"game.wasm":  "application/wasm",
		"data.bin":   "application/octet-stream",
	}
	for name, want := range tests {
		if got := ContentTypeFor(name); got != want {
			t.Errorf("ContentTypeFor(%q) = %q, want %q", name, got, want)
		}
	}
}
