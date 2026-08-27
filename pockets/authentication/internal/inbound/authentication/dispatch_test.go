package authentication

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// Dual JSON/form transport tests (AV3-8.3). They prove the single content-type
// dispatcher routes application/json to the byte-stable JSON contract and a
// urlencoded/multipart body to the HTML form arm only when Views is wired, that an
// unsupported type is 415, that form successes are 303 Post/Redirect/Get through the
// redirect allowlist, and that the JSON contract is unchanged by the presence of
// Views. Account-security forms are AV3-8.4.

// doCT posts body to path with an explicit Content-Type.
func doCT(t *testing.T, h http.Handler, path, contentType, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	r.Header.Set("Content-Type", contentType)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

// doJSONReq posts a JSON body with the application/json Content-Type.
func doJSONReq(t *testing.T, h http.Handler, path, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	return doCT(t, h, path, "application/json", body, cookies...)
}

// doFormReq posts a urlencoded form body.
func doFormReq(t *testing.T, h http.Handler, path string, form url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	return doCT(t, h, path, "application/x-www-form-urlencoded", form.Encode(), cookies...)
}

// sharedPOSTs are the canonical endpoints the content-type dispatcher fronts. Each
// gets one JSON body and one form body; the dispatch is asserted by routing, not by
// business success.
var sharedPOSTs = []struct {
	path string
	json string
	form url.Values
}{
	{"/auth/register", `{"email":"z@example.com","password":"password123456789","display_name":"Z"}`, url.Values{"email": {"z@example.com"}, "password": {"password123456789"}, "display_name": {"Z"}}},
	{"/auth/login", `{"email":"z@example.com","password":"password123456789"}`, url.Values{"email": {"z@example.com"}, "password": {"password123456789"}}},
	{"/auth/verify", `{"email":"z@example.com","code":"000000"}`, url.Values{"email": {"z@example.com"}, "code": {"000000"}}},
	{"/auth/password/forgot", `{"email":"z@example.com"}`, url.Values{"email": {"z@example.com"}}},
	{"/auth/password/reset", `{"token":"nope","password":"password123456789"}`, url.Values{"token": {"nope"}, "password": {"password123456789"}}},
	{"/auth/passwordless/start", `{"identifier_kind":"email","identifier":"z@example.com"}`, url.Values{"kind": {"email"}, "identifier": {"z@example.com"}}},
	{"/auth/passwordless/verify", `{"identifier_kind":"email","identifier":"z@example.com","code":"000000"}`, url.Values{"kind": {"email"}, "identifier": {"z@example.com"}, "code": {"000000"}}},
	{"/auth/passwordless/redeem", `{"token":"nope"}`, url.Values{"token": {"nope"}}},
}

// TestContentTypeDispatchRoutesByContentType proves the dispatcher selects JSON from
// application/json, form from urlencoded (Views wired), and 415 for an unsupported
// type — never by Accept, never by router ordering.
func TestContentTypeDispatchRoutesByContentType(t *testing.T) {
	h := newHTMLTestHandler(t, stubViews{})
	for _, e := range sharedPOSTs {
		if rec := doJSONReq(t, h, e.path, e.json); rec.Code == http.StatusUnsupportedMediaType || rec.Code == http.StatusNotFound {
			t.Errorf("JSON POST %s = %d, want the JSON arm reached", e.path, rec.Code)
		}
		if rec := doFormReq(t, h, e.path, e.form); rec.Code == http.StatusUnsupportedMediaType || rec.Code == http.StatusNotFound {
			t.Errorf("form POST %s = %d, want the form arm reached (Views wired)", e.path, rec.Code)
		}
		if rec := doCT(t, h, e.path, "text/plain", "hello"); rec.Code != http.StatusUnsupportedMediaType {
			t.Errorf("text/plain POST %s = %d, want 415", e.path, rec.Code)
		}
	}
}

// TestContentTypeDispatchNilViews proves the API-only posture: a form body is 415
// while the JSON arm still works (no route change, no page).
func TestContentTypeDispatchNilViews(t *testing.T) {
	h := newHTMLTestHandler(t, nil)
	for _, e := range sharedPOSTs {
		if rec := doFormReq(t, h, e.path, e.form); rec.Code != http.StatusUnsupportedMediaType {
			t.Errorf("form POST %s with nil Views = %d, want 415", e.path, rec.Code)
		}
		if rec := doJSONReq(t, h, e.path, e.json); rec.Code == http.StatusUnsupportedMediaType || rec.Code == http.StatusNotFound {
			t.Errorf("JSON POST %s with nil Views = %d, want the JSON arm still mounted", e.path, rec.Code)
		}
	}
}

// TestJSONContractUnchangedByViews proves the JSON register/login DTO/status/cookie
// contract is identical whether Views is wired or nil, and whether the client sends
// application/json or no Content-Type (the pre-v3 lenient client).
func TestJSONContractUnchangedByViews(t *testing.T) {
	for _, tc := range []struct {
		name  string
		views Views
	}{{"views", stubViews{}}, {"nil", nil}} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHTMLTestHandler(t, tc.views)
			// register (explicit application/json)
			reg := doJSONReq(t, h, "/auth/register", `{"email":"c@example.com","password":"password123456789","display_name":"C"}`)
			if reg.Code != http.StatusCreated {
				t.Fatalf("JSON register = %d, want 201", reg.Code)
			}
			// login (no Content-Type — the lenient JSON client still decodes as JSON)
			login := do(t, h, "POST", "/auth/login", `{"email":"c@example.com","password":"password123456789"}`)
			if login.Code != http.StatusOK {
				t.Fatalf("JSON login = %d, want 200", login.Code)
			}
			if sessionCookie(login) == nil {
				t.Error("JSON login set no session cookie")
			}
		})
	}
}

