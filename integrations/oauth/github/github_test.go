package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

const (
	testClientID     = "test-client-id"
	testClientSecret = "test-client-secret"
)

// newFakeGitHub stands up an httptest server, registers the caller's handlers,
// and returns a Provider wired to it via the injectable endpoints. Everything is
// hermetic — no live GitHub. The emails endpoint is served under /user/emails.
func newFakeGitHub(t *testing.T, register func(mux *http.ServeMux)) (*Provider, *httptest.Server) {
	t.Helper()

	mux := http.NewServeMux()
	if register != nil {
		register(mux)
	}

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	eps := endpoints{
		authURL:     srv.URL + "/authorize",
		tokenURL:    srv.URL + "/access_token",
		userInfoURL: srv.URL + "/user",
		emailsURL:   srv.URL + "/user/emails",
	}

	p := newProvider(testClientID, testClientSecret, nil, srv.Client(), eps)
	return p, srv
}

func TestProviderMetadata(t *testing.T) {
	p, _ := newFakeGitHub(t, nil)

	if got := p.Name(); got != "github" {
		t.Errorf("Name() = %q, want %q", got, "github")
	}
	if p.SupportsOIDC() {
		t.Error("SupportsOIDC() = true, want false")
	}
	if p.TrustEmailVerification() {
		t.Error("TrustEmailVerification() = true, want false")
	}
}

func TestNewDefaultsScopes(t *testing.T) {
	p := New(testClientID, testClientSecret, nil, nil)
	if len(p.config.Scopes) != 1 || p.config.Scopes[0] != "user:email" {
		t.Errorf("default scopes = %v, want [user:email]", p.config.Scopes)
	}
	if p.client == nil {
		t.Error("client is nil, want a default client")
	}
}

func TestGetAuthorizationURL(t *testing.T) {
	p, srv := newFakeGitHub(t, nil)

	const (
		state       = "state-123"
		verifier    = "verifier-abc"
		nonce       = "nonce-ignored"
		redirectURI = "https://app.example.com/callback"
	)

	raw := p.GetAuthorizationURL(state, verifier, nonce, redirectURI)
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}

	if base := srv.URL + "/authorize"; u.Scheme+"://"+u.Host+u.Path != base {
		t.Errorf("base = %q, want %q", u.Scheme+"://"+u.Host+u.Path, base)
	}

	q := u.Query()
	checks := map[string]string{
		"client_id":             testClientID,
		"redirect_uri":          redirectURI,
		"scope":                 "user:email",
		"state":                 state,
		"code_challenge_method": "S256",
	}
	for k, want := range checks {
		if got := q.Get(k); got != want {
			t.Errorf("query %q = %q, want %q", k, got, want)
		}
	}
	if q.Get("code_challenge") == "" {
		t.Error("code_challenge is empty")
	}
	// GitHub has no OIDC, so nonce must never be sent.
	if _, ok := q["nonce"]; ok {
		t.Error("nonce should not be present (GitHub has no OIDC)")
	}
}

func TestExchangeCode(t *testing.T) {
	var gotForm url.Values
	p, _ := newFakeGitHub(t, func(mux *http.ServeMux) {
		mux.HandleFunc("/access_token", func(w http.ResponseWriter, r *http.Request) {
			_ = r.ParseForm()
			gotForm = r.PostForm
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"access_token": "access-1",
				"refresh_token": "refresh-1",
				"expires_in": 28800,
				"token_type": "bearer",
				"scope": "user:email"
			}`))
		})
	})

	tok, err := p.ExchangeCode(context.Background(), "the-code", "the-verifier", "https://app.example.com/callback")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}

	if tok.AccessToken != "access-1" {
		t.Errorf("AccessToken = %q, want %q", tok.AccessToken, "access-1")
	}
	if tok.RefreshToken != "refresh-1" {
		t.Errorf("RefreshToken = %q, want %q", tok.RefreshToken, "refresh-1")
	}
	if tok.ExpiresIn != 28800 {
		t.Errorf("ExpiresIn = %d, want 28800", tok.ExpiresIn)
	}
	if tok.TokenType != "bearer" {
		t.Errorf("TokenType = %q, want %q", tok.TokenType, "bearer")
	}
	if tok.Scopes != "user:email" {
		t.Errorf("Scopes = %q, want %q", tok.Scopes, "user:email")
	}

	if gotForm.Get("code") != "the-code" {
		t.Errorf("code = %q, want %q", gotForm.Get("code"), "the-code")
	}
	if gotForm.Get("code_verifier") != "the-verifier" {
		t.Errorf("code_verifier = %q, want %q", gotForm.Get("code_verifier"), "the-verifier")
	}
	if gotForm.Get("client_secret") != testClientSecret {
		t.Errorf("client_secret = %q, want %q", gotForm.Get("client_secret"), testClientSecret)
	}
}

func TestExchangeCodeOAuthError(t *testing.T) {
	// GitHub returns 200 with an error object in the body for OAuth errors.
	p, _ := newFakeGitHub(t, func(mux *http.ServeMux) {
		mux.HandleFunc("/access_token", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"error": "bad_verification_code",
				"error_description": "The code passed is incorrect or expired."
			}`))
		})
	})

	if _, err := p.ExchangeCode(context.Background(), "bad", "v", "https://app.example.com/callback"); err == nil {
		t.Fatal("expected error when token response carries an OAuth error object")
	}
}

func TestExchangeCodeHTTPError(t *testing.T) {
	p, _ := newFakeGitHub(t, func(mux *http.ServeMux) {
		mux.HandleFunc("/access_token", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		})
	})

	if _, err := p.ExchangeCode(context.Background(), "c", "v", "https://app.example.com/callback"); err == nil {
		t.Fatal("expected error on non-200 token response")
	}
}

