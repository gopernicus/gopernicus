package authentication_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	authentication "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// These stubs intentionally have no operational storage. Construction and rejected
// credentials must never invoke their embedded repository methods.
type users struct{ user.UserRepository }
type identifiers struct {
	identifier.IdentifierRepository
}
type sessions struct{ session.SessionRepository }
type activeSessions struct{ session.ActiveUserRepository }
type signer struct{ calls *int }

func (s signer) Sign(map[string]any, time.Time) (string, error) {
	return "", errors.New("unexpected sign")
}
func (s signer) Verify(string) (map[string]any, error) {
	*s.calls++
	return nil, errors.New("invalid proof")
}

func validDeps() authenticationFixture {
	calls := 0
	return authenticationFixture{RuntimeMode: environment.ModeDevelopment, PasswordFlowsDisabled: true, Users: users{}, Identifiers: identifiers{}, Sessions: sessions{}, ActiveSessions: activeSessions{}, TokenSigner: signer{&calls}, Limiter: ratelimiter.NewMemory()}
}

func TestPublicServiceConstructorFailsBeforeStorage(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*authenticationFixture)
	}{
		{"missing user store", func(d *authenticationFixture) { d.Users = nil }},
		{"typed nil user store", func(d *authenticationFixture) { d.Users = (*users)(nil) }},
		{"missing signer", func(d *authenticationFixture) { d.TokenSigner = nil }},
		{"missing active session fence", func(d *authenticationFixture) { d.ActiveSessions = nil }},
		{"password mode without hasher", func(d *authenticationFixture) { d.PasswordFlowsDisabled = false }},
		{"passwordless without delivery", func(d *authenticationFixture) { d.Passwordless = []string{"email"} }},
		{"provisioning without atomic store", func(d *authenticationFixture) { d.ProvisionOnRedeem = true }},
		{"production without keyer", func(d *authenticationFixture) { d.RuntimeMode = environment.ModeProduction }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := validDeps()
			tc.mutate(&d)
			got, err := newPublicFixture(d)
			if got != nil || !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("NewService = %v, %v", got, err)
			}
		})
	}
	if _, err := newPublicFixture(validDeps()); err != nil {
		t.Fatal(err)
	}
}

