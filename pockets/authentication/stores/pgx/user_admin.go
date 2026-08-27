package pgx

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/pockets/authentication/domain/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/user"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// UserAdminStore implements user.AdminRepository over PostgreSQL: the operator
// directory and the ATOMIC lifecycle transition.
//
// SetStatus is the load-bearing half. It runs the status write, the auth_revision
// increment, and the session + authentication-grant deletion inside ONE
// transaction, so a crash or a concurrent reader can never observe a deactivated
// user still holding live sessions. It also takes a row lock on the user (SELECT
// ... FOR UPDATE), which is the other side of the fence ActiveSessionStore holds:
// a mint and a transition contend on the same row, so exactly one wins and the
// loser sees the winner's committed state.
type UserAdminStore struct {
	db *pgxdb.DB
	qualified
}

var _ user.AdminRepository = (*UserAdminStore)(nil)

// NewUserAdminStore returns a UserAdminStore backed by db.
func NewUserAdminStore(db *pgxdb.DB, opts ...Option) *UserAdminStore {
	return &UserAdminStore{db: db, qualified: qualified{schema: applyOptions(opts).schema}}
}

// userSummarySelect renders the directory-row projection under s's schema. The
// active primary email is resolved by a LEFT JOIN in the SAME query — one
// statement per page, never one identifier read per user. The join predicate
// matches the partial unique index idx_user_identifiers_primary (active,
// primary, per (user, kind)), so at most one identifier row can match and a user
// with a retired identifier history still appears exactly once. A user with no
// email identifier at all keeps its row with NULL email, rather than being
// dropped by an inner join.
//
// The join is wrapped in a derived table so the OUTER query sees flat, unambiguous
// `id` and `created_at` columns. Both tables carry a created_at, so the keyset
// predicate and the ORDER BY would otherwise be ambiguous references — and the
// keyset predicate lives in a WHERE clause, where an output-column alias would not
// resolve.
func userSummarySelect(s qualified) string {
	return `SELECT id, display_name, status, status_changed_at, created_at, updated_at, primary_email, primary_email_verified_at
	FROM (
		SELECT u.id AS id, u.display_name AS display_name, u.status AS status,
			u.status_changed_at AS status_changed_at, u.created_at AS created_at,
			u.updated_at AS updated_at,
			i.normalized_value AS primary_email, i.verified_at AS primary_email_verified_at
		FROM ` + s.table(usersTable) + ` u
		LEFT JOIN ` + s.table(identifiersTable) + ` i
			ON i.user_id = u.id
			AND i.kind = 'email'
			AND i.is_primary = TRUE
			AND i.replaced_at IS NULL
	) AS directory`
}

// userSummaryRow is the store-local, db-tagged projection of one directory row.
// The joined columns are nullable: a user may hold no active primary email, and
// an unverified identifier has a NULL verified_at.
type userSummaryRow struct {
	ID                     string     `db:"id"`
	DisplayName            string     `db:"display_name"`
	Status                 string     `db:"status"`
	StatusChangedAt        *time.Time `db:"status_changed_at"`
	CreatedAt              time.Time  `db:"created_at"`
	UpdatedAt              time.Time  `db:"updated_at"`
	PrimaryEmail           *string    `db:"primary_email"`
	PrimaryEmailVerifiedAt *time.Time `db:"primary_email_verified_at"`
}

func (r userSummaryRow) toDomain() user.Summary {
	s := user.Summary{
		ID:              r.ID,
		DisplayName:     r.DisplayName,
		Status:          user.NormalizeStatus(user.Status(r.Status)),
		StatusChangedAt: pgxdb.FromNullTime(r.StatusChangedAt),
		CreatedAt:       r.CreatedAt.UTC(),
		UpdatedAt:       r.UpdatedAt.UTC(),
	}
	if r.PrimaryEmail != nil {
		s.PrimaryEmail = *r.PrimaryEmail
		s.EmailVerified = r.PrimaryEmailVerifiedAt != nil
	}
	return s
}

