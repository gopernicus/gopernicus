// Package usersstore is the GOLDEN REFERENCE for generator-v2 output: the
// dialect-neutral store the spec-emitting template will generate for the
// users entity. It satisfies the existing users.Storer byte-for-byte at the
// interface level, so it can replace userspgx behind the unchanged
// Repository on either database.
//
// Shape: a declarative crud.Spec (what queries.sql + the schema snapshot
// determine), a Store embedding *crud.Store for the generic verbs, named
// record-state wrappers, and the entity's custom @func methods as literal
// SQL sharing the store's scanner and dialect. Transactional custom methods
// run through the injected crud-querier transaction runner.
package usersstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/core/repositories/auth/users"
	"github.com/gopernicus/gopernicus/infrastructure/database/crud"
	"github.com/gopernicus/gopernicus/sdk/errs"
)

// TxRunner executes fn inside a transaction, providing a tx-scoped querier.
// Wiring supplies it per driver (pgx tx + pgxq, or moderncdb tx + sqliteq).
type TxRunner func(ctx context.Context, fn func(crud.Querier) error) error

// Spec is the declarative description of the users entity — the part the
// generator derives from queries.sql and the reflected schema.
func Spec() crud.Spec[users.User, users.FilterList, users.CreateUser, users.UpdateUser] {
	return crud.Spec[users.User, users.FilterList, users.CreateUser, users.UpdateUser]{
		Table:   "users",
		PK:      "user_id",
		Columns: []string{"user_id", "email", "display_name", "email_verified", "last_login_at", "record_state", "created_at", "updated_at"},
		Filters: func(f users.FilterList) []crud.Pred {
			var preds []crud.Pred
			if f.UserID != nil {
				preds = append(preds, crud.Pred{Col: "user_id", Op: crud.OpEq, Val: *f.UserID})
			}
			if f.Email != nil {
				preds = append(preds, crud.Pred{Col: "email", Op: crud.OpEq, Val: *f.Email})
			}
			if f.DisplayName != nil {
				preds = append(preds, crud.Pred{Col: "display_name", Op: crud.OpEq, Val: *f.DisplayName})
			}
			if f.EmailVerified != nil {
				preds = append(preds, crud.Pred{Col: "email_verified", Op: crud.OpEq, Val: *f.EmailVerified})
			}
			if f.LastLoginAt != nil {
				preds = append(preds, crud.Pred{Col: "last_login_at", Op: crud.OpEq, Val: *f.LastLoginAt})
			}
			if f.LastLoginAtAfter != nil {
				preds = append(preds, crud.Pred{Col: "last_login_at", Op: crud.OpGT, Val: *f.LastLoginAtAfter})
			}
			if f.LastLoginAtBefore != nil {
				preds = append(preds, crud.Pred{Col: "last_login_at", Op: crud.OpLT, Val: *f.LastLoginAtBefore})
			}
			if f.RecordState != nil {
				preds = append(preds, crud.Pred{Col: "record_state", Op: crud.OpEq, Val: *f.RecordState})
			}
			if f.CreatedAt != nil {
				preds = append(preds, crud.Pred{Col: "created_at", Op: crud.OpEq, Val: *f.CreatedAt})
			}
			if f.CreatedAtAfter != nil {
				preds = append(preds, crud.Pred{Col: "created_at", Op: crud.OpGT, Val: *f.CreatedAtAfter})
			}
			if f.CreatedAtBefore != nil {
				preds = append(preds, crud.Pred{Col: "created_at", Op: crud.OpLT, Val: *f.CreatedAtBefore})
			}
			if f.UpdatedAt != nil {
				preds = append(preds, crud.Pred{Col: "updated_at", Op: crud.OpEq, Val: *f.UpdatedAt})
			}
			if f.UpdatedAtAfter != nil {
				preds = append(preds, crud.Pred{Col: "updated_at", Op: crud.OpGT, Val: *f.UpdatedAtAfter})
			}
			if f.UpdatedAtBefore != nil {
				preds = append(preds, crud.Pred{Col: "updated_at", Op: crud.OpLT, Val: *f.UpdatedAtBefore})
			}
			return preds
		},
		Search:        &crud.SearchSpec{Strategy: crud.SearchContains, Fields: []string{"email", "display_name"}},
		SearchTerm:    func(f users.FilterList) *string { return f.SearchTerm },
		AuthorizedIDs: func(f users.FilterList) []string { return f.AuthorizedIDs },
		Creates: func(c users.CreateUser) []crud.Set {
			return []crud.Set{
				{Col: "user_id", Val: c.UserID},
				{Col: "email", Val: c.Email},
				{Col: "display_name", Val: c.DisplayName},
				{Col: "email_verified", Val: c.EmailVerified},
				{Col: "last_login_at", Val: c.LastLoginAt},
				{Col: "record_state", Val: c.RecordState},
			}
		},
		Updates: func(u users.UpdateUser) []crud.Set {
			var sets []crud.Set
			if u.Email != nil {
				sets = append(sets, crud.Set{Col: "email", Val: *u.Email})
			}
			if u.DisplayName != nil {
				sets = append(sets, crud.Set{Col: "display_name", Val: *u.DisplayName})
			}
			if u.EmailVerified != nil {
				sets = append(sets, crud.Set{Col: "email_verified", Val: *u.EmailVerified})
			}
			if u.LastLoginAt != nil {
				sets = append(sets, crud.Set{Col: "last_login_at", Val: *u.LastLoginAt})
			}
			if u.UpdatedAt != nil {
				sets = append(sets, crud.Set{Col: "updated_at", Val: *u.UpdatedAt})
			}
			return sets
		},
		AutoNow:        []string{"updated_at"},
		AutoNowCreate:  []string{"created_at", "updated_at"},
		RecordStateCol: "record_state",
		OrderFields: map[string]crud.OrderField{
			"user_id":       {Col: "user_id"},
			"email":         {Col: "email", CastLower: true},
			"display_name":  {Col: "display_name", CastLower: true},
			"last_login_at": {Col: "last_login_at"},
			"record_state":  {Col: "record_state"},
			"created_at":    {Col: "created_at"},
			"updated_at":    {Col: "updated_at"},
		},
		DefaultOrder: "created_at",
		MapError: func(err error) error {
			switch {
			case errors.Is(err, crud.ErrNotFound), errors.Is(err, errs.ErrNotFound):
				return users.ErrUserNotFound
			case errors.Is(err, errs.ErrAlreadyExists):
				return users.ErrUserAlreadyExists
			case errors.Is(err, errs.ErrInvalidReference):
				return users.ErrUserInvalidReference
			default:
				return err
			}
		},
	}
}

