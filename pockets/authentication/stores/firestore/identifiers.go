package firestore

import (
	"context"
	"time"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/identifier"
)

var _ identifier.IdentifierRepository = (*identifierStore)(nil)

// identifierStore fills identifier.IdentifierRepository over the
// user_identifiers collection and its two claim collections — the authentication
// claim and the active-primary claim (SCHEMA.md §5.1, §5.2). Bodies land in
// N2a/N2c.
type identifierStore struct {
	db *firestoredb.DB
}

func newIdentifierStore(db *firestoredb.DB) *identifierStore {
	return &identifierStore{db: db}
}

// Get returns the identifier with the given id (active or replaced), or
// sdk.ErrNotFound.
func (s *identifierStore) Get(ctx context.Context, id string) (identifier.Identifier, error) {
	if err := refuseAmbient(ctx); err != nil {
		return identifier.Identifier{}, err
	}
	return identifier.Identifier{}, errNotImplemented
}

// GetLogin returns the active login-enabled identifier claiming
// (kind, normalizedValue) — resolved through the authentication claim document,
// never an equality filter on unbounded address text.
func (s *identifierStore) GetLogin(ctx context.Context, kind, normalizedValue string) (identifier.Identifier, error) {
	if err := refuseAmbient(ctx); err != nil {
		return identifier.Identifier{}, err
	}
	return identifier.Identifier{}, errNotImplemented
}

// GetRecovery returns the active recovery-enabled identifier claiming
// (kind, normalizedValue), or sdk.ErrNotFound.
func (s *identifierStore) GetRecovery(ctx context.Context, kind, normalizedValue string) (identifier.Identifier, error) {
	if err := refuseAmbient(ctx); err != nil {
		return identifier.Identifier{}, err
	}
	return identifier.Identifier{}, errNotImplemented
}

// ListByUser returns the user's ACTIVE identifiers, ordered (created_at, id).
func (s *identifierStore) ListByUser(ctx context.Context, userID string) ([]identifier.Identifier, error) {
	if err := refuseAmbient(ctx); err != nil {
		return nil, err
	}
	return nil, errNotImplemented
}

// ApplyVerifiedChange applies a confirmed change atomically: revision CAS, the
// retirement of the replaced and displaced rows, the new claim, the directory
// projection, and the auth_revision increment, in one transaction.
func (s *identifierStore) ApplyVerifiedChange(ctx context.Context, input identifier.ApplyVerifiedChangeInput, expectedAuthRevision int64, verifiedAt time.Time) (identifier.Identifier, error) {
	if err := refuseAmbient(ctx); err != nil {
		return identifier.Identifier{}, err
	}
	return identifier.Identifier{}, errNotImplemented
}
