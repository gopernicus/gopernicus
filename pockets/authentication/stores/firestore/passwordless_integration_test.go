//go:build integration && !live

package firestore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/authgrant"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passwordless"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
)

// The magic-link redemption's DOCUMENT-level obligations, which the shared
// conformance suite can only see through the ports.
//
// Three of them are invisible from the port surface and each is a way a green
// suite could hide a real defect. The adoption's revocation is asserted on the
// documents AND their claims, because a store that deleted a session row and
// left its refresh claim behind would pass every port assertion while making the
// freed credential permanently unmintable. A stable rejection is asserted to
// leave the token BOTH stored and still claimed, because the port can only see
// that a later redeem fails — which it would also do if the row had been
// consumed. And the rollback case injects a real Aborted mid-attempt, because
// "an infrastructure error rolls back the token consumption itself" is a
// property of the retry loop, not of any single attempt.

// linkPurpose is the magic-link challenge purpose these fixtures redeem.
const linkPurpose = challenge.PurposeLoginMagicLink

// revokedPurposes are the outstanding-secret purposes an adoption revokes — the
// same pair the conformance suite passes.
var revokedPurposes = []string{challenge.PurposePasswordReset, challenge.PurposeLoginOTP}

// linkFixture is one seeded magic link: the challenge that carries the binding
// and the coordinates a test needs to read it back as a DOCUMENT.
type linkFixture struct {
	digest     string
	subjectKey string
}

// seedLink writes a magic-link challenge carrying binding, through the port, so
// the fixture exercises the same write path the service does.
func seedLink(t *testing.T, r auth.Repositories, digest string, binding any, expiresAt time.Time) linkFixture {
	t.Helper()

	var blob []byte
	switch v := binding.(type) {
	case nil:
		blob = nil
	case []byte:
		blob = v
	default:
		encoded, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal binding: %v", err)
		}
		blob = encoded
	}

	fixture := linkFixture{digest: digest, subjectKey: "subject:" + digest}
	if _, err := r.Challenges.Replace(context.Background(), challenge.Challenge{
		SubjectKey:   fixture.subjectKey,
		Purpose:      linkPurpose,
		SecretDigest: digest,
		Context:      blob,
		ExpiresAt:    expiresAt,
		CreatedAt:    testBase,
	}); err != nil {
		t.Fatalf("Challenges.Replace: %v", err)
	}
	return fixture
}

// provisioningBinding is a well-formed current binding that carries the captured
// provisioning intent.
func provisioningBinding(value string) passwordless.Binding {
	return passwordless.Binding{
		Version:           passwordless.BindingVersion,
		Kind:              string(identifier.KindEmail),
		NormalizedValue:   value,
		ProvisionIfAbsent: true,
	}
}

// redeemInput builds a well-formed redemption for a digest.
func redeemInput(digest, refreshHash string, now time.Time) passwordless.RedeemInput {
	sess, _ := session.NewSession("", time.Hour, now)
	sess.RefreshTokenHash = refreshHash
	sess.Authentication = session.AuthenticationMetadata{
		AuthenticatedAt: now,
		Methods:         []session.AuthenticationMethod{{Kind: session.MethodEmailLink, Assurance: session.AssuranceAAL1}},
		Assurance:       session.AssuranceAAL1,
	}
	return passwordless.RedeemInput{
		Purpose:     linkPurpose,
		TokenDigest: digest,
		Session:     sess,
		NewUser:     user.NewUser(dbGenerated, "", now),
		NewIdentifier: identifier.Identifier{
			Kind:                identifier.KindEmail,
			LoginEnabled:        true,
			RecoveryEnabled:     true,
			NotificationEnabled: true,
			IsPrimary:           true,
		},
		AdoptedIdentifierUses:   identifier.Uses{Login: true, Recovery: true, Notification: true},
		RevokeChallengePurposes: revokedPurposes,
		Now:                     now,
	}
}

// tokenIsStored reports whether the link's challenge row AND its digest claim
// are both present — the two documents a consumption removes together.
func tokenIsStored(t *testing.T, db *firestoredb.DB, link linkFixture) (row bool, claim bool) {
	t.Helper()
	ctx := context.Background()
	_, ok, err := readChallenge(ctx, db, db.ReaderFrom(ctx), link.subjectKey, linkPurpose)
	if err != nil {
		t.Fatalf("readChallenge(%q): %v", link.digest, err)
	}
	var held challengeDigestClaimDoc
	return ok, readClaim(t, db, challengeDigestClaimRef(db, linkPurpose, link.digest), &held)
}

