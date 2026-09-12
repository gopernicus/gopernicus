//go:build integration && !live

package firestore

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passwordreset"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
)

// The password-reset cases the conformance suite cannot reach. The suite proves
// the composition's OBSERVABLE effects — the password changed, the sessions and
// grants and challenges are gone. What it cannot see is the CLAIM documents those
// deletions had to release, and a store that deleted every row and stranded every
// claim would pass the whole group while making each freed refresh hash and each
// freed digest permanently unusable.

// resetPurposes mirrors the service's password/reset purge set.
var resetPurposes = []string{challenge.PurposePasswordReset, challenge.PurposeRemovePassword}

// TestRedeemStrandsNoClaims walks every claim the composition is responsible for.
func TestRedeemStrandsNoClaims(t *testing.T) {
	ctx := context.Background()
	r, db := openRepos(t)

	const (
		userID = "u-reset-claims"
		digest = "reset-dig"
	)
	binding := seedResetOwner(t, r, userID, "hash:old")
	sess, err := r.Sessions.Create(ctx, session.Session{
		ID:               "sess-reset-claims",
		UserID:           userID,
		RefreshTokenHash: "refresh-reset-claims",
		CreatedAt:        time.Now(),
		ExpiresAt:        time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if _, err := r.Challenges.Replace(ctx, challengeFixture(userID, challenge.PurposePasswordReset, "", digest, binding, 0, time.Hour, time.Now())); err != nil {
		t.Fatalf("seed reset challenge: %v", err)
	}
	if _, err := r.Challenges.Replace(ctx, challengeFixture(userID, challenge.PurposeRemovePassword, "k1", "remove-dig", nil, 0, time.Hour, time.Now())); err != nil {
		t.Fatalf("seed remove_password challenge: %v", err)
	}

	res, err := r.PasswordResets.Redeem(ctx, passwordreset.RedeemInput{
		Purpose:                challenge.PurposePasswordReset,
		TokenDigest:            digest,
		NewPasswordHash:        "hash:new",
		PurgeChallengePurposes: resetPurposes,
		Now:                    time.Now(),
	})
	if err != nil || res.UserID != userID {
		t.Fatalf("Redeem = %+v err=%v, want %s", res, err, userID)
	}

	// The consumed reset row's digest claim — released by the consuming delete,
	// even though that row is also in the purge set (dropChallenges de-duplicates
	// the two, and two Deletes for one document in one commit would be the bug).
	if claim, ok := digestClaimHolder(t, db, challenge.PurposePasswordReset, digest); ok {
		t.Errorf("the consumed reset token is still claimed: %+v", claim)
	}
	// The purged sibling challenge's digest claim.
	if claim, ok := digestClaimHolder(t, db, challenge.PurposeRemovePassword, "remove-dig"); ok {
		t.Errorf("a purged challenge's digest is still claimed: %+v", claim)
	}
	// The revoked session's refresh-hash claim.
	var refresh refreshHashClaimDoc
	if readClaim(t, db, refreshClaimRef(db, "refresh-reset-claims"), &refresh) {
		t.Errorf("the revoked session's refresh hash is still claimed: %+v", refresh)
	}
	if _, err := r.Sessions.Get(ctx, sess.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Sessions.Get after the reset: err=%v, want ErrNotFound", err)
	}

	// The observable consequence of the releases: both freed secrets are usable
	// again, which is exactly what a stranded claim would forbid forever.
	if _, err := r.Challenges.Replace(ctx, challengeFixture("u-someone-else", challenge.PurposePasswordReset, "", digest, nil, 0, time.Hour, time.Now())); err != nil {
		t.Errorf("re-issuing the freed reset digest: err=%v, want nil", err)
	}
	if _, err := r.Sessions.Create(ctx, session.Session{
		ID:               "sess-reset-claims-2",
		UserID:           userID,
		RefreshTokenHash: "refresh-reset-claims",
		CreatedAt:        time.Now(),
		ExpiresAt:        time.Now().Add(time.Hour),
	}); err != nil {
		t.Errorf("re-minting a session on the freed refresh hash: err=%v, want nil", err)
	}
}

// TestExpiredResetTokenLeavesTheChallengeIntact is the deliberate contrast with
// the consume family beside it (N-D2). ConsumeToken DELETES an expired row before
// reporting sdk.ErrExpired; an expired RESET token is simply not live, and the
// composition writes nothing at all — so the row and its digest claim survive and
// the ordinary purge is what eventually collects them.
//
// Getting this backwards in either direction is a real defect: deleting here
// would turn an anti-probing refusal into a state change, and NOT deleting in
// ConsumeToken would leave a spent token replayable.
func TestExpiredResetTokenLeavesTheChallengeIntact(t *testing.T) {
	ctx := context.Background()
	r, db := openRepos(t)

	const (
		userID = "u-reset-expired"
		digest = "reset-dig-expired"
	)
	binding := seedResetOwner(t, r, userID, "hash:keep")
	if _, err := r.Challenges.Replace(ctx, challengeFixture(userID, challenge.PurposePasswordReset, "", digest, binding, 0, -time.Minute, time.Now())); err != nil {
		t.Fatalf("seed expired reset challenge: %v", err)
	}

	if _, err := r.PasswordResets.Redeem(ctx, passwordreset.RedeemInput{
		Purpose:                challenge.PurposePasswordReset,
		TokenDigest:            digest,
		NewPasswordHash:        "hash:new",
		PurgeChallengePurposes: resetPurposes,
		Now:                    time.Now(),
	}); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("Redeem(expired) err=%v, want ErrNotFound (the single generic failure)", err)
	}

	if got, err := r.Passwords.Get(ctx, userID); err != nil || got != "hash:keep" {
		t.Errorf("password = %q err=%v, want the unchanged hash:keep", got, err)
	}
	if _, found := storedChallenge(t, db, userID, challenge.PurposePasswordReset); !found {
		t.Error("the expired reset challenge was deleted — a not-live token must change nothing")
	}
	if _, ok := digestClaimHolder(t, db, challenge.PurposePasswordReset, digest); !ok {
		t.Error("the expired reset challenge's digest claim was released without its row")
	}

	// PurgeExpired is what collects it, and it releases the claim there.
	n, err := r.Challenges.PurgeExpired(ctx, time.Now(), 0)
	if err != nil || n != 1 {
		t.Fatalf("PurgeExpired = %d err=%v, want 1", n, err)
	}
	if claim, ok := digestClaimHolder(t, db, challenge.PurposePasswordReset, digest); ok {
		t.Errorf("the purged challenge's digest is still claimed: %+v", claim)
	}
}

// seedResetOwner supplies the active, verified recovery owner and proof revision
// required by the audited reset contract.
func seedResetOwner(t *testing.T, r auth.Repositories, userID, hash string) []byte {
	t.Helper()
	owner, ident, err := r.Users.Provision(t.Context(), user.User{ID: userID, Status: user.StatusActive, CreatedAt: testBase, UpdatedAt: testBase}, identifier.Identifier{ID: userID + "-email", Kind: identifier.KindEmail, NormalizedValue: userID + "@example.com", LoginEnabled: true, RecoveryEnabled: true, IsPrimary: true, VerifiedAt: testBase, CreatedAt: testBase, UpdatedAt: testBase}, user.InitialCredentials{PasswordHash: hash})
	if err != nil {
		t.Fatalf("seed reset owner: %v", err)
	}
	binding, err := json.Marshal(passwordreset.Binding{Version: passwordreset.BindingVersion, AuthRevision: owner.AuthRevision, IdentifierID: ident.ID})
	if err != nil {
		t.Fatal(err)
	}
	return binding
}
