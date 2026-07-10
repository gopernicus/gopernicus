package authorization

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/features/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/features/authorization/domain/role"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// relFake is a trivial relationship.Storer for socket wiring/delegation tests.
type relFake struct{ checkCalls int }

func (f *relFake) CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error) {
	f.checkCalls++
	return false, nil
}
func (f *relFake) GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]relationship.RelationTarget, error) {
	return nil, nil
}
func (f *relFake) CheckRelationExists(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error) {
	return false, nil
}
func (f *relFake) CheckBatchDirect(ctx context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string) (map[string]bool, error) {
	return map[string]bool{}, nil
}
func (f *relFake) CreateRelationships(ctx context.Context, relationships []relationship.CreateRelationship) error {
	return nil
}
func (f *relFake) DeleteResourceRelationships(ctx context.Context, resourceType, resourceID string) error {
	return nil
}
func (f *relFake) DeleteRelationship(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) error {
	return nil
}
func (f *relFake) DeleteByResourceAndSubject(ctx context.Context, resourceType, resourceID, subjectType, subjectID string) error {
	return nil
}
func (f *relFake) CountByResourceAndRelation(ctx context.Context, resourceType, resourceID, relation string) (int, error) {
	return 0, nil
}
func (f *relFake) ListRelationshipsBySubject(ctx context.Context, subjectType, subjectID string, filter relationship.SubjectRelationshipFilter, req crud.ListRequest) (crud.Page[relationship.SubjectRelationship], error) {
	return crud.Page[relationship.SubjectRelationship]{}, nil
}
func (f *relFake) ListRelationshipsByResource(ctx context.Context, resourceType, resourceID string, filter relationship.ResourceRelationshipFilter, req crud.ListRequest) (crud.Page[relationship.ResourceRelationship], error) {
	return crud.Page[relationship.ResourceRelationship]{}, nil
}
func (f *relFake) LookupResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType, subjectID string) ([]string, error) {
	return nil, nil
}
func (f *relFake) LookupResourceIDsByRelationTarget(ctx context.Context, resourceType, relation, targetType string, targetIDs []string) ([]string, error) {
	return nil, nil
}
func (f *relFake) LookupDescendantResourceIDs(ctx context.Context, resourceType, relation, subjectType string, rootIDs []string) ([]string, error) {
	return nil, nil
}

// roleFake is a trivial role.Storer for socket wiring/delegation tests.
type roleFake struct{ assignCalls int }

func (f *roleFake) Assign(ctx context.Context, a role.Assignment) error { f.assignCalls++; return nil }
func (f *roleFake) Unassign(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) error {
	return nil
}
func (f *roleFake) HasExactRole(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) (bool, error) {
	return false, nil
}
func (f *roleFake) ListBySubject(ctx context.Context, subjectType, subjectID string, req crud.ListRequest) (crud.Page[role.Assignment], error) {
	return crud.Page[role.Assignment]{}, nil
}
func (f *roleFake) ListByResource(ctx context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[role.Assignment], error) {
	return crud.Page[role.Assignment]{}, nil
}

