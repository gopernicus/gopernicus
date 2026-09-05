package storetest

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/mutation"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/sdk"
)

// The guarded-permission family (plan authorization-decisionview-permission,
// task 6): the host-facing DecisionView.CheckPermission walks the schema's
// Through hops INSIDE the mutation boundary over the store's transaction-bound
// primitives (RelationTargets, CheckRelationBounded), so a guard reaches the
// same decision the read-side Check reaches, with every navigated scope a
// revision-validated dependency. Every case runs the REAL authorization.Service
// over the store under test — the seam the fake-reader engine test cannot cover.

// permissionModel is the segovia v2 tenancy shape: dashboard.manage is inherited
// from the space, space.manage from the parent space or the tenant, tenant.admin
// may be a group userset. Every type declares `owner` so seeds satisfy the
// default guardian (owner-first) policy the conformance repositories carry.
func permissionModel() authorization.Schema {
	user := []authorization.SubjectTypeRef{{Type: "user"}}
	return authorization.NewSchema([]authorization.ResourceSchema{
		{Name: "group", Def: authorization.ResourceTypeDef{
			Relations: map[string]authorization.RelationDef{
				"owner":  {AllowedSubjects: user},
				"member": {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}},
			},
		}},
		{Name: "tenant", Def: authorization.ResourceTypeDef{
			Relations: map[string]authorization.RelationDef{
				"owner": {AllowedSubjects: user},
				"admin": {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}},
			},
			Permissions: map[string]authorization.PermissionRule{
				"manage": authorization.AnyOf(authorization.Direct("owner"), authorization.Direct("admin")),
			},
		}},
		{Name: "space", Def: authorization.ResourceTypeDef{
			Relations: map[string]authorization.RelationDef{
				"owner":   {AllowedSubjects: user},
				"tenant":  {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "tenant"}}},
				"parent":  {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "space"}}},
				"manager": {AllowedSubjects: user},
			},
			Permissions: map[string]authorization.PermissionRule{
				"manage": authorization.AnyOf(authorization.Direct("manager"), authorization.Through("parent", "manage"), authorization.Through("tenant", "manage")),
			},
		}},
		{Name: "dashboard", Def: authorization.ResourceTypeDef{
			Relations: map[string]authorization.RelationDef{
				"owner":  {AllowedSubjects: user},
				"space":  {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "space"}}},
				"viewer": {AllowedSubjects: user},
			},
			Permissions: map[string]authorization.PermissionRule{
				"manage": authorization.AnyOf(authorization.Direct("owner"), authorization.Through("space", "manage")),
			},
		}},
	})
}

// permissionGuard is the host-guard shape the consumer adopts: ask the view
// whether the actor holds `permission` on the mutated scope, deny otherwise. It
// captures what CheckPermission answered and what the view recorded.
type permissionGuard struct {
	permission string
	mu         sync.Mutex
	allowed    bool
	checkErr   error
	deps       []mutation.Dependency
}

func (g *permissionGuard) AuthorizeMutation(ctx context.Context, attempt authorization.MutationAttempt, view authorization.DecisionView) error {
	ok, err := view.CheckPermission(ctx, attempt.Scope, g.permission, attempt.Actor.Type, attempt.Actor.ID)
	g.mu.Lock()
	g.allowed, g.checkErr, g.deps = ok, err, view.Dependencies()
	g.mu.Unlock()
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("guard: %s on %s denied: %w", g.permission, attempt.Scope.Canonical(), sdk.ErrForbidden)
	}
	return nil
}

func typedScope(typ, id string) mutation.ScopeKey {
	return mutation.ScopeKey{Kind: mutation.ScopeResource, Type: typ, ID: id}
}

func typedEdge(t *testing.T, op mutation.Operation, scope mutation.ScopeKey, relation string, subject relationship.SubjectRef) mutation.Command {
	t.Helper()
	return mutation.Command{
		MutationID:    mustID(t),
		Scope:         scope,
		Operation:     op,
		Relationships: []mutation.RelationshipRow{{Relation: relation, Subject: subject}},
	}
}

func userRef(id string) relationship.SubjectRef { return relationship.SubjectRef{Type: "user", ID: id} }

