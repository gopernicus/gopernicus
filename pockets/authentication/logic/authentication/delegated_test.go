package authentication

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/authgrant"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
)

const delegatedTestIssuer = "https://login.example.test"

func delegatedFixture(t *testing.T) (*harness, TokenPair, session.Session, string) {
	t.Helper()
	h := newHarness(t, nil)
	web := h.loginPair(t, "delegated@example.test", "password123456789")
	userID, _, ok := h.svc.verifyBearerClaims(web.AccessToken)
	if !ok {
		t.Fatal("first-party login failed")
	}
	h.svc.delegatedTokens = DelegatedTokensConfig{Issuer: delegatedTestIssuer}
	delegated, refresh := addDelegatedSession(t, h, userID)
	return h, web, delegated, refresh
}

func addDelegatedSession(t *testing.T, h *harness, userID string) (session.Session, string) {
	t.Helper()
	sess, _ := session.NewSession(userID, time.Hour, time.Now())
	raw := session.NewDelegatedRefreshToken()
	sess.Profile = session.ProfileDelegated
	sess.Delegation = session.Delegation{
		Issuer: delegatedTestIssuer, ClientID: "https://client.example.test/metadata.json",
		Resource: "https://mcp.example.test", ExchangeResource: "https://api.example.test",
		ExchangeClientID: "mcp-service",
	}
	var err error
	sess.RefreshTokenHash, err = h.svc.hashSessionToken(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.sess.Create(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	return sess, raw
}

func delegatedClaims(sess session.Session, exchanged bool) map[string]any {
	claims := map[string]any{
		"user_id": sess.UserID, "session_id": sess.ID, "session_profile": "delegated",
		"iss": sess.Delegation.Issuer, "aud": sess.Delegation.Resource, "client_id": sess.Delegation.ClientID,
	}
	if exchanged {
		claims["aud"] = sess.Delegation.ExchangeResource
		claims["client_id"] = sess.Delegation.ExchangeClientID
		claims["origin_client_id"] = sess.Delegation.ClientID
		claims["act"] = map[string]any{"sub": sess.Delegation.ExchangeClientID}
	}
	return claims
}

func signDelegatedClaims(t *testing.T, h *harness, claims map[string]any) string {
	t.Helper()
	raw, err := h.signer.Sign(claims, time.Now().Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestDelegatedAuthenticationChecksGrantAndReturnsIsolatedMetadata(t *testing.T) {
	h, _, sess, _ := delegatedFixture(t)
	for _, exchanged := range []bool{false, true} {
		raw := signDelegatedClaims(t, h, delegatedClaims(sess, exchanged))
		ctx, ok := h.svc.Authenticate(context.Background(), CredentialAccessToken, TransportHeader, raw)
		if !ok {
			t.Fatalf("exchanged=%v: valid delegated proof denied", exchanged)
		}
		cred, ok := h.svc.CurrentCredential(ctx)
		if !ok || cred.Profile != session.ProfileDelegated || cred.Issuer != delegatedTestIssuer || len(cred.Audiences) != 1 {
			t.Fatalf("wrong verified metadata: %+v", cred)
		}
		if exchanged && (cred.OriginClientID != sess.Delegation.ClientID || cred.ActorID != sess.Delegation.ExchangeClientID || cred.ClientID != cred.ActorID) {
			t.Fatalf("wrong exchange attribution: %+v", cred)
		}
		if id, live := h.svc.CurrentSessionID(ctx); !live || id != sess.ID {
			t.Fatal("Authenticate returned delegated proof without checking liveness")
		}
		cred.Audiences[0] = "changed-by-caller"
		again, _ := h.svc.CurrentCredential(ctx)
		if again.Audiences[0] == "changed-by-caller" {
			t.Fatal("caller changed authenticated audience in context")
		}
		if _, ok := h.svc.Authenticate(context.Background(), CredentialAccessToken, TransportCookie, raw); ok {
			t.Fatal("delegated token authenticated as a browser cookie")
		}
	}
}

func TestDelegatedClaimsNeverDowngradeToFirstParty(t *testing.T) {
	h, _, sess, _ := delegatedFixture(t)
	cases := map[string]func(map[string]any){
		"missing profile":                     func(c map[string]any) { delete(c, "session_profile") },
		"empty profile":                       func(c map[string]any) { c["session_profile"] = "" },
		"unknown profile":                     func(c map[string]any) { c["session_profile"] = "other" },
		"malformed profile":                   func(c map[string]any) { c["session_profile"] = true },
		"first-party profile with delegation": func(c map[string]any) { c["session_profile"] = "first_party" },
		"missing session":                     func(c map[string]any) { delete(c, "session_id") },
		"malformed session":                   func(c map[string]any) { c["session_id"] = 9 },
		"missing user":                        func(c map[string]any) { delete(c, "user_id") },
		"missing issuer":                      func(c map[string]any) { delete(c, "iss") },
		"wrong issuer":                        func(c map[string]any) { c["iss"] = "https://untrusted.example.test" },
		"missing audience":                    func(c map[string]any) { delete(c, "aud") },
		"empty audience":                      func(c map[string]any) { c["aud"] = "" },
		"empty audience array":                func(c map[string]any) { c["aud"] = []string{} },
		"malformed audience":                  func(c map[string]any) { c["aud"] = 1 },
		"mixed audience array":                func(c map[string]any) { c["aud"] = []any{sess.Delegation.Resource, 1} },
		"dual audience": func(c map[string]any) {
			c["aud"] = []string{sess.Delegation.Resource, sess.Delegation.ExchangeResource}
		},
		"missing client":       func(c map[string]any) { delete(c, "client_id") },
		"wrong public client":  func(c map[string]any) { c["client_id"] = "different-client" },
		"direct token to API":  func(c map[string]any) { c["aud"] = sess.Delegation.ExchangeResource },
		"unbound resource":     func(c map[string]any) { c["aud"] = "https://third.example.test" },
		"origin without actor": func(c map[string]any) { c["origin_client_id"] = sess.Delegation.ClientID },
		"actor without origin": func(c map[string]any) { c["act"] = map[string]any{"sub": sess.Delegation.ExchangeClientID} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			claims := delegatedClaims(sess, false)
			mutate(claims)
			ctx, ok := h.svc.Authenticate(context.Background(), CredentialAccessToken, TransportHeader, signDelegatedClaims(t, h, claims))
			if ok {
				t.Fatal("invalid delegated proof admitted")
			}
			if _, ok := h.svc.CurrentCredential(ctx); ok {
				t.Fatal("failed authentication retained proof")
			}
		})
	}
	for _, name := range []string{"iss", "aud", "client_id", "origin_client_id", "act"} {
		t.Run("partial legacy "+name, func(t *testing.T) {
			claims := map[string]any{"user_id": sess.UserID, "session_id": sess.ID, name: "partial"}
			if _, ok := h.svc.Authenticate(context.Background(), CredentialAccessToken, TransportHeader, signDelegatedClaims(t, h, claims)); ok {
				t.Fatal("partial OAuth claims fell back to first-party")
			}
		})
	}
	for _, name := range []string{"origin_client_id", "act", "client_id", "aud"} {
		t.Run("exchange binding "+name, func(t *testing.T) {
			claims := delegatedClaims(sess, true)
			claims[name] = "wrong"
			if _, ok := h.svc.Authenticate(context.Background(), CredentialAccessToken, TransportHeader, signDelegatedClaims(t, h, claims)); ok {
				t.Fatal("invalid exchange binding admitted")
			}
		})
	}
	claims := delegatedClaims(sess, false)
	claims["aud"] = []string{sess.Delegation.Resource}
	if _, ok := h.svc.Authenticate(context.Background(), CredentialAccessToken, TransportHeader, signDelegatedClaims(t, h, claims)); !ok {
		t.Fatal("single-element audience array denied")
	}
}

func TestDelegatedAuthenticationRequiresLiveAuthoritativeBinding(t *testing.T) {
	cases := map[string]func(*harness, *session.Session){
		"feature disabled":      func(h *harness, _ *session.Session) { h.svc.delegatedTokens = DelegatedTokensConfig{} },
		"session store failure": func(h *harness, _ *session.Session) { h.sess.getErr = sdk.ErrUnavailable },
		"missing user store":    func(h *harness, _ *session.Session) { h.svc.users = nil },
		"missing user":          func(h *harness, s *session.Session) { delete(h.users.byID, s.UserID) },
		"disabled user": func(h *harness, s *session.Session) {
			u := h.users.byID[s.UserID]
			u.Status = user.StatusDeactivated
			h.users.byID[s.UserID] = u
		},
		"unknown user status": func(h *harness, s *session.Session) {
			u := h.users.byID[s.UserID]
			u.Status = "unknown"
			h.users.byID[s.UserID] = u
		},
		"wrong row ID":        func(_ *harness, s *session.Session) { s.ID = "other" },
		"wrong row user":      func(_ *harness, s *session.Session) { s.UserID = "other" },
		"expired row":         func(_ *harness, s *session.Session) { s.ExpiresAt = time.Now().Add(-time.Minute) },
		"legacy row":          func(_ *harness, s *session.Session) { s.Profile = ""; s.Delegation = session.Delegation{} },
		"first-party row":     func(_ *harness, s *session.Session) { s.Profile = session.ProfileFirstParty },
		"unknown row profile": func(_ *harness, s *session.Session) { s.Profile = "other" },
		"unpersisted binding": func(_ *harness, s *session.Session) { s.Delegation = session.Delegation{} },
		"changed issuer":      func(_ *harness, s *session.Session) { s.Delegation.Issuer = "other" },
		"changed client":      func(_ *harness, s *session.Session) { s.Delegation.ClientID = "other" },
		"changed resource":    func(_ *harness, s *session.Session) { s.Delegation.Resource = "other" },
		"collapsed resources": func(_ *harness, s *session.Session) { s.Delegation.ExchangeResource = s.Delegation.Resource },
		"collapsed clients":   func(_ *harness, s *session.Session) { s.Delegation.ExchangeClientID = s.Delegation.ClientID },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			h, _, sess, _ := delegatedFixture(t)
			raw := signDelegatedClaims(t, h, delegatedClaims(sess, false))
			id := sess.ID
			mutate(h, &sess)
			h.sess.m[id] = sess
			if _, ok := h.svc.Authenticate(context.Background(), CredentialAccessToken, TransportHeader, raw); ok {
				t.Fatal("unproven delegated binding authenticated")
			}
		})
	}
}

func TestRevocationOfDelegatedSessionIsIndependentAndNeverCached(t *testing.T) {
	h, web, a, _ := delegatedFixture(t)
	b, _ := addDelegatedSession(t, h, a.UserID)
	contexts := map[string]context.Context{}
	tokens := map[string]string{"web": web.AccessToken, "A": signDelegatedClaims(t, h, delegatedClaims(a, false)), "A-api": signDelegatedClaims(t, h, delegatedClaims(a, true)), "B": signDelegatedClaims(t, h, delegatedClaims(b, false)), "B-api": signDelegatedClaims(t, h, delegatedClaims(b, true))}
	for name, raw := range tokens {
		ctx, ok := h.svc.Authenticate(context.Background(), CredentialAccessToken, TransportHeader, raw)
		if !ok {
			t.Fatalf("%s initially denied", name)
		}
		contexts[name] = ctx
	}
	if err := h.sess.Delete(context.Background(), a.ID); err != nil {
		t.Fatal(err)
	}
	for name, raw := range tokens {
		want := name != "A" && name != "A-api"
		if _, ok := h.svc.Authenticate(context.Background(), CredentialAccessToken, TransportHeader, raw); ok != want {
			t.Errorf("%s next authentication = %v, want %v", name, ok, want)
		}
		ctx, ok := h.svc.RequireLive(contexts[name])
		if ok != want {
			t.Errorf("%s reused connection context liveness = %v, want %v", name, ok, want)
		}
		if !ok {
			if _, live := h.svc.CurrentSessionID(ctx); live {
				t.Fatal("failed live check retained proven session")
			}
		}
	}
	if err := h.svc.Logout(context.Background(), web.RefreshToken, web.AccessToken); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"B", "B-api"} {
		if _, ok := h.svc.RequireLive(contexts[name]); !ok {
			t.Fatalf("web logout revoked independent %s", name)
		}
	}
}

