//go:build integration && !live

package firestore

import (
	"context"
	"testing"
	"time"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
)

// The challenge claim-lifecycle and purge cases the conformance suite cannot
// reach: the digest claim documents (no port returns one), the attempt counter
// as it is STORED, the purge's exact boundary, and the transaction read set that
// makes a purge yield to a concurrent replacement (SCHEMA.md §5.7).

// raceHandshakeTimeout bounds the wait in
// TestPurgeSerializesWithAConcurrentReplacement. A Firestore read-write
// transaction takes READ LOCKS, so the concurrent Replace is normally BLOCKED
// behind the purge's own open transaction rather than committing inside it —
// which is the serialization the purge relies on. The wait is bounded so the
// test proves that rather than hanging on it.
const raceHandshakeTimeout = 2 * time.Second

// challengeFixture builds a challenge for a subject and purpose. A negative ttl
// yields an already-expired row.
func challengeFixture(userID, purpose, keyID, digest string, bind []byte, attempts int, ttl time.Duration, now time.Time) challenge.Challenge {
	now = now.UTC()
	return challenge.Challenge{
		UserID:         userID,
		Purpose:        purpose,
		SecretDigest:   digest,
		ProtectorKeyID: keyID,
		Context:        bind,
		AttemptCount:   attempts,
		CreatedAt:      now,
		ExpiresAt:      now.Add(ttl),
		Version:        1,
	}
}

// digestClaimHolder reports which challenge document holds the claim on
// (purpose, digest), if any.
func digestClaimHolder(t *testing.T, db *firestoredb.DB, purpose, digest string) (challengeDigestClaimDoc, bool) {
	t.Helper()
	var claim challengeDigestClaimDoc
	if !readClaim(t, db, challengeDigestClaimRef(db, purpose, digest), &claim) {
		return challengeDigestClaimDoc{}, false
	}
	return claim, true
}

// storedChallenge reads the row for (subject key, purpose) as it is PERSISTED,
// which is what an attempt counter assertion has to look at — no port exposes it.
func storedChallenge(t *testing.T, db *firestoredb.DB, subjectKey, purpose string) (challengeDoc, bool) {
	t.Helper()
	ctx := context.Background()
	row, found, err := readChallenge(ctx, db, db.ReaderFrom(ctx), subjectKey, purpose)
	if err != nil {
		t.Fatalf("readChallenge(%q, %q): %v", subjectKey, purpose, err)
	}
	return row, found
}

// TestReplaceReleasesTheDisplacedDigestClaim is the consequence of keying the
// document by (subject_key, purpose): a Replace DISPLACES the previous row
// instead of deleting it, so the displaced row's digest claim has no other
// chance to be released. If it were not released here, the old digest would stay
// claimed by a row that no longer exists, and the first later reuse of that
// digest — by any subject — would fail as a collision nothing explains.
func TestReplaceReleasesTheDisplacedDigestClaim(t *testing.T) {
	ctx := context.Background()
	r, db := openRepos(t)

	first, err := r.Challenges.Replace(ctx, challengeFixture("u-displace", challenge.PurposeLoginOTP, "k1", "dig-displaced", nil, 0, time.Hour, time.Now()))
	if err != nil {
		t.Fatalf("Replace #1: %v", err)
	}
	claim, ok := digestClaimHolder(t, db, challenge.PurposeLoginOTP, "dig-displaced")
	if !ok || claim.ChallengeID != first.ID {
		t.Fatalf("digest claim after Replace #1 = %+v ok=%v, want the first challenge", claim, ok)
	}

	if _, err := r.Challenges.Replace(ctx, challengeFixture("u-displace", challenge.PurposeLoginOTP, "k1", "dig-current", nil, 0, time.Hour, time.Now())); err != nil {
		t.Fatalf("Replace #2: %v", err)
	}
	if claim, ok := digestClaimHolder(t, db, challenge.PurposeLoginOTP, "dig-displaced"); ok {
		t.Errorf("the displaced digest is still claimed: %+v", claim)
	}

	// The freed digest is claimable by ANOTHER subject, which is the observable
	// consequence and the thing a stranded claim would silently forbid.
	if _, err := r.Challenges.Replace(ctx, challengeFixture("u-other", challenge.PurposeLoginOTP, "k1", "dig-displaced", nil, 0, time.Hour, time.Now())); err != nil {
		t.Errorf("Replace reusing the freed digest for another subject: err=%v, want nil", err)
	}
}

