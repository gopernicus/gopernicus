package storetest

import (
	"slices"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
)

// RunLookupSnapshots commits a write after reverse discovery and before forward
// verification. SQL factories use separate connections for that write while the
// lookup's read transaction remains open; no sleeps or probabilistic race needed.
func RunLookupSnapshots(t *testing.T, factory func(*testing.T) Repositories) {
	t.Helper()
	for _, resourceType := range []string{"space", "document"} {
		for _, page := range []bool{false, true} {
			for _, change := range []string{"revoke membership", "unrelated relationship", "unrelated role"} {
				name := resourceType + "/all/" + change
				if page {
					name = resourceType + "/page/" + change
				}
				t.Run(name, func(t *testing.T) {
					repos := factory(t)
					ctx := t.Context()
					rows := []relationships.CreateRelationship{
						{ResourceType: "group", ResourceID: "owners", Relation: "member", SubjectType: "user", SubjectID: "alice"},
					}
					for _, id := range []string{"a", "b"} {
						rows = append(rows,
							relationships.CreateRelationship{ResourceType: "space", ResourceID: id, Relation: "viewer", SubjectType: "group", SubjectID: "owners", SubjectRelation: "member"},
							relationships.CreateRelationship{ResourceType: "document", ResourceID: id, Relation: "parent", SubjectType: "space", SubjectID: id})
					}
					if err := repos.Relationships.CreateRelationships(ctx, rows); err != nil {
						t.Fatal(err)
					}
					writer := repos.Relationships
					changed := false
					repos.Tuples = interleavedTupleStore{Storer: repos.Tuples, after: func() error {
						if changed {
							return nil
						}
						changed = true
						switch change {
						case "revoke membership":
							return writer.DeleteRelationshipTarget(ctx, "group", "owners", "member", relationships.SubjectRef{Type: "user", ID: "alice"})
						case "unrelated relationship":
							return writer.CreateRelationships(ctx, []relationships.CreateRelationship{{ResourceType: "space", ResourceID: "elsewhere", Relation: "viewer", SubjectType: "user", SubjectID: "bob"}})
						default:
							return assignRole(ctx, repos.Tuples, roles.Assignment{SubjectType: "user", SubjectID: "bob", Role: "admin", Scope: fixtureScope("", "")})
						}
					}}

					// Each discovered resource gets its own verification batch; a
					// paged call also verifies the withheld lookahead in that snapshot.
					components, err := authorization.New(repos.Repositories, authorization.WithModel(lookupSnapshotSchema()), authorization.WithLimits(authmodel.EvaluationLimits{MaxBatchSize: 1}))
					if err != nil {
						t.Fatal(err)
					}
					lookup := func() ([]string, bool, error) {
						principal := authmodel.PrincipalRef{Type: "user", ID: "alice"}
						if page {
							result, err := components.Decisions.LookupResourceIDPage(ctx, decisions.ResourceIDPageRequest{Principal: principal, Permission: "view", ResourceType: resourceType, Limit: 1})
							return result.IDs, result.HasMore, err
						}
						result, err := components.Decisions.LookupAllResourceIDs(ctx, principal, "view", resourceType)
						return result.IDs, false, err
					}
					want := []string{"a", "b"}
					if page {
						want = want[:1]
					}
					got, more, err := lookup()
					if err != nil || !changed || !slices.Equal(got, want) || more != page {
						t.Fatalf("lookup mixed states: IDs=%v more=%v changed=%v err=%v; want %v/%v", got, more, changed, err, want, page)
					}
					got, more, err = lookup()
					if change == "revoke membership" {
						if err != nil || got == nil || len(got) != 0 || more {
							t.Fatalf("next lookup retained revoked membership: %v/%v/%v", got, more, err)
						}
					} else if err != nil || !slices.Equal(got, want) || more != page {
						t.Fatalf("unrelated write affected lookup: %v/%v/%v", got, more, err)
					}
				})
			}
		}
	}
}

func lookupSnapshotSchema() decisions.Model {
	return decisions.Model{ResourceTypes: map[string]decisions.ResourceTypeDef{
		"group":    {Relations: map[string]decisions.RelationDef{"member": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "user"}}}}},
		"space":    {Relations: map[string]decisions.RelationDef{"viewer": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}}}, Permissions: map[string]decisions.Expression{"view": decisions.AnyOf(decisions.Direct("viewer"))}},
		"document": {Relations: map[string]decisions.RelationDef{"parent": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "space"}}}}, Permissions: map[string]decisions.Expression{"view": decisions.AnyOf(decisions.Through("parent", "view"))}},
	}}
}
