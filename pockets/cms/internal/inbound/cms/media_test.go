package cms

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/cms/domain/media"
	"github.com/gopernicus/gopernicus/pockets/cms/internal/logic/mediasvc"
	"github.com/gopernicus/gopernicus/sdk"
)

var (
	pngBytes  = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 64)...)
	pdfBytes  = []byte("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n1 0 obj\n<<>>\nendobj\n")
	htmlBytes = []byte("<html><body><script>alert(document.cookie)</script></body></html>")
	// testAllowed mirrors cms.DefaultAllowedMediaTypes (unreachable from here:
	// the root package imports this one).
	testAllowed = []string{"image/png", "image/jpeg", "image/gif", "image/webp", "application/pdf"}
)

func TestMedia_LibraryAndServe(t *testing.T) {
	asset := media.Asset{ID: "a1", Filename: "logo.png", ContentType: "image/png", Size: 9, StorageKey: "assets/a1/logo.png"}
	svc := &fakeMediaSvc{
		listFn: func(ctx context.Context) ([]media.Asset, error) { return []media.Asset{asset}, nil },
		openFn: func(ctx context.Context, id string) (media.Asset, io.ReadCloser, error) {
			return asset, io.NopCloser(strings.NewReader("PNG-BYTES")), nil
		},
	}

	// Library lists the asset + upload form.
	rec := httptest.NewRecorder()
	mediaRouter(svc).ServeHTTP(rec, httptest.NewRequest("GET", "/media", nil))
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, "logo.png") || !strings.Contains(body, `enctype="multipart/form-data"`) {
		t.Fatalf("library: status=%d", rec.Code)
	}

	// Serve streams bytes with content type.
	rec = httptest.NewRecorder()
	mediaRouter(svc).ServeHTTP(rec, httptest.NewRequest("GET", "/media/a1/file", nil))
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/png" || rec.Body.String() != "PNG-BYTES" {
		t.Fatalf("serve: status=%d ct=%q body=%q", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
}

// TestMedia_ServeHeaders pins nosniff and a Content-Disposition built with
// mime.FormatMediaType: the four raster image types render inline, every other
// type (pdf, a host-added type, a pre-existing asset of any type) downloads.
func TestMedia_ServeHeaders(t *testing.T) {
	for _, tc := range []struct {
		name, filename, contentType, disposition string
	}{
		{"png inline", "logo.png", "image/png", "inline"},
		{"jpeg inline", "photo.jpg", "image/jpeg", "inline"},
		{"gif inline", "anim.gif", "image/gif", "inline"},
		{"webp inline", "pic.webp", "image/webp", "inline"},
		{"pdf attachment", "Annual Report.pdf", "application/pdf", "attachment"},
		{"host-added type attachment", "song.mp3", "audio/mpeg", "attachment"},
		{"pre-existing html asset attachment", "page.html", "text/html; charset=utf-8", "attachment"},
		{"pre-existing svg asset attachment", "icon.svg", "image/svg+xml", "attachment"},
		{"unparsable type attachment", "odd.bin", "not a type", "attachment"},
		{"path and quotes reduced", `C:\uploads\..\x "y".png`, "image/png", "inline"},
		{"control characters dropped, non-ASCII encoded", "r\u00e9sum\u00e9\r\nX-Evil: 1.pdf", "application/pdf", "attachment"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asset := media.Asset{ID: "a1", Filename: tc.filename, ContentType: tc.contentType, Size: 1, StorageKey: "assets/k/x"}
			svc := &fakeMediaSvc{openFn: func(context.Context, string) (media.Asset, io.ReadCloser, error) {
				return asset, io.NopCloser(strings.NewReader("x")), nil
			}}
			rec := httptest.NewRecorder()
			mediaRouter(svc).ServeHTTP(rec, httptest.NewRequest("GET", "/media/a1/file", nil))
			if rec.Code != 200 {
				t.Fatalf("status = %d", rec.Code)
			}
			if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
			}
			cd := rec.Header().Get("Content-Disposition")
			disposition, params, err := mime.ParseMediaType(cd)
			if err != nil {
				t.Fatalf("Content-Disposition %q does not parse: %v", cd, err)
			}
			if disposition != tc.disposition {
				t.Errorf("Content-Disposition = %q, want %s", cd, tc.disposition)
			}
			want := mime.FormatMediaType(tc.disposition, map[string]string{"filename": dispositionFilename(tc.filename)})
			if cd != want {
				t.Errorf("Content-Disposition = %q, want mime.FormatMediaType's %q", cd, want)
			}
			if fn := params["filename"]; fn == "" || strings.ContainsAny(fn, "/\\\r\n") {
				t.Errorf("filename = %q, want a bare name", fn)
			}
			if got := rec.Header().Get("Cache-Control"); got != "public, max-age=3600" {
				t.Errorf("Cache-Control = %q", got)
			}
		})
	}
	if got := dispositionFilename(`C:\uploads\..\x "y".png`); got != `x "y".png` {
		t.Errorf("dispositionFilename(path) = %q", got)
	}
	if got := dispositionFilename("r\u00e9sum\u00e9\r\nX-Evil: 1.pdf"); got != "r\u00e9sum\u00e9X-Evil: 1.pdf" {
		t.Errorf("dispositionFilename(control) = %q", got)
	}
	for _, empty := range []string{"", "/", "..", " \t"} {
		if got := dispositionFilename(empty); got != "file" {
			t.Errorf("dispositionFilename(%q) = %q, want file", empty, got)
		}
	}
}

