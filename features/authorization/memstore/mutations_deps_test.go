package memstore

import (
	"context"
	"testing"

	"github.com/gopernicus/gopernicus/features/authorization/domain/mutation"
	"github.com/gopernicus/gopernicus/features/authorization/domain/relationship"
)

// depsContain reports whether the recorded dependency set includes a scope with
// the given kind/type/id (matched by canonical key).
func depsContain(deps []mutation.Dependency, want mutation.ScopeKey) bool {
	for _, d := range deps {
		if d.Scope.Canonical() == want.Canonical() {
			return true
		}
	}
	return false
}

// TestDecisionViewCheckRelationRecordsIntermediateScopes proves the F2(a) fix:
// a group-expanded CheckRelation records EVERY intermediate resource scope whose
// membership edges the expansion traversed, not just the queried scope. Here
// alice is editor on doc:1 only via her membership edge on group:eng — so a
// concurrent revoke of that membership (bumping group:eng's scope) must be a
// tracked dependency.
func TestDecisionViewCheckRelationRecordsIntermediateScopes(t *testing.T) {
	ctx := context.Background()
	store := New(WithGuardianPolicy(mutation.GuardianPolicy{}))
	m := store.Mutations()

	// alice #member group:eng — the edge lives under group:eng's resource scope.
	memberEdge := mutation.Command{
		MutationID:    mustID(t),
		Scope:         mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "group", ID: "eng"},
		Operation:     mutation.OpGrant,
		Relationships: []mutation.RelationshipRow{{Relation: "member", Subject: relationship.SubjectRef{Type: "user", ID: "alice"}}},
	}
	if _, err := m.Apply(ctx, memberEdge, nil); err != nil {
		t.Fatalf("seed member edge: %v", err)
	}
	// group:eng#member #editor doc:1 — the edge lives under doc:1's resource scope.
	editorEdge := mutation.Command{
		MutationID:    mustID(t),
		Scope:         mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "doc", ID: "1"},
		Operation:     mutation.OpGrant,
		Relationships: []mutation.RelationshipRow{{Relation: "editor", Subject: relationship.SubjectRef{Type: "group", ID: "eng", Relation: "member"}}},
	}
	if _, err := m.Apply(ctx, editorEdge, nil); err != nil {
		t.Fatalf("seed editor edge: %v", err)
	}

	m.st.mu.Lock()
	defer m.st.mu.Unlock()
	view := &decisionView{m: m, deps: map[string]mutation.Dependency{}}
	ok, err := view.CheckRelation(ctx, mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "doc", ID: "1"}, "editor", "user", "alice")
	if err != nil {
		t.Fatalf("CheckRelation: %v", err)
	}
	if !ok {
		t.Fatalf("alice must be editor on doc:1 via group:eng membership")
	}
	deps := view.Dependencies()
	if !depsContain(deps, mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "doc", ID: "1"}) {
		t.Fatalf("queried scope doc:1 must be a dependency; deps=%+v", deps)
	}
	if !depsContain(deps, mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "group", ID: "eng"}) {
		t.Fatalf("intermediate scope group:eng must be a dependency (F2(a)); deps=%+v", deps)
	}
}

// TestDecisionViewHasRoleGlobalFallbackRecordsSubjectScope proves the F2(b) fix:
// when HasRole consults the global fallback (exact-resource role missing), the
// subject scope becomes a tracked dependency so a concurrent global grant/revoke
// invalidates the decision.
func TestDecisionViewHasRoleGlobalFallbackRecordsSubjectScope(t *testing.T) {
	ctx := context.Background()
	store := New(WithGuardianPolicy(mutation.GuardianPolicy{}))
	m := store.Mutations()

	// Global (subject-scoped) role assignment for alice.
	globalRole := mutation.Command{
		MutationID: mustID(t),
		Scope:      mutation.ScopeKey{Kind: mutation.ScopeSubject, Type: "user", ID: "alice"},
		Operation:  mutation.OpRoleAssign,
		Roles:      []mutation.RoleRow{{SubjectType: "user", SubjectID: "alice", Role: "auditor"}},
	}
	if _, err := m.Apply(ctx, globalRole, nil); err != nil {
		t.Fatalf("seed global role: %v", err)
	}

	m.st.mu.Lock()
	defer m.st.mu.Unlock()
	view := &decisionView{m: m, deps: map[string]mutation.Dependency{}}
	ok, err := view.HasRole(ctx, mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "doc", ID: "1"}, "auditor", "user", "alice")
	if err != nil {
		t.Fatalf("HasRole: %v", err)
	}
	if !ok {
		t.Fatalf("alice's global auditor role must satisfy the resource-scoped query via fallback")
	}
	deps := view.Dependencies()
	if !depsContain(deps, mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "doc", ID: "1"}) {
		t.Fatalf("queried scope doc:1 must be a dependency; deps=%+v", deps)
	}
	if !depsContain(deps, mutation.ScopeKey{Kind: mutation.ScopeSubject, Type: "user", ID: "alice"}) {
		t.Fatalf("subject scope user:alice must be a dependency when the global fallback is consulted (F2(b)); deps=%+v", deps)
	}
}

// TestDecisionViewHasRoleExactScopeSkipsSubjectScope is the negative: when the
// exact-resource role matches, the global roles are NOT consulted, so the subject
// scope must NOT be recorded — the decision is independent of it.
func TestDecisionViewHasRoleExactScopeSkipsSubjectScope(t *testing.T) {
	ctx := context.Background()
	store := New(WithGuardianPolicy(mutation.GuardianPolicy{}))
	m := store.Mutations()

	// Resource-scoped role assignment for alice on doc:1.
	scopedRole := mutation.Command{
		MutationID: mustID(t),
		Scope:      mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "doc", ID: "1"},
		Operation:  mutation.OpRoleAssign,
		Roles:      []mutation.RoleRow{{SubjectType: "user", SubjectID: "alice", Role: "auditor"}},
	}
	if _, err := m.Apply(ctx, scopedRole, nil); err != nil {
		t.Fatalf("seed scoped role: %v", err)
	}

	m.st.mu.Lock()
	defer m.st.mu.Unlock()
	view := &decisionView{m: m, deps: map[string]mutation.Dependency{}}
	ok, err := view.HasRole(ctx, mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "doc", ID: "1"}, "auditor", "user", "alice")
	if err != nil {
		t.Fatalf("HasRole: %v", err)
	}
	if !ok {
		t.Fatalf("alice's exact-scope auditor role must satisfy the query")
	}
	if depsContain(view.Dependencies(), mutation.ScopeKey{Kind: mutation.ScopeSubject, Type: "user", ID: "alice"}) {
		t.Fatalf("subject scope must NOT be recorded when the exact-resource role matched; deps=%+v", view.Dependencies())
	}
}
