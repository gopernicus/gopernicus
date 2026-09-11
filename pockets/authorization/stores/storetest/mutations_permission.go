package storetest

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/sdk"
)

// The guarded-permission family (plan authorization-decisionview-permission,
// task 6): the host-facing DecisionView.CheckPermission walks the schema's
// Through hops INSIDE the mutation boundary over the store's transaction-bound
// primitives (RelationTargets, CheckRelationBounded), so a guard reaches the
// same decision the read-side Check reaches, with every navigated target in
// the same serialized authorization boundary. Every case runs the REAL mutations.Service
// over the store under test — the seam the fake-reader engine test cannot cover.

// permissionModel is the segovia v2 tenancy shape: dashboard.manage is inherited
// from the space, space.manage from the parent space or the tenant, tenant.admin
// may be a group userset. Every type declares `owner` so seeds satisfy the
// default guardian (owner-first) policy the conformance repositories carry.
func permissionModel() relationships.Schema {
	user := []relationships.SubjectTypeRef{{Type: "user"}}
	return relationships.NewSchema([]relationships.ResourceSchema{
		{Name: "group", Def: relationships.ResourceTypeDef{
			Relations: map[string]relationships.RelationDef{
				"owner":  {AllowedSubjects: user},
				"member": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}},
			},
		}},
		{Name: "tenant", Def: relationships.ResourceTypeDef{
			Relations: map[string]relationships.RelationDef{
				"owner": {AllowedSubjects: user},
				"admin": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}},
			},
			Permissions: map[string]relationships.PermissionRule{
				"manage": relationships.AnyOf(relationships.Direct("owner"), relationships.Direct("admin")),
			},
		}},
		{Name: "space", Def: relationships.ResourceTypeDef{
			Relations: map[string]relationships.RelationDef{
				"owner":   {AllowedSubjects: user},
				"tenant":  {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "tenant"}}},
				"parent":  {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "space"}}},
				"manager": {AllowedSubjects: user},
			},
			Permissions: map[string]relationships.PermissionRule{
				"manage": relationships.AnyOf(relationships.Direct("manager"), relationships.Through("parent", "manage"), relationships.Through("tenant", "manage")),
			},
		}},
		{Name: "dashboard", Def: relationships.ResourceTypeDef{
			Relations: map[string]relationships.RelationDef{
				"owner":  {AllowedSubjects: user},
				"space":  {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "space"}}},
				"viewer": {AllowedSubjects: user},
			},
			Permissions: map[string]relationships.PermissionRule{
				"manage": relationships.AnyOf(relationships.Direct("owner"), relationships.Through("space", "manage")),
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
}

func (g *permissionGuard) AuthorizeMutation(ctx context.Context, attempt mutations.MutationAttempt, view mutations.DecisionView) error {
	result, err := view.Check(ctx, authmodel.CheckRequest{Principal: attempt.Actor.PrincipalRef, Permission: g.permission, Resource: authmodel.Resource{Type: attempt.Target.Type, ID: attempt.Target.ID}})
	ok := result.Allowed
	g.mu.Lock()
	g.allowed, g.checkErr = ok, err
	g.mu.Unlock()
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("guard: %s on %s denied: %w", g.permission, attempt.Target.String(), sdk.ErrForbidden)
	}
	return nil
}

func typedScope(typ, id string) mutations.Target {
	return mutations.Target{Kind: mutations.TargetResource, Type: typ, ID: id}
}

func typedEdge(t *testing.T, op mutations.Operation, scope mutations.Target, relation string, subject relationships.SubjectRef) mutations.Command {
	t.Helper()
	return mutations.Command{
		Target:        scope,
		Operation:     op,
		Relationships: []mutations.RelationshipRow{{Relation: relation, Subject: subject}},
	}
}

func userRef(id string) relationships.SubjectRef {
	return relationships.SubjectRef{Type: "user", ID: id}
}

