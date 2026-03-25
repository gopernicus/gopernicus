package authorization

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/fop"
)

// =============================================================================
// Mock Storer
// =============================================================================

// mockStorer is an in-package map-based mock that satisfies Storer.
type mockStorer struct {
	// relations stores tuples as "resourceType:resourceID#relation@subjectType:subjectID" -> true
	relations map[string]bool

	// targets stores "resourceType:resourceID#relation" -> []RelationTarget
	targets map[string][]RelationTarget

	// counts stores "resourceType:resourceID#relation" -> count
	counts map[string]int

	// created tracks CreateRelationships calls
	created []CreateRelationship

	// deleted tracks DeleteRelationship calls
	deleted []string

	// deletedByResourceAndSubject tracks DeleteByResourceAndSubject calls
	deletedByResourceAndSubject []string

	// err is returned from all methods if set
	err error
}

func newMockStorer() *mockStorer {
	return &mockStorer{
		relations: make(map[string]bool),
		targets:   make(map[string][]RelationTarget),
		counts:    make(map[string]int),
	}
}

func (m *mockStorer) addRelation(resourceType, resourceID, relation, subjectType, subjectID string) {
	key := fmt.Sprintf("%s:%s#%s@%s:%s", resourceType, resourceID, relation, subjectType, subjectID)
	m.relations[key] = true
}

func (m *mockStorer) addTargets(resourceType, resourceID, relation string, targets []RelationTarget) {
	key := fmt.Sprintf("%s:%s#%s", resourceType, resourceID, relation)
	m.targets[key] = targets
}

func (m *mockStorer) setCount(resourceType, resourceID, relation string, count int) {
	key := fmt.Sprintf("%s:%s#%s", resourceType, resourceID, relation)
	m.counts[key] = count
}

func (m *mockStorer) CheckRelationWithGroupExpansion(_ context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error) {
	if m.err != nil {
		return false, m.err
	}
	key := fmt.Sprintf("%s:%s#%s@%s:%s", resourceType, resourceID, relation, subjectType, subjectID)
	return m.relations[key], nil
}

func (m *mockStorer) GetRelationTargets(_ context.Context, resourceType, resourceID, relation string) ([]RelationTarget, error) {
	if m.err != nil {
		return nil, m.err
	}
	key := fmt.Sprintf("%s:%s#%s", resourceType, resourceID, relation)
	return m.targets[key], nil
}

func (m *mockStorer) CheckRelationExists(_ context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error) {
	if m.err != nil {
		return false, m.err
	}
	key := fmt.Sprintf("%s:%s#%s@%s:%s", resourceType, resourceID, relation, subjectType, subjectID)
	return m.relations[key], nil
}

func (m *mockStorer) CheckBatchDirect(_ context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string) (map[string]bool, error) {
	if m.err != nil {
		return nil, m.err
	}
	result := make(map[string]bool)
	for _, id := range resourceIDs {
		key := fmt.Sprintf("%s:%s#%s@%s:%s", resourceType, id, relation, subjectType, subjectID)
		result[id] = m.relations[key]
	}
	return result, nil
}

func (m *mockStorer) CreateRelationships(_ context.Context, relationships []CreateRelationship) error {
	if m.err != nil {
		return m.err
	}
	m.created = append(m.created, relationships...)
	return nil
}

func (m *mockStorer) DeleteResourceRelationships(_ context.Context, _, _ string) error {
	return m.err
}

func (m *mockStorer) DeleteRelationship(_ context.Context, resourceType, resourceID, relation, subjectType, subjectID string) error {
	if m.err != nil {
		return m.err
	}
	key := fmt.Sprintf("%s:%s#%s@%s:%s", resourceType, resourceID, relation, subjectType, subjectID)
	m.deleted = append(m.deleted, key)
	return nil
}

func (m *mockStorer) DeleteByResourceAndSubject(_ context.Context, resourceType, resourceID, subjectType, subjectID string) error {
	if m.err != nil {
		return m.err
	}
	key := fmt.Sprintf("%s:%s@%s:%s", resourceType, resourceID, subjectType, subjectID)
	m.deletedByResourceAndSubject = append(m.deletedByResourceAndSubject, key)
	return nil
}

func (m *mockStorer) CountByResourceAndRelation(_ context.Context, resourceType, resourceID, relation string) (int, error) {
	if m.err != nil {
		return 0, m.err
	}
	key := fmt.Sprintf("%s:%s#%s", resourceType, resourceID, relation)
	return m.counts[key], nil
}

func (m *mockStorer) ListRelationshipsBySubject(_ context.Context, _, _ string, _ SubjectRelationshipFilter, _ fop.Order, _ fop.PageStringCursor) ([]SubjectRelationship, fop.Pagination, error) {
	return nil, fop.Pagination{}, m.err
}

func (m *mockStorer) ListRelationshipsByResource(_ context.Context, _, _ string, _ ResourceRelationshipFilter, _ fop.Order, _ fop.PageStringCursor) ([]ResourceRelationship, fop.Pagination, error) {
	return nil, fop.Pagination{}, m.err
}

