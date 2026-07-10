package web

import (
	"io"
	"io/fs"
	"net/http"
	"path"
	"path/filepath"
	"strings"
)

// StaticFileServer serves static files from an fs.FS with optional SPA
// fallback and asset caching.
type StaticFileServer struct {
	fileFS      fs.FS
	assetPrefix string
	spaMode     bool
}

// StaticOption configures a StaticFileServer.
type StaticOption func(*StaticFileServer)

// WithAssetPrefix sets the path prefix for immutable asset caching. Files under
// this prefix are served with Cache-Control: public, max-age=31536000,
// immutable. Default is "assets/".
func WithAssetPrefix(prefix string) StaticOption {
	return func(s *StaticFileServer) {
		s.assetPrefix = prefix
	}
}

// WithSPAMode enables SPA fallback. When enabled, a missing path, a directory,
// or the root serves index.html instead of returning 404, allowing client-side
// routing. index.html is served with no-store headers.
func WithSPAMode() StaticOption {
	return func(s *StaticFileServer) {
		s.spaMode = true
	}
}

// NewStaticFileServer creates a static file server from an fs.FS.
func NewStaticFileServer(fileFS fs.FS, opts ...StaticOption) *StaticFileServer {
	s := &StaticFileServer{
		fileFS:      fileFS,
		assetPrefix: "assets/",
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// AddRoutes registers the file server on the handler under basePath. It serves
// GET {basePath}/{path...} and, when basePath is non-empty, redirects the
// bare basePath to its trailing-slash form.
func (s *StaticFileServer) AddRoutes(handler *WebHandler, basePath string, middleware ...Middleware) {
	basePath = strings.TrimSuffix(basePath, "/")

	serve := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.ServeHTTP(w, r)
	})

	if basePath == "" {
		handler.Handle("GET", "/{path...}", serve, middleware...)
		return
	}

	handler.Handle("GET", basePath+"/{path...}", serve, middleware...)
	handler.Handle("GET", basePath, func(w http.ResponseWriter, r *http.Request) {
		RespondRedirect(w, r, basePath+"/", http.StatusMovedPermanently)
	}, middleware...)
}

// ServeHTTP serves a static file. In SPA mode a missing path, a directory, or
// the root falls back to index.html; otherwise those cases return 404.
func (s *StaticFileServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	urlPath := r.PathValue("path")

	cleanPath := path.Clean(urlPath)
	cleanPath = strings.TrimPrefix(cleanPath, "/")

	if cleanPath == "." || cleanPath == "" {
		s.serveMissing(w, r)
		return
	}

	f, err := s.fileFS.Open(cleanPath)
	if err != nil {
		s.serveMissing(w, r)
		return
	}

	stat, err := f.Stat()
	if err != nil {
		f.Close()
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if stat.IsDir() {
		f.Close()
		s.serveMissing(w, r)
		return
	}

	defer f.Close()

	// Immutable caching for hashed assets.
	if s.assetPrefix != "" && strings.HasPrefix(cleanPath, s.assetPrefix) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}

	w.Header().Set("Content-Type", staticContentType(cleanPath))

	if seeker, ok := f.(io.ReadSeeker); ok {
		http.ServeContent(w, r, stat.Name(), stat.ModTime(), seeker)
		return
	}
	w.WriteHeader(http.StatusOK)
	io.Copy(w, f)
}

// serveMissing handles a path that resolves to nothing servable. In SPA mode it
// serves index.html; otherwise it returns 404.
func (s *StaticFileServer) serveMissing(w http.ResponseWriter, r *http.Request) {
	if s.spaMode {
		s.serveIndex(w, r)
		return
	}
	http.NotFound(w, r)
}

func (s *StaticFileServer) serveIndex(w http.ResponseWriter, r *http.Request) {
	f, err := s.fileFS.Open("index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")

	if seeker, ok := f.(io.ReadSeeker); ok {
		http.ServeContent(w, r, "index.html", stat.ModTime(), seeker)
		return
	}
	w.WriteHeader(http.StatusOK)
	io.Copy(w, f)
}

// staticContentType returns the MIME type for a file based on its extension.
func staticContentType(filePath string) string {
	switch strings.ToLower(filepath.Ext(filePath)) {
	case ".js", ".mjs":
		return "application/javascript"
	case ".css":
		return "text/css"
	case ".html":
		return "text/html; charset=utf-8"
	case ".json":
		return "application/json"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".ico":
		return "image/x-icon"
	case ".webp":
		return "image/webp"
	case ".woff":
		return "font/woff"
	case ".woff2":
		return "font/woff2"
	case ".ttf":
		return "font/ttf"
	case ".eot":
		return "application/vnd.ms-fontobject"
	case ".map":
		return "application/json"
	default:
		return "application/octet-stream"
	}
}
