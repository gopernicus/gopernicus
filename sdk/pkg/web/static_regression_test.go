package web

import (
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

type staticOpenFunc func(string) (fs.File, error)

func (f staticOpenFunc) Open(name string) (fs.File, error) { return f(name) }

type staticTestFile struct {
	fs.File
	statError  error
	readError  error
	closeError error
	closed     bool
	reads      int
}

func (f *staticTestFile) Stat() (fs.FileInfo, error) {
	if f.statError != nil {
		return nil, f.statError
	}
	return f.File.Stat()
}

func (f *staticTestFile) Read(p []byte) (int, error) {
	f.reads++
	if f.readError != nil {
		return 0, f.readError
	}
	return f.File.Read(p)
}

func (f *staticTestFile) Close() error {
	f.closed = true
	err := f.File.Close()
	if f.closeError != nil {
		return f.closeError
	}
	return err
}

type staticSeekTestFile struct {
	*staticTestFile
	seeker    io.ReadSeeker
	seekError error
}

func (f *staticSeekTestFile) Seek(offset int64, whence int) (int64, error) {
	if f.seekError != nil {
		return 0, f.seekError
	}
	return f.seeker.Seek(offset, whence)
}

type staticTestWriter struct {
	http.ResponseWriter
	recorded   error
	writeError error
	shortWrite bool
}

func (w *staticTestWriter) RecordError(err error) { w.recorded = err }
func (w *staticTestWriter) Write(p []byte) (int, error) {
	if w.writeError != nil {
		return 0, w.writeError
	}
	if w.shortWrite {
		return w.ResponseWriter.Write(p[:len(p)-1])
	}
	return w.ResponseWriter.Write(p)
}

func TestStaticFileServer_DirectAndMountedPaths(t *testing.T) {
	files := fstest.MapFS{
		"assets/app.js": &fstest.MapFile{Data: []byte("asset bytes")},
		"other.js":      &fstest.MapFile{Data: []byte("wrong file")},
	}
	static := NewStaticFileServer(files)
	mounted := NewWebHandler()
	static.AddRoutes(mounted, "/ui/")
	for _, tc := range []struct {
		name    string
		handler http.Handler
		path    string
	}{
		{"direct", static, "/assets/app.js"},
		{"mounted", mounted, "/ui/assets/app.js"},
		{"standard strip prefix", http.StripPrefix("/ui", static), "/ui/assets/app.js"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, tc.path, nil)
			r.SetPathValue("path", "other.js")
			w := httptest.NewRecorder()
			tc.handler.ServeHTTP(w, r)
			if w.Code != http.StatusOK || w.Body.String() != "asset bytes" {
				t.Fatalf("status = %d, body = %q", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
				t.Fatalf("asset cache policy lost: %q", w.Header().Get("Cache-Control"))
			}
			if r.URL.Path != tc.path {
				t.Fatalf("request path was mutated: %q", r.URL.Path)
			}
		})
	}
}

func TestStaticFileServer_SPA_IndexCachePolicy(t *testing.T) {
	files := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("SPA index")},
		"dir/a.js":   &fstest.MapFile{Data: []byte("asset")},
	}
	for _, requestPath := range []string{"/", "/index.html", "/missing", "/dir"} {
		t.Run(requestPath, func(t *testing.T) {
			w := httptest.NewRecorder()
			// Index policy must win even when the asset prefix includes it.
			NewStaticFileServer(files, WithSPAMode(), WithAssetPrefix("index")).ServeHTTP(w, httptest.NewRequest(http.MethodGet, requestPath, nil))
			if w.Code != http.StatusOK || w.Body.String() != "SPA index" {
				t.Fatalf("status = %d, body = %q", w.Code, w.Body.String())
			}
			if got := w.Header().Get("Cache-Control"); got != "no-cache, no-store, must-revalidate" {
				t.Fatalf("Cache-Control = %q", got)
			}
		})
	}
}

