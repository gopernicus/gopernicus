package turso

import (
	"context"
	"errors"
	"time"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passkey"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/totp"
	"github.com/gopernicus/gopernicus/sdk"
)

// totpMigration is the file that creates the totp_credentials and
// recovery_codes tables; ProbeSchema names it.
const totpMigration = "0026_totp.sql"

const totpColumns = "user_id, secret, session_id, tier, auth_revision, expires_at, confirmed_at, last_used_step, failed_attempts, locked_until, created_at, last_used_at"

// totpProbeTables are the tables migration 0026 creates. Repositories never
// probes them: they are required only when the host wires TOTP, and the pocket
// asks through TOTPStore.ProbeSchema (totp.SchemaProber) only then.
var totpProbeTables = []string{"totp_credentials", "recovery_codes"}

// TOTPStore implements totp.Repository over a libSQL database (SOC 2 S4c): one
// authenticator-app credential per user and its recovery codes. Confirm,
// Remove, ReplaceRecoveryCodes and ConsumeRecoveryCode each run in one BEGIN
// IMMEDIATE write transaction — this store's user-row lock, the one the
// credential-mutation rail takes; RecordUse and RecordFailure are single
// statements on totp_credentials and never read users.
type TOTPStore struct {
	db *tursodb.DB
}

var (
	_ totp.Repository   = (*TOTPStore)(nil)
	_ totp.SchemaProber = (*TOTPStore)(nil)
)

// NewTOTPStore returns a TOTPStore backed by db.
// It panics if db is nil; the caller owns the database lifecycle.
func NewTOTPStore(db *tursodb.DB) *TOTPStore {
	if db == nil {
		panic("authentication turso: NewTOTPStore received a nil database")
	}
	return &TOTPStore{db: db}
}

// totpRow is the store-local, db-tagged projection of a totp_credentials row.
type totpRow struct {
	UserID         string           `db:"user_id"`
	Secret         string           `db:"secret"`
	SessionID      string           `db:"session_id"`
	Tier           string           `db:"tier"`
	AuthRevision   int64            `db:"auth_revision"`
	ExpiresAt      tursodb.NullTime `db:"expires_at"`
	ConfirmedAt    tursodb.NullTime `db:"confirmed_at"`
	LastUsedStep   int64            `db:"last_used_step"`
	FailedAttempts int              `db:"failed_attempts"`
	LockedUntil    tursodb.NullTime `db:"locked_until"`
	CreatedAt      tursodb.Time     `db:"created_at"`
	LastUsedAt     tursodb.NullTime `db:"last_used_at"`
}

func (r totpRow) toDomain() totp.Credential {
	return totp.Credential{
		UserID:         r.UserID,
		Secret:         r.Secret,
		SessionID:      r.SessionID,
		Tier:           r.Tier,
		AuthRevision:   r.AuthRevision,
		ExpiresAt:      r.ExpiresAt.TimePtr(),
		ConfirmedAt:    r.ConfirmedAt.TimePtr(),
		LastUsedStep:   r.LastUsedStep,
		FailedAttempts: r.FailedAttempts,
		LockedUntil:    r.LockedUntil.TimePtr(),
		CreatedAt:      r.CreatedAt.Time,
		LastUsedAt:     r.LastUsedAt.TimePtr(),
	}
}

// failureRow is what RecordFailure's UPDATE … RETURNING reads back.
type failureRow struct {
	FailedAttempts int              `db:"failed_attempts"`
	LockedUntil    tursodb.NullTime `db:"locked_until"`
}

// ProbeSchema refuses a database without migration 0026's tables with
// sdk.ErrNotFound naming the file. The pocket calls it only when TOTP is wired.
func (s *TOTPStore) ProbeSchema(ctx context.Context) error {
	for _, table := range totpProbeTables {
		if err := probeMigrationTable(ctx, s.db, table, totpMigration); err != nil {
			return err
		}
	}
	return nil
}

