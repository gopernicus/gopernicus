package firestore

import (
	"context"
	"errors"
	"time"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/sdk"
)

var _ challenge.Repository = (*challengeStore)(nil)

// challengeStore fills challenge.Repository over the challenges collection —
// keyed by (subject_key, purpose), so replacement is a document Set — and the
// digest claim that reproduces the (purpose, secret_digest) unique index
// (SCHEMA.md §5.7). Every reference goes through challenges_doc.go.
//
// This is the store's atomic secret rail, and its three redemption methods share
// one shape: ONE transaction that reads, decides, and COMMITS the decision, with
// the domain outcome reported afterwards. The transaction's read set is what
// serializes concurrent redeemers — every attempt reads the same document, so
// exactly one commits and the losers re-run against the state it left.
type challengeStore struct {
	db *firestoredb.DB
}

func newChallengeStore(db *firestoredb.DB) *challengeStore {
	return &challengeStore{db: db}
}

// Replace atomically replaces the subject's live challenge for the purpose,
// releasing the DISPLACED row's digest claim and taking the new one in the same
// transaction; a digest another subject already holds is sdk.ErrAlreadyExists
// and nothing is written.
//
// The read is the displaced row, and it is the only reason this needs a
// transaction: the new row's Set would displace it structurally either way, but
// the claim it leaves behind has to be released by the write that displaces it,
// or the old digest stays claimed by a row that no longer exists.
func (s *challengeStore) Replace(ctx context.Context, c challenge.Challenge) (challenge.Challenge, error) {
	if err := refuseAmbient(ctx); err != nil {
		return challenge.Challenge{}, err
	}
	var row challengeDoc
	err := retryTransact(ctx, s.db, func(ctx context.Context) error {
		row = newChallengeDoc(c)

		old, found, err := readChallenge(ctx, s.db, s.db.ReaderFrom(ctx), row.SubjectKey, row.Purpose)
		if err != nil {
			return err
		}

		w := s.db.WriterFrom(ctx)
		plan := newClaimPlan()
		if found {
			releaseChallengeDigest(s.db, plan, old)
		}
		if err := putChallenge(ctx, s.db, w, plan, row); err != nil {
			return err
		}
		return plan.commit(ctx, w)
	})
	if err != nil {
		return challenge.Challenge{}, err
	}
	c.ID = row.ID
	if c.Version == 0 {
		c.Version = row.Version
	}
	return c, nil
}

// ConsumeCode evaluates the (userID, purpose) code in ONE transaction. Its
// ConsumeOutcome is AUTHORITATIVE and the error is infrastructure-only: the
// attempt increment, the lockout deletion, the expiry deletion, and the
// consumption of a correct-but-wrong-context code all COMMIT their writes and
// are then reported (N-D2).
//
// That is why the outcome is carried out of the callback in a value rather than
// returned from it: returning "expired" or "locked out" as an error would roll
// back the very deletion those outcomes describe, and the next attempt would
// find the row again. On any infrastructure failure the zero outcome —
// OutcomeNotFound — is what the caller sees, which is fail-closed.
func (s *challengeStore) ConsumeCode(ctx context.Context, userID, purpose string, candidates []challenge.DigestCandidate,
	expectedContextDigest string, maxAttempts int, now time.Time) (challenge.Consumed, challenge.ConsumeOutcome, error) {
	if err := refuseAmbient(ctx); err != nil {
		return challenge.Consumed{}, challenge.OutcomeNotFound, err
	}
	var out codeConsumption
	err := retryTransact(ctx, s.db, func(ctx context.Context) error {
		return s.consumeCode(ctx, userID, purpose, candidates, expectedContextDigest, maxAttempts, now, &out)
	})
	if err != nil {
		return challenge.Consumed{}, challenge.OutcomeNotFound, err
	}
	return out.consumed, out.outcome, nil
}

// ConsumeToken resolves (purpose, presentedDigest) through the digest claim and
// deletes the challenge with its claim. An expired token's DELETION COMMITS and
// sdk.ErrExpired is reported afterwards; an unknown or already-consumed digest
// is sdk.ErrNotFound.
//
// The empty digest is refused before the transaction opens: it never matches a
// row, and a claim document keyed on the empty digest is not a thing this store
// ever writes — but answering it with a lookup would spend a round trip proving
// what the port already states.
func (s *challengeStore) ConsumeToken(ctx context.Context, purpose, presentedDigest string, now time.Time) (challenge.Consumed, error) {
	if err := refuseAmbient(ctx); err != nil {
		return challenge.Consumed{}, err
	}
	if presentedDigest == "" {
		return challenge.Consumed{}, sdk.ErrNotFound
	}
	var out tokenConsumption
	err := retryTransact(ctx, s.db, func(ctx context.Context) error {
		return s.consumeToken(ctx, purpose, presentedDigest, now, &out)
	})
	if err != nil {
		return challenge.Consumed{}, err
	}
	switch {
	case !out.found:
		return challenge.Consumed{}, sdk.ErrNotFound
	case out.expired:
		return challenge.Consumed{}, sdk.ErrExpired
	}
	return out.consumed, nil
}

