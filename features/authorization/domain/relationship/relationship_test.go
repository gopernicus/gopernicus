package relationship

import (
	"context"

	"github.com/gopernicus/gopernicus/sdk/crud"
)

// stubStorer pins the full 14-method Storer surface at compile time. The real
// implementations (memstore, stores/turso, stores/pgx) are conformance-tested
// in storetest; this stub only guards the port's shape from silent drift.
type stubStorer struct{}

var _ Storer = (*stubStorer)(nil)

func (stubStorer) CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error) {
	return false, nil
}

func (stubStorer) GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]RelationTarget, error) {
	return nil, nil
}

func (stubStorer) CheckRelationExists(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error) {
	return false, nil
}

func (stubStorer) CheckBatchDirect(ctx context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string) (map[string]bool, error) {
	return nil, nil
}

func (stubStorer) CreateRelationships(ctx context.Context, relationships []CreateRelationship) error {
	return nil
}

func (stubStorer) DeleteResourceRelationships(ctx context.Context, resourceType, resourceID string) error {
	return nil
}

func (stubStorer) DeleteRelationship(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) error {
	return nil
}

func (stubStorer) DeleteByResourceAndSubject(ctx context.Context, resourceType, resourceID, subjectType, subjectID string) error {
	return nil
}

func (stubStorer) CountByResourceAndRelation(ctx context.Context, resourceType, resourceID, relation string) (int, error) {
	return 0, nil
}

func (stubStorer) ListRelationshipsBySubject(ctx context.Context, subjectType, subjectID string, filter SubjectRelationshipFilter, req crud.ListRequest) (crud.Page[SubjectRelationship], error) {
	return crud.Page[SubjectRelationship]{}, nil
}

func (stubStorer) ListRelationshipsByResource(ctx context.Context, resourceType, resourceID string, filter ResourceRelationshipFilter, req crud.ListRequest) (crud.Page[ResourceRelationship], error) {
	return crud.Page[ResourceRelationship]{}, nil
}

func (stubStorer) LookupResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType, subjectID string) ([]string, error) {
	return nil, nil
}

func (stubStorer) LookupResourceIDsByRelationTarget(ctx context.Context, resourceType, relation, targetType string, targetIDs []string) ([]string, error) {
	return nil, nil
}

func (stubStorer) LookupDescendantResourceIDs(ctx context.Context, resourceType, relation, subjectType string, rootIDs []string) ([]string, error) {
	return nil, nil
}
