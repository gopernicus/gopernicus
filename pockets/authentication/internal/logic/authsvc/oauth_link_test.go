package authsvc

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authentication/domain/oauthstate"
	"github.com/gopernicus/gopernicus/sdk"
)

// CHAU-7.1 — characterization for the EXISTING session-gated account-linking
// flow. The coordination-hub upstream flag claimed the capability was missing; it
// has shipped since pockets/authentication/v0.1.0. These tests pin the contract
// so the flow cannot regress into the shape the flag described, and they document
// what the server-side state actually binds.
//
// The exported-surface counterpart — the full settings-page recipe over real
// HTTP — lives in examples/auth-cms/cmd/server/oauth_link_settings_test.go.

// The harness fakes below are shared across this package's tests; these local,
// lock-respecting accessors keep this file from widening their API.

func peekOAuthState(f *fakeOAuthStates, token string) (oauthstate.State, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.m[token]
	return s, ok
}

func sessionCount(f *fakeSessions) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.m)
}

func userCount(f *fakeUsers) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.byID)
}

// TestLinkStartStateBindsFlowFacts pins that everything the callback needs lives
// in the SERVER-SIDE state row, not in the URL the browser carries. The linking
// user in particular is bound at start: that is what makes the callback immune to
// query-parameter steering.
func TestLinkStartStateBindsFlowFacts(t *testing.T) {
	ctx := context.Background()
	p := &fakeProvider{name: "google", oidc: true, trust: true, providerUserID: "g-bind", email: "bind@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil)
	u := h.mustOAuthUser(t, "bind@example.com")

	authURL, err := h.svc.StartLink(ctx, u.ID, "google", "https://app.example.com/welcome")
	if err != nil {
		t.Fatalf("StartLink: %v", err)
	}

	stored, ok := peekOAuthState(h.states, h.provider.lastState)
	if !ok {
		t.Fatal("StartLink persisted no server-side state")
	}
	if stored.Provider != "google" {
		t.Errorf("state.Provider = %q, want google", stored.Provider)
	}
	if stored.Purpose != oauthstate.PurposeFlow {
		t.Errorf("state.Purpose = %q, want %q", stored.Purpose, oauthstate.PurposeFlow)
	}

	var fs flowState
	if err := json.Unmarshal(stored.Payload, &fs); err != nil {
		t.Fatalf("decode flow state: %v", err)
	}
	if fs.LinkUserID != u.ID {
		t.Errorf("state.LinkUserID = %q, want the linking user %q", fs.LinkUserID, u.ID)
	}
	if fs.CodeVerifier == "" {
		t.Error("state carries no PKCE verifier")
	}
	if fs.Nonce == "" {
		t.Error("an OIDC link start carries no nonce")
	}
	if fs.RedirectTo != "https://app.example.com/welcome" {
		t.Errorf("state.RedirectTo = %q, want the allowlisted destination", fs.RedirectTo)
	}

	// None of those facts ride the browser's URL: only the opaque state token does.
	for _, secret := range []string{fs.CodeVerifier, fs.Nonce, fs.LinkUserID, fs.RedirectTo} {
		if secret != "" && strings.Contains(authURL, secret) {
			t.Errorf("authorization URL %q leaks a server-side flow fact %q", authURL, secret)
		}
	}
}

// TestLinkStartRejectsUnknownProvider pins that linking is available only for a
// WIRED provider (deny-by-absence, matching StartOAuth).
func TestLinkStartRejectsUnknownProvider(t *testing.T) {
	p := &fakeProvider{name: "google", trust: true, providerUserID: "g-1", email: "a@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil)
	u := h.mustOAuthUser(t, "a@example.com")

	if _, err := h.svc.StartLink(context.Background(), u.ID, "not-wired", ""); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("StartLink(unwired) = %v, want sdk.ErrNotFound", err)
	}
}

// TestLinkRedirectFallsBackToSameOrigin pins the open-redirect guard on the link
// lane specifically: an off-allowlist destination is replaced at START, so the
// callback can only ever redirect somewhere the host approved.
func TestLinkRedirectFallsBackToSameOrigin(t *testing.T) {
	ctx := context.Background()
	p := &fakeProvider{name: "google", trust: true, providerUserID: "g-redir", email: "redir@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil)
	u := h.mustOAuthUser(t, "redir@example.com")

	if _, err := h.svc.StartLink(ctx, u.ID, "google", "https://evil.example/steal"); err != nil {
		t.Fatalf("StartLink: %v", err)
	}
	res, err := h.svc.OAuthCallback(ctx, "google", "code", h.provider.lastState)
	if err != nil {
		t.Fatalf("OAuthCallback: %v", err)
	}
	if res.Action != ActionLinked {
		t.Fatalf("Action = %q, want linked", res.Action)
	}
	if res.RedirectTo == "https://evil.example/steal" {
		t.Fatal("an off-allowlist destination survived the redirect guard")
	}
}

// TestExplicitLinkMintsNoSession pins the behavior a settings page depends on:
// an explicit link for an ALREADY signed-in user must not mint or replace their
// session. Only login/register callbacks carry a token pair.
func TestExplicitLinkMintsNoSession(t *testing.T) {
	ctx := context.Background()
	p := &fakeProvider{name: "google", trust: true, providerUserID: "g-nosession", email: "nosession@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil)
	u := h.mustOAuthUser(t, "nosession@example.com")

	before := sessionCount(h.sess)

	if _, err := h.svc.StartLink(ctx, u.ID, "google", ""); err != nil {
		t.Fatalf("StartLink: %v", err)
	}
	res, err := h.svc.OAuthCallback(ctx, "google", "code", h.provider.lastState)
	if err != nil {
		t.Fatalf("OAuthCallback: %v", err)
	}

	if res.Action != ActionLinked {
		t.Fatalf("Action = %q, want linked", res.Action)
	}
	if res.Token != "" || res.RefreshToken != "" {
		t.Errorf("explicit link returned a token pair (%q/%q); it must not mint a session", res.Token, res.RefreshToken)
	}
	if got := sessionCount(h.sess); got != before {
		t.Errorf("session count = %d, want %d unchanged", got, before)
	}
	if res.User.ID != u.ID {
		t.Errorf("linked user = %q, want %q", res.User.ID, u.ID)
	}
}

// TestExplicitLinkCreatesNoSecondUser pins that the link lane never falls through
// to the register branch even when the provider's email matches no identifier.
func TestExplicitLinkCreatesNoSecondUser(t *testing.T) {
	ctx := context.Background()
	p := &fakeProvider{name: "google", trust: true, providerUserID: "g-other", email: "someone-else@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil)
	u := h.mustOAuthUser(t, "account-owner@example.com")

	before := userCount(h.users)

	if _, err := h.svc.StartLink(ctx, u.ID, "google", ""); err != nil {
		t.Fatalf("StartLink: %v", err)
	}
	res, err := h.svc.OAuthCallback(ctx, "google", "code", h.provider.lastState)
	if err != nil {
		t.Fatalf("OAuthCallback: %v", err)
	}
	if res.Action != ActionLinked {
		t.Fatalf("Action = %q, want linked", res.Action)
	}
	if got := userCount(h.users); got != before {
		t.Errorf("user count = %d, want %d — the link lane must never register", got, before)
	}
	linked, err := h.accounts.GetByProvider(ctx, "google", "g-other")
	if err != nil || linked.UserID != u.ID {
		t.Errorf("link landed on %+v (err=%v), want the linking user %q", linked, err, u.ID)
	}
}

// TestLinkCallbackTargetsStateOwnerNotCaller is the service-level half of the
// cross-user proof: the linking user is read from the consumed state, so nothing
// the callback receives can retarget it.
func TestLinkCallbackTargetsStateOwnerNotCaller(t *testing.T) {
	ctx := context.Background()
	p := &fakeProvider{name: "google", trust: true, providerUserID: "g-owner", email: "owner@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil)
	owner := h.mustOAuthUser(t, "owner@example.com")
	other := h.mustOAuthUser(t, "other@example.com")

	if _, err := h.svc.StartLink(ctx, owner.ID, "google", ""); err != nil {
		t.Fatalf("StartLink: %v", err)
	}
	if _, err := h.svc.OAuthCallback(ctx, "google", "code", h.provider.lastState); err != nil {
		t.Fatalf("OAuthCallback: %v", err)
	}

	linked, err := h.accounts.GetByProvider(ctx, "google", "g-owner")
	if err != nil {
		t.Fatalf("GetByProvider: %v", err)
	}
	if linked.UserID != owner.ID {
		t.Fatalf("link landed on %q, want the state owner %q", linked.UserID, owner.ID)
	}
	if linked.UserID == other.ID {
		t.Fatal("link landed on the wrong user")
	}
}

// TestLinkConflictDoesNotMoveIdentity pins that a provider account already owned
// by one user is not transferred by a second user's explicit link.
func TestLinkConflictDoesNotMoveIdentity(t *testing.T) {
	ctx := context.Background()
	p := &fakeProvider{name: "google", trust: true, providerUserID: "g-shared", email: "shared@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil)
	first := h.mustOAuthUser(t, "first@example.com")
	second := h.mustOAuthUser(t, "second@example.com")

	if _, err := h.svc.StartLink(ctx, first.ID, "google", ""); err != nil {
		t.Fatalf("StartLink(first): %v", err)
	}
	if _, err := h.svc.OAuthCallback(ctx, "google", "code", h.provider.lastState); err != nil {
		t.Fatalf("OAuthCallback(first): %v", err)
	}

	if _, err := h.svc.StartLink(ctx, second.ID, "google", ""); err != nil {
		t.Fatalf("StartLink(second): %v", err)
	}
	_, err := h.svc.OAuthCallback(ctx, "google", "code", h.provider.lastState)
	if err == nil {
		t.Fatal("a conflicting link succeeded; the identity must not be claimable twice")
	}

	linked, err := h.accounts.GetByProvider(ctx, "google", "g-shared")
	if err != nil {
		t.Fatalf("GetByProvider after conflict: %v", err)
	}
	if linked.UserID != first.ID {
		t.Errorf("identity moved to %q; it must stay with the original owner %q", linked.UserID, first.ID)
	}
}

// TestLinkStateIsSingleUse pins that a link state cannot be replayed.
func TestLinkStateIsSingleUse(t *testing.T) {
	ctx := context.Background()
	p := &fakeProvider{name: "google", trust: true, providerUserID: "g-replay", email: "replay@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil)
	u := h.mustOAuthUser(t, "replay@example.com")

	if _, err := h.svc.StartLink(ctx, u.ID, "google", ""); err != nil {
		t.Fatalf("StartLink: %v", err)
	}
	state := h.provider.lastState

	if _, err := h.svc.OAuthCallback(ctx, "google", "code", state); err != nil {
		t.Fatalf("first callback: %v", err)
	}
	if _, err := h.svc.OAuthCallback(ctx, "google", "code", state); err == nil {
		t.Fatal("a replayed link state was accepted; states must be single-use")
	}
}

// TestLinkedProviderIsImmediatelyListed pins that a completed explicit link is
// visible to the caller with no intermediate confirmation step, through the same
// service read the inventory route is built on.
//
// This asserts ListLinked rather than Methods: this package's shared
// fakeCredentialMutations.Snapshot reports only password state, so a Methods
// assertion here would test the fixture, not the pocket. The end-to-end proof
// that GET /auth/methods shows the provider against REAL repositories is
// examples/auth-cms/cmd/server/oauth_link_settings_test.go's
// TestOAuthExplicitLinkFromSettingsPage.
func TestLinkedProviderIsImmediatelyListed(t *testing.T) {
	ctx := context.Background()
	p := &fakeProvider{name: "google", trust: true, providerUserID: "g-inv", email: "inv@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil)
	u := h.mustOAuthUser(t, "inv@example.com")

	before, err := h.svc.ListLinked(ctx, u.ID)
	if err != nil {
		t.Fatalf("ListLinked (before): %v", err)
	}
	if len(before) != 0 {
		t.Fatalf("user already has %d links", len(before))
	}

	if _, err := h.svc.StartLink(ctx, u.ID, "google", ""); err != nil {
		t.Fatalf("StartLink: %v", err)
	}
	if _, err := h.svc.OAuthCallback(ctx, "google", "code", h.provider.lastState); err != nil {
		t.Fatalf("OAuthCallback: %v", err)
	}

	after, err := h.svc.ListLinked(ctx, u.ID)
	if err != nil {
		t.Fatalf("ListLinked (after): %v", err)
	}
	found := false
	for _, o := range after {
		if o.Provider == "google" && o.ProviderUserID == "g-inv" {
			found = true
		}
	}
	if !found {
		t.Errorf("the freshly linked provider is not listed: %+v", after)
	}
}
