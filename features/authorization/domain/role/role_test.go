package role

import (
	"context"

	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
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

func (stubStorer) ListBySubject(ctx context.Context, subjectType, subjectID string, req crud.ListRequest) (crud.Page[Assignment], error) {
	return crud.Page[Assignment]{}, nil
}

func (stubStorer) ListByResource(ctx context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[Assignment], error) {
	return crud.Page[Assignment]{}, nil
}

func (stubStorer) ListEffectiveByResource(ctx context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[EffectiveGrant], error) {
	return crud.Page[EffectiveGrant]{}, nil
}
