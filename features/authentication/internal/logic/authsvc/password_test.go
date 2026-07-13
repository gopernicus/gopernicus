package authsvc

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/authgrant"
	"github.com/gopernicus/gopernicus/features/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/features/authentication/domain/credential"
	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/features/authentication/domain/securityevent"
	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
	"github.com/gopernicus/gopernicus/sdk"
)

var _ credential.MutationRepository = (*fakeCredentials)(nil)

// fakeCredentials is the credential.MutationRepository the password suite drives:
// Snapshot projects the typed MethodSet from passwords + identifiers + the owning
// user's auth_revision, and Apply performs the typed mutation atomically under the
// revision-CAS. beforeApply is a one-shot concurrency hook fired before the CAS so a
// test can commit a competing mutation between the service's Snapshot and its Apply.
type fakeCredentials struct {
	users    *fakeUsers
	pw       *fakePasswords
	idents   *fakeIdentifiers
	accounts *fakeOAuthAccounts

	mu          sync.Mutex
	beforeApply func()
}

func (f *fakeCredentials) Snapshot(ctx context.Context, userID string) (credential.MethodSet, error) {
	u, err := f.users.Get(ctx, userID)
	if err != nil {
		return credential.MethodSet{}, err
	}
	set := credential.MethodSet{AuthRevision: u.AuthRevision}
	if _, err := f.pw.Get(ctx, userID); err == nil {
		set.HasPassword = true
	}
	if f.accounts != nil {
		links, err := f.accounts.ListByUser(ctx, userID)
		if err != nil {
			return credential.MethodSet{}, err
		}
		for _, l := range links {
			set.OAuth = append(set.OAuth, credential.OAuthMethod{Provider: l.Provider})
		}
	}
	idents, err := f.idents.ListByUser(ctx, userID)
	if err != nil {
		return credential.MethodSet{}, err
	}
	for _, it := range idents {
		set.Identifiers = append(set.Identifiers, credential.IdentifierMethod{
			ID:       it.ID,
			Kind:     string(it.Kind),
			Uses:     credential.IdentifierUses{Login: it.LoginEnabled, Recovery: it.RecoveryEnabled, Notification: it.NotificationEnabled},
			Verified: it.Verified(),
			Primary:  it.IsPrimary,
		})
	}
	return set, nil
}

func (f *fakeCredentials) Apply(_ context.Context, userID string, expected int64, m credential.Mutation) error {
	f.mu.Lock()
	hook := f.beforeApply
	f.beforeApply = nil
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	if err := f.users.applyRevision(userID, expected); err != nil {
		return err
	}
	now := time.Now().UTC()
	switch v := m.(type) {
	case credential.RemovePassword:
		f.pw.delete(userID)
	case credential.RetireIdentifier:
		f.idents.mu.Lock()
		if it, ok := f.idents.byID[v.IdentifierID]; ok {
			it.Retire(now)
			f.idents.byID[v.IdentifierID] = it
		}
		if v.ReplacementPrimaryID != "" {
			if it, ok := f.idents.byID[v.ReplacementPrimaryID]; ok {
				it.IsPrimary = true
				f.idents.byID[v.ReplacementPrimaryID] = it
			}
		}
		f.idents.mu.Unlock()
	case credential.ChangeIdentifierUses:
		f.idents.mu.Lock()
		if it, ok := f.idents.byID[v.IdentifierID]; ok {
			it.LoginEnabled = v.Uses.Login
			it.RecoveryEnabled = v.Uses.Recovery
			it.NotificationEnabled = v.Uses.Notification
			if v.MakePrimary {
				it.IsPrimary = true
			}
			f.idents.byID[v.IdentifierID] = it
		}
		f.idents.mu.Unlock()
	case credential.UnlinkOAuth:
		if f.accounts != nil {
			_ = f.accounts.Delete(context.Background(), userID, v.Provider)
		}
	}
	return nil
}

// --- helpers ---