// seedTenancy establishes the tenancy graph for suffix through the trusted
// Apply path (owner first on every resource, so the default guardian admits the
// rest): tenant-owner owns tenant:t; group-admin is a member of group:admins,
// which is tenant admin; space:root belongs to the tenant; space:child hangs
// under root and has space-manager; dashboard:d lives in child and has dash-owner.
func seedTenancy(t *testing.T, m mutation.MutationRepository, suffix string) {
	t.Helper()
	tenant, group := typedScope("tenant", "t"+suffix), typedScope("group", "admins"+suffix)
	root, child, dash := typedScope("space", "root"+suffix), typedScope("space", "child"+suffix), typedScope("dashboard", "d"+suffix)
	for _, cmd := range []mutation.Command{
		typedEdge(t, mutation.OpGrant, tenant, "owner", userRef("tenant-owner")),
		typedEdge(t, mutation.OpGrant, group, "owner", userRef("seed-owner")),
		typedEdge(t, mutation.OpGrant, group, "member", userRef("group-admin")),
		typedEdge(t, mutation.OpGrant, tenant, "admin", relationship.SubjectRef{Type: "group", ID: "admins" + suffix, Relation: "member"}),
		typedEdge(t, mutation.OpGrant, root, "owner", userRef("seed-owner")),
		typedEdge(t, mutation.OpGrant, root, "tenant", relationship.SubjectRef{Type: "tenant", ID: "t" + suffix}),
		typedEdge(t, mutation.OpGrant, child, "owner", userRef("seed-owner")),
		typedEdge(t, mutation.OpGrant, child, "parent", relationship.SubjectRef{Type: "space", ID: "root" + suffix}),
		typedEdge(t, mutation.OpGrant, child, "manager", userRef("space-manager")),
		typedEdge(t, mutation.OpGrant, dash, "owner", userRef("dash-owner")),
		typedEdge(t, mutation.OpGrant, dash, "space", relationship.SubjectRef{Type: "space", ID: "child" + suffix}),
	} {
		mustApply(t, m, cmd)
	}
}

func newPermissionService(t *testing.T, repos authorization.Repositories, guard authorization.MutationGuard, model authorization.Schema, limits authorization.EvaluationLimits) *authorization.Service {
	t.Helper()
	comps, err := authorization.NewService(authorization.Repositories{
		Relationships: repos.Relationships, Roles: repos.Roles, Mutations: repos.Mutations,
	}, authorization.Config{RelationshipModel: model, Guard: guard, Limits: limits})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return comps.Service
}

func grantDashboardViewer(t *testing.T, svc *authorization.Service, suffix, who, reader string) (*authorization.Receipt, error) {
	t.Helper()
	return svc.GrantRelationship(context.Background(), authorization.Actor{PrincipalRef: authorization.PrincipalRef{Type: "user", ID: who}}, authorization.GrantRelationshipCommand{
		MutationID: mustID(t), ResourceType: "dashboard", ResourceID: "d" + suffix, Relation: "viewer", Subject: authorization.SubjectRef{Type: "user", ID: reader},
	})
}

func depsHave(deps []mutation.Dependency, scope mutation.ScopeKey) bool {
	for _, d := range deps {
		if d.Scope.Canonical() == scope.Canonical() {
			return true
		}
	}
	return false
}

func viewerRowExists(t *testing.T, repos authorization.Repositories, suffix, reader string) bool {
	t.Helper()
	ok, err := repos.Relationships.CheckRelationExists(context.Background(), "dashboard", "d"+suffix, "viewer", "user", reader)
	if err != nil {
		t.Fatalf("CheckRelationExists: %v", err)
	}
	return ok
}

