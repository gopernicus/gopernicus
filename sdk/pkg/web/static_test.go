package web

import (
	"io/fs"
	"mime"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func newTestSPA() *WebHandler {
	files := fstest.MapFS{
		"index.html":       &fstest.MapFile{Data: []byte("<html>SPA</html>")},
		"assets/app.js":    &fstest.MapFile{Data: []byte("console.log('app')")},
		"assets/style.css": &fstest.MapFile{Data: []byte("body{}")},
		"favicon.ico":      &fstest.MapFile{Data: []byte("icon")},
	}

	static := NewStaticFileServer(files, WithSPAMode())
	h := NewWebHandler()
	static.AddRoutes(h, "")
	return h
}

func newTestStatic() *WebHandler {
	files := fstest.MapFS{
		"assets/app.js": &fstest.MapFile{Data: []byte("console.log('app')")},
		"favicon.ico":   &fstest.MapFile{Data: []byte("icon")},
	}

	static := NewStaticFileServer(files)
	h := NewWebHandler()
	static.AddRoutes(h, "")
	return h
}

// --- SPA mode ---

func TestStaticFileServer_SPA_ServeFile(t *testing.T) {
	h := newTestSPA()

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/favicon.ico", nil))

	if w.Code != http.StatusOK {
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

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "SPA") {
		t.Errorf("body = %q, want index.html content", w.Body.String())
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q, want no-store for index", cc)
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
	files := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html>Dashboard</html>")},
	}

	static := NewStaticFileServer(files, WithSPAMode())
	h := NewWebHandler()
	static.AddRoutes(h, "/dashboard")

	// Base path without trailing slash should redirect.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/dashboard", nil))
	if w.Code != http.StatusMovedPermanently {
		t.Errorf("status = %d, want 301", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/dashboard/" {
		t.Errorf("Location = %q, want /dashboard/", loc)
	}

	// Base path with trailing slash should serve index.
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, httptest.NewRequest("GET", "/dashboard/", nil))
	if !strings.Contains(w2.Body.String(), "Dashboard") {
		t.Errorf("body = %q, want Dashboard", w2.Body.String())
	}
}

func TestStaticFileServer_SPA_CustomAssetPrefix(t *testing.T) {
	files := fstest.MapFS{
		"index.html":       &fstest.MapFile{Data: []byte("<html></html>")},
		"static/bundle.js": &fstest.MapFile{Data: []byte("js")},
	}

	static := NewStaticFileServer(files, WithSPAMode(), WithAssetPrefix("static/"))
	h := NewWebHandler()
	static.AddRoutes(h, "")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/static/bundle.js", nil))

	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control = %q, want immutable for custom asset prefix", cc)
	}
}

func TestStaticFileServer_SPA_DirectoryFallback(t *testing.T) {
	files := fstest.MapFS{
		"index.html":       &fstest.MapFile{Data: []byte("<html>SPA</html>")},
		"assets/app.js":    &fstest.MapFile{Data: []byte("console.log('app')")},
		"assets/style.css": &fstest.MapFile{Data: []byte("body{}")},
		"docs/x.txt":       &fstest.MapFile{Data: []byte("doc")},
	}

	static := NewStaticFileServer(files, WithSPAMode())
	h := NewWebHandler()
	static.AddRoutes(h, "")

	// A directory outside the asset prefix falls back to index.html.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/docs", nil))

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "SPA") {
		t.Errorf("body = %q, want index.html content", w.Body.String())
	}

	// The asset directory itself is never the page.
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/assets", nil))

	if w.Code != http.StatusNotFound {
		t.Errorf("/assets status = %d, want 404", w.Code)
	}
	if strings.Contains(w.Body.String(), "SPA") {
		t.Errorf("/assets body = %q, want no index.html content", w.Body.String())
	}
}

func TestStaticFileServer_SPA_RangeRequest(t *testing.T) {
	files := fstest.MapFS{
		"index.html":    &fstest.MapFile{Data: []byte("<html>SPA</html>")},
		"assets/app.js": &fstest.MapFile{Data: []byte("0123456789")},
	}

	static := NewStaticFileServer(files, WithSPAMode())
	h := NewWebHandler()
	static.AddRoutes(h, "")

	req := httptest.NewRequest("GET", "/assets/app.js", nil)
	req.Header.Set("Range", "bytes=2-5")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusPartialContent {
		t.Errorf("status = %d, want 206", w.Code)
	}
	if body := w.Body.String(); body != "2345" {
		t.Errorf("body = %q, want 2345", body)
	}
}

// --- WithIndex, reserved prefixes, asset hard-404 ---

const staticNoStore = "no-cache, no-store, must-revalidate"