// seedPasswordlessUser inserts an OAuth-style user with no password and a single
// verified login+recovery+notification primary email.
func (h *harness) seedPasswordlessUser(userID, email string) {
	now := time.Now().UTC()
	h.users.mu.Lock()
	h.users.byID[userID] = user.User{ID: userID, DisplayName: "PW"}
	h.users.mu.Unlock()
	h.idents.insert(identifier.Identifier{
		ID: "id-" + userID, UserID: userID, Kind: identifier.KindEmail, NormalizedValue: email,
		VerifiedAt: now, LoginEnabled: true, RecoveryEnabled: true, NotificationEnabled: true,
		IsPrimary: true, CreatedAt: now, UpdatedAt: now,
	})
}

// grantFor persists an unexpired recent-auth grant bound to (session, purpose).
func (h *harness) grantFor(t *testing.T, sessionID, userID, purpose string) {
	t.Helper()
	now := time.Now().UTC()
	if _, err := h.grants.Create(context.Background(), authgrant.Grant{
		SessionID: sessionID, UserID: userID, Purpose: purpose,
		ContextDigest: grantContextDigest(""), ExpiresAt: now.Add(5 * time.Minute), CreatedAt: now,
	}); err != nil {
		t.Fatalf("grant create: %v", err)
	}
}

// --- set password ---

func TestPasswordSetSuccess(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	const userID, sessionID = "u-set", "sess-set"
	h.seedPasswordlessUser(userID, "set@example.com")
	h.grantFor(t, sessionID, userID, authgrant.PurposeSetPassword)

	pair, err := h.svc.SetPassword(ctx, sessionID, userID, "password123456789")
	if err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	if pair.AccessToken == "" {
		t.Fatal("SetPassword returned no fresh pair")
	}
	if _, err := h.pw.Get(ctx, userID); err != nil {
		t.Fatalf("password not stored: %v", err)
	}
	if h.grants.unconsumed() != 0 {
		t.Fatalf("set_password grant not consumed: %d unconsumed", h.grants.unconsumed())
	}
	if !hasEvent(h.events, securityevent.TypePasswordSet, securityevent.StatusSuccess) {
		t.Fatal("no password_set success event")
	}
}

func TestPasswordSetAlreadySet(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	u := h.mustRegister(t, "already@example.com", "password123456789")
	const sessionID = "sess-already"
	h.grantFor(t, sessionID, u.ID, authgrant.PurposeSetPassword)

	_, err := h.svc.SetPassword(ctx, sessionID, u.ID, "differentpass12345")
	if !errors.Is(err, ErrPasswordAlreadySet) {
		t.Fatalf("SetPassword on set account = %v, want ErrPasswordAlreadySet", err)
	}
	// The precondition is checked BEFORE the grant is spent.
	if h.grants.unconsumed() != 1 {
		t.Fatalf("already-set path spent the grant: %d unconsumed", h.grants.unconsumed())
	}
}

func TestPasswordSetRequiresGrant(t *testing.T) {
	h := newHarness(t, nil)
	const userID, sessionID = "u-nogrant", "sess-nogrant"
	h.seedPasswordlessUser(userID, "nogrant@example.com")

	_, err := h.svc.SetPassword(context.Background(), sessionID, userID, "password123456789")
	if !errors.Is(err, ErrStepUpRequired) {
		t.Fatalf("SetPassword without grant = %v, want ErrStepUpRequired", err)
	}
}

func TestPasswordSetShortPasswordRejected(t *testing.T) {
	h := newHarness(t, nil)
	const userID, sessionID = "u-short", "sess-short"
	h.seedPasswordlessUser(userID, "short@example.com")
	h.grantFor(t, sessionID, userID, authgrant.PurposeSetPassword)

	_, err := h.svc.SetPassword(context.Background(), sessionID, userID, "short")
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("short password = %v, want ErrInvalidInput", err)
	}
	if h.grants.unconsumed() != 1 {
		t.Fatalf("policy-rejected set spent the grant: %d unconsumed", h.grants.unconsumed())
	}
}

// --- change password (routed through the shared machinery) ---

func TestPasswordChangeRevokesAndRemints(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	userID, _, _ := h.mustVerifiedLogin(t, "change@example.com", "password123456789")
	if h.sess.count() != 1 {
		t.Fatalf("pre-change sessions = %d, want 1", h.sess.count())
	}
	pair, err := h.svc.ChangePassword(ctx, userID, "password123456789", "newpassword1234567")
	if err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	if pair.AccessToken == "" {
		t.Fatal("ChangePassword returned no fresh pair")
	}
	// All prior sessions revoked, exactly one fresh caller session minted.
	if h.sess.count() != 1 {
		t.Fatalf("post-change sessions = %d, want 1", h.sess.count())
	}
	if _, _, err := h.svc.Login(ctx, "change@example.com", "newpassword1234567"); err != nil {
		t.Fatalf("login with new password failed: %v", err)
	}
}

