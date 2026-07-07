package google

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/coreos/go-oidc/v3/oidc/oidctest"
)

const (
	testClientID     = "test-client-id"
	testClientSecret = "test-client-secret"
	testKeyID        = "test-key-id"
)

// fakeGoogle stands up an httptest server that serves OIDC discovery + JWKS via
// go-oidc's oidctest helper, plus caller-supplied token/userinfo/auth handlers,
// and returns a Provider wired to it. Everything is hermetic — no live Google.
type fakeGoogle struct {
	server *httptest.Server
	priv   *rsa.PrivateKey
}

func newFakeGoogle(t *testing.T, register func(mux *http.ServeMux)) (*Provider, *fakeGoogle) {
	t.Helper()

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	oidcSrv := &oidctest.Server{
		PublicKeys: []oidctest.PublicKey{{
			PublicKey: priv.Public(),
			KeyID:     testKeyID,
			Algorithm: oidc.RS256,
		}},
	}

	mux := http.NewServeMux()
	mux.Handle("/.well-known/openid-configuration", oidcSrv)
	mux.Handle("/keys", oidcSrv)
	if register != nil {
		register(mux)
	}

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	oidcSrv.SetIssuer(srv.URL)

	eps := endpoints{
		issuer:      srv.URL,
		authURL:     srv.URL + "/auth",
		tokenURL:    srv.URL + "/token",
		userInfoURL: srv.URL + "/userinfo",
		jwksURL:     srv.URL + "/keys",
	}

	p, err := newProvider(context.Background(), testClientID, testClientSecret, nil, srv.Client(), eps)
	if err != nil {
		t.Fatalf("newProvider: %v", err)
	}

	return p, &fakeGoogle{server: srv, priv: priv}
}

// signIDToken signs an ID token with the fake's key for the given claims.
func (f *fakeGoogle) signIDToken(t *testing.T, claims string) string {
	t.Helper()
	return oidctest.SignIDToken(f.priv, testKeyID, oidc.RS256, claims)
}

func TestProviderMetadata(t *testing.T) {
	p, _ := newFakeGoogle(t, nil)

	if got := p.Name(); got != "google" {
		t.Errorf("Name() = %q, want %q", got, "google")
	}
	if !p.SupportsOIDC() {
		t.Error("SupportsOIDC() = false, want true")
	}
	if !p.TrustEmailVerification() {
		t.Error("TrustEmailVerification() = false, want true")
	}
}

func TestGetAuthorizationURL(t *testing.T) {
	p, fake := newFakeGoogle(t, nil)

	const (
		state       = "state-123"
		verifier    = "verifier-abc"
		nonce       = "nonce-xyz"
		redirectURI = "https://app.example.com/callback"
	)

	raw := p.GetAuthorizationURL(state, verifier, nonce, redirectURI)
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}

	if base := fake.server.URL + "/auth"; u.Scheme+"://"+u.Host+u.Path != base {
		t.Errorf("base = %q, want %q", u.Scheme+"://"+u.Host+u.Path, base)
	}

	q := u.Query()
	checks := map[string]string{
		"client_id":             testClientID,
		"redirect_uri":          redirectURI,
		"response_type":         "code",
		"scope":                 "openid email profile",
		"state":                 state,
		"code_challenge_method": "S256",
		"access_type":           "offline",
		"prompt":                "consent",
		"nonce":                 nonce,
	}
	for k, want := range checks {
		if got := q.Get(k); got != want {
			t.Errorf("query %q = %q, want %q", k, got, want)
		}
	}
	if q.Get("code_challenge") == "" {
		t.Error("code_challenge is empty")
	}
}

func TestGetAuthorizationURLOmitsEmptyNonce(t *testing.T) {
	p, _ := newFakeGoogle(t, nil)

	raw := p.GetAuthorizationURL("s", "v", "", "https://app.example.com/callback")
	u, _ := url.Parse(raw)
	if _, ok := u.Query()["nonce"]; ok {
		t.Error("nonce should be omitted when empty")
	}
}

func TestExchangeCode(t *testing.T) {
	var gotForm url.Values
	p, _ := newFakeGoogle(t, func(mux *http.ServeMux) {
		mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
			_ = r.ParseForm()
			gotForm = r.PostForm
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"access_token": "access-1",
				"refresh_token": "refresh-1",
				"expires_in": 3600,
				"id_token": "id-1",
				"token_type": "Bearer",
				"scope": "openid email profile"
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
	if tok.ExpiresIn != 3600 {
		t.Errorf("ExpiresIn = %d, want 3600", tok.ExpiresIn)
	}
	if tok.IDToken != "id-1" {
		t.Errorf("IDToken = %q, want %q", tok.IDToken, "id-1")
	}
	if tok.TokenType != "Bearer" {
		t.Errorf("TokenType = %q, want %q", tok.TokenType, "Bearer")
	}
	if tok.Scopes != "openid email profile" {
		t.Errorf("Scopes = %q, want %q", tok.Scopes, "openid email profile")
	}

	if gotForm.Get("grant_type") != "authorization_code" {
		t.Errorf("grant_type = %q, want %q", gotForm.Get("grant_type"), "authorization_code")
	}
	if gotForm.Get("code") != "the-code" {
		t.Errorf("code = %q, want %q", gotForm.Get("code"), "the-code")
	}
	if gotForm.Get("code_verifier") != "the-verifier" {
		t.Errorf("code_verifier = %q, want %q", gotForm.Get("code_verifier"), "the-verifier")
	}
}

func TestExchangeCodeError(t *testing.T) {
	p, _ := newFakeGoogle(t, func(mux *http.ServeMux) {
		mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
		})
	})

	if _, err := p.ExchangeCode(context.Background(), "bad", "v", "https://app.example.com/callback"); err == nil {
		t.Fatal("expected error on non-200 token response")
	}
}