func serveStatic(h http.Handler, method, target string, header map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, nil)
	for k, v := range header {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func assertNoStore404(t *testing.T, w *httptest.ResponseRecorder, forbidden ...string) {
	t.Helper()
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}
	if cc := w.Header().Get("Cache-Control"); cc != staticNoStore {
		t.Errorf("Cache-Control = %q, want %q", cc, staticNoStore)
	}
	for _, f := range forbidden {
		if strings.Contains(w.Body.String(), f) {
			t.Errorf("body = %q, must not contain %q", w.Body.String(), f)
		}
	}
}

func TestStaticFileServer_WithIndex(t *testing.T) {
	const injected = "<html>INJECTED</html>"
	files := fstest.MapFS{
		"index.html":    &fstest.MapFile{Data: []byte("<html>FROM FS</html>")},
		"assets/app.js": &fstest.MapFile{Data: []byte("js")},
	}
	h := NewStaticFileServer(files, WithSPAMode(), WithIndex([]byte(injected)))

	for _, target := range []string{"/index.html", "/", "/client-route"} {
		t.Run("GET "+target, func(t *testing.T) {
			w := serveStatic(h, http.MethodGet, target, nil)
			if w.Code != http.StatusOK || w.Body.String() != injected {
				t.Fatalf("status = %d, body = %q", w.Code, w.Body.String())
			}
			if cl := w.Header().Get("Content-Length"); cl != strconv.Itoa(len(injected)) {
				t.Errorf("Content-Length = %q, want %d", cl, len(injected))
			}
			if cc := w.Header().Get("Cache-Control"); cc != staticNoStore {
				t.Errorf("Cache-Control = %q", cc)
			}
			if ct := w.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
				t.Errorf("Content-Type = %q", ct)
			}
			if lm := w.Header().Get("Last-Modified"); lm != "" {
				t.Errorf("Last-Modified = %q, want none", lm)
			}
		})
	}

	t.Run("HEAD /", func(t *testing.T) {
		w := serveStatic(h, http.MethodHead, "/", nil)
		if w.Code != http.StatusOK || w.Body.Len() != 0 {
			t.Fatalf("status = %d, body = %q", w.Code, w.Body.String())
		}
		if cl := w.Header().Get("Content-Length"); cl != strconv.Itoa(len(injected)) {
			t.Errorf("Content-Length = %q, want %d", cl, len(injected))
		}
	})

	for _, target := range []string{"/", "/client-route"} {
		t.Run("Range "+target, func(t *testing.T) {
			w := serveStatic(h, http.MethodGet, target, map[string]string{"Range": "bytes=0-3"})
			if w.Code != http.StatusPartialContent || w.Body.String() != injected[:4] {
				t.Fatalf("status = %d, body = %q", w.Code, w.Body.String())
			}
		})
	}

	for name, header := range map[string]map[string]string{
		"If-Modified-Since":      {"If-Modified-Since": time.Now().Add(time.Hour).UTC().Format(http.TimeFormat)},
		"If-None-Match entity":   {"If-None-Match": `"abc"`},
		"If-None-Match weak tag": {"If-None-Match": `W/"abc"`},
	} {
		t.Run(name, func(t *testing.T) {
			w := serveStatic(h, http.MethodGet, "/", header)
			if w.Code != http.StatusOK || w.Body.String() != injected {
				t.Fatalf("status = %d, body = %q", w.Code, w.Body.String())
			}
		})
	}

	// RFC 9110: "*" matches any current representation, so ServeContent
	// answers 304 even without an ETag.
	t.Run("If-None-Match star", func(t *testing.T) {
		w := serveStatic(h, http.MethodGet, "/", map[string]string{"If-None-Match": "*"})
		if w.Code != http.StatusNotModified {
			t.Fatalf("status = %d, want 304", w.Code)
		}
	})
}

func TestStaticFileServer_WithIndexNoFSIndex(t *testing.T) {
	files := fstest.MapFS{"assets/app.js": &fstest.MapFile{Data: []byte("js")}}
	h := NewStaticFileServer(files, WithSPAMode(), WithIndex([]byte("INDEX")))
	for _, target := range []string{"/", "/index.html", "/client/route"} {
		w := serveStatic(h, http.MethodGet, target, nil)
		if w.Code != http.StatusOK || w.Body.String() != "INDEX" {
			t.Errorf("%s: status = %d, body = %q", target, w.Code, w.Body.String())
		}
	}
}

