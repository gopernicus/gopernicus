package rolesvc

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/features/authorization/domain/role"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// fakeRoleStore is an in-package role.Storer: a set of exact assignments plus an
// optional injected error to prove fail-closed propagation.
type fakeRoleStore struct {
	rows map[string]bool // key: type|id|role|rtype|rid
	err  error
}

func key(subjectType, subjectID, roleName, resourceType, resourceID string) string {
	return subjectType + "|" + subjectID + "|" + roleName + "|" + resourceType + "|" + resourceID
}

func (f *fakeRoleStore) Assign(ctx context.Context, a role.Assignment) error {
	if f.err != nil {
		return f.err
	}
	if f.rows == nil {
		f.rows = map[string]bool{}
	}
	f.rows[key(a.SubjectType, a.SubjectID, a.Role, a.ResourceType, a.ResourceID)] = true
	return nil
}

func (f *fakeRoleStore) Unassign(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) error {
	if f.err != nil {
		return f.err
	}
	delete(f.rows, key(subjectType, subjectID, roleName, resourceType, resourceID))
	return nil
}

func (f *fakeRoleStore) HasExactRole(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.rows[key(subjectType, subjectID, roleName, resourceType, resourceID)], nil
}

func (f *fakeRoleStore) ListBySubject(ctx context.Context, subjectType, subjectID string, req crud.ListRequest) (crud.Page[role.Assignment], error) {
	return crud.Page[role.Assignment]{}, f.err
}

func (f *fakeRoleStore) ListByResource(ctx context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[role.Assignment], error) {
	return crud.Page[role.Assignment]{}, f.err
}

func TestAssignRoleIdempotentPassThrough(t *testing.T) {
	store := &fakeRoleStore{}
	svc := NewService(store)
	for range 2 {
		if err := svc.AssignRole(context.Background(), "user", "u1", "editor", "doc", "d1"); err != nil {
			t.Fatalf("AssignRole: %v", err)
		}
	}
	if len(store.rows) != 1 {
		t.Fatalf("duplicate assign must yield one row, got %d", len(store.rows))
	}
}

func TestValidationRejections(t *testing.T) {
	svc := NewService(&fakeRoleStore{})
	if err := svc.AssignRole(context.Background(), "", "u1", "editor", "", ""); !errors.Is(err, ErrInvalidRoleAssignment) {
		t.Fatalf("empty subject type: want ErrInvalidRoleAssignment, got %v", err)
	}
	if err := svc.AssignRole(context.Background(), "user", "u1", "", "", ""); !errors.Is(err, ErrInvalidRoleAssignment) {
		t.Fatalf("empty role: want ErrInvalidRoleAssignment, got %v", err)
	}
	if err := svc.AssignRole(context.Background(), "user", "u1", "editor", "doc", ""); !errors.Is(err, ErrHalfScopedAssignment) {
		t.Fatalf("half-scoped (rtype only): want ErrHalfScopedAssignment, got %v", err)
	}
	if err := svc.UnassignRole(context.Background(), "user", "u1", "editor", "", "d1"); !errors.Is(err, ErrHalfScopedAssignment) {
		t.Fatalf("half-scoped (rid only): want ErrHalfScopedAssignment, got %v", err)
	}
}

func TestHasRoleScopeRule(t *testing.T) {
	store := &fakeRoleStore{}
	svc := NewService(store)

	// Global grant satisfies a scoped query (Q5 fallback), scoped query for a
	// DIFFERENT scope does not.
	if err := svc.AssignRole(context.Background(), "user", "u1", "editor", "", ""); err != nil {
		t.Fatalf("assign global: %v", err)
	}
	if ok, err := svc.HasRole(context.Background(), "user", "u1", "editor", "doc", "d1"); err != nil || !ok {
		t.Fatalf("global grant should satisfy scoped check: ok=%v err=%v", ok, err)
	}

	// A scoped grant satisfies its own scope but not another.
	if err := svc.AssignRole(context.Background(), "user", "u2", "viewer", "doc", "d1"); err != nil {
		t.Fatalf("assign scoped: %v", err)
	}
	if ok, _ := svc.HasRole(context.Background(), "user", "u2", "viewer", "doc", "d1"); !ok {
		t.Fatalf("scoped grant should satisfy its exact scope")
	}
	if ok, _ := svc.HasRole(context.Background(), "user", "u2", "viewer", "doc", "d2"); ok {
		t.Fatalf("scoped grant must NOT satisfy a different scope")
	}

	// A miss is (false, nil).
	if ok, err := svc.HasRole(context.Background(), "user", "nobody", "editor", "doc", "d1"); err != nil || ok {
		t.Fatalf("miss must be (false, nil): ok=%v err=%v", ok, err)
	}
}

func TestHasRoleFailClosedOnStoreError(t *testing.T) {
	boom := errors.New("store down")
	svc := NewService(&fakeRoleStore{err: boom})
	ok, err := svc.HasRole(context.Background(), "user", "u1", "editor", "doc", "d1")
	if ok {
		t.Fatalf("store error must not grant access")
	}
	if !errors.Is(err, boom) {
		t.Fatalf("store error must propagate, got %v", err)
	}
}
