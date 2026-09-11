package firestore

import (
	"context"
	"time"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
)

var _ challenge.Repository = (*challengeStore)(nil)

// challengeStore fills challenge.Repository over the challenges collection —
// keyed by (subject_key, purpose), so replacement is a document Set — and the
// digest claim that reproduces the (purpose, secret_digest) unique index
// (SCHEMA.md §5.7). Bodies land in N4c.
type challengeStore struct {
	db *firestoredb.DB
}

func newChallengeStore(db *firestoredb.DB) *challengeStore {
	return &challengeStore{db: db}
}

// Replace atomically replaces the subject's live challenge for the purpose,
// releasing the displaced row's digest claim and taking the new one; a colliding
// digest is sdk.ErrAlreadyExists.
func (s *challengeStore) Replace(ctx context.Context, c challenge.Challenge) (challenge.Challenge, error) {
	if err := refuseAmbient(ctx); err != nil {
		return challenge.Challenge{}, err
	}
	return challenge.Challenge{}, errNotImplemented
}

// ConsumeCode evaluates the code in one transaction. Its ConsumeOutcome is
// AUTHORITATIVE and the error is infrastructure-only: attempt increments,
// lockout, expiry deletion, and context mismatch all COMMIT their writes and
// then report (N-D2). The zero outcome is OutcomeNotFound — fail closed.
func (s *challengeStore) ConsumeCode(ctx context.Context, userID, purpose string, candidates []challenge.DigestCandidate,
	expectedContextDigest string, maxAttempts int, now time.Time) (challenge.Consumed, challenge.ConsumeOutcome, error) {
	if err := refuseAmbient(ctx); err != nil {
		return challenge.Consumed{}, challenge.OutcomeNotFound, err
	}
	return challenge.Consumed{}, challenge.OutcomeNotFound, errNotImplemented
}

// ConsumeToken resolves (purpose, digest) through the digest claim and deletes
// the challenge with its claim. An expired token's DELETION COMMITS, then
// sdk.ErrExpired is returned.
func (s *challengeStore) ConsumeToken(ctx context.Context, purpose, presentedDigest string, now time.Time) (challenge.Consumed, error) {
	if err := refuseAmbient(ctx); err != nil {
		return challenge.Consumed{}, err
	}
	return challenge.Consumed{}, errNotImplemented
}

// PurgeExpired deletes up to limit challenges at or past before, WITH their
// digest claims, and returns the committed count. limit <= 0 is unbounded.
func (s *challengeStore) PurgeExpired(ctx context.Context, before time.Time, limit int) (int, error) {
	if err := refuseAmbient(ctx); err != nil {
		return 0, err
	}
	return 0, errNotImplemented
}
