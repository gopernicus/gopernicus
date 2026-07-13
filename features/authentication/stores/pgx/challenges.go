package pgx

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	auth "github.com/gopernicus/gopernicus/features/authentication"
	"github.com/gopernicus/gopernicus/features/authentication/domain/challenge"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk"
)

// ChallengeStore implements challenge.Repository over a PostgreSQL database
// (design §3.2). The plaintext secret is never persisted — only secret_digest.
// Replace atomically deletes the prior (user, purpose) row and inserts the new
// one; the (purpose, secret_digest) unique index maps a colliding claim to
// sdk.ErrAlreadyExists. ConsumeCode locks the (user, purpose) row FOR UPDATE and
// decides expiry, digest comparison, attempt counting, lockout, and consumption
// inside one transaction, so exactly one correct concurrent request wins.
// ConsumeToken is one atomic DELETE ... RETURNING by (purpose, secret_digest).
type ChallengeStore struct {
	db *pgxdb.DB
}

var _ challenge.Repository = (*ChallengeStore)(nil)

// NewChallengeStore returns a ChallengeStore backed by db.
func NewChallengeStore(db *pgxdb.DB) *ChallengeStore {
	return &ChallengeStore{db: db}
}

// nullText maps an empty string to a SQL NULL (protector_key_id has no pepper for
// tokens; a NULL context is the absent binding), the read-side twin below.
func nullText(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// nullBytes maps a zero-length binding blob to a SQL NULL; a non-empty blob is
// stored as its opaque text (an already-digested validator, never a secret).
func nullBytes(b []byte) *string {
	if len(b) == 0 {
		return nil
	}
	s := string(b)
	return &s
}

// textFrom maps a scanned nullable TEXT back to a string ("" for NULL).
func textFrom(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// bytesFrom maps a scanned nullable TEXT back to the domain's binding blob (nil
// for NULL).
func bytesFrom(p *string) []byte {
	if p == nil {
		return nil
	}
	return []byte(*p)
}

// Replace atomically replaces the prior (user, purpose) challenge with c and
// returns the stored row. A colliding (purpose, secret_digest) → sdk.ErrAlreadyExists.
func (s *ChallengeStore) Replace(ctx context.Context, c challenge.Challenge) (challenge.Challenge, error) {
	if c.Version == 0 {
		c.Version = 1
	}
	err := s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		if _, err := tx.Exec(ctx,
			`DELETE FROM challenges WHERE user_id = @user_id AND purpose = @purpose`,
			pgx.NamedArgs{"user_id": c.UserID, "purpose": c.Purpose}); err != nil {
			return err
		}
		args := pgx.NamedArgs{
			"user_id":          c.UserID,
			"purpose":          c.Purpose,
			"secret_digest":    c.SecretDigest,
			"protector_key_id": nullText(c.ProtectorKeyID),
			"context":          nullBytes(c.Context),
			"attempt_count":    c.AttemptCount,
			"expires_at":       c.ExpiresAt.UTC(),
			"created_at":       c.CreatedAt.UTC(),
			"version":          c.Version,
		}
		if c.ID == "" {
			const insert = `INSERT INTO challenges
				(user_id, purpose, secret_digest, protector_key_id, context, attempt_count, expires_at, created_at, version)
				VALUES (@user_id, @purpose, @secret_digest, @protector_key_id, @context, @attempt_count, @expires_at, @created_at, @version)
				RETURNING id`
			if err := tx.QueryRow(ctx, insert, args).Scan(&c.ID); err != nil {
				return pgxdb.MapError(err)
			}
			return nil
		}
		args["id"] = c.ID
		const insert = `INSERT INTO challenges
			(id, user_id, purpose, secret_digest, protector_key_id, context, attempt_count, expires_at, created_at, version)
			VALUES (@id, @user_id, @purpose, @secret_digest, @protector_key_id, @context, @attempt_count, @expires_at, @created_at, @version)`
		if _, err := tx.Exec(ctx, insert, args); err != nil {
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
// candidates within one FOR UPDATE transaction: the ConsumeOutcome is
// authoritative and error is infrastructure-only.
func (s *ChallengeStore) ConsumeCode(ctx context.Context, userID, purpose string, candidates []challenge.DigestCandidate,
	expectedContextDigest string, maxAttempts int, now time.Time) (challenge.Consumed, challenge.ConsumeOutcome, error) {
	var (
		consumed challenge.Consumed
		outcome  challenge.ConsumeOutcome
	)
	err := s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		var (
			id             string
			secretDigest   string
			protectorKeyID *string
			contextText    *string
			attemptCount   int
			expiresAt      time.Time
		)
		selErr := tx.QueryRow(ctx,
			`SELECT id, secret_digest, protector_key_id, context, attempt_count, expires_at
				FROM challenges WHERE user_id = @user_id AND purpose = @purpose FOR UPDATE`,
			pgx.NamedArgs{"user_id": userID, "purpose": purpose}).
			Scan(&id, &secretDigest, &protectorKeyID, &contextText, &attemptCount, &expiresAt)
		if selErr != nil {
			if selErr == pgx.ErrNoRows {
				outcome = challenge.OutcomeNotFound
				return nil
			}
			return pgxdb.MapError(selErr)
		}

		if !now.Before(expiresAt) {
			if _, err := tx.Exec(ctx, `DELETE FROM challenges WHERE id = @id`, pgx.NamedArgs{"id": id}); err != nil {
				return err
			}
			outcome = challenge.OutcomeExpired
			return nil
		}

		keyID := textFrom(protectorKeyID)
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
				if _, err := tx.Exec(ctx, `DELETE FROM challenges WHERE id = @id`, pgx.NamedArgs{"id": id}); err != nil {
					return err
				}
				outcome = challenge.OutcomeLockedOut
				return nil
			}
			if _, err := tx.Exec(ctx,
				`UPDATE challenges SET attempt_count = @count WHERE id = @id`,
				pgx.NamedArgs{"count": newCount, "id": id}); err != nil {
				return err
			}
			outcome = challenge.OutcomeRejected
			return nil
		}

		// Correct code — the row is consumed regardless of context (anti-probing).
		if _, err := tx.Exec(ctx, `DELETE FROM challenges WHERE id = @id`, pgx.NamedArgs{"id": id}); err != nil {
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
		if expectedContextDigest != "" && textFrom(contextText) != expectedContextDigest {
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
		contextText    *string
		protectorKeyID *string
		expiresAt      time.Time
	)
	const q = `DELETE FROM challenges WHERE purpose = @purpose AND secret_digest = @digest
		RETURNING id, user_id, purpose, context, protector_key_id, expires_at`
	err := s.db.QueryRow(ctx, q, pgx.NamedArgs{"purpose": purpose, "digest": presentedDigest}).
		Scan(&id, &userID, &gotPurpose, &contextText, &protectorKeyID, &expiresAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return challenge.Consumed{}, sdk.ErrNotFound
		}
		return challenge.Consumed{}, pgxdb.MapError(err)
	}
	if !now.Before(expiresAt) {
		return challenge.Consumed{}, sdk.ErrExpired
	}
	return challenge.Consumed{
		ID:             id,
		UserID:         userID,
		Purpose:        gotPurpose,
		Context:        bytesFrom(contextText),
		ProtectorKeyID: textFrom(protectorKeyID),
		ConsumedAt:     now.UTC(),
	}, nil
}

// PurgeExpired deletes up to limit rows at or past before and returns the number
// removed (bounded batching; limit <= 0 is unbounded).
func (s *ChallengeStore) PurgeExpired(ctx context.Context, before time.Time, limit int) (int, error) {
	args := pgx.NamedArgs{"before": before.UTC()}
	q := `DELETE FROM challenges WHERE expires_at <= @before`
	if limit > 0 {
		q = `DELETE FROM challenges WHERE id IN (
			SELECT id FROM challenges WHERE expires_at <= @before ORDER BY expires_at LIMIT @limit)`
		args["limit"] = limit
	}
	n, err := pgxdb.ExecAffecting(ctx, s.db, q, args)
	if err != nil {
		return 0, err
	}
	return int(n), nil
}