// TestAdoptionRevokesEveryDocumentItOwes is AdoptsUnverifiedAndRevokesSquatter
// asserted on the STORE rather than on the ports.
//
// Every line below names a document the port surface cannot see. The refresh
// claim is the sharpest: a store that deleted the squatter's session row and
// forgot its claim would satisfy the conformance suite completely, and the hash
// would stay unmintable forever — so the freed hash is re-claimed here to prove
// the release really happened.
func TestAdoptionRevokesEveryDocumentItOwes(t *testing.T) {
	r, db := openRepos(t)
	ctx := context.Background()

	const addr = "adopted-doc@example.com"
	const squatterRefresh = "squatter-refresh-hash"

	// The squatter shape: a login-enabled primary email that was never proven.
	u := user.NewUser(dbGenerated, "Squatter", testBase)
	ident, err := identifier.NewRegistrationEmail(dbGenerated, normalizer, "", addr, testBase)
	if err != nil {
		t.Fatalf("NewRegistrationEmail: %v", err)
	}
	squatter, squatterIdent, err := r.Users.CreateWithPrimaryIdentifier(ctx, u, ident)
	if err != nil {
		t.Fatalf("CreateWithPrimaryIdentifier: %v", err)
	}
	if err := r.Passwords.Set(ctx, squatter.ID, "hash:squatter"); err != nil {
		t.Fatalf("Passwords.Set: %v", err)
	}
	sess, _ := session.NewSession(squatter.ID, time.Hour, time.Now())
	sess.RefreshTokenHash = squatterRefresh
	if _, err := r.Sessions.Create(ctx, sess); err != nil {
		t.Fatalf("Sessions.Create: %v", err)
	}
	grant := authgrant.Grant{
		SessionID: sess.ID, UserID: squatter.ID, Purpose: "set_password", ContextDigest: "ctx",
		Assurance: session.AssuranceAAL1, AuthenticatedAt: time.Now(), CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
	}
	if _, err := r.AuthenticationGrants.Create(ctx, grant, squatter.AuthRevision+1, time.Now()); err != nil {
		t.Fatalf("grants.Create: %v", err)
	}
	// One outstanding secret per revoked purpose, so both are proven and not
	// just the first.
	for i, purpose := range revokedPurposes {
		if _, err := r.Challenges.Replace(ctx, challenge.Challenge{
			UserID: squatter.ID, Purpose: purpose,
			SecretDigest: fmt.Sprintf("squatter-secret-%d", i),
			ExpiresAt:    time.Now().Add(time.Hour), CreatedAt: testBase,
		}); err != nil {
			t.Fatalf("seed %s challenge: %v", purpose, err)
		}
	}

	binding := provisioningBinding(addr)
	binding.UserID, binding.IdentifierID = squatter.ID, squatterIdent.ID
	revision := revisionOf(t, r, squatter.ID)
	binding.AuthRevision = &revision
	link := seedLink(t, r, "digest-adopt-doc", binding, time.Now().Add(15*time.Minute))

	res, err := r.Passwordless.Redeem(ctx, redeemInput(link.digest, "adopting-refresh", time.Now()))
	if err != nil {
		t.Fatalf("Redeem: %v", err)
	}
	if res.Outcome != passwordless.OutcomeVerifyAndAdoptExistingUnverified {
		t.Fatalf("outcome = %q, want verify_and_adopt", res.Outcome)
	}

	// The token: row and digest claim both gone.
	if row, claim := tokenIsStored(t, db, link); row || claim {
		t.Errorf("the consumed link survived: row=%v claim=%v", row, claim)
	}
	// The password document is gone.
	if _, err := readPassword(ctx, db, db.ReaderFrom(ctx), squatter.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("the squatter password document survived: err=%v", err)
	}
	// Exactly one session remains — the adopting one — and the squatter's
	// refresh CLAIM is released, which the re-claim below proves.
	live, err := readSessionsForUser(ctx, db, db.ReaderFrom(ctx), squatter.ID)
	if err != nil {
		t.Fatalf("readSessionsForUser: %v", err)
	}
	if len(live) != 1 || live[0].ID != res.Session.ID {
		t.Errorf("sessions = %d (%+v), want only the adopting session %q", len(live), live, res.Session.ID)
	}
	var freed refreshHashClaimDoc
	if readClaim(t, db, refreshClaimRef(db, squatterRefresh), &freed) {
		t.Errorf("the squatter's refresh claim survived, holding %q unmintable forever", squatterRefresh)
	}
	reclaimed, _ := session.NewSession(squatter.ID, time.Hour, time.Now())
	reclaimed.RefreshTokenHash = squatterRefresh
	if _, err := r.Sessions.Create(ctx, reclaimed); err != nil {
		t.Errorf("the freed refresh hash is not re-claimable: %v", err)
	}
	// Every grant is gone.
	grants, err := readGrantsForUser(ctx, db, db.ReaderFrom(ctx), squatter.ID)
	if err != nil {
		t.Fatalf("readGrantsForUser: %v", err)
	}
	if len(grants) != 0 {
		t.Errorf("grants = %d, want 0", len(grants))
	}
	// Every named challenge purpose is gone, WITH its digest claim.
	for i, purpose := range revokedPurposes {
		digest := fmt.Sprintf("squatter-secret-%d", i)
		if _, ok, err := readChallenge(ctx, db, db.ReaderFrom(ctx), squatter.ID, purpose); err != nil || ok {
			t.Errorf("the %s challenge survived: ok=%v err=%v", purpose, ok, err)
		}
		var held challengeDigestClaimDoc
		if readClaim(t, db, challengeDigestClaimRef(db, purpose, digest), &held) {
			t.Errorf("the %s digest claim survived", purpose)
		}
	}
	// The identifier row is verified in place, keeping its claims.
	adopted := readRow(t, db, squatterIdent.ID)
	verifiedAt, err := adopted.verifiedAt()
	if err != nil {
		t.Fatalf("verifiedAt: %v", err)
	}
	if verifiedAt.IsZero() {
		t.Error("the adopted identifier is still unverified")
	}
	if owner, ok := authClaimOwner(t, db, string(identifier.KindEmail), addr); !ok || owner.IdentifierID != squatterIdent.ID {
		t.Errorf("authentication claim = %+v ok=%v, want the adopted row", owner, ok)
	}
	// The directory projection follows the proof.
	if address, verified := storedProjection(t, db, squatter.ID); address != addr || !verified {
		t.Errorf("projection = (%q, %v), want (%q, true)", address, verified, addr)
	}
	// The revision advanced exactly once, invalidating any in-flight mutation.
	after, err := readUser(ctx, db, db.ReaderFrom(ctx), squatter.ID)
	if err != nil {
		t.Fatalf("readUser: %v", err)
	}
	if after.AuthRevision != revision+1 {
		t.Errorf("auth_revision = %d, want %d", after.AuthRevision, revision+1)
	}
}

