package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/examples/auth-cms/pockets/authentication/outbound/authmem"
	"github.com/gopernicus/gopernicus/pockets"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	delivery "github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	authgoth "github.com/gopernicus/gopernicus/pockets/authentication/views/goth"
	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
	uigothassets "github.com/gopernicus/gopernicus/ui/goth/assets"
)

// gothProofRouter builds the host's real presentation composition — the ui/goth
// asset routes, the externalized fragment-reader and passkey script routes, and the auth pocket
// mounted with the ui/goth Views + the adapter-derived HTMLPolicy — exactly as run()
// wires them, so the HTTP-level proofs below drive the shipped surface, not a stub.
// global is installed with Use before anything mounts, as run() does.
func gothProofRouter(t *testing.T, global ...web.Middleware) *web.WebHandler {
	t.Helper()
	cfg, err := buildAuthConfig(quietLog(), environment.ModeDevelopment, nil)
	if err != nil {
		t.Fatalf("buildAuthConfig: %v", err)
	}
	cfg.DeliveryMode = delivery.ModeInProcess
	cfg.DeliveryEphemeralAcknowledged = true
	svc, err := auth.New(authmem.New().Repositories(), cfg.TokenSigner, cfg.RuntimeMode, cfg.DeliveryMode, cfg.options()...)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}

	router := web.NewWebHandler()
	if len(global) > 0 {
		router.Use(global...)
	}
	uigothStatic := web.NewStaticFileServer(uigothassets.FS, web.WithAssetPrefix("dist/"))
	uigothStatic.AddRoutes(router, authAssetBasePath)
	router.Handle(http.MethodGet, authgoth.DefaultFragmentScriptPath, authgoth.FragmentScriptHandler().ServeHTTP)
	router.Handle(http.MethodGet, authgoth.DefaultPasskeyScriptPath, authgoth.PasskeyScriptHandler().ServeHTTP)

	if err := svc.HTTP.Register(pockets.Mount{Router: router, Logger: quietLog(), Events: sdkevents.NewMemory()}); err != nil {
		t.Fatalf("auth Register: %v", err)
	}
	return router
}

// TestSecurityHeadersPresetBesidePocketPolicy drives the auth pages under the
// global stack run() installs (TrustProxies outermost, SecurityHeaders second):
// the preset adds HSTS and Permissions-Policy, and the pocket's stricter page
// policy wins — no-referrer, X-Frame-Options DENY, and its own CSP with
// frame-ancestors 'none'.
func TestSecurityHeadersPresetBesidePocketPolicy(t *testing.T) {
	t.Parallel()
	router := gothProofRouter(t, web.TrustProxies(0), web.SecurityHeaders())
	rec := get(t, router, "/auth/login")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /auth/login = %d, want 200", rec.Code)
	}
	h := rec.Header()
	for name, want := range map[string]string{
		"Referrer-Policy":           "no-referrer",
		"X-Frame-Options":           "DENY",
		"X-Content-Type-Options":    "nosniff",
		"Strict-Transport-Security": "max-age=31536000; includeSubDomains",
		"Permissions-Policy":        "camera=(), microphone=(), geolocation=()",
	} {
		if got := h.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if csp := h.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("Content-Security-Policy = %q, want the pocket's frame-ancestors 'none'", csp)
	}
}

