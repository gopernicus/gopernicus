package firestore

import (
	"context"
	"time"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
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
//
// It is ONE query over the users collection and reads NOTHING else: the active
// primary email and its verification come from the row's own projection fields,
// which is how this store satisfies the port's explicit rule that an
// implementation must not issue one identifier read per user (SCHEMA.md §6.1).
//
// A non-blank req.Search is refused with sdk.ErrInvalidInput by the connector's
// List, because this ListQuery declares no PostFilter: user.OrderFields exposes
// nothing searchable, and a top-level users-directory search is a plan-level
// decision rather than an accidental full collection scan (ruling R4).
func (s *userAdminStore) List(ctx context.Context, req list.Request) (list.Page[user.Summary], error) {
	if err := refuseAmbient(ctx); err != nil {
		return list.Page[user.Summary]{}, err
	}
	return firestoredb.List(ctx, s.db.ReaderFrom(ctx), listUsers(s.db), req)
}

// GetSummary returns one user's directory projection, or sdk.ErrNotFound. It
// reads the SAME projection the page does, from the same document, so the two
// can never disagree.
func (s *userAdminStore) GetSummary(ctx context.Context, id string) (user.Summary, error) {
	if err := refuseAmbient(ctx); err != nil {
		return user.Summary{}, err
	}
	row, err := readUser(ctx, s.db, s.db.ReaderFrom(ctx), id)
	if err != nil {
		return user.Summary{}, err
	}
	return row.summary()
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
