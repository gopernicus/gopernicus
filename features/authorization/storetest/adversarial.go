package storetest

import (
	"context"
	"testing"

	"github.com/gopernicus/gopernicus/features/authorization"
	"github.com/gopernicus/gopernicus/features/authorization/domain/relationship"
)

// fixtureSchema is the layer-(b) schema: doc.view = Direct(viewer); group carries
// `member` (nested-userset-capable) and `admin` relations; org carries `member`;
// platform carries `admin` for the platform-admin DATA tuple (now a host recipe,
// not an engine bypass — see PlatformAdminIsNotMagic). No Through rules — the
// adversarial cases exercise store-side EXACT userset expansion, which the engine
// drives via CheckRelationWithGroupExpansion. doc.viewer deliberately allows the
// concrete `group`, the `group#member` and `group#admin` usersets, and the
// `org#member` userset as DISTINCT allowed subjects, so the exact-pair validator
// and relation-aware expansion are both exercised. The only permission is "view";
// subjects are user/service_account/group; resources are doc/group/org/platform.
func fixtureSchema() authorization.Schema {
	return authorization.NewSchema([]authorization.ResourceSchema{
		{Name: "group", Def: authorization.ResourceTypeDef{
			Relations: map[string]authorization.RelationDef{
				"member": {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}},
				"admin":  {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}}},
			},
		}},
		{Name: "org", Def: authorization.ResourceTypeDef{
			Relations: map[string]authorization.RelationDef{
				"member": {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "group", Relation: "member"}}},
			},
		}},
		{Name: "doc", Def: authorization.ResourceTypeDef{
			Relations: map[string]authorization.RelationDef{
				"viewer": {AllowedSubjects: []authorization.SubjectTypeRef{
					{Type: "user"},
					{Type: "group"},
					{Type: "group", Relation: "member"},
					{Type: "group", Relation: "admin"},
					{Type: "org", Relation: "member"},
				}},
			},
			Permissions: map[string]authorization.PermissionRule{
				"view": authorization.AnyOf(authorization.Direct("viewer")),
			},
		}},
		{Name: "platform", Def: authorization.ResourceTypeDef{
			Relations: map[string]authorization.RelationDef{
				"admin": {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}, {Type: "service_account"}}},
			},
		}},
	})
}

