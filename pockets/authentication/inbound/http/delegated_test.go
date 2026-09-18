package authenticationhttp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

const (
	delegatedIssuer = "https://auth.example.com"
	mcpResource     = "https://mcp.example.com"
	apiResource     = "https://api.example.com"
)

// Seed only the future issuer's output: this slice deliberately has no OAuth
// issuance endpoint. Each connection owns a distinct session and refresh secret.
func (h *credentialHarness) delegatedToken(t *testing.T, id string) (string, string) {
	t.Helper()
	refresh := session.NewDelegatedRefreshToken()
	hash := sha256.Sum256([]byte(refresh))
	h.sess.mu.Lock()
	h.sess.m[id] = session.Session{
		ID: id, UserID: h.userID, Profile: session.ProfileDelegated,
		RefreshTokenHash: hex.EncodeToString(hash[:]),
		CreatedAt:        time.Now(), ExpiresAt: time.Now().Add(time.Hour),
		Delegation: session.Delegation{
			Issuer: delegatedIssuer, ClientID: "https://client.example.com/metadata.json",
			Resource: mcpResource, ExchangeResource: apiResource, ExchangeClientID: "mcp-server",
		},
	}
	h.sess.mu.Unlock()
	token, err := h.signer.Sign(map[string]any{
		"user_id": h.userID, "session_id": id, "session_profile": "delegated",
		"iss": delegatedIssuer, "aud": []string{mcpResource}, "client_id": "https://client.example.com/metadata.json",
	}, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	return token, refresh
}

func TestDelegatedPrincipalAdmission(t *testing.T) {
	h := newCredentialHarness(t, authlogic.DelegatedTokensConfig{Issuer: delegatedIssuer})
	token, _ := h.delegatedToken(t, "connection-a")
	key, _, _ := h.mintKey(t, "api", true, h.userID, time.Time{})
	for _, tc := range []struct {
		name           string
		opts           []PrincipalOption
		header, cookie string
		want           int
	}{
		{"default", nil, token, "", 401},
		{"first-party", []PrincipalOption{FirstParty()}, token, "", 401},
		{"first-party-with-audience", []PrincipalOption{FirstParty(), Audience(mcpResource)}, token, "", 401},
		{"mcp", []PrincipalOption{Audience(mcpResource)}, token, "", 200},
		{"api", []PrincipalOption{Audience(apiResource)}, token, "", 401},
		{"cookie", []PrincipalOption{Audience(mcpResource)}, "", token, 401},
		{"no-cookie-fallback", nil, token, h.accessJWT, 401},
		{"optional-invalid", []PrincipalOption{Optional()}, token, "", 401},
		{"optional-absent", []PrincipalOption{Optional()}, "", "", 200},
		{"web", []PrincipalOption{FirstParty()}, h.accessJWT, "", 200},
		{"audience-less-web", []PrincipalOption{Audience(mcpResource)}, h.accessJWT, "", 401},
		{"key-default", nil, key, "", 200},
		{"key-first-party", []PrincipalOption{FirstParty()}, key, "", 401},
		{"key-audience", []PrincipalOption{Audience(mcpResource)}, key, "", 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec, got := serveGuarded(h, h.svc.RequirePrincipal(tc.opts...), h.credentialRequest(tc.header, tc.cookie))
			if rec.Code != tc.want {
				t.Fatalf("status=%d, want %d: %s", rec.Code, tc.want, rec.Body)
			}
			if tc.name == "mcp" && (!got.sessionOK || got.sessionID != "connection-a" || got.cred.Profile != session.ProfileDelegated) {
				t.Fatalf("admitted without delegated session proof: %+v", got)
			}
		})
	}
}

