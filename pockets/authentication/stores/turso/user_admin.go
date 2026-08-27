package turso

import (
	"context"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/domain/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/user"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// UserAdminStore implements user.AdminRepository over libSQL: the operator
// directory and the ATOMIC lifecycle transition. It is the dialect sibling of the
// pgx UserAdminStore — same contract, same outcomes, same conformance suite.
//
// SetStatus runs the status write, the auth_revision increment, and the session +
// authentication-grant deletion inside ONE transaction. The connector opens every
// transaction with BEGIN IMMEDIATE, taking the write intent up front, so a
// concurrent ActiveSessionStore.CreateForActiveUser cannot interleave with it:
// one transaction holds the write lock and the other waits and then observes the
// committed result. That is the same fence the pgx sibling gets from explicit row
// locking, obtained here from the connector's write-serialization posture rather
// than from SELECT ... FOR UPDATE, which SQLite does not have.
type UserAdminStore struct {
	db *tursodb.DB
}

var _ user.AdminRepository = (*UserAdminStore)(nil)

// NewUserAdminStore returns a UserAdminStore backed by db.
func NewUserAdminStore(db *tursodb.DB) *UserAdminStore {
	return &UserAdminStore{db: db}
}

// userSummarySelect projects the directory row. The active primary email is
// resolved by a LEFT JOIN in the SAME query — one statement per page, never one
// identifier read per user. The join predicate matches the partial unique index
// idx_user_identifiers_primary (active, primary, per (user, kind)), so at most one
// identifier row can match and a user with a retired identifier history still
// appears exactly once. A user with no email identifier at all keeps its row with
// NULL email, rather than being dropped by an inner join.
//
// The join is wrapped in a derived table so the OUTER query sees flat,
// unambiguous `id` and `created_at` columns: both tables carry a created_at, and
// the keyset predicate lives in a WHERE clause where an output alias would not
// resolve. The pgx sibling uses the identical shape.
const userSummarySelect = `SELECT id, display_name, status, status_changed_at, created_at, updated_at, primary_email, primary_email_verified_at
	FROM (
		SELECT u.id AS id, u.display_name AS display_name, u.status AS status,
			u.status_changed_at AS status_changed_at, u.created_at AS created_at,
			u.updated_at AS updated_at,
			i.normalized_value AS primary_email, i.verified_at AS primary_email_verified_at
		FROM users u
		LEFT JOIN user_identifiers i
			ON i.user_id = u.id
			AND i.kind = 'email'
			AND i.is_primary = 1
			AND i.replaced_at IS NULL
	) AS directory`

// userSummaryRow is the store-local, db-tagged projection of one directory row.
// The joined columns are nullable: a user may hold no active primary email, and
// an unverified identifier has a NULL verified_at.
type userSummaryRow struct {
	ID                     string           `db:"id"`
	DisplayName            string           `db:"display_name"`
	Status                 string           `db:"status"`
	StatusChangedAt        tursodb.NullTime `db:"status_changed_at"`
	CreatedAt              tursodb.Time     `db:"created_at"`
	UpdatedAt              tursodb.Time     `db:"updated_at"`
	PrimaryEmail           *string          `db:"primary_email"`
	PrimaryEmailVerifiedAt tursodb.NullTime `db:"primary_email_verified_at"`
}

func (r userSummaryRow) toDomain() user.Summary {
	s := user.Summary{
		ID:              r.ID,
		DisplayName:     r.DisplayName,
		Status:          user.NormalizeStatus(user.Status(r.Status)),
		StatusChangedAt: r.StatusChangedAt.Time,
		CreatedAt:       r.CreatedAt.Time,
		UpdatedAt:       r.UpdatedAt.Time,
	}
	if r.PrimaryEmail != nil {
		s.PrimaryEmail = *r.PrimaryEmail
		s.EmailVerified = r.PrimaryEmailVerifiedAt.Valid
	}
	return s
}

// List returns a page of directory rows ordered created_at DESC, id DESC. SQLite's
// default TEXT collation is BINARY, which already is the byte-wise tiebreak order
// the pgx sibling pins with COLLATE "C".
func (s *UserAdminStore) List(ctx context.Context, req crud.ListRequest) (crud.Page[user.Summary], error) {
	q := tursodb.ListQuery[userSummaryRow]{
		BaseSQL:      userSummarySelect,
		OrderFields:  user.OrderFields,
		DefaultOrder: user.DefaultOrder,
		PK:           "id",
		OrderValueOf: func(r userSummaryRow, _ string) any { return r.CreatedAt.Time },
		PKOf:         func(r userSummaryRow) string { return r.ID },
	}
	page, err := tursodb.List(ctx, s.db, q, req)
	if err != nil {
		return crud.Page[user.Summary]{}, err
	}
	return crud.MapPage(page, userSummaryRow.toDomain), nil
}

// GetSummary returns one user's directory projection, or sdk.ErrNotFound.
func (s *UserAdminStore) GetSummary(ctx context.Context, id string) (user.Summary, error) {
	q := userSummarySelect + ` WHERE id = ?`
	row, err := tursodb.QueryOne[userSummaryRow](ctx, s.db, q, id)
	if err != nil {
		return user.Summary{}, err
	}
	return row.toDomain(), nil
}

// SetStatus atomically transitions the lifecycle status. The ordering inside the
// transaction is deliberate:
//
//  1. read the current status (the BEGIN IMMEDIATE write lock is already held, so
//     this read cannot be invalidated by a concurrent writer);
//  2. an unchanged desired status returns early with Changed=false, writing
//     nothing and leaving auth_revision alone — the idempotent replay;
//  3. the UPDATE writes status/status_changed_at/updated_at and increments
//     auth_revision exactly once; and
//  4. the grant and session deletes run last, inside the same transaction, so a
//     rollback at any point leaves the account exactly as it was.
//
// Grants are deleted by user_id AND through the session subquery, because a grant
// whose session was already gone must not outlive the transition either.
func (s *UserAdminStore) SetStatus(ctx context.Context, id string, status user.Status, now time.Time) (user.StatusChange, error) {
	if !status.Valid() {
		return user.StatusChange{}, fmt.Errorf("authentication turso store: %q: %w", status, user.ErrInvalidStatus)
	}
	now = now.UTC()

	var out user.StatusChange
	err := s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		var (
			current   string
			changedAt tursodb.NullTime
		)
		const readQ = `SELECT status, status_changed_at FROM users WHERE id = ?`
		if err := tx.QueryRow(ctx, readQ, id).Scan(&current, &changedAt); err != nil {
			return tursodb.MapError(err)
		}

		if user.NormalizeStatus(user.Status(current)) == status {
			out = user.StatusChange{Status: status, Changed: false, ChangedAt: changedAt.Time}
			return nil
		}

		const updateQ = `UPDATE users
			SET status = ?, status_changed_at = ?, updated_at = ?, auth_revision = auth_revision + 1
			WHERE id = ?`
		if _, err := tx.Exec(ctx, updateQ, string(status), tursodb.FormatTime(now), tursodb.FormatTime(now), id); err != nil {
			return tursodb.MapError(err)
		}

		const grantsQ = `DELETE FROM authentication_grants
			WHERE user_id = ? OR session_id IN (SELECT id FROM sessions WHERE user_id = ?)`
		if _, err := tx.Exec(ctx, grantsQ, id, id); err != nil {
			return tursodb.MapError(err)
		}

		const sessionsQ = `DELETE FROM sessions WHERE user_id = ?`
		res, err := tx.Exec(ctx, sessionsQ, id)
		if err != nil {
			return tursodb.MapError(err)
		}
		revoked, err := res.RowsAffected()
		if err != nil {
			return tursodb.MapError(err)
		}

		out = user.StatusChange{
			Status:          status,
			Changed:         true,
			ChangedAt:       now,
			RevokedSessions: int(revoked),
		}
		return nil
	})
	if err != nil {
		return user.StatusChange{}, err
	}
	return out, nil
}

