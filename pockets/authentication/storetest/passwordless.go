package storetest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/passwordless"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/user"
	"github.com/gopernicus/gopernicus/sdk"
)

// The atomic magic-link redemption conformance cases (CHAU-6.2 / CHAU-6.3).
//
// This is the adversarial suite, not a happy-path check. Provisioning turns a
// magic link into an account-creation credential, so what has to be proven is
// mostly what must NOT happen: no duplicate subject when the address gains an
// owner mid-flight, no surviving squatter credential after an adoption, no second
// session from a replayed or concurrent redeem, no partial write behind a
// rejection, and no branch-specific error a prober could read.

// linkTTL is the magic-link challenge lifetime the fixtures use.
const linkTTL = 15 * time.Minute

// runPasswordless registers the redemption conformance group.
func runPasswordless(t *testing.T, newRepos func(t *testing.T) auth.Repositories) {
	t.Helper()

	t.Run("PasswordlessRedeem", func(t *testing.T) {
		if newRepos(t).Passwordless == nil {
			t.Skip("Passwordless not wired — atomic magic-link redemption conformance NOT verified for this Repositories")
		}
		t.Run("ProvisionsWhenAddressUnknown", func(t *testing.T) { testRedeemProvision(t, newRepos(t)) })
		t.Run("LoginsWhenAddressVerified", func(t *testing.T) { testRedeemLogin(t, newRepos(t)) })
		t.Run("AdoptsUnverifiedAndRevokesSquatter", func(t *testing.T) { testRedeemAdopt(t, newRepos(t)) })
		t.Run("RegisteredBetweenSendAndConsumeLoginsCurrentOwner", func(t *testing.T) { testRedeemRaceRegistered(t, newRepos(t)) })
		t.Run("WithoutCapturedIntentNeverProvisions", func(t *testing.T) { testRedeemNoIntent(t, newRepos(t)) })
		t.Run("UnknownVersionRejected", func(t *testing.T) { testRedeemUnknownVersion(t, newRepos(t)) })
		t.Run("ReplayRejected", func(t *testing.T) { testRedeemReplay(t, newRepos(t)) })
		t.Run("ExpiredRejected", func(t *testing.T) { testRedeemExpired(t, newRepos(t)) })
		t.Run("UnknownTokenRejected", func(t *testing.T) { testRedeemUnknownToken(t, newRepos(t)) })
		t.Run("DeactivatedUserRejected", func(t *testing.T) { testRedeemDeactivated(t, newRepos(t)) })
		t.Run("LoginDisabledIdentifierRejected", func(t *testing.T) { testRedeemLoginDisabled(t, newRepos(t)) })
		t.Run("ConcurrentRedeemsCommitExactlyOne", func(t *testing.T) { testRedeemConcurrent(t, newRepos(t)) })
	})
}

// seedLinkChallenge writes a magic-link challenge carrying binding, and returns
// the token digest that redeems it.
func seedLinkChallenge(t *testing.T, repos auth.Repositories, digest string, binding passwordless.Binding, expiresAt time.Time) {
	t.Helper()
	blob, err := json.Marshal(binding)
	if err != nil {
		t.Fatalf("marshal binding: %v", err)
	}
	_, err = repos.Challenges.Replace(context.Background(), challenge.Challenge{
		// The subject key is what an unknown address is unique under; it is a
		// PII-free stand-in here, exactly as the service derives one.
		SubjectKey:   "subject:" + digest,
		UserID:       binding.UserID,
		Purpose:      challenge.PurposeLoginMagicLink,
		SecretDigest: digest,
		Context:      blob,
		ExpiresAt:    expiresAt,
		CreatedAt:    suiteBase,
	})
	if err != nil {
		t.Fatalf("Challenges.Replace: %v", err)
	}
}

// redeemInput builds a well-formed input for a token digest.
func redeemInput(digest, refreshHash string, now time.Time) passwordless.RedeemInput {
	sess, _ := session.NewSession("", time.Hour, now)
	sess.RefreshTokenHash = refreshHash
	sess.Authentication = session.AuthenticationMetadata{
		AuthenticatedAt: now,
		Methods:         []session.AuthenticationMethod{{Kind: session.MethodEmailLink, Assurance: session.AssuranceAAL1}},
		Assurance:       session.AssuranceAAL1,
	}
	return passwordless.RedeemInput{
		Purpose:     challenge.PurposeLoginMagicLink,
		TokenDigest: digest,
		Session:     sess,
		NewUser:     user.NewUser(dbIDs, "", now),
		NewIdentifier: identifier.Identifier{
			Kind:                identifier.KindEmail,
			LoginEnabled:        true,
			RecoveryEnabled:     true,
			NotificationEnabled: true,
			IsPrimary:           true,
		},
		AdoptedIdentifierUses:   identifier.Uses{Login: true, Recovery: true, Notification: true},
		RevokeChallengePurposes: []string{challenge.PurposePasswordReset, challenge.PurposeLoginOTP},
		Now:                     now,
	}
}

