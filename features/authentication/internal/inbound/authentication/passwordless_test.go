package authentication

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/features/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// newPasswordlessHandler builds a real authsvc.Service with passwordless enabled
// for email and mounts the full route table with no configured Origin allowlist. The
// enablement matrix is validated by package auth; the inbound test wires the resolved
// kind set + base URL directly.
func newPasswordlessHandler(t *testing.T, limiter ratelimiter.Limiter) http.Handler {
	return newPasswordlessHandlerWith(t, limiter, MutationSecurity{})
}

// newPasswordlessHandlerWith mounts the passwordless routes under a given
// browser-safe-mutation policy so the credential-establishment origin gate (design
// §9.1) can be exercised with a configured Origin allowlist.
func newPasswordlessHandlerWith(t *testing.T, limiter ratelimiter.Limiter, mutation MutationSecurity) http.Handler {
	t.Helper()
	if limiter == nil {
		limiter = ratelimiter.NewMemory()
	}
	users := newMemUsers()
	router, err := delivery.NewRouter(delivery.Deps{Mailer: nopMailer{}, MailFrom: "noreply@example.com"})
	if err != nil {
		t.Fatalf("delivery.NewRouter: %v", err)
	}
	svc := authsvc.NewService(authsvc.Deps{
		Users:             users,
		Identifiers:       newMemIdentifiers(users),
		Passwords:         &memPasswords{m: map[string]string{}},
		Sessions:          &memSessions{m: map[string]session.Session{}},
		Challenges:        &memChallenges{byID: map[string]challenge.Challenge{}},
		Protector:         memProtector{},
		Hasher:            fakeHasher{},
		Mailer:            nopMailer{},
		MailFrom:          "noreply@example.com",
		Deliver:           router,
		Queue:             stubQueue{},
		Limiter:           limiter,
		Cookie:            authsvc.CookieConfig{},
		TokenSigner:       newFakeSigner(),
		Passwordless:      []string{"email"},
		PublicAuthBaseURL: "https://auth.example.com",
	})
	h := web.NewWebHandler()
	Mount(h, svc, nil, "", mutation, nil)
	return h
}

// TestPasswordlessStartRouteAbsentByDefault proves deny-by-absence: with no
// passwordless kind enabled, the start route is not registered (404).
func TestPasswordlessStartRouteAbsentByDefault(t *testing.T) {
	h := newTestHandler(t, nil) // passwordless off
	rec := do(t, h, "POST", "/auth/passwordless/start", `{"identifier_kind":"email","identifier":"a@example.com"}`)
	if rec.Code != http.StatusNotFound {
		t.Errorf("start status with passwordless off = %d, want 404", rec.Code)
	}
}

// TestPasswordlessStartAccepted proves the enabled start returns the uniform 202
// accepted body for a valid identifier.
func TestPasswordlessStartAccepted(t *testing.T) {
	h := newPasswordlessHandler(t, nil)
	rec := do(t, h, "POST", "/auth/passwordless/start", `{"identifier_kind":"email","identifier":"a@example.com"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("start status = %d, want 202; body=%s", rec.Code, rec.Body)
	}
}

// TestPasswordlessStartUniformForUnknownAndMalformed proves the enumeration
// contract at the transport: an unknown-but-valid and a malformed identifier both
// return the same 202 accepted shape.
func TestPasswordlessStartUniformForUnknownAndMalformed(t *testing.T) {
	h := newPasswordlessHandler(t, nil)
	for _, body := range []string{
		`{"identifier_kind":"email","identifier":"ghost@example.com"}`,
		`{"identifier_kind":"email","identifier":"not-an-email"}`,
	} {
		rec := do(t, h, "POST", "/auth/passwordless/start", body)
		if rec.Code != http.StatusAccepted {
			t.Errorf("start(%s) status = %d, want 202", body, rec.Code)
		}
	}
}

// TestPasswordlessStartStrictDecode proves the strict body: an unknown field is a
// 400.
func TestPasswordlessStartStrictDecode(t *testing.T) {
	h := newPasswordlessHandler(t, nil)
	rec := do(t, h, "POST", "/auth/passwordless/start", `{"identifier_kind":"email","identifier":"a@example.com","extra":1}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unknown-field body status = %d, want 400", rec.Code)
	}
}