func TestStaticFileServer_SPADoesNotHideOpenErrors(t *testing.T) {
	for _, requestPath := range []string{"/blocked.js", "/"} {
		t.Run(requestPath, func(t *testing.T) {
			files := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("must not be served")}}
			fileFS := staticOpenFunc(func(name string) (fs.File, error) {
				if name == "blocked.js" || requestPath == "/" {
					return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
				}
				return files.Open(name)
			})
			recorder := httptest.NewRecorder()
			w := &staticTestWriter{ResponseWriter: recorder}
			NewStaticFileServer(fileFS, WithSPAMode()).ServeHTTP(w, httptest.NewRequest(http.MethodGet, requestPath, nil))
			if recorder.Code != http.StatusInternalServerError || !errors.Is(w.recorded, fs.ErrPermission) {
				t.Fatalf("status = %d, recorded = %v", recorder.Code, w.recorded)
			}
			if strings.Contains(recorder.Body.String(), "must not be served") {
				t.Fatal("filesystem failure was hidden by SPA fallback")
			}
		})
	}
}

func TestStaticFileServer_DirectoryPolicy(t *testing.T) {
	for _, tc := range []struct {
		name string
		spa  bool
		path string
	}{
		{"directory without SPA", false, "/dir"},
		{"missing SPA index", true, "/missing"},
		{"directory SPA index", true, "/index.html"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := fstest.MapFS{
				"dir/file.txt":        &fstest.MapFile{Data: []byte("no directory listing")},
				"index.html/file.txt": &fstest.MapFile{Data: []byte("no directory listing")},
			}
			if tc.name == "missing SPA index" {
				delete(files, "index.html/file.txt")
			}
			static := NewStaticFileServer(files)
			if tc.spa {
				static = NewStaticFileServer(files, WithSPAMode())
			}
			w := httptest.NewRecorder()
			static.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if w.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", w.Code)
			}
		})
	}
}

func TestStaticFileServer_RecordsFileErrors(t *testing.T) {
	want := errors.New("filesystem failure")
	for _, stage := range []string{"stat", "read", "seekable read", "multipart read", "seekable early EOF", "seek", "close"} {
		t.Run(stage, func(t *testing.T) {
			files := fstest.MapFS{"file.txt": &fstest.MapFile{Data: []byte("content")}}
			f, err := files.Open("file.txt")
			if err != nil {
				t.Fatal(err)
			}
			file := &staticTestFile{File: f}
			var served fs.File = file
			wantStatus := http.StatusOK
			wantError := want
			switch stage {
			case "stat":
				file.statError = want
				wantStatus = http.StatusInternalServerError
			case "read":
				file.readError = want
			case "seekable read", "multipart read":
				file.readError = want
				served = &staticSeekTestFile{staticTestFile: file, seeker: f.(io.ReadSeeker)}
			case "seekable early EOF":
				file.readError = io.EOF
				wantError = io.ErrUnexpectedEOF
				served = &staticSeekTestFile{staticTestFile: file, seeker: f.(io.ReadSeeker)}
			case "seek":
				served = &staticSeekTestFile{staticTestFile: file, seeker: f.(io.ReadSeeker), seekError: want}
				wantStatus = http.StatusInternalServerError
			case "close":
				file.closeError = want
			}
			fileFS := staticOpenFunc(func(string) (fs.File, error) { return served, nil })
			recorder := httptest.NewRecorder()
			w := &staticTestWriter{ResponseWriter: recorder}
			request := httptest.NewRequest(http.MethodGet, "/file.txt", nil)
			if stage == "multipart read" {
				request.Header.Set("Range", "bytes=0-1,4-5")
				wantStatus = http.StatusPartialContent
			}
			NewStaticFileServer(fileFS, WithSPAMode()).ServeHTTP(w, request)
			if recorder.Code != wantStatus || !errors.Is(w.recorded, wantError) {
				t.Fatalf("status = %d, recorded = %v; want %d, %v", recorder.Code, w.recorded, wantStatus, wantError)
			}
			if !file.closed {
				t.Fatal("file was not closed")
			}
		})
	}
}

func TestStaticFileServer_RecordsWriteErrors(t *testing.T) {
	for _, seekable := range []bool{false, true} {
		for _, shortWrite := range []bool{false, true} {
			files := fstest.MapFS{"file.txt": &fstest.MapFile{Data: []byte("content")}}
			fileFS := staticOpenFunc(func(name string) (fs.File, error) {
				file, err := files.Open(name)
				if err != nil || seekable {
					return file, err
				}
				return &staticTestFile{File: file}, nil
			})
			want := errors.New("connection write failed")
			w := &staticTestWriter{ResponseWriter: httptest.NewRecorder(), writeError: want}
			if shortWrite {
				want = io.ErrShortWrite
				w.writeError = nil
				w.shortWrite = true
			}
			NewStaticFileServer(fileFS).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/file.txt", nil))
			if !errors.Is(w.recorded, want) {
				t.Fatalf("seekable = %v, short = %v: recorded = %v, want %v", seekable, shortWrite, w.recorded, want)
			}
		}
	}
}

