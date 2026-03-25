package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func newTestSPA() *WebHandler {
	fs := fstest.MapFS{
		"index.html":       &fstest.MapFile{Data: []byte("<html>SPA</html>")},
		"assets/app.js":    &fstest.MapFile{Data: []byte("console.log('app')")},
		"assets/style.css": &fstest.MapFile{Data: []byte("body{}")},
		"favicon.ico":      &fstest.MapFile{Data: []byte("icon")},
	}

	static := NewStaticFileServer(fs, WithSPAMode())
	h := NewWebHandler()
	static.AddRoutes(h, "")
	return h
}

func newTestStatic() *WebHandler {
	fs := fstest.MapFS{
		"assets/app.js": &fstest.MapFile{Data: []byte("console.log('app')")},
		"favicon.ico":   &fstest.MapFile{Data: []byte("icon")},
	}

	static := NewStaticFileServer(fs)
	h := NewWebHandler()
	static.AddRoutes(h, "")
	return h
}

// --- SPA mode tests ---

func TestStaticFileServer_SPA_ServeFile(t *testing.T) {
	h := newTestSPA()

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/favicon.ico", nil))

	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "icon") {
		t.Errorf("body = %q, want icon data", w.Body.String())
	}
}

func TestStaticFileServer_SPA_AssetCaching(t *testing.T) {
	h := newTestSPA()

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/assets/app.js", nil))

	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control = %q, want immutable for assets", cc)
	}
}

func TestStaticFileServer_SPA_IndexFallback(t *testing.T) {
	h := newTestSPA()

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/some/client/route", nil))

	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "SPA") {
		t.Errorf("body = %q, want index.html content", w.Body.String())
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "no-cache") {
		t.Errorf("Cache-Control = %q, want no-cache for index", cc)
	}
}

func TestStaticFileServer_SPA_RootPath(t *testing.T) {
	h := newTestSPA()

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))

	if !strings.Contains(w.Body.String(), "SPA") {
		t.Errorf("body = %q, want index.html content", w.Body.String())
	}
}

func TestStaticFileServer_SPA_WithBasePath(t *testing.T) {
	fs := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html>Dashboard</html>")},
	}

	static := NewStaticFileServer(fs, WithSPAMode())
	h := NewWebHandler()
	static.AddRoutes(h, "/dashboard")

	// Base path without trailing slash should redirect.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/dashboard", nil))
	if w.Code != http.StatusMovedPermanently {
		t.Errorf("status = %d, want 301", w.Code)
	}

	// Base path with trailing slash should serve index.
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, httptest.NewRequest("GET", "/dashboard/", nil))
	if !strings.Contains(w2.Body.String(), "Dashboard") {
		t.Errorf("body = %q, want Dashboard", w2.Body.String())
	}
}

func TestStaticFileServer_SPA_CustomAssetPrefix(t *testing.T) {
	fs := fstest.MapFS{
		"index.html":       &fstest.MapFile{Data: []byte("<html></html>")},
		"static/bundle.js": &fstest.MapFile{Data: []byte("js")},
	}

	static := NewStaticFileServer(fs, WithSPAMode(), WithAssetPrefix("static/"))
	h := NewWebHandler()
	static.AddRoutes(h, "")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/static/bundle.js", nil))

	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control = %q, want immutable for custom asset prefix", cc)
	}
}

// --- Non-SPA mode tests ---

func TestStaticFileServer_NoSPA_ServeFile(t *testing.T) {
	h := newTestStatic()

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/favicon.ico", nil))

	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "icon") {
		t.Errorf("body = %q, want icon data", w.Body.String())
	}
}

func TestStaticFileServer_NoSPA_UnknownPath404(t *testing.T) {
	h := newTestStatic()

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/does/not/exist", nil))

	if w.Code != 404 {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestStaticFileServer_NoSPA_RootPath404(t *testing.T) {
	h := newTestStatic()

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))

	if w.Code != 404 {
		t.Errorf("status = %d, want 404 (no SPA mode, no index)", w.Code)
	}
}

func TestStaticContentType(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"app.js", "application/javascript"},
		{"style.css", "text/css"},
		{"index.html", "text/html; charset=utf-8"},
		{"data.json", "application/json"},
		{"logo.svg", "image/svg+xml"},
		{"photo.png", "image/png"},
		{"photo.jpg", "image/jpeg"},
		{"font.woff2", "font/woff2"},
		{"unknown.xyz", "application/octet-stream"},
	}

	for _, tt := range tests {
		got := staticContentType(tt.path)
		if got != tt.want {
			t.Errorf("staticContentType(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}
