package pgx

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk"
)

// IdentifierStore implements identifier.IdentifierRepository over a PostgreSQL
// database (design §2.2). Active-only reads filter replaced_at IS NULL; the
// partial unique authentication-claim index arbitrates login/recovery collisions
// as sdk.ErrAlreadyExists via MapError; ApplyVerifiedChange is one transaction
// that CAS-checks users.auth_revision, retires the replaced/displaced rows,
// claims the new value, and bumps the revision. It targets the phase-2
// user_identifiers table and is proven live in phase 2.
type IdentifierStore struct {
	db *pgxdb.DB
}

var _ identifier.IdentifierRepository = (*IdentifierStore)(nil)

// NewIdentifierStore returns an IdentifierStore backed by db.
func NewIdentifierStore(db *pgxdb.DB) *IdentifierStore {
	return &IdentifierStore{db: db}
}

const identifierColumns = "id, user_id, kind, normalized_value, verified_at, login_enabled, recovery_enabled, notification_enabled, is_primary, created_at, updated_at, replaced_at"

// identifierRow is the store-local, db-tagged projection of a user_identifiers
// row. The nullable verified_at/replaced_at columns are pointers so a NULL reads
// back as the zero time (the domain's unverified/active sentinels).
type identifierRow struct {
	ID                  string     `db:"id"`
	UserID              string     `db:"user_id"`
	Kind                string     `db:"kind"`
	NormalizedValue     string     `db:"normalized_value"`
	VerifiedAt          *time.Time `db:"verified_at"`
	LoginEnabled        bool       `db:"login_enabled"`
	RecoveryEnabled     bool       `db:"recovery_enabled"`
	NotificationEnabled bool       `db:"notification_enabled"`
	IsPrimary           bool       `db:"is_primary"`
	CreatedAt           time.Time  `db:"created_at"`
	UpdatedAt           time.Time  `db:"updated_at"`
	ReplacedAt          *time.Time `db:"replaced_at"`
}

func (r identifierRow) toDomain() identifier.Identifier {
	return identifier.Identifier{
		ID:                  r.ID,
		UserID:              r.UserID,
		Kind:                identifier.Kind(r.Kind),
		NormalizedValue:     r.NormalizedValue,
		VerifiedAt:          pgxdb.FromNullTime(r.VerifiedAt),
		LoginEnabled:        r.LoginEnabled,
		RecoveryEnabled:     r.RecoveryEnabled,
		NotificationEnabled: r.NotificationEnabled,
		IsPrimary:           r.IsPrimary,
		CreatedAt:           r.CreatedAt.UTC(),
		UpdatedAt:           r.UpdatedAt.UTC(),
		ReplacedAt:          pgxdb.FromNullTime(r.ReplacedAt),
	}
}

// insertIdentifier writes one identifier row through q (a *DB or *Tx). An empty
// ID uses the DB-generated convention (RETURNING id); a lost authentication claim
// → sdk.ErrAlreadyExists via MapError. It returns the identifier with its ID set.
func insertIdentifier(ctx context.Context, q pgxdb.Querier, ident identifier.Identifier) (identifier.Identifier, error) {
	args := pgx.NamedArgs{
		"user_id":              ident.UserID,
		"kind":                 string(ident.Kind),
		"normalized_value":     ident.NormalizedValue,
		"verified_at":          pgxdb.NullTime(ident.VerifiedAt),
		"login_enabled":        ident.LoginEnabled,
		"recovery_enabled":     ident.RecoveryEnabled,
		"notification_enabled": ident.NotificationEnabled,
		"is_primary":           ident.IsPrimary,
		"created_at":           ident.CreatedAt.UTC(),
		"updated_at":           ident.UpdatedAt.UTC(),
		"replaced_at":          pgxdb.NullTime(ident.ReplacedAt),
	}
	if ident.ID == "" {
		const insert = `INSERT INTO user_identifiers
			(user_id, kind, normalized_value, verified_at, login_enabled, recovery_enabled, notification_enabled, is_primary, created_at, updated_at, replaced_at)
			VALUES (@user_id, @kind, @normalized_value, @verified_at, @login_enabled, @recovery_enabled, @notification_enabled, @is_primary, @created_at, @updated_at, @replaced_at)
			RETURNING id`
		if err := q.QueryRow(ctx, insert, args).Scan(&ident.ID); err != nil {
			return identifier.Identifier{}, pgxdb.MapError(err)
		}
		return ident, nil
	}
	args["id"] = ident.ID
	const insert = `INSERT INTO user_identifiers (` + identifierColumns + `)
		VALUES (@id, @user_id, @kind, @normalized_value, @verified_at, @login_enabled, @recovery_enabled, @notification_enabled, @is_primary, @created_at, @updated_at, @replaced_at)`
	if _, err := q.Exec(ctx, insert, args); err != nil {
		return identifier.Identifier{}, pgxdb.MapError(err)
	}
	return ident, nil
}

