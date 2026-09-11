package authentication

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauthaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passwordreset"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
)

func wireCredentialFakes(users *fakeUsers, pw *fakePasswords, sess *fakeSessions, accounts *fakeOAuthAccounts, grants *fakeAuthGrants, ch *fakeChallenges) {
	users.pw = pw
	users.sess = sess
	users.accounts = accounts
	users.grants = grants
	users.challenges = ch
	pw.users = users
	accounts.users = users
	if grants != nil {
		grants.users = users
		grants.sessions = sess
	}
}
func (f *fakeUsers) Provision(ctx context.Context, u user.User, ident identifier.Identifier, credentials user.InitialCredentials) (user.User, identifier.Identifier, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.idents != nil && (ident.LoginEnabled || ident.RecoveryEnabled) && f.idents.authClaimTaken(ident) {
		return user.User{}, identifier.Identifier{}, sdk.ErrAlreadyExists
	}
	if _, ok := f.byID[u.ID]; ok {
		return user.User{}, identifier.Identifier{}, sdk.ErrAlreadyExists
	}
	if credentials.OAuth != nil {
		f.accounts.mu.Lock()
		defer f.accounts.mu.Unlock()
		for _, a := range f.accounts.m {
			if a.Provider == credentials.OAuth.Provider && a.ProviderUserID == credentials.OAuth.ProviderUserID {
				return user.User{}, identifier.Identifier{}, sdk.ErrAlreadyExists
			}
		}
	}
	f.byID[u.ID] = u
	ident.UserID = u.ID
	if f.idents != nil {
		ident = f.idents.insert(ident)
	}
	if credentials.PasswordHash != "" {
		f.pw.mu.Lock()
		f.pw.m[u.ID] = credentials.PasswordHash
		f.pw.mu.Unlock()
	}
	if credentials.OAuth != nil {
		a := *credentials.OAuth
		a.UserID = u.ID
		f.accounts.m = append(f.accounts.m, a)
	}
	return u, ident, nil
}
func (f *fakeUsers) revokeCredentialStateLocked(userID string) {
	if f.sess != nil {
		_ = f.sess.DeleteByUser(context.Background(), userID)
	}
	if f.grants != nil {
		f.grants.mu.Lock()
		for id, g := range f.grants.m {
			if g.UserID == userID {
				delete(f.grants.m, id)
			}
		}
		f.grants.mu.Unlock()
	}
	if f.challenges != nil {
		f.challenges.purgeUserPurposes(userID, []string{challenge.PurposePasswordReset})
	}
}
func (f *fakePasswords) Change(ctx context.Context, userID string, change user.PasswordChange) (int64, error) {
	f.users.mu.Lock()
	defer f.users.mu.Unlock()
	return f.changeLocked(userID, change, true)
}
func (f *fakePasswords) changeLocked(userID string, change user.PasswordChange, conditional bool) (int64, error) {
	u, ok := f.users.byID[userID]
	if !ok {
		return 0, sdk.ErrNotFound
	}
	if !u.Active() {
		return 0, session.ErrUserNotActive
	}
	f.mu.Lock()
	if conditional && (u.AuthRevision != change.ExpectedAuthRevision || f.m[userID] != change.ExpectedHash) {
		f.mu.Unlock()
		return 0, sdk.ErrConflict
	}
	f.m[userID] = change.NewHash
	f.mu.Unlock()
	u.AuthRevision++
	u.UpdatedAt = change.Now
	f.users.byID[userID] = u
	f.users.revokeCredentialStateLocked(userID)
	return u.AuthRevision, nil
}

type fakeActiveSessions struct {
	users    *fakeUsers
	sessions *fakeSessions
}

