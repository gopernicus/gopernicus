package authenticationhttp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
)

// The API sees the exchanged token: its actor is the confidential MCP client,
// its origin is the consenting public client, and its session is still the grant.
func (h *credentialHarness) delegatedAPIToken(t *testing.T, id string) string {
	t.Helper()
	h.delegatedToken(t, id)
	token, err := h.signer.Sign(map[string]any{
		"user_id": h.userID, "session_id": id, "session_profile": "delegated",
		"iss": delegatedIssuer, "aud": []string{apiResource},
		"client_id": "mcp-server", "origin_client_id": "https://client.example.com/metadata.json",
		"act": map[string]any{"sub": "mcp-server"},
	}, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestDelegatedAudienceMixedAdmission(t *testing.T) {
	h := newCredentialHarness(t, authlogic.DelegatedTokensConfig{Issuer: delegatedIssuer})
	api := h.delegatedAPIToken(t, "api-connection")
	mcp, _ := h.delegatedToken(t, "mcp-connection")
	key, _, _ := h.mintKey(t, "mixed-route", true, h.userID, time.Time{})
	for _, tc := range []struct {
		name           string
		opts           []PrincipalOption
		header, cookie string
		want           int
	}{
		{"default-still-denies", nil, api, "", 401},
		{"exchanged-api", []PrincipalOption{DelegatedAudience(apiResource)}, api, "", 200},
		{"direct-mcp", []PrincipalOption{DelegatedAudience(mcpResource)}, mcp, "", 200},
		{"multiple-resources", []PrincipalOption{DelegatedAudience(mcpResource, apiResource)}, api, "", 200},
		{"wrong-resource", []PrincipalOption{DelegatedAudience(apiResource)}, mcp, "", 401},
		{"exact-resource", []PrincipalOption{DelegatedAudience(apiResource + "/")}, api, "", 401},
		{"web-header", []PrincipalOption{DelegatedAudience(apiResource)}, h.accessJWT, "", 200},
		{"web-cookie", []PrincipalOption{DelegatedAudience(apiResource)}, "", h.accessJWT, 200},
		{"api-key", []PrincipalOption{DelegatedAudience(apiResource)}, key, "", 200},
		{"delegated-cookie", []PrincipalOption{DelegatedAudience(apiResource)}, "", api, 401},
		{"key-only-denies-delegated", []PrincipalOption{DelegatedAudience(apiResource), Accept(CredentialAPIKey)}, api, "", 401},
		{"key-only-preserves-key", []PrincipalOption{DelegatedAudience(apiResource), Accept(CredentialAPIKey)}, key, "", 200},
		{"key-only-denies-web", []PrincipalOption{DelegatedAudience(apiResource), Accept(CredentialAPIKey)}, h.accessJWT, "", 401},
		{"access-only-admits-delegated", []PrincipalOption{Accept(CredentialAccessToken), DelegatedAudience(apiResource)}, api, "", 200},
		{"access-only-denies-key", []PrincipalOption{Accept(CredentialAccessToken), DelegatedAudience(apiResource)}, key, "", 401},
		{"header-only-admits-delegated", []PrincipalOption{DelegatedAudience(apiResource), Transports(TransportHeader)}, api, "", 200},
		{"header-only-ignores-cookie", []PrincipalOption{DelegatedAudience(apiResource), Transports(TransportHeader)}, "", h.accessJWT, 401},
		{"cookie-only-ignores-header", []PrincipalOption{DelegatedAudience(apiResource), Transports(TransportCookie)}, api, h.accessJWT, 200},
		{"cookie-only-denies-delegated", []PrincipalOption{DelegatedAudience(apiResource), Transports(TransportCookie)}, "", api, 401},
		{"first-party-denies-delegated", []PrincipalOption{DelegatedAudience(apiResource), FirstParty()}, api, "", 401},
		{"first-party-denies-delegated-reversed", []PrincipalOption{FirstParty(), DelegatedAudience(apiResource)}, api, "", 401},
		{"first-party-preserves-web", []PrincipalOption{FirstParty(), DelegatedAudience(apiResource)}, "", h.accessJWT, 200},
		{"first-party-denies-key", []PrincipalOption{FirstParty(), DelegatedAudience(apiResource)}, key, "", 401},
		{"optional-absent", []PrincipalOption{DelegatedAudience(apiResource), Optional()}, "", "", 200},
		{"optional-delegated", []PrincipalOption{DelegatedAudience(apiResource), Optional()}, api, "", 200},
		{"optional-wrong-resource", []PrincipalOption{DelegatedAudience(apiResource), Optional()}, mcp, "", 401},
		{"no-wrong-resource-cookie-fallback", []PrincipalOption{DelegatedAudience(apiResource)}, mcp, h.accessJWT, 401},
		{"no-bad-signature-cookie-fallback", []PrincipalOption{DelegatedAudience(apiResource), Optional()}, h.badSignatureAccessJWT(t), h.accessJWT, 401},
		{"no-expired-cookie-fallback", []PrincipalOption{DelegatedAudience(apiResource), Optional()}, h.expiredAccessJWT(t), h.accessJWT, 401},
		{"no-invalid-key-cookie-fallback", []PrincipalOption{DelegatedAudience(apiResource), Optional()}, "invalid-key", h.accessJWT, 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec, got := serveGuarded(h, h.svc.RequirePrincipal(tc.opts...), h.credentialRequest(tc.header, tc.cookie))
			if rec.Code != tc.want || got.reached != (tc.want == http.StatusOK) {
				t.Fatalf("status=%d reached=%v, want %d", rec.Code, got.reached, tc.want)
			}
			if tc.name == "exchanged-api" {
				if got.principal.Type != "user" || got.principal.ID != h.userID || !got.sessionOK || got.sessionID != "api-connection" ||
					got.cred.Profile != session.ProfileDelegated || got.cred.Issuer != delegatedIssuer || !slices.Equal(got.cred.Audiences, []string{apiResource}) ||
					got.cred.ClientID != "mcp-server" || got.cred.ActorID != "mcp-server" || got.cred.OriginClientID != "https://client.example.com/metadata.json" {
					t.Fatalf("exchanged identity or live grant proof lost: %+v", got)
				}
			}
			if tc.name == "optional-absent" && (got.principal.ID != "" || got.cred.Kind != "" || got.sessionOK) {
				t.Fatalf("anonymous request acquired a credential: %+v", got)
			}
		})
	}
}

func TestDelegatedAudienceStrictIntersection(t *testing.T) {
	h := newCredentialHarness(t, authlogic.DelegatedTokensConfig{Issuer: delegatedIssuer})
	api := h.delegatedAPIToken(t, "connection-a")
	key, _, _ := h.mintKey(t, "strict-route", false, "", time.Time{})
	for _, tc := range []struct {
		name string
		opts []PrincipalOption
		want int
	}{
		{"strict-alone", []PrincipalOption{Audience(apiResource)}, 200},
		{"matching", []PrincipalOption{Audience(apiResource), DelegatedAudience(apiResource)}, 200},
		{"matching-reversed", []PrincipalOption{DelegatedAudience(apiResource), Audience(apiResource)}, 200},
		{"strict-narrows", []PrincipalOption{DelegatedAudience(mcpResource, apiResource), Audience(mcpResource)}, 401},
		{"strict-narrows-reversed", []PrincipalOption{Audience(mcpResource), DelegatedAudience(mcpResource, apiResource)}, 401},
		{"delegated-narrows", []PrincipalOption{Audience(mcpResource, apiResource), DelegatedAudience(mcpResource)}, 401},
		{"delegated-narrows-reversed", []PrincipalOption{DelegatedAudience(mcpResource), Audience(mcpResource, apiResource)}, 401},
		{"overlapping-sets", []PrincipalOption{Audience(apiResource, "https://one.example.com"), DelegatedAudience(apiResource, "https://two.example.com")}, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, cred := range []struct {
				name, header, cookie string
				want                 int
			}{
				{"delegated", api, "", tc.want},
				{"web-header", h.accessJWT, "", 401},
				{"web-cookie", "", h.accessJWT, 401},
				{"api-key", key, "", 401},
			} {
				t.Run(cred.name, func(t *testing.T) {
					rec, _ := serveGuarded(h, h.svc.RequirePrincipal(tc.opts...), h.credentialRequest(cred.header, cred.cookie))
					if rec.Code != cred.want {
						t.Fatalf("status=%d, want %d", rec.Code, cred.want)
					}
				})
			}
		})
	}
}