func TestLegacyLifecycleRejectsDelegatedWithoutMutatingAnySession(t *testing.T) {
	h, web, sess, refresh := delegatedFixture(t)
	access := signDelegatedClaims(t, h, delegatedClaims(sess, false))
	sess.PreviousRefreshTokenHash, _ = h.svc.hashSessionToken("spent-delegated-refresh")
	sess.PreviousUsed = true
	h.sess.m[sess.ID] = sess
	before := maps.Clone(h.sess.m)
	for _, raw := range []string{refresh, "spent-delegated-refresh"} {
		if _, err := h.svc.Refresh(context.Background(), raw); !errors.Is(err, ErrDelegatedCredential) {
			t.Fatalf("legacy refresh = %v", err)
		}
	}
	for _, pair := range [][2]string{{refresh, ""}, {"", access}, {refresh, web.AccessToken}, {web.RefreshToken, access}} {
		if err := h.svc.Logout(context.Background(), pair[0], pair[1]); !errors.Is(err, ErrDelegatedCredential) {
			t.Fatalf("cross-profile logout = %v", err)
		}
	}
	expiredAccess, err := h.signer.Sign(delegatedClaims(sess, false), time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.svc.Logout(context.Background(), web.RefreshToken, expiredAccess); !errors.Is(err, ErrDelegatedCredential) {
		t.Fatalf("expired delegated access revoked web refresh: %v", err)
	}
	if err := h.svc.Logout(context.Background(), web.RefreshToken, access+"corrupted-signature"); !errors.Is(err, ErrDelegatedCredential) {
		t.Fatalf("unverified delegated shape revoked web refresh: %v", err)
	}
	if !reflect.DeepEqual(before, h.sess.m) {
		t.Fatal("rejected lifecycle request mutated a session")
	}
	if _, err := h.svc.Refresh(context.Background(), web.RefreshToken); err != nil {
		t.Fatalf("web refresh no longer works: %v", err)
	}
}

func TestDelegatedLiveProofCannotOutliveAccessToken(t *testing.T) {
	h, _, sess, _ := delegatedFixture(t)
	raw := signDelegatedClaims(t, h, delegatedClaims(sess, false))
	ctx, ok := h.svc.Authenticate(context.Background(), CredentialAccessToken, TransportHeader, raw)
	if !ok {
		t.Fatal("initial delegated authentication failed")
	}
	h.svc.now = func() time.Time { return time.Now().Add(6 * time.Minute) }
	if _, ok := h.svc.RequireLive(ctx); ok {
		t.Fatal("reused connection proof outlived access expiry")
	}
}

func TestUnknownSessionProfilesFailClosedOnLegacyLifecycle(t *testing.T) {
	for _, profile := range []session.Profile{"unknown", session.ProfileFirstParty, ""} {
		t.Run(string(profile), func(t *testing.T) {
			h, web, sess, refresh := delegatedFixture(t)
			// Exercise the persisted-profile guard independently of the namespace.
			refresh = session.NewRefreshToken()
			sess.RefreshTokenHash, _ = h.svc.hashSessionToken(refresh)
			sess.Profile = profile
			h.sess.m[sess.ID] = sess
			before := maps.Clone(h.sess.m)
			if _, err := h.svc.Refresh(context.Background(), refresh); !errors.Is(err, ErrDelegatedCredential) {
				t.Fatalf("inconsistent session refresh: %v", err)
			}
			if err := h.svc.Logout(context.Background(), refresh, web.AccessToken); !errors.Is(err, ErrDelegatedCredential) {
				t.Fatalf("inconsistent session logout: %v", err)
			}
			if !reflect.DeepEqual(before, h.sess.m) {
				t.Fatal("inconsistent session was mutated")
			}
		})
	}
}

func TestRemovedDelegatedRefreshCannotRevokeWebSession(t *testing.T) {
	h, web, delegated, refresh := delegatedFixture(t)
	if err := h.sess.Delete(context.Background(), delegated.ID); err != nil {
		t.Fatal(err)
	}
	before := maps.Clone(h.sess.m)
	// Storage must not be consulted to distinguish an already removed OAuth
	// connection from the web session whose access token arrived alongside it.
	h.svc.sessions = logoutFailureSessions{SessionRepository: h.sess, lookupErr: sdk.ErrUnavailable}
	for _, raw := range []string{refresh, "oauth2_rt.", "oauth2_rt.invalid"} {
		if _, err := h.svc.Refresh(context.Background(), raw); !errors.Is(err, ErrDelegatedCredential) {
			t.Fatalf("removed delegated refresh = %v", err)
		}
		if err := h.svc.Logout(context.Background(), raw, web.AccessToken); !errors.Is(err, ErrDelegatedCredential) {
			t.Fatalf("removed delegated refresh selected web access: %v", err)
		}
	}
	if !reflect.DeepEqual(before, h.sess.m) {
		t.Fatal("removed delegated refresh revoked web session")
	}
	h.svc.sessions = h.sess
	if err := h.svc.Logout(context.Background(), session.NewRefreshToken(), web.AccessToken); err != nil {
		t.Fatalf("legacy unknown first-party refresh fallback changed: %v", err)
	}
	if len(h.sess.m) != 0 {
		t.Fatal("first-party access fallback did not revoke web session")
	}
}

func TestFirstPartyLiveGateRejectsCrossUserAndCrossProfileSessions(t *testing.T) {
	for _, profile := range []session.Profile{"", session.ProfileFirstParty, session.ProfileDelegated, "unknown"} {
		t.Run(string(profile), func(t *testing.T) {
			h, web, delegated, _ := delegatedFixture(t)
			_, webID, _ := h.svc.verifyBearerClaims(web.AccessToken)
			row := h.sess.m[webID]
			row.Profile = profile
			h.sess.m[webID] = row
			ctx, ok := h.svc.Authenticate(context.Background(), CredentialAccessToken, TransportHeader, web.AccessToken)
			if !ok {
				t.Fatal("first-party stateless semantics changed")
			}
			if _, ok := h.svc.RequireLive(ctx); ok != (profile == "" || profile == session.ProfileFirstParty) {
				t.Fatal("incorrect profile boundary")
			}
			row.Profile = session.ProfileFirstParty
			row.UserID = "other-user"
			h.sess.m[webID] = row
			if _, ok := h.svc.RequireLive(ctx); ok {
				t.Fatal("first-party proof used another user's live row")
			}
			claims := map[string]any{"user_id": delegated.UserID, "session_id": delegated.ID}
			forgedProfile := signDelegatedClaims(t, h, claims)
			if err := h.svc.Logout(context.Background(), "", forgedProfile); !errors.Is(err, ErrDelegatedCredential) {
				t.Fatalf("first-party logout accepted delegated backing row: %v", err)
			}
		})
	}
}

func TestDelegatedSessionCannotObtainFirstPartyStepUp(t *testing.T) {
	h, _, sess, _ := delegatedFixture(t)
	h.svc.authGrants = newFakeAuthGrants()
	sess.Authentication = h.svc.primaryAuthentication(session.MethodPassword)
	h.sess.m[sess.ID] = sess
	if _, err := h.svc.RequireRecentAuthentication(context.Background(), sess.ID, sess.UserID, authgrant.PurposeSetPassword, "", RecentAuthPolicy{}); !errors.Is(err, ErrDelegatedCredential) {
		t.Fatalf("delegated session satisfied first-party step-up: %v", err)
	}
}