func get(t *testing.T, router http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

var stylesheetHref = regexp.MustCompile(`<link rel="stylesheet" href="([^"]+)"`)

// mappedCSP is the set of directives the auth CSP must carry under the adapter's
// HTMLPolicy: the pocket-owned fixed protections plus the widened style/script-src.
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
// adapter's HTMLPolicy widens the pocket default into, and that the referenced asset
// actually serves — the migration's load-bearing browser property, driven over the
// real composed router. (Login is this host's asset-free branded override; every other
// page renders through ui/goth.)
func TestGOTHRegisterRendersStyledUnderMappedCSP(t *testing.T) {
	t.Parallel()
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
// pocket-applied per render, so a view can never weaken it.
func TestGOTHMappedCSPAppliesToHostOverride(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	cfg, err := buildAuthConfig(quietLog(), environment.ModeDevelopment, nil)
	if err != nil {
		t.Fatalf("buildAuthConfig: %v", err)
	}
	cfg.DeliveryMode = delivery.ModeInProcess
	cfg.DeliveryEphemeralAcknowledged = true
	cfg.Views = nil
	cfg.HTMLPolicy = nil
	cfg.Passkeys.Views = nil
	cfg.TOTP.Views = nil
	svc, err := auth.New(authmem.New().Repositories(), cfg.TokenSigner, cfg.RuntimeMode, cfg.DeliveryMode, cfg.options()...)
	if err != nil {
		t.Fatalf("auth.NewService (nil Views): %v", err)
	}
	router := web.NewWebHandler()
	if err := svc.HTTP.Register(pockets.Mount{Router: router, Logger: quietLog(), Events: sdkevents.NewMemory()}); err != nil {
		t.Fatalf("auth Register (nil Views): %v", err)
	}
	rec := get(t, router, "/auth/login")
	if rec.Code == http.StatusOK {
		t.Fatalf("GET /auth/login = 200 with nil Views: HTML surface leaked (want it unregistered)")
	}
}

// TestGOTHLoginPasskeyContract proves the composed host serves the custom-view
// passkey contract: the branded login renders the hidden sign-in control and the
// un-nonced script tag, the script serves same-origin as JavaScript with nosniff
// under a CSP whose script-src and connect-src admit 'self' with no
// 'unsafe-inline', and the global Permissions-Policy never denies the
// publickey-credentials-* features.
func TestGOTHLoginPasskeyContract(t *testing.T) {
	t.Parallel()
	router := gothProofRouter(t, web.TrustProxies(0), web.SecurityHeaders())
	rec := get(t, router, "/auth/login")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /auth/login = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`<script src="` + authgoth.DefaultPasskeyScriptPath + `" defer></script>`,
		`data-auth-passkey="signin"`,
		`data-options-url="/auth/passkeys/assertion/options"`,
		`data-finish-url="/auth/passkeys/assertion"`,
		`data-auth-passkey-start`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("login body missing %q", want)
		}
	}
	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{"script-src 'self'", "connect-src 'self'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP missing %q in: %s", want, csp)
		}
	}
	if strings.Contains(csp, "unsafe-inline") {
		t.Errorf("CSP admits unsafe-inline: %s", csp)
	}
	if pp := rec.Header().Get("Permissions-Policy"); strings.Contains(pp, "publickey-credentials") {
		t.Errorf("Permissions-Policy %q denies publickey-credentials-*", pp)
	}

	js := get(t, router, authgoth.DefaultPasskeyScriptPath)
	if js.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", authgoth.DefaultPasskeyScriptPath, js.Code)
	}
	if ct := js.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/javascript") {
		t.Errorf("passkey script content-type = %q, want application/javascript", ct)
	}
	if got := js.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("passkey script X-Content-Type-Options = %q, want nosniff", got)
	}

	// The passkey pages mount from PasskeyConfig.Views: an anonymous visit is
	// gated, not a 404.
	if page := get(t, router, "/auth/account/passkeys"); page.Code == http.StatusNotFound {
		t.Error("GET /auth/account/passkeys = 404: PasskeyConfig.Views is not wired")
	}
}

// formPost posts form as an HTML form would: urlencoded, same-origin, with the
// double-submit token in the body.
func (c *linkClient) formPost(path string, form url.Values) (*http.Response, string) {
	c.t.Helper()
	form.Set("csrf_token", c.csrfToken())
	req, err := http.NewRequest(http.MethodPost, c.host.srv.URL+path, strings.NewReader(form.Encode()))
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Origin", c.host.origin)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.t.Fatal(err)
	}
	return resp, string(body)
}