func (m *mockStorer) LookupResourceIDs(_ context.Context, resourceType string, relations []string, subjectType, subjectID string) ([]string, error) {
	if m.err != nil {
		return nil, m.err
	}
	var ids []string
	for key := range m.relations {
		// Parse "resourceType:resourceID#relation@subjectType:subjectID"
		for _, rel := range relations {
			prefix := resourceType + ":"
			suffix := fmt.Sprintf("#%s@%s:%s", rel, subjectType, subjectID)
			if strings.HasPrefix(key, prefix) && strings.HasSuffix(key, suffix) {
				id := key[len(prefix) : len(key)-len(suffix)]
				ids = append(ids, id)
			}
		}
	}
	return ids, nil
}

func (m *mockStorer) LookupResourceIDsByRelationTarget(_ context.Context, resourceType, relation, targetType string, targetIDs []string) ([]string, error) {
	if m.err != nil {
		return nil, m.err
	}
	targetSet := make(map[string]bool, len(targetIDs))
	for _, id := range targetIDs {
		targetSet[id] = true
	}
	var ids []string
	for key := range m.relations {
		// Parse "resourceType:resourceID#relation@targetType:targetID"
		prefix := resourceType + ":"
		for targetID := range targetSet {
			suffix := fmt.Sprintf("#%s@%s:%s", relation, targetType, targetID)
			if strings.HasPrefix(key, prefix) && strings.HasSuffix(key, suffix) {
				id := key[len(prefix) : len(key)-len(suffix)]
				ids = append(ids, id)
			}
		}
	}
	return ids, nil
}

// =============================================================================
// Test Helpers
// =============================================================================

func testSchema() Schema {
	return NewSchema([]ResourceSchema{
		{Name: "platform", Def: ResourceTypeDef{
			Relations: map[string]RelationDef{
				"admin": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}, {Type: "service_account"}}},
			},
		}},
		{Name: "tenant", Def: ResourceTypeDef{
			Relations: map[string]RelationDef{
				"owner":  {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
				"admin":  {AllowedSubjects: []SubjectTypeRef{{Type: "user"}, {Type: "service_account"}}},
				"member": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}, {Type: "service_account"}, {Type: "group", Relation: "member"}}},
			},
			Permissions: map[string]PermissionRule{
				"manage": AnyOf(Direct("owner"), Direct("admin")),
				"read":   AnyOf(Direct("owner"), Direct("admin"), Direct("member")),
				"delete": AnyOf(Direct("owner")),
			},
		}},
		{Name: "project", Def: ResourceTypeDef{
			Relations: map[string]RelationDef{
				"owner":  {AllowedSubjects: []SubjectTypeRef{{Type: "user"}, {Type: "service_account"}}},
				"editor": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}, {Type: "service_account"}}},
				"viewer": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}, {Type: "service_account"}}},
				"tenant": {AllowedSubjects: []SubjectTypeRef{{Type: "tenant"}}},
			},
			Permissions: map[string]PermissionRule{
				"edit": AnyOf(Direct("owner"), Direct("editor"), Through("tenant", "manage")),
				"read": AnyOf(Direct("owner"), Direct("editor"), Direct("viewer"), Through("tenant", "read")),
			},
		}},
		{Name: "user", Def: ResourceTypeDef{
			Relations: map[string]RelationDef{
				"owner":  {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
				"editor": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}, {Type: "service_account"}}},
				"viewer": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}, {Type: "service_account"}}},
			},
			Permissions: map[string]PermissionRule{
				"read":   AnyOf(Direct("viewer")),
				"update": AnyOf(Direct("editor")),
				"delete": AnyOf(Direct("owner")),
			},
		}},
		{Name: "service_account", Def: ResourceTypeDef{
			Relations: map[string]RelationDef{
				"owner":  {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
				"editor": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}, {Type: "service_account"}}},
				"viewer": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}, {Type: "service_account"}}},
			},
			Permissions: map[string]PermissionRule{
				"read":   AnyOf(Direct("viewer")),
				"update": AnyOf(Direct("editor")),
				"delete": AnyOf(Direct("owner")),
			},
		}},
	})
}

func testAuthorizer(store *mockStorer) *Authorizer {
	return NewAuthorizer(store, testSchema(), Config{MaxTraversalDepth: 10})
}

// =============================================================================
// Check Tests
// =============================================================================

