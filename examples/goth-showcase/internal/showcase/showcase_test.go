package showcase

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

var idAttrRE = regexp.MustCompile(`\sid="([^"]+)"`)

// TestAllPageNamespacingResolvesCollisions proves the /all composition carries no
// cross-specimen id collision: many specimens reuse the same caller-authored ids
// (dialog-trigger, cb-input, htmx-result, …) which collide verbatim, and the
// per-specimen namespacer must give every specimen its own id space so label/for
// and aria wiring stay intact and axe's duplicate-id rules never fire from the
// combination. It first proves the raw bodies DO collide (so the test is
// load-bearing) and then that the namespaced bodies /all embeds do not.
func TestAllPageNamespacingResolvesCollisions(t *testing.T) {
	s := newTestServer(t)
	specs := s.Registry().All()

	// Raw specimen bodies share ids across specimens — the collision the combined
	// page must reconcile. If this ever stops being true the namespacer is moot.
	rawOwner := map[string]string{}
	rawCollision := false
	for _, spec := range specs {
		for _, m := range idAttrRE.FindAllStringSubmatch(spec.Body(), -1) {
			if prev, ok := rawOwner[m[1]]; ok && prev != spec.ID {
				rawCollision = true
			}
			rawOwner[m[1]] = spec.ID
		}
	}
	if !rawCollision {
		t.Fatal("expected raw specimen bodies to carry colliding ids; namespacer test is no longer load-bearing")
	}

	// Namespaced bodies (exactly as allBody embeds them) share no id across two
	// specimens. Intra-specimen duplicates (a body that defines the same id twice,
	// e.g. the chart specimen — present standalone too) are the specimen's own and
	// are not a combination collision, so a match on the same specimen is allowed.
	owner := map[string]string{}
	for _, spec := range specs {
		body := namespaceIDs(allAnchor(spec.ID)+"-", spec.Body())
		for _, m := range idAttrRE.FindAllStringSubmatch(body, -1) {
			if prev, ok := owner[m[1]]; ok && prev != spec.ID {
				t.Errorf("cross-specimen id collision on %q: %s and %s", m[1], prev, spec.ID)
			}
			owner[m[1]] = spec.ID
		}
	}
}

// TestAllPageServesEverySpecimenUnderStrictCSP proves the combined page returns 200
// HTML under the same strict CSP contract as the per-specimen pages, links every
// specimen from its table of contents by a stable anchor, and reuses each specimen
// body from the registry.
func TestAllPageServesEverySpecimenUnderStrictCSP(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, allPath, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: status = %d, want 200", allPath, rec.Code)
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("%s: CSP missing default-src 'none': %q", allPath, csp)
	}
	for _, unsafe := range []string{"unsafe-eval", "unsafe-inline"} {
		if strings.Contains(csp, unsafe) {
			t.Errorf("%s: CSP contains %s: %q", allPath, unsafe, csp)
		}
	}
	body := rec.Body.String()
	for _, spec := range s.Registry().All() {
		anchor := allAnchor(spec.ID)
		if !strings.Contains(body, `href="#`+anchor+`"`) {
			t.Errorf("%s: table of contents missing anchor link for %s", allPath, spec.ID)
		}
		if !strings.Contains(body, `id="`+anchor+`"`) {
			t.Errorf("%s: missing section anchor for %s", allPath, spec.ID)
		}
	}
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	h := web.NewWebHandler()
	s, err := New(h)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

