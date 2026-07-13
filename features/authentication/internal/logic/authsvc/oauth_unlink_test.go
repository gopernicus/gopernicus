package authsvc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/credential"
	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/features/authentication/domain/oauthaccount"
	"github.com/gopernicus/gopernicus/features/authentication/domain/securityevent"
	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
	"github.com/gopernicus/gopernicus/sdk"
)

// seedUnlinkUser inserts a user with an optional password, the named provider links,
// and a single verified primary email identifier whose recovery/login roles are set
// as requested. It returns the normalized email the unlink_oauth code is delivered
// to. It drives the provider-bound OAuth unlink suite (design §5.4).
func (h *harness) seedUnlinkUser(t *testing.T, userID, email string, hasPassword, loginEmail bool, providers ...string) string {
	t.Helper()
	now := time.Now().UTC()
	h.users.mu.Lock()
	h.users.byID[userID] = user.User{ID: userID, DisplayName: "Unlink"}
	h.users.mu.Unlock()
	if hasPassword {
		if err := h.pw.Set(context.Background(), userID, "hash:password123456789"); err != nil {
			t.Fatalf("seed password: %v", err)
		}
	}
	h.idents.insert(identifier.Identifier{
		ID: "id-" + userID, UserID: userID, Kind: identifier.KindEmail, NormalizedValue: email,
		VerifiedAt: now, LoginEnabled: loginEmail, RecoveryEnabled: true, NotificationEnabled: true,
		IsPrimary: true, CreatedAt: now, UpdatedAt: now,
	})
	for _, p := range providers {
		acct, err := oauthaccount.New(userID, p, p+"-uid-"+userID, now)
		if err != nil {
			t.Fatalf("new oauth account: %v", err)
		}
		if _, err := h.accounts.Create(context.Background(), acct); err != nil {
			t.Fatalf("seed oauth link: %v", err)
		}
	}
	return email
}

func (h *harness) linkCount(t *testing.T, userID string) int {
	t.Helper()
	links, err := h.accounts.ListByUser(context.Background(), userID)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	return len(links)
}

func TestOAuthUnlinkSuccess(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	const userID = "u-unlink"
	email := h.seedUnlinkUser(t, userID, "unlink@example.com", true, true, "google")

	receipt, err := h.svc.StartUnlinkOAuth(ctx, userID, "google")
	if err != nil {
		t.Fatalf("StartUnlinkOAuth: %v", err)
	}
	if !receipt.Delivered {
		t.Fatal("StartUnlinkOAuth reported no delivery")
	}
	if !hasEvent(h.events, securityevent.TypeOAuthUnlinkCodeSent, securityevent.StatusSuccess) {
		t.Fatal("no oauth_unlink_code_sent event")
	}
	code := h.mailer.codeFor(t, email)

	if err := h.svc.UnlinkOAuth(ctx, userID, "google", code); err != nil {
		t.Fatalf("UnlinkOAuth: %v", err)
	}
	if h.linkCount(t, userID) != 0 {
		t.Fatalf("google link survived unlink: %d", h.linkCount(t, userID))
	}
	if !hasEvent(h.events, securityevent.TypeOAuthUnlinked, securityevent.StatusSuccess) {
		t.Fatal("no oauth_unlinked event")
	}
}

// TestOAuthUnlinkWrongProvider proves the code binds the exact provider: a code
// minted to unlink Google cannot unlink GitHub, the wrong-provider attempt CONSUMES
// the code, and a later correct-provider retry with the spent code fails (design §5.4).
func TestOAuthUnlinkWrongProvider(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	const userID = "u-wrong"
	email := h.seedUnlinkUser(t, userID, "wrong@example.com", true, true, "google", "github")

	if _, err := h.svc.StartUnlinkOAuth(ctx, userID, "google"); err != nil {
		t.Fatalf("StartUnlinkOAuth(google): %v", err)
	}
	googleCode := h.mailer.codeFor(t, email)

	// A Google code cannot unlink GitHub: the context mismatch consumes the code.
	if err := h.svc.UnlinkOAuth(ctx, userID, "github", googleCode); !errors.Is(err, ErrChallengeInvalid) {
		t.Fatalf("unlink github with google code = %v, want ErrChallengeInvalid", err)
	}
	if h.linkCount(t, userID) != 2 {
		t.Fatalf("wrong-provider unlink removed a link: %d links remain", h.linkCount(t, userID))
	}
	// The code was spent by the wrong-provider attempt: the correct provider now fails.
	if err := h.svc.UnlinkOAuth(ctx, userID, "google", googleCode); !errors.Is(err, ErrChallengeInvalid) {
		t.Fatalf("reused consumed code = %v, want ErrChallengeInvalid", err)
	}
	if h.linkCount(t, userID) != 2 {
		t.Fatalf("google link removed by a spent code: %d links remain", h.linkCount(t, userID))
	}
}