// --- remove password ---

func TestPasswordRemoveSuccess(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	userID, normEmail, _ := h.mustVerifiedLogin(t, "remove@example.com", "password123456789")

	receipt, err := h.svc.StartRemovePassword(ctx, userID)
	if err != nil {
		t.Fatalf("StartRemovePassword: %v", err)
	}
	if !receipt.Delivered {
		t.Fatal("StartRemovePassword reported no delivery")
	}
	if !hasEvent(h.events, securityevent.TypePasswordRemoveCodeSent, securityevent.StatusSuccess) {
		t.Fatal("no password_remove_code_sent event")
	}
	code := h.mailer.codeFor(t, normEmail)

	if _, err := h.svc.RemovePassword(ctx, userID, code); err != nil {
		t.Fatalf("RemovePassword: %v", err)
	}
	if _, err := h.pw.Get(ctx, userID); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("password not removed: %v", err)
	}
	if h.sess.count() != 1 {
		t.Fatalf("post-remove sessions = %d, want 1 (revoke + remint)", h.sess.count())
	}
	if !hasEvent(h.events, securityevent.TypePasswordRemoved, securityevent.StatusSuccess) {
		t.Fatal("no password_removed event")
	}
}

func TestPasswordRemoveNotSet(t *testing.T) {
	h := newHarness(t, nil)
	const userID = "u-nopass"
	h.seedPasswordlessUser(userID, "nopass@example.com")

	if _, err := h.svc.StartRemovePassword(context.Background(), userID); !errors.Is(err, ErrPasswordNotSet) {
		t.Fatalf("StartRemovePassword no password = %v, want ErrPasswordNotSet", err)
	}
	if _, err := h.svc.RemovePassword(context.Background(), userID, "123456"); !errors.Is(err, ErrPasswordNotSet) {
		t.Fatalf("RemovePassword no password = %v, want ErrPasswordNotSet", err)
	}
}

func TestPasswordRemoveWrongCode(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	userID, _, _ := h.mustVerifiedLogin(t, "wrongcode@example.com", "password123456789")
	if _, err := h.svc.StartRemovePassword(ctx, userID); err != nil {
		t.Fatalf("StartRemovePassword: %v", err)
	}
	_, err := h.svc.RemovePassword(ctx, userID, "000000")
	if !errors.Is(err, ErrChallengeInvalid) {
		t.Fatalf("wrong code = %v, want ErrChallengeInvalid", err)
	}
	if _, err := h.pw.Get(ctx, userID); err != nil {
		t.Fatalf("password removed on wrong code: %v", err)
	}
}

func TestPasswordRemoveLastLoginMethodRejected(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	const userID = "u-last"
	now := time.Now().UTC()
	h.users.mu.Lock()
	h.users.byID[userID] = user.User{ID: userID, DisplayName: "Last"}
	h.users.mu.Unlock()
	h.pw.Set(ctx, userID, "hash:password123456789")
	// A recovery-only verified email: it is a recovery method but NOT a login method,
	// so removing the password would leave no direct login method.
	h.idents.insert(identifier.Identifier{
		ID: "id-last", UserID: userID, Kind: identifier.KindEmail, NormalizedValue: "last@example.com",
		VerifiedAt: now, LoginEnabled: false, RecoveryEnabled: true, NotificationEnabled: true,
		IsPrimary: true, CreatedAt: now, UpdatedAt: now,
	})
	if _, err := h.svc.StartRemovePassword(ctx, userID); err != nil {
		t.Fatalf("StartRemovePassword: %v", err)
	}
	code := h.mailer.codeFor(t, "last@example.com")

	_, err := h.svc.RemovePassword(ctx, userID, code)
	if !errors.Is(err, credential.ErrNoLoginMethod) {
		t.Fatalf("remove last login method = %v, want credential.ErrNoLoginMethod", err)
	}
	if _, err := h.pw.Get(ctx, userID); err != nil {
		t.Fatalf("password removed despite policy rejection: %v", err)
	}
}

