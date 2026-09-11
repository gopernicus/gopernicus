package pgx

import (
	"context"
	"time"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/contactchange"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/jackc/pgx/v5"
)

// ContactChangeStore implements contactchange.Repository over a PostgreSQL
// database (design §2.4). Create is a delete-before-insert per (user, kind) so at
// most one pending change is active for a pair (the idx_contact_changes_user_kind
// unique index is the concurrent backstop). Consume is one atomic DELETE ...
// RETURNING keyed by (user, kind): the row is deleted regardless of expiry, so an
// expired Consume deletes and returns sdk.ErrExpired.
type ContactChangeStore struct {
	db *pgxdb.DB
	qualified
}

var _ contactchange.Repository = (*ContactChangeStore)(nil)

// NewContactChangeStore returns a ContactChangeStore backed by db.
// It panics if db is nil; the caller owns the database lifecycle.
func NewContactChangeStore(db *pgxdb.DB, opts ...Option) *ContactChangeStore {
	if db == nil {
		panic("authentication pgx: NewContactChangeStore received a nil database")
	}
	return &ContactChangeStore{db: db, qualified: qualified{schema: applyOptions(opts).schema}}
}

const contactChangeReturning = "id, user_id, kind, new_value, login_enabled, recovery_enabled, notification_enabled, make_primary, replaces_identifier_id, expires_at, created_at"

// scanContactChange scans a full contact_changes row from a Scanner.
func scanContactChange(row pgxdb.Scanner) (contactchange.PendingChange, error) {
	var (
		p    contactchange.PendingChange
		kind string
	)
	err := row.Scan(
		&p.ID, &p.UserID, &kind, &p.NewValue,
		&p.LoginEnabled, &p.RecoveryEnabled, &p.NotificationEnabled,
		&p.MakePrimary, &p.ReplacesIdentifierID, &p.ExpiresAt, &p.CreatedAt,
	)
	if err != nil {
		return contactchange.PendingChange{}, err
	}
	p.Kind = identifier.Kind(kind)
	p.ExpiresAt = p.ExpiresAt.UTC()
	p.CreatedAt = p.CreatedAt.UTC()
	return p, nil
}

// Create atomically replaces any prior (UserID, Kind) pending change with p and
// returns the stored row (with its assigned ID).
func (s *ContactChangeStore) Create(ctx context.Context, p contactchange.PendingChange) (contactchange.PendingChange, error) {
	args := pgx.NamedArgs{
		"user_id": p.UserID, "kind": string(p.Kind), "new_value": p.NewValue,
		"login_enabled": p.LoginEnabled, "recovery_enabled": p.RecoveryEnabled,
		"notification_enabled": p.NotificationEnabled, "make_primary": p.MakePrimary,
		"replaces_identifier_id": p.ReplacesIdentifierID, "expires_at": p.ExpiresAt.UTC(), "created_at": p.CreatedAt.UTC(),
	}
	columns := "user_id, kind, new_value, login_enabled, recovery_enabled, notification_enabled, make_primary, replaces_identifier_id, expires_at, created_at"
	values := "@user_id, @kind, @new_value, @login_enabled, @recovery_enabled, @notification_enabled, @make_primary, @replaces_identifier_id, @expires_at, @created_at"
	if p.ID != "" {
		columns = "id, " + columns
		values = "@id, " + values
		args["id"] = p.ID
	}
	q := `INSERT INTO ` + s.table(contactChangesTable) + ` (` + columns + `) VALUES (` + values + `)
		ON CONFLICT (user_id, kind) DO UPDATE SET id = EXCLUDED.id, new_value = EXCLUDED.new_value,
		login_enabled = EXCLUDED.login_enabled, recovery_enabled = EXCLUDED.recovery_enabled,
		notification_enabled = EXCLUDED.notification_enabled, make_primary = EXCLUDED.make_primary,
		replaces_identifier_id = EXCLUDED.replaces_identifier_id, expires_at = EXCLUDED.expires_at,
		created_at = EXCLUDED.created_at RETURNING id`
	if err := s.db.QueryRow(ctx, q, args).Scan(&p.ID); err != nil {
		return contactchange.PendingChange{}, pgxdb.MapError(err)
	}
	return p, nil
}

func (s *ContactChangeStore) Get(ctx context.Context, userID string, kind identifier.Kind) (contactchange.PendingChange, error) {
	p, err := scanContactChange(s.db.QueryRow(ctx, `SELECT `+contactChangeReturning+` FROM `+s.table(contactChangesTable)+` WHERE user_id = $1 AND kind = $2`, userID, string(kind)))
	return p, pgxdb.MapError(err)
}

// Consume atomically deletes and returns the (userID, kind) pending change: live →
// the PendingChange, expired → sdk.ErrExpired (row deleted), missing → sdk.ErrNotFound.
func (s *ContactChangeStore) Consume(ctx context.Context, userID string, kind identifier.Kind, expectedID string) (contactchange.PendingChange, error) {
	q := `DELETE FROM ` + s.table(contactChangesTable) + ` WHERE user_id = @user_id AND kind = @kind AND id = @id RETURNING ` + contactChangeReturning
	p, err := scanContactChange(s.db.QueryRow(ctx, q, pgx.NamedArgs{"user_id": userID, "kind": string(kind), "id": expectedID}))
	if err != nil {
		if err == pgx.ErrNoRows {
			return contactchange.PendingChange{}, sdk.ErrNotFound
		}
		return contactchange.PendingChange{}, pgxdb.MapError(err)
	}
	if p.Expired(time.Now()) {
		return contactchange.PendingChange{}, sdk.ErrExpired
	}
	return p, nil
}
