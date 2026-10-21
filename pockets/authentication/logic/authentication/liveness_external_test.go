package authentication

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/externalsubject"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
	sdkauth "github.com/gopernicus/gopernicus/sdk/capabilities/authentication"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// hybridService builds a Service through New with one external provider and a
// mirrored, active local user u1 for subject s1.
func hybridService(t *testing.T, h *harness, mutate func(*ExternalConfig)) (*Service, *extProvider, *extRevocation) {
	t.Helper()
	store := newExtStore()
	store.subjects["alpha|s1"] = externalsubject.ExternalSubject{Provider: "alpha", Subject: "s1", UserID: "u1"}
	h.users.byID["u1"] = user.User{ID: "u1", Status: user.StatusActive}
	provider := &extProvider{issuer: extIssuerA, identities: map[string]sdkauth.Identity{}}
	revocation := &extRevocation{}
	cfg := ExternalConfig{Providers: []ExternalProvider{{Name: "alpha", Provider: provider, Revocation: revocation}}}
	if mutate != nil {
		mutate(&cfg)
	}
	components, err := New(Repositories{
		Users: h.users, Identifiers: h.idents, Sessions: h.sess, ActiveSessions: fakeActiveSessions{h.users, h.sess},
		ExternalSubjects: extSubjects{store},
	}, h.signer, environment.ModeDevelopment, ratelimiter.NewMemory(),
		WithPassword(PasswordConfig{PasswordFlowsDisabled: true}),
		WithDelegatedTokens(DelegatedTokensConfig{Issuer: delegatedTestIssuer}),
		WithExternal(cfg))
	if err != nil {
		t.Fatal(err)
	}
	return components.Service, provider, revocation
}

func TestServiceClassifyBearerKeepsNativeTokensAccessTokens(t *testing.T) {
	h, web, sess, _ := delegatedFixture(t)
	delegated := signDelegatedClaims(t, h, delegatedClaims(sess, false))
	svc, _, _ := hybridService(t, h, nil)
	for name, raw := range map[string]string{"first-party": web.AccessToken, "delegated": delegated} {
		if kind, ok := svc.ClassifyBearer(raw); !ok || kind != CredentialAccessToken {
			t.Fatalf("%s token classified %q, %v; want access_token", name, kind, ok)
		}
		if kind, ok := h.svc.ClassifyBearer(raw); !ok || kind != CredentialAccessToken {
			t.Fatalf("%s token on a host without providers classified %q, %v", name, kind, ok)
		}
	}
	if kind, ok := svc.ClassifyBearer(extToken(extIssuerA, "s1")); !ok || kind != CredentialExternalToken {
		t.Fatalf("wired issuer classified %q, %v", kind, ok)
	}
	for _, raw := range []string{extToken("https://unwired.example.test", "s1"), "dotless_api_key", "a.!!!.b"} {
		if _, ok := svc.ClassifyBearer(raw); ok {
			t.Fatalf("%q classified", raw)
		}
	}
	if _, ok := h.svc.ClassifyBearer(extToken(extIssuerA, "s1")); ok {
		t.Fatal("a host without providers classified an external token")
	}
	if !svc.ExternalEnabled() || h.svc.ExternalEnabled() || svc.ExternalAuthenticator() == nil {
		t.Fatal("ExternalEnabled does not follow the wiring")
	}
}

func TestServiceExternalLivenessIsCheckedBeforeKindPass(t *testing.T) {
	h := newHarness(t, nil)
	svc, provider, revocation := hybridService(t, h, nil)
	raw := extToken(extIssuerA, "s1")
	provider.identities[raw] = sdkauth.Identity{Issuer: extIssuerA, Subject: "s1", AuthenticatedAt: time.Now().Add(-time.Hour)}
	revocation.set("s1", sdkauth.RevocationFacts{ValidAfter: time.Now()}, nil)

	ctx, ok := svc.Authenticate(context.Background(), CredentialExternalToken, TransportHeader, raw)
	if !ok {
		t.Fatal("hybrid Service rejected a wired external token")
	}
	if p, _ := svc.CurrentPrincipal(ctx); p.ID != "u1" {
		t.Fatalf("principal = %+v, want the mirrored user", p)
	}
	if _, ok := svc.RequireLive(ctx); ok {
		t.Fatal("Live() passed a revoked external token")
	}
	if _, err := svc.requireLive(ctx, LivenessOnMutation, false); err != nil {
		t.Fatalf("safe read under the default tier re-checked: %v", err)
	}
	if _, err := svc.requireLive(ctx, LivenessStateless, true); err != nil {
		t.Fatalf("Stateless re-checked: %v", err)
	}
	if _, err := svc.requireLive(ctx, LivenessOnMutation, true); !errors.Is(err, ErrRevoked) {
		t.Fatalf("mutation under the default tier = %v, want revoked", err)
	}
}

