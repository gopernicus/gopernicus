package cms

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gopernicus/gopernicus/pockets"
	"github.com/gopernicus/gopernicus/pockets/cms/domain/content"
	"github.com/gopernicus/gopernicus/pockets/cms/domain/media"
	"github.com/gopernicus/gopernicus/pockets/cms/domain/menus"
	"github.com/gopernicus/gopernicus/pockets/cms/domain/messaging"
	"github.com/gopernicus/gopernicus/pockets/cms/domain/taxonomy"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// pngHeader is enough of a PNG for http.DetectContentType to sniff image/png.
var pngHeader = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

// muxRegistrar mounts the pocket's routes on a real ServeMux so the wiring
// tests drive Register's handlers end to end.
type muxRegistrar struct{ mux *http.ServeMux }

func (r muxRegistrar) Handle(method, path string, h http.HandlerFunc, mw ...web.Middleware) {
	var handler http.Handler = h
	for i := len(mw) - 1; i >= 0; i-- {
		handler = mw[i](handler)
	}
	r.mux.Handle(method+" "+path, handler)
}

// wiringViews renders the contact thank-you page; every other view stays nil
// because the create paths these tests drive redirect instead of rendering.
type wiringViews struct{ stubViews }

func (wiringViews) ContactThanks() web.Renderer { return okRenderer{} }

type okRenderer struct{}

func (okRenderer) Render(context.Context, io.Writer) error { return nil }

// minted records the key each entity carried when it reached its repository.
// The fakes embed the port interfaces: an unexpected method call panics.
type minted struct {
	entry, term, menu, menuItem, asset, inquiry string
	storageKey                                  string
}

type wiringEntries struct {
	content.EntryRepository
	m *minted
}

func (r wiringEntries) Create(_ context.Context, e content.Entry) (content.Entry, error) {
	r.m.entry = e.ID
	return e, nil
}

func (wiringEntries) SetTerms(context.Context, string, []string) error { return nil }

type wiringTerms struct {
	taxonomy.TermRepository
	m *minted
}

func (r wiringTerms) Create(_ context.Context, t taxonomy.Term) (taxonomy.Term, error) {
	r.m.term = t.ID
	return t, nil
}

type wiringMenus struct {
	menus.MenuRepository
	m *minted
}

func (r wiringMenus) CreateMenu(_ context.Context, mn menus.Menu) (menus.Menu, error) {
	r.m.menu = mn.ID
	return mn, nil
}

func (r wiringMenus) AddItem(_ context.Context, it menus.MenuItem) (menus.MenuItem, error) {
	r.m.menuItem = it.ID
	return it, nil
}

type wiringAssets struct {
	media.AssetRepository
	m *minted
}

func (r wiringAssets) Create(_ context.Context, a media.Asset) (media.Asset, error) {
	r.m.asset = a.ID
	r.m.storageKey = a.StorageKey
	return a, nil
}

type wiringBlobs struct{ media.BlobStore }

func (wiringBlobs) Upload(_ context.Context, _ string, r io.Reader) error {
	_, err := io.Copy(io.Discard, r)
	return err
}

type wiringInquiries struct {
	messaging.InquiryRepository
	m *minted
}

func (r wiringInquiries) Create(_ context.Context, in messaging.Inquiry) (messaging.Inquiry, error) {
	r.m.inquiry = in.ID
	return in, nil
}

// countingIDs returns a generator minting prefix-1, prefix-2, …: distinct keys,
// so a test can tell which mint reached which entity.
func countingIDs(prefix string) sdk.IDGenerator {
	var n atomic.Int64
	return sdk.NewIDGenerator(func() (string, error) {
		return fmt.Sprintf("%s-%d", prefix, n.Add(1)), nil
	})
}