// seedTenancy establishes the tenancy graph for suffix through the trusted
// Apply path (owner first on every resource, so the default guardian admits the
// rest): tenant-owner owns tenant:t; group-admin is a member of group:admins,
// which is tenant admin; space:root belongs to the tenant; space:child hangs
// under root and has space-manager; dashboard:d lives in child and has dash-owner.
func seedTenancy(t *testing.T, m mutations.MutationRepository, suffix string) {
	t.Helper()
	tenant, group := typedScope("tenant", "t"+suffix), typedScope("group", "admins"+suffix)
	root, child, dash := typedScope("space", "root"+suffix), typedScope("space", "child"+suffix), typedScope("dashboard", "d"+suffix)
	for _, cmd := range []mutations.Command{
		typedEdge(t, mutations.OpGrant, tenant, "owner", userRef("tenant-owner")),
		typedEdge(t, mutations.OpGrant, group, "owner", userRef("seed-owner")),
		typedEdge(t, mutations.OpGrant, group, "member", userRef("group-admin")),
		typedEdge(t, mutations.OpGrant, tenant, "admin", relationships.SubjectRef{Type: "group", ID: "admins" + suffix, Relation: "member"}),
		typedEdge(t, mutations.OpGrant, root, "owner", userRef("seed-owner")),
		typedEdge(t, mutations.OpGrant, root, "tenant", relationships.SubjectRef{Type: "tenant", ID: "t" + suffix}),
		typedEdge(t, mutations.OpGrant, child, "owner", userRef("seed-owner")),
		typedEdge(t, mutations.OpGrant, child, "parent", relationships.SubjectRef{Type: "space", ID: "root" + suffix}),
		typedEdge(t, mutations.OpGrant, child, "manager", userRef("space-manager")),
		typedEdge(t, mutations.OpGrant, dash, "owner", userRef("dash-owner")),
		typedEdge(t, mutations.OpGrant, dash, "space", relationships.SubjectRef{Type: "space", ID: "child" + suffix}),
	} {
		mustApply(t, m, cmd)
	}
}

func newPermissionService(t *testing.T, repos authorization.Repositories, guard mutations.MutationGuard, model relationships.Schema, limits authmodel.EvaluationLimits) authorization.Components {
	t.Helper()
	comps, err := authorization.New(authorization.Repositories{
		Relationships: repos.Relationships, Roles: repos.Roles, Mutations: repos.Mutations,
	}, authorization.WithRelationshipModel(model), authorization.WithGuard(guard), authorization.WithLimits(limits))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return comps
}