// List returns a page of directory rows ordered created_at DESC, id DESC (the id
// tiebreak is contractual — see the 0014 collation note).
func (s *UserAdminStore) List(ctx context.Context, req crud.ListRequest) (crud.Page[user.Summary], error) {
	q := pgxdb.ListQuery[userSummaryRow]{
		BaseSQL:      userSummarySelect(s.qualified),
		OrderFields:  user.OrderFields,
		DefaultOrder: user.DefaultOrder,
		PK:           "id",
		// .UTC() is load-bearing for CROSS-DIALECT cursor parity: pgx returns a
		// timestamptz in the session's time zone, so an un-normalized value encodes
		// as "…T05:10:00-07:00" while the turso and reference stores encode the same
		// instant as "…T12:10:00Z". The two cursors would then differ byte-for-byte
		// for identical data, which the directory contract forbids. Normalizing here
		// does not affect the keyset predicate — Postgres compares the same instant
		// either way.
		OrderValueOf: func(r userSummaryRow, _ string) any { return r.CreatedAt.UTC() },
		PKOf:         func(r userSummaryRow) string { return r.ID },
	}
	page, err := pgxdb.List(ctx, s.db, q, req)
	if err != nil {
		return crud.Page[user.Summary]{}, err
	}
	return crud.MapPage(page, userSummaryRow.toDomain), nil
}

// GetSummary returns one user's directory projection, or sdk.ErrNotFound.
func (s *UserAdminStore) GetSummary(ctx context.Context, id string) (user.Summary, error) {
	q := userSummarySelect(s.qualified) + ` WHERE id = @id`
	row, err := pgxdb.QueryOne[userSummaryRow](ctx, s.db, q, pgx.NamedArgs{"id": id})
	if err != nil {
		return user.Summary{}, err
	}
	return row.toDomain(), nil
}

// SetStatus atomically transitions the lifecycle status. See the type doc for the
// transaction contract; the ordering inside it is deliberate:
//
//  1. SELECT ... FOR UPDATE locks the user row, so a concurrent
//     CreateForActiveUser blocks here rather than reading a stale status;
//  2. an unchanged desired status returns early with Changed=false, holding no
//     write and leaving auth_revision alone — the idempotent replay;
//  3. the UPDATE writes status/status_changed_at/updated_at and increments
//     auth_revision exactly once; and
//  4. the grant and session deletes run last, inside the same transaction, so a
//     rollback at any point leaves the account exactly as it was.
//
// Grants are deleted by user_id AND through the session join, because a grant
// whose session was already gone must not outlive the transition either.
func (s *UserAdminStore) SetStatus(ctx context.Context, id string, status user.Status, now time.Time) (user.StatusChange, error) {
	if !status.Valid() {
		return user.StatusChange{}, fmt.Errorf("authentication pgx store: %q: %w", status, user.ErrInvalidStatus)
	}
	now = now.UTC()

	var out user.StatusChange
	err := s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		var (
			current   string
			changedAt *time.Time
		)
		lockQ := `SELECT status, status_changed_at FROM ` + s.table(usersTable) + ` WHERE id = @id FOR UPDATE`
		if err := tx.QueryRow(ctx, lockQ, pgx.NamedArgs{"id": id}).Scan(&current, &changedAt); err != nil {
			return pgxdb.MapError(err)
		}

		if user.NormalizeStatus(user.Status(current)) == status {
			out = user.StatusChange{Status: status, Changed: false, ChangedAt: pgxdb.FromNullTime(changedAt)}
			return nil
		}

		updateQ := `UPDATE ` + s.table(usersTable) + `
			SET status = @status, status_changed_at = @now, updated_at = @now,
				auth_revision = auth_revision + 1
			WHERE id = @id`
		if _, err := tx.Exec(ctx, updateQ, pgx.NamedArgs{"status": string(status), "now": now, "id": id}); err != nil {
			return pgxdb.MapError(err)
		}

		grantsQ := `DELETE FROM ` + s.table(authGrantsTable) + ` WHERE user_id = @id
			OR session_id IN (SELECT id FROM ` + s.table(sessionsTable) + ` WHERE user_id = @id)`
		if _, err := tx.Exec(ctx, grantsQ, pgx.NamedArgs{"id": id}); err != nil {
			return pgxdb.MapError(err)
		}

		sessionsQ := `DELETE FROM ` + s.table(sessionsTable) + ` WHERE user_id = @id`
		tag, err := tx.Exec(ctx, sessionsQ, pgx.NamedArgs{"id": id})
		if err != nil {
			return pgxdb.MapError(err)
		}

		out = user.StatusChange{
			Status:          status,
			Changed:         true,
			ChangedAt:       now,
			RevokedSessions: int(tag.RowsAffected()),
		}
		return nil
	})
	if err != nil {
		return user.StatusChange{}, err
	}
	return out, nil
}

