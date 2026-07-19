package main

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/authmem"
	auth "github.com/gopernicus/gopernicus/features/authentication"
	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
	uigothassets "github.com/gopernicus/gopernicus/ui/goth/assets"

	authgoth "github.com/gopernicus/gopernicus/features/authentication/views/goth"
)

// gothProofRouter builds the host's real presentation composition — the ui/goth
// asset routes, the externalized fragment-reader script route, and the auth feature
// mounted with the ui/goth Views + the adapter-derived HTMLPolicy — exactly as run()
// wires them, so the HTTP-level proofs below drive the shipped surface, not a stub.
func gothProofRouter(t *testing.T) *web.WebHandler {
	t.Helper()
	cfg, err := buildAuthConfig(quietLog(), nil)
	if err != nil {
		t.Fatalf("buildAuthConfig: %v", err)
	}
	cfg.DeliveryMode = auth.DeliveryModeInProcess
	cfg.DeliveryEphemeralAcknowledged = true
	svc, err := auth.NewService(authmem.New().Repositories(), cfg)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}

	router := web.NewWebHandler(web.WithLogging(quietLog()))
	uigothStatic := web.NewStaticFileServer(uigothassets.FS, web.WithAssetPrefix("dist/"))
	uigothStatic.AddRoutes(router, authAssetBasePath)
	router.Handle(http.MethodGet, authgoth.DefaultFragmentScriptPath, authgoth.FragmentScriptHandler().ServeHTTP)

	if err := svc.Register(feature.Mount{Router: router, Logger: quietLog(), Events: sdkevents.NewMemory()}); err != nil {
		t.Fatalf("auth Register: %v", err)
	}
	return router
}

func get(t *testing.T, router http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

var stylesheetHref = regexp.MustCompile(`<link rel="stylesheet" href="([^"]+)"`)

// mappedCSP is the set of directives the auth CSP must carry under the adapter's
// HTMLPolicy: the feature-owned fixed protections plus the widened style/script-src.
var mappedCSP = []string{
	"default-src 'none'",
	"base-uri 'none'",
	"form-action 'self'",
	"frame-ancestors 'none'",
	"style-src 'self'",
	"script-src 'self'",
	"'nonce-",
}

// TestGOTHRegisterRendersStyledUnderMappedCSP proves a GOTH-rendered page (register)
// loads its ui/goth fingerprinted stylesheet from the host origin under the CSP the
// adapter's HTMLPolicy widens the feature default into, and that the referenced asset
// actually serves — the migration's load-bearing browser property, driven over the
// real composed router. (Login is this host's asset-free branded override; every other
// page renders through ui/goth.)
func TestGOTHRegisterRendersStyledUnderMappedCSP(t *testing.T) {
	router := gothProofRouter(t)
	rec := get(t, router, "/auth/register")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /auth/register = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`rel="stylesheet"`, `/assets/goth/`, `.css`, "goth-input", "goth-button"} {
		if !strings.Contains(body, want) {
			t.Errorf("register body missing %q", want)
		}
	}

	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range mappedCSP {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP missing %q in: %s", want, csp)
		}
	}

	// The referenced stylesheet actually resolves over the host asset route (200 CSS).
	m := stylesheetHref.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no stylesheet href found in register body")
	}
	asset := get(t, router, m[1])
	if asset.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200 (GOTH stylesheet must serve)", m[1], asset.Code)
	}
	if ct := asset.Header().Get("Content-Type"); !strings.Contains(ct, "css") {
		t.Errorf("stylesheet content-type = %q, want text/css", ct)
	}
}

// TestGOTHMappedCSPAppliesToHostOverride proves the adapter-derived HTMLPolicy governs
// EVERY auth page, including this host's asset-free Login override: the mapped CSP is
// feature-applied per render, so a view can never weaken it.
func TestGOTHMappedCSPAppliesToHostOverride(t *testing.T) {
	rec := get(t, gothProofRouter(t), "/auth/login")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /auth/login = %d, want 200", rec.Code)
	}
	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range mappedCSP {
		if !strings.Contains(csp, want) {
			t.Errorf("host-override login CSP missing %q in: %s", want, csp)
		}
	}
}

// TestGOTHResetLandingExternalizedReader proves the reset landing wires the
// externalized fragment-reader script (no inline script) and that the script serves
// same-origin so script-src 'self' covers it.
func TestGOTHResetLandingExternalizedReader(t *testing.T) {
	router := gothProofRouter(t)
	rec := get(t, router, "/auth/password/reset")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /auth/password/reset = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`data-auth-fragment="hash"`,
		`data-auth-fragment-target="reset-token"`,
		`src="` + authgoth.DefaultFragmentScriptPath + `"`,
		`name="token" id="reset-token" value=""`,
		"<noscript>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("reset body missing %q", want)
		}
	}
	// No inline reader survived the externalization.
	if strings.Contains(body, "replaceState") {
		t.Error("reset page still carries an inline fragment reader")
	}

	js := get(t, router, authgoth.DefaultFragmentScriptPath)
	if js.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", authgoth.DefaultFragmentScriptPath, js.Code)
	}
	if ct := js.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("fragment script content-type = %q, want javascript", ct)
	}
}

// TestGOTHMigrationPreservesJSONAPI proves the JSON API is untouched by the view
// migration: a POST to /auth/login with a JSON content type is dispatched to the JSON
// arm and answered with a JSON error (never an HTML page).
func TestGOTHMigrationPreservesJSONAPI(t *testing.T) {
	router := gothProofRouter(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"email":"x","password":"y"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	if rec.Code == http.StatusNotFound {
		t.Fatalf("POST /auth/login (JSON) = 404: the JSON API route is missing")
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("JSON login response content-type = %q, want application/json (JSON arm must stay JSON)", ct)
	}
}

// TestGOTHNilViewsAssetFreePosture proves the nil-Views posture is preserved: with no
// Views wired the HTML GET page is unregistered (405 — the JSON POST twin still owns
// the path) and no widened CSP/asset route is required.
func TestGOTHNilViewsAssetFreePosture(t *testing.T) {
	cfg, err := buildAuthConfig(quietLog(), nil)
	if err != nil {
		t.Fatalf("buildAuthConfig: %v", err)
	}
	cfg.DeliveryMode = auth.DeliveryModeInProcess
	cfg.DeliveryEphemeralAcknowledged = true
	cfg.Views = nil
	cfg.HTMLPolicy = nil
	svc, err := auth.NewService(authmem.New().Repositories(), cfg)
	if err != nil {
		t.Fatalf("auth.NewService (nil Views): %v", err)
	}
	router := web.NewWebHandler(web.WithLogging(quietLog()))
	if err := svc.Register(feature.Mount{Router: router, Logger: quietLog(), Events: sdkevents.NewMemory()}); err != nil {
		t.Fatalf("auth Register (nil Views): %v", err)
	}
	rec := get(t, router, "/auth/login")
	if rec.Code == http.StatusOK {
		t.Fatalf("GET /auth/login = 200 with nil Views: HTML surface leaked (want it unregistered)")
	}
}