// Begin upserts the pending row; a confirmed row → totp.ErrAlreadyEnrolled.
func (s *TOTPStore) Begin(ctx context.Context, c totp.Credential) error {
	n, err := tursodb.ExecAffecting(ctx, s.db, `INSERT INTO totp_credentials
		(user_id, secret, session_id, tier, auth_revision, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (user_id) DO UPDATE SET secret = excluded.secret, session_id = excluded.session_id, tier = excluded.tier,
			auth_revision = excluded.auth_revision, expires_at = excluded.expires_at, created_at = excluded.created_at,
			confirmed_at = NULL, last_used_step = 0, failed_attempts = 0, locked_until = NULL, last_used_at = NULL
		WHERE totp_credentials.confirmed_at IS NULL`,
		c.UserID, c.Secret, c.SessionID, c.Tier, c.AuthRevision, tursodb.FormatNullTimePtr(c.ExpiresAt), tursodb.FormatTime(c.CreatedAt))
	if err != nil {
		return tursodb.MapError(err)
	}
	if n == 0 {
		return totp.ErrAlreadyEnrolled
	}
	return nil
}

// Get returns userID's credential, pending or confirmed, or sdk.ErrNotFound.
func (s *TOTPStore) Get(ctx context.Context, userID string) (totp.Credential, error) {
	row, err := tursodb.QueryOne[totpRow](ctx, s.db, `SELECT `+totpColumns+` FROM totp_credentials WHERE user_id = ?`, userID)
	if err != nil {
		return totp.Credential{}, err
	}
	return row.toDomain(), nil
}