func provisioningBinding(value string) passwordless.Binding {
	return passwordless.Binding{
		Version:           passwordless.BindingVersion,
		Kind:              string(identifier.KindEmail),
		NormalizedValue:   value,
		ProvisionIfAbsent: true,
	}
}

// testRedeemProvision is the headline case: an address nobody owns becomes an
// account, verified, with a session — all in one commit.
func testRedeemProvision(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	const addr = "provision-me@example.com"
	const digest = "digest-provision"

	seedLinkChallenge(t, repos, digest, provisioningBinding(addr), time.Now().Add(linkTTL))

	now := time.Now()
	res, err := repos.Passwordless.Redeem(ctx, redeemInput(digest, "refresh-provision", now))
	if err != nil {
		t.Fatalf("Redeem: %v", err)
	}
	if res.Outcome != passwordless.OutcomeProvisionNew || !res.Provisioned {
		t.Fatalf("outcome = %q provisioned=%v, want provision_new/true", res.Outcome, res.Provisioned)
	}
	if res.User.ID == "" {
		t.Fatal("no user was created")
	}
	if res.User.DisplayName != "" {
		t.Errorf("display name = %q, want empty — the pocket invents no name", res.User.DisplayName)
	}
	if !res.User.Active() {
		t.Error("the provisioned user is not active")
	}

	// The identifier is VERIFIED: clicking the link proved the address.
	stored, err := repos.Identifiers.GetLogin(ctx, string(identifier.KindEmail), addr)
	if err != nil {
		t.Fatalf("the provisioned identifier is not login-resolvable: %v", err)
	}
	if !stored.Verified() {
		t.Error("the provisioned identifier is unverified; consuming the link IS the proof")
	}
	if stored.UserID != res.User.ID || !stored.IsPrimary {
		t.Errorf("identifier = %+v, want the new user's primary", stored)
	}

	// The session committed with the user.
	got, err := repos.Sessions.Get(ctx, res.Session.ID)
	if err != nil || got.UserID != res.User.ID {
		t.Errorf("session not committed for the new user: %+v err=%v", got, err)
	}
}

// seedVerifiedOwner creates a user owning a verified primary email.
func seedVerifiedOwner(t *testing.T, repos auth.Repositories, addr string) user.User {
	t.Helper()
	u, _ := seedUserWithIdentifier(t, repos, addr, addr, identifier.KindEmail, loginRecoveryUses, true, suiteBase)
	return u
}

// seedUnverifiedRegistration creates the SQUATTER shape: an account whose primary
// email is login/recovery-enabled but NEVER PROVEN.
//
// It must go through identifier.NewRegistrationEmail rather than identifier.New,
// because the domain refuses to build a login-enabled identifier without a proof
// time — the pending registration email is the single documented exception, and
// it is exactly the state this case is about.
func seedUnverifiedRegistration(t *testing.T, repos auth.Repositories, addr string) (user.User, identifier.Identifier) {
	t.Helper()
	u := user.NewUser(dbIDs, "Squatter", suiteBase)
	ident, err := identifier.NewRegistrationEmail(dbIDs, idNorm, "", addr, suiteBase)
	if err != nil {
		t.Fatalf("NewRegistrationEmail(%q): %v", addr, err)
	}
	cu, ci, err := repos.Users.CreateWithPrimaryIdentifier(context.Background(), u, ident)
	if err != nil {
		t.Fatalf("CreateWithPrimaryIdentifier: %v", err)
	}
	return cu, ci
}