func TestDelegatedAudienceSnapshotsAndReplacesResources(t *testing.T) {
	h := newCredentialHarness(t, authlogic.DelegatedTokensConfig{Issuer: delegatedIssuer})
	api := h.delegatedAPIToken(t, "api-connection")
	mcp, _ := h.delegatedToken(t, "mcp-connection")
	resources := []string{apiResource}
	option := DelegatedAudience(resources...)
	resources[0] = mcpResource
	for _, opts := range [][]PrincipalOption{
		{option},
		{DelegatedAudience(mcpResource), option},
		{DelegatedAudience(mcpResource, apiResource), option},
	} {
		for _, tc := range []struct {
			token string
			want  int
		}{{api, 200}, {mcp, 401}} {
			rec, _ := serveGuarded(h, h.svc.RequirePrincipal(opts...), h.credentialRequest(tc.token, ""))
			if rec.Code != tc.want {
				t.Fatalf("snapshot or replacement changed admission: status=%d, want %d", rec.Code, tc.want)
			}
		}
	}
}

func TestDelegatedAudienceInvalidResourcesPanic(t *testing.T) {
	for _, tc := range []struct {
		name      string
		resources []string
	}{
		{"nil", nil},
		{"empty", []string{}},
		{"empty-resource", []string{""}},
		{"space", []string{" "}},
		{"whitespace", []string{"\t\n"}},
		{"leading-space", []string{" " + apiResource}},
		{"trailing-space", []string{apiResource + " "}},
		{"invalid-second-resource", []string{apiResource, ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("invalid resources did not panic at option construction")
				}
			}()
			DelegatedAudience(tc.resources...)
		})
	}
}

