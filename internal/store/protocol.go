package store

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// ProtocolScheme is the custom scheme that serves installed apps. On Windows
// mygo maps it to http://bkapp.localhost internally; business code always
// builds bkapp://localhost URLs.
const ProtocolScheme = "bkapp"

// AppRootURL returns the base URL of an app, ending with a slash so that
// relative links inside the page resolve under the app.
func AppRootURL(id string) string {
	return appURL(id, "")
}

// AppURL returns the URL of entry inside the app.
func AppURL(id, entry string) string {
	return appURL(id, strings.TrimPrefix(entry, "/"))
}

func appURL(id, entry string) string {
	u := url.URL{
		Scheme: ProtocolScheme,
		Host:   "localhost",
		Path:   "/" + id + "/" + entry,
	}
	return u.String()
}

// ProtocolHandler serves files of installed apps under bkapp://localhost/<id>/.
type ProtocolHandler struct {
	// AppsDir is {DataDir}/apps.
	AppsDir string
}

// NewProtocolHandler returns the handler for the bkapp scheme.
func NewProtocolHandler(appsDir string) *ProtocolHandler {
	return &ProtocolHandler{AppsDir: appsDir}
}

// ServeHTTP serves a package file. Only GET and HEAD are allowed; a missing
// app, an invalid path and a directory all return 404.
func (h *ProtocolHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeProtocolError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id, rel, ok := splitAppPath(r.URL.Path)
	if !ok {
		writeProtocolError(w, http.StatusNotFound, "not found")
		return
	}
	appDir := filepath.Join(h.AppsDir, id)
	if info, err := os.Lstat(filepath.Join(appDir, ManifestFileName)); err != nil || !info.Mode().IsRegular() {
		writeProtocolError(w, http.StatusNotFound, "not found")
		return
	}
	target, err := resolveInRoot(appDir, rel)
	if err != nil {
		writeProtocolError(w, http.StatusNotFound, "not found")
		return
	}
	info, err := os.Lstat(target)
	if err != nil || !info.Mode().IsRegular() {
		writeProtocolError(w, http.StatusNotFound, "not found")
		return
	}
	f, err := os.Open(target)
	if err != nil {
		writeProtocolError(w, http.StatusNotFound, "not found")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", ContentTypeFor(target))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(w, f)
}

// splitAppPath extracts the app ID and the package-relative path from a
// request path such as /docs/assets/app.js. The app root itself is rejected
// because there is no directory listing.
func splitAppPath(p string) (id, rel string, ok bool) {
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return "", "", false
	}
	parts := strings.SplitN(p, "/", 2)
	id = parts[0]
	if !appIDPattern.MatchString(id) {
		return "", "", false
	}
	if len(parts) < 2 || parts[1] == "" {
		return "", "", false
	}
	rel, err := CleanRelativePath(parts[1])
	if err != nil {
		return "", "", false
	}
	return id, rel, true
}

func writeProtocolError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_, _ = io.WriteString(w, message+"\n")
}

// ContentTypeFor returns the MIME type of a package file by extension. Text
// formats carry charset=utf-8; unknown types are served as a byte stream.
func ContentTypeFor(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".txt", ".md":
		return "text/plain; charset=utf-8"
	case ".xml":
		return "application/xml; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".ico":
		return "image/x-icon"
	case ".woff":
		return "font/woff"
	case ".woff2":
		return "font/woff2"
	case ".ttf":
		return "font/ttf"
	case ".otf":
		return "font/otf"
	case ".wasm":
		return "application/wasm"
	case ".mp3":
		return "audio/mpeg"
	case ".mp4":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".pdf":
		return "application/pdf"
	default:
		return "application/octet-stream"
	}
}