// TestWrongAttemptsPersistBetweenConsumeCalls asserts the counter as STORED. The
// conformance suite proves the OUTCOMES of a run of wrong codes; this proves the
// increment is durable between calls rather than recomputed, and that a rejected
// attempt leaves the row and its claim entirely intact — a rejected attempt has
// consumed nothing.
func TestWrongAttemptsPersistBetweenConsumeCalls(t *testing.T) {
	ctx := context.Background()
	r, db := openRepos(t)

	const subject = "u-attempts"
	if _, err := r.Challenges.Replace(ctx, challengeFixture(subject, challenge.PurposeLoginOTP, "k1", "dig-attempts", nil, 0, time.Hour, time.Now())); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	// One short of the budget: the maxAttempts-th wrong code below is the one that
	// locks out.
	for want := 1; want <= challenge.MaxAttempts-1; want++ {
		if _, out, err := r.Challenges.ConsumeCode(ctx, subject, challenge.PurposeLoginOTP,
			[]challenge.DigestCandidate{{KeyID: "k1", Digest: "wrong"}}, "", challenge.MaxAttempts, time.Now()); err != nil || out != challenge.OutcomeRejected {
			t.Fatalf("wrong attempt #%d: out=%s err=%v, want rejected", want, out, err)
		}
		row, found := storedChallenge(t, db, subject, challenge.PurposeLoginOTP)
		if !found || row.AttemptCount != want {
			t.Fatalf("stored attempt_count after #%d = %d found=%v, want %d", want, row.AttemptCount, found, want)
		}
		if _, ok := digestClaimHolder(t, db, challenge.PurposeLoginOTP, "dig-attempts"); !ok {
			t.Fatalf("the digest claim was released by a rejected attempt #%d", want)
		}
	}

	// The maxAttempts-th wrong code locks out: the row AND its claim go.
	if _, out, err := r.Challenges.ConsumeCode(ctx, subject, challenge.PurposeLoginOTP,
		[]challenge.DigestCandidate{{KeyID: "k1", Digest: "wrong"}}, "", challenge.MaxAttempts, time.Now()); err != nil || out != challenge.OutcomeLockedOut {
		t.Fatalf("lockout attempt: out=%s err=%v, want locked_out", out, err)
	}
	if _, found := storedChallenge(t, db, subject, challenge.PurposeLoginOTP); found {
		t.Error("the locked-out row survived")
	}
	if claim, ok := digestClaimHolder(t, db, challenge.PurposeLoginOTP, "dig-attempts"); ok {
		t.Errorf("the locked-out row's digest is still claimed: %+v", claim)
	}
}

// TestPurgeBoundaryIsInclusive pins the comparison the port words as "at or past
// before" and both SQL adapters write as `expires_at <= ?`. The two rows differ
// by ONE MICROSECOND across the boundary — Firestore's own resolution, so the
// case is decided by the operator rather than by rounding.
func TestPurgeBoundaryIsInclusive(t *testing.T) {
	ctx := context.Background()
	r, db := openRepos(t)

	before := firestoredb.TruncateTime(time.Now())
	exactly := challengeFixture("u-boundary-eq", challenge.PurposeLoginOTP, "k1", "dig-eq", nil, 0, 0, before)
	after := challengeFixture("u-boundary-gt", challenge.PurposeLoginOTP, "k1", "dig-gt", nil, 0, firestoredb.TimePrecision, before)
	for _, c := range []challenge.Challenge{exactly, after} {
		if _, err := r.Challenges.Replace(ctx, c); err != nil {
			t.Fatalf("Replace(%s): %v", c.SecretDigest, err)
		}
	}

	n, err := r.Challenges.PurgeExpired(ctx, before, 0)
	if err != nil || n != 1 {
		t.Fatalf("PurgeExpired(before, unbounded) = %d err=%v, want 1 (the exactly-equal row only)", n, err)
	}
	if _, found := storedChallenge(t, db, "u-boundary-eq", challenge.PurposeLoginOTP); found {
		t.Error("the exactly-at-boundary row survived the purge")
	}
	if _, found := storedChallenge(t, db, "u-boundary-gt", challenge.PurposeLoginOTP); !found {
		t.Error("the one-microsecond-later row was purged")
	}

	// The purged row released its digest, so the value is claimable again; the
	// survivor kept its own.
	if claim, ok := digestClaimHolder(t, db, challenge.PurposeLoginOTP, "dig-eq"); ok {
		t.Errorf("a purged row's digest is still claimed: %+v", claim)
	}
	if _, ok := digestClaimHolder(t, db, challenge.PurposeLoginOTP, "dig-gt"); !ok {
		t.Error("a surviving row's digest claim was released by the purge")
	}
	if _, err := r.Challenges.Replace(ctx, challengeFixture("u-reuse", challenge.PurposeLoginOTP, "k1", "dig-eq", nil, 0, time.Hour, time.Now())); err != nil {
		t.Errorf("Replace reusing a purged digest: err=%v, want nil (the purge released the claim)", err)
	}
}