// specGuardedPermissionThrough: a host guard's CheckPermission answers exactly
// what the read-side Check answers — direct owner, one Through hop, three
// Through hops, and a userset reached through a container — and records every
// navigated scope; a principal with no authority is denied and writes nothing.
func specGuardedPermissionThrough(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	ctx := context.Background()
	repos := newRepos(t)
	if repos.Relationships == nil {
		t.Skip("relationship kind not wired")
	}
	seedTenancy(t, repos.Mutations, "")
	dash, child, root, tenant, group := typedScope("dashboard", "d"), typedScope("space", "child"), typedScope("space", "root"), typedScope("tenant", "t"), typedScope("group", "admins")

	cases := []struct {
		who      string
		allowed  bool
		wantDeps []mutation.ScopeKey
	}{
		{"dash-owner", true, []mutation.ScopeKey{dash}},
		{"space-manager", true, []mutation.ScopeKey{dash, child}},
		{"tenant-owner", true, []mutation.ScopeKey{dash, child, root, tenant}},
		{"group-admin", true, []mutation.ScopeKey{dash, child, root, tenant, group}},
		{"stranger", false, []mutation.ScopeKey{dash, child, root, tenant}},
	}
	for i, tc := range cases {
		guard := &permissionGuard{permission: "manage"}
		svc := newPermissionService(t, repos, guard, permissionModel(), authorization.EvaluationLimits{})
		want, err := svc.Check(ctx, authorization.CheckRequest{
			Principal: authorization.PrincipalRef{Type: "user", ID: tc.who}, Permission: "manage", Resource: authorization.Resource{Type: "dashboard", ID: "d"},
		})
		if err != nil || want.Allowed != tc.allowed {
			t.Fatalf("%s: read-side Check = (%v, %v), want allowed=%v", tc.who, want.Allowed, err, tc.allowed)
		}
		reader := "reader" + strconv.Itoa(i)
		rcpt, err := grantDashboardViewer(t, svc, "", tc.who, reader)
		if guard.checkErr != nil {
			t.Fatalf("%s: CheckPermission errored: %v", tc.who, guard.checkErr)
		}
		if guard.allowed != tc.allowed {
			t.Fatalf("%s: guard CheckPermission=%v, read-side Check=%v", tc.who, guard.allowed, tc.allowed)
		}
		for _, sk := range tc.wantDeps {
			if !depsHave(guard.deps, sk) {
				t.Fatalf("%s: navigated scope %s must be a dependency; deps=%+v", tc.who, sk.Canonical(), guard.deps)
			}
		}
		if tc.allowed {
			if err != nil || rcpt == nil || rcpt.Outcome != mutation.OutcomeApplied {
				t.Fatalf("%s: inherited authority must apply: rcpt=%+v err=%v", tc.who, rcpt, err)
			}
			if !viewerRowExists(t, repos, "", reader) {
				t.Fatalf("%s: applied grant must leave its row", tc.who)
			}
		} else {
			if !errors.Is(err, sdk.ErrForbidden) || rcpt != nil {
				t.Fatalf("%s: want forbidden with no receipt, got rcpt=%+v err=%v", tc.who, rcpt, err)
			}
			if viewerRowExists(t, repos, "", reader) {
				t.Fatalf("%s: a denied guarded write must commit nothing", tc.who)
			}
		}
	}
}

// expansionModel: doc.view is inherited from the folder (one Through hop), and
// the folder's viewer may be a group userset — so the guard's direct read on the
// folder expands a membership chain inside the store adapter.
func expansionModel() authorization.Schema {
	user := []authorization.SubjectTypeRef{{Type: "user"}}
	return authorization.NewSchema([]authorization.ResourceSchema{
		{Name: "group", Def: authorization.ResourceTypeDef{
			Relations: map[string]authorization.RelationDef{
				"owner":  {AllowedSubjects: user},
				"member": {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}},
			},
		}},
		{Name: "folder", Def: authorization.ResourceTypeDef{
			Relations: map[string]authorization.RelationDef{
				"owner":  {AllowedSubjects: user},
				"viewer": {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}},
			},
			Permissions: map[string]authorization.PermissionRule{"view": authorization.AnyOf(authorization.Direct("viewer"))},
		}},
		{Name: "doc", Def: authorization.ResourceTypeDef{
			Relations: map[string]authorization.RelationDef{
				"owner":  {AllowedSubjects: user},
				"folder": {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "folder"}}},
				"viewer": {AllowedSubjects: user},
			},
			Permissions: map[string]authorization.PermissionRule{"view": authorization.AnyOf(authorization.Direct("viewer"), authorization.Through("folder", "view"))},
		}},
	})
}

