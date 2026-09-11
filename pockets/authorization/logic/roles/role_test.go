package roles

import (
	"context"

	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// stubStorer pins the role.Storer surface at compile time. Behavioral
// conformance lives in storetest's Roles/* family.
type stubStorer struct{}

var _ Storer = (*stubStorer)(nil)

func (stubStorer) Assign(ctx context.Context, a Assignment) error { return nil }

func (stubStorer) Unassign(ctx context.Context, subjectType, subjectID, role, resourceType, resourceID string) error {
	return nil
}

func (stubStorer) HasExactRole(ctx context.Context, subjectType, subjectID, role, resourceType, resourceID string) (bool, error) {
	return false, nil
}

func (stubStorer) ListBySubject(ctx context.Context, subjectType, subjectID string, req list.Request) (list.Page[Assignment], error) {
	return list.Page[Assignment]{}, nil
}

func (stubStorer) ListByResource(ctx context.Context, resourceType, resourceID string, req list.Request) (list.Page[Assignment], error) {
	return list.Page[Assignment]{}, nil
}

func (stubStorer) LookupResourceIDsBySubjectAndRoles(ctx context.Context, subjectType, subjectID, resourceType string, roles []string, after string, limit int) ([]string, bool, error) {
	return nil, false, nil
}

func (stubStorer) ListEffectiveByResource(ctx context.Context, resourceType, resourceID string, req list.Request) (list.Page[EffectiveGrant], error) {
	return list.Page[EffectiveGrant]{}, nil
}