func TestStaticFileServer_NonseekableRangeAndHead(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		t.Run(method, func(t *testing.T) {
			files := fstest.MapFS{"file.txt": &fstest.MapFile{Data: []byte("0123456789")}}
			var opened *staticTestFile
			fileFS := staticOpenFunc(func(name string) (fs.File, error) {
				file, err := files.Open(name)
				if err != nil {
					return nil, err
				}
				opened = &staticTestFile{File: file}
				return opened, nil
			})
			r := httptest.NewRequest(method, "/file.txt", nil)
			r.Header.Set("Range", "bytes=2-5")
			w := httptest.NewRecorder()
			NewStaticFileServer(fileFS).ServeHTTP(w, r)
			if w.Code != http.StatusOK || w.Header().Get("Content-Range") != "" {
				t.Fatalf("nonseekable file claimed range support: status = %d, header = %v", w.Code, w.Header())
			}
			if method == http.MethodHead {
				if w.Body.Len() != 0 || opened.reads != 0 {
					t.Fatal("HEAD read or wrote the file body")
				}
			} else if w.Body.String() != "0123456789" {
				t.Fatalf("body = %q, want complete file", w.Body.String())
			}
		})
	}
}

func TestStaticContentType_RegisteredExtension(t *testing.T) {
	const extension = ".gopernicus-static-test"
	const want = "application/x-gopernicus-test"
	if err := mime.AddExtensionType(extension, want); err != nil {
		t.Fatal(err)
	}
	if got := staticContentType("file" + strings.ToUpper(extension)); got != want {
		t.Fatalf("registered MIME type = %q, want %q", got, want)
	}
}

func TestStaticFileServer_MountedRangeWire(t *testing.T) {
	files := fstest.MapFS{"assets/app.js": &fstest.MapFile{Data: []byte("0123456789")}}
	handler := NewWebHandler()
	NewStaticFileServer(files).AddRoutes(handler, "/ui")
	server := httptest.NewServer(handler)
	defer server.Close()
	request, err := http.NewRequest(http.MethodGet, server.URL+"/ui/assets/app.js", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Range", "bytes=2-5")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusPartialContent || string(body) != "2345" {
		t.Fatalf("status = %d, body = %q", response.StatusCode, body)
	}
}

func TestStaticOptionsDoNotRetainConstructionConfig(t *testing.T) {
	var settings *staticConfig
	files := fstest.MapFS{"index.html": {Data: []byte("SPA")}, "public/app.js": {Data: []byte("asset")}}
	server := NewStaticFileServer(files, WithAssetPrefix("old/"), WithAssetPrefix("public/"), WithSPAMode(), func(c *staticConfig) { settings = c })
	settings.assetPrefix = ""
	settings.spaMode = false
	for _, tc := range []struct{ path, body, cache string }{
		{"/public/app.js", "asset", "public, max-age=31536000, immutable"},
		{"/client-route", "SPA", "no-cache, no-store, must-revalidate"},
	} {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if recorder.Code != http.StatusOK || recorder.Body.String() != tc.body || recorder.Header().Get("Cache-Control") != tc.cache {
			t.Errorf("%s: status=%d body=%q cache=%q", tc.path, recorder.Code, recorder.Body.String(), recorder.Header().Get("Cache-Control"))
		}
	}
}

func TestWebConstructorsRejectNilOptions(t *testing.T) {
	for _, tc := range []struct {
		name      string
		construct func()
	}{
		{"web.NewStaticFileServer", func() { NewStaticFileServer(fstest.MapFS{}, nil) }},
		{"web.NewSSEStream", func() { NewSSEStream(make(chan SSEEvent), nil) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if got := recover(); got != tc.name+": nil option" {
					t.Fatalf("panic = %v", got)
				}
			}()
			tc.construct()
		})
	}
}