func (f fakeActiveSessions) CreateForActiveUser(ctx context.Context, s session.Session, expectedAuthRevision int64) (session.Session, error) {
	f.users.mu.Lock()
	defer f.users.mu.Unlock()
	u, ok := f.users.byID[s.UserID]
	if !ok {
		return session.Session{}, sdk.ErrNotFound
	}
	if !u.Active() {
		return session.Session{}, session.ErrUserNotActive
	}
	if u.AuthRevision != expectedAuthRevision {
		return session.Session{}, sdk.ErrConflict
	}
	return f.sessions.Create(ctx, s)
}
func (f *fakeOAuthAccounts) Link(ctx context.Context, a oauthaccount.OAuthAccount, expectedAuthRevision int64, adoptIdentifierID string, now time.Time) (oauthaccount.OAuthAccount, int64, error) {
	f.users.mu.Lock()
	defer f.users.mu.Unlock()
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users.byID[a.UserID]
	if !ok {
		return oauthaccount.OAuthAccount{}, 0, sdk.ErrNotFound
	}
	if !u.Active() {
		return oauthaccount.OAuthAccount{}, 0, session.ErrUserNotActive
	}
	if u.AuthRevision != expectedAuthRevision {
		return oauthaccount.OAuthAccount{}, 0, sdk.ErrConflict
	}
	for _, ex := range f.m {
		if ex.Provider == a.Provider && ex.ProviderUserID == a.ProviderUserID {
			return oauthaccount.OAuthAccount{}, 0, sdk.ErrAlreadyExists
		}
	}
	if adoptIdentifierID != "" {
		f.users.idents.mu.Lock()
		ident, ok := f.users.idents.byID[adoptIdentifierID]
		if !ok || ident.UserID != u.ID || !ident.Active() || (!ident.LoginEnabled && !ident.RecoveryEnabled) || ident.Kind != identifier.KindEmail {
			f.users.idents.mu.Unlock()
			return oauthaccount.OAuthAccount{}, 0, sdk.ErrConflict
		}
		ident.VerifiedAt = now
		ident.UpdatedAt = now
		f.users.idents.byID[ident.ID] = ident
		f.users.idents.mu.Unlock()
		f.users.pw.delete(u.ID)
		f.users.revokeCredentialStateLocked(u.ID)
	}
	f.m = append(f.m, a)
	u.AuthRevision++
	f.users.byID[u.ID] = u
	return a, u.AuthRevision, nil
}

func (h *harness) currentAuthRevision(userID string) int64 {
	u, _ := h.users.Get(context.Background(), userID)
	return u.AuthRevision
}

func newServiceWithFakes(d constructorConfig) *Service {
	users, ok := d.Users.(*fakeUsers)
	if !ok {
		return newService(d)
	}
	pw, _ := d.Passwords.(*fakePasswords)
	if pw == nil {
		pw = users.pw
	}
	sess, _ := d.Sessions.(*fakeSessions)
	if sess == nil {
		sess = users.sess
	}
	accounts, _ := d.OAuthAccounts.(*fakeOAuthAccounts)
	if accounts == nil {
		accounts = users.accounts
	}
	if accounts == nil {
		accounts = newFakeOAuthAccounts()
	}
	grants, _ := d.AuthenticationGrants.(*fakeAuthGrants)
	ch, _ := d.Challenges.(*fakeChallenges)
	if pw != nil && sess != nil {
		wireCredentialFakes(users, pw, sess, accounts, grants, ch)
		if d.ActiveSessions == nil {
			d.ActiveSessions = fakeActiveSessions{users, sess}
		}
	}
	return newService(d)
}

// Blocking after verification reproduces the credential read/admission window,
// rather than depending on scheduler timing or simply deleting old rows.
type pausedPasswordProof struct {
	base    Hasher
	once    sync.Once
	entered chan struct{}
	resume  chan struct{}
}

func (h *pausedPasswordProof) HashPassword(value string) (string, error) {
	return h.base.HashPassword(value)
}
func (h *pausedPasswordProof) VerifyPassword(hash, value string) error {
	if err := h.base.VerifyPassword(hash, value); err != nil {
		return err
	}
	block := false
	h.once.Do(func() { block = true; close(h.entered) })
	if block {
		<-h.resume
	}
	return nil
}
func TestOldPasswordProofCannotAdmitSessionAfterMutation(t *testing.T) {
	for _, operation := range []string{"change", "reset"} {
		for _, flow := range []string{"login", "token"} {
			t.Run(operation+"/"+flow, func(t *testing.T) {
				h := newHarness(t, nil)
				ctx := context.Background()
				uid, email, oldSession := h.mustVerifiedLogin(t, "fenced@example.com", "oldpassword123456")
				gate := &pausedPasswordProof{base: h.hasher, entered: make(chan struct{}), resume: make(chan struct{})}
				h.svc.hasher = gate
				var release sync.Once
				unblock := func() { release.Do(func() { close(gate.resume) }) }
				t.Cleanup(unblock)
				type result struct {
					pair TokenPair
					err  error
				}
				done := make(chan result, 1)
				go func() {
					var pair TokenPair
					var err error
					if flow == "login" {
						pair, _, err = h.svc.Login(ctx, email, "oldpassword123456")
					} else {
						pair, err = h.svc.IssueToken(ctx, email, "oldpassword123456")
					}
					done <- result{pair, err}
				}()
				select {
				case <-gate.entered:
				case <-time.After(3 * time.Second):
					t.Fatal("password proof was not reached")
				}
				if operation == "change" {
					if _, err := h.svc.ChangePassword(ctx, uid, "oldpassword123456", "newpassword123456"); err != nil {
						t.Fatal(err)
					}
				} else {
					owner, err := h.users.Get(ctx, uid)
					if err != nil {
						t.Fatal(err)
					}
					recovery, err := h.idents.GetRecovery(ctx, "email", email)
					if err != nil {
						t.Fatal(err)
					}
					token, err := h.svc.issueChallengeSecret(ctx, uid, challenge.PurposePasswordReset, withStoredContext(passwordreset.Binding{Version: passwordreset.BindingVersion, AuthRevision: owner.AuthRevision, IdentifierID: recovery.ID}))
					if err != nil {
						t.Fatal(err)
					}
					if err := h.svc.ResetPassword(ctx, token, "newpassword123456"); err != nil {
						t.Fatal(err)
					}
				}
				unblock()
				select {
				case got := <-done:
					if !errors.Is(got.err, sdk.ErrConflict) {
						t.Fatalf("old proof error=%v, want revision conflict", got.err)
					}
					if got.pair.AccessToken != "" || got.pair.RefreshToken != "" {
						t.Fatal("old proof returned credentials")
					}
				case <-time.After(3 * time.Second):
					t.Fatal("login remained blocked")
				}
				if _, err := h.sess.Get(ctx, oldSession); !errors.Is(err, sdk.ErrNotFound) {
					t.Fatalf("pre-mutation session survived: %v", err)
				}
				expectedSessions := 0
				if operation == "change" {
					expectedSessions = 1
				}
				if h.sess.count() != expectedSessions {
					t.Fatalf("session count=%d want%d", h.sess.count(), expectedSessions)
				}
			})
		}
	}
}

