package storetest

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/gopernicus/gopernicus/features/authorization"
)

// oracleSchema is the layer-(b) Check/Lookup parity schema. It exercises every
// enumeration shape the AZ3-1.4 oracle must prove complete in ONE universe:
//
//   - direct grants (doc.viewer = user);
//   - group/userset grants (doc.viewer = group#member, nested + cycle capable);
//   - a non-self Through (doc inherits view from its org);
//   - a same-permission self-referential hierarchy (doc→parent→doc);
//   - mixed Direct/Through on one permission;
//   - two Through relations to one target permission (org AND parent → view).
//
// doc.view is the rich permission; org.view is a plain Direct so the non-self
// Through has a real target permission to resolve.
func oracleSchema() authorization.Schema {
	return authorization.NewSchema([]authorization.ResourceSchema{
		{Name: "group", Def: authorization.ResourceTypeDef{
			Relations: map[string]authorization.RelationDef{
				"member": {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}},
			},
		}},
		{Name: "org", Def: authorization.ResourceTypeDef{
			Relations: map[string]authorization.RelationDef{
				"admin": {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}}},
			},
			Permissions: map[string]authorization.PermissionRule{
				"view": authorization.AnyOf(authorization.Direct("admin")),
			},
		}},
		{Name: "doc", Def: authorization.ResourceTypeDef{
			Relations: map[string]authorization.RelationDef{
				"viewer": {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}},
				"parent": {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "doc"}}},
				"org":    {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "org"}}},
			},
			Permissions: map[string]authorization.PermissionRule{
				"view": authorization.AnyOf(
					authorization.Direct("viewer"),
					authorization.Through("org", "view"),
					authorization.Through("parent", "view"),
				),
			},
		}},
	})
}

// oracleFixture is the finite tuple universe the oracle sweeps. Every doc/org ID
// created here is enumerated in oracleUniverse so the completeness sweep is over
// a closed set.
func oracleFixture() []authorization.CreateRelationship {
	return []authorization.CreateRelationship{
		// Direct viewer.
		ctUserset("doc", "d1", "viewer", "user", "u_direct", ""),

		// Nested group/userset: u_group → g_leaf#member → g_top#member; d2 viewer g_top#member.
		ct("group", "g_leaf", "member", "user", "u_group"),
		ctUserset("group", "g_top", "member", "group", "g_leaf", "member"),
		ctUserset("doc", "d2", "viewer", "group", "g_top", "member"),

		// Non-self Through: u_org admin of org o1; d3 inherits view from o1.
		ct("org", "o1", "admin", "user", "u_org"),
		ct("doc", "d3", "org", "org", "o1"),

		// Self-hierarchy from a DIRECT root: d_root viewer u_hier; d_mid←d_root; d_leaf←d_mid.
		ct("doc", "d_root", "viewer", "user", "u_hier"),
		ct("doc", "d_mid", "parent", "doc", "d_root"),
		ct("doc", "d_leaf", "parent", "doc", "d_mid"),

		// D1(c): self-hierarchy whose root is reachable ONLY via a non-self Through.
		// u_hc admins org o2; d_hroot inherits view from o2; d_hmid←d_hroot; d_hleaf←d_hmid.
		ct("org", "o2", "admin", "user", "u_hc"),
		ct("doc", "d_hroot", "org", "org", "o2"),
		ct("doc", "d_hmid", "parent", "doc", "d_hroot"),
		ct("doc", "d_hleaf", "parent", "doc", "d_hmid"),

		// Diamond: u_dia reaches gtop#member by two membership paths; d_dia viewer gtop#member.
		ct("group", "gl", "member", "user", "u_dia"),
		ct("group", "gr", "member", "user", "u_dia"),
		ctUserset("group", "gtop", "member", "group", "gl", "member"),
		ctUserset("group", "gtop", "member", "group", "gr", "member"),
		ctUserset("doc", "d_dia", "viewer", "group", "gtop", "member"),

		// Membership cycle: gca#member ↔ gcb#member; u_cyc ∈ gcb#member; d_cyc viewer gca#member.
		ctUserset("group", "gca", "member", "group", "gcb", "member"),
		ctUserset("group", "gcb", "member", "group", "gca", "member"),
		ct("group", "gcb", "member", "user", "u_cyc"),
		ctUserset("doc", "d_cyc", "viewer", "group", "gca", "member"),

		// Mixed paths to one doc: u_multi is a direct viewer of d_both AND admin of
		// org o3 which grants d_both — the ID must appear exactly once.
		ctUserset("doc", "d_both", "viewer", "user", "u_multi", ""),
		ct("org", "o3", "admin", "user", "u_multi"),
		ct("doc", "d_both", "org", "org", "o3"),
	}
}