func TestStaticFileServer_WithIndexCopiesInput(t *testing.T) {
	input := []byte("original")
	opt := WithIndex(input)
	copy(input, "MUTATED!")
	h := NewStaticFileServer(fstest.MapFS{}, WithSPAMode(), opt)
	copy(input, "AGAIN!!!")
	if w := serveStatic(h, http.MethodGet, "/", nil); w.Body.String() != "original" {
		t.Fatalf("body = %q, want original", w.Body.String())
	}

	input = []byte("original")
	h = NewStaticFileServer(fstest.MapFS{}, WithSPAMode(), WithIndex(input))
	copy(input, "MUTATED!")
	if w := serveStatic(h, http.MethodGet, "/", nil); w.Body.String() != "original" {
		t.Fatalf("after construction: body = %q, want original", w.Body.String())
	}
}

func TestStaticFileServer_WithIndexWithoutSPA(t *testing.T) {
	files := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("from fs")}}
	h := NewStaticFileServer(files, WithIndex([]byte("bytes")))

	w := serveStatic(h, http.MethodGet, "/index.html", nil)
	if w.Code != http.StatusOK || w.Body.String() != "bytes" {
		t.Fatalf("/index.html: status = %d, body = %q", w.Code, w.Body.String())
	}
	if cc := w.Header().Get("Cache-Control"); cc != "" {
		t.Errorf("/index.html Cache-Control = %q, want none outside SPA mode", cc)
	}
	for _, target := range []string{"/", "/missing"} {
		if w := serveStatic(h, http.MethodGet, target, nil); w.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", target, w.Code)
		}
	}
}

func TestStaticFileServer_ReservedPrefixes(t *testing.T) {
	const page = "<html>PAGE</html>"
	files := fstest.MapFS{
		"index.html":   &fstest.MapFile{Data: []byte("<html>FS INDEX</html>")},
		"api/real.txt": &fstest.MapFile{Data: []byte("real")},
	}
	h := NewStaticFileServer(files, WithSPAMode(), WithIndex([]byte(page)), WithReservedPrefixes("/api/", "/healthz"))

	for _, target := range []string{"/api", "/api/", "/api/x", "/api/real.txt", "/healthz", "/healthz/live"} {
		t.Run("reserved "+target, func(t *testing.T) {
			assertNoStore404(t, serveStatic(h, http.MethodGet, target, nil), "PAGE", "FS INDEX", "real")
		})
	}
	for _, target := range []string{"/healthzz", "/apix"} {
		t.Run("page "+target, func(t *testing.T) {
			w := serveStatic(h, http.MethodGet, target, nil)
			if w.Code != http.StatusOK || w.Body.String() != page {
				t.Fatalf("status = %d, body = %q", w.Code, w.Body.String())
			}
		})
	}

	// ServeHTTP called directly sees uncleaned paths.
	for _, target := range []string{"//api/x", "/x/../api/y"} {
		t.Run("direct "+target, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.URL.Path = target
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			assertNoStore404(t, w, "PAGE")
		})
	}
}

func TestStaticFileServer_ReservedBeforeFilesystem(t *testing.T) {
	files := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("INDEX")}}
	fileFS := staticOpenFunc(func(name string) (fs.File, error) {
		if name == "api" || strings.HasPrefix(name, "api/") {
			t.Fatalf("filesystem consulted for reserved path %q", name)
		}
		return files.Open(name)
	})
	h := NewStaticFileServer(fileFS, WithSPAMode(), WithReservedPrefixes("/api"))
	for _, target := range []string{"/api", "/api/x"} {
		assertNoStore404(t, serveStatic(h, http.MethodGet, target, nil), "INDEX")
	}
}

func TestStaticFileServer_ReservedWithoutSPA(t *testing.T) {
	files := fstest.MapFS{"api/real.txt": &fstest.MapFile{Data: []byte("real")}}
	h := NewStaticFileServer(files, WithReservedPrefixes("/api/"))
	assertNoStore404(t, serveStatic(h, http.MethodGet, "/api/real.txt", nil), "real")
}

func TestStaticFileServer_ReservedMounted(t *testing.T) {
	files := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("INDEX")}}
	h := NewWebHandler()
	NewStaticFileServer(files, WithSPAMode(), WithReservedPrefixes("/api/")).AddRoutes(h, "/app")

	assertNoStore404(t, serveStatic(h, http.MethodGet, "/app/api/x", nil), "INDEX")
	if w := serveStatic(h, http.MethodGet, "/app/other", nil); w.Code != http.StatusOK || w.Body.String() != "INDEX" {
		t.Fatalf("/app/other: status = %d, body = %q", w.Code, w.Body.String())
	}
}

