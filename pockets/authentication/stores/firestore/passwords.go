package firestore

import (
	"context"
	"errors"
	"time"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
)

var _ user.PasswordRepository = (*passwordStore)(nil)

// passwordStore fills user.PasswordRepository over the user_passwords
// collection, whose document id IS the user id — so the port's upsert is a
// document Set (SCHEMA.md §3.2).
type passwordStore struct {
	db *firestoredb.DB
}

func newPasswordStore(db *firestoredb.DB) *passwordStore {
	return &passwordStore{db: db}
}

// Set replaces the password while advancing the credential revision and revoking
// sessions, grants and password-reset challenges in the same transaction.
func (s *passwordStore) Set(ctx context.Context, userID, hash string) error {
	_, err := s.change(ctx, userID, user.PasswordChange{NewHash: hash, Now: time.Now().UTC()}, false)
	return err
}
func (s *passwordStore) Change(ctx context.Context, userID string, change user.PasswordChange) (int64, error) {
	return s.change(ctx, userID, change, true)
}
func (s *passwordStore) change(ctx context.Context, userID string, change user.PasswordChange, conditional bool) (int64, error) {
	if err := refuseAmbient(ctx); err != nil {
		return 0, err
	}
	var revision int64
	err := retryTransact(ctx, s.db, func(ctx context.Context) error {
		owner, err := readUser(ctx, s.db, s.db.ReaderFrom(ctx), userID)
		if err != nil {
			return err
		}
		if !user.NormalizeStatus(user.Status(owner.Status)).Active() {
			return session.ErrUserNotActive
		}
		if conditional {
			if owner.AuthRevision != change.ExpectedAuthRevision {
				return sdk.ErrConflict
			}
			current, err := readPassword(ctx, s.db, s.db.ReaderFrom(ctx), userID)
			if err != nil && !errors.Is(err, sdk.ErrNotFound) {
				return err
			}
			if current != change.ExpectedHash {
				return sdk.ErrConflict
			}
		}
		revoke, err := readCredentialRevocations(ctx, s.db, userID)
		if err != nil {
			return err
		}
		w := s.db.WriterFrom(ctx)
		plan := newClaimPlan()
		if err := putPassword(ctx, s.db, w, userID, change.NewHash); err != nil {
			return err
		}
		revision = owner.AuthRevision + 1
		if err := advanceCredentialRevision(ctx, s.db, w, userID, revision, change.Now); err != nil {
			return err
		}
		if err := revoke.apply(ctx, s.db, w, plan); err != nil {
			return err
		}
		return plan.commit(ctx, w)
	})
	if err != nil {
		return 0, err
	}
	return revision, nil
}

// Get returns the stored hash, or sdk.ErrNotFound.
func (s *passwordStore) Get(ctx context.Context, userID string) (string, error) {
	if err := refuseAmbient(ctx); err != nil {
		return "", err
	}
	return readPassword(ctx, s.db, s.db.ReaderFrom(ctx), userID)
}