// TestEverySpecimenServesPageUnderStrictCSP proves each registered specimen route
// returns 200 HTML with a strict CSP header and no unsafe directives.
func TestEverySpecimenServesPageUnderStrictCSP(t *testing.T) {
	s := newTestServer(t)
	for _, spec := range s.Registry().All() {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, spec.Path(), nil)
		s.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", spec.ID, rec.Code)
		}
		csp := rec.Header().Get("Content-Security-Policy")
		if csp == "" {
			t.Errorf("%s: missing CSP header", spec.ID)
		}
		if !strings.Contains(csp, "default-src 'none'") {
			t.Errorf("%s: CSP missing default-src 'none': %q", spec.ID, csp)
		}
		for _, unsafe := range []string{"unsafe-eval", "unsafe-inline"} {
			if strings.Contains(csp, unsafe) {
				t.Errorf("%s: CSP contains %s: %q", spec.ID, unsafe, csp)
			}
		}
		body := rec.Body.String()
		if !strings.Contains(body, "<!doctype html>") && !strings.Contains(body, "<!DOCTYPE html>") {
			t.Errorf("%s: body is not a full document", spec.ID)
		}
		if !strings.Contains(body, "/assets/goth/dist/theme.") {
			t.Errorf("%s: page does not link the fingerprinted theme.css", spec.ID)
		}
	}
}

// TestProfileCSPMapping proves the CSP script-src/style-src exactly track the
// bundle Requirements per profile.
func TestProfileCSPMapping(t *testing.T) {
	s := newTestServer(t)
	cases := map[string]struct {
		wantScript bool
		wantHTMX   bool
	}{
		"profile-styles":      {wantScript: false},
		"profile-interactive": {wantScript: true},
		"profile-full":        {wantScript: true, wantHTMX: true},
	}
	for id, want := range cases {
		spec, ok := s.Registry().Lookup(id)
		if !ok {
			t.Fatalf("specimen %s not registered", id)
		}
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, spec.Path(), nil))
		csp := rec.Header().Get("Content-Security-Policy")
		hasScript := strings.Contains(csp, "script-src")
		if hasScript != want.wantScript {
			t.Errorf("%s: script-src present = %v, want %v (%q)", id, hasScript, want.wantScript, csp)
		}
		body := rec.Body.String()
		hasHTMX := strings.Contains(body, "/assets/goth/dist/htmx.")
		if hasHTMX != want.wantHTMX {
			t.Errorf("%s: htmx.js linked = %v, want %v", id, hasHTMX, want.wantHTMX)
		}
	}
}

// TestHostStylesheetSpecimenLinksHostThemeNoNonce proves the host-theme specimen
// links the host stylesheet AFTER the kit stylesheet with no integrity attribute,
// emits zero inline <style> and zero server-rendered style attribute, and its CSP
// style-src is exactly 'self' with no nonce.
func TestHostStylesheetSpecimenLinksHostThemeNoNonce(t *testing.T) {
	s := newTestServer(t)
	spec, ok := s.Registry().Lookup("theme-host-stylesheet")
	if !ok {
		t.Fatal("theme-host-stylesheet not registered")
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, spec.Path(), nil))
	body := rec.Body.String()
	csp := rec.Header().Get("Content-Security-Policy")

	// The host theme stylesheet is linked verbatim, with no integrity attribute.
	hostLink := `<link rel="stylesheet" href="` + hostThemePath + `">`
	if !strings.Contains(body, hostLink) {
		t.Fatalf("host-theme page missing the verbatim host stylesheet link %q: %s", hostThemePath, body)
	}
	// Order: kit theme.css link → host stylesheet link.
	iKit := strings.Index(body, "/assets/goth/dist/theme.")
	iHost := strings.Index(body, hostThemePath)
	if iKit < 0 || iHost < 0 || iKit > iHost {
		t.Errorf("host stylesheet must be linked after the kit stylesheet: kit=%d host=%d", iKit, iHost)
	}
	// No inline style and no server-rendered style attribute anywhere.
	if strings.Contains(body, "<style") {
		t.Errorf("host-theme page emitted an inline <style>: %s", body)
	}
	if strings.Contains(body, "style=") {
		t.Errorf("host-theme page emitted a server-rendered style attribute: %s", body)
	}
	// CSP style-src is exactly 'self' with no nonce.
	if !strings.Contains(csp, "style-src 'self'") {
		t.Errorf("CSP style-src is not 'self': %q", csp)
	}
	if strings.Contains(csp, "nonce-") || strings.Contains(csp, "unsafe-inline") {
		t.Errorf("CSP style-src carried a nonce or unsafe-inline: %q", csp)
	}
}

