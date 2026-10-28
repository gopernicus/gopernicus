package authentication

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

const (
	testTokenIssuer   = "https://auth.example.test"
	testTokenAudience = "app"
)

func withTokenIssuer(h *harness, issuer, audience string) {
	h.svc.tokenIssuer, h.svc.tokenAudience = issuer, audience
}

// TestFirstPartyTokenCarriesConfiguredIssuerAndAudience proves a set
// SessionsConfig.Issuer/Audience is stamped on the access token and that the
// token then verifies.
func TestFirstPartyTokenCarriesConfiguredIssuerAndAudience(t *testing.T) {
	h := newHarness(t, nil)
	withTokenIssuer(h, testTokenIssuer, testTokenAudience)
	pair := h.loginPair(t, "issuer@example.com", "password123456789")
	claims, err := h.signer.Verify(pair.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if claims["iss"] != testTokenIssuer || claims["aud"] != testTokenAudience {
		t.Fatalf("iss/aud = %v/%v", claims["iss"], claims["aud"])
	}
	if _, _, ok := h.svc.verifyBearerClaims(pair.AccessToken); !ok {
		t.Fatal("a token carrying the configured iss/aud was refused")
	}
}

// TestFirstPartyTokenIssuerAudienceMatrix pins the exact-match rule on a
// first-party token: with Issuer/Audience set a token must carry both exactly,
// aud as a string or a one-element array; with neither set it must carry
// neither; Audience alone requires only aud.
func TestFirstPartyTokenIssuerAudienceMatrix(t *testing.T) {
	claims := func(extra map[string]any) map[string]any {
		c := map[string]any{"user_id": "u1", "session_id": "s1", "session_profile": "first_party"}
		for k, v := range extra {
			c[k] = v
		}
		return c
	}
	both := &Service{tokenIssuer: testTokenIssuer, tokenAudience: testTokenAudience}
	audienceOnly := &Service{tokenAudience: testTokenAudience}
	unset := &Service{}
	cases := []struct {
		name string
		svc  *Service
		in   map[string]any
		want bool
	}{
		{"both/exact", both, map[string]any{"iss": testTokenIssuer, "aud": testTokenAudience}, true},
		{"both/aud array", both, map[string]any{"iss": testTokenIssuer, "aud": []any{testTokenAudience}}, true},
		{"both/no claims", both, nil, false},
		{"both/no iss", both, map[string]any{"aud": testTokenAudience}, false},
		{"both/no aud", both, map[string]any{"iss": testTokenIssuer}, false},
		{"both/wrong iss", both, map[string]any{"iss": "https://other.example.test", "aud": testTokenAudience}, false},
		{"both/wrong aud", both, map[string]any{"iss": testTokenIssuer, "aud": "other"}, false},
		{"both/two audiences", both, map[string]any{"iss": testTokenIssuer, "aud": []any{testTokenAudience, "other"}}, false},
		{"both/non-string iss", both, map[string]any{"iss": 7, "aud": testTokenAudience}, false},
		{"both/client_id", both, map[string]any{"iss": testTokenIssuer, "aud": testTokenAudience, "client_id": "c"}, false},
		{"audience only/aud", audienceOnly, map[string]any{"aud": testTokenAudience}, true},
		{"audience only/stray iss", audienceOnly, map[string]any{"iss": testTokenIssuer, "aud": testTokenAudience}, false},
		{"unset/plain", unset, nil, true},
		{"unset/iss", unset, map[string]any{"iss": testTokenIssuer}, false},
		{"unset/aud", unset, map[string]any{"aud": testTokenAudience}, false},
	}
	for _, tc := range cases {
		if _, _, ok := tc.svc.parseAccessClaims(claims(tc.in)); ok != tc.want {
			t.Errorf("%s: admitted = %v, want %v", tc.name, ok, tc.want)
		}
	}
}

// TestDelegatedTokenUnaffectedByFirstPartyIssuer proves the delegated branch
// keeps its own iss/aud rule when a first-party issuer is configured.
func TestDelegatedTokenUnaffectedByFirstPartyIssuer(t *testing.T) {
	svc := &Service{tokenIssuer: testTokenIssuer, tokenAudience: testTokenAudience}
	delegated := map[string]any{"user_id": "u1", "session_id": "s1", "session_profile": "delegated",
		"exp": float64(time.Now().Add(time.Minute).Unix()), "iss": "https://as.example.test", "client_id": "client", "aud": "https://rs.example.test"}
	if _, cred, ok := svc.parseAccessClaims(delegated); !ok || cred.Issuer != "https://as.example.test" {
		t.Fatalf("delegated token = %+v, %v", cred, ok)
	}
}

// TestTokenWithoutIssuerRefusedAfterEnablement proves a token minted before
// Issuer was set fails once it is (the client refreshes once).
func TestTokenWithoutIssuerRefusedAfterEnablement(t *testing.T) {
	h := newHarness(t, nil)
	pair := h.loginPair(t, "enable@example.com", "password123456789")
	withTokenIssuer(h, testTokenIssuer, testTokenAudience)
	if _, _, ok := h.svc.verifyBearerClaims(pair.AccessToken); ok {
		t.Fatal("a token without iss/aud verified under a configured issuer")
	}
}

// TestExpiredTokenLogoutUnderIssuer is BE-2: an expired first-party token
// carrying the configured iss/aud is not mistaken for a delegated credential,
// so logout with it and the refresh token succeeds; a foreign iss still is.
func TestExpiredTokenLogoutUnderIssuer(t *testing.T) {
	h := newHarness(t, nil)
	withTokenIssuer(h, testTokenIssuer, testTokenAudience)
	pair := h.loginPair(t, "logout-issuer@example.com", "password123456789")
	_, sid, ok := h.svc.verifyBearerClaims(pair.AccessToken)
	if !ok {
		t.Fatal("fresh token refused")
	}
	h.signer.now = func() time.Time { return time.Now().Add(24 * time.Hour) }
	if err := h.svc.Logout(context.Background(), "", pair.AccessToken); errors.Is(err, ErrDelegatedCredential) || !errors.Is(err, sdk.ErrUnauthorized) {
		t.Fatalf("expired access-only logout = %v, want the generic 401", err)
	}
	if err := h.svc.Logout(context.Background(), pair.RefreshToken, pair.AccessToken); err != nil {
		t.Fatalf("expired-token logout with Issuer set: %v", err)
	}
	if _, err := h.sess.Get(context.Background(), sid); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("session survived logout: %v", err)
	}

	// session_profile=first_party is present, so iss/aud are left to
	// admission: whatever the configured issuer, the shape is first-party.
	h.svc.tokenIssuer = "https://other.example.test"
	if unverifiedDelegatedShape(pair.AccessToken) {
		t.Error("a first_party payload with iss is treated as delegated")
	}
	h.svc.tokenIssuer, h.svc.tokenAudience = "", ""
	if unverifiedDelegatedShape(pair.AccessToken) {
		t.Error("a first_party payload with iss is treated as delegated with no issuer configured")
	}
}