// ActiveSessionStore implements session.ActiveUserRepository over libSQL: the
// fenced session mint.
//
// The status proof and the insert share one BEGIN IMMEDIATE transaction. Because
// the connector takes the write intent at BEGIN, a concurrent
// UserAdminStore.SetStatus cannot commit between the read and the insert: the two
// transactions serialize, so either the mint commits and the transition then
// deletes its session, or the transition commits first and the mint reads
// deactivated and refuses.
type ActiveSessionStore struct {
	db *tursodb.DB
}

var _ session.ActiveUserRepository = (*ActiveSessionStore)(nil)

// NewActiveSessionStore returns an ActiveSessionStore backed by db.
func NewActiveSessionStore(db *tursodb.DB) *ActiveSessionStore {
	return &ActiveSessionStore{db: db}
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

	err = s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		var status string
		const readQ = `SELECT status FROM users WHERE id = ?`
		if err := tx.QueryRow(ctx, readQ, sess.UserID).Scan(&status); err != nil {
			return tursodb.MapError(err)
		}
		if !user.NormalizeStatus(user.Status(status)).Active() {
			return session.ErrUserNotActive
		}

		const insertQ = `INSERT INTO sessions (` + sessionColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
		if _, err := tx.Exec(ctx, insertQ,
			sess.ID, sess.UserID, sess.RefreshTokenHash, nullHash(sess.PreviousRefreshTokenHash),
			tursodb.BoolToInt(sess.PreviousUsed), sess.RotationCount,
			tursodb.FormatNullTime(sess.Authentication.AuthenticatedAt), methods, string(sess.Authentication.Assurance),
			tursodb.FormatTime(sess.CreatedAt), tursodb.FormatTime(sess.ExpiresAt),
		); err != nil {
			return tursodb.MapError(err)
		}
		return nil
	})
	if err != nil {
		return session.Session{}, err
	}
	return sess, nil
}