// newServiceFor builds the feature Service over the stores under test, supplying
// the fixture Model only when the relationship kind is wired (so a roles-only
// backend still constructs).
func newServiceFor(t *testing.T, repos authorization.Repositories) *authorization.Service {
	t.Helper()
	cfg := authorization.Config{}
	if repos.Relationships != nil {
		cfg.Model = fixtureSchema()
	}
	comps, err := authorization.NewService(repos, cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return comps.Service
}

func ctUserset(rt, rid, relation, st, sid, subjRel string) relationship.CreateRelationship {
	c := ct(rt, rid, relation, st, sid)
	c.SubjectRelation = subjRel
	return c
}

func mustView(t *testing.T, svc *authorization.Service, subjectType, subjectID, docID string, want bool) {
	t.Helper()
	res, err := svc.Check(context.Background(), authorization.CheckRequest{
		Principal:  authorization.PrincipalRef{Type: subjectType, ID: subjectID},
		Permission: "view",
		Resource:   authorization.Resource{Type: "doc", ID: docID},
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res.Allowed != want {
		t.Fatalf("view(%s:%s, doc:%s) = %v, want %v (reason %q)", subjectType, subjectID, docID, res.Allowed, want, res.Reason)
	}
}

// runAdversarial is layer (b): engine/service outcomes over the stores under test.
func runAdversarial(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	ctx := context.Background()

	t.Run("MembershipCycle", func(t *testing.T) {
		repos := newRepos(t)
		mustCreate(t, repos.Relationships,
			ctUserset("group", "a", "member", "group", "b", "member"),
			ctUserset("group", "b", "member", "group", "a", "member"), // A#member∋B#member, B#member∋A#member
			ct("group", "b", "member", "user", "u1"),
			ctUserset("doc", "d1", "viewer", "group", "a", "member"),
		)
		svc := newServiceFor(t, repos)
		mustView(t, svc, "user", "u1", "d1", true)  // resolves through the cycle
		mustView(t, svc, "user", "u2", "d1", false) // outside the cycle
	})

	t.Run("DeepNesting", func(t *testing.T) {
		repos := newRepos(t)
		mustCreate(t, repos.Relationships,
			ctUserset("group", "g1", "member", "group", "g2", "member"),
			ctUserset("group", "g2", "member", "group", "g3", "member"),
			ct("group", "g3", "member", "user", "u1"), // u1 → g3#member → g2#member → g1#member
			ctUserset("doc", "d1", "viewer", "group", "g1", "member"),
		)
		svc := newServiceFor(t, repos)
		mustView(t, svc, "user", "u1", "d1", true)
	})

	t.Run("DiamondDedup", func(t *testing.T) {
		repos := newRepos(t)
		// u1 ∈ gl#member and gr#member; both ∈ gtop#member; doc viewer = gtop#member
		// (two paths to gtop#member).
		mustCreate(t, repos.Relationships,
			ct("group", "gl", "member", "user", "u1"),
			ct("group", "gr", "member", "user", "u1"),
			ctUserset("group", "gtop", "member", "group", "gl", "member"),
			ctUserset("group", "gtop", "member", "group", "gr", "member"),
			ctUserset("doc", "d1", "viewer", "group", "gtop", "member"),
		)
		svc := newServiceFor(t, repos)
		mustView(t, svc, "user", "u1", "d1", true)
		// Multiple expansion paths must NOT inflate the DIRECT count (§2.5).
		if n, _ := repos.Relationships.CountByResourceAndRelation(ctx, "doc", "d1", "viewer"); n != 1 {
			t.Fatalf("direct viewer count must be 1 despite diamond membership, got %d", n)
		}
	})

	t.Run("NestedUserset", func(t *testing.T) {
		repos := newRepos(t)
		// Tuple-side userset: org:acme#member@group:eng#member (subject_relation set)
		// and doc:d1#viewer@org:acme#member. The check carries NO subject relation;
		// the exact userset chain resolves via stored tuples + expansion.
		mustCreate(t, repos.Relationships,
			ct("group", "eng", "member", "user", "u1"),
			ctUserset("org", "acme", "member", "group", "eng", "member"),
			ctUserset("doc", "d1", "viewer", "org", "acme", "member"),
		)
		svc := newServiceFor(t, repos)
		mustView(t, svc, "user", "u1", "d1", true)  // u1 → eng#member → acme#member → viewer
		mustView(t, svc, "user", "u2", "d1", false) // not a member
	})

	t.Run("NestedMixedUsersetsRelationAware", func(t *testing.T) {
		repos := newRepos(t)
		// A nested MEMBER chain grants d1; an ADMIN userset grants d2. u1 is a
		// nested member (not admin); u2 is a direct group admin (not member). The
		// two usersets must not cross-satisfy.
		mustCreate(t, repos.Relationships,
			ct("group", "inner", "member", "user", "u1"),
			ctUserset("group", "outer", "member", "group", "inner", "member"),
			ctUserset("doc", "d1", "viewer", "group", "outer", "member"),
			ct("group", "outer", "admin", "user", "u2"),
			ctUserset("doc", "d2", "viewer", "group", "outer", "admin"),
		)
		svc := newServiceFor(t, repos)
		mustView(t, svc, "user", "u1", "d1", true)  // nested member chain
		mustView(t, svc, "user", "u1", "d2", false) // u1 is not an admin
		mustView(t, svc, "user", "u2", "d2", true)  // direct admin userset
		mustView(t, svc, "user", "u2", "d1", false) // u2 is not a member
	})

	t.Run("MemberAdminUsersetSeparation", func(t *testing.T) {
		repos := newRepos(t)
		// One group g with a member (u_m) and an admin (u_a). doc dM is granted to
		// g#member, doc dA to g#admin. Neither userset satisfies the other, and a
		// broader/other relation never satisfies a narrower one.
		mustCreate(t, repos.Relationships,
			ct("group", "g", "member", "user", "u_m"),
			ct("group", "g", "admin", "user", "u_a"),
			ctUserset("doc", "dM", "viewer", "group", "g", "member"),
			ctUserset("doc", "dA", "viewer", "group", "g", "admin"),
		)
		svc := newServiceFor(t, repos)
		mustView(t, svc, "user", "u_m", "dM", true)  // member → member grant
		mustView(t, svc, "user", "u_m", "dA", false) // member ≠ admin grant
		mustView(t, svc, "user", "u_a", "dA", true)  // admin → admin grant
		mustView(t, svc, "user", "u_a", "dM", false) // admin ≠ member grant
	})

	t.Run("CyclePerRelationIsRelationAware", func(t *testing.T) {
		repos := newRepos(t)
		// A member-userset cycle A#member↔B#member with u1 ∈ B#member. d1 is granted
		// to A#member, d2 to A#admin. The member cycle must reach A#member but never
		// leak into A#admin, so u1 sees d1 but not d2.
		mustCreate(t, repos.Relationships,
			ctUserset("group", "a", "member", "group", "b", "member"),
			ctUserset("group", "b", "member", "group", "a", "member"),
			ct("group", "b", "member", "user", "u1"),
			ctUserset("doc", "d1", "viewer", "group", "a", "member"),
			ctUserset("doc", "d2", "viewer", "group", "a", "admin"),
		)
		svc := newServiceFor(t, repos)
		mustView(t, svc, "user", "u1", "d1", true)  // through the member cycle
		mustView(t, svc, "user", "u1", "d2", false) // member cycle never yields A#admin
	})

	t.Run("RelationAwareConcreteGroupGrant", func(t *testing.T) {
		repos := newRepos(t)
		// A CONCRETE group grant (subject_relation empty) names the group ENTITY,
		// not its members: only the principal that IS group:g satisfies it. A member
		// of g does not.
		mustCreate(t, repos.Relationships,
			ct("group", "g", "member", "user", "u1"),
			ct("doc", "dc", "viewer", "group", "g"), // concrete group:g, not group:g#member
		)
		svc := newServiceFor(t, repos)
		mustView(t, svc, "group", "g", "dc", true)  // the concrete group entity itself
		mustView(t, svc, "user", "u1", "dc", false) // a member is NOT the concrete group
	})

	t.Run("MissingUsersetRelationRejected", func(t *testing.T) {
		repos := newRepos(t)
		svc := newServiceFor(t, repos)
		// org.member allows ONLY group#member. A concrete group and a group#admin
		// are both rejected by the exact-pair validator; group#member is accepted.
		// The schema validator is exercised directly (ValidateRelationships) — the raw
		// write path was removed from Service (AZ3-3.4), and the store port does not
		// validate against the schema.
		if err := svc.ValidateRelationships([]relationship.CreateRelationship{
			ct("org", "acme", "member", "group", "eng"), // concrete group — missing #member
		}); err == nil {
			t.Fatalf("concrete group must be rejected where only group#member is allowed")
		}
		if err := svc.ValidateRelationships([]relationship.CreateRelationship{
			ctUserset("org", "acme", "member", "group", "eng", "admin"), // group#admin ≠ group#member
		}); err == nil {
			t.Fatalf("group#admin must not satisfy a group#member requirement")
		}
		if err := svc.ValidateRelationships([]relationship.CreateRelationship{
			ctUserset("org", "acme", "member", "group", "eng", "member"), // exact pair — accepted
		}); err != nil {
			t.Fatalf("group#member must be accepted for org.member: %v", err)
		}
	})

	t.Run("PlatformAdminIsNotMagic", func(t *testing.T) {
		for _, subjType := range []string{"user", "service_account"} {
			repos := newRepos(t)
			mustCreate(t, repos.Relationships,
				ct("platform", "main", "admin", subjType, "admin1"),
				ct("doc", "d1", "viewer", "user", "u9"), // a non-admin's grant
			)
			svc := newServiceFor(t, repos)

			// A platform-admin tuple is NOT an engine bypass: the admin is
			// enumerated only for docs it holds real grants on (none here).
			res, err := svc.LookupResources(ctx, authorization.PrincipalRef{Type: subjType, ID: "admin1"}, "view", "doc")
			if err != nil {
				t.Fatalf("%s admin LookupResources: %v", subjType, err)
			}
			if res.IDs == nil {
				t.Fatalf("%s admin IDs must be non-nil", subjType)
			}
			if len(res.IDs) != 0 {
				t.Fatalf("%s admin has no doc grant → want empty ids, got %v", subjType, res.IDs)
			}
			// The non-admin owner of d1 is enumerated for exactly that doc.
			nonAdmin, err := svc.LookupResources(ctx, authorization.PrincipalRef{Type: "user", ID: "u9"}, "view", "doc")
			if err != nil {
				t.Fatalf("non-admin LookupResources: %v", err)
			}
			if len(nonAdmin.IDs) != 1 || nonAdmin.IDs[0] != "d1" {
				t.Fatalf("u9 must see exactly [d1], got %v", nonAdmin.IDs)
			}
			// And the admin tuple does NOT satisfy a schema Check on d1.
			mustView(t, svc, subjType, "admin1", "d1", false)
		}
	})
}