func testRedeemLogin(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	const addr = "known-owner@example.com"
	const digest = "digest-login"

	owner := seedVerifiedOwner(t, repos, addr)
	binding := provisioningBinding(addr)
	binding.UserID = owner.ID
	seedLinkChallenge(t, repos, digest, binding, time.Now().Add(linkTTL))

	now := time.Now()
	res, err := repos.Passwordless.Redeem(ctx, redeemInput(digest, "refresh-login", now))
	if err != nil {
		t.Fatalf("Redeem: %v", err)
	}
	if res.Outcome != passwordless.OutcomeLoginExistingVerified || res.Provisioned {
		t.Fatalf("outcome = %q provisioned=%v, want login_existing_verified/false", res.Outcome, res.Provisioned)
	}
	if res.User.ID != owner.ID {
		t.Errorf("logged in %q, want the owner %q", res.User.ID, owner.ID)
	}
}

// testRedeemAdopt is the anti-takeover case. An UNVERIFIED claim means someone
// registered the address without proving it; clicking the link proves it, so the
// claim is adopted — and every credential that predates the proof must be gone
// BEFORE the new session exists.
func testRedeemAdopt(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	const addr = "squatted@example.com"
	const digest = "digest-adopt"

	// A squatter registers the address (unverified) and sets a password + session.
	squatter, ident := seedUnverifiedRegistration(t, repos, addr)
	if ident.Verified() {
		t.Fatal("the fixture identifier is verified; this case needs an unverified claim")
	}
	if err := repos.Passwords.Set(ctx, squatter.ID, "hash:squatter"); err != nil {
		t.Fatalf("Passwords.Set: %v", err)
	}
	squatterSession := newSession(squatter.ID, "squatter-refresh", time.Hour, time.Now())
	if _, err := repos.Sessions.Create(ctx, squatterSession); err != nil {
		t.Fatalf("Sessions.Create: %v", err)
	}
	if repos.AuthenticationGrants != nil {
		g := newGrant(squatterSession.ID, squatter.ID, "set_password", "ctx", time.Hour, time.Now())
		if _, err := repos.AuthenticationGrants.Create(ctx, g); err != nil {
			t.Fatalf("grants.Create: %v", err)
		}
	}
	// A pending reset the squatter could still redeem.
	if _, err := repos.Challenges.Replace(ctx, challenge.Challenge{
		UserID: squatter.ID, Purpose: challenge.PurposePasswordReset,
		SecretDigest: "squatter-reset-digest", ExpiresAt: suiteBase.Add(time.Hour), CreatedAt: suiteBase,
	}); err != nil {
		t.Fatalf("seed reset challenge: %v", err)
	}

	binding := provisioningBinding(addr)
	binding.UserID = squatter.ID
	binding.IdentifierID = ident.ID
	seedLinkChallenge(t, repos, digest, binding, time.Now().Add(linkTTL))

	now := time.Now()
	res, err := repos.Passwordless.Redeem(ctx, redeemInput(digest, "refresh-adopt", now))
	if err != nil {
		t.Fatalf("Redeem: %v", err)
	}
	if res.Outcome != passwordless.OutcomeVerifyAndAdoptExistingUnverified || res.Provisioned {
		t.Fatalf("outcome = %q provisioned=%v, want verify_and_adopt/false", res.Outcome, res.Provisioned)
	}

	// The squatter's password is gone.
	if _, err := repos.Passwords.Get(ctx, squatter.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("the squatter password survived adoption: err=%v", err)
	}
	// Their session is gone …
	if _, err := repos.Sessions.Get(ctx, squatterSession.ID); !errors.Is(err, sdk.ErrNotFound) && !errors.Is(err, sdk.ErrExpired) {
		t.Errorf("the squatter session survived adoption: err=%v", err)
	}
	// … while the NEW session exists.
	if got, err := repos.Sessions.Get(ctx, res.Session.ID); err != nil || got.UserID != squatter.ID {
		t.Errorf("the adopting session is missing: %+v err=%v", got, err)
	}
	// Their grant is gone.
	if repos.AuthenticationGrants != nil {
		if _, err := repos.AuthenticationGrants.Consume(ctx, squatterSession.ID, "set_password", "ctx", time.Now()); !errors.Is(err, sdk.ErrNotFound) {
			t.Errorf("the squatter grant survived adoption: err=%v", err)
		}
	}
	// Their pending reset is gone.
	if _, err := repos.Challenges.ConsumeToken(ctx, challenge.PurposePasswordReset, "squatter-reset-digest", time.Now()); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("the squatter's pending reset survived adoption: err=%v", err)
	}
	// The identifier is now verified.
	stored, err := repos.Identifiers.GetLogin(ctx, string(identifier.KindEmail), addr)
	if err != nil || !stored.Verified() {
		t.Errorf("the adopted identifier is not verified: %+v err=%v", stored, err)
	}
	// The revision moved, invalidating any in-flight credential mutation.
	after, err := repos.Users.Get(ctx, squatter.ID)
	if err != nil {
		t.Fatalf("Users.Get: %v", err)
	}
	if after.AuthRevision <= squatter.AuthRevision {
		t.Errorf("auth_revision = %d, want greater than %d", after.AuthRevision, squatter.AuthRevision)
	}
}