// ActiveSessionStore implements session.ActiveUserRepository over PostgreSQL: the
// fenced session mint.
//
// The whole point is that the status proof and the insert share one serialization
// boundary. The INSERT ... SELECT reads users under the same statement that writes
// the session, with FOR SHARE taken on the user row first so a concurrent
// UserAdminStore.SetStatus (which takes FOR UPDATE) cannot commit between the two.
// The two legal outcomes are therefore: the mint commits and the transition then
// deletes it, or the transition commits and the mint sees deactivated and refuses.
type ActiveSessionStore struct {
	db *pgxdb.DB
	qualified
}

var _ session.ActiveUserRepository = (*ActiveSessionStore)(nil)

// NewActiveSessionStore returns an ActiveSessionStore backed by db.
func NewActiveSessionStore(db *pgxdb.DB, opts ...Option) *ActiveSessionStore {
	return &ActiveSessionStore{db: db, qualified: qualified{schema: applyOptions(opts).schema}}
}

// CreateForActiveUser inserts sess only while its owning user is active.
//
// Unknown user → sdk.ErrNotFound; deactivated → session.ErrUserNotActive with no
// row written; a colliding refresh_token_hash → sdk.ErrAlreadyExists, exactly as
// SessionStore.Create reports it.
func (s *ActiveSessionStore) CreateForActiveUser(ctx context.Context, sess session.Session) (session.Session, error) {
	methods, err := encodeMethods(sess.Authentication.Methods)
	if err != nil {
		return session.Session{}, err
	}

	err = s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		// FOR SHARE, not FOR UPDATE: many concurrent mints for one user may proceed
		// together, while a SetStatus taking FOR UPDATE is blocked until this
		// transaction commits. That is exactly the fence — the transition can only
		// run before this insert (and be seen here) or after it (and revoke it).
		var status string
		lockQ := `SELECT status FROM ` + s.table(usersTable) + ` WHERE id = @user_id FOR SHARE`
		if err := tx.QueryRow(ctx, lockQ, pgx.NamedArgs{"user_id": sess.UserID}).Scan(&status); err != nil {
			return pgxdb.MapError(err)
		}
		if !user.NormalizeStatus(user.Status(status)).Active() {
			return session.ErrUserNotActive
		}

		insertQ := `INSERT INTO ` + s.table(sessionsTable) + ` (` + sessionColumns + `)
			VALUES (@id, @user_id, @refresh_token_hash, @previous_refresh_token_hash, @previous_used, @rotation_count, @authenticated_at, @authentication_methods, @assurance_level, @created_at, @expires_at)`
		if _, err := tx.Exec(ctx, insertQ, pgx.NamedArgs{
			"id":                          sess.ID,
			"user_id":                     sess.UserID,
			"refresh_token_hash":          sess.RefreshTokenHash,
			"previous_refresh_token_hash": nullHash(sess.PreviousRefreshTokenHash),
			"previous_used":               sess.PreviousUsed,
			"rotation_count":              sess.RotationCount,
			"authenticated_at":            pgxdb.NullTime(sess.Authentication.AuthenticatedAt),
			"authentication_methods":      methods,
			"assurance_level":             string(sess.Authentication.Assurance),
			"created_at":                  sess.CreatedAt.UTC(),
			"expires_at":                  sess.ExpiresAt.UTC(),
		}); err != nil {
			return pgxdb.MapError(err)
		}
		return nil
	})
	if err != nil {
		return session.Session{}, err
	}
	return sess, nil
}