// TestDelegatedShape pins the one logout shape rule (HIGH-1, MED-1).
func TestDelegatedShape(t *testing.T) {
	cases := []struct {
		name   string
		claims map[string]any
		want   bool
	}{
		{"first_party plain", map[string]any{"session_profile": "first_party"}, false},
		{"first_party with iss/aud", map[string]any{"session_profile": "first_party", "iss": "x", "aud": "y"}, false},
		{"first_party with client_id", map[string]any{"session_profile": "first_party", "client_id": "c"}, true},
		{"first_party with act", map[string]any{"session_profile": "first_party", "act": map[string]any{"sub": "c"}}, true},
		{"delegated", map[string]any{"session_profile": "delegated"}, true},
		{"profile absent plain", map[string]any{"user_id": "u"}, false},
		{"profile absent iss", map[string]any{"iss": "x"}, true},
		{"profile absent aud", map[string]any{"aud": "y"}, true},
		{"profile absent origin_client_id", map[string]any{"origin_client_id": "c"}, true},
	}
	for _, tc := range cases {
		if got := delegatedShape(tc.claims); got != tc.want {
			t.Errorf("%s: delegatedShape = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestLogoutRevokesAcrossIssuerChange is HIGH-1: a still-valid bearer minted
// before an Issuer/Audience enable, change or rollback is refused by
// admission but is first-party shaped, so logout with it and the refresh
// token revokes the session, while an access-only logout gets the generic 401.
func TestLogoutRevokesAcrossIssuerChange(t *testing.T) {
	for _, tc := range []struct {
		name               string
		mintIss, mintAud   string
		laterIss, laterAud string
	}{
		{"enable", "", "", testTokenIssuer, testTokenAudience},
		{"rollback", testTokenIssuer, testTokenAudience, "", ""},
		{"change", testTokenIssuer, testTokenAudience, "https://new.example.test", "app2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, nil)
			withTokenIssuer(h, tc.mintIss, tc.mintAud)
			pair := h.loginPair(t, "change-"+tc.name+"@example.com", "password123456789")
			_, sid, ok := h.svc.verifyBearerClaims(pair.AccessToken)
			if !ok {
				t.Fatal("fresh token refused")
			}
			withTokenIssuer(h, tc.laterIss, tc.laterAud)
			if _, _, ok := h.svc.verifyBearerClaims(pair.AccessToken); ok {
				t.Fatal("the pre-change token still passes admission")
			}
			ctx := context.Background()
			if err := h.svc.Logout(ctx, "", pair.AccessToken); errors.Is(err, ErrDelegatedCredential) || !errors.Is(err, sdk.ErrUnauthorized) {
				t.Fatalf("access-only logout = %v, want the generic 401", err)
			}
			if err := h.svc.Logout(ctx, pair.RefreshToken, pair.AccessToken); err != nil {
				t.Fatalf("logout with the pre-change bearer and the refresh token: %v", err)
			}
			if _, err := h.sess.Get(ctx, sid); !errors.Is(err, sdk.ErrNotFound) {
				t.Fatalf("session survived logout: %v", err)
			}
		})
	}
}

func TestValidateTokenIssuer(t *testing.T) {
	cases := []struct {
		name string
		cfg  constructorConfig
		want error
	}{
		{"unset", constructorConfig{}, nil},
		{"both", constructorConfig{TokenIssuer: testTokenIssuer, TokenAudience: testTokenAudience}, nil},
		{"audience only", constructorConfig{TokenAudience: testTokenAudience}, nil},
		{"whitespace issuer", constructorConfig{TokenIssuer: "   "}, ErrTokenIssuerInvalid},
		{"padded issuer", constructorConfig{TokenIssuer: " " + testTokenIssuer}, ErrTokenIssuerInvalid},
		{"padded audience", constructorConfig{TokenAudience: testTokenAudience + "\n"}, ErrTokenIssuerInvalid},
		{"delegated issuer", constructorConfig{TokenIssuer: testTokenIssuer, DelegatedTokens: DelegatedTokensConfig{Issuer: testTokenIssuer}}, ErrTokenAudienceCollides},
		{"distinct delegated issuer", constructorConfig{TokenIssuer: testTokenIssuer, DelegatedTokens: DelegatedTokensConfig{Issuer: "https://as.example.test"}}, nil},
		{"external provider issuer", constructorConfig{TokenIssuer: extIssuerA, External: &ExternalConfig{Providers: []ExternalProvider{{Name: "alpha", Provider: &extProvider{issuer: extIssuerA}}}}}, ErrTokenAudienceCollides},
		{"distinct external provider issuer", constructorConfig{TokenIssuer: testTokenIssuer, External: &ExternalConfig{Providers: []ExternalProvider{{Name: "alpha", Provider: &extProvider{issuer: extIssuerA}}}}}, nil},
	}
	for _, tc := range cases {
		err := validateTokenIssuer(tc.cfg)
		if (tc.want == nil) != (err == nil) || (tc.want != nil && (!errors.Is(err, tc.want) || !errors.Is(err, sdk.ErrInvalidInput))) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
}