// testRedeemRaceRegistered is the send-then-register race: the link was issued
// for an unknown address that gained a VERIFIED owner before it was clicked. The
// now-current owner must be logged in — never a second, duplicate subject.
func testRedeemRaceRegistered(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	const addr = "raced@example.com"
	const digest = "digest-raced"

	// The link was issued when nobody owned the address …
	seedLinkChallenge(t, repos, digest, provisioningBinding(addr), time.Now().Add(linkTTL))
	// … and then someone registered and verified it.
	owner := seedVerifiedOwner(t, repos, addr)

	now := time.Now()
	res, err := repos.Passwordless.Redeem(ctx, redeemInput(digest, "refresh-raced", now))
	if err != nil {
		t.Fatalf("Redeem: %v", err)
	}
	if res.Outcome != passwordless.OutcomeLoginExistingVerified {
		t.Fatalf("outcome = %q, want login_existing_verified — the current owner wins", res.Outcome)
	}
	if res.User.ID != owner.ID {
		t.Errorf("logged in %q, want the now-current owner %q", res.User.ID, owner.ID)
	}
	if res.Provisioned {
		t.Error("a duplicate user was provisioned for an address that gained an owner")
	}
}

// testRedeemNoIntent proves a link issued WITHOUT the provisioning intent never
// provisions, whatever the current configuration says.
func testRedeemNoIntent(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	const digest = "digest-no-intent"

	binding := provisioningBinding("never-provisioned@example.com")
	binding.ProvisionIfAbsent = false // a login-only link
	seedLinkChallenge(t, repos, digest, binding, time.Now().Add(linkTTL))

	_, err := repos.Passwordless.Redeem(ctx, redeemInput(digest, "refresh-no-intent", time.Now()))
	if !errors.Is(err, passwordless.ErrRedemption) {
		t.Fatalf("err = %v, want ErrRedemption", err)
	}
	if _, err := repos.Identifiers.GetLogin(ctx, string(identifier.KindEmail), "never-provisioned@example.com"); !errors.Is(err, sdk.ErrNotFound) {
		t.Error("a link with no captured intent provisioned an account anyway")
	}
}

func testRedeemUnknownVersion(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	const digest = "digest-bad-version"

	binding := provisioningBinding("future@example.com")
	binding.Version = passwordless.BindingVersion + 99
	seedLinkChallenge(t, repos, digest, binding, time.Now().Add(linkTTL))

	if _, err := repos.Passwordless.Redeem(ctx, redeemInput(digest, "refresh-bad-version", time.Now())); !errors.Is(err, passwordless.ErrRedemption) {
		t.Fatalf("err = %v, want ErrRedemption — an unknown binding version must never be guessed at", err)
	}
	if _, err := repos.Identifiers.GetLogin(ctx, string(identifier.KindEmail), "future@example.com"); !errors.Is(err, sdk.ErrNotFound) {
		t.Error("an unknown-version binding provisioned an account")
	}
}

func testRedeemReplay(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	const addr = "replayed@example.com"
	const digest = "digest-replay"

	seedLinkChallenge(t, repos, digest, provisioningBinding(addr), time.Now().Add(linkTTL))

	first, err := repos.Passwordless.Redeem(ctx, redeemInput(digest, "refresh-replay-1", time.Now()))
	if err != nil {
		t.Fatalf("first redeem: %v", err)
	}
	second, err := repos.Passwordless.Redeem(ctx, redeemInput(digest, "refresh-replay-2", time.Now()))
	if !errors.Is(err, passwordless.ErrRedemption) {
		t.Fatalf("replayed redeem = %+v err=%v, want ErrRedemption", second, err)
	}
	if second.User.ID != "" {
		t.Error("a replayed redemption produced a user")
	}
	_ = first
}

func testRedeemExpired(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	const digest = "digest-expired"
	seedLinkChallenge(t, repos, digest, provisioningBinding("expired@example.com"), time.Now().Add(-time.Minute))

	if _, err := repos.Passwordless.Redeem(ctx, redeemInput(digest, "refresh-expired", time.Now())); !errors.Is(err, passwordless.ErrRedemption) {
		t.Fatalf("err = %v, want ErrRedemption", err)
	}
}