// TestFormLoginPRGSetsSession proves the form arm calls the same Login service and
// gets the same security result (a session cookie) as JSON, but responds with a 303
// Post/Redirect/Get to the same-origin default.
func TestFormLoginPRGSetsSession(t *testing.T) {
	h := newHTMLTestHandler(t, stubViews{})
	if reg := doJSONReq(t, h, "/auth/register", `{"email":"f@example.com","password":"password123456789","display_name":"F"}`); reg.Code != http.StatusCreated {
		t.Fatalf("setup register = %d, want 201", reg.Code)
	}
	rec := doFormReq(t, h, "/auth/login", url.Values{"email": {"f@example.com"}, "password": {"password123456789"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("form login = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Errorf("form login Location = %q, want / (same-origin default)", loc)
	}
	if sessionCookie(rec) == nil {
		t.Error("form login set no session cookie")
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("form login redirect missing Cache-Control: no-store")
	}
}

// TestFormLoginReturnToAllowlist proves an allowlisted return_to is honored on 303
// while a non-allowlisted one falls back to the same-origin default (open-redirect
// guard shared with OAuth).
func TestFormLoginReturnToAllowlist(t *testing.T) {
	h := newHTMLTestHandler(t, stubViews{})
	doJSONReq(t, h, "/auth/register", `{"email":"rt@example.com","password":"password123456789","display_name":"R"}`)
	// The test service wires no RedirectAllowlist, so only "/" is allowed; an absolute
	// target must fall back to "/".
	rec := doFormReq(t, h, "/auth/login", url.Values{
		"email": {"rt@example.com"}, "password": {"password123456789"}, "return_to": {"https://evil.example.com"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("form login = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Errorf("open-redirect return_to honored: Location = %q, want /", loc)
	}
}

// TestFormLoginFailureReRenders proves a bad credential re-renders the login page
// (through the port) rather than redirecting, and sets no session cookie.
func TestFormLoginFailureReRenders(t *testing.T) {
	h := newHTMLTestHandler(t, stubViews{})
	doJSONReq(t, h, "/auth/register", `{"email":"bad@example.com","password":"password123456789","display_name":"B"}`)
	rec := doFormReq(t, h, "/auth/login", url.Values{"email": {"bad@example.com"}, "password": {"wrongpassword12345"}})
	if rec.Code == http.StatusSeeOther {
		t.Fatalf("form login with wrong password = 303, want a re-rendered form")
	}
	if !strings.Contains(rec.Body.String(), "PAGE:login") {
		t.Errorf("form login failure body = %q, want the login page re-rendered", rec.Body.String())
	}
	if sessionCookie(rec) != nil {
		t.Error("form login failure set a session cookie")
	}
}

// TestFormRegisterPRG proves the form register creates the account (a later form
// login succeeds) and 303s to the verification page.
func TestFormRegisterPRG(t *testing.T) {
	h := newHTMLTestHandler(t, stubViews{})
	rec := doFormReq(t, h, "/auth/register", url.Values{
		"email": {"newform@example.com"}, "password": {"password123456789"}, "display_name": {"N"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("form register = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "/auth/verify") {
		t.Errorf("form register Location = %q, want the verify page", loc)
	}
	// The account exists: a form login now succeeds.
	login := doFormReq(t, h, "/auth/login", url.Values{"email": {"newform@example.com"}, "password": {"password123456789"}})
	if login.Code != http.StatusSeeOther || sessionCookie(login) == nil {
		t.Errorf("post-register form login = %d (cookie=%v), want a 303 with a session", login.Code, sessionCookie(login) != nil)
	}
}

// TestFormForgotPRGEnumerationSafe proves the form forgot lands on the same generic
// PRG confirmation for an unknown address (no account enumeration).
func TestFormForgotPRGEnumerationSafe(t *testing.T) {
	h := newHTMLTestHandler(t, stubViews{})
	rec := doFormReq(t, h, "/auth/password/forgot", url.Values{"email": {"ghost@example.com"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("form forgot = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "/auth/password/forgot?sent=1") {
		t.Errorf("form forgot Location = %q, want the generic sent confirmation", loc)
	}
}

// TestFormPasswordlessStartPRG proves the form passwordless start 303s to the generic
// check-delivery page (same for known/unknown).
func TestFormPasswordlessStartPRG(t *testing.T) {
	h := newHTMLTestHandler(t, stubViews{})
	rec := doFormReq(t, h, "/auth/passwordless/start", url.Values{"kind": {"email"}, "identifier": {"anyone@example.com"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("form passwordless start = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "/auth/passwordless/check") {
		t.Errorf("form passwordless start Location = %q, want the check-delivery page", loc)
	}
}

// TestHTMLSecurityHeaders proves every HTML GET page carries the full security-header
// policy (design §9.1/§9.2).
func TestHTMLSecurityHeaders(t *testing.T) {
	h := newHTMLTestHandler(t, stubViews{})
	rec := do(t, h, "GET", "/auth/login", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /auth/login = %d, want 200", rec.Code)
	}
	want := map[string]string{
		"Cache-Control":          "no-store",
		"Referrer-Policy":        "no-referrer",
		"X-Frame-Options":        "DENY",
		"X-Content-Type-Options": "nosniff",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("GET /auth/login header %s = %q, want %q", k, got, v)
		}
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("GET /auth/login CSP = %q, want a restrictive policy", csp)
	}
}

// TestFormOriginRejectedCrossSite proves a cross-site browser form POST to a public
// establishment endpoint is blocked (login CSRF), while the JSON/native path (no
// Origin) is unaffected.
func TestFormOriginRejectedCrossSite(t *testing.T) {
	h := newHTMLTestHandler(t, stubViews{})
	r := httptest.NewRequest("POST", "/auth/login", strings.NewReader(url.Values{"email": {"x@example.com"}, "password": {"password123456789"}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "https://evil.example.com")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-site form login = %d, want 403", rec.Code)
	}
	if sessionCookie(rec) != nil {
		t.Error("cross-site form login minted a session")
	}
}