// Confirm confirms the pending row inside the write transaction. See
// totp.Repository.
func (s *TOTPStore) Confirm(ctx context.Context, in totp.Confirmation) error {
	return s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		revision, err := lockCredentialUser(ctx, tx, in.UserID)
		if err != nil {
			return err
		}
		var (
			secret, sessionID string
			confirmedAt       tursodb.NullTime
		)
		if err := tx.QueryRow(ctx, `SELECT secret, session_id, confirmed_at FROM totp_credentials WHERE user_id = ?`, in.UserID).
			Scan(&secret, &sessionID, &confirmedAt); err != nil {
			return tursodb.MapError(err)
		}
		switch {
		case confirmedAt.Valid:
			return totp.ErrAlreadyEnrolled
		case secret != in.ExpectedSecret || sessionID != in.ExpectedSessionID:
			return sdk.ErrNotFound
		case revision != in.ExpectedAuthRevision:
			return sdk.ErrConflict
		}
		if in.Bootstrap {
			closed, err := holdsAAL2Passkey(ctx, tx, in.UserID)
			if err != nil {
				return err
			}
			if closed {
				return totp.ErrBootstrapClosed
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE totp_credentials
			SET confirmed_at = ?, last_used_step = ?, session_id = '', tier = '', auth_revision = 0,
				expires_at = NULL, failed_attempts = 0, locked_until = NULL
			WHERE user_id = ?`, tursodb.FormatTime(in.ConfirmedAt), in.Step, in.UserID); err != nil {
			return tursodb.MapError(err)
		}
		if err := replaceCodes(ctx, tx, in.UserID, in.Codes); err != nil {
			return err
		}
		return bumpCredentialRevision(ctx, tx, in.UserID, time.Now())
	})
}

// holdsAAL2Passkey reports whether userID holds an AAL2-capable passkey.
func holdsAAL2Passkey(ctx context.Context, tx *tursodb.Tx, userID string) (bool, error) {
	rows, err := tx.Query(ctx, `SELECT user_verified FROM passkey_credentials WHERE user_id = ?`, userID)
	if err != nil {
		return false, tursodb.MapError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var verified tursodb.Bool
		if err := rows.Scan(&verified); err != nil {
			return false, tursodb.MapError(err)
		}
		if (passkey.Passkey{UserVerified: bool(verified)}).AAL2Capable() {
			return true, nil
		}
	}
	return false, tursodb.MapError(rows.Err())
}

// replaceCodes deletes every recovery code of userID and inserts codes; an
// empty ID takes the schema default.
func replaceCodes(ctx context.Context, tx *tursodb.Tx, userID string, codes []totp.RecoveryCode) error {
	if _, err := tx.Exec(ctx, `DELETE FROM recovery_codes WHERE user_id = ?`, userID); err != nil {
		return tursodb.MapError(err)
	}
	if len(codes) == 0 {
		return nil
	}
	q := `INSERT INTO recovery_codes (id, user_id, key_id, code_digest, generated_at) VALUES `
	args := make([]any, 0, 5*len(codes))
	for i, c := range codes {
		if i > 0 {
			q += ", "
		}
		q += `(COALESCE(NULLIF(?, ''), lower(hex(randomblob(16)))), ?, ?, ?, ?)`
		args = append(args, c.ID, userID, c.KeyID, c.Digest, tursodb.FormatTime(c.GeneratedAt))
	}
	_, err := tx.Exec(ctx, q, args...)
	return tursodb.MapError(err)
}

// RecordUse is the compare-and-swap on a confirmed, unlocked row. See
// totp.Repository.
func (s *TOTPStore) RecordUse(ctx context.Context, userID string, expectedStep, step int64, at time.Time) error {
	if step <= expectedStep {
		return sdk.ErrConflict
	}
	stamp := tursodb.FormatTime(at)
	n, err := tursodb.ExecAffecting(ctx, s.db, `UPDATE totp_credentials
		SET last_used_step = ?, last_used_at = ?, failed_attempts = 0, locked_until = NULL
		WHERE user_id = ? AND confirmed_at IS NOT NULL AND last_used_step = ?
			AND (locked_until IS NULL OR locked_until <= ?)`, step, stamp, userID, expectedStep, stamp)
	if err != nil {
		return tursodb.MapError(err)
	}
	if n == 0 {
		return sdk.ErrConflict
	}
	return nil
}

// RecordFailure is one atomic increment that reads the row it writes: the
// failure reaching lockAt sets the lock and resets the count in the same
// statement. See totp.Repository.
func (s *TOTPStore) RecordFailure(ctx context.Context, userID string, lockAt int, lockFor time.Duration, now time.Time) (totp.Failure, error) {
	row, err := tursodb.QueryOne[failureRow](ctx, s.db, `UPDATE totp_credentials
		SET failed_attempts = CASE WHEN failed_attempts + 1 >= ? THEN 0 ELSE failed_attempts + 1 END,
			locked_until = CASE WHEN failed_attempts + 1 >= ? THEN ? ELSE locked_until END
		WHERE user_id = ?
		RETURNING failed_attempts, locked_until`, lockAt, lockAt, tursodb.FormatTime(now.Add(lockFor)), userID)
	if err != nil {
		return totp.Failure{}, err
	}
	// A count back at zero is the failure that reached lockAt.
	if row.FailedAttempts == 0 {
		return totp.Failure{Attempts: lockAt, LockedUntil: row.LockedUntil.TimePtr()}, nil
	}
	return totp.Failure{Attempts: row.FailedAttempts}, nil
}

// confirmedIn reports whether userID's credential is confirmed; no row →
// sdk.ErrNotFound.
func confirmedIn(ctx context.Context, tx *tursodb.Tx, userID string) (bool, error) {
	var confirmedAt tursodb.NullTime
	if err := tx.QueryRow(ctx, `SELECT confirmed_at FROM totp_credentials WHERE user_id = ?`, userID).Scan(&confirmedAt); err != nil {
		return false, tursodb.MapError(err)
	}
	return confirmedAt.Valid, nil
}

// countCodes reads how many codes userID holds and the set's generation (nil
// when none remains).
func countCodes(ctx context.Context, q tursodb.Querier, userID string) (int, *time.Time, error) {
	var (
		n  int
		at tursodb.NullTime
	)
	if err := q.QueryRow(ctx, `SELECT COUNT(*), MIN(generated_at) FROM recovery_codes WHERE user_id = ?`, userID).Scan(&n, &at); err != nil {
		return 0, nil, tursodb.MapError(err)
	}
	return n, at.TimePtr(), nil
}

// ReplaceRecoveryCodes replaces the codes of a confirmed credential, as a
// compare-and-swap on the current generation when expectedGeneratedAt is set.
// See totp.Repository.
func (s *TOTPStore) ReplaceRecoveryCodes(ctx context.Context, userID string, codes []totp.RecoveryCode, expectedGeneratedAt *time.Time) error {
	return s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		confirmed, err := confirmedIn(ctx, tx, userID)
		if err != nil {
			return err
		}
		if !confirmed {
			return sdk.ErrNotFound
		}
		if expectedGeneratedAt != nil {
			_, current, err := countCodes(ctx, tx, userID)
			if err != nil {
				return err
			}
			if current == nil || !current.Equal(*expectedGeneratedAt) {
				return sdk.ErrConflict
			}
		}
		return replaceCodes(ctx, tx, userID, codes)
	})
}

// ConsumeRecoveryCode deletes the one code of userID matching a candidate and
// clears the credential's failures. See totp.Repository.
func (s *TOTPStore) ConsumeRecoveryCode(ctx context.Context, userID string, candidates []challenge.DigestCandidate, _ time.Time) (int, error) {
	if len(candidates) == 0 {
		return 0, sdk.ErrNotFound
	}
	match := ""
	args := []any{userID}
	for i, c := range candidates {
		if i > 0 {
			match += " OR "
		}
		match += "(key_id = ? AND code_digest = ?)"
		args = append(args, c.KeyID, c.Digest)
	}
	var remaining int
	err := s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		n, err := tursodb.ExecAffecting(ctx, tx, `DELETE FROM recovery_codes WHERE id = (
			SELECT id FROM recovery_codes WHERE user_id = ? AND (`+match+`) LIMIT 1)`, args...)
		if err != nil {
			return tursodb.MapError(err)
		}
		if n == 0 {
			return sdk.ErrNotFound
		}
		if _, err := tx.Exec(ctx, `UPDATE totp_credentials SET failed_attempts = 0 WHERE user_id = ?`, userID); err != nil {
			return tursodb.MapError(err)
		}
		remaining, _, err = countCodes(ctx, tx, userID)
		return err
	})
	if err != nil {
		return 0, err
	}
	return remaining, nil
}

// CountRecoveryCodes returns how many codes userID holds and their generation.
func (s *TOTPStore) CountRecoveryCodes(ctx context.Context, userID string) (int, *time.Time, error) {
	return countCodes(ctx, s.db, userID)
}

// Remove deletes the credential inside the write transaction. See
// totp.Repository.
func (s *TOTPStore) Remove(ctx context.Context, userID string, expectedRevision int64) (totp.Removed, error) {
	var out totp.Removed
	err := s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		// The users row, as lockCredentialUser reads it, without its active
		// check: an administrator resets a deactivated user's TOTP too.
		var revision int64
		if err := tx.QueryRow(ctx, `SELECT auth_revision FROM users WHERE id = ?`, userID).Scan(&revision); err != nil {
			return tursodb.MapError(err)
		}
		if revision != expectedRevision {
			return sdk.ErrConflict
		}
		confirmed, err := confirmedIn(ctx, tx, userID)
		if errors.Is(err, sdk.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if !confirmed {
			if _, err := tx.Exec(ctx, `DELETE FROM totp_credentials WHERE user_id = ?`, userID); err != nil {
				return tursodb.MapError(err)
			}
			out = totp.Removed{Pending: true}
			return nil
		}
		if _, err := tx.Exec(ctx, `DELETE FROM totp_credentials WHERE user_id = ?`, userID); err != nil {
			return tursodb.MapError(err)
		}
		n, err := tursodb.ExecAffecting(ctx, tx, `DELETE FROM recovery_codes WHERE user_id = ?`, userID)
		if err != nil {
			return tursodb.MapError(err)
		}
		if err := bumpCredentialRevision(ctx, tx, userID, time.Now()); err != nil {
			return err
		}
		if err := revokeCredentialState(ctx, tx, userID); err != nil {
			return err
		}
		out = totp.Removed{Credential: true, RecoveryCodes: int(n)}
		return nil
	})
	if err != nil {
		return totp.Removed{}, err
	}
	return out, nil
}

// RecoveryCodeKeyIDs returns the distinct key ids recovery codes are stored
// under, across every user.
func (s *TOTPStore) RecoveryCodeKeyIDs(ctx context.Context) ([]string, error) {
	rows, err := s.db.Query(ctx, `SELECT DISTINCT key_id FROM recovery_codes ORDER BY key_id`)
	if err != nil {
		return nil, tursodb.MapError(err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, tursodb.MapError(err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, tursodb.MapError(err)
	}
	return out, nil
}