// specGuardedPermissionExpansionParity: the guard's bounded expansion overflows
// at exactly the budget the read-side Check overflows at — through a container
// — and an over-budget guard is ErrEvaluationLimit that persists no row, no
// receipt, and no revision change.
func specGuardedPermissionExpansionParity(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	ctx := context.Background()
	repos := newRepos(t)
	if repos.Relationships == nil {
		t.Skip("relationship kind not wired")
	}
	m := repos.Mutations
	// alice -> g1#member -> g2#member -> g3#member; folder:f#viewer@g3#member;
	// doc:x#folder@folder:f. alice's expansion on the folder read: alice, g1, g2,
	// g3, and the folder grant state = 5 distinct states. Engine graph states for
	// doc.view: (doc,view) + (folder,view) = 2.
	for _, g := range []string{"g1", "g2", "g3"} {
		mustApply(t, m, typedEdge(t, mutation.OpGrant, typedScope("group", g), "owner", userRef("seed-owner")))
	}
	mustApply(t, m, typedEdge(t, mutation.OpGrant, typedScope("group", "g1"), "member", userRef("alice")))
	mustApply(t, m, typedEdge(t, mutation.OpGrant, typedScope("group", "g2"), "member", relationship.SubjectRef{Type: "group", ID: "g1", Relation: "member"}))
	mustApply(t, m, typedEdge(t, mutation.OpGrant, typedScope("group", "g3"), "member", relationship.SubjectRef{Type: "group", ID: "g2", Relation: "member"}))
	mustApply(t, m, typedEdge(t, mutation.OpGrant, typedScope("folder", "f"), "owner", userRef("seed-owner")))
	mustApply(t, m, typedEdge(t, mutation.OpGrant, typedScope("folder", "f"), "viewer", relationship.SubjectRef{Type: "group", ID: "g3", Relation: "member"}))
	mustApply(t, m, typedEdge(t, mutation.OpGrant, typedScope("doc", "x"), "owner", userRef("seed-owner")))
	docBefore := mustApply(t, m, typedEdge(t, mutation.OpGrant, typedScope("doc", "x"), "folder", relationship.SubjectRef{Type: "folder", ID: "f"})).Revision

	for i, limit := range []int{4, 5, 6} {
		guard := &permissionGuard{permission: "view"}
		svc := newPermissionService(t, repos, guard, expansionModel(), authorization.EvaluationLimits{MaxGraphStates: limit})
		want, wantErr := svc.Check(ctx, authorization.CheckRequest{
			Principal: authorization.PrincipalRef{Type: "user", ID: "alice"}, Permission: "view", Resource: authorization.Resource{Type: "doc", ID: "x"},
		})
		reader := "reader" + strconv.Itoa(i)
		rcpt, err := svc.GrantRelationship(ctx, authorization.Actor{PrincipalRef: authorization.PrincipalRef{Type: "user", ID: "alice"}}, authorization.GrantRelationshipCommand{
			MutationID: mustID(t), ResourceType: "doc", ResourceID: "x", Relation: "viewer", Subject: authorization.SubjectRef{Type: "user", ID: reader},
		})
		if (wantErr == nil) != (guard.checkErr == nil) || !errors.Is(guard.checkErr, wantErr) || want.Allowed != guard.allowed {
			t.Fatalf("MaxGraphStates=%d: read-side (%v, %v) vs guard (%v, %v)", limit, want.Allowed, wantErr, guard.allowed, guard.checkErr)
		}
		switch limit {
		case 4:
			if !errors.Is(err, authorization.ErrEvaluationLimit) || !errors.Is(err, sdk.ErrUnavailable) || rcpt != nil {
				t.Fatalf("MaxGraphStates=4 must be ErrEvaluationLimit (unavailable) with no receipt: rcpt=%+v err=%v", rcpt, err)
			}
			if ok, _ := repos.Relationships.CheckRelationExists(ctx, "doc", "x", "viewer", "user", reader); ok {
				t.Fatalf("an over-budget guarded write must persist no row")
			}
			// No revision change: an exact-revision no-op probe still sees docBefore.
			probe := typedEdge(t, mutation.OpGrant, typedScope("doc", "x"), "folder", relationship.SubjectRef{Type: "folder", ID: "f"})
			probe.ExpectedRevision = &docBefore
			if p, err := m.Apply(ctx, probe, nil); err != nil || p.Outcome != mutation.OutcomeNoChange {
				t.Fatalf("doc:x revision must be unchanged by the over-budget write (expected %d): rcpt=%+v err=%v", docBefore, p, err)
			}
		default:
			if err != nil || rcpt == nil || rcpt.Outcome != mutation.OutcomeApplied || !guard.allowed {
				t.Fatalf("MaxGraphStates=%d must allow the 5-state expansion: rcpt=%+v err=%v allowed=%v", limit, rcpt, err, guard.allowed)
			}
			docBefore = rcpt.Revision
		}
	}
}

