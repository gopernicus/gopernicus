package web

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

// StaticFileServer serves static files from an fs.FS with optional SPA
// fallback and asset caching.
type StaticFileServer struct {
	fileFS      fs.FS
	assetPrefix string
	spaMode     bool
	index       []byte
	reserved    []string
}

// StaticOption configures a StaticFileServer at construction.
type StaticOption func(*staticConfig)

type staticConfig struct {
	assetPrefix string
	spaMode     bool
	index       []byte
	reserved    []string
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
// immutable. Default is "assets/". The prefix is relative to the fs root, with
// no leading slash, and names a directory ending in "/". The cache rule is a
// raw prefix match; the SPA-mode 404 zone is the directory.
func WithAssetPrefix(prefix string) StaticOption {
	return func(c *staticConfig) {
		c.assetPrefix = prefix
	}
}

// WithSPAMode enables SPA fallback. When enabled, a missing path, a directory,
// or the root serves index.html instead of returning 404, allowing client-side
// routing. index.html is served with no-store headers.
//
// A missing path under the asset prefix is a 404, not index.html: a browser
// holding a stale hashed chunk must get a hard failure the app reloads from,
// not HTML parsed as a module. The 404 is no-store. A client route under the
// asset prefix is therefore unreachable by deep link; name the bundler's asset
// dir so they do not collide.
func WithSPAMode() StaticOption {
	return func(c *staticConfig) {
		c.spaMode = true
	}
}

// WithIndex serves index for index.html — and, in SPA mode, for every fallback —
// instead of the file in the fs.FS, which need not contain one. Hosts use it to
// serve an index.html rewritten once at boot (runtime config injected into a
// data block); those bytes are public. The slice is copied. Responses carry no
// ETag or Last-Modified, so conditional requests get 200, except
// If-None-Match: *, which matches any representation and gets 304. Empty
// index panics. Detecting an absent bundle stays with the host.
func WithIndex(index []byte) StaticOption {
	if len(index) == 0 {
		panic("web.WithIndex: empty index")
	}
	index = bytes.Clone(index)
	return func(c *staticConfig) {
		c.index = index
	}
}

// WithReservedPrefixes names rooted paths that belong to other mounts ("/api/",
// "/healthz"). A request at or under one answers a plain-text 404 before the
// filesystem is consulted — never index.html, even in SPA mode, and never a
// file that happens to exist there. A trailing slash is optional: "/api/" and
// "/api" both cover /api and everything under /api/. Matching is byte-exact.
// Paths are compared after AddRoutes strips its basePath, so prefixes are
// relative to the mount. The last call wins. An entry that is empty, unrooted,
// or cleans to "/" panics.
func WithReservedPrefixes(prefixes ...string) StaticOption {
	reserved := make([]string, 0, len(prefixes))
	for _, prefix := range prefixes {
		if prefix == "" {
			panic("web.WithReservedPrefixes: empty prefix")
		}
		if !strings.HasPrefix(prefix, "/") {
			panic("web.WithReservedPrefixes: prefix " + strconv.Quote(prefix) + " is not rooted")
		}
		cleaned := path.Clean(prefix)
		if cleaned == "/" {
			panic("web.WithReservedPrefixes: prefix " + strconv.Quote(prefix) + " reserves the whole mount")
		}
		reserved = append(reserved, cleaned)
	}
	return func(c *staticConfig) {
		c.reserved = reserved
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
	return &StaticFileServer{
		fileFS:      fileFS,
		assetPrefix: cfg.assetPrefix,
		spaMode:     cfg.spaMode,
		index:       bytes.Clone(cfg.index),
		reserved:    append([]string(nil), cfg.reserved...),
	}
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

// ServeHTTP serves a static file. A path at or under a reserved prefix answers
// a no-store 404 first, before the root or the filesystem is considered. In SPA
// mode a missing path, a directory, or the root falls back to index.html;
// otherwise those cases return 404. index.html is the WithIndex bytes when
// set. Files implementing io.ReadSeeker support ranges and conditional
// requests through http.ServeContent. Other files are streamed without range
// support.
func (s *StaticFileServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rooted := path.Clean("/" + r.URL.Path)
	for _, prefix := range s.reserved {
		if rooted == prefix || strings.HasPrefix(rooted, prefix+"/") {
			notFoundNoStore(w, r)
			return
		}
	}
	cleanPath := strings.TrimPrefix(rooted, "/")

	if cleanPath == "" {
		s.serveMissing(w, r, cleanPath)
		return
	}
	s.serveFile(w, r, cleanPath)
}

func (s *StaticFileServer) serveFile(w http.ResponseWriter, r *http.Request, name string) {
	if name == "index.html" && s.index != nil {
		s.setStaticHeaders(w, name)
		http.ServeContent(staticResponseWriter{w}, r, name, time.Time{}, bytes.NewReader(s.index))
		return
	}

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

	s.setStaticHeaders(w, name)

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

// setStaticHeaders sets Cache-Control and Content-Type for a servable file.
func (s *StaticFileServer) setStaticHeaders(w http.ResponseWriter, name string) {
	if s.spaMode && name == "index.html" {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	} else if s.assetPrefix != "" && strings.HasPrefix(name, s.assetPrefix) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}

	w.Header().Set("Content-Type", staticContentType(name))
}

// serveMissing handles a path that resolves to nothing servable. In SPA mode it
// serves index.html, except under the asset directory; otherwise it returns 404.
func (s *StaticFileServer) serveMissing(w http.ResponseWriter, r *http.Request, name string) {
	if s.spaMode && name != "index.html" {
		if dir := strings.TrimSuffix(s.assetPrefix, "/"); dir != "" && (name == dir || strings.HasPrefix(name, dir+"/")) {
			notFoundNoStore(w, r) // a missing or directory asset is never the page
			return
		}
		s.serveFile(w, r, "index.html")
		return
	}
	http.NotFound(w, r)
}

// notFoundNoStore answers a 404 that caches must not keep: during a rolling
// deploy the path may exist on the next instance.
func notFoundNoStore(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
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
