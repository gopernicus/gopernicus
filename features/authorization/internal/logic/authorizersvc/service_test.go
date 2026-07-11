package authorizersvc

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/features/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
)

// fakeStore is a minimal in-package relationship.Storer for engine unit tests:
// a slice of tuples with direct-match checks. Real group expansion and keyset
// listing are the memstore's job (task-7) and storetest's (task-8); this fake
// exercises engine logic only.
type fakeStore struct {
	tuples    []relationship.CreateRelationship
	lastBatch []relationship.CreateRelationship // captured for mint assertions
}

func (f *fakeStore) match(resourceType, resourceID, relation, subjectType, subjectID string) bool {
	for _, t := range f.tuples {
		if t.ResourceType == resourceType && t.ResourceID == resourceID && t.Relation == relation &&
			t.SubjectType == subjectType && t.SubjectID == subjectID {
			return true
		}
	}
	return false
}

func (f *fakeStore) CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error) {
	return f.match(resourceType, resourceID, relation, subjectType, subjectID), nil
}

func (f *fakeStore) CheckRelationExists(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error) {
	return f.match(resourceType, resourceID, relation, subjectType, subjectID), nil
}

func (f *fakeStore) GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]relationship.RelationTarget, error) {
	var out []relationship.RelationTarget
	for _, t := range f.tuples {
		if t.ResourceType == resourceType && t.ResourceID == resourceID && t.Relation == relation {
			out = append(out, relationship.RelationTarget{SubjectType: t.SubjectType, SubjectID: t.SubjectID, SubjectRelation: t.SubjectRelation})
		}
	}
	return out, nil
}

func (f *fakeStore) CheckBatchDirect(ctx context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string) (map[string]bool, error) {
	out := make(map[string]bool, len(resourceIDs))
	for _, id := range resourceIDs {
		out[id] = f.match(resourceType, id, relation, subjectType, subjectID)
	}
	return out, nil
}

func (f *fakeStore) CreateRelationships(ctx context.Context, relationships []relationship.CreateRelationship) error {
	f.lastBatch = relationships
	f.tuples = append(f.tuples, relationships...)
	return nil
}

func (f *fakeStore) DeleteResourceRelationships(ctx context.Context, resourceType, resourceID string) error {
	f.tuples = filter(f.tuples, func(t relationship.CreateRelationship) bool {
		return !(t.ResourceType == resourceType && t.ResourceID == resourceID)
	})
	return nil
}

func (f *fakeStore) DeleteRelationship(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) error {
	f.tuples = filter(f.tuples, func(t relationship.CreateRelationship) bool {
		return !(t.ResourceType == resourceType && t.ResourceID == resourceID && t.Relation == relation &&
			t.SubjectType == subjectType && t.SubjectID == subjectID)
	})
	return nil
}

func (f *fakeStore) DeleteByResourceAndSubject(ctx context.Context, resourceType, resourceID, subjectType, subjectID string) error {
	f.tuples = filter(f.tuples, func(t relationship.CreateRelationship) bool {
		return !(t.ResourceType == resourceType && t.ResourceID == resourceID &&
			t.SubjectType == subjectType && t.SubjectID == subjectID)
	})
	return nil
}

func (f *fakeStore) CountByResourceAndRelation(ctx context.Context, resourceType, resourceID, relation string) (int, error) {
	n := 0
	for _, t := range f.tuples {
		if t.ResourceType == resourceType && t.ResourceID == resourceID && t.Relation == relation {
			n++
		}
	}
	return n, nil
}

func (f *fakeStore) ListRelationshipsBySubject(ctx context.Context, subjectType, subjectID string, filter relationship.SubjectRelationshipFilter, req crud.ListRequest) (crud.Page[relationship.SubjectRelationship], error) {
	return crud.Page[relationship.SubjectRelationship]{}, nil
}

func (f *fakeStore) ListRelationshipsByResource(ctx context.Context, resourceType, resourceID string, filter relationship.ResourceRelationshipFilter, req crud.ListRequest) (crud.Page[relationship.ResourceRelationship], error) {
	return crud.Page[relationship.ResourceRelationship]{}, nil
}

func (f *fakeStore) LookupResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType, subjectID string) ([]string, error) {
	var out []string
	for _, t := range f.tuples {
		if t.ResourceType != resourceType || t.SubjectType != subjectType || t.SubjectID != subjectID {
			continue
		}
		for _, r := range relations {
			if t.Relation == r {
				out = append(out, t.ResourceID)
			}
		}
	}
	return out, nil
}

func (f *fakeStore) LookupResourceIDsByRelationTarget(ctx context.Context, resourceType, relation, targetType string, targetIDs []string) ([]string, error) {
	set := make(map[string]bool, len(targetIDs))
	for _, id := range targetIDs {
		set[id] = true
	}
	var out []string
	for _, t := range f.tuples {
		if t.ResourceType == resourceType && t.Relation == relation && t.SubjectType == targetType && set[t.SubjectID] {
			out = append(out, t.ResourceID)
		}
	}
	return out, nil
}

