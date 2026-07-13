package pgx

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/features/authentication/domain/contactchange"
	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk"
)

// ContactChangeStore implements contactchange.Repository over a PostgreSQL
// database (design §2.4). Create is a delete-before-insert per (user, kind) so at
// most one pending change is active for a pair (the idx_contact_changes_user_kind
// unique index is the concurrent backstop). Consume is one atomic DELETE ...
// RETURNING keyed by (user, kind): the row is deleted regardless of expiry, so an
// expired Consume deletes and returns sdk.ErrExpired.
type ContactChangeStore struct {
	db *pgxdb.DB
}

var _ contactchange.Repository = (*ContactChangeStore)(nil)

// NewContactChangeStore returns a ContactChangeStore backed by db.
func NewContactChangeStore(db *pgxdb.DB) *ContactChangeStore {
	return &ContactChangeStore{db: db}
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
	err := s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		if _, err := tx.Exec(ctx,
			`DELETE FROM contact_changes WHERE user_id = @user_id AND kind = @kind`,
			pgx.NamedArgs{"user_id": p.UserID, "kind": string(p.Kind)}); err != nil {
			return err
		}
		args := pgx.NamedArgs{
			"user_id":                p.UserID,
			"kind":                   string(p.Kind),
			"new_value":              p.NewValue,
			"login_enabled":          p.LoginEnabled,
			"recovery_enabled":       p.RecoveryEnabled,
			"notification_enabled":   p.NotificationEnabled,
			"make_primary":           p.MakePrimary,
			"replaces_identifier_id": p.ReplacesIdentifierID,
			"expires_at":             p.ExpiresAt.UTC(),
			"created_at":             p.CreatedAt.UTC(),
		}
		if p.ID == "" {
			const insert = `INSERT INTO contact_changes
				(user_id, kind, new_value, login_enabled, recovery_enabled, notification_enabled, make_primary, replaces_identifier_id, expires_at, created_at)
				VALUES (@user_id, @kind, @new_value, @login_enabled, @recovery_enabled, @notification_enabled, @make_primary, @replaces_identifier_id, @expires_at, @created_at)
				RETURNING id`
			if err := tx.QueryRow(ctx, insert, args).Scan(&p.ID); err != nil {
				return pgxdb.MapError(err)
			}
			return nil
		}
		args["id"] = p.ID
		const insert = `INSERT INTO contact_changes
			(id, user_id, kind, new_value, login_enabled, recovery_enabled, notification_enabled, make_primary, replaces_identifier_id, expires_at, created_at)
			VALUES (@id, @user_id, @kind, @new_value, @login_enabled, @recovery_enabled, @notification_enabled, @make_primary, @replaces_identifier_id, @expires_at, @created_at)`
		if _, err := tx.Exec(ctx, insert, args); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return contactchange.PendingChange{}, err
	}
	return p, nil
}

// Consume atomically deletes and returns the (userID, kind) pending change: live →
// the PendingChange, expired → sdk.ErrExpired (row deleted), missing → sdk.ErrNotFound.
func (s *ContactChangeStore) Consume(ctx context.Context, userID string, kind identifier.Kind) (contactchange.PendingChange, error) {
	const q = `DELETE FROM contact_changes WHERE user_id = @user_id AND kind = @kind RETURNING ` + contactChangeReturning
	p, err := scanContactChange(s.db.QueryRow(ctx, q, pgx.NamedArgs{"user_id": userID, "kind": string(kind)}))
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