func grantDashboardViewer(t *testing.T, svc authorization.Components, suffix, who, reader string) (*mutations.Result, error) {
	t.Helper()
	return svc.Mutations.GrantRelationship(context.Background(), mutations.Actor{PrincipalRef: authmodel.PrincipalRef{Type: "user", ID: who}}, mutations.GrantRelationshipCommand{
		ResourceType: "dashboard", ResourceID: "d" + suffix, Relation: "viewer", Subject: relationships.SubjectRef{Type: "user", ID: reader},
	})
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
// Through hops, and a userset reached through a container — and observes all
// navigated targets; a principal with no authority is denied and writes nothing.
func specGuardedPermissionThrough(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	ctx := context.Background()
	repos := newRepos(t)
	if repos.Relationships == nil {
		t.Skip("relationship kind not wired")
	}
	seedTenancy(t, repos.Mutations, "")
	cases := []struct {
		who     string
		allowed bool
	}{
		{"dash-owner", true}, {"space-manager", true}, {"tenant-owner", true}, {"group-admin", true}, {"stranger", false},
	}
	for i, tc := range cases {
		guard := &permissionGuard{permission: "manage"}
		svc := newPermissionService(t, repos, guard, permissionModel(), authmodel.EvaluationLimits{})
		want, err := svc.Decisions.Check(ctx, authmodel.CheckRequest{
			Principal: authmodel.PrincipalRef{Type: "user", ID: tc.who}, Permission: "manage", Resource: authmodel.Resource{Type: "dashboard", ID: "d"},
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
		if tc.allowed {
			if err != nil || rcpt == nil || rcpt.Outcome != mutations.OutcomeApplied {
				t.Fatalf("%s: inherited authority must apply: rcpt=%+v err=%v", tc.who, rcpt, err)
			}
			if !viewerRowExists(t, repos, "", reader) {
				t.Fatalf("%s: applied grant must leave its row", tc.who)
			}
		} else {
			if !errors.Is(err, sdk.ErrForbidden) || rcpt != nil {
				t.Fatalf("%s: want forbidden with no result, got rcpt=%+v err=%v", tc.who, rcpt, err)
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
func expansionModel() relationships.Schema {
	user := []relationships.SubjectTypeRef{{Type: "user"}}
	return relationships.NewSchema([]relationships.ResourceSchema{
		{Name: "group", Def: relationships.ResourceTypeDef{
			Relations: map[string]relationships.RelationDef{
				"owner":  {AllowedSubjects: user},
				"member": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}},
			},
		}},
		{Name: "folder", Def: relationships.ResourceTypeDef{
			Relations: map[string]relationships.RelationDef{
				"owner":  {AllowedSubjects: user},
				"viewer": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}},
			},
			Permissions: map[string]relationships.PermissionRule{"view": relationships.AnyOf(relationships.Direct("viewer"))},
		}},
		{Name: "doc", Def: relationships.ResourceTypeDef{
			Relations: map[string]relationships.RelationDef{
				"owner":  {AllowedSubjects: user},
				"folder": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "folder"}}},
				"viewer": {AllowedSubjects: user},
			},
			Permissions: map[string]relationships.PermissionRule{"view": relationships.AnyOf(relationships.Direct("viewer"), relationships.Through("folder", "view"))},
		}},
	})
}

// specGuardedPermissionExpansionParity: the guard's bounded expansion overflows
// at exactly the budget the read-side Check overflows at — through a container
// — and an over-budget guard is ErrEvaluationLimit that persists no row, no
// or result.
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
		mustApply(t, m, typedEdge(t, mutations.OpGrant, typedScope("group", g), "owner", userRef("seed-owner")))
	}
	mustApply(t, m, typedEdge(t, mutations.OpGrant, typedScope("group", "g1"), "member", userRef("alice")))
	mustApply(t, m, typedEdge(t, mutations.OpGrant, typedScope("group", "g2"), "member", relationships.SubjectRef{Type: "group", ID: "g1", Relation: "member"}))
	mustApply(t, m, typedEdge(t, mutations.OpGrant, typedScope("group", "g3"), "member", relationships.SubjectRef{Type: "group", ID: "g2", Relation: "member"}))
	mustApply(t, m, typedEdge(t, mutations.OpGrant, typedScope("folder", "f"), "owner", userRef("seed-owner")))
	mustApply(t, m, typedEdge(t, mutations.OpGrant, typedScope("folder", "f"), "viewer", relationships.SubjectRef{Type: "group", ID: "g3", Relation: "member"}))
	mustApply(t, m, typedEdge(t, mutations.OpGrant, typedScope("doc", "x"), "owner", userRef("seed-owner")))
	mustApply(t, m, typedEdge(t, mutations.OpGrant, typedScope("doc", "x"), "folder", relationships.SubjectRef{Type: "folder", ID: "f"}))

	for i, limit := range []int{4, 5, 6} {
		guard := &permissionGuard{permission: "view"}
		svc := newPermissionService(t, repos, guard, expansionModel(), authmodel.EvaluationLimits{MaxGraphStates: limit})
		want, wantErr := svc.Decisions.Check(ctx, authmodel.CheckRequest{
			Principal: authmodel.PrincipalRef{Type: "user", ID: "alice"}, Permission: "view", Resource: authmodel.Resource{Type: "doc", ID: "x"},
		})
		reader := "reader" + strconv.Itoa(i)
		rcpt, err := svc.Mutations.GrantRelationship(ctx, mutations.Actor{PrincipalRef: authmodel.PrincipalRef{Type: "user", ID: "alice"}}, mutations.GrantRelationshipCommand{
			ResourceType: "doc", ResourceID: "x", Relation: "viewer", Subject: relationships.SubjectRef{Type: "user", ID: reader},
		})
		if (wantErr == nil) != (guard.checkErr == nil) || !errors.Is(guard.checkErr, wantErr) || want.Allowed != guard.allowed {
			t.Fatalf("MaxGraphStates=%d: read-side (%v, %v) vs guard (%v, %v)", limit, want.Allowed, wantErr, guard.allowed, guard.checkErr)
		}
		switch limit {
		case 4:
			if !errors.Is(err, authmodel.ErrEvaluationLimit) || !errors.Is(err, sdk.ErrUnavailable) || rcpt != nil {
				t.Fatalf("MaxGraphStates=4 must be ErrEvaluationLimit (unavailable) with no result: rcpt=%+v err=%v", rcpt, err)
			}
			if ok, _ := repos.Relationships.CheckRelationExists(ctx, "doc", "x", "viewer", "user", reader); ok {
				t.Fatalf("an over-budget guarded write must persist no row")
			}
		default:
			if err != nil || rcpt == nil || rcpt.Outcome != mutations.OutcomeApplied || !guard.allowed {
				t.Fatalf("MaxGraphStates=%d must allow the 5-state expansion: rcpt=%+v err=%v allowed=%v", limit, rcpt, err, guard.allowed)
			}
		}
	}
}

