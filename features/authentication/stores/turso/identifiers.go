package turso

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk"
)

// IdentifierStore implements identifier.IdentifierRepository over a libSQL
// database (design §2.2). Active-only reads filter replaced_at IS NULL; the
// partial unique authentication-claim index arbitrates login/recovery collisions
// as sdk.ErrAlreadyExists via MapError; ApplyVerifiedChange is one transaction
// that CAS-checks users.auth_revision, retires the replaced/displaced rows,
// claims the new value, and bumps the revision. It targets the phase-2
// user_identifiers table and is proven live in phase 2.
type IdentifierStore struct {
	db *tursodb.DB
}

var _ identifier.IdentifierRepository = (*IdentifierStore)(nil)

// NewIdentifierStore returns an IdentifierStore backed by db.
func NewIdentifierStore(db *tursodb.DB) *IdentifierStore {
	return &IdentifierStore{db: db}
}

const identifierColumns = "id, user_id, kind, normalized_value, verified_at, login_enabled, recovery_enabled, notification_enabled, is_primary, created_at, updated_at, replaced_at"

// identifierRow is the store-local, db-tagged projection of a user_identifiers
// row. The boolean columns scan through tursodb.Bool and the nullable
// verified_at/replaced_at through tursodb.NullTime so a NULL reads back as the
// zero time (the domain's unverified/active sentinels).
type identifierRow struct {
	ID                  string           `db:"id"`
	UserID              string           `db:"user_id"`
	Kind                string           `db:"kind"`
	NormalizedValue     string           `db:"normalized_value"`
	VerifiedAt          tursodb.NullTime `db:"verified_at"`
	LoginEnabled        tursodb.Bool     `db:"login_enabled"`
	RecoveryEnabled     tursodb.Bool     `db:"recovery_enabled"`
	NotificationEnabled tursodb.Bool     `db:"notification_enabled"`
	IsPrimary           tursodb.Bool     `db:"is_primary"`
	CreatedAt           tursodb.Time     `db:"created_at"`
	UpdatedAt           tursodb.Time     `db:"updated_at"`
	ReplacedAt          tursodb.NullTime `db:"replaced_at"`
}

func (r identifierRow) toDomain() identifier.Identifier {
	return identifier.Identifier{
		ID:                  r.ID,
		UserID:              r.UserID,
		Kind:                identifier.Kind(r.Kind),
		NormalizedValue:     r.NormalizedValue,
		VerifiedAt:          r.VerifiedAt.Time,
		LoginEnabled:        bool(r.LoginEnabled),
		RecoveryEnabled:     bool(r.RecoveryEnabled),
		NotificationEnabled: bool(r.NotificationEnabled),
		IsPrimary:           bool(r.IsPrimary),
		CreatedAt:           r.CreatedAt.Time,
		UpdatedAt:           r.UpdatedAt.Time,
		ReplacedAt:          r.ReplacedAt.Time,
	}
}

// insertIdentifier writes one identifier row through q (a *DB or *Tx). An empty
// ID uses the DB-generated convention (RETURNING id); a lost authentication claim
// → sdk.ErrAlreadyExists via MapError. It returns the identifier with its ID set.
func insertIdentifier(ctx context.Context, q tursodb.Querier, ident identifier.Identifier) (identifier.Identifier, error) {
	args := []any{
		ident.UserID,
		string(ident.Kind),
		ident.NormalizedValue,
		tursodb.FormatNullTime(ident.VerifiedAt),
		tursodb.BoolToInt(ident.LoginEnabled),
		tursodb.BoolToInt(ident.RecoveryEnabled),
		tursodb.BoolToInt(ident.NotificationEnabled),
		tursodb.BoolToInt(ident.IsPrimary),
		tursodb.FormatTime(ident.CreatedAt),
		tursodb.FormatTime(ident.UpdatedAt),
		tursodb.FormatNullTime(ident.ReplacedAt),
	}
	if ident.ID == "" {
		const insert = `INSERT INTO user_identifiers
			(user_id, kind, normalized_value, verified_at, login_enabled, recovery_enabled, notification_enabled, is_primary, created_at, updated_at, replaced_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`
		if err := q.QueryRow(ctx, insert, args...).Scan(&ident.ID); err != nil {
			return identifier.Identifier{}, tursodb.MapError(err)
		}
		return ident, nil
	}
	const insert = `INSERT INTO user_identifiers (` + identifierColumns + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if _, err := q.Exec(ctx, insert, append([]any{ident.ID}, args...)...); err != nil {
		return identifier.Identifier{}, tursodb.MapError(err)
	}
	return ident, nil
}

// Get returns the identifier with the given id (active or replaced), or
// sdk.ErrNotFound.
func (s *IdentifierStore) Get(ctx context.Context, id string) (identifier.Identifier, error) {
	const q = `SELECT ` + identifierColumns + ` FROM user_identifiers WHERE id = ?`
	row, err := tursodb.QueryOne[identifierRow](ctx, s.db, q, id)
	if err != nil {
		return identifier.Identifier{}, err
	}
	return row.toDomain(), nil
}

