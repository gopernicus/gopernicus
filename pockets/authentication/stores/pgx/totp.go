package pgx

import (
	"context"
	"errors"
	"time"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passkey"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/totp"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/jackc/pgx/v5"
)

// totpMigration is the file that creates totpCredentialsTable and
// recoveryCodesTable; ProbeSchema names it.
const totpMigration = "0026_totp.sql"

const totpColumns = "user_id, secret, session_id, tier, auth_revision, expires_at, confirmed_at, last_used_step, failed_attempts, locked_until, created_at, last_used_at"

// totpProbeTables are the tables migration 0026 creates. Repositories never
// probes them: they are required only when the host wires TOTP, and the pocket
// asks through TOTPStore.ProbeSchema (totp.SchemaProber) only then.
var totpProbeTables = []string{totpCredentialsTable, recoveryCodesTable}

// TOTPStore implements totp.Repository over a PostgreSQL database (SOC 2 S4c):
// one authenticator-app credential per user and its recovery codes. Confirm and
// Remove take the owner's users row lock FIRST — the lock the
// credential-mutation rail takes — then the credential row; ReplaceRecoveryCodes
// and ConsumeRecoveryCode take the credential row before the codes; RecordUse
// and RecordFailure are single statements on totp_credentials and never read
// users.
type TOTPStore struct {
	db *pgxdb.DB
	qualified
}

var (
	_ totp.Repository   = (*TOTPStore)(nil)
	_ totp.SchemaProber = (*TOTPStore)(nil)
)

// NewTOTPStore returns a TOTPStore backed by db.
// It panics if db is nil; the caller owns the database lifecycle.
func NewTOTPStore(db *pgxdb.DB, opts ...Option) *TOTPStore {
	if db == nil {
		panic("authentication pgx: NewTOTPStore received a nil database")
	}
	return &TOTPStore{db: db, qualified: qualified{schema: applyOptions(opts).schema}}
}

// totpRow is the store-local, db-tagged projection of a totp_credentials row.
type totpRow struct {
	UserID         string     `db:"user_id"`
	Secret         string     `db:"secret"`
	SessionID      string     `db:"session_id"`
	Tier           string     `db:"tier"`
	AuthRevision   int64      `db:"auth_revision"`
	ExpiresAt      *time.Time `db:"expires_at"`
	ConfirmedAt    *time.Time `db:"confirmed_at"`
	LastUsedStep   int64      `db:"last_used_step"`
	FailedAttempts int        `db:"failed_attempts"`
	LockedUntil    *time.Time `db:"locked_until"`
	CreatedAt      time.Time  `db:"created_at"`
	LastUsedAt     *time.Time `db:"last_used_at"`
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	at := t.UTC()
	return &at
}

func (r totpRow) toDomain() totp.Credential {
	return totp.Credential{
		UserID:         r.UserID,
		Secret:         r.Secret,
		SessionID:      r.SessionID,
		Tier:           r.Tier,
		AuthRevision:   r.AuthRevision,
		ExpiresAt:      utcPtr(r.ExpiresAt),
		ConfirmedAt:    utcPtr(r.ConfirmedAt),
		LastUsedStep:   r.LastUsedStep,
		FailedAttempts: r.FailedAttempts,
		LockedUntil:    utcPtr(r.LockedUntil),
		CreatedAt:      r.CreatedAt.UTC(),
		LastUsedAt:     utcPtr(r.LastUsedAt),
	}
}

// ProbeSchema refuses a database without migration 0026's tables with
// sdk.ErrNotFound naming the file. The pocket calls it only when TOTP is wired.
func (s *TOTPStore) ProbeSchema(ctx context.Context) error {
	for _, table := range totpProbeTables {
		if err := s.probeMigrationTable(ctx, s.db, table, totpMigration); err != nil {
			return err
		}
	}
	return nil
}

