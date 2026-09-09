package firestore

import (
	"context"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/role"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

var _ role.Storer = (*roleStore)(nil)

// roleStore fills role.Storer over the iam_roles collection, whose document id
// IS the unique 5-tuple (SCHEMA.md §3.2). Every method refuses an ambient
// transaction first (R1); the bodies land in A3a–A3b.
type roleStore struct {
	db *firestoredb.DB
}

func newRoleStore(db *firestoredb.DB) *roleStore {
	return &roleStore{db: db}
}

func (s *roleStore) Assign(ctx context.Context, a role.Assignment) error {
	if err := refuseAmbient(ctx); err != nil {
		return err
	}
	return errNotImplemented
}

func (s *roleStore) Unassign(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) error {
	if err := refuseAmbient(ctx); err != nil {
		return err
	}
	return errNotImplemented
}

func (s *roleStore) HasExactRole(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) (bool, error) {
	if err := refuseAmbient(ctx); err != nil {
		return false, err
	}
	return false, errNotImplemented
}

func (s *roleStore) ListBySubject(ctx context.Context, subjectType, subjectID string, req crud.ListRequest) (crud.Page[role.Assignment], error) {
	if err := refuseAmbient(ctx); err != nil {
		return crud.Page[role.Assignment]{}, err
	}
	return crud.Page[role.Assignment]{}, errNotImplemented
}

func (s *roleStore) ListByResource(ctx context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[role.Assignment], error) {
	if err := refuseAmbient(ctx); err != nil {
		return crud.Page[role.Assignment]{}, err
	}
	return crud.Page[role.Assignment]{}, errNotImplemented
}

func (s *roleStore) ListEffectiveByResource(ctx context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[role.EffectiveGrant], error) {
	if err := refuseAmbient(ctx); err != nil {
		return crud.Page[role.EffectiveGrant]{}, err
	}
	return crud.Page[role.EffectiveGrant]{}, errNotImplemented
}

func (s *roleStore) LookupResourceIDsBySubjectAndRoles(ctx context.Context, subjectType, subjectID, resourceType string, roles []string, after string, limit int) ([]string, bool, error) {
	if err := refuseAmbient(ctx); err != nil {
		return nil, false, err
	}
	return nil, false, errNotImplemented
}