func TestRemovalStartRequiresMutationStoreBeforeIssuance(t *testing.T) {
	for _, operation := range []string{"password", "oauth"} {
		t.Run(operation, func(t *testing.T) {
			h := newHarness(t, nil)
			ctx := context.Background()
			h.seedUnlinkUser(t, "no-removal-store", "unwired@example.com", true, true, "google")
			h.svc.credentialMutations = nil
			limiter := &failedAuthenticationLimiter{}
			h.svc.limiter = limiter
			before, sent := h.ch.countRows(), h.mailer.count()
			var err error
			if operation == "password" {
				_, err = h.svc.StartRemovePassword(ctx, "no-removal-store")
			} else {
				_, err = h.svc.StartUnlinkOAuth(ctx, "no-removal-store", "google")
			}
			if !errors.Is(err, ErrCredentialMutationUnavailable) {
				t.Fatalf("start: %v", err)
			}
			if limiter.calls != 0 || h.ch.countRows() != before || h.mailer.count() != sent {
				t.Fatal("unavailable removal consumed budget or issued/delivered code")
			}
		})
	}
}

type afterCredentialCodeConsume struct {
	challenge.Repository
	after func()
}

func (r afterCredentialCodeConsume) ConsumeCode(ctx context.Context, subject, purpose string, candidates []challenge.DigestCandidate, expected string, attempts int, now time.Time) (challenge.Consumed, challenge.ConsumeOutcome, error) {
	consumed, outcome, err := r.Repository.ConsumeCode(ctx, subject, purpose, candidates, expected, attempts, now)
	if err == nil && outcome == challenge.OutcomeRedeemed {
		r.after()
	}
	return consumed, outcome, err
}

func TestRegistrationVerificationDoesNotRebaseConsumedProof(t *testing.T) {
	for _, mutation := range []string{"password", "identifier"} {
		t.Run(mutation, func(t *testing.T) {
			h := newHarness(t, nil)
			ctx := context.Background()
			email := "registration-race@example.com"
			owner := h.mustRegister(t, email, "password123456789")
			code := h.mailer.codeFor(t, email)
			h.svc.challenges = afterCredentialCodeConsume{Repository: h.ch, after: func() {
				if mutation == "password" {
					if err := h.pw.Set(ctx, owner.ID, "hash:newpassword"); err != nil {
						t.Fatal(err)
					}
					return
				}
				current, err := h.users.Get(ctx, owner.ID)
				if err != nil {
					t.Fatal(err)
				}
				_, err = h.idents.ApplyVerifiedChange(ctx, identifier.ApplyVerifiedChangeInput{UserID: owner.ID, Kind: identifier.KindEmail, NormalizedValue: "replacement@example.com", LoginEnabled: true, RecoveryEnabled: true, MakePrimary: true}, current.AuthRevision, time.Now())
				if err != nil {
					t.Fatal(err)
				}
			}}
			if err := h.svc.Verify(ctx, email, code); !errors.Is(err, ErrRegistrationVerificationConflict) {
				t.Fatalf("verification rebased proof: %v", err)
			}
			if mutation == "password" {
				ident, err := h.idents.GetLogin(ctx, "email", email)
				if err != nil || ident.Verified() {
					t.Fatalf("stale proof verified identifier: %v", err)
				}
			} else if _, err := h.idents.GetLogin(ctx, "email", email); !errors.Is(err, sdk.ErrNotFound) {
				t.Fatalf("retired identifier reactivated: %v", err)
			}
		})
	}
}