func TestPublicAuthenticationRequiresVerifiedProof(t *testing.T) {
	d := validDeps()
	calls := 0
	d.TokenSigner = signer{&calls}
	components, err := newPublicFixture(d)
	if err != nil {
		t.Fatal(err)
	}
	service := components.Service
	ctx := sdk.WithPrincipal(context.Background(), sdk.Principal{Type: "user", ID: "unverified"})
	if _, ok := service.RequireLive(ctx); ok {
		t.Fatal("host principal context became credential proof")
	}
	if _, ok := service.Authenticate(ctx, authentication.CredentialAccessToken, authentication.TransportHeader, "invalid"); ok {
		t.Fatal("invalid token authenticated")
	}
	if calls != 1 {
		t.Fatalf("verification calls=%d", calls)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, ok := service.Authenticate(canceled, authentication.CredentialAccessToken, authentication.TransportHeader, "invalid"); ok {
		t.Fatal("canceled proof authenticated")
	}
	if calls != 1 {
		t.Fatal("canceled authentication called the signer")
	}
	if _, ok := service.Authenticate(ctx, authentication.CredentialAccessToken, "invalid", "token"); ok {
		t.Fatal("unknown transport authenticated")
	}
	if _, ok := service.Authenticate(ctx, authentication.CredentialAPIKey, authentication.TransportCookie, "key"); ok {
		t.Fatal("cookie API key authenticated")
	}
}

// liveStore isolates the service/store boundary: only explicitly live tokens
// have backing sessions, and each store counts its own verification work.
type liveStore struct {
	session.SessionRepository
	live  map[string]bool
	reads int
}

func (s *liveStore) Get(_ context.Context, id string) (session.Session, error) {
	s.reads++
	if !s.live[id] {
		return session.Session{}, sdk.ErrNotFound
	}
	return session.Session{ID: id, UserID: "user", ExpiresAt: time.Now().Add(time.Hour)}, nil
}

type proofSigner struct{}

func (proofSigner) Sign(map[string]any, time.Time) (string, error) {
	return "", errors.New("unexpected sign")
}
func (proofSigner) Verify(raw string) (map[string]any, error) {
	if raw == "invalid" {
		return nil, errors.New("invalid signature")
	}
	return map[string]any{"user_id": "user", "session_id": raw}, nil
}

func TestReplacingCredentialClearsEarlierLiveSession(t *testing.T) {
	store := &liveStore{live: map[string]bool{"A": true}}
	d := validDeps()
	d.Sessions = store
	d.TokenSigner = proofSigner{}
	built, err := newPublicFixture(d)
	if err != nil {
		t.Fatal(err)
	}
	service := built.Service
	first, ok := service.Authenticate(context.Background(), authentication.CredentialAccessToken, authentication.TransportHeader, "A")
	if !ok {
		t.Fatal("valid signature rejected")
	}
	first, ok = service.RequireLive(first)
	if !ok {
		t.Fatal("live A rejected")
	}
	if _, ok = service.RequireLive(first); !ok || store.reads != 1 {
		t.Fatal("same proof did not reuse its live check")
	}
	replacement, ok := service.Authenticate(first, authentication.CredentialAccessToken, authentication.TransportHeader, "B")
	if !ok {
		t.Fatal("valid signature for revoked B rejected at stateless tier")
	}
	if id, ok := service.CurrentSessionID(replacement); ok {
		t.Fatalf("new proof inherited live session %q", id)
	}
	if _, ok = service.RequireLive(replacement); ok {
		t.Fatal("revoked B borrowed A's liveness")
	}
	if id, ok := service.CurrentSessionID(first); !ok || id != "A" {
		t.Fatal("replacement mutated its caller's original context")
	}
	failed, ok := service.Authenticate(first, authentication.CredentialAccessToken, authentication.TransportHeader, "invalid")
	if ok {
		t.Fatal("invalid signature accepted")
	}
	if _, ok := service.CurrentCredential(failed); ok {
		t.Fatal("failed replacement retained credential proof")
	}
	if _, ok := service.RequireLive(failed); ok {
		t.Fatal("failed replacement retained liveness")
	}
	if _, ok := service.CurrentPrincipal(failed); ok {
		t.Fatal("failed replacement retained principal")
	}
	altered := sdk.WithPrincipal(first, sdk.Principal{Type: "user", ID: "other"})
	if _, ok := service.RequireLive(altered); ok {
		t.Fatal("host principal replacement retained another principal's proof")
	}
}

func TestCredentialProofCannotCrossServiceStores(t *testing.T) {
	firstStore := &liveStore{live: map[string]bool{"A": true}}
	secondStore := &liveStore{live: map[string]bool{}}
	d := validDeps()
	d.Sessions = firstStore
	d.TokenSigner = proofSigner{}
	first, err := newPublicFixture(d)
	if err != nil {
		t.Fatal(err)
	}
	d.Sessions = secondStore
	second, err := newPublicFixture(d)
	if err != nil {
		t.Fatal(err)
	}
	ctx, ok := first.Service.Authenticate(context.Background(), authentication.CredentialAccessToken, authentication.TransportHeader, "A")
	if !ok {
		t.Fatal("valid proof rejected")
	}
	ctx, ok = first.Service.RequireLive(ctx)
	if !ok {
		t.Fatal("live proof rejected")
	}
	if _, ok := second.Service.CurrentCredential(ctx); ok {
		t.Fatal("foreign service accepted credential proof")
	}
	if _, ok := second.Service.CurrentSessionID(ctx); ok {
		t.Fatal("foreign service accepted live session")
	}
	if _, ok := second.Service.RequireLive(ctx); ok {
		t.Fatal("foreign proof bypassed its own store")
	}
	rebound, ok := second.Service.Authenticate(ctx, authentication.CredentialAccessToken, authentication.TransportHeader, "A")
	if !ok {
		t.Fatal("reverification rejected valid signature")
	}
	if _, ok := second.Service.RequireLive(rebound); ok {
		t.Fatal("reverified proof bypassed missing session in second store")
	}
	if secondStore.reads != 1 {
		t.Fatalf("second store reads=%d, want 1", secondStore.reads)
	}
}

func TestPublicOptionsRequireExplicitPrerequisites(t *testing.T) {
	if _, err := authentication.New(authentication.Repositories{}, nil, environment.ModeDevelopment, nil, nil); !errors.Is(err, sdk.ErrInvalidInput) || !strings.Contains(err.Error(), "nil option") {
		t.Fatalf("nil option: %v", err)
	}
	d := validDeps()
	_, err := authentication.New(authentication.Repositories{Users: d.Users, Identifiers: d.Identifiers, Sessions: d.Sessions, ActiveSessions: d.ActiveSessions}, d.TokenSigner, d.RuntimeMode, d.Limiter,
		authentication.WithPassword(authentication.PasswordConfig{PasswordFlowsDisabled: true}),
		authentication.WithPassword(authentication.PasswordConfig{}),
	)
	if !errors.Is(err, sdk.ErrInvalidInput) || !strings.Contains(err.Error(), "password flows require") {
		t.Fatalf("password policy replacement: %v", err)
	}
}