func TestServiceExternalNestedGateReadsOnce(t *testing.T) {
	h := newHarness(t, nil)
	svc, provider, revocation := hybridService(t, h, nil)
	raw := extToken(extIssuerA, "s1")
	provider.identities[raw] = sdkauth.Identity{Issuer: extIssuerA, Subject: "s1", AuthenticatedAt: time.Now()}
	revocation.set("s1", sdkauth.RevocationFacts{}, nil)
	ctx, err := svc.authenticate(context.Background(), CredentialExternalToken, TransportHeader, raw)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err = svc.requireLive(ctx, LivenessOnMutation, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.requireLive(ctx, LivenessLive, true); err != nil {
		t.Fatal(err)
	}
	stats := svc.ExternalAuthenticator().RevocationStats()
	if got := revocation.calls.Load(); got != 1 || stats.Hits+stats.Misses != 1 {
		t.Fatalf("CheckRevocation = %d, revocation lookups = %d, want the proof stamp to spare the nested gate", got, stats.Hits+stats.Misses)
	}
}

func TestServiceExternalWiringValidation(t *testing.T) {
	h := newHarness(t, nil)
	provider := &extProvider{issuer: extIssuerA}
	build := func(repos Repositories, issuer string) error {
		_, err := New(repos, h.signer, environment.ModeDevelopment, ratelimiter.NewMemory(),
			WithPassword(PasswordConfig{PasswordFlowsDisabled: true}),
			WithDelegatedTokens(DelegatedTokensConfig{Issuer: delegatedTestIssuer}),
			WithExternal(ExternalConfig{Providers: []ExternalProvider{{Name: "alpha", Provider: &extProvider{issuer: issuer}}}}))
		return err
	}
	base := Repositories{Users: h.users, Identifiers: h.idents, Sessions: h.sess, ActiveSessions: fakeActiveSessions{h.users, h.sess}}
	if err := build(base, provider.issuer); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("providers without ExternalSubjects = %v, want invalid input", err)
	}
	base.ExternalSubjects = extSubjects{newExtStore()}
	if err := build(base, delegatedTestIssuer); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("provider reusing the delegated issuer = %v, want invalid input", err)
	}
	if err := build(base, provider.issuer); err != nil {
		t.Fatal(err)
	}
}

func TestServiceNativeLivenessTiers(t *testing.T) {
	h := newHarness(t, nil)
	pair := h.loginPair(t, "tiers@example.test", "password123456789")
	ctx, ok := h.svc.Authenticate(context.Background(), CredentialAccessToken, TransportHeader, pair.AccessToken)
	if !ok {
		t.Fatal("first-party token rejected")
	}
	_, sessionID, _ := h.svc.verifyBearerClaims(pair.AccessToken)
	if err := h.sess.Delete(context.Background(), sessionID); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		tier   LivenessTier
		unsafe bool
		live   bool
	}{
		{LivenessStateless, true, true},
		{LivenessOnMutation, false, true},
		{LivenessOnMutation, true, false},
		{LivenessLive, false, false},
	} {
		_, err := h.svc.requireLive(ctx, tc.tier, tc.unsafe)
		if (err == nil) != tc.live {
			t.Fatalf("tier=%d unsafe=%v: err = %v, want live=%v", tc.tier, tc.unsafe, err, tc.live)
		}
		if err != nil && !errors.Is(err, sdk.ErrUnauthorized) {
			t.Fatalf("revoked session = %v, want unauthorized", err)
		}
	}
}

func TestServiceSessionReadErrorIsUnavailable(t *testing.T) {
	h := newHarness(t, nil)
	pair := h.loginPair(t, "outage@example.test", "password123456789")
	ctx, ok := h.svc.Authenticate(context.Background(), CredentialAccessToken, TransportHeader, pair.AccessToken)
	if !ok {
		t.Fatal("first-party token rejected")
	}
	h.sess.getErr = errors.New("connection reset")
	_, err := h.svc.requireLive(ctx, LivenessLive, true)
	if !errors.Is(err, sdk.ErrUnavailable) || errors.Is(err, sdk.ErrUnauthorized) {
		t.Fatalf("session store outage = %v, want unavailable, not a denial", err)
	}
	if _, ok := h.svc.RequireLive(ctx); ok {
		t.Fatal("the bool surface passed an unreadable session")
	}
}
