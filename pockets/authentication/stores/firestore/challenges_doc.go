package firestore

import (
	"context"
	"errors"
	"fmt"
	"time"

	gcfs "cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/sdk"
)

// The challenges collection AND its digest claim collection are owned here:
// putChallenge, updateChallengeAttempts and dropChallenge are the only writers,
// and ownership_test.go refuses any other non-test file to name either.
//
// Two things about this collection are unlike every other one in the store, and
// both are why the pair matters (SCHEMA.md §3.11, §5.7, §5.10).
//
// The document id is NOT the row's primary key. It is
// h(subject_key, purpose) — migration 0015's unique index — because Replace is a
// REPLACEMENT: one live challenge per subject and purpose. Writing the new row
// with Set DISPLACES the old one structurally, in one write, instead of a
// delete-then-insert whose two halves could interleave. The surrogate id lives
// on in a FIELD and is what Challenge.ID and Consumed.ID return.
//
// Because the row is displaced rather than deleted, the DISPLACED row's digest
// claim has no other chance to be released. Replace must read the old row first
// and release it in the same transaction, or the old digest stays claimed
// forever and its later reuse would look like a collision that no row explains.
// PurgeExpired carries the same obligation, which is why it reads its candidates
// INSIDE its transaction.

// The challenge field paths the queries filter, order, and update on.
const (
	fieldChallengeExpiresAt    = "expires_at"
	fieldChallengeID           = "id"
	fieldChallengeUserID       = "user_id"
	fieldChallengePurpose      = "purpose"
	fieldChallengeAttemptCount = "attempt_count"
)

// purposeChunkSize bounds one `purpose in [...]` disjunction. Firestore expands
// a query to disjunctive normal form and caps it at 30 disjuncts, and the Go
// client pre-validates none of it — an over-long list arrives as the server's
// InvalidArgument. The purge sets this pocket passes are two or three purposes,
// so the chunking is a guard rather than a hot path.
const purposeChunkSize = 30

// challengeRef is the row's document — the KeyHash of the (subject_key, purpose)
// tuple a live challenge is unique under, NOT of its surrogate id.
func challengeRef(db *firestoredb.DB, subjectKey, purpose string) *gcfs.DocumentRef {
	return db.Doc(collectionChallenges, challengeDocID(subjectKey, purpose))
}

// challengeDigestClaimRef is the claim on a (purpose, secret_digest) pair. It
// doubles as ConsumeToken's access path: one point read resolves a presented
// digest to its challenge without a second index on the digest field.
func challengeDigestClaimRef(db *firestoredb.DB, purpose, secretDigest string) *gcfs.DocumentRef {
	return db.Doc(collectionChallengeDigests, challengeDigestClaimDocID(purpose, secretDigest))
}

// expiredChallengesQuery is PurgeExpired's selection: rows AT or past before,
// oldest first, bounded. The boundary is `<=`, matching the port ("at or past
// before") and both SQL adapters' `expires_at <= ?`; before is truncated to
// Firestore's microsecond resolution so an exactly-equal expiry compares equal
// rather than falling on either side of a sub-microsecond difference.
//
// A non-positive limit is UNBOUNDED, which is the port's own contract; the query
// then carries no Limit clause.
func expiredChallengesQuery(db *firestoredb.DB, before time.Time, limit int) gcfs.Query {
	q := db.Collection(collectionChallenges).
		Where(fieldChallengeExpiresAt, "<=", firestoredb.TruncateTime(before)).
		OrderBy(fieldChallengeExpiresAt, gcfs.Asc).
		OrderBy(fieldChallengeID, gcfs.Asc)
	if limit > 0 {
		q = q.Limit(limit)
	}
	return q
}

// challengesForPurposesQuery is the revocation cascade's selection: every
// challenge a user holds for one chunk of purposes — the SQL adapters'
// `user_id = ? AND purpose IN (…)`. Equality only, so Firestore serves it from
// the automatic single-field indexes without a composite.
func challengesForPurposesQuery(db *firestoredb.DB, userID string, purposes []string) gcfs.Query {
	return db.Collection(collectionChallenges).
		Where(fieldChallengeUserID, "==", userID).
		Where(fieldChallengePurpose, "in", purposes)
}

