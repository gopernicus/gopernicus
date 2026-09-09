package firestore

import (
	"context"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

var _ relationship.Storer = (*relationshipStore)(nil)

// relationshipStore fills relationship.Storer over the iam_relationships
// collection and its two claim collections (SCHEMA.md §5). Every method refuses
// an ambient transaction first (R1); the bodies land in A2a–A2d.
type relationshipStore struct {
	db *firestoredb.DB
}

func newRelationshipStore(db *firestoredb.DB) *relationshipStore {
	return &relationshipStore{db: db}
}

func (s *relationshipStore) CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string, maxExpansionStates int) (bool, error) {
	if err := refuseAmbient(ctx); err != nil {
		return false, err
	}
	return false, errNotImplemented
}

func (s *relationshipStore) GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]relationship.RelationTarget, error) {
	if err := refuseAmbient(ctx); err != nil {
		return nil, err
	}
	return nil, errNotImplemented
}

func (s *relationshipStore) FilterRelation(ctx context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string, maxExpansionStates int) ([]string, error) {
	if err := refuseAmbient(ctx); err != nil {
		return nil, err
	}
	return nil, errNotImplemented
}

func (s *relationshipStore) RelationTargetsFor(ctx context.Context, resourceType string, resourceIDs []string, relation string) (map[string][]relationship.RelationTarget, error) {
	if err := refuseAmbient(ctx); err != nil {
		return nil, err
	}
	return nil, errNotImplemented
}

func (s *relationshipStore) CheckRelationExists(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error) {
	if err := refuseAmbient(ctx); err != nil {
		return false, err
	}
	return false, errNotImplemented
}

func (s *relationshipStore) CheckBatchDirect(ctx context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string, maxExpansionStates int) (map[string]bool, error) {
	if err := refuseAmbient(ctx); err != nil {
		return nil, err
	}
	return nil, errNotImplemented
}

func (s *relationshipStore) CreateRelationships(ctx context.Context, relationships []relationship.CreateRelationship) error {
	if err := refuseAmbient(ctx); err != nil {
		return err
	}
	return errNotImplemented
}

func (s *relationshipStore) SetRelationTargets(ctx context.Context, resourceType, resourceID, relation string, targets []relationship.CreateRelationship) error {
	if err := refuseAmbient(ctx); err != nil {
		return err
	}
	return errNotImplemented
}

func (s *relationshipStore) DeleteRelationshipTarget(ctx context.Context, resourceType, resourceID, relation string, target relationship.SubjectRef) error {
	if err := refuseAmbient(ctx); err != nil {
		return err
	}
	return errNotImplemented
}

func (s *relationshipStore) DeleteResourceRelationships(ctx context.Context, resourceType, resourceID string) error {
	if err := refuseAmbient(ctx); err != nil {
		return err
	}
	return errNotImplemented
}

func (s *relationshipStore) DeleteRelationship(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) error {
	if err := refuseAmbient(ctx); err != nil {
		return err
	}
	return errNotImplemented
}

func (s *relationshipStore) DeleteByResourceAndSubject(ctx context.Context, resourceType, resourceID, subjectType, subjectID string) error {
	if err := refuseAmbient(ctx); err != nil {
		return err
	}
	return errNotImplemented
}

func (s *relationshipStore) CountByResourceAndRelation(ctx context.Context, resourceType, resourceID, relation string) (int, error) {
	if err := refuseAmbient(ctx); err != nil {
		return 0, err
	}
	return 0, errNotImplemented
}

func (s *relationshipStore) ListRelationshipsBySubject(ctx context.Context, subjectType, subjectID string, filter relationship.SubjectRelationshipFilter, req crud.ListRequest) (crud.Page[relationship.SubjectRelationship], error) {
	if err := refuseAmbient(ctx); err != nil {
		return crud.Page[relationship.SubjectRelationship]{}, err
	}
	return crud.Page[relationship.SubjectRelationship]{}, errNotImplemented
}

func (s *relationshipStore) ListRelationshipsByResource(ctx context.Context, resourceType, resourceID string, filter relationship.ResourceRelationshipFilter, req crud.ListRequest) (crud.Page[relationship.ResourceRelationship], error) {
	if err := refuseAmbient(ctx); err != nil {
		return crud.Page[relationship.ResourceRelationship]{}, err
	}
	return crud.Page[relationship.ResourceRelationship]{}, errNotImplemented
}

func (s *relationshipStore) LookupResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType, subjectID, after string, limit int) ([]string, error) {
	if err := refuseAmbient(ctx); err != nil {
		return nil, err
	}
	return nil, errNotImplemented
}

func (s *relationshipStore) LookupResourceIDsByRelationTarget(ctx context.Context, resourceType, relation, targetType string, targetIDs []string, after string, limit int) ([]string, error) {
	if err := refuseAmbient(ctx); err != nil {
		return nil, err
	}
	return nil, errNotImplemented
}

func (s *relationshipStore) LookupDescendantResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType string, rootIDs []string, after string, limit int) ([]string, error) {
	if err := refuseAmbient(ctx); err != nil {
		return nil, err
	}
	return nil, errNotImplemented
}
