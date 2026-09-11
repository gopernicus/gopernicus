package relationships

import (
	"context"

	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// stubStorer pins the full 16-method Storer surface at compile time. The real
// implementations (memstore, stores/turso, stores/pgx) are conformance-tested
// in storetest; this stub only guards the port's shape from silent drift.
type stubStorer struct{}

var _ Storer = (*stubStorer)(nil)

func (stubStorer) CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string, maxExpansionStates int) (bool, error) {
	return false, nil
}

func (stubStorer) GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]RelationTarget, error) {
	return nil, nil
}

func (stubStorer) FilterRelation(ctx context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string, maxExpansionStates int) ([]string, error) {
	return nil, nil
}

func (stubStorer) RelationTargetsFor(ctx context.Context, resourceType string, resourceIDs []string, relation string) (map[string][]RelationTarget, error) {
	return nil, nil
}

func (stubStorer) CheckRelationExists(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error) {
	return false, nil
}

func (stubStorer) CheckBatchDirect(ctx context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string, maxExpansionStates int) (map[string]bool, error) {
	return nil, nil
}

func (stubStorer) CreateRelationships(ctx context.Context, relationships []CreateRelationship) error {
	return nil
}

func (stubStorer) SetRelationTargets(ctx context.Context, resourceType, resourceID, relationName string, targets []CreateRelationship) error {
	return nil
}

func (stubStorer) DeleteRelationshipTarget(ctx context.Context, resourceType, resourceID, relationName string, target SubjectRef) error {
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

func (stubStorer) ListRelationshipsBySubject(ctx context.Context, subjectType, subjectID string, filter SubjectRelationshipFilter, req list.Request) (list.Page[SubjectRelationship], error) {
	return list.Page[SubjectRelationship]{}, nil
}

func (stubStorer) ListRelationshipsByResource(ctx context.Context, resourceType, resourceID string, filter ResourceRelationshipFilter, req list.Request) (list.Page[ResourceRelationship], error) {
	return list.Page[ResourceRelationship]{}, nil
}

func (stubStorer) LookupResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType, subjectID, after string, limit int) ([]string, error) {
	return nil, nil
}

func (stubStorer) LookupResourceIDsByRelationTarget(ctx context.Context, resourceType, relation, targetType string, targetIDs []string, after string, limit int) ([]string, error) {
	return nil, nil
}

func (stubStorer) LookupDescendantResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType string, rootIDs []string, after string, limit int) ([]string, error) {
	return nil, nil
}

func (s *stubStorer) ForModel(ReadModel) Reader { return s }