func testRedeemUnknownToken(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	for _, digest := range []string{"", "no-such-digest"} {
		if _, err := repos.Passwordless.Redeem(ctx, redeemInput(digest, "refresh-unknown-"+digest, time.Now())); !errors.Is(err, passwordless.ErrRedemption) {
			t.Errorf("digest %q: err = %v, want ErrRedemption", digest, err)
		}
	}
}

func testRedeemDeactivated(t *testing.T, repos auth.Repositories) {
	if repos.UserAdmin == nil {
		t.Skip("UserAdmin not wired — the deactivated-subject case NOT verified")
	}
	ctx := context.Background()
	const addr = "deactivated-link@example.com"
	const digest = "digest-deactivated"

	owner := seedVerifiedOwner(t, repos, addr)
	if _, err := repos.UserAdmin.SetStatus(ctx, owner.ID, user.StatusDeactivated, suiteBase.Add(time.Hour)); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	binding := provisioningBinding(addr)
	binding.UserID = owner.ID
	seedLinkChallenge(t, repos, digest, binding, time.Now().Add(linkTTL))

	if _, err := repos.Passwordless.Redeem(ctx, redeemInput(digest, "refresh-deactivated", time.Now())); !errors.Is(err, passwordless.ErrRedemption) {
		t.Fatalf("err = %v, want ErrRedemption for a deactivated subject", err)
	}
}

func testRedeemLoginDisabled(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	const addr = "recovery-only@example.com"
	const digest = "digest-login-disabled"

	// A verified RECOVERY-only identifier: it holds the authentication claim but
	// cannot log in.
	owner, _ := seedUserWithIdentifier(t, repos, addr, addr, identifier.KindEmail,
		identifier.Uses{Recovery: true, Notification: true}, true, suiteBase)

	binding := provisioningBinding(addr)
	binding.UserID = owner.ID
	seedLinkChallenge(t, repos, digest, binding, time.Now().Add(linkTTL))

	if _, err := repos.Passwordless.Redeem(ctx, redeemInput(digest, "refresh-login-disabled", time.Now())); !errors.Is(err, passwordless.ErrRedemption) {
		t.Fatalf("err = %v, want ErrRedemption for a login-disabled identifier", err)
	}
}

// testRedeemConcurrent is the case the whole atomic port exists for: two
// simultaneous redemptions of ONE link must produce exactly one committed session
// and exactly one subject.
//
// Run under -race, and against a live database: a mutex-based reference passes
// trivially where a SQL store needs its guarded delete to serialize.
func testRedeemConcurrent(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()

	const rounds = 8
	for round := range rounds {
		addr := fmt.Sprintf("concurrent-%d@example.com", round)
		digest := fmt.Sprintf("digest-concurrent-%d", round)
		seedLinkChallenge(t, repos, digest, provisioningBinding(addr), time.Now().Add(linkTTL))

		var (
			wg      sync.WaitGroup
			results [2]passwordless.RedeemResult
			errs    [2]error
		)
		start := make(chan struct{})
		for i := range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				results[i], errs[i] = repos.Passwordless.Redeem(ctx,
					redeemInput(digest, fmt.Sprintf("refresh-concurrent-%d-%d", round, i), time.Now()))
			}()
		}
		close(start)
		wg.Wait()

		wins := 0
		for i := range 2 {
			switch {
			case errs[i] == nil:
				wins++
			case errors.Is(errs[i], passwordless.ErrRedemption):
			default:
				t.Fatalf("round %d: unexpected error: %v", round, errs[i])
			}
		}
		if wins != 1 {
			t.Fatalf("round %d: %d redemptions committed, want exactly 1", round, wins)
		}

		// Exactly one subject owns the address, and exactly one session exists.
		ident, err := repos.Identifiers.GetLogin(ctx, string(identifier.KindEmail), addr)
		if err != nil {
			t.Fatalf("round %d: the winning redemption left no identifier: %v", round, err)
		}
		for i := range 2 {
			if errs[i] != nil {
				continue
			}
			if results[i].User.ID != ident.UserID {
				t.Fatalf("round %d: the committed session belongs to %q but the address is owned by %q",
					round, results[i].User.ID, ident.UserID)
			}
			if _, err := repos.Sessions.Get(ctx, results[i].Session.ID); err != nil {
				t.Fatalf("round %d: the winning session is missing: %v", round, err)
			}
		}
	}
}