// Begin upserts the pending row; a confirmed row → totp.ErrAlreadyEnrolled.
func (s *TOTPStore) Begin(ctx context.Context, c totp.Credential) error {
	n, err := pgxdb.ExecAffecting(ctx, s.db, `INSERT INTO `+s.table(totpCredentialsTable)+`
		(user_id, secret, session_id, tier, auth_revision, expires_at, created_at)
		VALUES (@user_id, @secret, @session_id, @tier, @auth_revision, @expires_at, @created_at)
		ON CONFLICT (user_id) DO UPDATE SET secret = excluded.secret, session_id = excluded.session_id, tier = excluded.tier,
			auth_revision = excluded.auth_revision, expires_at = excluded.expires_at, created_at = excluded.created_at,
			confirmed_at = NULL, last_used_step = 0, failed_attempts = 0, locked_until = NULL, last_used_at = NULL
		WHERE `+s.table(totpCredentialsTable)+`.confirmed_at IS NULL`, pgx.NamedArgs{
		"user_id": c.UserID, "secret": c.Secret, "session_id": c.SessionID, "tier": c.Tier,
		"auth_revision": c.AuthRevision, "expires_at": utcPtr(c.ExpiresAt), "created_at": c.CreatedAt.UTC(),
	})
	if err != nil {
		return pgxdb.MapError(err)
	}
	if n == 0 {
		return totp.ErrAlreadyEnrolled
	}
	return nil
}

// Get returns userID's credential, pending or confirmed, or sdk.ErrNotFound.
func (s *TOTPStore) Get(ctx context.Context, userID string) (totp.Credential, error) {
	row, err := pgxdb.QueryOne[totpRow](ctx, s.db, `SELECT `+totpColumns+` FROM `+s.table(totpCredentialsTable)+` WHERE user_id = @user_id`,
		pgx.NamedArgs{"user_id": userID})
	if err != nil {
		return totp.Credential{}, err
	}
	return row.toDomain(), nil
}