func TestStaticFileServer_ReservedInsideAssetZone(t *testing.T) {
	files := fstest.MapFS{
		"index.html":        &fstest.MapFile{Data: []byte("INDEX")},
		"assets/app.js":     &fstest.MapFile{Data: []byte("app")},
		"assets/goth/x.css": &fstest.MapFile{Data: []byte("goth")},
	}
	h := NewStaticFileServer(files, WithSPAMode(), WithReservedPrefixes("/assets/goth/"))

	assertNoStore404(t, serveStatic(h, http.MethodGet, "/assets/goth/x.css", nil), "goth", "INDEX")
	w := serveStatic(h, http.MethodGet, "/assets/app.js", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("/assets/app.js: status = %d, Cache-Control = %q", w.Code, w.Header().Get("Cache-Control"))
	}
}

func TestStaticFileServer_ReservedLastCallWins(t *testing.T) {
	files := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("INDEX")}}
	h := NewStaticFileServer(files, WithSPAMode(), WithReservedPrefixes("/old"), WithReservedPrefixes("/new"))

	if w := serveStatic(h, http.MethodGet, "/old/x", nil); w.Code != http.StatusOK || w.Body.String() != "INDEX" {
		t.Errorf("/old/x: status = %d, body = %q", w.Code, w.Body.String())
	}
	assertNoStore404(t, serveStatic(h, http.MethodGet, "/new/x", nil), "INDEX")
}

func TestStaticFileServer_SPA_AssetHard404(t *testing.T) {
	const page = "<html>SPA</html>"
	files := fstest.MapFS{
		"index.html":       &fstest.MapFile{Data: []byte(page)},
		"assets/app.js":    &fstest.MapFile{Data: []byte("app")},
		"assets/sub/x.js":  &fstest.MapFile{Data: []byte("x")},
		"static/bundle.js": &fstest.MapFile{Data: []byte("bundle")},
	}
	h := NewStaticFileServer(files, WithSPAMode())

	for _, target := range []string{"/assets/index-OLD.js", "/assets", "/assets/", "/assets/sub/"} {
		t.Run(target, func(t *testing.T) {
			w := serveStatic(h, http.MethodGet, target, nil)
			assertNoStore404(t, w, "SPA")
			if strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
				t.Errorf("Cache-Control = %q, must not be immutable", w.Header().Get("Cache-Control"))
			}
		})
	}
	if w := serveStatic(h, http.MethodGet, "/elsewhere", nil); w.Code != http.StatusOK || w.Body.String() != page {
		t.Errorf("/elsewhere: status = %d, body = %q", w.Code, w.Body.String())
	}

	custom := NewStaticFileServer(files, WithSPAMode(), WithAssetPrefix("static"))
	assertNoStore404(t, serveStatic(custom, http.MethodGet, "/static/x.js", nil), "SPA")
	for _, target := range []string{"/static-info", "/statistics", "/assets/index-OLD.js"} {
		if w := serveStatic(custom, http.MethodGet, target, nil); w.Code != http.StatusOK || w.Body.String() != page {
			t.Errorf("static prefix %s: status = %d, body = %q", target, w.Code, w.Body.String())
		}
	}

	off := NewStaticFileServer(files, WithSPAMode(), WithAssetPrefix(""))
	if w := serveStatic(off, http.MethodGet, "/assets/x.js", nil); w.Code != http.StatusOK || w.Body.String() != page {
		t.Errorf("empty prefix: status = %d, body = %q, want page", w.Code, w.Body.String())
	}

	mounted := NewWebHandler()
	NewStaticFileServer(files, WithSPAMode()).AddRoutes(mounted, "/app")
	assertNoStore404(t, serveStatic(mounted, http.MethodGet, "/app/assets/old.js", nil), "SPA")
}

func TestStaticFileServer_NoSPA_AssetMissingUnchanged(t *testing.T) {
	h := newTestStatic()
	w := serveStatic(h, http.MethodGet, "/assets/index-OLD.js", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "" {
		t.Errorf("Cache-Control = %q, want none outside SPA mode", cc)
	}
}

// --- Non-SPA mode ---

func TestStaticFileServer_NoSPA_ServeFile(t *testing.T) {
	h := newTestStatic()

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/favicon.ico", nil))

	if w.Code != http.StatusOK {
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

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestStaticFileServer_NoSPA_RootPath404(t *testing.T) {
	h := newTestStatic()

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (no SPA mode, no index)", w.Code)
	}
}

func TestStaticContentType(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"app.js", mime.TypeByExtension(".js")},
		{"style.css", "text/css; charset=utf-8"},
		{"index.html", "text/html; charset=utf-8"},
		{"data.json", "application/json"},
		{"logo.svg", "image/svg+xml"},
		{"photo.png", "image/png"},
		{"photo.jpg", "image/jpeg"},
		{"font.woff2", "font/woff2"},
		{"unknown.gopernicus-unknown", "application/octet-stream"},
	}

	for _, tt := range tests {
		if got := staticContentType(tt.path); got != tt.want {
			t.Errorf("staticContentType(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}