func TestCheck_PlatformAdminBypass(t *testing.T) {
	store := newMockStorer()
	store.addRelation("platform", "main", "admin", "user", "admin-1")
	authz := testAuthorizer(store)

	result, err := authz.Check(context.Background(), CheckRequest{
		Subject:    Subject{Type: "user", ID: "admin-1"},
		Permission: "delete",
		Resource:   Resource{Type: "tenant", ID: "tenant-1"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Allowed {
		t.Fatal("expected platform admin to be allowed")
	}
	if result.Reason != "platform:admin" {
		t.Errorf("expected reason 'platform:admin', got %q", result.Reason)
	}
}

func TestCheck_PlatformAdminBypass_ServiceAccount(t *testing.T) {
	store := newMockStorer()
	store.addRelation("platform", "main", "admin", "service_account", "sa-1")
	authz := testAuthorizer(store)

	result, err := authz.Check(context.Background(), CheckRequest{
		Subject:    Subject{Type: "service_account", ID: "sa-1"},
		Permission: "delete",
		Resource:   Resource{Type: "tenant", ID: "tenant-1"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Allowed {
		t.Fatal("expected service_account platform admin to be allowed")
	}
	if result.Reason != "platform:admin" {
		t.Errorf("expected reason 'platform:admin', got %q", result.Reason)
	}
}

func TestCheck_SelfAccess_User(t *testing.T) {
	store := newMockStorer()
	authz := testAuthorizer(store)

	for _, perm := range []string{"read", "update", "delete"} {
		t.Run(perm, func(t *testing.T) {
			result, err := authz.Check(context.Background(), CheckRequest{
				Subject:    Subject{Type: "user", ID: "user-1"},
				Permission: perm,
				Resource:   Resource{Type: "user", ID: "user-1"},
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !result.Allowed {
				t.Fatalf("expected self-access to be allowed for %s", perm)
			}
			if result.Reason != "self" {
				t.Errorf("expected reason 'self', got %q", result.Reason)
			}
		})
	}
}

func TestCheck_SelfAccess_ServiceAccount(t *testing.T) {
	store := newMockStorer()
	authz := testAuthorizer(store)

	for _, perm := range []string{"read", "update", "delete"} {
		t.Run(perm, func(t *testing.T) {
			result, err := authz.Check(context.Background(), CheckRequest{
				Subject:    Subject{Type: "service_account", ID: "sa-1"},
				Permission: perm,
				Resource:   Resource{Type: "service_account", ID: "sa-1"},
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !result.Allowed {
				t.Fatalf("expected service_account self-access to be allowed for %s", perm)
			}
			if result.Reason != "self" {
				t.Errorf("expected reason 'self', got %q", result.Reason)
			}
		})
	}
}

func TestCheck_SelfAccess_DeniedForCreate(t *testing.T) {
	store := newMockStorer()
	authz := testAuthorizer(store)

	result, err := authz.Check(context.Background(), CheckRequest{
		Subject:    Subject{Type: "user", ID: "user-1"},
		Permission: "create",
		Resource:   Resource{Type: "user", ID: "user-1"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Allowed {
		t.Fatal("expected create to be denied for self-access")
	}
}

func TestCheck_SelfAccess_DifferentType(t *testing.T) {
	store := newMockStorer()
	authz := testAuthorizer(store)

	// user subject accessing a tenant resource with same ID — should NOT trigger self-access
	result, err := authz.Check(context.Background(), CheckRequest{
		Subject:    Subject{Type: "user", ID: "tenant-1"},
		Permission: "read",
		Resource:   Resource{Type: "tenant", ID: "tenant-1"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Allowed {
		t.Fatal("expected denial when subject type != resource type for self-access")
	}
}

func TestCheck_SelfAccess_ServiceAccountAccessingUserResource(t *testing.T) {
	store := newMockStorer()
	authz := testAuthorizer(store)

	// service_account accessing user resource — types don't match, no self-access
	result, err := authz.Check(context.Background(), CheckRequest{
		Subject:    Subject{Type: "service_account", ID: "user-1"},
		Permission: "read",
		Resource:   Resource{Type: "user", ID: "user-1"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Allowed {
		t.Fatal("expected denial: service_account cannot self-access user resources")
	}
}

func TestCheck_DirectRelation(t *testing.T) {
	store := newMockStorer()
	store.addRelation("tenant", "tenant-1", "member", "user", "user-1")
	authz := testAuthorizer(store)

	result, err := authz.Check(context.Background(), CheckRequest{
		Subject:    Subject{Type: "user", ID: "user-1"},
		Permission: "read",
		Resource:   Resource{Type: "tenant", ID: "tenant-1"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Allowed {
		t.Fatal("expected direct member to have read")
	}
	if result.Reason != "direct:member" {
		t.Errorf("expected reason 'direct:member', got %q", result.Reason)
	}
}

func TestCheck_DirectRelation_ServiceAccount(t *testing.T) {
	store := newMockStorer()
	store.addRelation("tenant", "tenant-1", "admin", "service_account", "sa-1")
	authz := testAuthorizer(store)

	result, err := authz.Check(context.Background(), CheckRequest{
		Subject:    Subject{Type: "service_account", ID: "sa-1"},
		Permission: "manage",
		Resource:   Resource{Type: "tenant", ID: "tenant-1"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Allowed {
		t.Fatal("expected service_account admin to have manage")
	}
	if result.Reason != "direct:admin" {
		t.Errorf("expected reason 'direct:admin', got %q", result.Reason)
	}
}

func TestCheck_DirectRelation_Denied(t *testing.T) {
	store := newMockStorer()
	store.addRelation("tenant", "tenant-1", "member", "user", "user-1")
	authz := testAuthorizer(store)

	result, err := authz.Check(context.Background(), CheckRequest{
		Subject:    Subject{Type: "user", ID: "user-1"},
		Permission: "delete",
		Resource:   Resource{Type: "tenant", ID: "tenant-1"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Allowed {
		t.Fatal("expected member to be denied delete (requires owner)")
	}
}

func TestCheck_ThroughRelation(t *testing.T) {
	store := newMockStorer()
	store.addTargets("project", "project-1", "tenant", []RelationTarget{
		{SubjectType: "tenant", SubjectID: "tenant-1"},
	})
	store.addRelation("tenant", "tenant-1", "admin", "user", "user-1")
	authz := testAuthorizer(store)

	result, err := authz.Check(context.Background(), CheckRequest{
		Subject:    Subject{Type: "user", ID: "user-1"},
		Permission: "edit",
		Resource:   Resource{Type: "project", ID: "project-1"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Allowed {
		t.Fatal("expected through-relation to grant access")
	}
	if !strings.Contains(result.Reason, "through:tenant") {
		t.Errorf("expected reason to contain 'through:tenant', got %q", result.Reason)
	}
}

func TestCheck_ThroughRelation_ServiceAccount(t *testing.T) {
	store := newMockStorer()
	store.addTargets("project", "project-1", "tenant", []RelationTarget{
		{SubjectType: "tenant", SubjectID: "tenant-1"},
	})
	store.addRelation("tenant", "tenant-1", "admin", "service_account", "sa-1")
	authz := testAuthorizer(store)

	result, err := authz.Check(context.Background(), CheckRequest{
		Subject:    Subject{Type: "service_account", ID: "sa-1"},
		Permission: "edit",
		Resource:   Resource{Type: "project", ID: "project-1"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Allowed {
		t.Fatal("expected service_account to get edit via through-relation")
	}
	if !strings.Contains(result.Reason, "through:tenant") {
		t.Errorf("expected reason to contain 'through:tenant', got %q", result.Reason)
	}
}

func TestCheck_NoRulesDefined(t *testing.T) {
	store := newMockStorer()
	authz := testAuthorizer(store)

	result, err := authz.Check(context.Background(), CheckRequest{
		Subject:    Subject{Type: "user", ID: "user-1"},
		Permission: "nonexistent",
		Resource:   Resource{Type: "tenant", ID: "tenant-1"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Allowed {
		t.Fatal("expected denied for undefined permission")
	}
	if result.Reason != "no rules defined" {
		t.Errorf("expected reason 'no rules defined', got %q", result.Reason)
	}
}

func TestCheck_CycleDetection(t *testing.T) {
	schema := NewSchema([]ResourceSchema{
		{Name: "a", Def: ResourceTypeDef{
			Relations: map[string]RelationDef{
				"parent": {AllowedSubjects: []SubjectTypeRef{{Type: "b"}}},
			},
			Permissions: map[string]PermissionRule{
				"read": AnyOf(Through("parent", "read")),
			},
		}},
		{Name: "b", Def: ResourceTypeDef{
			Relations: map[string]RelationDef{
				"parent": {AllowedSubjects: []SubjectTypeRef{{Type: "a"}}},
			},
			Permissions: map[string]PermissionRule{
				"read": AnyOf(Through("parent", "read")),
			},
		}},
	})

	store := newMockStorer()
	store.addTargets("a", "a-1", "parent", []RelationTarget{{SubjectType: "b", SubjectID: "b-1"}})
	store.addTargets("b", "b-1", "parent", []RelationTarget{{SubjectType: "a", SubjectID: "a-1"}})

	authz := NewAuthorizer(store, schema, Config{MaxTraversalDepth: 10})

	result, err := authz.Check(context.Background(), CheckRequest{
		Subject:    Subject{Type: "user", ID: "user-1"},
		Permission: "read",
		Resource:   Resource{Type: "a", ID: "a-1"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Allowed {
		t.Fatal("expected denied for circular through-relations")
	}
}

func TestCheck_DepthLimit(t *testing.T) {
	schema := NewSchema([]ResourceSchema{
		{Name: "node", Def: ResourceTypeDef{
			Relations: map[string]RelationDef{
				"parent": {AllowedSubjects: []SubjectTypeRef{{Type: "node"}}},
			},
			Permissions: map[string]PermissionRule{
				"read": AnyOf(Through("parent", "read")),
			},
		}},
	})

	store := newMockStorer()
	for i := 0; i < 15; i++ {
		store.addTargets("node", fmt.Sprintf("node-%d", i), "parent", []RelationTarget{
			{SubjectType: "node", SubjectID: fmt.Sprintf("node-%d", i+1)},
		})
	}

	authz := NewAuthorizer(store, schema, Config{MaxTraversalDepth: 5})

	result, err := authz.Check(context.Background(), CheckRequest{
		Subject:    Subject{Type: "user", ID: "user-1"},
		Permission: "read",
		Resource:   Resource{Type: "node", ID: "node-0"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Allowed {
		t.Fatal("expected denied when depth limit exceeded")
	}
	// The depth limit prevents infinite recursion. The final reason is "no matching rule"
	// because the depth-exceeded denial at the leaf propagates up as a non-match.
}

// =============================================================================
// CheckBatch Tests
// =============================================================================

func TestCheckBatch_Empty(t *testing.T) {
	store := newMockStorer()
	authz := testAuthorizer(store)

	results, err := authz.CheckBatch(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results != nil {
		t.Fatalf("expected nil results, got %v", results)
	}
}

func TestCheckBatch_OptimizedPath(t *testing.T) {
	store := newMockStorer()
	store.addRelation("post", "post-1", "viewer", "user", "user-1")
	store.addRelation("post", "post-3", "viewer", "user", "user-1")

	schema := NewSchema([]ResourceSchema{
		{Name: "post", Def: ResourceTypeDef{
			Relations: map[string]RelationDef{
				"viewer": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}, {Type: "service_account"}}},
			},
			Permissions: map[string]PermissionRule{
				"read": AnyOf(Direct("viewer")),
			},
		}},
	})
	authz := NewAuthorizer(store, schema, Config{MaxTraversalDepth: 10})

	reqs := []CheckRequest{
		{Subject: Subject{Type: "user", ID: "user-1"}, Permission: "read", Resource: Resource{Type: "post", ID: "post-1"}},
		{Subject: Subject{Type: "user", ID: "user-1"}, Permission: "read", Resource: Resource{Type: "post", ID: "post-2"}},
		{Subject: Subject{Type: "user", ID: "user-1"}, Permission: "read", Resource: Resource{Type: "post", ID: "post-3"}},
	}

	results, err := authz.CheckBatch(context.Background(), reqs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if !results[0].Allowed {
		t.Error("expected post-1 allowed")
	}
	if results[1].Allowed {
		t.Error("expected post-2 denied")
	}
	if !results[2].Allowed {
		t.Error("expected post-3 allowed")
	}
}

func TestCheckBatch_OptimizedPath_ServiceAccount(t *testing.T) {
	store := newMockStorer()
	store.addRelation("project", "proj-1", "editor", "service_account", "sa-1")
	store.addRelation("project", "proj-2", "editor", "service_account", "sa-1")

	authz := testAuthorizer(store)

	reqs := []CheckRequest{
		{Subject: Subject{Type: "service_account", ID: "sa-1"}, Permission: "read", Resource: Resource{Type: "project", ID: "proj-1"}},
		{Subject: Subject{Type: "service_account", ID: "sa-1"}, Permission: "read", Resource: Resource{Type: "project", ID: "proj-2"}},
		{Subject: Subject{Type: "service_account", ID: "sa-1"}, Permission: "read", Resource: Resource{Type: "project", ID: "proj-3"}},
	}

	results, err := authz.CheckBatch(context.Background(), reqs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if !results[0].Allowed {
		t.Error("expected proj-1 allowed for service_account")
	}
	if !results[1].Allowed {
		t.Error("expected proj-2 allowed for service_account")
	}
	if results[2].Allowed {
		t.Error("expected proj-3 denied for service_account")
	}
}

func TestCheckBatch_MixedSubjects_FallsBack(t *testing.T) {
	store := newMockStorer()
	store.addRelation("project", "proj-1", "viewer", "user", "user-1")
	store.addRelation("project", "proj-1", "editor", "service_account", "sa-1")

	authz := testAuthorizer(store)

	reqs := []CheckRequest{
		{Subject: Subject{Type: "user", ID: "user-1"}, Permission: "read", Resource: Resource{Type: "project", ID: "proj-1"}},
		{Subject: Subject{Type: "service_account", ID: "sa-1"}, Permission: "read", Resource: Resource{Type: "project", ID: "proj-1"}},
	}

	results, err := authz.CheckBatch(context.Background(), reqs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if !results[0].Allowed {
		t.Error("expected user allowed via viewer")
	}
	if !results[1].Allowed {
		t.Error("expected service_account allowed via editor")
	}
}

// =============================================================================
// FilterAuthorized Tests
// =============================================================================

func TestFilterAuthorized_Empty(t *testing.T) {
	store := newMockStorer()
	authz := testAuthorizer(store)

	allowed, err := authz.FilterAuthorized(context.Background(), Subject{Type: "user", ID: "user-1"}, "read", "post", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed != nil {
		t.Fatalf("expected nil, got %v", allowed)
	}
}

func TestFilterAuthorized_FiltersCorrectly(t *testing.T) {
	store := newMockStorer()
	store.addRelation("project", "proj-1", "viewer", "user", "user-1")
	store.addRelation("project", "proj-3", "viewer", "user", "user-1")

	authz := testAuthorizer(store)

	allowed, err := authz.FilterAuthorized(context.Background(), Subject{Type: "user", ID: "user-1"}, "read", "project", []string{"proj-1", "proj-2", "proj-3"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(allowed) != 2 || allowed[0] != "proj-1" || allowed[1] != "proj-3" {
		t.Errorf("expected [proj-1, proj-3], got %v", allowed)
	}
}

func TestFilterAuthorized_ServiceAccount(t *testing.T) {
	store := newMockStorer()
	store.addRelation("project", "proj-1", "editor", "service_account", "sa-1")
	store.addRelation("project", "proj-2", "editor", "service_account", "sa-1")

	authz := testAuthorizer(store)

	allowed, err := authz.FilterAuthorized(context.Background(), Subject{Type: "service_account", ID: "sa-1"}, "read", "project", []string{"proj-1", "proj-2", "proj-3"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(allowed) != 2 || allowed[0] != "proj-1" || allowed[1] != "proj-2" {
		t.Errorf("expected [proj-1, proj-2], got %v", allowed)
	}
}

// =============================================================================
// Membership Tests
// =============================================================================

func TestRemoveMember_LastOwnerGuard(t *testing.T) {
	store := newMockStorer()
	store.addRelation("tenant", "tenant-1", "owner", "user", "user-1")
	store.setCount("tenant", "tenant-1", "owner", 1)
	authz := testAuthorizer(store)

	err := authz.RemoveMember(context.Background(), "tenant", "tenant-1", "user", "user-1")
	if !errors.Is(err, ErrCannotRemoveLastOwner) {
		t.Fatalf("expected ErrCannotRemoveLastOwner, got %v", err)
	}
}

func TestRemoveMember_AllowedWhenMultipleOwners(t *testing.T) {
	store := newMockStorer()
	store.addRelation("tenant", "tenant-1", "owner", "user", "user-1")
	store.setCount("tenant", "tenant-1", "owner", 2)
	authz := testAuthorizer(store)

	err := authz.RemoveMember(context.Background(), "tenant", "tenant-1", "user", "user-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(store.deletedByResourceAndSubject) != 1 {
		t.Fatalf("expected 1 DeleteByResourceAndSubject call, got %d", len(store.deletedByResourceAndSubject))
	}
}

func TestRemoveMember_NonOwner(t *testing.T) {
	store := newMockStorer()
	authz := testAuthorizer(store)

	err := authz.RemoveMember(context.Background(), "tenant", "tenant-1", "user", "user-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(store.deletedByResourceAndSubject) != 1 {
		t.Fatalf("expected 1 DeleteByResourceAndSubject call, got %d", len(store.deletedByResourceAndSubject))
	}
}

func TestRemoveMember_ServiceAccount(t *testing.T) {
	store := newMockStorer()
	store.addRelation("tenant", "tenant-1", "admin", "service_account", "sa-1")
	authz := testAuthorizer(store)

	err := authz.RemoveMember(context.Background(), "tenant", "tenant-1", "service_account", "sa-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(store.deletedByResourceAndSubject) != 1 {
		t.Fatalf("expected 1 DeleteByResourceAndSubject call, got %d", len(store.deletedByResourceAndSubject))
	}
}

func TestChangeMemberRole_SelfChangeGuard(t *testing.T) {
	store := newMockStorer()
	authz := testAuthorizer(store)

	err := authz.ChangeMemberRole(context.Background(), "tenant", "tenant-1", "user", "user-1", "admin", "member", "user-1")
	if !errors.Is(err, ErrCannotChangeOwnRole) {
		t.Fatalf("expected ErrCannotChangeOwnRole, got %v", err)
	}
}

func TestChangeMemberRole_SelfChangeGuard_ServiceAccount(t *testing.T) {
	store := newMockStorer()
	authz := testAuthorizer(store)

	// Service account trying to change its own role
	err := authz.ChangeMemberRole(context.Background(), "tenant", "tenant-1", "service_account", "sa-1", "admin", "member", "sa-1")
	if !errors.Is(err, ErrCannotChangeOwnRole) {
		t.Fatalf("expected ErrCannotChangeOwnRole, got %v", err)
	}
}

func TestChangeMemberRole_LastOwnerGuard(t *testing.T) {
	store := newMockStorer()
	store.setCount("tenant", "tenant-1", "owner", 1)
	authz := testAuthorizer(store)

	err := authz.ChangeMemberRole(context.Background(), "tenant", "tenant-1", "user", "user-1", "owner", "member", "actor-1")
	if !errors.Is(err, ErrCannotChangeLastOwner) {
		t.Fatalf("expected ErrCannotChangeLastOwner, got %v", err)
	}
}

func TestChangeMemberRole_Success(t *testing.T) {
	store := newMockStorer()
	store.setCount("tenant", "tenant-1", "owner", 2)
	authz := testAuthorizer(store)

	err := authz.ChangeMemberRole(context.Background(), "tenant", "tenant-1", "user", "user-1", "owner", "admin", "actor-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(store.deleted) != 1 {
		t.Fatalf("expected 1 delete call, got %d", len(store.deleted))
	}
	if len(store.created) != 1 {
		t.Fatalf("expected 1 create call, got %d", len(store.created))
	}
	if store.created[0].Relation != "admin" {
		t.Errorf("expected new relation 'admin', got %q", store.created[0].Relation)
	}
}

func TestChangeMemberRole_ServiceAccountSuccess(t *testing.T) {
	store := newMockStorer()
	authz := testAuthorizer(store)

	// Actor (user) changes service_account from admin to member
	err := authz.ChangeMemberRole(context.Background(), "tenant", "tenant-1", "service_account", "sa-1", "admin", "member", "actor-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(store.created) != 1 {
		t.Fatalf("expected 1 create call, got %d", len(store.created))
	}
	if store.created[0].SubjectType != "service_account" {
		t.Errorf("expected subject type 'service_account', got %q", store.created[0].SubjectType)
	}
}

// =============================================================================
// Builder Tests
// =============================================================================

func TestNewSchema_MergesDuplicates(t *testing.T) {
	base := []ResourceSchema{
		{Name: "tenant", Def: ResourceTypeDef{
			Relations: map[string]RelationDef{
				"owner": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
			},
			Permissions: map[string]PermissionRule{
				"read": AnyOf(Direct("owner")),
			},
		}},
	}

	override := []ResourceSchema{
		{Name: "tenant", Def: ResourceTypeDef{
			Relations: map[string]RelationDef{
				"admin": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}, {Type: "service_account"}}},
			},
			Permissions: map[string]PermissionRule{
				"manage": AnyOf(Direct("admin")),
			},
		}},
	}

	schema := NewSchema(base, override)
	tenantDef := schema.ResourceTypes["tenant"]

	if _, ok := tenantDef.Relations["owner"]; !ok {
		t.Error("expected 'owner' relation from base")
	}
	if _, ok := tenantDef.Relations["admin"]; !ok {
		t.Error("expected 'admin' relation from override")
	}
	if len(tenantDef.Relations["admin"].AllowedSubjects) != 2 {
		t.Error("expected admin relation to allow both user and service_account")
	}
	if _, ok := tenantDef.Permissions["read"]; !ok {
		t.Error("expected 'read' permission from base")
	}
	if _, ok := tenantDef.Permissions["manage"]; !ok {
		t.Error("expected 'manage' permission from override")
	}
}

func TestMergeResourceType_RemovePermission(t *testing.T) {
	base := ResourceTypeDef{
		Relations: map[string]RelationDef{
			"owner": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}, {Type: "service_account"}}},
		},
		Permissions: map[string]PermissionRule{
			"read":   AnyOf(Direct("owner")),
			"delete": AnyOf(Direct("owner")),
		},
	}

	override := ResourceTypeDef{
		Permissions: map[string]PermissionRule{
			"delete": Remove(),
		},
	}

	merged := MergeResourceType(base, override)

	if _, ok := merged.Permissions["read"]; !ok {
		t.Error("expected 'read' to be preserved")
	}
	if _, ok := merged.Permissions["delete"]; ok {
		t.Error("expected 'delete' to be removed")
	}
}

// =============================================================================
// SchemaValidator Tests
// =============================================================================

func TestValidateSchema_Valid(t *testing.T) {
	schema := testSchema()
	if err := ValidateSchema(schema); err != nil {
		t.Fatalf("expected valid schema, got: %v", err)
	}
}

func TestValidateSchema_UndefinedThroughRelation(t *testing.T) {
	schema := NewSchema([]ResourceSchema{
		{Name: "post", Def: ResourceTypeDef{
			Permissions: map[string]PermissionRule{
				"read": AnyOf(Through("nonexistent", "read")),
			},
		}},
	})

	err := ValidateSchema(schema)
	if err == nil {
		t.Fatal("expected validation error")
	}
	var ve *SchemaValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected SchemaValidationError, got %T", err)
	}
	if len(ve.Errors) == 0 {
		t.Fatal("expected at least one error")
	}
}

func TestValidateSchema_UndefinedDirectRelation(t *testing.T) {
	schema := NewSchema([]ResourceSchema{
		{Name: "post", Def: ResourceTypeDef{
			Permissions: map[string]PermissionRule{
				"read": AnyOf(Direct("nonexistent")),
			},
		}},
	})

	err := ValidateSchema(schema)
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestValidateSchema_CircularThrough(t *testing.T) {
	schema := NewSchema([]ResourceSchema{
		{Name: "a", Def: ResourceTypeDef{
			Relations: map[string]RelationDef{
				"parent": {AllowedSubjects: []SubjectTypeRef{{Type: "b"}}},
			},
			Permissions: map[string]PermissionRule{
				"read": AnyOf(Through("parent", "read")),
			},
		}},
		{Name: "b", Def: ResourceTypeDef{
			Relations: map[string]RelationDef{
				"parent": {AllowedSubjects: []SubjectTypeRef{{Type: "a"}}},
			},
			Permissions: map[string]PermissionRule{
				"read": AnyOf(Through("parent", "read")),
			},
		}},
	})

	err := ValidateSchema(schema)
	if err == nil {
		t.Fatal("expected circular reference error")
	}
	var ve *SchemaValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected SchemaValidationError, got %T", err)
	}
	found := false
	for _, e := range ve.Errors {
		if strings.Contains(e, "circular") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected circular error, got: %v", ve.Errors)
	}
}

// =============================================================================
// Explain Tests
// =============================================================================

func TestCheckExplain_RecordsSteps(t *testing.T) {
	store := newMockStorer()
	store.addRelation("tenant", "tenant-1", "admin", "user", "user-1")
	authz := testAuthorizer(store)

	result, err := authz.CheckExplain(context.Background(), CheckRequest{
		Subject:    Subject{Type: "user", ID: "user-1"},
		Permission: "manage",
		Resource:   Resource{Type: "tenant", ID: "tenant-1"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Allowed {
		t.Fatal("expected allowed")
	}
	if len(result.Steps) == 0 {
		t.Fatal("expected traversal steps")
	}
	if result.Steps[0].Type != StepPlatformAdmin {
		t.Errorf("expected first step to be platform_admin, got %s", result.Steps[0].Type)
	}
}

func TestCheckExplain_ServiceAccount(t *testing.T) {
	store := newMockStorer()
	store.addRelation("project", "proj-1", "editor", "service_account", "sa-1")
	authz := testAuthorizer(store)

	result, err := authz.CheckExplain(context.Background(), CheckRequest{
		Subject:    Subject{Type: "service_account", ID: "sa-1"},
		Permission: "edit",
		Resource:   Resource{Type: "project", ID: "proj-1"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Allowed {
		t.Fatal("expected service_account to be allowed")
	}
	if !strings.Contains(result.Reason, "direct:editor") {
		t.Errorf("expected reason to contain 'direct:editor', got %q", result.Reason)
	}
}

func TestCheckExplain_FormatExplain(t *testing.T) {
	store := newMockStorer()
	store.addRelation("tenant", "tenant-1", "admin", "user", "user-1")
	authz := testAuthorizer(store)

	result, err := authz.CheckExplain(context.Background(), CheckRequest{
		Subject:    Subject{Type: "user", ID: "user-1"},
		Permission: "manage",
		Resource:   Resource{Type: "tenant", ID: "tenant-1"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.FormatExplain()
	if !strings.Contains(output, "Permission Check:") {
		t.Error("expected FormatExplain to contain 'Permission Check:'")
	}
	if !strings.Contains(output, "Traversal Path:") {
		t.Error("expected FormatExplain to contain 'Traversal Path:'")
	}
}

// =============================================================================
// Relationship Validation Tests
// =============================================================================

func TestValidateRelation_Valid(t *testing.T) {
	authz := testAuthorizer(newMockStorer())

	if err := authz.ValidateRelation("tenant", "member", "user"); err != nil {
		t.Fatalf("expected valid: %v", err)
	}
}

func TestValidateRelation_ServiceAccount(t *testing.T) {
	authz := testAuthorizer(newMockStorer())

	if err := authz.ValidateRelation("tenant", "admin", "service_account"); err != nil {
		t.Fatalf("expected service_account allowed as admin: %v", err)
	}
	if err := authz.ValidateRelation("tenant", "member", "service_account"); err != nil {
		t.Fatalf("expected service_account allowed as member: %v", err)
	}
}

func TestValidateRelation_UnknownResourceType(t *testing.T) {
	authz := testAuthorizer(newMockStorer())

	err := authz.ValidateRelation("nonexistent", "member", "user")
	if !errors.Is(err, ErrInvalidRelation) {
		t.Fatalf("expected ErrInvalidRelation, got: %v", err)
	}
}

func TestValidateRelation_UnknownRelation(t *testing.T) {
	authz := testAuthorizer(newMockStorer())

	err := authz.ValidateRelation("tenant", "nonexistent", "user")
	if !errors.Is(err, ErrInvalidRelation) {
		t.Fatalf("expected ErrInvalidRelation, got: %v", err)
	}
}

func TestValidateRelation_DisallowedSubjectType(t *testing.T) {
	authz := testAuthorizer(newMockStorer())

	err := authz.ValidateRelation("tenant", "owner", "apikey")
	if !errors.Is(err, ErrInvalidRelation) {
		t.Fatalf("expected ErrInvalidRelation, got: %v", err)
	}
}

func TestCreateRelationships_ValidatesSchema(t *testing.T) {
	authz := testAuthorizer(newMockStorer())

	err := authz.CreateRelationships(context.Background(), []CreateRelationship{
		{ResourceType: "tenant", ResourceID: "t-1", Relation: "owner", SubjectType: "apikey", SubjectID: "k-1"},
	})
	if !errors.Is(err, ErrInvalidRelation) {
		t.Fatalf("expected ErrInvalidRelation from schema validation, got: %v", err)
	}
}

func TestCreateRelationships_ServiceAccount(t *testing.T) {
	store := newMockStorer()
	authz := testAuthorizer(store)

	err := authz.CreateRelationships(context.Background(), []CreateRelationship{
		{ResourceType: "tenant", ResourceID: "t-1", Relation: "admin", SubjectType: "service_account", SubjectID: "sa-1"},
	})
	if err != nil {
		t.Fatalf("expected service_account as admin to be valid: %v", err)
	}
	if len(store.created) != 1 {
		t.Fatalf("expected 1 created relationship, got %d", len(store.created))
	}
}

func TestGetPermissionsForRelation(t *testing.T) {
	authz := testAuthorizer(newMockStorer())

	perms := authz.GetPermissionsForRelation("tenant", "owner")
	if len(perms) == 0 {
		t.Fatal("expected permissions for owner")
	}

	permSet := make(map[string]bool)
	for _, p := range perms {
		permSet[p] = true
	}

	for _, expected := range []string{"manage", "read", "delete"} {
		if !permSet[expected] {
			t.Errorf("expected owner to grant %q", expected)
		}
	}
}
