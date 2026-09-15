package storetest

import (
	"context"
	"slices"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
)

type interleavedLookupStore struct {
	relationships.Storer
	after func() error
}

func (s interleavedLookupStore) ForModel(model relationships.ReadModel) relationships.Reader {
	return interleavedLookupReader{Reader: s.Storer.ForModel(model), after: s.after}
}

type interleavedLookupReader struct {
	relationships.Reader
	after func() error
}

func (r interleavedLookupReader) LookupResourceIDs(ctx context.Context, rt string, relations []string, st, sid, after string, limit int) ([]string, error) {
	ids, err := r.Reader.LookupResourceIDs(ctx, rt, relations, st, sid, after, limit)
	if err == nil {
		err = r.after()
	}
	return ids, err
}

func (r interleavedLookupReader) ReadLookupSnapshot(ctx context.Context, fn func(context.Context, relationships.Reader) error) error {
	if source, ok := r.Reader.(relationships.LookupSnapshotter); ok {
		return source.ReadLookupSnapshot(ctx, func(ctx context.Context, reader relationships.Reader) error {
			return fn(ctx, interleavedLookupReader{Reader: reader, after: r.after})
		})
	}
	return fn(ctx, r)
}

// RunLookupSnapshots commits a write after reverse discovery and before forward
// verification. SQL factories use separate connections for that write while the
// lookup's read transaction remains open; no sleeps or probabilistic race needed.
func RunLookupSnapshots(t *testing.T, factory func(*testing.T) authorization.Repositories) {
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
					repos.Relationships = interleavedLookupStore{Storer: writer, after: func() error {
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
							return repos.Roles.Assign(ctx, roles.Assignment{SubjectType: "user", SubjectID: "bob", Role: "admin"})
						}
					}}

					// Each discovered resource gets its own verification batch; a
					// paged call also verifies the withheld lookahead in that snapshot.
					components, err := authorization.New(repos, authorization.WithRelationshipModel(lookupSnapshotSchema()), authorization.WithLimits(authmodel.EvaluationLimits{MaxBatchSize: 1}))
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

func lookupSnapshotSchema() relationships.Schema {
	return relationships.Schema{ResourceTypes: map[string]relationships.ResourceTypeDef{
		"group":    {Relations: map[string]relationships.RelationDef{"member": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}}}}},
		"space":    {Relations: map[string]relationships.RelationDef{"viewer": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}}}, Permissions: map[string]relationships.PermissionRule{"view": relationships.AnyOf(relationships.Direct("viewer"))}},
		"document": {Relations: map[string]relationships.RelationDef{"parent": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "space"}}}}, Permissions: map[string]relationships.PermissionRule{"view": relationships.AnyOf(relationships.Through("parent", "view"))}},
	}}
}