func (f *fakeStore) LookupDescendantResourceIDs(ctx context.Context, resourceType, relation, subjectType string, rootIDs []string) ([]string, error) {
	return nil, nil
}

func filter(in []relationship.CreateRelationship, keep func(relationship.CreateRelationship) bool) []relationship.CreateRelationship {
	out := in[:0:0]
	for _, t := range in {
		if keep(t) {
			out = append(out, t)
		}
	}
	return out
}

// testSchema: org.manage=Direct(admin); post.view=AnyOf(Direct(owner),
// Through(org, manage)); post.delete=Direct(owner).
func testSchema() Schema {
	return NewSchema([]ResourceSchema{
		{Name: "org", Def: ResourceTypeDef{
			Relations:   map[string]RelationDef{"admin": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
			Permissions: map[string]PermissionRule{"manage": AnyOf(Direct("admin"))},
		}},
		{Name: "post", Def: ResourceTypeDef{
			Relations: map[string]RelationDef{
				"owner": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
				"org":   {AllowedSubjects: []SubjectTypeRef{{Type: "org"}}},
			},
			Permissions: map[string]PermissionRule{
				"view":   AnyOf(Direct("owner"), Through("org", "manage")),
				"delete": AnyOf(Direct("owner")),
			},
		}},
	})
}

func newTestService(t *testing.T, store relationship.Storer, ids cryptids.IDGenerator) *Service {
	t.Helper()
	svc, err := NewService(store, testSchema(), Config{IDs: ids})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

func TestNewServiceRejectsInvalidSchema(t *testing.T) {
	bad := NewSchema([]ResourceSchema{{
		Name: "org",
		Def: ResourceTypeDef{
			Relations:   map[string]RelationDef{"admin": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
			Permissions: map[string]PermissionRule{"manage": AnyOf(Direct("owner"))}, // owner undefined
		},
	}})
	_, err := NewService(&fakeStore{}, bad, Config{})
	if !errors.Is(err, ErrInvalidSchema) {
		t.Fatalf("want ErrInvalidSchema, got %v", err)
	}
}

func TestCheckDirectRelation(t *testing.T) {
	store := &fakeStore{}
	svc := newTestService(t, store, cryptids.IDGenerator{})
	store.tuples = append(store.tuples, relationship.CreateRelationship{
		ResourceType: "post", ResourceID: "p1", Relation: "owner", SubjectType: "user", SubjectID: "u1",
	})

	res, err := svc.Check(context.Background(), CheckRequest{
		Subject: Subject{Type: "user", ID: "u1"}, Permission: "delete", Resource: Resource{Type: "post", ID: "p1"},
	})
	if err != nil || !res.Allowed {
		t.Fatalf("owner should have delete: allowed=%v reason=%q err=%v", res.Allowed, res.Reason, err)
	}

	deny, _ := svc.Check(context.Background(), CheckRequest{
		Subject: Subject{Type: "user", ID: "u2"}, Permission: "delete", Resource: Resource{Type: "post", ID: "p1"},
	})
	if deny.Allowed {
		t.Fatalf("non-owner must be denied delete")
	}
}

// TestCheckNoImplicitSelfAccess proves the engine grants NO self-access: a
// subject reading its own record with no tuple and no schema rule is DENIED.
// Self-access is host composition, not engine behavior.
func TestCheckNoImplicitSelfAccess(t *testing.T) {
	svc := newTestService(t, &fakeStore{}, cryptids.IDGenerator{})
	res, err := svc.Check(context.Background(), CheckRequest{
		Subject: Subject{Type: "user", ID: "u1"}, Permission: "read", Resource: Resource{Type: "user", ID: "u1"},
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res.Allowed {
		t.Fatalf("engine must not grant implicit self-access: %+v", res)
	}
}

// TestCheckPlatformAdminIsNotMagic proves a platform-admin tuple holder is
// DENIED on an unrelated resource type: the tuple no longer bypasses the
// schema. Platform-admin is host composition — the host runs an `admin`
// permission Check in its own closure before delegating here.
func TestCheckPlatformAdminIsNotMagic(t *testing.T) {
	for _, subjType := range []string{"user", "service_account"} {
		store := &fakeStore{}
		svc := newTestService(t, store, cryptids.IDGenerator{})
		store.tuples = append(store.tuples, relationship.CreateRelationship{
			ResourceType: "platform", ResourceID: "main", Relation: "admin", SubjectType: subjType, SubjectID: "admin1",
		})
		res, err := svc.Check(context.Background(), CheckRequest{
			Subject: Subject{Type: subjType, ID: "admin1"}, Permission: "delete", Resource: Resource{Type: "post", ID: "pX"},
		})
		if err != nil {
			t.Fatalf("%s Check: %v", subjType, err)
		}
		if res.Allowed {
			t.Fatalf("%s platform admin must NOT bypass the schema: %+v", subjType, res)
		}
	}
}

func TestCheckThroughTraversal(t *testing.T) {
	store := &fakeStore{}
	svc := newTestService(t, store, cryptids.IDGenerator{})
	// u1 is admin of org o1; post p1 belongs to org o1 → u1 can view p1.
	store.tuples = append(store.tuples,
		relationship.CreateRelationship{ResourceType: "org", ResourceID: "o1", Relation: "admin", SubjectType: "user", SubjectID: "u1"},
		relationship.CreateRelationship{ResourceType: "post", ResourceID: "p1", Relation: "org", SubjectType: "org", SubjectID: "o1"},
	)
	res, err := svc.Check(context.Background(), CheckRequest{
		Subject: Subject{Type: "user", ID: "u1"}, Permission: "view", Resource: Resource{Type: "post", ID: "p1"},
	})
	if err != nil || !res.Allowed {
		t.Fatalf("through traversal should allow view: %+v err=%v", res, err)
	}
}

func TestCheckBatchAndFilterAuthorized(t *testing.T) {
	store := &fakeStore{}
	svc := newTestService(t, store, cryptids.IDGenerator{})
	store.tuples = append(store.tuples,
		relationship.CreateRelationship{ResourceType: "post", ResourceID: "p1", Relation: "owner", SubjectType: "user", SubjectID: "u1"},
		relationship.CreateRelationship{ResourceType: "post", ResourceID: "p3", Relation: "owner", SubjectType: "user", SubjectID: "u1"},
	)
	got, err := svc.FilterAuthorized(context.Background(), Subject{Type: "user", ID: "u1"}, "delete", "post", []string{"p1", "p2", "p3"})
	if err != nil {
		t.Fatalf("FilterAuthorized: %v", err)
	}
	if len(got) != 2 || got[0] != "p1" || got[1] != "p3" {
		t.Fatalf("want [p1 p3], got %v", got)
	}
}

func TestCreateRelationshipsMintsIDs(t *testing.T) {
	store := &fakeStore{}
	svc := newTestService(t, store, cryptids.IDGenerator{}) // default nanoid
	err := svc.CreateRelationships(context.Background(), []relationship.CreateRelationship{
		{ResourceType: "post", ResourceID: "p1", Relation: "owner", SubjectType: "user", SubjectID: "u1"},
		{ResourceType: "post", ResourceID: "p2", Relation: "owner", SubjectType: "user", SubjectID: "u1"},
	})
	if err != nil {
		t.Fatalf("CreateRelationships: %v", err)
	}
	for i, tup := range store.lastBatch {
		if tup.RelationshipID == "" {
			t.Fatalf("tuple %d got empty id under nanoid generator", i)
		}
	}
}

func TestCreateRelationshipsDatabaseGeneratorLeavesEmptyIDs(t *testing.T) {
	store := &fakeStore{}
	svc := newTestService(t, store, cryptids.NewGenerator(cryptids.Database))
	err := svc.CreateRelationships(context.Background(), []relationship.CreateRelationship{
		{ResourceType: "post", ResourceID: "p1", Relation: "owner", SubjectType: "user", SubjectID: "u1"},
	})
	if err != nil {
		t.Fatalf("CreateRelationships: %v", err)
	}
	if store.lastBatch[0].RelationshipID != "" {
		t.Fatalf("Database generator must leave id empty, got %q", store.lastBatch[0].RelationshipID)
	}
}

func TestCreateRelationshipsValidatesAgainstSchema(t *testing.T) {
	store := &fakeStore{}
	svc := newTestService(t, store, cryptids.IDGenerator{})
	err := svc.CreateRelationships(context.Background(), []relationship.CreateRelationship{
		{ResourceType: "post", ResourceID: "p1", Relation: "bogus", SubjectType: "user", SubjectID: "u1"},
	})
	if !errors.Is(err, ErrInvalidRelation) {
		t.Fatalf("want ErrInvalidRelation, got %v", err)
	}
	if len(store.tuples) != 0 {
		t.Fatalf("invalid batch must not persist")
	}
}

// CreateRelationships must not mutate the caller's slice (ids are stamped on a copy).
func TestCreateRelationshipsDoesNotMutateInput(t *testing.T) {
	store := &fakeStore{}
	svc := newTestService(t, store, cryptids.IDGenerator{})
	in := []relationship.CreateRelationship{
		{ResourceType: "post", ResourceID: "p1", Relation: "owner", SubjectType: "user", SubjectID: "u1"},
	}
	if err := svc.CreateRelationships(context.Background(), in); err != nil {
		t.Fatalf("CreateRelationships: %v", err)
	}
	if in[0].RelationshipID != "" {
		t.Fatalf("caller slice was mutated: id=%q", in[0].RelationshipID)
	}
}