// PurgeExpired deletes up to limit challenges at or past before, WITH their
// digest claims, and returns the COMMITTED count. A non-positive limit is
// unbounded (the port's own contract).
//
// The candidates are selected INSIDE the transaction, which is the whole design
// of this method. Selecting them outside would make the deletion act on a stale
// snapshot: a Replace that landed in between would have written a LIVE challenge
// into the very document the purge selected as expired, and the purge would
// delete a valid secret. Reading them here puts them in the transaction's read
// set, so that Replace aborts this transaction instead, and the retry
// re-evaluates the predicate and skips the replaced row.
func (s *challengeStore) PurgeExpired(ctx context.Context, before time.Time, limit int) (int, error) {
	if err := refuseAmbient(ctx); err != nil {
		return 0, err
	}
	var purged int
	err := retryTransact(ctx, s.db, func(ctx context.Context) error {
		purged = 0

		rows, err := readExpiredChallenges(ctx, s.db, s.db.ReaderFrom(ctx), before, limit)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}

		w := s.db.WriterFrom(ctx)
		plan := newClaimPlan()
		if err := dropChallenges(ctx, s.db, w, plan, rows); err != nil {
			return err
		}
		if err := plan.commit(ctx, w); err != nil {
			return err
		}
		purged = len(rows)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return purged, nil
}

// codeConsumption is ONE attempt's outcome of a code evaluation: the row it
// consumed (empty unless it redeemed one) and the disposition the caller reports
// after the transaction commits.
type codeConsumption struct {
	consumed challenge.Consumed
	outcome  challenge.ConsumeOutcome
}

// tokenConsumption is ONE attempt's outcome of a token redemption.
type tokenConsumption struct {
	consumed challenge.Consumed
	found    bool
	expired  bool
}

// consumeCode is one attempt of the code evaluation: it RESETS the outcome,
// reads the live row, decides, and queues exactly the write its decision
// implies.
//
// The reset is the load-bearing line. A Firestore transaction callback may run
// more than once, and the disposition an aborted attempt computed — "this code
// was wrong", "this row was expired" — is not the disposition of the transaction
// that commits. Eight concurrent redeemers of one code produce exactly that:
// seven attempts observe a live row, lose the commit race, and re-run against a
// row that is no longer there. Without the reset, one of them would report a
// redemption that never happened.
//
// The order of the branches is the SQL adapters' order, and each is a decision
// about the row this transaction read:
//
//   - no live row → OutcomeNotFound, nothing written;
//   - expired → the row is DELETED and the deletion commits;
//   - no candidate digest matches → the attempt is counted, and the count
//     reaching maxAttempts deletes the row (lockout) instead of incrementing;
//   - a matching digest → the row is CONSUMED regardless of context, and a
//     mismatched context is reported as such (anti-probing: a valid secret never
//     survives a wrong-context replay).
func (s *challengeStore) consumeCode(ctx context.Context, userID, purpose string, candidates []challenge.DigestCandidate,
	expectedContextDigest string, maxAttempts int, now time.Time, out *codeConsumption) error {
	*out = codeConsumption{}

	// userID IS the subject key for every code purpose: none of them can be
	// issued without an account (the port says so), and Replace stored the row
	// under exactly that key.
	row, found, err := readChallenge(ctx, s.db, s.db.ReaderFrom(ctx), userID, purpose)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}

	w := s.db.WriterFrom(ctx)
	plan := newClaimPlan()

	if row.expired(now) {
		out.outcome = challenge.OutcomeExpired
		return dropAndCommit(ctx, s.db, w, plan, row)
	}

	if !matchesCandidate(row, candidates) {
		attempts := row.AttemptCount + 1
		if attempts >= maxAttempts {
			out.outcome = challenge.OutcomeLockedOut
			return dropAndCommit(ctx, s.db, w, plan, row)
		}
		out.outcome = challenge.OutcomeRejected
		return updateChallengeAttempts(ctx, s.db, w, row, attempts)
	}

	out.consumed = row.consumed(now)
	out.outcome = challenge.OutcomeRedeemed
	if expectedContextDigest != "" && row.bindingText() != expectedContextDigest {
		out.outcome = challenge.OutcomeContextMismatch
	}
	return dropAndCommit(ctx, s.db, w, plan, row)
}

// consumeToken is one attempt of the token redemption: it RESETS the outcome,
// resolves the digest claim to its row, records what it found, and queues the
// deletion of both.
//
// An absent claim is NOT an error here: the callback returns nil, commits
// nothing, and ConsumeToken reports sdk.ErrNotFound from the outcome. Returning
// the sentinel from the callback would say the same thing, but through a
// rollback of a transaction that wrote nothing — and it would make the "expired"
// branch below, which MUST commit, look like the exception rather than the rule.
func (s *challengeStore) consumeToken(ctx context.Context, purpose, presentedDigest string, now time.Time, out *tokenConsumption) error {
	*out = tokenConsumption{}

	row, err := readChallengeByDigestClaim(ctx, s.db, s.db.ReaderFrom(ctx), purpose, presentedDigest)
	if errors.Is(err, sdk.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}

	out.consumed, out.found, out.expired = row.consumed(now), true, row.expired(now)

	w := s.db.WriterFrom(ctx)
	plan := newClaimPlan()
	return dropAndCommit(ctx, s.db, w, plan, row)
}

// dropAndCommit deletes one row with its claim and commits the plan — the write
// phase every consuming branch above shares.
func dropAndCommit(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, plan *claimPlan, row challengeDoc) error {
	if err := dropChallenge(ctx, db, w, plan, row); err != nil {
		return err
	}
	return plan.commit(ctx, w)
}

// matchesCandidate reports whether any candidate names the row's protector key
// AND carries its digest. The key selection is what keeps an unexpired challenge
// issued under a rotated-out pepper verifiable, and the comparison is
// constant-time because the candidate digest is attacker-supplied. An empty
// candidate digest never matches: auth.ConstantTimeDigestEqual refuses it, so a
// caller that computed nothing counts a wrong attempt rather than redeeming.
func matchesCandidate(row challengeDoc, candidates []challenge.DigestCandidate) bool {
	for _, cand := range candidates {
		if cand.KeyID == row.ProtectorKeyID && auth.ConstantTimeDigestEqual(cand.Digest, row.SecretDigest) {
			return true
		}
	}
	return false
}
