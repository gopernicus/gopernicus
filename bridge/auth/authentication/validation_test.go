package authentication

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	coreauth "github.com/gopernicus/gopernicus/core/auth/authentication"
	"github.com/gopernicus/gopernicus/infrastructure/oauth"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// newValidationTestBridge constructs a Bridge with the given allowed frontends
// for testing origin/URL validation. The authenticator is minimal — only
// enough to not panic on handler entry.
func newValidationTestBridge(t *testing.T, allowedFrontends []string) *Bridge {
	t.Helper()

	oauthRepo := newStubOAuthRepo()

	auth := coreauth.NewAuthenticator(
		"test",
		coreauth.Repositories{},
		nil, // hasher
		nil, // signer
		nil, // bus
		coreauth.Config{},
		coreauth.WithOAuth(map[string]oauth.Provider{
			"google": nil, // provider value unused for these tests
		}, oauthRepo),
	)

	cfg := Config{
		AllowedFrontends: allowedFrontends,
	}

	return New(nil, cfg, auth, nil)
}

func jsonBody(t *testing.T, v any) *bytes.Buffer {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	return bytes.NewBuffer(b)
}

// ---------------------------------------------------------------------------
// httpInitiatePasswordReset validation
// ---------------------------------------------------------------------------

func TestHTTPInitiatePasswordReset_FallsBackOnInvalidResetURL(t *testing.T) {
	b := newValidationTestBridge(t, []string{"https://app.example.com"})

	body := jsonBody(t, InitiatePasswordResetRequest{
		Email:    "user@example.com",
		ResetURL: "https://evil.com/reset",
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/auth/password-reset/initiate", body)
	r.Header.Set("Content-Type", "application/json")

	// Off-list URLs no longer fail validation — the bridge silently substitutes
	// the first allowed frontend. The stub authenticator panics once we reach
	// core logic, which is the signal that validation passed.
	defer func() {
		_ = recover()
		if w.Code == http.StatusBadRequest && bytes.Contains(w.Body.Bytes(), []byte("reset_url")) {
			t.Errorf("off-list reset_url should fall back, not 400; got: %s", w.Body.String())
		}
	}()

	b.httpInitiatePasswordReset(w, r)
}

func TestHTTPInitiatePasswordReset_FallsBackWhenMissing(t *testing.T) {
	b := newValidationTestBridge(t, []string{"https://app.example.com"})

	body := jsonBody(t, InitiatePasswordResetRequest{
		Email:    "user@example.com",
		ResetURL: "", // missing — bridge fills in the default
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/auth/password-reset/initiate", body)
	r.Header.Set("Content-Type", "application/json")

	defer func() {
		_ = recover()
		if w.Code == http.StatusBadRequest && bytes.Contains(w.Body.Bytes(), []byte("reset_url")) {
			t.Errorf("missing reset_url should fall back, not 400; got: %s", w.Body.String())
		}
	}()

	b.httpInitiatePasswordReset(w, r)
}

func TestResolveResetURL(t *testing.T) {
	b := newValidationTestBridge(t, []string{"https://app.example.com", "https://other.example.com"})

	cases := []struct {
		name     string
		provided string
		want     string
	}{
		{"valid client URL kept as-is", "https://app.example.com/somewhere", "https://app.example.com/somewhere"},
		{"second allowed origin also kept", "https://other.example.com/x", "https://other.example.com/x"},
		{"off-list URL falls back to default", "https://evil.com/reset", "https://app.example.com/reset-password"},
		{"empty falls back to default", "", "https://app.example.com/reset-password"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := b.resolveResetURL(tc.provided)
			if got != tc.want {
				t.Errorf("resolveResetURL(%q) = %q, want %q", tc.provided, got, tc.want)
			}
		})
	}
}

func TestResolveResetURL_LegacyMode(t *testing.T) {
	b := newValidationTestBridge(t, nil) // no allow-list

	// Legacy mode: pass through whatever was sent, including empty (which
	// triggers the email subscriber's raw-token fallback).
	if got := b.resolveResetURL("https://anything.example.com/x"); got != "https://anything.example.com/x" {
		t.Errorf("legacy mode should pass through provided URL; got %q", got)
	}
	if got := b.resolveResetURL(""); got != "" {
		t.Errorf("legacy mode + empty should stay empty; got %q", got)
	}
}

func TestHTTPInitiatePasswordReset_AcceptsValidResetURL(t *testing.T) {
	b := newValidationTestBridge(t, []string{"https://app.example.com"})

	body := jsonBody(t, InitiatePasswordResetRequest{
		Email:    "user@example.com",
		ResetURL: "https://app.example.com/reset-password",
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/auth/password-reset/initiate", body)
	r.Header.Set("Content-Type", "application/json")

	// The authenticator isn't fully wired, so the handler will panic after
	// validation passes. We recover and check we got past the validation step.
	defer func() {
		if err := recover(); err != nil {
			// Panic means validation passed and we reached core logic.
			// Check that the response (if any) wasn't a validation error.
			if w.Code == http.StatusBadRequest && bytes.Contains(w.Body.Bytes(), []byte("reset_url")) {
				t.Errorf("should accept valid reset_url; got validation error before panic: %s", w.Body.String())
			}
			// Panic after validation is expected — test passes.
			return
		}
		// No panic means handler completed normally (shouldn't happen with stub auth).
		if w.Code == http.StatusBadRequest && bytes.Contains(w.Body.Bytes(), []byte("reset_url")) {
			t.Errorf("should accept valid reset_url; got validation error: %s", w.Body.String())
		}
	}()

	b.httpInitiatePasswordReset(w, r)
}

func TestHTTPInitiatePasswordReset_AllowsAnyURLInLegacyMode(t *testing.T) {
	b := newValidationTestBridge(t, nil) // no allowed frontends = legacy mode

	body := jsonBody(t, InitiatePasswordResetRequest{
		Email:    "user@example.com",
		ResetURL: "https://any-url.com/reset",
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/auth/password-reset/initiate", body)
	r.Header.Set("Content-Type", "application/json")

	// The authenticator isn't fully wired, so we expect a panic after validation.
	defer func() {
		if err := recover(); err != nil {
			// Panic means validation passed (no error about reset_url).
			if w.Code == http.StatusBadRequest && bytes.Contains(w.Body.Bytes(), []byte("reset_url")) {
				t.Errorf("should allow any URL in legacy mode; got: %s", w.Body.String())
			}
			return
		}
		// No panic — check we didn't get a validation error.
		if w.Code == http.StatusBadRequest && bytes.Contains(w.Body.Bytes(), []byte("reset_url")) {
			t.Errorf("should allow any URL in legacy mode; got: %s", w.Body.String())
		}
	}()

	b.httpInitiatePasswordReset(w, r)
}

// ---------------------------------------------------------------------------
// httpOAuthInitiate validation
// ---------------------------------------------------------------------------

func TestHTTPOAuthInitiate_RejectsInvalidAppOrigin(t *testing.T) {
	b := newValidationTestBridge(t, []string{"https://app.example.com"})

	body := jsonBody(t, InitiateOAuthRequest{
		Provider:    "google",
		RedirectURI: "https://api.example.com/callback",
		AppOrigin:   "https://evil.com",
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/auth/oauth/initiate", body)
	r.Header.Set("Content-Type", "application/json")

	b.httpOAuthInitiate(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}

	if !bytes.Contains(w.Body.Bytes(), []byte("app_origin")) {
		t.Errorf("response should mention app_origin; got: %s", w.Body.String())
	}
}

func TestHTTPOAuthInitiate_RequiresAppOriginWhenStrictMode(t *testing.T) {
	b := newValidationTestBridge(t, []string{"https://app.example.com"})

	body := jsonBody(t, InitiateOAuthRequest{
		Provider:    "google",
		RedirectURI: "https://api.example.com/callback",
		AppOrigin:   "", // missing
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/auth/oauth/initiate", body)
	r.Header.Set("Content-Type", "application/json")

	b.httpOAuthInitiate(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}

	if !bytes.Contains(w.Body.Bytes(), []byte("app_origin")) {
		t.Errorf("response should mention app_origin is required; got: %s", w.Body.String())
	}
}

func TestHTTPOAuthInitiate_AllowsAnyOriginInLegacyMode(t *testing.T) {
	b := newValidationTestBridge(t, nil) // no allowed frontends = legacy mode

	body := jsonBody(t, InitiateOAuthRequest{
		Provider:    "google",
		RedirectURI: "https://api.example.com/callback",
		AppOrigin:   "https://any-origin.com",
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/auth/oauth/initiate", body)
	r.Header.Set("Content-Type", "application/json")

	// The authenticator isn't fully wired, so we expect a panic after validation.
	defer func() {
		if err := recover(); err != nil {
			// Panic means validation passed (no error about app_origin).
			if w.Code == http.StatusBadRequest && bytes.Contains(w.Body.Bytes(), []byte("app_origin")) {
				t.Errorf("should allow any origin in legacy mode; got: %s", w.Body.String())
			}
			return
		}
		// No panic — check we didn't get a validation error about app_origin.
		if w.Code == http.StatusBadRequest && bytes.Contains(w.Body.Bytes(), []byte("app_origin")) {
			t.Errorf("should allow any origin in legacy mode; got: %s", w.Body.String())
		}
	}()

	b.httpOAuthInitiate(w, r)
}

// ---------------------------------------------------------------------------
// httpOAuthStart validation (GET endpoint)
// ---------------------------------------------------------------------------

func TestHTTPOAuthStart_RejectsInvalidAppOrigin(t *testing.T) {
	b := newValidationTestBridge(t, []string{"https://app.example.com"})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/auth/oauth/start/google?app_origin=https://evil.com", nil)
	r.SetPathValue("provider", "google")

	b.httpOAuthStart(w, r)

	// httpOAuthStart is a GET endpoint the browser navigates to directly, so
	// validation errors redirect to the first allowed frontend with an error
	// code instead of returning a JSON 400.
	if w.Code != http.StatusTemporaryRedirect {
		t.Errorf("status = %d, want 307; body: %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "https://app.example.com:443/login?error=invalid_app_origin") {
		t.Errorf("Location = %q, want it to contain the allowed frontend + error=invalid_app_origin", loc)
	}
}

func TestHTTPOAuthStart_RequiresAppOriginWhenStrictMode(t *testing.T) {
	b := newValidationTestBridge(t, []string{"https://app.example.com"})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/auth/oauth/start/google", nil) // no app_origin
	r.SetPathValue("provider", "google")

	b.httpOAuthStart(w, r)

	if w.Code != http.StatusTemporaryRedirect {
		t.Errorf("status = %d, want 307; body: %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "https://app.example.com:443/login?error=app_origin_required") {
		t.Errorf("Location = %q, want it to contain the allowed frontend + error=app_origin_required", loc)
	}
}