func TestOAuthUnlinkUnknownProvider(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	const userID = "u-unknown"
	h.seedUnlinkUser(t, userID, "unknown@example.com", true, true, "google")

	if _, err := h.svc.StartUnlinkOAuth(ctx, userID, "facebook"); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("StartUnlinkOAuth(unlinked provider) = %v, want ErrNotFound", err)
	}
	if err := h.svc.UnlinkOAuth(ctx, userID, "facebook", "123456"); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("UnlinkOAuth(unlinked provider) = %v, want ErrNotFound", err)
	}
}

// TestOAuthUnlinkLastAcceptableMethod proves the credential policy guards the unlink:
// removing the only login method (an OAuth-only account, recovery-only email) is
// rejected and the link survives (design §5.4/§5.6).
func TestOAuthUnlinkLastAcceptableMethod(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	const userID = "u-lastmethod"
	// No password, recovery-only email (not login-enabled), single google link.
	email := h.seedUnlinkUser(t, userID, "last@example.com", false, false, "google")

	if _, err := h.svc.StartUnlinkOAuth(ctx, userID, "google"); err != nil {
		t.Fatalf("StartUnlinkOAuth: %v", err)
	}
	code := h.mailer.codeFor(t, email)

	if err := h.svc.UnlinkOAuth(ctx, userID, "google", code); !errors.Is(err, credential.ErrNoLoginMethod) {
		t.Fatalf("unlink last login method = %v, want credential.ErrNoLoginMethod", err)
	}
	if h.linkCount(t, userID) != 1 {
		t.Fatalf("link removed despite policy rejection: %d", h.linkCount(t, userID))
	}
}

// TestOAuthUnlinkStaleRevisionReevaluatesPolicy proves a concurrent password removal
// between the service's Snapshot and its revision-CAS Apply forces a re-evaluation:
// the stale (safe-looking) unlink cannot commit, and the reload rejects it because
// the password that was the surviving login method is now gone (design §5.4/§5.6).
func TestOAuthUnlinkStaleRevisionReevaluatesPolicy(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	const userID = "u-stale"
	// Password + recovery-only email + one google link. At the initial snapshot,
	// removing google is safe (the password still logs in).
	email := h.seedUnlinkUser(t, userID, "stale@example.com", true, false, "google")

	if _, err := h.svc.StartUnlinkOAuth(ctx, userID, "google"); err != nil {
		t.Fatalf("StartUnlinkOAuth: %v", err)
	}
	code := h.mailer.codeFor(t, email)

	// A competing removal of the password commits first: the CAS conflicts, and the
	// retry reloads a set where removing google leaves no login method.
	h.creds.beforeApply = func() {
		u, _ := h.users.Get(ctx, userID)
		h.pw.delete(userID)
		_ = h.users.applyRevision(userID, u.AuthRevision)
	}
	if err := h.svc.UnlinkOAuth(ctx, userID, "google", code); !errors.Is(err, credential.ErrNoLoginMethod) {
		t.Fatalf("stale unlink = %v, want credential.ErrNoLoginMethod after re-evaluation", err)
	}
	if h.linkCount(t, userID) != 1 {
		t.Fatalf("google link removed despite re-evaluated policy rejection: %d", h.linkCount(t, userID))
	}
}

// TestOAuthUnlinkReplay proves a completed unlink cannot be replayed: the second
// attempt with the spent code finds the link already gone (design §5.4).
func TestOAuthUnlinkReplay(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	const userID = "u-replay"
	email := h.seedUnlinkUser(t, userID, "replay@example.com", true, true, "google")

	if _, err := h.svc.StartUnlinkOAuth(ctx, userID, "google"); err != nil {
		t.Fatalf("StartUnlinkOAuth: %v", err)
	}
	code := h.mailer.codeFor(t, email)
	if err := h.svc.UnlinkOAuth(ctx, userID, "google", code); err != nil {
		t.Fatalf("UnlinkOAuth: %v", err)
	}
	if err := h.svc.UnlinkOAuth(ctx, userID, "google", code); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("replayed unlink = %v, want ErrNotFound (link already gone)", err)
	}
}

// TestOAuthUnlinkFailsClosedWithoutRail proves the unlink fails closed when the
// revision-serialized credential-mutation rail is unwired, never bypassing it.
func TestOAuthUnlinkFailsClosedWithoutRail(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	const userID = "u-norail"
	h.seedUnlinkUser(t, userID, "norail@example.com", true, true, "google")
	h.svc.credentialMutations = nil

	if err := h.svc.UnlinkOAuth(ctx, userID, "google", "123456"); !errors.Is(err, ErrCredentialMutationUnavailable) {
		t.Fatalf("UnlinkOAuth without rail = %v, want ErrCredentialMutationUnavailable", err)
	}
	if h.linkCount(t, userID) != 1 {
		t.Fatalf("link removed with the rail unwired: %d", h.linkCount(t, userID))
	}
}
