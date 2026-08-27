package authentication

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// originReq builds a POST carrying an Origin / Sec-Fetch-Site / Content-Type surface
// plus optional cookies so the browser-origin gates (design §9.1) can be exercised end
// to end through either the JSON or the form arm.
func originReq(method, path, body, contentType, origin, secFetchSite string, cookies ...*http.Cookie) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if secFetchSite != "" {
		r.Header.Set("Sec-Fetch-Site", secFetchSite)
	}
	for _, c := range cookies {
		r.AddCookie(c)
	}
	return r
}

func serve(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

// TestCredentialEstablishmentRejectsSiblingOrigin is the IX-04 end-to-end regression on
// a requireBrowserSafeOrigin route (passwordless start): a same-site sibling origin is
// rejected (403) through BOTH the JSON arm and the form arm, a same-site request with
// no Origin is rejected, and a same-site request from the allowlisted origin passes.
func TestCredentialEstablishmentRejectsSiblingOrigin(t *testing.T) {
	h := newPasswordlessHandlerWith(t, nil, passwordlessOrigins)
	const path = "/auth/passwordless/start"

	jsonBody := `{"identifier_kind":"email","identifier":"a@example.com"}`
	formBody := "kind=email&identifier=a@example.com&method=code"

	// Sibling origin rejected on both arms.
	if rec := serve(h, originReq("POST", path, jsonBody, "application/json", "https://evil.example.com", "same-site")); rec.Code != http.StatusForbidden {
		t.Fatalf("JSON sibling-origin start = %d, want 403; body=%s", rec.Code, rec.Body)
	}
	if rec := serve(h, originReq("POST", path, formBody, "application/x-www-form-urlencoded", "https://evil.example.com", "same-site")); rec.Code != http.StatusForbidden {
		t.Fatalf("form sibling-origin start = %d, want 403; body=%s", rec.Code, rec.Body)
	}

	// Same-site with no Origin header is rejected (cannot clear the exact allowlist).
	if rec := serve(h, originReq("POST", path, jsonBody, "application/json", "", "same-site")); rec.Code != http.StatusForbidden {
		t.Fatalf("same-site no-origin start = %d, want 403; body=%s", rec.Code, rec.Body)
	}

	// Same-site from the allowlisted origin passes the gate (enumeration-safe 202).
	if rec := serve(h, originReq("POST", path, jsonBody, "application/json", "https://app.example.com", "same-site")); rec.Code != http.StatusAccepted {
		t.Fatalf("same-site allowlisted start = %d, want 202; body=%s", rec.Code, rec.Body)
	}
}

// TestMutationRejectsSiblingOrigin is the IX-04 end-to-end regression on a
// requireBrowserSafeMutation route (password change): a same-site sibling origin is
// rejected (403) on both the JSON and the form arm even with a valid double-submit
// token, while the same-site allowlisted origin clears the origin gate and the mutation
// proceeds (303 PRG on the form arm).
func TestMutationRejectsSiblingOrigin(t *testing.T) {
	f := newAccountFormFixture(t)
	f.seedLoginUser("u-sib", "sib@example.com")
	sess := f.login(t, "sib@example.com")

	changeForm := url.Values{
		"current_password": {"password123456789"}, "password": {"newpassword123456"}, "csrf_token": {"tok"},
	}.Encode()
	csrfCookie := &http.Cookie{Name: csrfCookieName, Value: "tok"}

	// Form arm, same-site sibling: rejected at the origin gate before CSRF.
	if rec := serve(f.h, originReq("POST", "/auth/password/change", changeForm, "application/x-www-form-urlencoded",
		"https://evil.example.com", "same-site", sess, csrfCookie)); rec.Code != http.StatusForbidden {
		t.Fatalf("form sibling-origin change = %d, want 403; body=%s", rec.Code, rec.Body)
	}

	// JSON arm, same-site sibling: rejected at the origin gate (cookie-authenticated,
	// not bearer-only).
	jsonBody := `{"current_password":"password123456789","new_password":"newpassword123456","csrf_token":"tok"}`
	if rec := serve(f.h, originReq("POST", "/auth/password/change", jsonBody, "application/json",
		"https://evil.example.com", "same-site", sess, csrfCookie)); rec.Code != http.StatusForbidden {
		t.Fatalf("JSON sibling-origin change = %d, want 403; body=%s", rec.Code, rec.Body)
	}

	// Form arm, same-site allowlisted origin with a valid double-submit token: clears the
	// origin gate and the mutation runs (303 PRG).
	if rec := serve(f.h, originReq("POST", "/auth/password/change", changeForm, "application/x-www-form-urlencoded",
		"https://app.example.com", "same-site", sess, csrfCookie)); rec.Code != http.StatusSeeOther {
		t.Fatalf("form same-site allowlisted change = %d, want 303; body=%s", rec.Code, rec.Body)
	}
}

// TestLogoutRejectsSiblingOrigin is the IX-16 regression: /auth/logout is cookie-driven,
// so a same-site sibling-origin form/fetch post is now rejected by the origin-only gate.
func TestLogoutRejectsSiblingOrigin(t *testing.T) {
	h := newTestHandler(t, nil)
	rec := serve(h, originReq("POST", "/auth/logout", "", "", "https://evil.example.com", "same-site"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("sibling-origin logout = %d, want 403; body=%s", rec.Code, rec.Body)
	}
}

// TestLogoutSameOriginExpiredSessionSucceeds is the IX-16 preservation check: a
// same-origin browser logout with NO live session (expired/absent access token) still
// succeeds (200) and clears both cookies — the origin gate must not gate on a live
// credential (§1.5).
func TestLogoutSameOriginExpiredSessionSucceeds(t *testing.T) {
	h := newTestHandler(t, nil)
	rec := serve(h, originReq("POST", "/auth/logout", "", "", "", "same-origin"))
	if rec.Code != http.StatusOK {
		t.Fatalf("same-origin expired-session logout = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if sessionCookie(rec) == nil || sessionCookie(rec).MaxAge >= 0 {
		t.Errorf("logout did not clear the access cookie: %+v", sessionCookie(rec))
	}
	if rc := refreshCookie(rec); rc == nil || rc.MaxAge >= 0 {
		t.Errorf("logout did not clear the refresh cookie: %+v", rc)
	}
}

// TestLogoutBearerUnaffectedByOriginGate is the IX-16 preservation check: a native /
// bearer client that sends neither Origin nor Sec-Fetch-Site is not a browser and logs
// out normally (200).
func TestLogoutBearerUnaffectedByOriginGate(t *testing.T) {
	h := newTestHandler(t, nil)
	r := originReq("POST", "/auth/logout", "", "", "", "")
	r.Header.Set("Authorization", "Bearer some-access-token")
	rec := serve(h, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("bearer logout = %d, want 200; body=%s", rec.Code, rec.Body)
	}
}
