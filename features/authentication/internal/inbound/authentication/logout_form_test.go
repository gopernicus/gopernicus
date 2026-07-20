package authentication

import (
	"net/http"
	"net/url"
	"testing"
)

// Form-aware logout tests (#9). They prove POST /auth/logout dispatches a form body
// to logoutForm when Views is wired — clearing both cookies and 303ing to the login
// page over the SAME idempotent Logout the JSON arm uses — stays 415 when Views is
// nil, still logs out an expired/stale credential, and remains behind the existing
// origin-only gate. The JSON contract is asserted unchanged by the existing
// sessions_test.go / origin_sibling_test.go logout tests (they pass unmodified).

// TestFormLogoutClearsCookiesAndRedirects proves the form arm calls the same Logout
// service (revoking the live session) and responds with a 303 Post/Redirect/Get to
// the login page while clearing both cookies.
func TestFormLogoutClearsCookiesAndRedirects(t *testing.T) {
	h := newHTMLTestHandler(t, stubViews{})
	doJSONReq(t, h, "/auth/register", `{"email":"lo@example.com","password":"password123456789","display_name":"L"}`)
	login := doJSONReq(t, h, "/auth/login", `{"email":"lo@example.com","password":"password123456789"}`)
	access := sessionCookie(login)
	refresh := refreshCookie(login)
	if access == nil || refresh == nil {
		t.Fatal("login did not set both cookies")
	}

	rec := doFormReq(t, h, "/auth/logout", url.Values{},
		&http.Cookie{Name: access.Name, Value: access.Value},
		&http.Cookie{Name: refresh.Name, Value: refresh.Value})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("form logout = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/auth/login" {
		t.Errorf("form logout Location = %q, want /auth/login", loc)
	}
	if c := sessionCookie(rec); c == nil || c.MaxAge >= 0 {
		t.Errorf("form logout did not clear the access cookie: %+v", c)
	}
	if c := refreshCookie(rec); c == nil || c.MaxAge >= 0 {
		t.Errorf("form logout did not clear the refresh cookie: %+v", c)
	}

	// The session was deleted: a RequireLiveSession route now rejects the same access
	// cookie even though the stateless access JWT is still within its TTL.
	stale := do(t, h, "POST", "/auth/password/change",
		`{"current_password":"password123456789","new_password":"newpassword456789"}`,
		&http.Cookie{Name: access.Name, Value: access.Value})
	if stale.Code != http.StatusUnauthorized {
		t.Errorf("live-session route after form logout = %d, want 401", stale.Code)
	}
}

// TestFormLogoutNilViews415 proves the API-only posture: a form body to /auth/logout
// with nil Views returns the shared 415 (the JSON arm still logs out).
func TestFormLogoutNilViews415(t *testing.T) {
	h := newHTMLTestHandler(t, nil)
	rec := doFormReq(t, h, "/auth/logout", url.Values{})
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("form logout with nil Views = %d, want 415", rec.Code)
	}
}

// TestFormLogoutExpiredSessionSucceeds mirrors TestLogoutSameOriginExpiredSessionSucceeds
// for the form path: a same-origin browser logout with no live session (expired/absent
// access token) still 303s to the login page and clears both cookies — logout is never
// gated on a live credential (§1.5).
func TestFormLogoutExpiredSessionSucceeds(t *testing.T) {
	h := newHTMLTestHandler(t, stubViews{})
	rec := serve(h, originReq("POST", "/auth/logout", "", "application/x-www-form-urlencoded", "", "same-origin"))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("same-origin expired-session form logout = %d, want 303; body=%s", rec.Code, rec.Body)
	}
	if loc := rec.Header().Get("Location"); loc != "/auth/login" {
		t.Errorf("form logout Location = %q, want /auth/login", loc)
	}
	if c := sessionCookie(rec); c == nil || c.MaxAge >= 0 {
		t.Errorf("form logout did not clear the access cookie: %+v", c)
	}
	if c := refreshCookie(rec); c == nil || c.MaxAge >= 0 {
		t.Errorf("form logout did not clear the refresh cookie: %+v", c)
	}
}

// TestFormLogoutRejectsUntrustedOrigin proves the existing origin-only gate still
// guards the form arm: an untrusted same-site sibling and a cross-site form POST are
// both rejected before any credential work.
func TestFormLogoutRejectsUntrustedOrigin(t *testing.T) {
	h := newHTMLTestHandler(t, stubViews{})
	for _, site := range []string{"same-site", "cross-site"} {
		rec := serve(h, originReq("POST", "/auth/logout", "", "application/x-www-form-urlencoded", "https://evil.example.com", site))
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s form logout = %d, want 403; body=%s", site, rec.Code, rec.Body)
		}
		if sessionCookie(rec) != nil {
			t.Errorf("%s form logout cleared/set a cookie despite rejection", site)
		}
	}
}