// TestGOTHTOTPPagesThroughTheHost drives the TOTP pages over this host's real
// composition — TOTPConfig.Views from the authpages override, the QR drawn by
// rsc-qr — as plain HTML forms: the chooser as the MFA redirect target, the
// enrollment PRG, the QR code and the key, a wrong code keeping the same key,
// the recovery codes shown once, and a code from the chooser reaching aal2.
func TestGOTHTOTPPagesThroughTheHost(t *testing.T) {
	t.Parallel()
	host := newPasskeyHost(t, nil)
	dora := host.signUp("dora@example.com")

	resp, _ := dora.do("GET", "/demo/mfa/page", "", nil)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/auth/step-up/mfa?return_to=%2Fdemo%2Fmfa%2Fpage" {
		t.Fatalf("AAL1 /demo/mfa/page = %d %q, want the chooser", resp.StatusCode, resp.Header.Get("Location"))
	}
	_, chooser := dora.do("GET", "/auth/step-up/mfa?return_to=%2Fdemo%2Fmfa%2Fpage", "", nil)
	if !strings.Contains(string(chooser), `href="/auth/account/totp"`) || strings.Contains(string(chooser), `action="/auth/step-up/totp"`) {
		t.Fatalf("chooser without TOTP:\n%s", chooser)
	}

	resp, _ = dora.formPost("/auth/totp/enrollment", url.Values{})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/auth/account/totp/enroll" {
		t.Fatalf("enrollment form = %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	secretRE := regexp.MustCompile(`<code data-slot="auth-totp-secret">([A-Z2-7 ]+)</code>`)
	_, page := dora.do("GET", "/auth/account/totp/enroll", "", nil)
	for _, want := range []string{`data-slot="auth-totp-qr"`, `<rect width="100%" height="100%" fill="#fff">`, `<path d="M4 4h7v1h-7z`, `href="otpauth://totp/Gopernicus%20CMS:`} {
		if !strings.Contains(string(page), want) {
			t.Fatalf("enroll page missing %q:\n%s", want, page)
		}
	}
	m := secretRE.FindStringSubmatch(string(page))
	if m == nil {
		t.Fatalf("no secret on the enroll page:\n%s", page)
	}
	secret := strings.ReplaceAll(m[1], " ", "")

	resp, wrong := dora.formPost("/auth/totp/enrollment/confirm", url.Values{"code": {totpCode(t, secret, 5)}})
	if resp.StatusCode != http.StatusUnauthorized || secretRE.FindStringSubmatch(wrong)[1] != m[1] {
		t.Fatalf("wrong code = %d, want 401 with the same key", resp.StatusCode)
	}
	resp, codes := dora.formPost("/auth/totp/enrollment/confirm", url.Values{"code": {totpCode(t, secret, 0)}})
	if resp.StatusCode != http.StatusOK || strings.Count(codes, "<li><code>") != 10 || !strings.Contains(codes, "You won't see them again.") {
		t.Fatalf("confirm = %d:\n%s", resp.StatusCode, codes)
	}

	_, chooser = dora.do("GET", "/auth/step-up/mfa?return_to=%2Fdemo%2Fmfa%2Fpage", "", nil)
	if !strings.Contains(string(chooser), `action="/auth/step-up/totp"`) || !strings.Contains(string(chooser), `action="/auth/step-up/recovery-code"`) {
		t.Fatalf("chooser after enrollment:\n%s", chooser)
	}
	resp, _ = dora.formPost("/auth/step-up/totp", url.Values{"code": {totpCode(t, secret, 1)}, "return_to": {"/demo/mfa/page"}})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/demo/mfa/page" {
		t.Fatalf("TOTP step-up form = %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if status, level := dora.assurance("/demo/mfa"); status != http.StatusOK || level != "aal2" {
		t.Fatalf("/demo/mfa after the form step-up = %d %q", status, level)
	}
}
