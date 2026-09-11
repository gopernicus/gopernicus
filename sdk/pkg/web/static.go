package web

import (
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"sync"
)

// StaticFileServer serves static files from an fs.FS with optional SPA
// fallback and asset caching.
type StaticFileServer struct {
	fileFS      fs.FS
	assetPrefix string
	spaMode     bool
}

// StaticOption configures a StaticFileServer at construction.
type StaticOption func(*staticConfig)

type staticConfig struct {
	assetPrefix string
	spaMode     bool
}

type staticResponseWriter struct {
	http.ResponseWriter
}

type staticReadSeeker struct {
	io.ReadSeeker
	mu  sync.Mutex
	err error
}

// WithAssetPrefix sets the path prefix for immutable asset caching. Files under
// this prefix are served with Cache-Control: public, max-age=31536000,
// immutable. Default is "assets/".
func WithAssetPrefix(prefix string) StaticOption {
	return func(c *staticConfig) {
		c.assetPrefix = prefix
	}
}

// WithSPAMode enables SPA fallback. When enabled, a missing path, a directory,
// or the root serves index.html instead of returning 404, allowing client-side
// routing. index.html is served with no-store headers.
func WithSPAMode() StaticOption {
	return func(c *staticConfig) {
		c.spaMode = true
	}
}

// NewStaticFileServer creates a static file server from an fs.FS.
// A nil option panics.
func NewStaticFileServer(fileFS fs.FS, opts ...StaticOption) *StaticFileServer {
	cfg := staticConfig{assetPrefix: "assets/"}
	for _, opt := range opts {
		if opt == nil {
			panic("web.NewStaticFileServer: nil option")
		}
		opt(&cfg)
	}
	return &StaticFileServer{fileFS: fileFS, assetPrefix: cfg.assetPrefix, spaMode: cfg.spaMode}
}

// AddRoutes registers the file server on the handler under basePath. It serves
// GET {basePath}/{path...} and, when basePath is non-empty, redirects the
// bare basePath to its trailing-slash form.
func (s *StaticFileServer) AddRoutes(handler *WebHandler, basePath string, middleware ...Middleware) {
	basePath = strings.TrimSuffix(basePath, "/")

	if basePath == "" {
		handler.Handle("GET", "/{path...}", s.ServeHTTP, middleware...)
		return
	}

	serve := http.StripPrefix(basePath, s)
	handler.Handle("GET", basePath+"/{path...}", serve.ServeHTTP, middleware...)
	handler.Handle("GET", basePath, func(w http.ResponseWriter, r *http.Request) {
		RespondRedirect(w, r, basePath+"/", http.StatusMovedPermanently)
	}, middleware...)
}

// ServeHTTP serves a static file. In SPA mode a missing path, a directory, or
// the root falls back to index.html; otherwise those cases return 404.
// Files implementing io.ReadSeeker support ranges and conditional requests
// through http.ServeContent. Other files are streamed without range support.
func (s *StaticFileServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	cleanPath := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")

	if cleanPath == "" {
		s.serveMissing(w, r, cleanPath)
		return
	}
	s.serveFile(w, r, cleanPath)
}

func (s *StaticFileServer) serveFile(w http.ResponseWriter, r *http.Request, name string) {
	f, err := s.fileFS.Open(name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			s.serveMissing(w, r, name)
		} else {
			RecordError(w, err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
		return
	}
	defer func() {
		if err := f.Close(); err != nil {
			RecordError(w, err)
		}
	}()

	stat, err := f.Stat()
	if err != nil {
		RecordError(w, err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if stat.IsDir() {
		s.serveMissing(w, r, name)
		return
	}

	if s.spaMode && name == "index.html" {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	} else if s.assetPrefix != "" && strings.HasPrefix(name, s.assetPrefix) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}

	w.Header().Set("Content-Type", staticContentType(name))

	if seeker, ok := f.(io.ReadSeeker); ok {
		// ServeContent does not return its read, seek, or write errors.
		content := &staticReadSeeker{ReadSeeker: seeker}
		http.ServeContent(staticResponseWriter{w}, r, stat.Name(), stat.ModTime(), content)
		RecordError(w, content.readError())
		return
	}
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		if _, err := io.Copy(w, f); err != nil {
			RecordError(w, err)
		}
	}
}

// serveMissing handles a path that resolves to nothing servable. In SPA mode it
// serves index.html; otherwise it returns 404.
func (s *StaticFileServer) serveMissing(w http.ResponseWriter, r *http.Request, name string) {
	if s.spaMode && name != "index.html" {
		s.serveFile(w, r, "index.html")
		return
	}
	http.NotFound(w, r)
}

// staticContentType returns the MIME type for a file based on its extension.
func staticContentType(filePath string) string {
	if contentType := mime.TypeByExtension(path.Ext(filePath)); contentType != "" {
		return contentType
	}
	return "application/octet-stream"
}

func (w staticResponseWriter) Write(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		RecordError(w.ResponseWriter, err)
	}
	return n, err
}

func (w staticResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (f *staticReadSeeker) Read(data []byte) (int, error) {
	n, err := f.ReadSeeker.Read(data)
	// ServeContent bounds reads to the selected content length. An earlier
	// EOF means the file ended before the promised response was complete.
	if err == io.EOF && n < len(data) {
		err = io.ErrUnexpectedEOF
	}
	if err != nil && err != io.EOF {
		f.recordError(err)
	}
	return n, err
}

func (f *staticReadSeeker) Seek(offset int64, whence int) (int64, error) {
	n, err := f.ReadSeeker.Seek(offset, whence)
	if err != nil {
		f.recordError(err)
	}
	return n, err
}

// Multipart ranges read in ServeContent's goroutine. Collect the first file
// error here and offer it to the response recorder in the serving goroutine.
func (f *staticReadSeeker) recordError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err == nil {
		f.err = err
	}
}

func (f *staticReadSeeker) readError() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}