// Get returns the identifier with the given id (active or replaced), or
// sdk.ErrNotFound.
func (s *IdentifierStore) Get(ctx context.Context, id string) (identifier.Identifier, error) {
	const q = `SELECT ` + identifierColumns + ` FROM user_identifiers WHERE id = @id`
	row, err := pgxdb.QueryOne[identifierRow](ctx, s.db, q, pgx.NamedArgs{"id": id})
	if err != nil {
		return identifier.Identifier{}, err
	}
	return row.toDomain(), nil
}

// GetLogin returns the active login-enabled identifier claiming
// (kind, normalizedValue), or sdk.ErrNotFound.
func (s *IdentifierStore) GetLogin(ctx context.Context, kind, normalizedValue string) (identifier.Identifier, error) {
	const q = `SELECT ` + identifierColumns + ` FROM user_identifiers
		WHERE kind = @kind AND normalized_value = @normalized_value AND replaced_at IS NULL AND login_enabled = TRUE`
	row, err := pgxdb.QueryOne[identifierRow](ctx, s.db, q, pgx.NamedArgs{"kind": kind, "normalized_value": normalizedValue})
	if err != nil {
		return identifier.Identifier{}, err
	}
	return row.toDomain(), nil
}

// GetRecovery returns the active recovery-enabled identifier claiming
// (kind, normalizedValue), or sdk.ErrNotFound.
func (s *IdentifierStore) GetRecovery(ctx context.Context, kind, normalizedValue string) (identifier.Identifier, error) {
	const q = `SELECT ` + identifierColumns + ` FROM user_identifiers
		WHERE kind = @kind AND normalized_value = @normalized_value AND replaced_at IS NULL AND recovery_enabled = TRUE`
	row, err := pgxdb.QueryOne[identifierRow](ctx, s.db, q, pgx.NamedArgs{"kind": kind, "normalized_value": normalizedValue})
	if err != nil {
		return identifier.Identifier{}, err
	}
	return row.toDomain(), nil
}

// ListByUser returns the user's active identifiers (replaced rows excluded).
func (s *IdentifierStore) ListByUser(ctx context.Context, userID string) ([]identifier.Identifier, error) {
	const q = `SELECT ` + identifierColumns + ` FROM user_identifiers
		WHERE user_id = @user_id AND replaced_at IS NULL ORDER BY created_at, id`
	rows, err := s.db.Query(ctx, q, pgx.NamedArgs{"user_id": userID})
	if err != nil {
		return nil, pgxdb.MapError(err)
	}
	collected, err := pgx.CollectRows(rows, pgx.RowToStructByName[identifierRow])
	if err != nil {
		return nil, pgxdb.MapError(err)
	}
	out := make([]identifier.Identifier, 0, len(collected))
	for _, r := range collected {
		out = append(out, r.toDomain())
	}
	return out, nil
}

// ApplyVerifiedChange applies a confirmed change atomically (design §2.2). See the
// port contract for the error mapping: stale revision → sdk.ErrConflict, lost
// claim → sdk.ErrAlreadyExists, unknown user → sdk.ErrNotFound.
func (s *IdentifierStore) ApplyVerifiedChange(ctx context.Context, input identifier.ApplyVerifiedChangeInput, expectedAuthRevision int64, verifiedAt time.Time) (identifier.Identifier, error) {
	var result identifier.Identifier
	err := s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		// CAS anchor: lock the user row and compare its revision.
		var current int64
		row := tx.QueryRow(ctx, `SELECT auth_revision FROM users WHERE id = @id FOR UPDATE`, pgx.NamedArgs{"id": input.UserID})
		if err := row.Scan(&current); err != nil {
			return pgxdb.MapError(err)
		}
		if current != expectedAuthRevision {
			return sdk.ErrConflict
		}

		now := verifiedAt.UTC()
		// Retire the explicitly replaced identifier.
		if input.ReplacesIdentifierID != "" {
			if _, err := tx.Exec(ctx,
				`UPDATE user_identifiers SET replaced_at = @now, updated_at = @now WHERE id = @id AND replaced_at IS NULL`,
				pgx.NamedArgs{"now": now, "id": input.ReplacesIdentifierID}); err != nil {
				return pgxdb.MapError(err)
			}
		}
		// Retire any displaced active primary of the same kind.
		if input.MakePrimary {
			if _, err := tx.Exec(ctx,
				`UPDATE user_identifiers SET replaced_at = @now, updated_at = @now
					WHERE user_id = @user_id AND kind = @kind AND is_primary = TRUE AND replaced_at IS NULL`,
				pgx.NamedArgs{"now": now, "user_id": input.UserID, "kind": string(input.Kind)}); err != nil {
				return pgxdb.MapError(err)
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
			`UPDATE users SET auth_revision = auth_revision + 1, updated_at = @now WHERE id = @id`,
			pgx.NamedArgs{"now": now, "id": input.UserID}); err != nil {
			return pgxdb.MapError(err)
		}
		result = created
		return nil
	})
	if err != nil {
		return identifier.Identifier{}, err
	}
	return result, nil
}
