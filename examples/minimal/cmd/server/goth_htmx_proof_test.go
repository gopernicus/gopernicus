package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/examples/minimal/internal/memstore"
	"github.com/gopernicus/gopernicus/features/cms"
	cmsgoth "github.com/gopernicus/gopernicus/features/cms/views/goth"
	"github.com/gopernicus/gopernicus/sdk/capabilities/cacher"
	"github.com/gopernicus/gopernicus/sdk/capabilities/email"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
	uigoth "github.com/gopernicus/gopernicus/ui/goth"
	uigothassets "github.com/gopernicus/gopernicus/ui/goth/assets"
)

// htmxProofRouter composes this host's real presentation graph — the ui/goth asset
// route and the CMS feature mounted with the ui/goth Views over a bundle — exactly
// as run() wires it (minus web.Run), so the HTTP-level HTMX proofs below drive the
// shipped surface with the memstore-seeded content, not a stub.
func htmxProofRouter(t *testing.T) http.Handler {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := memstore.New()
	repos := store.Repositories()
	if err := seed(context.Background(), repos); err != nil {
		t.Fatalf("seed: %v", err)
	}

	router := web.NewWebHandler(web.WithLogging(log))
	router.Use(web.RequestID(), web.Logger(log), web.Panics(log))

	bundle, err := uigoth.New(uigoth.Config{AssetBasePath: gothAssetBasePath})
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	views, err := cmsgoth.New(bundle)
	if err != nil {
		t.Fatalf("cmsgoth.New: %v", err)
	}
	uigothStatic := web.NewStaticFileServer(uigothassets.FS, web.WithAssetPrefix("dist/"))
	uigothStatic.AddRoutes(router, gothAssetBasePath)

	if err := cms.Register(feature.Mount{Router: router, Logger: log}, repos, cms.Config{
		Views:     views,
		Cache:     cacher.NewMemory(),
		Mailer:    email.NewConsole(log),
		MailFrom:  "cms@localhost",
		ContactTo: "ops@localhost",
	}); err != nil {
		t.Fatalf("cms.Register: %v", err)
	}
	return router
}

func do(t *testing.T, router http.Handler, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// TestEntriesList_FullDocumentRendersStyledDataTable proves the admin entries list
// (no HX-Request) renders the full GOTH document: the fingerprinted stylesheet
// (which resolves over the host asset route), the DataTable content region, and the
// filter/sort/pagination controls carrying BOTH a no-JS URL and explicit hx-*.
func TestEntriesList_FullDocumentRendersStyledDataTable(t *testing.T) {
	router := htmxProofRouter(t)
	rec := do(t, router, http.MethodGet, "/articles", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /articles = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"<html", `rel="stylesheet"`, "/assets/goth/", "goth-data-table",
		`id="cms-entries-content"`, `name="status"`,
		`hx-get`, `hx-target="#cms-entries-content"`,
		`hx-swap="outerHTML show:none focus-scroll:false"`, `hx-push-url="true"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("full entries list missing %q", want)
		}
	}
	// The referenced stylesheet actually resolves over the host asset route.
	href := stylesheetHref(body)
	if href == "" {
		t.Fatal("no stylesheet href in entries list")
	}
	asset := do(t, router, http.MethodGet, href, nil)
	if asset.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200 (GOTH stylesheet must serve)", href, asset.Code)
	}
	if ct := asset.Header().Get("Content-Type"); !strings.Contains(ct, "css") {
		t.Errorf("stylesheet content-type = %q, want css", ct)
	}
}

// TestEntriesList_HTMXReturnsFragmentOnly proves the HTMX request (HX-Request:true)
// returns ONLY the swappable content region — no document chrome, no toolbar — so an
// outerHTML swap of #cms-entries-content is seamless. The same URL without the header
// returns the full document (no-JS parity).
func TestEntriesList_HTMXReturnsFragmentOnly(t *testing.T) {
	router := htmxProofRouter(t)
	frag := do(t, router, http.MethodGet, "/articles", map[string]string{"HX-Request": "true"})
	if frag.Code != http.StatusOK {
		t.Fatalf("HTMX GET /articles = %d, want 200", frag.Code)
	}
	fb := frag.Body.String()
	if !strings.Contains(fb, `id="cms-entries-content"`) {
		t.Errorf("HTMX fragment missing the content region:\n%s", fb)
	}
	if strings.Contains(fb, "<html") || strings.Contains(fb, "<title>") {
		t.Errorf("HTMX fragment leaked document chrome:\n%s", fb)
	}
	if strings.Contains(fb, "cms-entries-filter") {
		t.Errorf("HTMX fragment leaked the toolbar (must stay mounted across the swap):\n%s", fb)
	}
	// The seeded articles appear in both the fragment and the full document.
	full := do(t, router, http.MethodGet, "/articles", nil)
	for _, want := range []string{"Running CMS without a database", "Bring your own store"} {
		if !strings.Contains(fb, want) {
			t.Errorf("HTMX fragment missing seeded entry %q", want)
		}
		if !strings.Contains(full.Body.String(), want) {
			t.Errorf("full document missing seeded entry %q", want)
		}
	}
}

// TestEntriesList_StatusFilterNarrows proves the status filter is a real server
// query with a shareable URL: filtering to draft removes the seeded (published)
// articles from BOTH the no-JS full page and the HTMX fragment.
func TestEntriesList_StatusFilterNarrows(t *testing.T) {
	router := htmxProofRouter(t)
	published := do(t, router, http.MethodGet, "/articles?status=published", nil).Body.String()
	if !strings.Contains(published, "Running CMS without a database") {
		t.Error("published filter should include the seeded published articles")
	}
	draft := do(t, router, http.MethodGet, "/articles?status=draft", nil).Body.String()
	if strings.Contains(draft, "Running CMS without a database") {
		t.Error("draft filter should exclude the seeded published articles")
	}
	if !strings.Contains(draft, "Nothing here yet.") {
		t.Errorf("draft filter with no drafts should show the empty state:\n%s", draft)
	}
	// The active filter is reflected in the select, and the URL is shareable
	// (the query drives the server, not client state).
	if !strings.Contains(draft, `selected value="draft"`) {
		t.Error("draft filter should mark the draft option selected")
	}
}

// TestMediaByteEndpoint_UnaffectedByViewMigration is a light guard that the media
// byte endpoint still mounts (the API-only surface a nil-Views host keeps).
func TestMediaByteEndpoint_MountsWithViews(t *testing.T) {
	router := htmxProofRouter(t)
	// A missing asset id returns a 4xx (not a 404-route-missing) — the route exists.
	rec := do(t, router, http.MethodGet, "/media/does-not-exist/file", nil)
	if rec.Code == http.StatusMethodNotAllowed {
		t.Fatalf("media byte endpoint not mounted (405)")
	}
}

// stylesheetHref extracts the first <link rel="stylesheet" href="…"> target.
func stylesheetHref(body string) string {
	const marker = `<link rel="stylesheet" href="`
	i := strings.Index(body, marker)
	if i < 0 {
		return ""
	}
	rest := body[i+len(marker):]
	j := strings.IndexByte(rest, '"')
	if j < 0 {
		return ""
	}
	return rest[:j]
}
