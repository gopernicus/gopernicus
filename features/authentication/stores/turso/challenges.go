package turso

import (
	"context"
	"database/sql"
	"errors"
	"time"

	auth "github.com/gopernicus/gopernicus/features/authentication"
	"github.com/gopernicus/gopernicus/features/authentication/domain/challenge"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk"
)

// ChallengeStore implements challenge.Repository over a libSQL database
// (design §3.2). The plaintext secret is never persisted — only secret_digest.
// Replace atomically deletes the prior (user, purpose) row and inserts the new
// one; the (purpose, secret_digest) unique index maps a colliding claim to
// sdk.ErrAlreadyExists. libSQL/SQLite has no FOR UPDATE, so ConsumeCode holds the
// read/compare/attempt-or-delete sequence inside one write transaction and relies
// on the connector's serialized writes: exactly one correct concurrent request
// commits its consuming DELETE and every loser's transaction aborts, so ConsumeCode
// reports OutcomeNotFound (the fail-closed zero value) on any infrastructure error.
// ConsumeToken is one atomic DELETE ... RETURNING by (purpose, secret_digest).
type ChallengeStore struct {
	db *tursodb.DB
}

var _ challenge.Repository = (*ChallengeStore)(nil)

// NewChallengeStore returns a ChallengeStore backed by db.
func NewChallengeStore(db *tursodb.DB) *ChallengeStore {
	return &ChallengeStore{db: db}
}

// nullText maps an empty string to a SQL NULL (protector_key_id has no pepper for
// tokens; a NULL context is the absent binding), for the write side.
func nullText(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// nullBytes maps a zero-length binding blob to a SQL NULL; a non-empty blob is
// stored as its opaque text (an already-digested validator, never a secret).
func nullBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}

// bytesFrom maps a scanned nullable TEXT back to the domain's binding blob (nil
// for NULL).
func bytesFrom(ns sql.NullString) []byte {
	if !ns.Valid {
		return nil
	}
	return []byte(ns.String)
}