// TestPurgeSerializesWithAConcurrentReplacement is the read-set proof, and it
// drives the interleaving rather than hoping for it: the test opens the purge's
// transaction itself — through the same read and write helpers PurgeExpired uses
// — and hands control to a goroutine that calls Replace on the very document the
// purge just selected.
//
// What it establishes, measured rather than assumed: the replacement CANNOT land
// between the purge's read and its commit. A Firestore read-write transaction
// takes read locks, so the Replace blocks until the purge settles, and the
// handshake below times out (which is the PASSING observation, logged). That
// serialization is what makes selecting the candidates inside the transaction
// correct: selecting them outside would delete a valid secret and report it as an
// expired one, because nothing would order the two.
//
// The retry branch is asserted too, for the deployment where the lock is dropped
// instead of held (the emulator documents incomplete transaction locking, and a
// lock timeout on a real database ends the same way): if the replacement DOES
// commit inside the transaction, the purge must abort, re-run, and select nothing
// — its candidate is live now. Either way the invariant at the end is the same:
// the replacement survived and is redeemable.
func TestPurgeSerializesWithAConcurrentReplacement(t *testing.T) {
	ctx := context.Background()
	r, db := openRepos(t)

	const (
		subject = "u-purge-race"
		purpose = challenge.PurposeLoginOTP
	)
	if _, err := r.Challenges.Replace(ctx, challengeFixture(subject, purpose, "k1", "dig-stale", nil, 0, -time.Minute, time.Now())); err != nil {
		t.Fatalf("seed the expired row: %v", err)
	}
	before := time.Now()

	selected := make(chan struct{})
	replaced := make(chan error, 1)
	go func() {
		<-selected
		_, err := r.Challenges.Replace(ctx, challengeFixture(subject, purpose, "k1", "dig-fresh", nil, 0, time.Hour, time.Now()))
		replaced <- err
	}()

	attempts := 0
	var candidates []int
	interleaved := false
	err := db.Transact(ctx, func(ctx context.Context) error {
		attempts++
		rows, err := readExpiredChallenges(ctx, db, db.ReaderFrom(ctx), before, 0)
		if err != nil {
			return err
		}
		candidates = append(candidates, len(rows))

		if attempts == 1 {
			close(selected)
			select {
			case err := <-replaced:
				if err != nil {
					return err
				}
				interleaved = true
			case <-time.After(raceHandshakeTimeout):
				// The replacement is blocked behind this transaction's read
				// lock. Fall through: it commits after this one settles, and
				// the invariant below still has to hold.
			}
		}

		w := db.WriterFrom(ctx)
		plan := newClaimPlan()
		if err := dropChallenges(ctx, db, w, plan, rows); err != nil {
			return err
		}
		return plan.commit(ctx, w)
	})
	if err != nil {
		t.Fatalf("the purge transaction failed: %v", err)
	}
	// The channel is buffered and drained exactly once: attempt 1 took it when the
	// replacement committed inside the transaction, otherwise it is still pending
	// and arrives now that the purge has settled.
	if !interleaved {
		if err := <-replaced; err != nil {
			t.Fatalf("the concurrent Replace failed: %v", err)
		}
	}

	t.Logf("purge attempts=%d candidates per attempt=%v replacement committed mid-transaction=%v", attempts, candidates, interleaved)
	if interleaved {
		if attempts < 2 {
			t.Errorf("the purge committed on attempt 1 after a replacement landed in its read set — the candidates were not re-evaluated")
		}
		if last := candidates[len(candidates)-1]; last != 0 {
			t.Errorf("the retried attempt selected %d candidates, want 0 (the replaced row is live)", last)
		}
	}

	// The invariant, under either interleaving: the live replacement survived and
	// is redeemable.
	row, found := storedChallenge(t, db, subject, purpose)
	if !found {
		t.Fatal("the live replacement was deleted by the purge")
	}
	if row.SecretDigest != "dig-fresh" {
		t.Fatalf("stored digest = %q, want dig-fresh", row.SecretDigest)
	}
	if _, out, err := r.Challenges.ConsumeCode(ctx, subject, purpose,
		[]challenge.DigestCandidate{{KeyID: "k1", Digest: "dig-fresh"}}, "", challenge.MaxAttempts, time.Now()); err != nil || out != challenge.OutcomeRedeemed {
		t.Fatalf("redeeming the replacement: out=%s err=%v, want redeemed", out, err)
	}
}
