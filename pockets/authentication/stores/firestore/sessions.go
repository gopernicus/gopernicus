package firestore

import (
	"context"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/session"
)

var (
	_ session.SessionRepository    = (*sessionStore)(nil)
	_ session.ActiveUserRepository = (*activeSessionStore)(nil)
)

// sessionStore fills session.SessionRepository over the sessions collection and
// the current-refresh-hash claim (SCHEMA.md §5.3). Bodies land in N3a.
type sessionStore struct {
	db *firestoredb.DB
}

func newSessionStore(db *firestoredb.DB) *sessionStore {
	return &sessionStore{db: db}
}

// Create persists a new session and claims its current refresh hash; a
// collision is sdk.ErrAlreadyExists.
func (s *sessionStore) Create(ctx context.Context, sess session.Session) (session.Session, error) {
	if err := refuseAmbient(ctx); err != nil {
		return session.Session{}, err
	}
	return session.Session{}, errNotImplemented
}

// Get returns the live session: unknown → sdk.ErrNotFound, expired →
// sdk.ErrExpired at the read clock.
func (s *sessionStore) Get(ctx context.Context, id string) (session.Session, error) {
	if err := refuseAmbient(ctx); err != nil {
		return session.Session{}, err
	}
	return session.Session{}, errNotImplemented
}

// GetByRefreshHash returns the session whose CURRENT or PREVIOUS hash equals
// hash, verbatim, reporting which slot matched. An empty hash never matches.
func (s *sessionStore) GetByRefreshHash(ctx context.Context, hash string) (session.Session, session.RefreshMatch, error) {
	if err := refuseAmbient(ctx); err != nil {
		return session.Session{}, 0, err
	}
	return session.Session{}, 0, errNotImplemented
}

// Rotate compare-and-swaps the live refresh token, moving the expected current
// hash into the grace slot and re-claiming the new one, without touching
// expires_at. A lost CAS is session.ErrRotationConflict.
func (s *sessionStore) Rotate(ctx context.Context, id, expectedCurrentHash, newHash string) error {
	if err := refuseAmbient(ctx); err != nil {
		return err
	}
	return errNotImplemented
}

// ConsumeGrace flips the grace slot's consumed flag under CAS, RETAINING the
// previous hash so a later reuse still resolves the session for revocation.
func (s *sessionStore) ConsumeGrace(ctx context.Context, id, previousHash string) error {
	if err := refuseAmbient(ctx); err != nil {
		return err
	}
	return errNotImplemented
}

// Delete removes the session and releases its hash claim; unknown →
// sdk.ErrNotFound.
func (s *sessionStore) Delete(ctx context.Context, id string) error {
	if err := refuseAmbient(ctx); err != nil {
		return err
	}
	return errNotImplemented
}

// DeleteByUser removes every session of userID and their claims. It is bulk and
// idempotent: zero matches is nil.
func (s *sessionStore) DeleteByUser(ctx context.Context, userID string) error {
	if err := refuseAmbient(ctx); err != nil {
		return err
	}
	return errNotImplemented
}

// activeSessionStore fills session.ActiveUserRepository: the fenced mint that
// proves the owner active inside the transaction that inserts the session.
type activeSessionStore struct {
	db *firestoredb.DB
}

func newActiveSessionStore(db *firestoredb.DB) *activeSessionStore {
	return &activeSessionStore{db: db}
}

// CreateForActiveUser inserts sess only while its owning user is active:
// unknown → sdk.ErrNotFound, deactivated → session.ErrUserNotActive with
// nothing written.
func (s *activeSessionStore) CreateForActiveUser(ctx context.Context, sess session.Session) (session.Session, error) {
	if err := refuseAmbient(ctx); err != nil {
		return session.Session{}, err
	}
	return session.Session{}, errNotImplemented
}
