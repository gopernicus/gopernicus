package storetest

import (
	"context"
	"testing"

	"github.com/gopernicus/gopernicus/features/authorization"
	"github.com/gopernicus/gopernicus/features/authorization/domain/relationship"
)

// fixtureSchema is the layer-(b) schema: doc.view = Direct(viewer); group and org
// carry a `member` relation for group expansion; platform carries `admin` for the
// platform-admin DATA tuple (now a host recipe, not an engine bypass — see
// PlatformAdminIsNotMagic). No Through rules — the adversarial cases exercise
// store-side group expansion, which the engine drives via
// CheckRelationWithGroupExpansion. The only permission is "view"; subjects are
// user/service_account/group; resources are doc/group/org/platform.
func fixtureSchema() authorization.Schema {
	return authorization.NewSchema([]authorization.ResourceSchema{
		{Name: "group", Def: authorization.ResourceTypeDef{
			Relations: map[string]authorization.RelationDef{
				"member": {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}},
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
					{Type: "user"}, {Type: "group", Relation: "member"}, {Type: "org", Relation: "member"},
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
	svc, err := authorization.NewService(repos, cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

func ctUserset(rt, rid, relation, st, sid, subjRel string) relationship.CreateRelationship {
	c := ct(rt, rid, relation, st, sid)
	c.SubjectRelation = &subjRel
	return c
}

func mustView(t *testing.T, svc *authorization.Service, subjectType, subjectID, docID string, want bool) {
	t.Helper()
	res, err := svc.Check(context.Background(), authorization.CheckRequest{
		Subject:    authorization.Subject{Type: subjectType, ID: subjectID},
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
			ct("group", "a", "member", "group", "b"),
			ct("group", "b", "member", "group", "a"), // A∈B, B∈A
			ct("group", "b", "member", "user", "u1"),
			ct("doc", "d1", "viewer", "group", "a"),
		)
		svc := newServiceFor(t, repos)
		mustView(t, svc, "user", "u1", "d1", true)  // resolves through the cycle
		mustView(t, svc, "user", "u2", "d1", false) // outside the cycle
	})

	t.Run("DeepNesting", func(t *testing.T) {
		repos := newRepos(t)
		mustCreate(t, repos.Relationships,
			ct("group", "g1", "member", "group", "g2"),
			ct("group", "g2", "member", "group", "g3"),
			ct("group", "g3", "member", "user", "u1"), // u1 → g3 → g2 → g1
			ct("doc", "d1", "viewer", "group", "g1"),
		)
		svc := newServiceFor(t, repos)
		mustView(t, svc, "user", "u1", "d1", true)
	})

	t.Run("DiamondDedup", func(t *testing.T) {
		repos := newRepos(t)
		// u1 ∈ gl and gr; both ∈ gtop; doc viewer = gtop (two paths to gtop).
		mustCreate(t, repos.Relationships,
			ct("group", "gl", "member", "user", "u1"),
			ct("group", "gr", "member", "user", "u1"),
			ct("group", "gtop", "member", "group", "gl"),
			ct("group", "gtop", "member", "group", "gr"),
			ct("doc", "d1", "viewer", "group", "gtop"),
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
		// Tuple-side userset: org:acme#member@group:eng#member (subject_relation set).
		// The check carries NO subject relation; the userset resolves via stored
		// tuples + expansion.
		mustCreate(t, repos.Relationships,
			ct("group", "eng", "member", "user", "u1"),
			ctUserset("org", "acme", "member", "group", "eng", "member"),
			ct("doc", "d1", "viewer", "org", "acme"),
		)
		svc := newServiceFor(t, repos)
		mustView(t, svc, "user", "u1", "d1", true)  // u1 → eng → acme → viewer
		mustView(t, svc, "user", "u2", "d1", false) // not a member
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
			res, err := svc.LookupResources(ctx, authorization.Subject{Type: subjType, ID: "admin1"}, "view", "doc")
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
			nonAdmin, err := svc.LookupResources(ctx, authorization.Subject{Type: "user", ID: "u9"}, "view", "doc")
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
