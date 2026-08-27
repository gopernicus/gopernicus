package authentication

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/pockets/authentication/internal/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// JSON CSRF bootstrap tests (upstream evidence §1a/§4). GET /auth/csrf is the
// supported seam for a cookie-authenticated SPA served from a DIFFERENT origin
// than the API: it cannot read the API-origin __Host-auth_csrf cookie, so it reads the
// matching token from this body. The end-to-end test below drives a real TLS
// server through a real cookie jar, so it NEVER reads the cookie value itself —
// only the JSON body — exactly like the browser it stands in for.

const (
	// spaOrigin is the allowlisted, same-site-but-cross-origin SPA.
	spaOrigin = "https://spa.example.com"

	// corsOnlyOrigin is allowed by CORS but NOT by the feature's mutation
	// allowlist — the misconfiguration origin_rejected exists to diagnose.
	corsOnlyOrigin = "https://other.example.com"
)

// newCSRFHandler mounts the routes over the in-memory fakes with spaOrigin
// allowlisted for browser mutations, and installs the sdk CORS middleware
// globally the way a cross-origin host wires it: the HOST opts X-CSRF-Token into
// the request-header policy (the sdk knows no feature header). corsOnlyOrigin is
// CORS-allowed so a browser can actually read the feature's own origin denial.
func newCSRFHandler(t *testing.T) *web.WebHandler {
	t.Helper()
	users := newMemUsers()
	identifiers := newMemIdentifiers(users)
	passwords := &memPasswords{m: map[string]string{}}
	sessions := &memSessions{m: map[string]session.Session{}}
	challenges := &memChallenges{byID: map[string]challenge.Challenge{}}
	router, err := delivery.NewRouter(delivery.Deps{Mailer: nopMailer{}, MailFrom: "noreply@example.com"})
	if err != nil {
		t.Fatalf("delivery.NewRouter: %v", err)
	}
	svc := authsvc.NewService(authsvc.Deps{
		Users:          users,
		Identifiers:    identifiers,
		Passwords:      passwords,
		Sessions:       sessions,
		Challenges:     challenges,
		Protector:      memProtector{},
		PasswordResets: &memPasswordResets{ch: challenges, pw: passwords, sess: sessions},
		Hasher:         fakeHasher{},
		Deliver:        router,
		Queue:          stubQueue{},
		Limiter:        ratelimiter.NewMemory(),
		Cookie:         authsvc.CookieConfig{},
		TokenSigner:    newFakeSigner(),
	})
	h := web.NewWebHandler()
	h.Use(web.CORSWithConfig(web.CORSConfig{
		AllowedOrigins: []string{spaOrigin, corsOnlyOrigin},
		AllowedHeaders: []string{"Accept", "Content-Type", "Authorization", csrfHeaderName},
	}))
	Mount(h, Deps{Auth: svc, Mutation: MutationSecurity{
		AllowedOrigins:    []string{spaOrigin},
		SessionCookieName: svc.SessionCookieName(),
	}})
	return h
}

// spaRequest builds a request carrying the SPA's browser surface (Origin +
// Sec-Fetch-Site: same-site — a sibling origin under one registrable domain).
func spaRequest(method, path, body, origin string, cookies ...*http.Cookie) *http.Request {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
		r.Header.Set("Sec-Fetch-Site", "same-site")
	}
	for _, c := range cookies {
		r.AddCookie(c)
	}
	return r
}

