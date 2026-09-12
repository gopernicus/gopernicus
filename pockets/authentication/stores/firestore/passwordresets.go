package firestore

import (
	"context"
	"errors"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passwordreset"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
)

var _ passwordreset.Repository = (*passwordResetStore)(nil)

// passwordResetStore fills passwordreset.Repository: ONE transaction that
// consumes the live reset challenge with its digest claim, sets the typed
// password row, and revokes every session (with its refresh claim), every
// outstanding grant, and the named challenge purposes for the resolved user. A
// non-live challenge — unknown, consumed, or expired — is sdk.ErrNotFound with
// nothing applied.
//
// It writes four collections it does not own, and reaches every one of them
// through that collection's owner file: putPassword (passwords_doc.go),
// readSessionsForUser/dropSessionsForUser (sessions_doc.go),
// readGrantsForUser/dropAuthGrants (grants_doc.go), and the challenge helpers
// this train owns. That is the discipline SCHEMA.md §7.2 makes executable — a
// composition may compose owners, never bypass them, because each pair is what
// keeps a row and its claims moving together.
type passwordResetStore struct {
	db *firestoredb.DB
}

func newPasswordResetStore(db *firestoredb.DB) *passwordResetStore {
	return &passwordResetStore{db: db}
}

// Redeem atomically consumes the live reset challenge and applies the full reset
// composition, returning the reset user's ID.
//
// The single generic failure is the point of the shape: unknown, already
// consumed, and expired are ONE answer (sdk.ErrNotFound) with nothing applied,
// so a caller cannot probe which tokens exist. Note the deliberate contrast with
// the consume family beside it — ConsumeToken DELETES an expired row before
// reporting expiry, while an expired reset token is simply not live and this
// method writes nothing at all. Both are the ports' own contracts (N-D2), and
// interchanging them would either leave a spent token replayable or turn a probe
// into a state change.
//
// Two simultaneous resets presenting one token resolve to exactly one commit:
// both transactions read the same digest claim and challenge document, so the
// loser aborts, re-runs, and finds the claim gone.
func (s *passwordResetStore) Redeem(ctx context.Context, in passwordreset.RedeemInput) (passwordreset.RedeemResult, error) {
	if err := refuseAmbient(ctx); err != nil {
		return passwordreset.RedeemResult{}, err
	}
	// An empty digest never matches a live challenge, and it is not a key this
	// store ever claims — the SQL adapters refuse it before their statement too.
	if in.TokenDigest == "" {
		return passwordreset.RedeemResult{}, sdk.ErrNotFound
	}

	var out resetRedemption
	err := retryTransact(ctx, s.db, func(ctx context.Context) error {
		return s.redeem(ctx, in, &out)
	})
	if err != nil {
		return passwordreset.RedeemResult{}, err
	}
	if !out.applied {
		return passwordreset.RedeemResult{}, sdk.ErrNotFound
	}
	return passwordreset.RedeemResult{UserID: out.userID}, nil
}

// resetRedemption is ONE attempt's outcome: whether the composition applied, and
// to whom.
type resetRedemption struct {
	userID  string
	applied bool
}

// redeem is one attempt of the composition, reads strictly before writes.
//
// out is RESET first: a Firestore callback may run more than once, and the user
// an aborted attempt resolved is not the user of the transaction that commits.
//
// The READ PHASE is the complete revocation set, because the vendor refuses any
// read issued after the transaction's first write — the reset challenge through
// its digest claim, every session of the resolved user (each carrying the
// refresh claim its deletion must release), every grant the user owns, and every
// challenge of the named purposes with its own digest claim. Reading them is
// also what makes them the CONTENTION set, which is what serializes two
// simultaneous resets.
func (s *passwordResetStore) redeem(ctx context.Context, in passwordreset.RedeemInput, out *resetRedemption) error {
	*out = resetRedemption{}
	r := s.db.ReaderFrom(ctx)

	row, err := readChallengeByDigestClaim(ctx, s.db, r, in.Purpose, in.TokenDigest)
	if errors.Is(err, sdk.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	// Not live. The SQL adapters express this as an `expires_at > ?` guard on the
	// consuming DELETE, so an expired token selects no row and changes nothing;
	// the same answer, reached by the same reasoning.
	if row.expired(in.Now) {
		return nil
	}

	userID := row.UserID
	binding, err := passwordreset.ParseBinding(row.binding())
	if err != nil {
		return err
	}
	owner, err := readUser(ctx, s.db, r, userID)
	if err != nil {
		return err
	}
	if !user.NormalizeStatus(user.Status(owner.Status)).Active() {
		return sdk.ErrNotFound
	}
	ident, found, err := readOptionalIdentifier(ctx, s.db, r, binding.IdentifierID)
	if err != nil {
		return err
	}
	if !found {
		return sdk.ErrNotFound
	}
	current, err := ident.toDomain()
	if err != nil {
		return err
	}
	if !binding.Matches(userID, owner.AuthRevision, current) {
		return sdk.ErrNotFound
	}
	live, err := readSessionsForUser(ctx, s.db, r, userID)
	if err != nil {
		return err
	}
	grants, err := readGrantsForUser(ctx, s.db, r, userID)
	if err != nil {
		return err
	}
	purgeRows, err := readChallengesForPurposes(ctx, s.db, r, userID, in.PurgeChallengePurposes)
	if err != nil {
		return err
	}

	// WRITE PHASE. Nothing below reads. The password, the sessions with their
	// claims, the grants, and the challenges with theirs commit together or not
	// at all: there is never a changed-password/live-old-session partial state,
	// which is the whole reason this composition is a port rather than four
	// calls.
	//
	// The consumed reset row leads the challenge set. It may also appear in the
	// purge set (password_reset is normally one of the named purposes), which is
	// why dropChallenges de-duplicates by document.
	w := s.db.WriterFrom(ctx)
	plan := newClaimPlan()
	if err := advanceUserRevision(ctx, s.db, w, userID, owner.AuthRevision+1, in.Now, projectionOfUser(owner)); err != nil {
		return err
	}
	if err := putPassword(ctx, s.db, w, userID, in.NewPasswordHash); err != nil {
		return err
	}
	if err := dropSessionsForUser(ctx, s.db, w, plan, live); err != nil {
		return err
	}
	if err := dropAuthGrants(ctx, s.db, w, grants); err != nil {
		return err
	}
	if err := dropChallenges(ctx, s.db, w, plan, append([]challengeDoc{row}, purgeRows...)); err != nil {
		return err
	}
	if err := plan.commit(ctx, w); err != nil {
		return err
	}

	out.userID, out.applied = userID, true
	return nil
}