func TestRefreshToken(t *testing.T) {
	var gotForm url.Values
	p, _ := newFakeGitHub(t, func(mux *http.ServeMux) {
		mux.HandleFunc("/access_token", func(w http.ResponseWriter, r *http.Request) {
			_ = r.ParseForm()
			gotForm = r.PostForm
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"access_token": "access-2",
				"refresh_token": "refresh-2",
				"expires_in": 28800,
				"token_type": "bearer",
				"scope": "user:email"
			}`))
		})
	})

	tok, err := p.RefreshToken(context.Background(), "the-refresh-token")
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}

	if tok.AccessToken != "access-2" {
		t.Errorf("AccessToken = %q, want %q", tok.AccessToken, "access-2")
	}
	if tok.RefreshToken != "refresh-2" {
		t.Errorf("RefreshToken = %q, want %q", tok.RefreshToken, "refresh-2")
	}
	if tok.ExpiresIn != 28800 {
		t.Errorf("ExpiresIn = %d, want 28800", tok.ExpiresIn)
	}
	if gotForm.Get("grant_type") != "refresh_token" {
		t.Errorf("grant_type = %q, want %q", gotForm.Get("grant_type"), "refresh_token")
	}
	if gotForm.Get("refresh_token") != "the-refresh-token" {
		t.Errorf("refresh_token = %q, want %q", gotForm.Get("refresh_token"), "the-refresh-token")
	}
}

func TestRefreshTokenOAuthError(t *testing.T) {
	p, _ := newFakeGitHub(t, func(mux *http.ServeMux) {
		mux.HandleFunc("/access_token", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"error":"bad_refresh_token","error_description":"expired"}`))
		})
	})

	if _, err := p.RefreshToken(context.Background(), "stale"); err == nil {
		t.Fatal("expected error when refresh response carries an OAuth error object")
	}
}

func TestGetUserInfo(t *testing.T) {
	var gotProfileAuth, gotEmailsAuth string
	p, _ := newFakeGitHub(t, func(mux *http.ServeMux) {
		mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
			gotProfileAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"id": 42,
				"login": "octocat",
				"email": null,
				"name": "The Octocat",
				"avatar_url": "https://example.com/octocat.png"
			}`))
		})
		mux.HandleFunc("/user/emails", func(w http.ResponseWriter, r *http.Request) {
			gotEmailsAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[
				{"email": "secondary@example.com", "primary": false, "verified": true},
				{"email": "octocat@example.com", "primary": true, "verified": true}
			]`))
		})
	})

	info, err := p.GetUserInfo(context.Background(), "the-access-token")
	if err != nil {
		t.Fatalf("GetUserInfo: %v", err)
	}

	if info.ProviderUserID != "42" {
		t.Errorf("ProviderUserID = %q, want %q", info.ProviderUserID, "42")
	}
	if info.Email != "octocat@example.com" {
		t.Errorf("Email = %q, want %q (primary verified email)", info.Email, "octocat@example.com")
	}
	if !info.EmailVerified {
		t.Error("EmailVerified = false, want true")
	}
	if info.Name != "The Octocat" {
		t.Errorf("Name = %q, want %q", info.Name, "The Octocat")
	}
	if info.Picture != "https://example.com/octocat.png" {
		t.Errorf("Picture = %q, want %q", info.Picture, "https://example.com/octocat.png")
	}
	if gotProfileAuth != "Bearer the-access-token" {
		t.Errorf("profile Authorization = %q, want %q", gotProfileAuth, "Bearer the-access-token")
	}
	if gotEmailsAuth != "Bearer the-access-token" {
		t.Errorf("emails Authorization = %q, want %q", gotEmailsAuth, "Bearer the-access-token")
	}
}

func TestGetUserInfoFallsBackToProfileEmail(t *testing.T) {
	// No primary email in /user/emails, but the profile carries one. It is used
	// and treated as unverified.
	p, _ := newFakeGitHub(t, func(mux *http.ServeMux) {
		mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id": 7, "email": "fallback@example.com", "name": "Fallback"}`))
		})
		mux.HandleFunc("/user/emails", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"email":"other@example.com","primary":false,"verified":true}]`))
		})
	})

	info, err := p.GetUserInfo(context.Background(), "tok")
	if err != nil {
		t.Fatalf("GetUserInfo: %v", err)
	}
	if info.Email != "fallback@example.com" {
		t.Errorf("Email = %q, want %q", info.Email, "fallback@example.com")
	}
	if info.EmailVerified {
		t.Error("EmailVerified = true, want false (profile-email fallback is unverified)")
	}
}

func TestGetUserInfoNoEmail(t *testing.T) {
	p, _ := newFakeGitHub(t, func(mux *http.ServeMux) {
		mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id": 9, "email": null, "name": "No Email"}`))
		})
		mux.HandleFunc("/user/emails", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`))
		})
	})

	if _, err := p.GetUserInfo(context.Background(), "tok"); err == nil {
		t.Fatal("expected error when no email can be resolved")
	}
}

func TestGetUserInfoProfileError(t *testing.T) {
	p, _ := newFakeGitHub(t, func(mux *http.ServeMux) {
		mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		})
	})

	if _, err := p.GetUserInfo(context.Background(), "bad-token"); err == nil {
		t.Fatal("expected error on non-200 profile response")
	}
}

func TestValidateIDTokenNotSupported(t *testing.T) {
	p, _ := newFakeGitHub(t, nil)

	if _, err := p.ValidateIDToken(context.Background(), "any-token", "any-nonce"); err == nil {
		t.Fatal("expected ValidateIDToken to return an OIDC-not-supported error")
	}
}
