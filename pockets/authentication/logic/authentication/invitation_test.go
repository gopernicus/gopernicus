package authentication

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
)

// fakeResolver records the verified-ownership resolver calls authentication service makes and can
// force an error to prove the best-effort contract.
type fakeResolver struct {
	calls []resolverCall
	err   error
}

type resolverCall struct{ email, subjectType, subjectID string }

func (f *fakeResolver) ResolveInvitations(_ context.Context, email, subjectType, subjectID string) (int, error) {
	f.calls = append(f.calls, resolverCall{email, subjectType, subjectID})
	return len(f.calls), f.err
}

func serviceWithResolver(t *testing.T, resolver invitationResolver) (*Service, *recordingMailer) {
	t.Helper()
	users := newFakeUsers()
	mailer := &recordingMailer{}
	svc := newServiceWithFakes(constructorConfig{
		Users:       users,
		Identifiers: newFakeIdentifiers(users),
		Passwords:   newFakePasswords(),
		Sessions:    newFakeSessions(),
		Challenges:  newFakeChallenges(),
		Protector:   newFakeProtector("k1", "k1"),
		Hasher:      &fakeHasher{},
		Limiter:     ratelimiter.NewMemory(),
		Invitations: resolver,
	})
	wireSyncDelivery(t, svc, mailer, nil)
	return svc, mailer
}

// Registration alone does not establish ownership, even on a host that permits
// unverified login. Invitation grants wait for verification.
func TestRegisterDoesNotResolveUnverifiedInvitations(t *testing.T) {
	resolver := &fakeResolver{}
	svc, _ := serviceWithResolver(t, resolver)
	if _, err := svc.Register(context.Background(), "New@Example.com", "password123456789", "N"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(resolver.calls) != 0 {
		t.Fatalf("unverified registration resolved invitations: %+v", resolver.calls)
	}
}

// TestRegisterResolveErrorDoesNotFailRegistration proves the best-effort
// contract: a resolver error never aborts registration.
func TestRegisterResolveErrorDoesNotFailRegistration(t *testing.T) {
	resolver := &fakeResolver{err: errors.New("grant boom")}
	svc, _ := serviceWithResolver(t, resolver)

	if _, err := svc.Register(context.Background(), "user@example.com", "password123456789", "U"); err != nil {
		t.Errorf("Register with a failing resolver: err=%v, want nil (best-effort)", err)
	}
}

// TestVerifyResolvesInvitations proves grants follow verified ownership.
func TestVerifyResolvesInvitations(t *testing.T) {
	resolver := &fakeResolver{}
	svc, mailer := serviceWithResolver(t, resolver)

	u, err := svc.Register(context.Background(), "verify@example.com", "password123456789", "V")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	code := verificationCodeFromMail(t, mailer.last())
	if err := svc.Verify(context.Background(), "verify@example.com", code); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(resolver.calls) != 1 {
		t.Fatalf("resolver call count = %d, want 1 (verify only)", len(resolver.calls))
	}
	last := resolver.calls[0]
	if last.email != "verify@example.com" || last.subjectType != PrincipalUser || last.subjectID != u.ID {
		t.Errorf("verify resolve call = %+v, want {verify@example.com, user, %s}", last, u.ID)
	}
}

// TestRegisterNilResolverIsNoop proves a nil resolver (invitations off) never
// panics and never affects registration.
func TestRegisterNilResolverIsNoop(t *testing.T) {
	svc, _ := serviceWithResolver(t, nil)
	if _, err := svc.Register(context.Background(), "noresolver@example.com", "password123456789", "N"); err != nil {
		t.Errorf("Register with nil resolver: %v", err)
	}
}