// Store satisfies users.Storer on any crud-supported database.
type Store struct {
	*crud.Store[users.User, users.FilterList, users.CreateUser, users.UpdateUser]
	inTx TxRunner
}

var _ users.Storer = (*Store)(nil)

// NewStore builds the users store over a querier, dialect, and transaction
// runner for the same connection.
func NewStore(q crud.Querier, d crud.Dialect, inTx TxRunner) (*Store, error) {
	generic, err := crud.NewStore(q, d, Spec())
	if err != nil {
		return nil, err
	}
	return &Store{Store: generic, inTx: inTx}, nil
}

// SoftDelete / Archive / Restore are record-state transitions.
func (s *Store) SoftDelete(ctx context.Context, userID string) error {
	return s.SetRecordState(ctx, userID, "deleted")
}

func (s *Store) Archive(ctx context.Context, userID string) error {
	return s.SetRecordState(ctx, userID, "archived")
}

func (s *Store) Restore(ctx context.Context, userID string) error {
	return s.SetRecordState(ctx, userID, "active")
}

// Create is the entity's custom transactional method: principal row first,
// then the user row, atomically. ON CONFLICT DO NOTHING is portable across
// Postgres and SQLite.
func (s *Store) Create(ctx context.Context, input users.CreateUser) (users.User, error) {
	var created users.User
	err := s.inTx(ctx, func(q crud.Querier) error {
		args := crud.NewArgs(s.Dialect())
		principalQuery := "INSERT INTO principals (principal_id, principal_type, created_at) VALUES (" +
			args.Add(input.UserID) + ", " + args.Add("user") + ", " + args.Add(s.Dialect().TimeArg(time.Now().UTC())) +
			") ON CONFLICT (principal_id) DO NOTHING"
		if _, err := q.Exec(ctx, principalQuery, args.Values()...); err != nil {
			return err
		}

		record, err := s.WithQuerier(q).Create(ctx, input)
		if err != nil {
			return err
		}
		created = record
		return nil
	})
	if err != nil {
		return users.User{}, err
	}
	return created, nil
}

// GetByEmail is a generated custom @func: literal SQL through the store's
// dialect and scanner.
func (s *Store) GetByEmail(ctx context.Context, email string) (users.User, error) {
	args := crud.NewArgs(s.Dialect())
	query := "SELECT user_id, email, display_name, email_verified, last_login_at, record_state, created_at, updated_at FROM users WHERE email = " + args.Add(email)

	rows, err := s.Querier().Query(ctx, query, args.Values()...)
	if err != nil {
		return users.User{}, s.mapErr(err)
	}
	record, err := crud.ScanOne[users.User](rows, s.Dialect())
	if err != nil {
		return users.User{}, s.mapErr(err)
	}
	return record, nil
}

// SetEmailVerified is a generated custom @func (exec).
func (s *Store) SetEmailVerified(ctx context.Context, updatedAt time.Time, userID string) error {
	args := crud.NewArgs(s.Dialect())
	query := "UPDATE users SET email_verified = TRUE, updated_at = " + args.Add(s.Dialect().TimeArg(updatedAt)) +
		" WHERE user_id = " + args.Add(userID)

	affected, err := s.Querier().Exec(ctx, query, args.Values()...)
	if err != nil {
		return s.mapErr(err)
	}
	if affected == 0 {
		return users.ErrUserNotFound
	}
	return nil
}

// SetLastLogin is a generated custom @func (exec).
func (s *Store) SetLastLogin(ctx context.Context, lastLoginAt time.Time, updatedAt time.Time, userID string) error {
	args := crud.NewArgs(s.Dialect())
	query := "UPDATE users SET last_login_at = " + args.Add(s.Dialect().TimeArg(lastLoginAt)) +
		", updated_at = " + args.Add(s.Dialect().TimeArg(updatedAt)) +
		" WHERE user_id = " + args.Add(userID)

	affected, err := s.Querier().Exec(ctx, query, args.Values()...)
	if err != nil {
		return s.mapErr(err)
	}
	if affected == 0 {
		return users.ErrUserNotFound
	}
	return nil
}

func (s *Store) mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, crud.ErrNotFound) || errors.Is(err, errs.ErrNotFound) {
		return users.ErrUserNotFound
	}
	if errors.Is(err, errs.ErrAlreadyExists) {
		return fmt.Errorf("users: %w", users.ErrUserAlreadyExists)
	}
	return err
}