// createEveryEntity registers the pocket with ids and drives each create path
// through the public HTTP surface, in a fixed order: entry, term, menu, menu
// item, asset, inquiry.
func createEveryEntity(t *testing.T, ids sdk.IDGenerator) minted {
	t.Helper()
	var m minted
	mux := http.NewServeMux()
	repos := Repositories{
		Entries:   wiringEntries{m: &m},
		Terms:     wiringTerms{m: &m},
		Menus:     wiringMenus{m: &m},
		Media:     wiringAssets{m: &m},
		Inquiries: wiringInquiries{m: &m},
	}
	cfg := Config{
		Views:                    wiringViews{},
		Blobs:                    wiringBlobs{},
		Mailer:                   tlsSender{},
		IDs:                      ids,
		AdminUngatedAcknowledged: true,
	}
	if err := Register(pockets.Mount{Router: muxRegistrar{mux: mux}}, repos, cfg); err != nil {
		t.Fatalf("Register: %v", err)
	}

	postForm(t, mux, "/articles", url.Values{"title": {"Hello"}})
	postForm(t, mux, "/terms", url.Values{"kind": {string(taxonomy.KindCategory)}, "name": {"News"}})
	postForm(t, mux, "/menus", url.Values{"name": {"Main"}})
	postForm(t, mux, "/menus/"+m.menu+"/items", url.Values{"label": {"Home"}, "url": {"/"}})

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "photo.png")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write(pngHeader); err != nil {
		t.Fatalf("write upload: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	serve(t, mux, httptest.NewRequest(http.MethodPost, "/media", &body), mw.FormDataContentType(), http.StatusSeeOther)

	req := httptest.NewRequest(http.MethodPost, "/contact", strings.NewReader(url.Values{
		"name": {"Jane"}, "email": {"jane@example.com"}, "message": {"Hi"},
	}.Encode()))
	serve(t, mux, req, "application/x-www-form-urlencoded", http.StatusOK)
	return m
}

func postForm(t *testing.T, h http.Handler, path string, form url.Values) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	serve(t, h, req, "application/x-www-form-urlencoded", http.StatusSeeOther)
}

func serve(t *testing.T, h http.Handler, req *http.Request, contentType string, want int) {
	t.Helper()
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != want {
		t.Fatalf("%s %s = %d, want %d (body %q)", req.Method, req.URL.Path, rec.Code, want, rec.Body.String())
	}
}

// TestRegister_IDsReachEveryEntity proves a host generator passed as
// Config.IDs keys every entity the pocket creates, one mint per create.
func TestRegister_IDsReachEveryEntity(t *testing.T) {
	m := createEveryEntity(t, countingIDs("fixed"))

	got := map[string]string{
		"entry": m.entry, "term": m.term, "menu": m.menu,
		"menu item": m.menuItem, "asset": m.asset, "inquiry": m.inquiry,
	}
	want := map[string]string{
		"entry": "fixed-1", "term": "fixed-2", "menu": "fixed-3",
		"menu item": "fixed-4", "asset": "fixed-5", "inquiry": "fixed-6",
	}
	for kind, id := range want {
		if got[kind] != id {
			t.Errorf("%s id = %q, want %q", kind, got[kind], id)
		}
	}
	if strings.Contains(m.storageKey, "fixed-") {
		t.Errorf("storage key %q follows Config.IDs; it must keep its own generator", m.storageKey)
	}
}

// TestRegister_ZeroIDsSelectUUIDv7 proves the zero Config.IDs keys every entity
// with the record-class default: an RFC 9562 version-7 UUID.
func TestRegister_ZeroIDsSelectUUIDv7(t *testing.T) {
	m := createEveryEntity(t, sdk.IDGenerator{})

	seen := map[string]bool{}
	for kind, id := range map[string]string{
		"entry": m.entry, "term": m.term, "menu": m.menu,
		"menu item": m.menuItem, "asset": m.asset, "inquiry": m.inquiry,
	} {
		if len(id) != 36 || id[14] != '7' || !strings.ContainsRune("89ab", rune(id[19])) {
			t.Errorf("%s id = %q, want a version-7 UUID", kind, id)
		}
		if seen[id] {
			t.Errorf("%s id %q minted twice", kind, id)
		}
		seen[id] = true
	}
}