func TestDelegatedNestedAdmissionRechecksPolicyAndRevocation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		inner  []PrincipalOption
		revoke bool
		want   int
	}{
		{"same-audience", []PrincipalOption{Audience(mcpResource)}, false, 200},
		{"different-audience", []PrincipalOption{Audience(apiResource)}, false, 401},
		{"default-denies", nil, false, 401},
		{"first-party-denies", []PrincipalOption{FirstParty()}, false, 401},
		{"revoked-between-gates", []PrincipalOption{Audience(mcpResource)}, true, 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newCredentialHarness(t, authlogic.DelegatedTokensConfig{Issuer: delegatedIssuer})
			token, _ := h.delegatedToken(t, "connection-a")
			mw := func(next http.Handler) http.Handler {
				inner := h.svc.RequirePrincipal(tc.inner...)(next)
				return h.svc.RequirePrincipal(Audience(mcpResource))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if tc.revoke {
						if err := h.sess.Delete(r.Context(), "connection-a"); err != nil {
							t.Fatal(err)
						}
					}
					inner.ServeHTTP(w, r)
				}))
			}
			rec, _ := serveGuarded(h, mw, h.credentialRequest(token, ""))
			if rec.Code != tc.want {
				t.Fatalf("status=%d, want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestBundledOverridesCannotAdmitDelegatedCredentials(t *testing.T) {
	h := newCredentialHarness(t, authlogic.DelegatedTokensConfig{Issuer: delegatedIssuer})
	token, _ := h.delegatedToken(t, "connection-a")
	resource := h.svc.RequirePrincipal(Audience(mcpResource))
	a := (RouteAuthentication{
		OAuthLinkStart: resource, SessionSecurityReads: resource, SessionHydration: resource,
		CredentialManagement: resource, MachineLifecycle: resource, UserAdministration: resource,
		Invitations: resource, BrowserAccount: resource,
	}).withDefaults(h.svc)
	for name, mw := range map[string]web.Middleware{
		"link": a.OAuthLinkStart, "security": a.SessionSecurityReads, "me": a.SessionHydration,
		"credentials": a.CredentialManagement, "keys": a.MachineLifecycle,
		"admin": a.UserAdministration, "invitations": a.Invitations, "account": a.BrowserAccount,
	} {
		t.Run(name, func(t *testing.T) {
			rec, got := serveGuarded(h, mw, h.credentialRequest(token, ""))
			if rec.Code != http.StatusUnauthorized || got.reached {
				t.Fatalf("delegated token reached bundled handler: status=%d", rec.Code)
			}
		})
	}
}

func TestDelegatedLegacyLifecyclePreservesWebSessionAndCookies(t *testing.T) {
	for _, form := range []bool{false, true} {
		for _, delegatedRefresh := range []bool{false, true} {
			h := newCredentialHarness(t, authlogic.DelegatedTokensConfig{Issuer: delegatedIssuer})
			token, refresh := h.delegatedToken(t, "connection-a")
			webPair, _, err := h.svc.Login(context.Background(), "matrix@example.com", credentialPassword)
			if err != nil {
				t.Fatal(err)
			}
			handler := handlers{svc: h.svc, views: stubViews{}}
			r := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
			access, rawRefresh := token, webPair.RefreshToken
			if delegatedRefresh {
				access, rawRefresh = webPair.AccessToken, refresh
			}
			r.Header.Set("Authorization", "Bearer "+access)
			r.AddCookie(&http.Cookie{Name: h.svc.RefreshCookieName(), Value: rawRefresh})
			r.AddCookie(&http.Cookie{Name: h.svc.SessionCookieName(), Value: webPair.AccessToken})
			rec := httptest.NewRecorder()
			if form {
				handler.logoutForm(rec, r)
			} else {
				handler.logoutJSON(rec, r)
			}
			if rec.Code != http.StatusUnauthorized || sessionCookie(rec) != nil || refreshCookie(rec) != nil {
				t.Fatalf("logout form=%v delegatedRefresh=%v: status=%d cookies=%v", form, delegatedRefresh, rec.Code, rec.Result().Cookies())
			}
			if _, err := h.svc.Refresh(context.Background(), webPair.RefreshToken); err != nil {
				t.Fatalf("web session was affected: %v", err)
			}
			if _, err := h.sess.Get(context.Background(), "connection-a"); err != nil {
				t.Fatalf("delegated session was affected: %v", err)
			}
			r = httptest.NewRequest(http.MethodPost, "/auth/refresh", strings.NewReader(`{"refresh_token":"`+refresh+`"}`))
			rec = httptest.NewRecorder()
			handler.refresh(rec, r)
			if rec.Code != http.StatusUnauthorized || len(rec.Result().Cookies()) != 0 {
				t.Fatalf("delegated refresh issued browser cookies: %d %s", rec.Code, rec.Body)
			}
		}
	}
}

func TestRevokedDelegatedRefreshCannotSelectWebLogout(t *testing.T) {
	for _, form := range []bool{false, true} {
		h := newCredentialHarness(t, authlogic.DelegatedTokensConfig{Issuer: delegatedIssuer})
		_, refresh := h.delegatedToken(t, "connection-a")
		if err := h.sess.Delete(context.Background(), "connection-a"); err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, "/auth/logout", strings.NewReader(`{"refresh_token":"`+refresh+`"}`))
		r.AddCookie(&http.Cookie{Name: h.svc.SessionCookieName(), Value: h.accessJWT})
		r.AddCookie(&http.Cookie{Name: h.svc.RefreshCookieName(), Value: refresh})
		rec := httptest.NewRecorder()
		handler := handlers{svc: h.svc, views: stubViews{}}
		if form {
			handler.logoutForm(rec, r)
		} else {
			handler.logoutJSON(rec, r)
		}
		if rec.Code != http.StatusUnauthorized || sessionCookie(rec) != nil || refreshCookie(rec) != nil {
			t.Fatalf("revoked MCP refresh changed web cookies (form=%v): %d %s", form, rec.Code, rec.Body)
		}
		rec, _ = serveGuarded(h, h.svc.RequirePrincipal(FirstParty(), Live()), h.credentialRequest("", h.accessJWT))
		if rec.Code != http.StatusOK {
			t.Fatal("revoked MCP refresh deleted web session")
		}
	}
}

func TestIndependentSessionsOverHTTP(t *testing.T) {
	h := newCredentialHarness(t, authlogic.DelegatedTokensConfig{Issuer: delegatedIssuer})
	a, _ := h.delegatedToken(t, "connection-a")
	b, _ := h.delegatedToken(t, "connection-b")
	mux := http.NewServeMux()
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.Handle("/web", h.svc.RequirePrincipal(FirstParty(), Live())(ok))
	mux.Handle("/mcp", h.svc.RequirePrincipal(Audience(mcpResource))(ok))
	server := httptest.NewServer(mux)
	defer server.Close()
	check := func(path, token string, status int) {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != status {
			t.Fatalf("%s returned %d, want %d", path, res.StatusCode, status)
		}
	}
	check("/web", h.accessJWT, 204)
	check("/mcp", a, 204)
	check("/mcp", b, 204)
	if err := h.sess.Delete(context.Background(), "connection-a"); err != nil {
		t.Fatal(err)
	}
	check("/mcp", a, 401)
	check("/mcp", b, 204)
	check("/web", h.accessJWT, 204)
	if err := h.svc.Logout(context.Background(), "", h.accessJWT); err != nil {
		t.Fatal(err)
	}
	check("/web", h.accessJWT, 401)
	check("/mcp", b, 204)
}