// oracleUniverse is the closed set of resource IDs per type that the completeness
// sweep ranges over. d_none is a doc no principal can access (the negative case).
func oracleUniverse() map[string][]string {
	return map[string][]string{
		"doc": {"d1", "d2", "d3", "d_root", "d_mid", "d_leaf", "d_hroot", "d_hmid", "d_hleaf", "d_dia", "d_cyc", "d_both", "d_none"},
		"org": {"o1", "o2", "o3"},
	}
}

// oraclePrincipals is every concrete decision caller the sweep runs. Each is a
// distinct enumeration shape; u_none proves the empty case is empty, not magic.
func oraclePrincipals() []authorization.PrincipalRef {
	return []authorization.PrincipalRef{
		{Type: "user", ID: "u_direct"},
		{Type: "user", ID: "u_group"},
		{Type: "user", ID: "u_org"},
		{Type: "user", ID: "u_hier"},
		{Type: "user", ID: "u_hc"},
		{Type: "user", ID: "u_dia"},
		{Type: "user", ID: "u_cyc"},
		{Type: "user", ID: "u_multi"},
		{Type: "user", ID: "u_none"},
	}
}

// generousLimits sizes every budget dimension well above the fixture so the
// oracle's completeness sweep is never masked by exhaustion — exhaustion is
// asserted separately (LimitExhaustionIsError).
func generousLimits() authorization.EvaluationLimits {
	return authorization.EvaluationLimits{
		MaxThroughDepth:    50,
		MaxGraphStates:     10000,
		MaxRelationTargets: 1000,
		MaxBatchSize:       1000,
		MaxLookupResults:   10000,
	}
}

// runParity is the generic Check/Lookup conformance oracle (AZ3-1.4). For a
// finite fixture universe it proves the two directions of the standing
// invariant — every resource Check ALLOWS is discoverable by LookupResources
// (completeness), and every ID LookupResources returns PASSES Check (soundness)
// — on whichever store dialect is under test. It also proves LookupResources
// output is sorted with each ID exactly once, and that limit exhaustion is an
// error rather than a truncated-complete list.
func runParity(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	ctx := context.Background()

	t.Run("CheckLookupOracle", func(t *testing.T) {
		repos := newRepos(t)
		mustCreate(t, repos.Relationships, oracleFixture()...)
		svc := newOracleService(t, repos, generousLimits())

		universe := oracleUniverse()
		for _, principal := range oraclePrincipals() {
			for _, resourceType := range []string{"doc", "org"} {
				assertCheckLookupParity(t, ctx, svc, principal, "view", resourceType, universe[resourceType])
			}
		}
	})

	t.Run("D1cOrgSeededDescendants", func(t *testing.T) {
		// The pointed D1(c) closure: a self-hierarchy root reachable ONLY via a
		// non-self Through still expands its descendants in LookupResources.
		repos := newRepos(t)
		mustCreate(t, repos.Relationships, oracleFixture()...)
		svc := newOracleService(t, repos, generousLimits())

		got, err := svc.LookupResources(ctx, authorization.PrincipalRef{Type: "user", ID: "u_hc"}, "view", "doc")
		if err != nil {
			t.Fatalf("LookupResources: %v", err)
		}
		if want := []string{"d_hleaf", "d_hmid", "d_hroot"}; !idsEqual(got.IDs, want) {
			t.Fatalf("D1(c): org-seeded root must expand descendants: want %v, got %v", want, got.IDs)
		}
	})

	t.Run("LimitExhaustionIsError", func(t *testing.T) {
		// u_hc can view exactly 3 docs (d_hroot + descendants). With
		// MaxLookupResults=2 that is over budget: the result is ErrEvaluationLimit,
		// NEVER a truncated 2-element "complete" list. A generous budget returns all 3.
		repos := newRepos(t)
		mustCreate(t, repos.Relationships, oracleFixture()...)

		tight := newOracleService(t, repos, authorization.EvaluationLimits{MaxLookupResults: 2})
		if _, err := tight.LookupResources(ctx, authorization.PrincipalRef{Type: "user", ID: "u_hc"}, "view", "doc"); !errors.Is(err, authorization.ErrEvaluationLimit) {
			t.Fatalf("3 accessible docs over MaxLookupResults=2: want ErrEvaluationLimit, got %v", err)
		}

		reposOK := newRepos(t)
		mustCreate(t, reposOK.Relationships, oracleFixture()...)
		ok := newOracleService(t, reposOK, generousLimits())
		res, err := ok.LookupResources(ctx, authorization.PrincipalRef{Type: "user", ID: "u_hc"}, "view", "doc")
		if err != nil {
			t.Fatalf("generous budget: %v", err)
		}
		if len(res.IDs) != 3 {
			t.Fatalf("generous budget must return all 3 accessible docs, got %v", res.IDs)
		}
	})
}