func TestDelegatedAudienceRequiresConfiguredVerifier(t *testing.T) {
	h := newCredentialHarness(t)
	api := h.delegatedAPIToken(t, "connection-a")
	rec, got := serveGuarded(h, h.svc.RequirePrincipal(DelegatedAudience(apiResource)), h.credentialRequest(api, h.accessJWT))
	if rec.Code != http.StatusUnauthorized || got.reached {
		t.Fatalf("audience option enabled unconfigured delegated verification: status=%d", rec.Code)
	}
}

func TestDelegatedAudienceNestedAdmission(t *testing.T) {
	for _, tc := range []struct {
		name         string
		outer, inner []PrincipalOption
		revoke       bool
		want         int
	}{
		{"same-resource", []PrincipalOption{DelegatedAudience(apiResource)}, []PrincipalOption{DelegatedAudience(apiResource)}, false, 200},
		{"strict-inner", []PrincipalOption{DelegatedAudience(apiResource)}, []PrincipalOption{Audience(apiResource)}, false, 200},
		{"strict-outer", []PrincipalOption{Audience(apiResource)}, []PrincipalOption{DelegatedAudience(apiResource)}, false, 200},
		{"default-inner", []PrincipalOption{DelegatedAudience(apiResource)}, nil, false, 401},
		{"first-party-inner", []PrincipalOption{DelegatedAudience(apiResource)}, []PrincipalOption{DelegatedAudience(apiResource), FirstParty()}, false, 401},
		{"wrong-resource-inner", []PrincipalOption{DelegatedAudience(apiResource)}, []PrincipalOption{DelegatedAudience(mcpResource)}, false, 401},
		{"optional-does-not-widen", []PrincipalOption{DelegatedAudience(apiResource)}, []PrincipalOption{Optional()}, false, 401},
		{"default-outer", nil, []PrincipalOption{DelegatedAudience(apiResource)}, false, 401},
		{"wrong-resource-outer", []PrincipalOption{DelegatedAudience(mcpResource)}, []PrincipalOption{DelegatedAudience(apiResource)}, false, 401},
		{"revoked-between-gates", []PrincipalOption{DelegatedAudience(apiResource)}, []PrincipalOption{DelegatedAudience(apiResource)}, true, 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newCredentialHarness(t, authlogic.DelegatedTokensConfig{Issuer: delegatedIssuer})
			api := h.delegatedAPIToken(t, "connection-a")
			mw := func(next http.Handler) http.Handler {
				inner := h.svc.RequirePrincipal(tc.inner...)(next)
				return h.svc.RequirePrincipal(tc.outer...)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if tc.revoke {
						if err := h.sess.Delete(r.Context(), "connection-a"); err != nil {
							t.Fatal(err)
						}
					}
					inner.ServeHTTP(w, r)
				}))
			}
			rec, got := serveGuarded(h, mw, h.credentialRequest(api, h.accessJWT))
			if rec.Code != tc.want || got.reached != (tc.want == http.StatusOK) {
				t.Fatalf("status=%d reached=%v, want %d", rec.Code, got.reached, tc.want)
			}
		})
	}
}