// bootstrapToken decodes the bootstrap body. The cookie value is deliberately not
// consulted: a cross-origin SPA cannot read it, so neither may this helper.
func bootstrapToken(t *testing.T, body []byte) string {
	t.Helper()
	var resp struct {
		Token string `json:"csrf_token"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode bootstrap body %q: %v", body, err)
	}
	if resp.Token == "" {
		t.Fatalf("bootstrap body carried no csrf_token: %s", body)
	}
	return resp.Token
}

// TestCSRFBootstrapCrossOriginSPAFlow is the headline end-to-end proof: an
// allowlisted, same-site cross-origin SPA logs in over cookies, bootstraps a CSRF
// token from the JSON body WITHOUT reading the API-origin cookie, clears a real
// preflight through the host's configured sdk CORS middleware, and completes the
// browser-safe mutation with the session cookie, the returned CSRF cookie, and the
// X-CSRF-Token echo header.
func TestCSRFBootstrapCrossOriginSPAFlow(t *testing.T) {
	srv := httptest.NewTLSServer(newCSRFHandler(t))
	defer srv.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	client := srv.Client()
	client.Jar = jar

	send := func(method, path, body string, header http.Header) (*http.Response, []byte) {
		t.Helper()
		var rdr io.Reader
		if body != "" {
			rdr = strings.NewReader(body)
		}
		req, err := http.NewRequest(method, srv.URL+path, rdr)
		if err != nil {
			t.Fatalf("new request %s %s: %v", method, path, err)
		}
		req.Header.Set("Origin", spaOrigin)
		req.Header.Set("Sec-Fetch-Site", "same-site")
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		for k, vs := range header {
			for _, v := range vs {
				req.Header.Add(k, v)
			}
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		payload, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read %s %s body: %v", method, path, err)
		}
		return resp, payload
	}

	send("POST", "/auth/register", `{"email":"spa@example.com","password":"password123456789","display_name":"S"}`, nil)
	if resp, body := send("POST", "/auth/login", `{"email":"spa@example.com","password":"password123456789"}`, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want 200; body=%s", resp.StatusCode, body)
	}

	// The mutation's preflight clears the host's configured CORS policy: the echo
	// header is allowed and the response is credentialed.
	preflight, _ := send("OPTIONS", "/auth/password/change", "", http.Header{
		"Access-Control-Request-Method":  {"POST"},
		"Access-Control-Request-Headers": {"content-type, x-csrf-token"},
	})
	if preflight.StatusCode != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", preflight.StatusCode)
	}
	if got := preflight.Header.Get("Access-Control-Allow-Origin"); got != spaOrigin {
		t.Errorf("preflight Allow-Origin = %q, want %q", got, spaOrigin)
	}
	if got := preflight.Header.Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("preflight Allow-Credentials = %q, want true", got)
	}
	if got := preflight.Header.Get("Access-Control-Allow-Headers"); !strings.Contains(strings.ToLower(got), strings.ToLower(csrfHeaderName)) {
		t.Fatalf("preflight Allow-Headers = %q, want it to include %s", got, csrfHeaderName)
	}

	boot, bootBody := send("GET", "/auth/csrf", "", nil)
	if boot.StatusCode != http.StatusOK {
		t.Fatalf("bootstrap status = %d, want 200; body=%s", boot.StatusCode, bootBody)
	}
	if got := boot.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("bootstrap Cache-Control = %q, want no-store", got)
	}
	if got := boot.Header.Get("Access-Control-Allow-Origin"); got != spaOrigin {
		t.Errorf("bootstrap Allow-Origin = %q, want %q (the SPA must be able to read the body)", got, spaOrigin)
	}
	token := bootstrapToken(t, bootBody)

	// The mutation itself: the jar carries the session + CSRF cookies the SPA can
	// never read, and the echo header carries the token it read from the body.
	change, changeBody := send("POST", "/auth/password/change",
		`{"current_password":"password123456789","new_password":"newpassword456789"}`,
		http.Header{csrfHeaderName: {token}})
	if change.StatusCode != http.StatusOK {
		t.Fatalf("cross-origin password change status = %d, want 200; body=%s", change.StatusCode, changeBody)
	}

	// Without the echo header the same request is a generic CSRF denial — NOT an
	// origin rejection: the origin was fine, the double-submit was not.
	denied, deniedBody := send("POST", "/auth/password/change",
		`{"current_password":"newpassword456789","new_password":"finalpass789012"}`, nil)
	if denied.StatusCode != http.StatusForbidden {
		t.Fatalf("change without the echo header = %d, want 403; body=%s", denied.StatusCode, deniedBody)
	}
	if code := bodyErrorCode(t, deniedBody); code != "permission_denied" {
		t.Errorf("missing-token denial code = %q, want permission_denied", code)
	}
}

// bodyErrorCode reads the machine-readable code off a JSON error body read from a
// live http.Response (the recorder-based sibling is errorCode).
func bodyErrorCode(t *testing.T, body []byte) string {
	t.Helper()
	var resp struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode error body %q: %v", body, err)
	}
	return resp.Code
}

// TestCSRFBootstrapBodyMatchesCookie pins the contract the SPA depends on: the
// token in the body is ALWAYS the value of the __Host-auth_csrf cookie on the same
// response, whether it was minted or reused.
func TestCSRFBootstrapBodyMatchesCookie(t *testing.T) {
	h := newCSRFHandler(t)
	sess := csrfLogin(t, h, "match@example.com")

	first := serve(h, spaRequest("GET", "/auth/csrf", "", spaOrigin, sess))
	if first.Code != http.StatusOK {
		t.Fatalf("bootstrap status = %d, want 200; body=%s", first.Code, first.Body)
	}
	minted := bootstrapToken(t, first.Body.Bytes())
	mintedCookie := csrfCookie(first)
	if mintedCookie == nil {
		t.Fatal("bootstrap set no __Host-auth_csrf cookie")
	}
	if mintedCookie.Value != minted {
		t.Fatalf("minted cookie = %q, body = %q; the two must never differ", mintedCookie.Value, minted)
	}

	second := serve(h, spaRequest("GET", "/auth/csrf", "", spaOrigin, sess, &http.Cookie{Name: csrfCookieName, Value: mintedCookie.Value}))
	reused := bootstrapToken(t, second.Body.Bytes())
	reusedCookie := csrfCookie(second)
	if reusedCookie == nil {
		t.Fatal("bootstrap reuse set no __Host-auth_csrf cookie")
	}
	if reusedCookie.Value != reused {
		t.Fatalf("reused cookie = %q, body = %q; the two must never differ", reusedCookie.Value, reused)
	}
}

// TestCSRFBootstrapReusesExistingToken is the risk-3 regression: repeated
// bootstraps must NOT rotate, or a second tab holding the current token in flight
// is silently broken. A malformed (never-minted) cookie value is not reused.
func TestCSRFBootstrapReusesExistingToken(t *testing.T) {
	h := newCSRFHandler(t)
	sess := csrfLogin(t, h, "reuse@example.com")

	first := serve(h, spaRequest("GET", "/auth/csrf", "", spaOrigin, sess))
	token := bootstrapToken(t, first.Body.Bytes())
	held := &http.Cookie{Name: csrfCookieName, Value: csrfCookie(first).Value}

	for i := 0; i < 3; i++ {
		again := serve(h, spaRequest("GET", "/auth/csrf", "", spaOrigin, sess, held))
		if again.Code != http.StatusOK {
			t.Fatalf("repeat bootstrap %d status = %d, want 200; body=%s", i, again.Code, again.Body)
		}
		if got := bootstrapToken(t, again.Body.Bytes()); got != token {
			t.Fatalf("repeat bootstrap %d rotated the token: %q -> %q", i, token, got)
		}
	}

	// A foreign, wrong-shape cookie value is never echoed back as if the feature
	// had minted it.
	fresh := serve(h, spaRequest("GET", "/auth/csrf", "", spaOrigin, sess, &http.Cookie{Name: csrfCookieName, Value: "attacker-chosen"}))
	if got := bootstrapToken(t, fresh.Body.Bytes()); got == "attacker-chosen" {
		t.Fatal("bootstrap echoed a malformed cookie value instead of minting")
	}
}

// TestCSRFBootstrapReusesForeignWellFormedToken PINS the accepted residual the
// sibling malformed-cookie case above does NOT cover: the shape check is not a
// provenance check. A well-formed 32-byte base64url __Host-auth_csrf cookie the
// feature never minted IS reused and echoed in the body.
//
// Provenance is the cookie NAME's job, not this check's: the __Host- prefix makes
// a browser refuse a Domain-scoped Set-Cookie for this name, so a sibling host
// under the same registrable domain can no longer plant it — what remains is
// script already on this origin. Prefix rules are enforced by BROWSERS; Go's
// net/http treats the name as an ordinary string, which is exactly why this test
// can hand the handler a foreign value at all.
//
// Reuse itself is a deliberate decision, not an accident: rotating on every
// bootstrap would break a second tab holding the current token in flight, and the
// ORIGIN ALLOWLIST is the control that makes a fixated token unusable (the
// mutation must still come from an allowlisted origin — see
// TestCSRFBootstrapRejectsDisallowedOrigin). If this test ever fails, the reuse
// policy changed and the security note on currentCSRFToken must change with it.
func TestCSRFBootstrapReusesForeignWellFormedToken(t *testing.T) {
	h := newCSRFHandler(t)
	sess := csrfLogin(t, h, "foreign@example.com")

	var raw [csrfTokenBytes]byte
	for i := range raw {
		raw[i] = byte(i)
	}
	foreign := base64.RawURLEncoding.EncodeToString(raw[:])

	rec := serve(h, spaRequest("GET", "/auth/csrf", "", spaOrigin, sess, &http.Cookie{Name: csrfCookieName, Value: foreign}))
	if rec.Code != http.StatusOK {
		t.Fatalf("bootstrap status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if got := bootstrapToken(t, rec.Body.Bytes()); got != foreign {
		t.Fatalf("bootstrap body = %q, want the foreign value %q reused (pinned current behavior)", got, foreign)
	}
	c := csrfCookie(rec)
	if c == nil {
		t.Fatal("bootstrap set no __Host-auth_csrf cookie")
	}
	if c.Value != foreign {
		t.Fatalf("bootstrap cookie = %q, want the foreign value %q reused (pinned current behavior)", c.Value, foreign)
	}
}

// TestCSRFBootstrapRequiresLiveSession proves the gate: no credential cannot
// bootstrap, and a revoked session cannot bootstrap either (RequireLiveSession,
// not the ≤AccessTokenTTL stale window).
func TestCSRFBootstrapRequiresLiveSession(t *testing.T) {
	h := newCSRFHandler(t)

	if rec := serve(h, spaRequest("GET", "/auth/csrf", "", spaOrigin)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bootstrap without a session = %d, want 401; body=%s", rec.Code, rec.Body)
	}

	sess := csrfLogin(t, h, "revoked@example.com")
	if rec := serve(h, spaRequest("GET", "/auth/csrf", "", spaOrigin, sess)); rec.Code != http.StatusOK {
		t.Fatalf("bootstrap with a live session = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	// Logout deletes the session row; the access JWT itself is still unexpired.
	out := serve(h, spaRequest("POST", "/auth/logout", "", spaOrigin, sess))
	if out.Code != http.StatusOK {
		t.Fatalf("logout = %d, want 200; body=%s", out.Code, out.Body)
	}
	if rec := serve(h, spaRequest("GET", "/auth/csrf", "", spaOrigin, sess)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bootstrap with a revoked session = %d, want 401; body=%s", rec.Code, rec.Body)
	}
}

// TestCSRFBootstrapRejectsDisallowedOrigin is the §4 diagnosability proof: an
// origin CORS allows but the feature's allowlist does not gets a 403 the browser
// can actually READ, carrying the stable origin_rejected code rather than the
// permission_denied an authorization denial uses.
func TestCSRFBootstrapRejectsDisallowedOrigin(t *testing.T) {
	h := newCSRFHandler(t)
	sess := csrfLogin(t, h, "denied@example.com")

	rec := serve(h, spaRequest("GET", "/auth/csrf", "", corsOnlyOrigin, sess))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("disallowed-origin bootstrap = %d, want 403; body=%s", rec.Code, rec.Body)
	}
	if code := errorCode(t, rec); code != "origin_rejected" {
		t.Fatalf("disallowed-origin code = %q, want origin_rejected", code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != corsOnlyOrigin {
		t.Errorf("Allow-Origin = %q, want %q — the denial must be readable to be diagnosable", got, corsOnlyOrigin)
	}
	if csrfCookie(rec) != nil {
		t.Error("a rejected origin must not receive a CSRF cookie")
	}
}

// TestOriginRejectionCodeSplit pins the split at both gates: an origin failure
// carries origin_rejected while a failed double-submit keeps permission_denied.
func TestOriginRejectionCodeSplit(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	cfg := csrfConfig{allowedOrigins: []string{spaOrigin}, sessionCookieName: testSessionCookie}

	tests := []struct {
		name string
		mw   web.Middleware
		req  *http.Request
		want string
	}{
		{
			name: "mutation gate origin failure",
			mw:   requireBrowserSafeMutation(cfg),
			req:  csrfReq{origin: corsOnlyOrigin, secFetchSite: "same-site", sessionCookie: true, csrfCookie: "abc", csrfHeader: "abc"}.build(),
			want: "origin_rejected",
		},
		{
			name: "mutation gate token failure",
			mw:   requireBrowserSafeMutation(cfg),
			req:  csrfReq{origin: spaOrigin, secFetchSite: "same-site", sessionCookie: true, csrfCookie: "abc", csrfHeader: "def"}.build(),
			want: "permission_denied",
		},
		{
			name: "origin-only gate failure",
			mw:   requireBrowserSafeOrigin(cfg),
			req:  csrfReq{origin: corsOnlyOrigin, secFetchSite: "same-site"}.build(),
			want: "origin_rejected",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tt.mw(next).ServeHTTP(rec, tt.req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", rec.Code)
			}
			if code := errorCode(t, rec); code != tt.want {
				t.Fatalf("code = %q, want %q", code, tt.want)
			}
		})
	}
}

// TestCSRFBootstrapFailsClosedWithoutRandomness proves the fail-closed path: with
// no entropy the bootstrap returns a safe 500, no token, and no cookie.
func TestCSRFBootstrapFailsClosedWithoutRandomness(t *testing.T) {
	h := newCSRFHandler(t)
	sess := csrfLogin(t, h, "norand@example.com")

	original := randRead
	randRead = func([]byte) (int, error) { return 0, errors.New("no entropy") }
	t.Cleanup(func() { randRead = original })

	rec := serve(h, spaRequest("GET", "/auth/csrf", "", spaOrigin, sess))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("bootstrap without entropy = %d, want 500; body=%s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "csrf_token") {
		t.Fatalf("failed bootstrap returned a token body: %s", rec.Body)
	}
	if csrfCookie(rec) != nil {
		t.Error("failed bootstrap set a CSRF cookie")
	}
}

// csrfLogin registers and logs in a user over the cookie lane, returning the
// access cookie.
func csrfLogin(t *testing.T, h http.Handler, email string) *http.Cookie {
	t.Helper()
	serve(h, spaRequest("POST", "/auth/register", `{"email":"`+email+`","password":"password123456789","display_name":"C"}`, spaOrigin))
	rec := serve(h, spaRequest("POST", "/auth/login", `{"email":"`+email+`","password":"password123456789"}`, spaOrigin))
	c := sessionCookie(rec)
	if c == nil {
		t.Fatalf("login set no session cookie; status=%d body=%s", rec.Code, rec.Body)
	}
	return &http.Cookie{Name: c.Name, Value: c.Value}
}

// csrfCookie extracts the __Host-auth_csrf cookie a response set, if any.
func csrfCookie(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range (&http.Response{Header: rec.Header()}).Cookies() {
		if c.Name == csrfCookieName {
			return c
		}
	}
	return nil
}