// newChallengeDoc builds the document for a challenge being written, minting the
// id when the caller left it empty, resolving the subject key through the
// domain's own rule (SubjectKey when set, else UserID), and defaulting the row
// version the SQL adapters default.
//
// Context is nullable rather than "": the domain distinguishes a nil binding
// blob from an empty one, and collapsing them would make an unbound challenge
// indistinguishable from one bound to nothing.
//
// It is called INSIDE the write path so a retried attempt mints a fresh id
// rather than reusing one a rolled-back attempt claimed (N-D5).
func newChallengeDoc(c challenge.Challenge) challengeDoc {
	challengeID := c.ID
	if challengeID == "" {
		challengeID = firestoredb.NewID()
	}
	version := c.Version
	if version == 0 {
		version = 1
	}
	return challengeDoc{
		ID:             challengeID,
		SubjectKey:     c.ResolvedSubjectKey(),
		UserID:         c.UserID,
		Purpose:        c.Purpose,
		SecretDigest:   c.SecretDigest,
		ProtectorKeyID: c.ProtectorKeyID,
		Context:        nullBlob(c.Context),
		AttemptCount:   c.AttemptCount,
		ExpiresAt:      firestoredb.TruncateTime(c.ExpiresAt),
		CreatedAt:      firestoredb.TruncateTime(c.CreatedAt),
		Version:        version,
	}
}

// nullBlob maps an absent binding blob to Firestore's null and a present one to
// its opaque text — an already-digested validator, never a secret.
func nullBlob(b []byte) any {
	if b == nil {
		return nil
	}
	return string(b)
}

// putChallenge writes a challenge row with Set and records the digest claim it
// takes. Set rather than Create because the document id IS the replacement key:
// the write displaces whatever row held (subject_key, purpose), which is exactly
// the port's "two active rows never coexist".
//
// The CLAIM is still taken with Create when this transaction did not release it,
// so a digest another subject already holds loses at the SERVER as
// sdk.ErrAlreadyExists rather than being silently re-pointed (ruling R3).
func putChallenge(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, plan *claimPlan, row challengeDoc) error {
	plan.take(challengeDigestClaimRef(db, row.Purpose, row.SecretDigest), challengeDigestClaimDoc{
		DocID:       challengeDocID(row.SubjectKey, row.Purpose),
		ChallengeID: row.ID,
	})
	return w.Set(ctx, challengeRef(db, row.SubjectKey, row.Purpose), row)
}

// releaseChallengeDigest records that a row is leaving — displaced, consumed,
// locked out, or purged — so its digest stops being claimed. Every caller has
// READ the row it names, which is what makes the release its own to make.
func releaseChallengeDigest(db *firestoredb.DB, plan *claimPlan, row challengeDoc) {
	plan.release(challengeDigestClaimRef(db, row.Purpose, row.SecretDigest))
}

// updateChallengeAttempts records ONE wrong code. It is a field update rather
// than a whole-document Set: the counter is the only thing that changes, the row
// survives, and its digest claim is untouched — a rejected attempt has not
// consumed anything.
func updateChallengeAttempts(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, row challengeDoc, attempts int) error {
	return w.Update(ctx, challengeRef(db, row.SubjectKey, row.Purpose), []gcfs.Update{
		{Path: fieldChallengeAttemptCount, Value: attempts},
	})
}

// dropChallenge deletes one row and releases its digest claim.
func dropChallenge(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, plan *claimPlan, row challengeDoc) error {
	releaseChallengeDigest(db, plan, row)
	return w.Delete(ctx, challengeRef(db, row.SubjectKey, row.Purpose))
}

// dropChallenges enqueues the deletion of every row in rows together with its
// claim, skipping a repeat of a document already queued — a purge set and a
// separately consumed row can name the same document, and two Deletes for one
// document in one commit is an ambiguous transaction rather than a stronger
// delete. It takes ALREADY-READ documents and reads nothing itself, so a caller
// revoking more than challenges in one transaction can finish its read phase
// first.
func dropChallenges(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, plan *claimPlan, rows []challengeDoc) error {
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		key := challengeDocID(row.SubjectKey, row.Purpose)
		if seen[key] {
			continue
		}
		seen[key] = true
		if err := dropChallenge(ctx, db, w, plan, row); err != nil {
			return err
		}
	}
	return nil
}

// readChallenge reads the live row for (subject_key, purpose). An absent
// document is (zero, false, nil): having no live challenge is a normal state and
// the port answers it with an OUTCOME, not an error.
func readChallenge(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader, subjectKey, purpose string) (challengeDoc, bool, error) {
	snap, err := r.Get(ctx, challengeRef(db, subjectKey, purpose))
	if errors.Is(err, sdk.ErrNotFound) {
		return challengeDoc{}, false, nil
	}
	if err != nil {
		return challengeDoc{}, false, err
	}
	row, err := decodeChallenge(snap)
	if err != nil {
		return challengeDoc{}, false, err
	}
	return row, true, nil
}