func TestDelegatedAudiencePreservesWebLivenessPolicy(t *testing.T) {
	h := newCredentialHarness(t, authlogic.DelegatedTokensConfig{Issuer: delegatedIssuer})
	if err := h.sess.Delete(context.Background(), h.sessionID); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		opts []PrincipalOption
		want int
	}{
		{"stateless", []PrincipalOption{DelegatedAudience(apiResource)}, 200},
		{"live", []PrincipalOption{DelegatedAudience(apiResource), Live()}, 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec, got := serveGuarded(h, h.svc.RequirePrincipal(tc.opts...), h.credentialRequest("", h.accessJWT))
			if rec.Code != tc.want || got.sessionOK {
				t.Fatalf("web liveness policy changed: status=%d sessionOK=%v", rec.Code, got.sessionOK)
			}
		})
	}
}

func TestDelegatedAudienceRejectsUnboundExchangeClaims(t *testing.T) {
	h := newCredentialHarness(t, authlogic.DelegatedTokensConfig{Issuer: delegatedIssuer})
	api := h.delegatedAPIToken(t, "connection-a")
	for _, tc := range []struct {
		name  string
		claim string
		value any
	}{
		{"wrong-actor", "act", map[string]any{"sub": "another-mcp-server"}},
		{"wrong-client", "client_id", "another-mcp-server"},
		{"wrong-origin", "origin_client_id", "https://another-client.example.com/metadata.json"},
		{"wrong-session", "session_id", h.sessionID},
		{"wrong-issuer", "iss", "https://another-issuer.example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claims, err := h.signer.Verify(api)
			if err != nil {
				t.Fatal(err)
			}
			claims[tc.claim] = tc.value
			token, err := h.signer.Sign(claims, time.Now().Add(time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			rec, got := serveGuarded(h, h.svc.RequirePrincipal(DelegatedAudience(apiResource), Optional()), h.credentialRequest(token, h.accessJWT))
			if rec.Code != http.StatusUnauthorized || got.reached {
				t.Fatalf("invalid exchange reached handler or fell back to cookie: status=%d", rec.Code)
			}
		})
	}
}

func TestDelegatedAudienceIndependentRevocationOverHTTP(t *testing.T) {
	h := newCredentialHarness(t, authlogic.DelegatedTokensConfig{Issuer: delegatedIssuer})
	a := h.delegatedAPIToken(t, "connection-a")
	b := h.delegatedAPIToken(t, "connection-b")
	key, keyID, _ := h.mintKey(t, "http-mixed-route", true, h.userID, time.Time{})
	type identity struct {
		Principal  Principal
		Credential Credential
		SessionID  string
		Live       bool
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, _ := h.svc.CurrentPrincipal(r.Context())
		c, _ := h.svc.CurrentCredential(r.Context())
		sid, live := h.svc.CurrentSessionID(r.Context())
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(identity{p, c, sid, live}); err != nil {
			t.Errorf("encode identity: %v", err)
		}
	})
	mux := http.NewServeMux()
	mux.Handle("/mixed", h.svc.RequirePrincipal(DelegatedAudience(apiResource), Live())(handler))
	mux.Handle("/strict", h.svc.RequirePrincipal(Audience(apiResource))(handler))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	client := server.Client()
	client.Timeout = 5 * time.Second
	request := func(path, header, cookie string, want int) identity {
		t.Helper()
		r, err := http.NewRequest(http.MethodGet, server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if header != "" {
			r.Header.Set("Authorization", "Bearer "+header)
		}
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: h.svc.SessionCookieName(), Value: cookie})
		}
		resp, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("%s status=%d, want %d", path, resp.StatusCode, want)
		}
		var got identity
		if want == http.StatusOK {
			if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
				t.Fatal(err)
			}
		} else if _, err := io.Copy(io.Discard, resp.Body); err != nil {
			t.Fatal(err)
		}
		return got
	}
	for _, grant := range []struct{ token, id string }{{a, "connection-a"}, {b, "connection-b"}} {
		got := request("/mixed", grant.token, "", 200)
		if got.Principal.Type != "user" || got.Principal.ID != h.userID || !got.Live || got.SessionID != grant.id || got.Credential.SessionID != grant.id || got.Credential.Profile != session.ProfileDelegated ||
			got.Credential.ClientID != "mcp-server" || got.Credential.ActorID != "mcp-server" || got.Credential.OriginClientID != "https://client.example.com/metadata.json" ||
			!slices.Equal(got.Credential.Audiences, []string{apiResource}) {
			t.Fatalf("API received incomplete exchanged identity: %+v", got)
		}
	}
	request("/mixed", h.accessJWT, "", 200)
	web := request("/mixed", "", h.accessJWT, 200)
	if web.SessionID != h.sessionID || !web.Live || web.Credential.Profile != session.ProfileFirstParty {
		t.Fatalf("web identity changed: %+v", web)
	}
	keyIdentity := request("/mixed", key, "", 200)
	if keyIdentity.Credential.Kind != CredentialAPIKey || keyIdentity.Credential.APIKeyID != keyID || keyIdentity.Live {
		t.Fatalf("API key acquired a session or lost attribution: %+v", keyIdentity)
	}
	request("/strict", a, "", 200)
	request("/strict", key, "", 401)
	request("/strict", "", h.accessJWT, 401)
	if err := h.sess.Delete(context.Background(), "connection-a"); err != nil {
		t.Fatal(err)
	}
	request("/mixed", a, h.accessJWT, 401)
	request("/strict", a, "", 401)
	request("/mixed", b, "", 200)
	request("/mixed", "", h.accessJWT, 200)
	request("/mixed", key, "", 200)
	if err := h.svc.Logout(context.Background(), "", h.accessJWT); err != nil {
		t.Fatal(err)
	}
	request("/mixed", "", h.accessJWT, 401)
	request("/mixed", b, "", 200)
	request("/mixed", key, "", 200)
	if err := h.svc.RevokeAPIKey(context.Background(), keyID); err != nil {
		t.Fatal(err)
	}
	request("/mixed", key, "", 401)
	request("/mixed", b, "", 200)
}