// specGuardedPermissionThroughRevokeRaces: a guarded write whose authority is
// INHERITED (a parent edge; a folder-style userset grant) races a committed
// revoke of that inherited edge. The revoke always commits; the guarded write is
// applied (it serialized first — both commits are legitimate) or aborts cleanly
// as stale/denied with no row and no receipt — never a committed stale allow.
// Portable across the three stores: the deterministic interleaving proof is
// pgx-specific (see stores/pgx mutations_permission_live_test.go).
func specGuardedPermissionThroughRevokeRaces(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	repos := newRepos(t)
	if repos.Relationships == nil {
		t.Skip("relationship kind not wired")
	}
	m := repos.Mutations
	const rounds = 6

	type race struct {
		name   string
		who    string
		revoke func(t *testing.T, suffix string) mutation.Command
	}
	races := []race{
		{"parent edge", "space-manager", func(t *testing.T, suffix string) mutation.Command {
			return typedEdge(t, mutation.OpRevoke, typedScope("dashboard", "d"+suffix), "space", relationship.SubjectRef{Type: "space", ID: "child" + suffix})
		}},
		{"inherited userset grant", "group-admin", func(t *testing.T, suffix string) mutation.Command {
			return typedEdge(t, mutation.OpRevoke, typedScope("group", "admins"+suffix), "member", userRef("group-admin"))
		}},
	}
	for _, rc := range races {
		t.Run(rc.name, func(t *testing.T) {
			for r := 0; r < rounds; r++ {
				suffix := "-" + rc.who + strconv.Itoa(r)
				seedTenancy(t, m, suffix)
				guard := &permissionGuard{permission: "manage"}
				svc := newPermissionService(t, repos, guard, permissionModel(), authorization.EvaluationLimits{})
				revokeCmd := rc.revoke(t, suffix)

				var wg sync.WaitGroup
				var guardedRcpt, revokeRcpt *mutation.Receipt
				var guardedErr, revokeErr error
				wg.Add(2)
				go func() {
					defer wg.Done()
					guardedRcpt, guardedErr = grantDashboardViewer(t, svc, suffix, rc.who, "reader")
				}()
				go func() {
					defer wg.Done()
					revokeRcpt, revokeErr = m.Apply(context.Background(), revokeCmd, nil)
				}()
				wg.Wait()

				if revokeErr != nil || revokeRcpt.Outcome != mutation.OutcomeApplied {
					t.Fatalf("round %d: the revoke must commit: rcpt=%+v err=%v", r, revokeRcpt, revokeErr)
				}
				switch {
				case guardedErr == nil:
					if guardedRcpt == nil || guardedRcpt.Outcome != mutation.OutcomeApplied || !viewerRowExists(t, repos, suffix, "reader") {
						t.Fatalf("round %d: a nil-error guarded write must be applied with its row: %+v", r, guardedRcpt)
					}
				case errors.Is(guardedErr, sdk.ErrConflict) || errors.Is(guardedErr, sdk.ErrForbidden):
					if guardedRcpt != nil || viewerRowExists(t, repos, suffix, "reader") {
						t.Fatalf("round %d: a stale/denied guarded write must leave no receipt and no row: rcpt=%+v err=%v", r, guardedRcpt, guardedErr)
					}
				default:
					t.Fatalf("round %d: guarded write must be applied, stale, or denied; got rcpt=%+v err=%v", r, guardedRcpt, guardedErr)
				}
			}
		})
	}
}