// TestMedia_UploadPassesSniffedType: the part's client Content-Type is never
// consulted — the handler hands the service the parsed sniffed type and the
// whole file, sniffed prefix included.
func TestMedia_UploadPassesSniffedType(t *testing.T) {
	for _, tc := range []struct {
		name, claimed, want string
		data                []byte
	}{
		{"html claiming png", "image/png", "text/html", htmlBytes},
		{"png claiming html", "text/html", "image/png", pngBytes},
		{"pdf claiming nothing", "", "application/pdf", pdfBytes},
		{"large png", "image/png", "image/png", append(append([]byte{}, pngBytes...), bytes.Repeat([]byte{1}, 4096)...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gotType string
			var gotBody []byte
			svc := &fakeMediaSvc{uploadFn: func(_ context.Context, _, contentType string, _ int64, r io.Reader) (media.Asset, error) {
				gotType = contentType
				gotBody, _ = io.ReadAll(r)
				return media.Asset{}, nil
			}}
			rec := httptest.NewRecorder()
			mediaRouter(svc).ServeHTTP(rec, uploadRequest(t, "f.png", tc.claimed, tc.data))
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("status = %d, body = %q", rec.Code, rec.Body.String())
			}
			if gotType != tc.want {
				t.Errorf("service got content type %q, want the sniffed %q", gotType, tc.want)
			}
			if !bytes.Equal(gotBody, tc.data) {
				t.Errorf("service got %d bytes, want the whole %d-byte file", len(gotBody), len(tc.data))
			}
		})
	}
}

// TestMedia_UploadPolicyThroughTheService drives the real media service: a
// listed sniffed type is stored, an unlisted one re-renders the library with
// 400 naming the allowed types and stores nothing.
func TestMedia_UploadPolicyThroughTheService(t *testing.T) {
	assets, blobs := &memAssets{byID: map[string]media.Asset{}}, &memBlobs{data: map[string][]byte{}}
	svc := mediasvc.NewService(assets, blobs, sdk.IDGenerator{}, nil)
	if err := svc.SetUploadPolicy(mediasvc.UploadPolicy{AllowedMediaTypes: testAllowed}); err != nil {
		t.Fatal(err)
	}
	h := mediaRouter(svc)

	for _, tc := range []struct {
		filename string
		data     []byte
	}{{"logo.png", pngBytes}, {"doc.pdf", pdfBytes}} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, uploadRequest(t, tc.filename, "application/octet-stream", tc.data))
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("%s: status = %d, body = %q", tc.filename, rec.Code, rec.Body.String())
		}
	}
	if len(assets.byID) != 2 || len(blobs.data) != 2 {
		t.Fatalf("stored %d assets / %d blobs, want 2/2", len(assets.byID), len(blobs.data))
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, uploadRequest(t, "evil.png", "image/png", htmlBytes))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("html as png: status = %d, want 400", rec.Code)
	}
	body := rec.Body.String()
	if want := "unsupported file type; allowed: image/png, image/jpeg, image/gif, image/webp, application/pdf"; !strings.HasPrefix(body, "STUB-MEDIA") || !strings.Contains(body, want) {
		t.Errorf("html as png: body = %q, want the library naming %q", body, want)
	}
	if len(assets.byID) != 2 || len(blobs.data) != 2 {
		t.Errorf("refused upload stored something: %d assets / %d blobs", len(assets.byID), len(blobs.data))
	}

	// The stored png serves inline; the pdf downloads.
	for _, a := range assets.byID {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/media/"+a.ID+"/file", nil))
		disposition, _, _ := mime.ParseMediaType(rec.Header().Get("Content-Disposition"))
		want := map[string]string{"image/png": "inline", "application/pdf": "attachment"}[a.ContentType]
		if rec.Code != 200 || disposition != want || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("serve %s: status=%d Content-Disposition=%q nosniff=%q, want %s", a.ContentType, rec.Code, rec.Header().Get("Content-Disposition"), rec.Header().Get("X-Content-Type-Options"), want)
		}
	}
}