// TestStableRejectionsRetainTheToken walks every shape of stable bad outcome and
// asserts the one thing the ports cannot see: the link is still STORED and still
// CLAIMED, so a rejection burned nothing.
//
// It matters because a redemption that consumed the token first and rejected
// afterwards would look identical through the port — the second redeem fails
// either way — while quietly turning every recoverable refusal into a dead link.
func TestStableRejectionsRetainTheToken(t *testing.T) {
	malformed := []byte("{not json")
	unknownVersion := provisioningBinding("future@example.com")
	unknownVersion.Version = passwordless.BindingVersion + 99
	noIntent := provisioningBinding("never-provisioned@example.com")
	noIntent.ProvisionIfAbsent = false
	blankValue := provisioningBinding("")

	cases := []struct {
		name    string
		binding any
	}{
		{"absent_binding", nil},
		{"malformed_binding", malformed},
		{"unknown_version", unknownVersion},
		{"blank_normalized_value", blankValue},
		{"without_captured_intent", noIntent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, db := openRepos(t)
			ctx := context.Background()

			link := seedLink(t, r, "digest-"+tc.name, tc.binding, time.Now().Add(15*time.Minute))
			if _, err := r.Passwordless.Redeem(ctx, redeemInput(link.digest, "refresh-"+tc.name, time.Now())); !errors.Is(err, passwordless.ErrRedemption) {
				t.Fatalf("err = %v, want ErrRedemption", err)
			}
			row, claim := tokenIsStored(t, db, link)
			if !row || !claim {
				t.Errorf("the rejected link was consumed: row=%v claim=%v — a stable rejection writes NOTHING", row, claim)
			}
		})
	}
}