// Confirm confirms the pending row under the owner's users row lock. See
// totp.Repository.
func (s *TOTPStore) Confirm(ctx context.Context, in totp.Confirmation) error {
	return s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		revision, err := lockCredentialUser(ctx, tx, s.qualified, in.UserID)
		if err != nil {
			return err
		}
		var (
			secret, sessionID string
			confirmedAt       *time.Time
		)
		err = tx.QueryRow(ctx, `SELECT secret, session_id, confirmed_at FROM `+s.table(totpCredentialsTable)+` WHERE user_id = @user_id FOR UPDATE`,
			pgx.NamedArgs{"user_id": in.UserID}).Scan(&secret, &sessionID, &confirmedAt)
		if err != nil {
			return pgxdb.MapError(err)
		}
		switch {
		case confirmedAt != nil:
			return totp.ErrAlreadyEnrolled
		case secret != in.ExpectedSecret || sessionID != in.ExpectedSessionID:
			return sdk.ErrNotFound
		case revision != in.ExpectedAuthRevision:
			return sdk.ErrConflict
		}
		if in.Bootstrap {
			verified, err := pgxdb.Collect[passkeyOwned](ctx, tx, `SELECT user_verified, user_handle FROM `+s.table(passkeyCredentialsTable)+` WHERE user_id = @user_id`,
				pgx.NamedArgs{"user_id": in.UserID})
			if err != nil {
				return err
			}
			for _, p := range verified {
				if (passkey.Passkey{UserVerified: p.UserVerified}).AAL2Capable() {
					return totp.ErrBootstrapClosed
				}
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE `+s.table(totpCredentialsTable)+`
			SET confirmed_at = @confirmed_at, last_used_step = @step, session_id = '', tier = '', auth_revision = 0,
				expires_at = NULL, failed_attempts = 0, locked_until = NULL
			WHERE user_id = @user_id`, pgx.NamedArgs{"confirmed_at": in.ConfirmedAt.UTC(), "step": in.Step, "user_id": in.UserID}); err != nil {
			return pgxdb.MapError(err)
		}
		if err := s.replaceCodes(ctx, tx, in.UserID, in.Codes); err != nil {
			return err
		}
		return bumpCredentialRevision(ctx, tx, s.qualified, in.UserID, time.Now())
	})
}

// replaceCodes deletes every recovery code of userID and inserts codes in one
// statement; an empty ID takes the schema default.
func (s *TOTPStore) replaceCodes(ctx context.Context, tx *pgxdb.Tx, userID string, codes []totp.RecoveryCode) error {
	if _, err := tx.Exec(ctx, `DELETE FROM `+s.table(recoveryCodesTable)+` WHERE user_id = @user_id`, pgx.NamedArgs{"user_id": userID}); err != nil {
		return pgxdb.MapError(err)
	}
	if len(codes) == 0 {
		return nil
	}
	ids := make([]string, len(codes))
	keys := make([]string, len(codes))
	digests := make([]string, len(codes))
	generated := make([]time.Time, len(codes))
	for i, c := range codes {
		ids[i], keys[i], digests[i], generated[i] = c.ID, c.KeyID, c.Digest, c.GeneratedAt.UTC()
	}
	_, err := tx.Exec(ctx, `INSERT INTO `+s.table(recoveryCodesTable)+` (id, user_id, key_id, code_digest, generated_at)
		SELECT COALESCE(NULLIF(c.id, ''), gen_random_uuid()::text), @user_id, c.key_id, c.digest, c.generated_at
		FROM unnest(@ids::text[], @keys::text[], @digests::text[], @generated::timestamptz[]) AS c(id, key_id, digest, generated_at)`,
		pgx.NamedArgs{"user_id": userID, "ids": ids, "keys": keys, "digests": digests, "generated": generated})
	return pgxdb.MapError(err)
}

// RecordUse is the compare-and-swap on a confirmed, unlocked row. See
// totp.Repository.
func (s *TOTPStore) RecordUse(ctx context.Context, userID string, expectedStep, step int64, at time.Time) error {
	if step <= expectedStep {
		return sdk.ErrConflict
	}
	n, err := pgxdb.ExecAffecting(ctx, s.db, `UPDATE `+s.table(totpCredentialsTable)+`
		SET last_used_step = @step, last_used_at = @at, failed_attempts = 0, locked_until = NULL
		WHERE user_id = @user_id AND confirmed_at IS NOT NULL AND last_used_step = @expected
			AND (locked_until IS NULL OR locked_until <= @at)`,
		pgx.NamedArgs{"step": step, "at": at.UTC(), "user_id": userID, "expected": expectedStep})
	if err != nil {
		return pgxdb.MapError(err)
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
	var (
		attempts int
		locked   bool
		until    *time.Time
	)
	err := s.db.QueryRow(ctx, `UPDATE `+s.table(totpCredentialsTable)+`
		SET failed_attempts = CASE WHEN failed_attempts + 1 >= @lock_at THEN 0 ELSE failed_attempts + 1 END,
			locked_until = CASE WHEN failed_attempts + 1 >= @lock_at THEN @until ELSE locked_until END
		WHERE user_id = @user_id
		RETURNING failed_attempts, failed_attempts = 0, locked_until`,
		pgx.NamedArgs{"lock_at": lockAt, "until": now.Add(lockFor).UTC(), "user_id": userID}).Scan(&attempts, &locked, &until)
	if err != nil {
		return totp.Failure{}, pgxdb.MapError(err)
	}
	if locked {
		return totp.Failure{Attempts: lockAt, LockedUntil: utcPtr(until)}, nil
	}
	return totp.Failure{Attempts: attempts}, nil
}

// lockConfirmed row-locks userID's credential and reports whether it is
// confirmed; no row → sdk.ErrNotFound.
func (s *TOTPStore) lockConfirmed(ctx context.Context, tx *pgxdb.Tx, userID string) (bool, error) {
	var confirmedAt *time.Time
	err := tx.QueryRow(ctx, `SELECT confirmed_at FROM `+s.table(totpCredentialsTable)+` WHERE user_id = @user_id FOR UPDATE`,
		pgx.NamedArgs{"user_id": userID}).Scan(&confirmedAt)
	if err != nil {
		return false, pgxdb.MapError(err)
	}
	return confirmedAt != nil, nil
}

// generatedAt reads the current set's generation (nil when no code remains).
func (s *TOTPStore) generatedAt(ctx context.Context, q pgxdb.Querier, userID string) (int, *time.Time, error) {
	var (
		n  int
		at *time.Time
	)
	err := q.QueryRow(ctx, `SELECT COUNT(*), MIN(generated_at) FROM `+s.table(recoveryCodesTable)+` WHERE user_id = @user_id`,
		pgx.NamedArgs{"user_id": userID}).Scan(&n, &at)
	if err != nil {
		return 0, nil, pgxdb.MapError(err)
	}
	return n, utcPtr(at), nil
}

// ReplaceRecoveryCodes replaces the codes of a confirmed credential, as a
// compare-and-swap on the current generation when expectedGeneratedAt is set.
// See totp.Repository.
func (s *TOTPStore) ReplaceRecoveryCodes(ctx context.Context, userID string, codes []totp.RecoveryCode, expectedGeneratedAt *time.Time) error {
	return s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		confirmed, err := s.lockConfirmed(ctx, tx, userID)
		if err != nil {
			return err
		}
		if !confirmed {
			return sdk.ErrNotFound
		}
		if expectedGeneratedAt != nil {
			_, current, err := s.generatedAt(ctx, tx, userID)
			if err != nil {
				return err
			}
			if current == nil || !current.Equal(*expectedGeneratedAt) {
				return sdk.ErrConflict
			}
		}
		return s.replaceCodes(ctx, tx, userID, codes)
	})
}

// ConsumeRecoveryCode deletes the one code of userID matching a candidate and
// clears the credential's failures. See totp.Repository.
func (s *TOTPStore) ConsumeRecoveryCode(ctx context.Context, userID string, candidates []challenge.DigestCandidate, _ time.Time) (int, error) {
	keys := make([]string, len(candidates))
	digests := make([]string, len(candidates))
	for i, c := range candidates {
		keys[i], digests[i] = c.KeyID, c.Digest
	}
	var remaining int
	err := s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		// The credential row first, as ReplaceRecoveryCodes takes it.
		if _, err := tx.Exec(ctx, `UPDATE `+s.table(totpCredentialsTable)+` SET failed_attempts = 0 WHERE user_id = @user_id`,
			pgx.NamedArgs{"user_id": userID}); err != nil {
			return pgxdb.MapError(err)
		}
		table := s.table(recoveryCodesTable)
		n, err := pgxdb.ExecAffecting(ctx, tx, `DELETE FROM `+table+` WHERE id = (
				SELECT id FROM `+table+` WHERE user_id = @user_id
					AND (key_id, code_digest) IN (SELECT * FROM unnest(@keys::text[], @digests::text[]))
				LIMIT 1)`, pgx.NamedArgs{"user_id": userID, "keys": keys, "digests": digests})
		if err != nil {
			return pgxdb.MapError(err)
		}
		if n == 0 {
			return sdk.ErrNotFound
		}
		remaining, _, err = s.generatedAt(ctx, tx, userID)
		return err
	})
	if err != nil {
		return 0, err
	}
	return remaining, nil
}

// CountRecoveryCodes returns how many codes userID holds and their generation.
func (s *TOTPStore) CountRecoveryCodes(ctx context.Context, userID string) (int, *time.Time, error) {
	return s.generatedAt(ctx, s.db, userID)
}

// Remove deletes the credential under the owner's users row lock. See
// totp.Repository.
func (s *TOTPStore) Remove(ctx context.Context, userID string, expectedRevision int64) (totp.Removed, error) {
	var out totp.Removed
	err := s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		// The users row, as lockCredentialUser takes it, without its active
		// check: an administrator resets a deactivated user's TOTP too.
		var revision int64
		if err := tx.QueryRow(ctx, `SELECT auth_revision FROM `+s.table(usersTable)+` WHERE id = @id FOR UPDATE`,
			pgx.NamedArgs{"id": userID}).Scan(&revision); err != nil {
			return pgxdb.MapError(err)
		}
		if revision != expectedRevision {
			return sdk.ErrConflict
		}
		confirmed, err := s.lockConfirmed(ctx, tx, userID)
		if errors.Is(err, sdk.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		args := pgx.NamedArgs{"user_id": userID}
		if !confirmed {
			if _, err := tx.Exec(ctx, `DELETE FROM `+s.table(totpCredentialsTable)+` WHERE user_id = @user_id`, args); err != nil {
				return pgxdb.MapError(err)
			}
			out = totp.Removed{Pending: true}
			return nil
		}
		n, err := pgxdb.ExecAffecting(ctx, tx, `DELETE FROM `+s.table(recoveryCodesTable)+` WHERE user_id = @user_id`, args)
		if err != nil {
			return pgxdb.MapError(err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM `+s.table(totpCredentialsTable)+` WHERE user_id = @user_id`, args); err != nil {
			return pgxdb.MapError(err)
		}
		if err := bumpCredentialRevision(ctx, tx, s.qualified, userID, time.Now()); err != nil {
			return err
		}
		if err := revokeCredentialState(ctx, tx, s.qualified, userID); err != nil {
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
	rows, err := s.db.Query(ctx, `SELECT DISTINCT key_id FROM `+s.table(recoveryCodesTable)+` ORDER BY key_id`)
	if err != nil {
		return nil, pgxdb.MapError(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, pgxdb.MapError(err)
	}
	return ids, nil
}
