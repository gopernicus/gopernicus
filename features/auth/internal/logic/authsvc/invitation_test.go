package authsvc

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/ratelimiter"
)

// fakeResolver records the resolve-on-registration calls authsvc makes and can
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

func serviceWithResolver(resolver invitationResolver) (*Service, *fakeCodes) {
	codes := newFakeCodes()
	svc := NewService(Deps{
		Users:       newFakeUsers(),
		Passwords:   newFakePasswords(),
		Sessions:    newFakeSessions(),
		Codes:       codes,
		Tokens:      newFakeTokens(),
		Hasher:      &fakeHasher{},
		Mailer:      &recordingMailer{},
		Limiter:     ratelimiter.NewMemory(),
		Cookie:      CookieConfig{},
		Invitations: resolver,
	})
	return svc, codes
}

// TestRegisterResolvesInvitations proves Register calls the resolver with the
// normalized email, the "user" subject type, and the new user id.
func TestRegisterResolvesInvitations(t *testing.T) {
	resolver := &fakeResolver{}
	svc, _ := serviceWithResolver(resolver)

	u, err := svc.Register(context.Background(), "New@Example.com", "password123", "N")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(resolver.calls) != 1 {
		t.Fatalf("resolver call count = %d, want 1", len(resolver.calls))
	}
	got := resolver.calls[0]
	if got.email != "new@example.com" || got.subjectType != PrincipalUser || got.subjectID != u.ID {
		t.Errorf("resolve call = %+v, want {new@example.com, user, %s}", got, u.ID)
	}
}

// TestRegisterResolveErrorDoesNotFailRegistration proves the best-effort
// contract: a resolver error never aborts registration.
func TestRegisterResolveErrorDoesNotFailRegistration(t *testing.T) {
	resolver := &fakeResolver{err: errors.New("grant boom")}
	svc, _ := serviceWithResolver(resolver)

	if _, err := svc.Register(context.Background(), "user@example.com", "password123", "U"); err != nil {
		t.Errorf("Register with a failing resolver: err=%v, want nil (best-effort)", err)
	}
}

// TestVerifyResolvesInvitations proves Verify also resolves (a no-verify host
// resolves at register; a verifying host resolves again — idempotent).
func TestVerifyResolvesInvitations(t *testing.T) {
	resolver := &fakeResolver{}
	svc, codes := serviceWithResolver(resolver)

	u, err := svc.Register(context.Background(), "verify@example.com", "password123", "V")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	var code string
	for c := range codes.m {
		code = c
	}
	if err := svc.Verify(context.Background(), code); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(resolver.calls) != 2 {
		t.Fatalf("resolver call count = %d, want 2 (register + verify)", len(resolver.calls))
	}
	last := resolver.calls[1]
	if last.email != "verify@example.com" || last.subjectID != u.ID {
		t.Errorf("verify resolve call = %+v, want {verify@example.com, user, %s}", last, u.ID)
	}
}

// TestRegisterNilResolverIsNoop proves a nil resolver (invitations off) never
// panics and never affects registration.
func TestRegisterNilResolverIsNoop(t *testing.T) {
	svc, _ := serviceWithResolver(nil)
	if _, err := svc.Register(context.Background(), "noresolver@example.com", "password123", "N"); err != nil {
		t.Errorf("Register with nil resolver: %v", err)
	}
}