// A retained known-owner token remains stale after a lifecycle revision changes.
func TestRejectedBoundTokenRemainsStaleAfterReactivation(t *testing.T) {
	r, db := openRepos(t)
	ctx := t.Context()
	const addr = "reactivated@example.com"
	owner, ident := seedUser(t, r, identifier.KindEmail, addr, identifier.Uses{Login: true, Recovery: true}, true, testBase, testBase)
	binding := provisioningBinding(addr)
	binding.UserID, binding.IdentifierID, binding.AuthRevision = owner.ID, ident.ID, &owner.AuthRevision
	link := seedLink(t, r, "digest-reactivated", binding, time.Now().Add(15*time.Minute))
	if _, err := r.UserAdmin.SetStatus(ctx, owner.ID, user.StatusDeactivated, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Passwordless.Redeem(ctx, redeemInput(link.digest, "refresh-refused", time.Now())); !errors.Is(err, passwordless.ErrRedemption) {
		t.Fatalf("deactivated redemption: %v", err)
	}
	if _, err := r.UserAdmin.SetStatus(ctx, owner.ID, user.StatusActive, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Passwordless.Redeem(ctx, redeemInput(link.digest, "refresh-stale", time.Now())); !errors.Is(err, passwordless.ErrRedemption) {
		t.Fatalf("stale redemption after reactivation: %v", err)
	}
	if row, claim := tokenIsStored(t, db, link); !row || !claim {
		t.Fatal("rejected proof was consumed")
	}
	revision := revisionOf(t, r, owner.ID)
	binding.AuthRevision = &revision
	fresh := seedLink(t, r, "digest-fresh-reactivated", binding, time.Now().Add(15*time.Minute))
	result, err := r.Passwordless.Redeem(ctx, redeemInput(fresh.digest, "refresh-accepted", time.Now()))
	if err != nil || result.Outcome != passwordless.OutcomeLoginExistingVerified || result.User.ID != owner.ID {
		t.Fatalf("fresh redemption: outcome=%s err=%v", result.Outcome, err)
	}
}

// TestRedeemRollsBackWholeAndRetriesClean drives the adoption through a real
// injected Aborted: attempt one stages the whole revocation and is aborted, and
// the world must be UNTOUCHED — including the token, whose retention is what
// makes a transient failure recoverable instead of a burned link.
//
// Between the attempts the squatter mints ANOTHER session. The committing
// attempt must revoke that one too, which is only true if the revocation set is
// re-read inside every attempt; an implementation that computed it once outside
// the callback would leave it alive and pass every other assertion here.
func TestRedeemRollsBackWholeAndRetriesClean(t *testing.T) {
	r, db := openRepos(t)
	ctx := context.Background()

	const addr = "rollback-adopt@example.com"

	u := user.NewUser(dbGenerated, "Squatter", testBase)
	ident, err := identifier.NewRegistrationEmail(dbGenerated, normalizer, "", addr, testBase)
	if err != nil {
		t.Fatalf("NewRegistrationEmail: %v", err)
	}
	squatter, squatterIdent, err := r.Users.CreateWithPrimaryIdentifier(ctx, u, ident)
	if err != nil {
		t.Fatalf("CreateWithPrimaryIdentifier: %v", err)
	}
	if err := r.Passwords.Set(ctx, squatter.ID, "hash:squatter"); err != nil {
		t.Fatalf("Passwords.Set: %v", err)
	}
	first, _ := session.NewSession(squatter.ID, time.Hour, time.Now())
	first.RefreshTokenHash = "rollback-refresh-1"
	if _, err := r.Sessions.Create(ctx, first); err != nil {
		t.Fatalf("Sessions.Create: %v", err)
	}

	binding := provisioningBinding(addr)
	binding.UserID, binding.IdentifierID = squatter.ID, squatterIdent.ID
	revision := revisionOf(t, r, squatter.ID)
	binding.AuthRevision = &revision
	link := seedLink(t, r, "digest-rollback", binding, time.Now().Add(15*time.Minute))

	in := redeemInput(link.digest, "rollback-adopting", time.Now())
	store := newPasswordlessStore(db)

	var out passwordless.RedeemResult
	attempts, err := retryWithInjectedAbort(ctx, db,
		func() {
			// Attempt one has rolled back. NOTHING it staged may be visible.
			if row, claim := tokenIsStored(t, db, link); !row || !claim {
				t.Errorf("the rolled-back attempt consumed the link: row=%v claim=%v", row, claim)
			}
			if _, err := readPassword(ctx, db, db.ReaderFrom(ctx), squatter.ID); err != nil {
				t.Errorf("the rolled-back attempt removed the password: %v", err)
			}
			staged := readRow(t, db, squatterIdent.ID)
			if at, err := staged.verifiedAt(); err != nil {
				t.Fatalf("verifiedAt: %v", err)
			} else if !at.IsZero() {
				t.Error("the rolled-back attempt verified the identifier")
			}
			if row, err := readUser(ctx, db, db.ReaderFrom(ctx), squatter.ID); err != nil {
				t.Fatalf("readUser: %v", err)
			} else if row.AuthRevision != revision {
				t.Errorf("auth_revision = %d, want the untouched %d", row.AuthRevision, revision)
			}
			// A second session, minted between attempts: the committing attempt
			// owes its revocation too.
			second, _ := session.NewSession(squatter.ID, time.Hour, time.Now())
			second.RefreshTokenHash = "rollback-refresh-2"
			if _, err := r.Sessions.Create(ctx, second); err != nil {
				t.Fatalf("second Sessions.Create: %v", err)
			}
		},
		func(ctx context.Context) error {
			return store.redeem(ctx, in, in.Now.UTC(), &out)
		})
	if err != nil {
		t.Fatalf("Redeem under an injected abort: %v", err)
	}
	if attempts != 2 {
		t.Errorf("attempts = %d, want exactly 2", attempts)
	}
	if out.Outcome != passwordless.OutcomeVerifyAndAdoptExistingUnverified {
		t.Errorf("outcome = %q, want verify_and_adopt", out.Outcome)
	}

	live, err := readSessionsForUser(ctx, db, db.ReaderFrom(ctx), squatter.ID)
	if err != nil {
		t.Fatalf("readSessionsForUser: %v", err)
	}
	if len(live) != 1 || live[0].ID != out.Session.ID {
		t.Errorf("sessions = %d (%+v), want only the adopting session — the between-attempts session must be revoked too", len(live), live)
	}
	if row, claim := tokenIsStored(t, db, link); row || claim {
		t.Errorf("the committed attempt left the link behind: row=%v claim=%v", row, claim)
	}
	after, err := readUser(ctx, db, db.ReaderFrom(ctx), squatter.ID)
	if err != nil {
		t.Fatalf("readUser: %v", err)
	}
	if after.AuthRevision != revision+1 {
		t.Errorf("auth_revision = %d, want exactly one increment from %d", after.AuthRevision, revision)
	}
}

// TestFourWayConcurrentRedemptionCommitsExactlyOne widens the conformance
// suite's two-way race to four contenders on one link.
//
// Four is not decoration: with two, an implementation could serialize by luck.
// Four contenders on ONE token document force the loop that actually decides
// this — every attempt reads the digest claim and the row it names, so the three
// losers abort, re-run, find the claim gone, and answer ErrRedemption rather
// than a conflict the caller would have to interpret.
func TestFourWayConcurrentRedemptionCommitsExactlyOne(t *testing.T) {
	r, db := openRepos(t)
	ctx := context.Background()

	const rounds = 2
	const racers = 4
	for round := range rounds {
		addr := fmt.Sprintf("four-way-%d@example.com", round)
		link := seedLink(t, r, fmt.Sprintf("digest-four-way-%d", round), provisioningBinding(addr), time.Now().Add(15*time.Minute))

		var (
			wg      sync.WaitGroup
			results [racers]passwordless.RedeemResult
			errs    [racers]error
		)
		start := make(chan struct{})
		for i := range racers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				results[i], errs[i] = r.Passwordless.Redeem(ctx,
					redeemInput(link.digest, fmt.Sprintf("refresh-four-way-%d-%d", round, i), time.Now()))
			}()
		}
		close(start)
		wg.Wait()

		winner := -1
		for i := range racers {
			switch {
			case errs[i] == nil:
				if winner >= 0 {
					t.Fatalf("round %d: redemptions %d and %d both committed", round, winner, i)
				}
				winner = i
			case errors.Is(errs[i], passwordless.ErrRedemption):
			default:
				t.Fatalf("round %d: racer %d got a caller-visible failure instead of the generic rejection: %v", round, i, errs[i])
			}
		}
		if winner < 0 {
			t.Fatalf("round %d: no redemption committed", round)
		}

		// ONE subject owns the address, ONE session exists, and the link is gone.
		owner, err := r.Identifiers.GetLogin(ctx, string(identifier.KindEmail), addr)
		if err != nil {
			t.Fatalf("round %d: the winner left no identifier: %v", round, err)
		}
		if owner.UserID != results[winner].User.ID {
			t.Fatalf("round %d: the address is owned by %q but the committed session belongs to %q",
				round, owner.UserID, results[winner].User.ID)
		}
		live, err := readSessionsForUser(ctx, db, db.ReaderFrom(ctx), owner.UserID)
		if err != nil {
			t.Fatalf("round %d: readSessionsForUser: %v", round, err)
		}
		if len(live) != 1 || live[0].ID != results[winner].Session.ID {
			t.Fatalf("round %d: sessions = %d (%+v), want only the winner's", round, len(live), live)
		}
		if row, claim := tokenIsStored(t, db, link); row || claim {
			t.Errorf("round %d: the redeemed link survived: row=%v claim=%v", round, row, claim)
		}
	}
}