// readChallengeByDigestClaim resolves a presented (purpose, digest) through its
// claim: the claim document names the row's document, and the row is then read.
// Absence at either hop is sdk.ErrNotFound, matching the SQL adapters' empty
// result for `WHERE purpose = ? AND secret_digest = ?`.
//
// The claim names the row by (subject_key, purpose) rather than by the surrogate
// id, because that pair IS the document — resolving through the id would need a
// second lookup this collection deliberately does not have (§5.10).
func readChallengeByDigestClaim(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader, purpose, presentedDigest string) (challengeDoc, error) {
	snap, err := r.Get(ctx, challengeDigestClaimRef(db, purpose, presentedDigest))
	if err != nil {
		return challengeDoc{}, err
	}
	var claim challengeDigestClaimDoc
	if err := snap.DataTo(&claim); err != nil {
		return challengeDoc{}, fmt.Errorf("authentication firestore store: decoding %s: %s: %w", collectionChallengeDigests, err, sdk.ErrInvalidInput)
	}
	row, err := r.Get(ctx, db.Doc(collectionChallenges, claim.DocID))
	if err != nil {
		return challengeDoc{}, err
	}
	return decodeChallenge(row)
}

// readExpiredChallenges reads the purge candidates INSIDE the caller's
// transaction. Reading them there is not a formality: it puts every candidate in
// the transaction's READ SET, so a concurrent Replace of one of those
// (subject_key, purpose) documents aborts this transaction and the retry
// re-evaluates the predicate — which is how a live replacement of an expired row
// survives a purge that had already selected it.
func readExpiredChallenges(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader, before time.Time, limit int) ([]challengeDoc, error) {
	return queryChallenges(ctx, r, expiredChallengesQuery(db, before, limit))
}

// readChallengesForPurposes reads every challenge a user holds for the named
// purposes — the READ half the revocation cascades pair with dropChallenges. An
// empty purpose list reads nothing (the SQL adapters skip the statement
// entirely), and the list is chunked so a long one cannot exceed Firestore's
// disjunction cap. Results are DEDUPLICATED by document, because two chunks
// cannot overlap but a caller may pass the same purpose twice.
func readChallengesForPurposes(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader, userID string, purposes []string) ([]challengeDoc, error) {
	if userID == "" || len(purposes) == 0 {
		return nil, nil
	}
	seen := make(map[string]bool, len(purposes))
	var out []challengeDoc
	for start := 0; start < len(purposes); start += purposeChunkSize {
		end := min(start+purposeChunkSize, len(purposes))
		rows, err := queryChallenges(ctx, r, challengesForPurposesQuery(db, userID, purposes[start:end]))
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			key := challengeDocID(row.SubjectKey, row.Purpose)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, row)
		}
	}
	return out, nil
}

// queryChallenges runs one challenge query and decodes every document it
// returns, consuming iterator.Done as the loop terminator and mapping every
// other Next error HERE, at the iteration boundary.
func queryChallenges(ctx context.Context, r firestoredb.Reader, q gcfs.Query) ([]challengeDoc, error) {
	it := r.Documents(ctx, q)
	defer it.Stop()

	var out []challengeDoc
	for {
		snap, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return out, nil
		}
		if err != nil {
			return nil, firestoredb.MapError(err)
		}
		row, err := decodeChallenge(snap)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
}

// decodeChallenge turns one snapshot into a challenge document.
func decodeChallenge(snap *gcfs.DocumentSnapshot) (challengeDoc, error) {
	var row challengeDoc
	if err := snap.DataTo(&row); err != nil {
		return challengeDoc{}, fmt.Errorf("authentication firestore store: decoding %s: %s: %w", collectionChallenges, err, sdk.ErrInvalidInput)
	}
	return row, nil
}

// expired reports whether the row is at or past its expiry at now — the domain's
// own boundary, applied to the stored value.
func (d challengeDoc) expired(now time.Time) bool {
	return !now.Before(d.ExpiresAt)
}

// binding renders the stored context blob: a stored null is a nil blob, which
// the domain distinguishes from an empty one.
func (d challengeDoc) binding() []byte {
	s, ok := d.Context.(string)
	if !ok {
		return nil
	}
	return []byte(s)
}

// bindingText renders the stored context blob as the text ConsumeCode compares
// by equality. A null blob compares as "", matching the SQL adapters' NULL-typed
// scan into a zero-value sql.NullString.
func (d challengeDoc) bindingText() string {
	s, _ := d.Context.(string)
	return s
}

// consumed projects the row onto the value an atomic consume returns. It carries
// BOTH SubjectKey and UserID, so a redeemer can act on a row whose UserID is
// empty — the magic-link case the subject key exists for.
func (d challengeDoc) consumed(now time.Time) challenge.Consumed {
	return challenge.Consumed{
		ID:             d.ID,
		SubjectKey:     d.SubjectKey,
		UserID:         d.UserID,
		Purpose:        d.Purpose,
		Context:        d.binding(),
		ProtectorKeyID: d.ProtectorKeyID,
		ConsumedAt:     firestoredb.TruncateTime(now),
	}
}