// TestPasswordlessStartDisabledKind proves a start for a kind the host did not
// enable is a 400 (a deterministic request-shape rejection, not an account signal).
func TestPasswordlessStartDisabledKind(t *testing.T) {
	h := newPasswordlessHandler(t, nil) // only email enabled
	rec := do(t, h, "POST", "/auth/passwordless/start", `{"identifier_kind":"phone","identifier":"+15551112222"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("disabled-kind start status = %d, want 400", rec.Code)
	}
}

// TestPasswordlessStartRateLimited proves an exhausted start budget is a generic
// 429.
func TestPasswordlessStartRateLimited(t *testing.T) {
	h := newPasswordlessHandler(t, denyLimiter{})
	rec := do(t, h, "POST", "/auth/passwordless/start", `{"identifier_kind":"email","identifier":"a@example.com"}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("rate-limited start status = %d, want 429", rec.Code)
	}
}

// TestPasswordlessVerifyRouteAbsentByDefault proves deny-by-absence: with no
// passwordless kind enabled, the verify route is not registered (404).
func TestPasswordlessVerifyRouteAbsentByDefault(t *testing.T) {
	h := newTestHandler(t, nil) // passwordless off
	rec := do(t, h, "POST", "/auth/passwordless/verify", `{"identifier_kind":"email","identifier":"a@example.com","code":"123456"}`)
	if rec.Code != http.StatusNotFound {
		t.Errorf("verify status with passwordless off = %d, want 404", rec.Code)
	}
}

