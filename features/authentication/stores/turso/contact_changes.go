package turso

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/contactchange"
	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk"
)

// ContactChangeStore implements contactchange.Repository over a libSQL database
// (design §2.4). Create is a delete-before-insert per (user, kind) so at most one
// pending change is active for a pair (the idx_contact_changes_user_kind unique
// index is the concurrent backstop). Consume is one atomic DELETE ... RETURNING
// keyed by (user, kind): the row is deleted regardless of expiry, so an expired
// Consume deletes and returns sdk.ErrExpired.
type ContactChangeStore struct {
	db *tursodb.DB
}

var _ contactchange.Repository = (*ContactChangeStore)(nil)

// NewContactChangeStore returns a ContactChangeStore backed by db.
func NewContactChangeStore(db *tursodb.DB) *ContactChangeStore {
	return &ContactChangeStore{db: db}
}

const contactChangeReturning = "id, user_id, kind, new_value, login_enabled, recovery_enabled, notification_enabled, make_primary, replaces_identifier_id, expires_at, created_at"

// scanContactChange scans a full contact_changes row from a Scanner, mapping the
// 0/1 boolean columns and fixed-width TEXT timestamps back to their domain types.
func scanContactChange(row tursodb.Scanner) (contactchange.PendingChange, error) {
	var (
		p            contactchange.PendingChange
		kind         string
		login        tursodb.Bool
		recovery     tursodb.Bool
		notification tursodb.Bool
		makePrimary  tursodb.Bool
		expiresAt    tursodb.Time
		createdAt    tursodb.Time
	)
	err := row.Scan(
		&p.ID, &p.UserID, &kind, &p.NewValue,
		&login, &recovery, &notification, &makePrimary,
		&p.ReplacesIdentifierID, &expiresAt, &createdAt,
	)
	if err != nil {
		return contactchange.PendingChange{}, err
	}
	p.Kind = identifier.Kind(kind)
	p.LoginEnabled = bool(login)
	p.RecoveryEnabled = bool(recovery)
	p.NotificationEnabled = bool(notification)
	p.MakePrimary = bool(makePrimary)
	p.ExpiresAt = expiresAt.Time
	p.CreatedAt = createdAt.Time
	return p, nil
}

// Create atomically replaces any prior (UserID, Kind) pending change with p and
// returns the stored row (with its assigned ID).
func (s *ContactChangeStore) Create(ctx context.Context, p contactchange.PendingChange) (contactchange.PendingChange, error) {
	err := s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		if _, err := tx.Exec(ctx,
			`DELETE FROM contact_changes WHERE user_id = ? AND kind = ?`, p.UserID, string(p.Kind)); err != nil {
			return err
		}
		args := []any{
			p.UserID,
			string(p.Kind),
			p.NewValue,
			tursodb.BoolToInt(p.LoginEnabled),
			tursodb.BoolToInt(p.RecoveryEnabled),
			tursodb.BoolToInt(p.NotificationEnabled),
			tursodb.BoolToInt(p.MakePrimary),
			p.ReplacesIdentifierID,
			tursodb.FormatTime(p.ExpiresAt),
			tursodb.FormatTime(p.CreatedAt),
		}
		if p.ID == "" {
			const insert = `INSERT INTO contact_changes
				(user_id, kind, new_value, login_enabled, recovery_enabled, notification_enabled, make_primary, replaces_identifier_id, expires_at, created_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				RETURNING id`
			if err := tx.QueryRow(ctx, insert, args...).Scan(&p.ID); err != nil {
				return tursodb.MapError(err)
			}
			return nil
		}
		const insert = `INSERT INTO contact_changes
			(id, user_id, kind, new_value, login_enabled, recovery_enabled, notification_enabled, make_primary, replaces_identifier_id, expires_at, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
		if _, err := tx.Exec(ctx, insert, append([]any{p.ID}, args...)...); err != nil {
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
	const q = `DELETE FROM contact_changes WHERE user_id = ? AND kind = ? RETURNING ` + contactChangeReturning
	p, err := scanContactChange(s.db.QueryRow(ctx, q, userID, string(kind)))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return contactchange.PendingChange{}, sdk.ErrNotFound
		}
		return contactchange.PendingChange{}, tursodb.MapError(err)
	}
	if p.Expired(time.Now()) {
		return contactchange.PendingChange{}, sdk.ErrExpired
	}
	return p, nil
}