type afterCredentialTokenConsume struct {
	challenge.Repository
	after func()
}

func (r afterCredentialTokenConsume) ConsumeToken(ctx context.Context, purpose, digest string, now time.Time) (challenge.Consumed, error) {
	consumed, err := r.Repository.ConsumeToken(ctx, purpose, digest, now)
	if err == nil {
		r.after()
	}
	return consumed, err
}

func TestLegacyMagicLinkKeepsIssuingCredentialRevision(t *testing.T) {
	for _, timing := range []string{"before-consume", "after-consume"} {
		t.Run(timing, func(t *testing.T) {
			h := newHarness(t, nil)
			ctx := context.Background()
			addr := "legacy-fence@example.com"
			owner := h.mustRegister(t, addr, "password123456789")
			h.mustVerify(t, addr)
			enablePasswordless(h, "email")
			token := startPasswordlessLink(t, h, "email", addr)
			mutate := func() {
				if err := h.pw.Set(ctx, owner.ID, "hash:replacement"); err != nil {
					t.Fatal(err)
				}
			}
			if timing == "before-consume" {
				mutate()
			} else {
				h.svc.challenges = afterCredentialTokenConsume{Repository: h.ch, after: mutate}
			}
			pair, err := h.svc.RedeemPasswordless(ctx, token)
			if !errors.Is(err, ErrPasswordlessLogin) || pair.AccessToken != "" || pair.RefreshToken != "" {
				t.Fatalf("stale magic-link proof admitted: %+v %v", pair, err)
			}
			if h.sess.count() != 0 {
				t.Fatal("stale magic-link proof persisted session")
			}
		})
	}
}

func TestRegistrationVerificationPreservesCurrentIdentifierUses(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	addr := "registration-uses@example.com"
	h.mustRegister(t, addr, "password123456789")
	code := h.mailer.codeFor(t, addr)
	current, err := h.idents.GetLogin(ctx, "email", addr)
	if err != nil {
		t.Fatal(err)
	}
	h.idents.mu.Lock()
	current.RecoveryEnabled = false
	current.NotificationEnabled = false
	current.IsPrimary = false
	h.idents.byID[current.ID] = current
	h.idents.mu.Unlock()
	if err := h.svc.Verify(ctx, addr, code); err != nil {
		t.Fatal(err)
	}
	verified, err := h.idents.GetLogin(ctx, "email", addr)
	if err != nil || !verified.Verified() || verified.RecoveryEnabled || verified.NotificationEnabled || verified.IsPrimary {
		t.Fatalf("verification restored host-disabled uses: %+v %v", verified, err)
	}
}

func TestPasswordlessCodeKeepsIssuingCredentialRevision(t *testing.T) {
	for _, timing := range []string{"before-consume", "after-consume"} {
		t.Run(timing, func(t *testing.T) {
			h := newHarness(t, nil)
			ctx := context.Background()
			addr := "otp-fence@example.com"
			owner := h.mustRegister(t, addr, "password123456789")
			h.mustVerify(t, addr)
			enablePasswordless(h, "email")
			if err := h.svc.StartPasswordless(ctx, "email", addr, "code"); err != nil {
				t.Fatal(err)
			}
			code := extractCode(t, h.mailer.last().Text)
			mutate := func() {
				if err := h.pw.Set(ctx, owner.ID, "hash:replacement"); err != nil {
					t.Fatal(err)
				}
			}
			expected := ErrPasswordlessLogin
			if timing == "before-consume" {
				mutate()
			} else {
				h.svc.challenges = afterCredentialCodeConsume{Repository: h.ch, after: mutate}
				expected = sdk.ErrConflict
			}
			pair, err := h.svc.VerifyPasswordless(ctx, "email", addr, code)
			if !errors.Is(err, expected) || pair.AccessToken != "" || pair.RefreshToken != "" {
				t.Fatalf("stale passwordless code admitted: %+v %v", pair, err)
			}
			if h.sess.count() != 0 {
				t.Fatal("stale passwordless code persisted session")
			}
		})
	}
}