// TestPasswordlessVerifyStrictDecode proves the strict body: an unknown field is a
// 400.
func TestPasswordlessVerifyStrictDecode(t *testing.T) {
	h := newPasswordlessHandler(t, nil)
	rec := do(t, h, "POST", "/auth/passwordless/verify", `{"identifier_kind":"email","identifier":"a@example.com","code":"123456","extra":1}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unknown-field verify status = %d, want 400", rec.Code)
	}
}

// TestPasswordlessVerifyGeneric401 proves every failure reason collapses to one
// generic 401 at the transport (design §5.8): an unknown identifier is a 401,
// indistinguishable from a wrong code, and mints no session cookie.
func TestPasswordlessVerifyGeneric401(t *testing.T) {
	h := newPasswordlessHandler(t, nil)
	rec := do(t, h, "POST", "/auth/passwordless/verify", `{"identifier_kind":"email","identifier":"ghost@example.com","code":"123456"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown-identifier verify status = %d, want 401; body=%s", rec.Code, rec.Body)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Errorf("failed verify set %d cookies, want 0", len(rec.Result().Cookies()))
	}
}

// TestPasswordlessVerifyRateLimited proves an exhausted verify budget is a generic
// 429, distinct from the credential 401.
func TestPasswordlessVerifyRateLimited(t *testing.T) {
	h := newPasswordlessHandler(t, denyLimiter{})
	rec := do(t, h, "POST", "/auth/passwordless/verify", `{"identifier_kind":"email","identifier":"a@example.com","code":"123456"}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("rate-limited verify status = %d, want 429", rec.Code)
	}
}

// TestPasswordlessRedeemRouteAbsentByDefault proves deny-by-absence: with no
// passwordless kind enabled, the redeem route is not registered (404).
func TestPasswordlessRedeemRouteAbsentByDefault(t *testing.T) {
	h := newTestHandler(t, nil) // passwordless off
	rec := do(t, h, "POST", "/auth/passwordless/redeem", `{"token":"abc"}`)
	if rec.Code != http.StatusNotFound {
		t.Errorf("redeem status with passwordless off = %d, want 404", rec.Code)
	}
}

// TestPasswordlessRedeemStrictDecode proves the strict body: an unknown field is a
// 400.
func TestPasswordlessRedeemStrictDecode(t *testing.T) {
	h := newPasswordlessHandler(t, nil)
	rec := do(t, h, "POST", "/auth/passwordless/redeem", `{"token":"abc","extra":1}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unknown-field redeem status = %d, want 400", rec.Code)
	}
}

// TestPasswordlessRedeemGeneric401 proves an unknown/garbage token collapses to one
// generic 401 (design §5.8) and mints no session cookie.
func TestPasswordlessRedeemGeneric401(t *testing.T) {
	h := newPasswordlessHandler(t, nil)
	rec := do(t, h, "POST", "/auth/passwordless/redeem", `{"token":"not-a-real-token"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown-token redeem status = %d, want 401; body=%s", rec.Code, rec.Body)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Errorf("failed redeem set %d cookies, want 0", len(rec.Result().Cookies()))
	}
}

// TestPasswordlessRedeemRateLimited proves an exhausted redeem budget is a generic
// 429, distinct from the credential 401.
func TestPasswordlessRedeemRateLimited(t *testing.T) {
	h := newPasswordlessHandler(t, denyLimiter{})
	rec := do(t, h, "POST", "/auth/passwordless/redeem", `{"token":"abc"}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("rate-limited redeem status = %d, want 429", rec.Code)
	}
}

// TestPasswordlessRedeemNoGetConsume proves a link scanner cannot authenticate a user
// by merely fetching the URL: there is no GET redeem route, so a GET is rejected
// (method not allowed) and sets no session cookie (design §6.4 — no GET consumes a
// token).
func TestPasswordlessRedeemNoGetConsume(t *testing.T) {
	h := newPasswordlessHandler(t, nil)
	rec := do(t, h, "GET", "/auth/passwordless/redeem", "")
	if rec.Code == http.StatusOK {
		t.Fatalf("GET redeem returned 200 — a scanner GET must never consume a token")
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Errorf("GET redeem set %d cookies, want 0", len(rec.Result().Cookies()))
	}
}

// TestPasswordlessRedeemSetsNoStoreNoReferrer proves the credential-establishment
// response guidance rides every redeem response (design §6.4/§9.1): Cache-Control:
// no-store keeps the pair out of shared caches and Referrer-Policy: no-referrer keeps
// a fragment-borne token out of any downstream Referer header.
func TestPasswordlessRedeemSetsNoStoreNoReferrer(t *testing.T) {
	h := newPasswordlessHandler(t, nil)
	rec := do(t, h, "POST", "/auth/passwordless/redeem", `{"token":"not-a-real-token"}`)
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if got := rec.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("Referrer-Policy = %q, want no-referrer", got)
	}
}

// TestPasswordlessRedeemHostileHostIgnored proves a spoofed Host header does not
// change redeem behavior: the redeem consumes the token and re-resolves the bound
// identifier off the request, never off the request Host, so a hostile Host yields
// the identical generic 401 (design §6.4 — request Host never participates).
func TestPasswordlessRedeemHostileHostIgnored(t *testing.T) {
	h := newPasswordlessHandler(t, nil)
	r := httptest.NewRequest("POST", "/auth/passwordless/redeem", strings.NewReader(`{"token":"not-a-real-token"}`))
	r.Host = "evil.example.com"
	r.Header.Set("X-Forwarded-Host", "evil.example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("redeem with hostile Host status = %d, want 401 (Host ignored)", rec.Code)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Errorf("redeem with hostile Host set %d cookies, want 0", len(rec.Result().Cookies()))
	}
}

// passwordlessOrigins is the Origin allowlist for the credential-establishment gate
// tests. The session cookie name marks a request browser-driven for the shared gate
// config, though the passwordless gate never checks a session (there is none yet).
var passwordlessOrigins = MutationSecurity{
	AllowedOrigins:    []string{"https://app.example.com"},
	SessionCookieName: testSessionCookie,
}