// TestHostThemeStylesheetServedAsCSS proves the embedded host theme stylesheet is
// served under its wired path with a text/css content type and carries both the
// token- and class-level overrides.
func TestHostThemeStylesheetServedAsCSS(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, hostThemePath, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: status = %d, want 200", hostThemePath, rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("%s: Content-Type = %q, want text/css", hostThemePath, ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "--primary") {
		t.Errorf("host stylesheet missing the --primary token override: %s", body)
	}
	if !strings.Contains(body, ".goth-badge") {
		t.Errorf("host stylesheet missing the .goth-badge class override: %s", body)
	}
}

// TestHTMXFixturesResponseCodes proves the deliberate success/validation/error
// fixtures return their designed status codes, and the history route degrades to
// a full document on a non-HTMX request.
func TestHTMXFixturesResponseCodes(t *testing.T) {
	s := newTestServer(t)
	cases := []struct {
		method, path string
		status       int
		wantBody     string
	}{
		{http.MethodGet, "/htmx/fragment/success", http.StatusOK, "htmx-success"},
		{http.MethodPost, "/htmx/fragment/validate", http.StatusUnprocessableEntity, "htmx-validation"},
		{http.MethodGet, "/htmx/fragment/error", http.StatusInternalServerError, "htmx-error"},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		if rec.Code != c.status {
			t.Errorf("%s %s: status = %d, want %d", c.method, c.path, rec.Code, c.status)
		}
		if !strings.Contains(rec.Body.String(), c.wantBody) {
			t.Errorf("%s %s: body missing %q", c.method, c.path, c.wantBody)
		}
	}

	// HTMX request → fragment.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/htmx/page/two", nil)
	req.Header.Set("HX-Request", "true")
	s.Handler().ServeHTTP(rec, req)
	if b := rec.Body.String(); strings.Contains(b, "<html") {
		t.Errorf("HTMX request to /htmx/page/two returned a full document: %s", b)
	}

	// Direct request → full document (history re-fetch correctness).
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/htmx/page/two", nil))
	if b := rec.Body.String(); !strings.Contains(b, "<html") {
		t.Errorf("direct request to /htmx/page/two did not return a full document: %s", b)
	}
}

// TestEveryImplementedPrimitiveHasSpecimen fails automatically if any primitive
// declared implemented lacks a registered showcase specimen. It is vacuously
// green at Phase 1 (no primitive implemented yet) and becomes load-bearing as
// Phase 2–6 append ids to ImplementedPrimitives.
func TestEveryImplementedPrimitiveHasSpecimen(t *testing.T) {
	registered := DefaultRegistry().PrimitiveSpecimens()
	for _, id := range ImplementedPrimitives {
		if !registered[id] {
			t.Errorf("primitive %s is declared implemented but has no showcase specimen", id)
		}
	}
}

// TestEveryImplementedComponentHasSpecimen fails automatically if any GOTH-7.1
// component declared implemented lacks a registered showcase specimen — the
// component analogue of the primitive completeness gate.
func TestEveryImplementedComponentHasSpecimen(t *testing.T) {
	registered := DefaultRegistry().ComponentSpecimens()
	for _, key := range ImplementedComponents {
		if !registered[key] {
			t.Errorf("component %s is declared implemented but has no showcase specimen", key)
		}
	}
}

// TestIndexListsEverySpecimen proves the index page links every registered
// specimen so the harness can crawl them.
func TestIndexListsEverySpecimen(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	body := rec.Body.String()
	for _, spec := range s.Registry().All() {
		if !strings.Contains(body, `href="`+spec.Path()+`"`) {
			t.Errorf("index missing link to %s", spec.ID)
		}
	}
}