// GetLogin returns the active login-enabled identifier claiming
// (kind, normalizedValue), or sdk.ErrNotFound.
func (s *IdentifierStore) GetLogin(ctx context.Context, kind, normalizedValue string) (identifier.Identifier, error) {
	const q = `SELECT ` + identifierColumns + ` FROM user_identifiers
		WHERE kind = ? AND normalized_value = ? AND replaced_at IS NULL AND login_enabled = 1`
	row, err := tursodb.QueryOne[identifierRow](ctx, s.db, q, kind, normalizedValue)
	if err != nil {
		return identifier.Identifier{}, err
	}
	return row.toDomain(), nil
}

// GetRecovery returns the active recovery-enabled identifier claiming
// (kind, normalizedValue), or sdk.ErrNotFound.
func (s *IdentifierStore) GetRecovery(ctx context.Context, kind, normalizedValue string) (identifier.Identifier, error) {
	const q = `SELECT ` + identifierColumns + ` FROM user_identifiers
		WHERE kind = ? AND normalized_value = ? AND replaced_at IS NULL AND recovery_enabled = 1`
	row, err := tursodb.QueryOne[identifierRow](ctx, s.db, q, kind, normalizedValue)
	if err != nil {
		return identifier.Identifier{}, err
	}
	return row.toDomain(), nil
}

// ListByUser returns the user's active identifiers (replaced rows excluded).
func (s *IdentifierStore) ListByUser(ctx context.Context, userID string) ([]identifier.Identifier, error) {
	const q = `SELECT ` + identifierColumns + ` FROM user_identifiers
		WHERE user_id = ? AND replaced_at IS NULL ORDER BY created_at, id`
	rows, err := s.db.Query(ctx, q, userID)
	if err != nil {
		return nil, tursodb.MapError(err)
	}
	defer rows.Close()
	out := make([]identifier.Identifier, 0)
	for rows.Next() {
		r, err := tursodb.ScanStruct[identifierRow](rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r.toDomain())
	}
	if err := rows.Err(); err != nil {
		return nil, tursodb.MapError(err)
	}
	return out, nil
}

// ApplyVerifiedChange applies a confirmed change atomically (design §2.2). See the
// port contract for the error mapping: stale revision → sdk.ErrConflict, lost
// claim → sdk.ErrAlreadyExists, unknown user → sdk.ErrNotFound.
func (s *IdentifierStore) ApplyVerifiedChange(ctx context.Context, input identifier.ApplyVerifiedChangeInput, expectedAuthRevision int64, verifiedAt time.Time) (identifier.Identifier, error) {
	var result identifier.Identifier
	err := s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		var current int64
		if err := tx.QueryRow(ctx, `SELECT auth_revision FROM users WHERE id = ?`, input.UserID).Scan(&current); err != nil {
			return tursodb.MapError(err)
		}
		if current != expectedAuthRevision {
			return sdk.ErrConflict
		}

		now := verifiedAt.UTC()
		if input.ReplacesIdentifierID != "" {
			if _, err := tx.Exec(ctx,
				`UPDATE user_identifiers SET replaced_at = ?, updated_at = ? WHERE id = ? AND replaced_at IS NULL`,
				tursodb.FormatTime(now), tursodb.FormatTime(now), input.ReplacesIdentifierID); err != nil {
				return tursodb.MapError(err)
			}
		}
		if input.MakePrimary {
			if _, err := tx.Exec(ctx,
				`UPDATE user_identifiers SET replaced_at = ?, updated_at = ?
					WHERE user_id = ? AND kind = ? AND is_primary = 1 AND replaced_at IS NULL`,
				tursodb.FormatTime(now), tursodb.FormatTime(now), input.UserID, string(input.Kind)); err != nil {
				return tursodb.MapError(err)
			}
		}

		created, err := insertIdentifier(ctx, tx, identifier.Identifier{
			UserID:              input.UserID,
			Kind:                input.Kind,
			NormalizedValue:     input.NormalizedValue,
			VerifiedAt:          now,
			LoginEnabled:        input.LoginEnabled,
			RecoveryEnabled:     input.RecoveryEnabled,
			NotificationEnabled: input.NotificationEnabled,
			IsPrimary:           input.MakePrimary,
			CreatedAt:           now,
			UpdatedAt:           now,
		})
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE users SET auth_revision = auth_revision + 1, updated_at = ? WHERE id = ?`,
			tursodb.FormatTime(now), input.UserID); err != nil {
			return tursodb.MapError(err)
		}
		result = created
		return nil
	})
	if err != nil {
		return identifier.Identifier{}, err
	}
	return result, nil
}