// assertCheckLookupParity is the oracle core: it sweeps the finite universe with
// Check to build the allowed set, then asserts LookupResources returns exactly
// that set — sorted, each ID once — with both directions independently checked
// against Check so neither a missed grant nor a spurious ID can pass.
func assertCheckLookupParity(t *testing.T, ctx context.Context, svc *authorization.Service, principal authorization.PrincipalRef, permission, resourceType string, universe []string) {
	t.Helper()

	allowed := make(map[string]bool)
	for _, id := range universe {
		res, err := svc.Check(ctx, authorization.CheckRequest{
			Principal:  principal,
			Permission: permission,
			Resource:   authorization.Resource{Type: resourceType, ID: id},
		})
		if err != nil {
			t.Fatalf("Check(%s:%s, %s, %s:%s): %v", principal.Type, principal.ID, permission, resourceType, id, err)
		}
		if res.Allowed {
			allowed[id] = true
		}
	}

	look, err := svc.LookupResources(ctx, principal, permission, resourceType)
	if err != nil {
		t.Fatalf("LookupResources(%s:%s, %s, %s): %v", principal.Type, principal.ID, permission, resourceType, err)
	}
	if look.IDs == nil {
		t.Fatalf("LookupResources IDs must be non-nil (%s:%s, %s)", principal.Type, principal.ID, resourceType)
	}
	if !sort.StringsAreSorted(look.IDs) {
		t.Fatalf("LookupResources must be sorted, got %v (%s:%s, %s)", look.IDs, principal.Type, principal.ID, resourceType)
	}

	lookSet := make(map[string]bool, len(look.IDs))
	for _, id := range look.IDs {
		if lookSet[id] {
			t.Fatalf("LookupResources returned %s more than once (%s:%s, %s): %v", id, principal.Type, principal.ID, resourceType, look.IDs)
		}
		lookSet[id] = true
	}

	// Completeness: every Check-allowed resource in the universe is discoverable.
	for id := range allowed {
		if !lookSet[id] {
			t.Fatalf("Check allows %s:%s on %s:%s but LookupResources omits it: %v", principal.Type, principal.ID, resourceType, id, look.IDs)
		}
	}

	// Soundness: every looked-up ID independently passes Check (covers IDs outside
	// the declared universe too — Lookup may never invent access).
	for id := range lookSet {
		res, err := svc.Check(ctx, authorization.CheckRequest{
			Principal:  principal,
			Permission: permission,
			Resource:   authorization.Resource{Type: resourceType, ID: id},
		})
		if err != nil {
			t.Fatalf("Check(lookup id %s): %v", id, err)
		}
		if !res.Allowed {
			t.Fatalf("LookupResources returned %s:%s on %s:%s but Check denies it (%s)", principal.Type, principal.ID, resourceType, id, res.Reason)
		}
	}
}

func newOracleService(t *testing.T, repos authorization.Repositories, limits authorization.EvaluationLimits) *authorization.Service {
	t.Helper()
	comps, err := authorization.NewService(repos, authorization.Config{Model: oracleSchema(), Limits: limits})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return comps.Service
}
