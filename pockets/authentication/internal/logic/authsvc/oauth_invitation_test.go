package authsvc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/domain/oauthaccount"
)

// withResolver wires the invitation resolver into the OAuth harness Deps.
func withResolver(r invitationResolver) func(*Deps) {
	return func(d *Deps) { d.Invitations = r }
}

// TestOAuthRegisterResolvesInvitations proves the ONE new OAuth resolve site
// (branch 3, register-and-link): provisioning a brand-new account from a
// provider-verified email calls the SAME resolver Register calls, with the
// normalized stored email, the "user" subject type, and the new user id.
func TestOAuthRegisterResolvesInvitations(t *testing.T) {
	resolver := &fakeResolver{}
	p := &fakeProvider{name: "google", trust: true, providerUserID: "g-invited", email: "Invited@Example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil, withResolver(resolver))

	state := h.startState(t, "")
	res, err := h.svc.OAuthCallback(context.Background(), "google", "code", state)
	if err != nil {
		t.Fatalf("OAuthCallback: %v", err)
	}
	if res.Action != ActionRegister {
		t.Fatalf("Action = %q, want register", res.Action)
	}
	if len(resolver.calls) != 1 {
		t.Fatalf("resolver call count = %d, want 1", len(resolver.calls))
	}
	got := resolver.calls[0]
	if got.email != "invited@example.com" || got.subjectType != PrincipalUser || got.subjectID != res.User.ID {
		t.Errorf("resolve call = %+v, want {invited@example.com, user, %s}", got, res.User.ID)
	}
}

// TestOAuthLoginExistingUserDoesNotResolve proves branch 1 never re-grants: an
// ordinary OAuth login of an ALREADY-LINKED user is not an account provisioning,
// so the resolver is not invoked (a second grant per sign-in would be wrong).
func TestOAuthLoginExistingUserDoesNotResolve(t *testing.T) {
	resolver := &fakeResolver{}
	p := &fakeProvider{name: "google", trust: true, providerUserID: "g-linked", email: "linked@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil, withResolver(resolver))

	pre := h.mustOAuthUser(t, "linked@example.com")
	acct, _ := oauthaccount.New(pre.ID, "google", "g-linked", time.Now())
	if _, err := h.accounts.Create(context.Background(), acct); err != nil {
		t.Fatalf("seed link: %v", err)
	}

	state := h.startState(t, "")
	res, err := h.svc.OAuthCallback(context.Background(), "google", "code", state)
	if err != nil {
		t.Fatalf("OAuthCallback: %v", err)
	}
	if res.Action != ActionLogin || res.User.ID != pre.ID {
		t.Fatalf("action=%q user=%q, want login for %q", res.Action, res.User.ID, pre.ID)
	}
	if len(resolver.calls) != 0 {
		t.Fatalf("existing-user OAuth login resolved invitations: %+v", resolver.calls)
	}

	// A second sign-in is still not a provisioning event.
	state = h.startState(t, "")
	if _, err := h.svc.OAuthCallback(context.Background(), "google", "code", state); err != nil {
		t.Fatalf("second OAuthCallback: %v", err)
	}
	if len(resolver.calls) != 0 {
		t.Fatalf("repeat OAuth login resolved invitations: %+v", resolver.calls)
	}
}

// TestOAuthPendingLinkDoesNotResolve proves branch 2 never resolves either: the
// address already belongs to a registered account, so an invitation for it was
// direct-added at create time. Neither starting the pending link nor completing it
// through VerifyLink provisions an account, so neither calls the resolver.
func TestOAuthPendingLinkDoesNotResolve(t *testing.T) {
	resolver := &fakeResolver{}
	p := &fakeProvider{name: "google", trust: true, providerUserID: "g-pending", email: "pending@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil, withResolver(resolver))

	pre := h.mustOAuthUser(t, "pending@example.com") // registered, not linked

	state := h.startState(t, "")
	res, err := h.svc.OAuthCallback(context.Background(), "google", "code", state)
	if err != nil {
		t.Fatalf("OAuthCallback: %v", err)
	}
	if res.Action != ActionPendingLink {
		t.Fatalf("Action = %q, want pending_link", res.Action)
	}
	if len(resolver.calls) != 0 {
		t.Fatalf("pending-link start resolved invitations: %+v", resolver.calls)
	}

	token := h.states.pendingLinkToken()
	if token == "" {
		t.Fatal("no pending-link state was stored")
	}
	linked, err := h.svc.VerifyLink(context.Background(), token)
	if err != nil {
		t.Fatalf("VerifyLink: %v", err)
	}
	if linked.User.ID != pre.ID {
		t.Errorf("VerifyLink user = %q, want the pre-existing %q", linked.User.ID, pre.ID)
	}
	if len(resolver.calls) != 0 {
		t.Fatalf("pending-link completion resolved invitations: %+v", resolver.calls)
	}
}

// TestOAuthRegisterResolveErrorDoesNotFailProvisioning proves the best-effort
// contract on the OAuth side: a failing resolver never fails account provisioning
// or the OAuth login — the user, the link, and the session all still exist.
func TestOAuthRegisterResolveErrorDoesNotFailProvisioning(t *testing.T) {
	resolver := &fakeResolver{err: errors.New("grant boom")}
	p := &fakeProvider{name: "google", trust: true, providerUserID: "g-boom", email: "boom@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil, withResolver(resolver))

	state := h.startState(t, "")
	res, err := h.svc.OAuthCallback(context.Background(), "google", "code", state)
	if err != nil {
		t.Fatalf("OAuthCallback with a failing resolver: %v", err)
	}
	if res.Action != ActionRegister || res.Token == "" || res.RefreshToken == "" {
		t.Fatalf("failing resolver degraded provisioning: %+v", res)
	}
	if _, err := h.accounts.GetByProvider(context.Background(), "google", "g-boom"); err != nil {
		t.Errorf("link not persisted after a resolver failure: %v", err)
	}
	if len(resolver.calls) != 1 {
		t.Errorf("resolver call count = %d, want 1", len(resolver.calls))
	}
}

// TestOAuthRegisterNilResolverIsNoop proves invitations-off (a nil resolver) never
// panics and never affects OAuth provisioning.
func TestOAuthRegisterNilResolverIsNoop(t *testing.T) {
	p := &fakeProvider{name: "google", trust: true, providerUserID: "g-noresolver", email: "noresolver@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil, withResolver(nil))

	state := h.startState(t, "")
	res, err := h.svc.OAuthCallback(context.Background(), "google", "code", state)
	if err != nil {
		t.Fatalf("OAuthCallback with a nil resolver: %v", err)
	}
	if res.Action != ActionRegister {
		t.Fatalf("Action = %q, want register", res.Action)
	}
}
