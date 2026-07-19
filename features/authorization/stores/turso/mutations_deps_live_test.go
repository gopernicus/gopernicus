//go:build integration

// Live turso white-box tests for the DecisionView dependency tracker (F2). They
// prove the guard reader records the FULL dependency set: every intermediate
// resource scope a group-expanded CheckRelation traversed, and the subject scope
// whenever HasRole consults the global fallback. Under-recording is the
// optimistic-concurrency safety defect these guard against. They open a real
// transaction, so they are integration-tag gated and require
// TURSO_DATABASE_URL/TURSO_AUTH_TOKEN, like the conformance suite.
package turso

import (
	"context"
	"testing"

	"github.com/gopernicus/gopernicus/features/authorization"
	"github.com/gopernicus/gopernicus/features/authorization/domain/mutation"
	"github.com/gopernicus/gopernicus/features/authorization/domain/relationship"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
)

// liveReposNoGuardian builds repositories with an empty guardian policy so
// member-first / group seeding is not invariant-blocked.
func liveReposNoGuardian(t *testing.T) (*tursodb.DB, authorization.Repositories) {
	t.Helper()
	url, token := requireTursoEnv(t)
	db := openAndMigrate(t, url, token)
	repos, err := Repositories(db, WithGuardianPolicy(mutation.GuardianPolicy{}))
	if err != nil {
		t.Fatalf("Repositories: %v", err)
	}
	return db, repos
}

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

// TestDecisionViewCheckRelationRecordsIntermediateScopesLive proves the F2(a)
// fix: a group-expanded CheckRelation records EVERY intermediate resource scope
// whose membership edges the reachable CTE traversed, not just the queried scope.
func TestDecisionViewCheckRelationRecordsIntermediateScopesLive(t *testing.T) {
	ctx := context.Background()
	db, repos := liveReposNoGuardian(t)
	m := repos.Mutations

	// alice #member group:eng — the edge lives under group:eng's resource scope.
	memberEdge := mutation.Command{
		MutationID:    mutID(t),
		Scope:         mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "group", ID: "eng"},
		Operation:     mutation.OpGrant,
		Relationships: []mutation.RelationshipRow{{Relation: "member", Subject: relationship.SubjectRef{Type: "user", ID: "alice"}}},
	}
	mustApplyLive(t, m, memberEdge)
	// group:eng#member #editor doc:1 — the edge lives under doc:1's resource scope.
	editorEdge := mutation.Command{
		MutationID:    mutID(t),
		Scope:         mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "doc", ID: "1"},
		Operation:     mutation.OpGrant,
		Relationships: []mutation.RelationshipRow{{Relation: "editor", Subject: relationship.SubjectRef{Type: "group", ID: "eng", Relation: "member"}}},
	}
	mustApplyLive(t, m, editorEdge)

	var ok bool
	var deps []mutation.Dependency
	if err := db.InTx(ctx, func(tx *tursodb.Tx) error {
		view := newDecisionView(tx)
		var e error
		ok, e = view.CheckRelation(ctx, mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "doc", ID: "1"}, "editor", "user", "alice")
		if e != nil {
			return e
		}
		deps = view.Dependencies()
		return nil
	}); err != nil {
		t.Fatalf("InTx: %v", err)
	}
	if !ok {
		t.Fatalf("alice must be editor on doc:1 via group:eng membership")
	}
	if !depsContain(deps, mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "doc", ID: "1"}) {
		t.Fatalf("queried scope doc:1 must be a dependency; deps=%+v", deps)
	}
	if !depsContain(deps, mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "group", ID: "eng"}) {
		t.Fatalf("intermediate scope group:eng must be a dependency (F2(a)); deps=%+v", deps)
	}
}

// TestDecisionViewHasRoleGlobalFallbackRecordsSubjectScopeLive proves the F2(b)
// fix: when HasRole consults the global fallback (exact-resource role missing),
// the subject scope becomes a tracked dependency.
func TestDecisionViewHasRoleGlobalFallbackRecordsSubjectScopeLive(t *testing.T) {
	ctx := context.Background()
	db, repos := liveReposNoGuardian(t)
	m := repos.Mutations

	globalRole := mutation.Command{
		MutationID: mutID(t),
		Scope:      mutation.ScopeKey{Kind: mutation.ScopeSubject, Type: "user", ID: "alice"},
		Operation:  mutation.OpRoleAssign,
		Roles:      []mutation.RoleRow{{SubjectType: "user", SubjectID: "alice", Role: "auditor"}},
	}
	mustApplyLive(t, m, globalRole)

	var ok bool
	var deps []mutation.Dependency
	if err := db.InTx(ctx, func(tx *tursodb.Tx) error {
		view := newDecisionView(tx)
		var e error
		ok, e = view.HasRole(ctx, mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "doc", ID: "1"}, "auditor", "user", "alice")
		if e != nil {
			return e
		}
		deps = view.Dependencies()
		return nil
	}); err != nil {
		t.Fatalf("InTx: %v", err)
	}
	if !ok {
		t.Fatalf("alice's global auditor role must satisfy the resource-scoped query via fallback")
	}
	if !depsContain(deps, mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "doc", ID: "1"}) {
		t.Fatalf("queried scope doc:1 must be a dependency; deps=%+v", deps)
	}
	if !depsContain(deps, mutation.ScopeKey{Kind: mutation.ScopeSubject, Type: "user", ID: "alice"}) {
		t.Fatalf("subject scope user:alice must be a dependency when the global fallback is consulted (F2(b)); deps=%+v", deps)
	}
}

// TestDecisionViewHasRoleExactScopeSkipsSubjectScopeLive is the negative: when
// the exact-resource role matches, the global roles are NOT consulted, so the
// subject scope must NOT be recorded.
func TestDecisionViewHasRoleExactScopeSkipsSubjectScopeLive(t *testing.T) {
	ctx := context.Background()
	db, repos := liveReposNoGuardian(t)
	m := repos.Mutations

	scopedRole := mutation.Command{
		MutationID: mutID(t),
		Scope:      mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "doc", ID: "1"},
		Operation:  mutation.OpRoleAssign,
		Roles:      []mutation.RoleRow{{SubjectType: "user", SubjectID: "alice", Role: "auditor"}},
	}
	mustApplyLive(t, m, scopedRole)

	var ok bool
	var deps []mutation.Dependency
	if err := db.InTx(ctx, func(tx *tursodb.Tx) error {
		view := newDecisionView(tx)
		var e error
		ok, e = view.HasRole(ctx, mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "doc", ID: "1"}, "auditor", "user", "alice")
		if e != nil {
			return e
		}
		deps = view.Dependencies()
		return nil
	}); err != nil {
		t.Fatalf("InTx: %v", err)
	}
	if !ok {
		t.Fatalf("alice's exact-scope auditor role must satisfy the query")
	}
	if depsContain(deps, mutation.ScopeKey{Kind: mutation.ScopeSubject, Type: "user", ID: "alice"}) {
		t.Fatalf("subject scope must NOT be recorded when the exact-resource role matched; deps=%+v", deps)
	}
}