func TestPasswordRemoveInvalidatesPendingReset(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	userID, normEmail, _ := h.mustVerifiedLogin(t, "reset@example.com", "password123456789")

	// A live password_reset token exists for the user.
	resetToken, err := h.svc.IssueChallenge(ctx, userID, challenge.PurposePasswordReset)
	if err != nil {
		t.Fatalf("IssueChallenge(reset): %v", err)
	}

	if _, err := h.svc.StartRemovePassword(ctx, userID); err != nil {
		t.Fatalf("StartRemovePassword: %v", err)
	}
	code := h.mailer.codeFor(t, normEmail)
	if _, err := h.svc.RemovePassword(ctx, userID, code); err != nil {
		t.Fatalf("RemovePassword: %v", err)
	}
	// The previously live reset token is no longer redeemable.
	if err := h.svc.ResetPassword(ctx, resetToken, "brandnewpass123456"); !errors.Is(err, ErrPasswordResetInvalid) {
		t.Fatalf("stale reset token still redeemable: %v", err)
	}
}

func TestPasswordRemoveStaleRevisionRetriesAndSucceeds(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	userID, normEmail, _ := h.mustVerifiedLogin(t, "retry@example.com", "password123456789")
	if _, err := h.svc.StartRemovePassword(ctx, userID); err != nil {
		t.Fatalf("StartRemovePassword: %v", err)
	}
	code := h.mailer.codeFor(t, normEmail)

	// A competing mutation bumps auth_revision between the service's Snapshot and its
	// Apply: the first CAS conflicts, and the retry reloads and commits.
	h.creds.beforeApply = func() {
		u, _ := h.users.Get(ctx, userID)
		_ = h.users.applyRevision(userID, u.AuthRevision)
	}
	if _, err := h.svc.RemovePassword(ctx, userID, code); err != nil {
		t.Fatalf("RemovePassword after conflict retry: %v", err)
	}
	if _, err := h.pw.Get(ctx, userID); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("password not removed after retry: %v", err)
	}
}

func TestPasswordRemoveConcurrentRemovalReevaluatesPolicy(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	const userID = "u-race"
	now := time.Now().UTC()
	h.users.mu.Lock()
	h.users.byID[userID] = user.User{ID: userID, DisplayName: "Race"}
	h.users.mu.Unlock()
	h.pw.Set(ctx, userID, "hash:password123456789")
	// Login+recovery email A (primary) and recovery-only email B. At the initial
	// snapshot, removing the password is safe (A still logs in); a competing removal
	// of A commits first, so the retry must re-evaluate and reject the removal.
	h.idents.insert(identifier.Identifier{
		ID: "id-A", UserID: userID, Kind: identifier.KindEmail, NormalizedValue: "a@example.com",
		VerifiedAt: now, LoginEnabled: true, RecoveryEnabled: true, NotificationEnabled: true,
		IsPrimary: true, CreatedAt: now, UpdatedAt: now,
	})
	h.idents.insert(identifier.Identifier{
		ID: "id-B", UserID: userID, Kind: identifier.KindEmail, NormalizedValue: "b@example.com",
		VerifiedAt: now, LoginEnabled: false, RecoveryEnabled: true, NotificationEnabled: true,
		CreatedAt: now, UpdatedAt: now,
	})
	if _, err := h.svc.StartRemovePassword(ctx, userID); err != nil {
		t.Fatalf("StartRemovePassword: %v", err)
	}
	code := h.mailer.codeFor(t, "a@example.com")

	h.creds.beforeApply = func() {
		u, _ := h.users.Get(ctx, userID)
		h.idents.mu.Lock()
		it := h.idents.byID["id-A"]
		it.Retire(time.Now())
		h.idents.byID["id-A"] = it
		h.idents.mu.Unlock()
		_ = h.users.applyRevision(userID, u.AuthRevision)
	}
	_, err := h.svc.RemovePassword(ctx, userID, code)
	if !errors.Is(err, credential.ErrNoLoginMethod) {
		t.Fatalf("concurrent last-login removal = %v, want credential.ErrNoLoginMethod", err)
	}
	if _, err := h.pw.Get(ctx, userID); err != nil {
		t.Fatalf("password removed despite re-evaluated policy rejection: %v", err)
	}
}