func validModel() Schema {
	return NewSchema([]ResourceSchema{{
		Name: "post",
		Def: ResourceTypeDef{
			Relations:   map[string]RelationDef{"owner": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
			Permissions: map[string]PermissionRule{"delete": AnyOf(Direct("owner"))},
		},
	}})
}

func TestNewServiceZeroKinds(t *testing.T) {
	_, err := NewService(Repositories{}, Config{})
	if !errors.Is(err, ErrNoKindConfigured) {
		t.Fatalf("want ErrNoKindConfigured, got %v", err)
	}
}

func TestNewServicePartialWiring(t *testing.T) {
	// Relationships without a Model.
	if _, err := NewService(Repositories{Relationships: &relFake{}}, Config{}); !errors.Is(err, ErrModelRequired) {
		t.Fatalf("rel-without-model: want ErrModelRequired, got %v", err)
	}
	// Model without Relationships.
	if _, err := NewService(Repositories{Roles: &roleFake{}}, Config{Model: validModel()}); !errors.Is(err, ErrModelRequired) {
		t.Fatalf("model-without-rel: want ErrModelRequired, got %v", err)
	}
}

func TestNewServiceInvalidModel(t *testing.T) {
	bad := NewSchema([]ResourceSchema{{
		Name: "post",
		Def: ResourceTypeDef{
			Relations:   map[string]RelationDef{"owner": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
			Permissions: map[string]PermissionRule{"delete": AnyOf(Direct("nonexistent"))},
		},
	}})
	_, err := NewService(Repositories{Relationships: &relFake{}}, Config{Model: bad})
	if err == nil || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("want a schema validation error, got %v", err)
	}
}

func TestNewServiceRolesOnlySucceeds(t *testing.T) {
	if _, err := NewService(Repositories{Roles: &roleFake{}}, Config{}); err != nil {
		t.Fatalf("roles-only wiring should succeed with no model: %v", err)
	}
}

func TestNewServiceRelationshipsOnlySucceeds(t *testing.T) {
	if _, err := NewService(Repositories{Relationships: &relFake{}}, Config{Model: validModel()}); err != nil {
		t.Fatalf("relationships-only wiring should succeed: %v", err)
	}
}

func TestUnwiredRelationshipSentinel(t *testing.T) {
	svc, err := NewService(Repositories{Roles: &roleFake{}}, Config{})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := svc.Check(context.Background(), CheckRequest{}); !errors.Is(err, ErrRelationshipsNotConfigured) {
		t.Fatalf("Check: want ErrRelationshipsNotConfigured, got %v", err)
	}
	if err := svc.CreateRelationships(context.Background(), []CreateRelationship{{}}); !errors.Is(err, ErrRelationshipsNotConfigured) {
		t.Fatalf("CreateRelationships: want ErrRelationshipsNotConfigured, got %v", err)
	}
	if _, err := svc.GetSchema(); !errors.Is(err, ErrRelationshipsNotConfigured) {
		t.Fatalf("GetSchema: want ErrRelationshipsNotConfigured, got %v", err)
	}
}

func TestUnwiredRolesSentinel(t *testing.T) {
	svc, err := NewService(Repositories{Relationships: &relFake{}}, Config{Model: validModel()})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if err := svc.AssignRole(context.Background(), Subject{Type: "user", ID: "u1"}, "editor", "", ""); !errors.Is(err, ErrRolesNotConfigured) {
		t.Fatalf("AssignRole: want ErrRolesNotConfigured, got %v", err)
	}
	if _, err := svc.HasRole(context.Background(), Subject{Type: "user", ID: "u1"}, "editor", "", ""); !errors.Is(err, ErrRolesNotConfigured) {
		t.Fatalf("HasRole: want ErrRolesNotConfigured, got %v", err)
	}
}

func TestUsersetSubjectRejectedOnEveryRoleMethod(t *testing.T) {
	svc, err := NewService(Repositories{Roles: &roleFake{}}, Config{})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	userset := Subject{Type: "group", ID: "eng", Relation: "member"}
	if err := svc.AssignRole(context.Background(), userset, "editor", "", ""); !errors.Is(err, ErrUsersetSubjectOnRole) {
		t.Fatalf("AssignRole: want ErrUsersetSubjectOnRole, got %v", err)
	}
	if err := svc.UnassignRole(context.Background(), userset, "editor", "", ""); !errors.Is(err, ErrUsersetSubjectOnRole) {
		t.Fatalf("UnassignRole: want ErrUsersetSubjectOnRole, got %v", err)
	}
	if _, err := svc.HasRole(context.Background(), userset, "editor", "", ""); !errors.Is(err, ErrUsersetSubjectOnRole) {
		t.Fatalf("HasRole: want ErrUsersetSubjectOnRole, got %v", err)
	}
	if _, err := svc.ListRoleAssignmentsBySubject(context.Background(), userset, crud.ListRequest{}); !errors.Is(err, ErrUsersetSubjectOnRole) {
		t.Fatalf("ListRoleAssignmentsBySubject: want ErrUsersetSubjectOnRole, got %v", err)
	}
}

func TestDelegationSmokeBothKinds(t *testing.T) {
	rel := &relFake{}
	roles := &roleFake{}
	svc, err := NewService(Repositories{Relationships: rel, Roles: roles}, Config{Model: validModel()})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := svc.Check(context.Background(), CheckRequest{
		Subject: Subject{Type: "user", ID: "u1"}, Permission: "delete", Resource: Resource{Type: "post", ID: "p1"},
	}); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if rel.checkCalls == 0 {
		t.Fatalf("Check did not reach the relationship store")
	}
	if err := svc.AssignRole(context.Background(), Subject{Type: "user", ID: "u1"}, "editor", "", ""); err != nil {
		t.Fatalf("AssignRole: %v", err)
	}
	if roles.assignCalls == 0 {
		t.Fatalf("AssignRole did not reach the role store")
	}
}

func TestRegister(t *testing.T) {
	svc, err := NewService(Repositories{Roles: &roleFake{}}, Config{})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	// With a logger.
	if err := svc.Register(feature.Mount{Logger: slog.Default()}); err != nil {
		t.Fatalf("Register with logger: %v", err)
	}
	// Zero-value Mount (nil logger) is tolerated.
	if err := svc.Register(feature.Mount{}); err != nil {
		t.Fatalf("Register with zero Mount: %v", err)
	}
}