// specGuardedPermissionThroughRevokeRaces: a guarded write whose authority is
// INHERITED (a parent edge; a folder-style userset grant) races a committed
// revoke of that inherited edge. The revoke always commits; the guarded write is
// applied (it serialized first — both commits are legitimate) or aborts cleanly
// as stale/denied with no row and no result — never a committed stale allow.
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
		revoke func(t *testing.T, suffix string) mutations.Command
	}
	races := []race{
		{"parent edge", "space-manager", func(t *testing.T, suffix string) mutations.Command {
			return typedEdge(t, mutations.OpRevoke, typedScope("dashboard", "d"+suffix), "space", relationships.SubjectRef{Type: "space", ID: "child" + suffix})
		}},
		{"inherited userset grant", "group-admin", func(t *testing.T, suffix string) mutations.Command {
			return typedEdge(t, mutations.OpRevoke, typedScope("group", "admins"+suffix), "member", userRef("group-admin"))
		}},
	}
	for _, rc := range races {
		t.Run(rc.name, func(t *testing.T) {
			for r := 0; r < rounds; r++ {
				suffix := "-" + rc.who + strconv.Itoa(r)
				seedTenancy(t, m, suffix)
				guard := &permissionGuard{permission: "manage"}
				svc := newPermissionService(t, repos, guard, permissionModel(), authmodel.EvaluationLimits{})
				revokeCmd := rc.revoke(t, suffix)

				var wg sync.WaitGroup
				var guardedRcpt, revokeRcpt *mutations.Result
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

				if revokeErr != nil || revokeRcpt.Outcome != mutations.OutcomeApplied {
					t.Fatalf("round %d: the revoke must commit: rcpt=%+v err=%v", r, revokeRcpt, revokeErr)
				}
				switch {
				case guardedErr == nil:
					if guardedRcpt == nil || guardedRcpt.Outcome != mutations.OutcomeApplied || !viewerRowExists(t, repos, suffix, "reader") {
						t.Fatalf("round %d: a nil-error guarded write must be applied with its row: %+v", r, guardedRcpt)
					}
				case errors.Is(guardedErr, sdk.ErrConflict) || errors.Is(guardedErr, sdk.ErrForbidden):
					if guardedRcpt != nil || viewerRowExists(t, repos, suffix, "reader") {
						t.Fatalf("round %d: a stale/denied guarded write must leave no result and no row: rcpt=%+v err=%v", r, guardedRcpt, guardedErr)
					}
				default:
					t.Fatalf("round %d: guarded write must be applied, stale, or denied; got rcpt=%+v err=%v", r, guardedRcpt, guardedErr)
				}
			}
		})
	}
}