// Replace atomically replaces the prior (user, purpose) challenge with c and
// returns the stored row. A colliding (purpose, secret_digest) → sdk.ErrAlreadyExists.
func (s *ChallengeStore) Replace(ctx context.Context, c challenge.Challenge) (challenge.Challenge, error) {
	if c.Version == 0 {
		c.Version = 1
	}
	err := s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		if _, err := tx.Exec(ctx,
			`DELETE FROM challenges WHERE user_id = ? AND purpose = ?`, c.UserID, c.Purpose); err != nil {
			return err
		}
		args := []any{
			c.UserID,
			c.Purpose,
			c.SecretDigest,
			nullText(c.ProtectorKeyID),
			nullBytes(c.Context),
			c.AttemptCount,
			tursodb.FormatTime(c.ExpiresAt),
			tursodb.FormatTime(c.CreatedAt),
			c.Version,
		}
		if c.ID == "" {
			const insert = `INSERT INTO challenges
				(user_id, purpose, secret_digest, protector_key_id, context, attempt_count, expires_at, created_at, version)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
				RETURNING id`
			if err := tx.QueryRow(ctx, insert, args...).Scan(&c.ID); err != nil {
				return tursodb.MapError(err)
			}
			return nil
		}
		const insert = `INSERT INTO challenges
			(id, user_id, purpose, secret_digest, protector_key_id, context, attempt_count, expires_at, created_at, version)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
		if _, err := tx.Exec(ctx, insert, append([]any{c.ID}, args...)...); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return challenge.Challenge{}, err
	}
	return c, nil
}

// ConsumeCode atomically evaluates the (userID, purpose) code challenge against
// candidates within one write transaction: the ConsumeOutcome is authoritative and
// error is infrastructure-only (a lost write race surfaces as OutcomeNotFound).
func (s *ChallengeStore) ConsumeCode(ctx context.Context, userID, purpose string, candidates []challenge.DigestCandidate,
	expectedContextDigest string, maxAttempts int, now time.Time) (challenge.Consumed, challenge.ConsumeOutcome, error) {
	var (
		consumed challenge.Consumed
		outcome  challenge.ConsumeOutcome
	)
	err := s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		var (
			id             string
			secretDigest   string
			protectorKeyID sql.NullString
			contextText    sql.NullString
			attemptCount   int
			expiresAt      tursodb.Time
		)
		selErr := tx.QueryRow(ctx,
			`SELECT id, secret_digest, protector_key_id, context, attempt_count, expires_at
				FROM challenges WHERE user_id = ? AND purpose = ?`, userID, purpose).
			Scan(&id, &secretDigest, &protectorKeyID, &contextText, &attemptCount, &expiresAt)
		if selErr != nil {
			if errors.Is(selErr, sql.ErrNoRows) {
				outcome = challenge.OutcomeNotFound
				return nil
			}
			return tursodb.MapError(selErr)
		}

		if !now.Before(expiresAt.Time) {
			if _, err := tx.Exec(ctx, `DELETE FROM challenges WHERE id = ?`, id); err != nil {
				return err
			}
			outcome = challenge.OutcomeExpired
			return nil
		}

		keyID := protectorKeyID.String
		matched := false
		for _, cand := range candidates {
			if cand.KeyID == keyID && auth.ConstantTimeDigestEqual(cand.Digest, secretDigest) {
				matched = true
				break
			}
		}
		if !matched {
			newCount := attemptCount + 1
			if newCount >= maxAttempts {
				if _, err := tx.Exec(ctx, `DELETE FROM challenges WHERE id = ?`, id); err != nil {
					return err
				}
				outcome = challenge.OutcomeLockedOut
				return nil
			}
			if _, err := tx.Exec(ctx, `UPDATE challenges SET attempt_count = ? WHERE id = ?`, newCount, id); err != nil {
				return err
			}
			outcome = challenge.OutcomeRejected
			return nil
		}

		// Correct code — the row is consumed regardless of context (anti-probing).
		if _, err := tx.Exec(ctx, `DELETE FROM challenges WHERE id = ?`, id); err != nil {
			return err
		}
		consumed = challenge.Consumed{
			ID:             id,
			UserID:         userID,
			Purpose:        purpose,
			Context:        bytesFrom(contextText),
			ProtectorKeyID: keyID,
			ConsumedAt:     now.UTC(),
		}
		if expectedContextDigest != "" && contextText.String != expectedContextDigest {
			outcome = challenge.OutcomeContextMismatch
			return nil
		}
		outcome = challenge.OutcomeRedeemed
		return nil
	})
	if err != nil {
		return challenge.Consumed{}, challenge.OutcomeNotFound, err
	}
	return consumed, outcome, nil
}

// ConsumeToken atomically deletes and returns the (purpose, presentedDigest) token
// row: empty → sdk.ErrNotFound, expired → sdk.ErrExpired (row deleted), live → the
// Consumed row.
func (s *ChallengeStore) ConsumeToken(ctx context.Context, purpose, presentedDigest string, now time.Time) (challenge.Consumed, error) {
	if presentedDigest == "" {
		return challenge.Consumed{}, sdk.ErrNotFound
	}
	var (
		id             string
		userID         string
		gotPurpose     string
		contextText    sql.NullString
		protectorKeyID sql.NullString
		expiresAt      tursodb.Time
	)
	const q = `DELETE FROM challenges WHERE purpose = ? AND secret_digest = ?
		RETURNING id, user_id, purpose, context, protector_key_id, expires_at`
	err := s.db.QueryRow(ctx, q, purpose, presentedDigest).
		Scan(&id, &userID, &gotPurpose, &contextText, &protectorKeyID, &expiresAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return challenge.Consumed{}, sdk.ErrNotFound
		}
		return challenge.Consumed{}, tursodb.MapError(err)
	}
	if !now.Before(expiresAt.Time) {
		return challenge.Consumed{}, sdk.ErrExpired
	}
	return challenge.Consumed{
		ID:             id,
		UserID:         userID,
		Purpose:        gotPurpose,
		Context:        bytesFrom(contextText),
		ProtectorKeyID: protectorKeyID.String,
		ConsumedAt:     now.UTC(),
	}, nil
}

// PurgeExpired deletes up to limit rows at or past before and returns the number
// removed (bounded batching; limit <= 0 is unbounded).
func (s *ChallengeStore) PurgeExpired(ctx context.Context, before time.Time, limit int) (int, error) {
	args := []any{tursodb.FormatTime(before)}
	q := `DELETE FROM challenges WHERE expires_at <= ?`
	if limit > 0 {
		q = `DELETE FROM challenges WHERE id IN (
			SELECT id FROM challenges WHERE expires_at <= ? ORDER BY expires_at LIMIT ?)`
		args = append(args, limit)
	}
	n, err := tursodb.ExecAffecting(ctx, s.db, q, args...)
	if err != nil {
		return 0, err
	}
	return int(n), nil
}
