package firestore

import (
	"context"
	"time"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/user"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

var _ user.AdminRepository = (*userAdminStore)(nil)

// userAdminStore fills user.AdminRepository: the operator directory and the
// atomic lifecycle transition. Both directory reads answer from the users
// document's PROJECTION fields (SCHEMA.md §6) — Firestore has no join, and one
// identifier read per listed user would make a page O(page) round trips. Bodies
// land in N2b (SetStatus) and N2c (List/GetSummary).
type userAdminStore struct {
	db *firestoredb.DB
}

func newUserAdminStore(db *firestoredb.DB) *userAdminStore {
	return &userAdminStore{db: db}
}

// List pages the directory, ordered (created_at DESC, id DESC) by default.
func (s *userAdminStore) List(ctx context.Context, req crud.ListRequest) (crud.Page[user.Summary], error) {
	if err := refuseAmbient(ctx); err != nil {
		return crud.Page[user.Summary]{}, err
	}
	return crud.Page[user.Summary]{}, errNotImplemented
}

// GetSummary returns one user's directory projection, or sdk.ErrNotFound.
func (s *userAdminStore) GetSummary(ctx context.Context, id string) (user.Summary, error) {
	if err := refuseAmbient(ctx); err != nil {
		return user.Summary{}, err
	}
	return user.Summary{}, errNotImplemented
}

// SetStatus transitions the lifecycle status in ONE transaction: the status and
// its timestamp, auth_revision + 1, and the deletion of every session (with its
// refresh claim) and every grant owned by the user or bound to those sessions.
// Replaying the same status changes nothing and revokes nothing.
func (s *userAdminStore) SetStatus(ctx context.Context, id string, status user.Status, now time.Time) (user.StatusChange, error) {
	if err := refuseAmbient(ctx); err != nil {
		return user.StatusChange{}, err
	}
	return user.StatusChange{}, errNotImplemented
}