// doOrigin issues a POST carrying the given Origin / Sec-Fetch-Site surface so the
// credential-establishment origin gate (design §9.1) can be exercised.
func doOrigin(t *testing.T, h http.Handler, path, body, origin, secFetchSite string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if secFetchSite != "" {
		r.Header.Set("Sec-Fetch-Site", secFetchSite)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

// TestPasswordlessStartNoPreexistingCSRFSession proves the §9.1 credential-
// establishment distinction: a same-origin browser start succeeds (202) WITHOUT any
// pre-existing auth_csrf cookie/header — these endpoints have no session yet, so the
// double-submit CSRF gate must not apply, or a first-time browser sign-in could never
// begin.
func TestPasswordlessStartNoPreexistingCSRFSession(t *testing.T) {
	h := newPasswordlessHandlerWith(t, nil, passwordlessOrigins)
	rec := doOrigin(t, h, "/auth/passwordless/start", `{"identifier_kind":"email","identifier":"a@example.com"}`,
		"https://app.example.com", "same-origin")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("same-origin start (no CSRF cookie) = %d, want 202; body=%s", rec.Code, rec.Body)
	}
}

// TestPasswordlessCredentialEstablishmentRejectsCrossSite proves a cross-site page
// cannot force a credential mint: a disallowed Origin is rejected (403) on start,
// verify, and redeem, and no session cookie is minted — so the minted cookie can
// never be established from a disallowed origin (design §9.1).
func TestPasswordlessCredentialEstablishmentRejectsCrossSite(t *testing.T) {
	h := newPasswordlessHandlerWith(t, nil, passwordlessOrigins)
	cases := []struct {
		path, body string
	}{
		{"/auth/passwordless/start", `{"identifier_kind":"email","identifier":"a@example.com"}`},
		{"/auth/passwordless/verify", `{"identifier_kind":"email","identifier":"a@example.com","code":"123456"}`},
		{"/auth/passwordless/redeem", `{"token":"abc"}`},
	}
	for _, c := range cases {
		rec := doOrigin(t, h, c.path, c.body, "https://evil.example.com", "cross-site")
		if rec.Code != http.StatusForbidden {
			t.Errorf("cross-site %s status = %d, want 403", c.path, rec.Code)
		}
		if len(rec.Result().Cookies()) != 0 {
			t.Errorf("cross-site %s minted %d cookies, want 0", c.path, len(rec.Result().Cookies()))
		}
	}
}

// TestPasswordlessCredentialEstablishmentAllowsListedOrigin proves an allowlisted
// cross-origin start is admitted (202): the gate allows a configured origin, it just
// rejects everything else (design §9.1).
func TestPasswordlessCredentialEstablishmentAllowsListedOrigin(t *testing.T) {
	h := newPasswordlessHandlerWith(t, nil, passwordlessOrigins)
	rec := doOrigin(t, h, "/auth/passwordless/start", `{"identifier_kind":"email","identifier":"a@example.com"}`,
		"https://app.example.com", "cross-site")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("allowlisted cross-origin start = %d, want 202; body=%s", rec.Code, rec.Body)
	}
}

// TestPasswordlessCredentialEstablishmentAllowsNativeClient proves the phase-7 stop
// condition: a native/mobile/CLI client that sends neither Origin nor Sec-Fetch-Site
// is admitted (202) even under a configured Origin allowlist — browser origin
// enforcement never blocks the bearer/body non-cookie path (design §9.1).
func TestPasswordlessCredentialEstablishmentAllowsNativeClient(t *testing.T) {
	h := newPasswordlessHandlerWith(t, nil, passwordlessOrigins)
	rec := doOrigin(t, h, "/auth/passwordless/start", `{"identifier_kind":"email","identifier":"a@example.com"}`, "", "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("native-client start (no Origin) = %d, want 202; body=%s", rec.Code, rec.Body)
	}
}