func TestRefreshToken(t *testing.T) {
	var gotForm url.Values
	p, _ := newFakeGoogle(t, func(mux *http.ServeMux) {
		mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
			_ = r.ParseForm()
			gotForm = r.PostForm
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"access_token": "access-2",
				"expires_in": 1800,
				"token_type": "Bearer",
				"scope": "openid email"
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
	if tok.ExpiresIn != 1800 {
		t.Errorf("ExpiresIn = %d, want 1800", tok.ExpiresIn)
	}
	if gotForm.Get("grant_type") != "refresh_token" {
		t.Errorf("grant_type = %q, want %q", gotForm.Get("grant_type"), "refresh_token")
	}
	if gotForm.Get("refresh_token") != "the-refresh-token" {
		t.Errorf("refresh_token = %q, want %q", gotForm.Get("refresh_token"), "the-refresh-token")
	}
}

func TestGetUserInfo(t *testing.T) {
	var gotAuth string
	p, _ := newFakeGoogle(t, func(mux *http.ServeMux) {
		mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"sub": "google-user-1",
				"email": "user@example.com",
				"email_verified": true,
				"name": "Test User",
				"picture": "https://example.com/pic.png"
			}`))
		})
	})

	info, err := p.GetUserInfo(context.Background(), "the-access-token")
	if err != nil {
		t.Fatalf("GetUserInfo: %v", err)
	}

	if info.ProviderUserID != "google-user-1" {
		t.Errorf("ProviderUserID = %q, want %q", info.ProviderUserID, "google-user-1")
	}
	if info.Email != "user@example.com" {
		t.Errorf("Email = %q, want %q", info.Email, "user@example.com")
	}
	if !info.EmailVerified {
		t.Error("EmailVerified = false, want true")
	}
	if info.Name != "Test User" {
		t.Errorf("Name = %q, want %q", info.Name, "Test User")
	}
	if info.Picture != "https://example.com/pic.png" {
		t.Errorf("Picture = %q, want %q", info.Picture, "https://example.com/pic.png")
	}
	if gotAuth != "Bearer the-access-token" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer the-access-token")
	}
}

func TestValidateIDToken(t *testing.T) {
	p, fake := newFakeGoogle(t, nil)

	claims := `{
		"iss": "` + fake.server.URL + `",
		"aud": "` + testClientID + `",
		"sub": "google-user-1",
		"exp": ` + strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10) + `,
		"email": "user@example.com",
		"email_verified": true,
		"name": "Test User",
		"picture": "https://example.com/pic.png",
		"nonce": "the-nonce"
	}`
	token := fake.signIDToken(t, claims)

	got, err := p.ValidateIDToken(context.Background(), token, "the-nonce")
	if err != nil {
		t.Fatalf("ValidateIDToken: %v", err)
	}

	if got.Subject != "google-user-1" {
		t.Errorf("Subject = %q, want %q", got.Subject, "google-user-1")
	}
	if got.Email != "user@example.com" {
		t.Errorf("Email = %q, want %q", got.Email, "user@example.com")
	}
	if !got.EmailVerified {
		t.Error("EmailVerified = false, want true")
	}
	if got.Name != "Test User" {
		t.Errorf("Name = %q, want %q", got.Name, "Test User")
	}
	if got.Nonce != "the-nonce" {
		t.Errorf("Nonce = %q, want %q", got.Nonce, "the-nonce")
	}
}

func TestValidateIDTokenNonceMismatch(t *testing.T) {
	p, fake := newFakeGoogle(t, nil)

	claims := `{
		"iss": "` + fake.server.URL + `",
		"aud": "` + testClientID + `",
		"sub": "u1",
		"exp": ` + strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10) + `,
		"email": "user@example.com",
		"nonce": "actual-nonce"
	}`
	token := fake.signIDToken(t, claims)

	if _, err := p.ValidateIDToken(context.Background(), token, "expected-nonce"); err == nil {
		t.Fatal("expected nonce mismatch error")
	}
}

func TestValidateIDTokenMissingEmail(t *testing.T) {
	p, fake := newFakeGoogle(t, nil)

	claims := `{
		"iss": "` + fake.server.URL + `",
		"aud": "` + testClientID + `",
		"sub": "u1",
		"exp": ` + strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10) + `
	}`
	token := fake.signIDToken(t, claims)

	if _, err := p.ValidateIDToken(context.Background(), token, ""); err == nil {
		t.Fatal("expected missing-email error")
	}
}

func TestValidateIDTokenWrongAudience(t *testing.T) {
	p, fake := newFakeGoogle(t, nil)

	claims := `{
		"iss": "` + fake.server.URL + `",
		"aud": "some-other-client",
		"sub": "u1",
		"exp": ` + strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10) + `,
		"email": "user@example.com"
	}`
	token := fake.signIDToken(t, claims)

	if _, err := p.ValidateIDToken(context.Background(), token, ""); err == nil {
		t.Fatal("expected audience mismatch verification error")
	}
}

func TestNewDiscoveryFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	eps := googleEndpoints()
	eps.issuer = srv.URL

	if _, err := newProvider(context.Background(), testClientID, testClientSecret, nil, srv.Client(), eps); err == nil {
		t.Fatal("expected fail-fast discovery error")
	}
}

func TestNewFailsFastOnCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := New(ctx, testClientID, testClientSecret, nil, http.DefaultClient); err == nil {
		t.Fatal("expected discovery to fail fast on a cancelled context")
	}
}
