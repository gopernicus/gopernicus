// Package devissuer is this host's development identity provider: a local
// OIDC issuer (gooidc's oidctest) that serves its JWKS and mints signed tokens
// on request, so the external identity path runs end to end with no provider
// account. It knows no pocket and no domain; cmd wires its issuer strings into
// a verifier and mounts its handlers.
//
// It exists only in development: New refuses any other runtime mode, so a
// production boot can never mount a token-minting endpoint.
package devissuer

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/integrations/cryptids/go-oidc/oidctest"
	"github.com/gopernicus/gopernicus/sdk"
	sdkauth "github.com/gopernicus/gopernicus/sdk/capabilities/authentication"
	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

const (
	// IssuerPath is where the issuer lives under the host origin; tokens carry
	// iss = origin + IssuerPath.
	IssuerPath = "/dev/issuer"
	// SessionIssuerPath is the second issuer string the session cookie carries.
	// One subject reaches the same local user through both, because the host
	// wires both under one provider name.
	SessionIssuerPath = IssuerPath + "/session"
	// KeysPath serves the JWKS both issuers sign with.
	KeysPath = IssuerPath + oidctest.JWKSPath
	// TokenPath mints a header token: POST {"sub", "email", ...} → {"token"}.
	TokenPath = "/dev/token"
	// SessionPath mints a session-issuer token and sets it as CookieName.
	SessionPath = "/dev/session"
	// CookieName is the provider session cookie the host reads.
	CookieName = "__session"
	// Audience is the aud every minted token carries.
	Audience = "gopernicus-dev"

	maxRequestBytes = 16 << 10
)

// ErrNotDevelopment refuses construction outside development mode.
var ErrNotDevelopment = fmt.Errorf("devissuer: the development issuer is refused outside development mode: %w", sdk.ErrInvalidInput)

// Issuer mints development tokens under two issuer strings with one key set.
type Issuer struct {
	keys          *oidctest.Issuer
	sessionIssuer string
	secureCookie  bool
	now           func() time.Time
}

// Provider is the external identity provider for tokens this issuer mints: the
// generic OIDC provider, declared development-only, so a production
// authentication pocket refuses it even when the issuer was built in development.
type Provider struct {
	*sdkauth.OIDC
}

// TokenRequest is the body TokenPath and SessionPath accept. Iss, when set,
// overrides the issuer so a caller can mint a token from an unwired issuer.
type TokenRequest struct {
	Subject       string   `json:"sub"`
	Email         string   `json:"email,omitempty"`
	EmailVerified *bool    `json:"email_verified,omitempty"`
	Name          string   `json:"name,omitempty"`
	AMR           []string `json:"amr,omitempty"`
	Issuer        string   `json:"iss,omitempty"`
}

// New builds the issuer at origin + IssuerPath. origin is the host's
// scheme-qualified, path-free origin. Any mode but development is refused.
func New(origin string, mode environment.Mode) (*Issuer, error) {
	if mode != environment.ModeDevelopment {
		return nil, ErrNotDevelopment
	}
	origin = strings.TrimRight(origin, "/")
	if !strings.HasPrefix(origin, "http://") && !strings.HasPrefix(origin, "https://") {
		return nil, fmt.Errorf("devissuer: origin %q must be scheme-qualified: %w", origin, sdk.ErrInvalidInput)
	}
	keys, err := oidctest.New(origin+IssuerPath, oidctest.WithAudience(Audience))
	if err != nil {
		return nil, err
	}
	return &Issuer{
		keys:          keys,
		sessionIssuer: origin + SessionIssuerPath,
		secureCookie:  strings.HasPrefix(origin, "https://"),
		now:           time.Now,
	}, nil
}

// NewProvider verifies tokens minted under issuer — Issuer or SessionIssuer —
// with verifier.
func NewProvider(issuer string, verifier cryptids.JWTVerifier, profile sdkauth.Profile) Provider {
	return Provider{OIDC: sdkauth.NewOIDC(issuer, verifier, profile)}
}

// Capabilities declares the provider development-only.
func (Provider) Capabilities() sdkauth.Capabilities {
	return sdkauth.Capabilities{DevelopmentOnly: true}
}

// Issuer is the iss of header tokens.
func (i *Issuer) Issuer() string { return i.keys.Issuer() }

// SessionIssuer is the iss of session-cookie tokens.
func (i *Issuer) SessionIssuer() string { return i.sessionIssuer }

// JWKSURL is the absolute key set URL both issuers verify against.
func (i *Issuer) JWKSURL() string { return i.keys.JWKSURL() }

// KeysHandler serves the JWKS at KeysPath.
func (i *Issuer) KeysHandler() http.Handler {
	return http.StripPrefix(IssuerPath, i.keys.Handler())
}

// TokenHandler mints a header token and answers {"token": "..."}.
func (i *Issuer) TokenHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := i.mint(w, r, i.Issuer())
		if !ok {
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"token": token})
	}
}

// SessionHandler mints a session-issuer token and sets it as CookieName
// (HttpOnly, SameSite=Lax; Secure on an https origin).
func (i *Issuer) SessionHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := i.mint(w, r, i.sessionIssuer)
		if !ok {
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     CookieName,
			Value:    token,
			Path:     "/",
			MaxAge:   int(oidctest.DefaultTTL / time.Second),
			HttpOnly: true,
			Secure:   i.secureCookie,
			SameSite: http.SameSiteLaxMode,
		})
		writeJSON(w, http.StatusOK, map[string]string{"cookie": CookieName})
	}
}

func (i *Issuer) mint(w http.ResponseWriter, r *http.Request, defaultIssuer string) (string, bool) {
	var req TokenRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || strings.TrimSpace(req.Subject) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": `body must be JSON with a non-empty "sub"`})
		return "", false
	}
	claims := map[string]any{"sub": req.Subject, "auth_time": i.now().Unix()}
	if req.Email != "" {
		claims["email"] = req.Email
	}
	if req.EmailVerified != nil {
		claims["email_verified"] = *req.EmailVerified
	}
	if req.Name != "" {
		claims["name"] = req.Name
	}
	if len(req.AMR) > 0 {
		claims["amr"] = req.AMR
	}
	issuer := defaultIssuer
	if req.Issuer != "" {
		issuer = req.Issuer
	}
	token, err := i.keys.MintAs(issuer, claims)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "mint failed"})
		return "", false
	}
	return token, true
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