// TestMedia_UploadCap: a body past the cap answers 413 with the library
// re-rendered, the service untouched; at the default cap the same body passes.
func TestMedia_UploadCap(t *testing.T) {
	called := false
	svc := &fakeMediaSvc{uploadFn: func(context.Context, string, string, int64, io.Reader) (media.Asset, error) {
		called = true
		return media.Asset{}, nil
	}}
	data := append(append([]byte{}, pngBytes...), bytes.Repeat([]byte{1}, 4096)...)
	small := BuildRouter(newTestRegistry(), &fakeEntrySvc{}, &fakeTaxo{}, &fakeMenuSvc{}, svc, &fakeContactSvc{}, nil, slog.New(slog.DiscardHandler), WithViews(stubViews{}), WithAdminUngated(), WithMaxUploadBytes(1024))

	rec := httptest.NewRecorder()
	small.ServeHTTP(rec, uploadRequest(t, "big.png", "image/png", data))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize: status = %d, want 413", rec.Code)
	}
	if body := rec.Body.String(); !strings.HasPrefix(body, "STUB-MEDIA") || !strings.Contains(body, "upload too large") {
		t.Errorf("oversize: body = %q, want the library with \"upload too large\"", body)
	}
	if called {
		t.Error("oversize upload reached the service")
	}

	rec = httptest.NewRecorder()
	mediaRouter(svc).ServeHTTP(rec, uploadRequest(t, "big.png", "image/png", data))
	if rec.Code != http.StatusSeeOther || !called {
		t.Fatalf("default cap: status = %d called = %t, want 303 and the service called", rec.Code, called)
	}

	// A malformed body stays a 400.
	rec = httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/media", strings.NewReader("not multipart"))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	mediaRouter(svc).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "upload malformed") {
		t.Errorf("malformed: status = %d body = %q, want 400 upload malformed", rec.Code, rec.Body.String())
	}
}

// uploadRequest builds POST /media with one "file" part whose client-claimed
// Content-Type is claimed (omitted when empty).
func uploadRequest(t *testing.T, filename, claimed string, data []byte) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	hdr := textproto.MIMEHeader{}
	hdr.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": "file", "filename": filename}))
	if claimed != "" {
		hdr.Set("Content-Type", claimed)
	}
	part, err := mw.CreatePart(hdr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/media", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

// memAssets is an in-memory media.AssetRepository.
type memAssets struct{ byID map[string]media.Asset }

func (m *memAssets) Create(_ context.Context, a media.Asset) (media.Asset, error) {
	m.byID[a.ID] = a
	return a, nil
}
func (m *memAssets) Get(_ context.Context, id string) (media.Asset, error) {
	a, ok := m.byID[id]
	if !ok {
		return media.Asset{}, sdk.ErrNotFound
	}
	return a, nil
}
func (m *memAssets) List(context.Context) ([]media.Asset, error) {
	out := make([]media.Asset, 0, len(m.byID))
	for _, a := range m.byID {
		out = append(out, a)
	}
	return out, nil
}
func (m *memAssets) Delete(_ context.Context, id string) error {
	delete(m.byID, id)
	return nil
}

// memBlobs is an in-memory media.BlobStore.
type memBlobs struct{ data map[string][]byte }

func (m *memBlobs) Upload(_ context.Context, path string, r io.Reader) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	m.data[path] = b
	return nil
}
func (m *memBlobs) Download(_ context.Context, path string) (io.ReadCloser, error) {
	b, ok := m.data[path]
	if !ok {
		return nil, sdk.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}
func (m *memBlobs) Delete(_ context.Context, path string) error {
	delete(m.data, path)
	return nil
}
