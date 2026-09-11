//go:build integration

// The ambient-refusal table (ruling R1), shared by the EMULATOR entrypoint
// (conformance_test.go, integration && !live) and the LIVE one
// (conformance_live_test.go, integration && live) — one build tag, both builds.
// Duplicating a twenty-seven-method table per leg is how one of the two copies
// silently loses a method.
package firestore

import (
	"context"
	"errors"
	"testing"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// assertAmbientRefusal drives every public port method inside BOTH kinds of
// ambient transaction — a read-write Transact and a read-only ReadSnapshot — and
// requires each one to answer ErrAmbientTransactionUnsupported. It is the
// executable form of R1's "refuse rather than split": a store that ran on the
// client beside the host's transaction would silently break the host's atomic
// unit, so silence is the failure and an error is the contract.
func assertAmbientRefusal(t *testing.T, db *firestoredb.DB, repos authorization.Repositories) {
	t.Helper()

	for _, ambient := range []struct {
		name string
		run  func(context.Context, func(context.Context)) error
	}{
		{
			name: "Transact",
			run: func(ctx context.Context, call func(context.Context)) error {
				return db.Transact(ctx, func(ctx context.Context) error {
					call(ctx)
					return nil
				})
			},
		},
		{
			name: "ReadSnapshot",
			run: func(ctx context.Context, call func(context.Context)) error {
				return db.ReadSnapshot(ctx, func(ctx context.Context, _ firestoredb.Reader) error {
					call(ctx)
					return nil
				})
			},
		},
	} {
		t.Run(ambient.name, func(t *testing.T) {
			if err := ambient.run(context.Background(), func(ctx context.Context) {
				for _, c := range portCalls(repos) {
					err := c.call(ctx)
					if !errors.Is(err, ErrAmbientTransactionUnsupported) {
						t.Errorf("%s inside an ambient transaction: got %v, want ErrAmbientTransactionUnsupported", c.name, err)
					}
					if c.mutation && !errors.Is(err, mutations.ErrGuardedInsideTransaction) {
						t.Errorf("%s inside an ambient transaction: got %v, want it to also carry mutation.ErrGuardedInsideTransaction", c.name, err)
					}
				}
			}); err != nil {
				t.Fatalf("%s: %v", ambient.name, err)
			}
		})
	}
}

// portCall is one public port method reduced to "call it, keep the error" so the
// ambient refusal can be asserted for ALL of them without thirty near-identical
// blocks. Arguments are deliberately trivial: the refusal precedes every
// validation, so nothing here needs to be a legal command.
type portCall struct {
	name     string
	mutation bool
	call     func(context.Context) error
}

// portCalls enumerates the three ports' database methods and the optional
// relation set reads this store supplies. GuardianPolicy reads configuration
// only, so it has no ambient transaction to refuse.
func portCalls(repos authorization.Repositories) []portCall {
	r, o, m := repos.Relationships, repos.Roles, repos.Mutations
	ref := relationships.SubjectRef{Type: "user", ID: "u1"}
	ids := []string{"d1"}

	err1 := func(_ any, err error) error { return err }

	return []portCall{
		{name: "CheckRelationWithGroupExpansion", call: func(ctx context.Context) error {
			_, err := r.CheckRelationWithGroupExpansion(ctx, "doc", "d1", "owner", "user", "u1", 0)
			return err
		}},
		{name: "GetRelationTargets", call: func(ctx context.Context) error {
			return err1(r.GetRelationTargets(ctx, "doc", "d1", "owner"))
		}},
		{name: "FilterRelation", call: func(ctx context.Context) error {
			return err1(r.(relationships.RelationSetReader).FilterRelation(ctx, "doc", ids, "owner", "user", "u1", 0))
		}},
		{name: "RelationTargetsFor", call: func(ctx context.Context) error {
			return err1(r.(relationships.RelationSetReader).RelationTargetsFor(ctx, "doc", ids, "owner"))
		}},
		{name: "CheckRelationExists", call: func(ctx context.Context) error {
			_, err := r.CheckRelationExists(ctx, "doc", "d1", "owner", "user", "u1")
			return err
		}},
		{name: "CheckBatchDirect", call: func(ctx context.Context) error {
			return err1(r.CheckBatchDirect(ctx, "doc", ids, "owner", "user", "u1", 0))
		}},
		{name: "CreateRelationships", call: func(ctx context.Context) error {
			return r.CreateRelationships(ctx, []relationships.CreateRelationship{{
				ResourceType: "doc", ResourceID: "d1", Relation: "owner", SubjectType: "user", SubjectID: "u1",
			}})
		}},
		{name: "SetRelationTargets", call: func(ctx context.Context) error {
			return r.SetRelationTargets(ctx, "doc", "d1", "owner", nil)
		}},
		{name: "DeleteRelationshipTarget", call: func(ctx context.Context) error {
			return r.DeleteRelationshipTarget(ctx, "doc", "d1", "owner", ref)
		}},
		{name: "DeleteResourceRelationships", call: func(ctx context.Context) error {
			return r.DeleteResourceRelationships(ctx, "doc", "d1")
		}},
		{name: "DeleteRelationship", call: func(ctx context.Context) error {
			return r.DeleteRelationship(ctx, "doc", "d1", "owner", "user", "u1")
		}},
		{name: "DeleteByResourceAndSubject", call: func(ctx context.Context) error {
			return r.DeleteByResourceAndSubject(ctx, "doc", "d1", "user", "u1")
		}},
		{name: "CountByResourceAndRelation", call: func(ctx context.Context) error {
			_, err := r.CountByResourceAndRelation(ctx, "doc", "d1", "owner")
			return err
		}},
		{name: "ListRelationshipsBySubject", call: func(ctx context.Context) error {
			return err1(r.ListRelationshipsBySubject(ctx, "user", "u1", relationships.SubjectRelationshipFilter{}, list.Request{}))
		}},
		{name: "ListRelationshipsByResource", call: func(ctx context.Context) error {
			return err1(r.ListRelationshipsByResource(ctx, "doc", "d1", relationships.ResourceRelationshipFilter{}, list.Request{}))
		}},
		{name: "LookupResourceIDs", call: func(ctx context.Context) error {
			return err1(r.LookupResourceIDs(ctx, "doc", []string{"owner"}, "user", "u1", "", 10))
		}},
		{name: "LookupResourceIDsByRelationTarget", call: func(ctx context.Context) error {
			return err1(r.LookupResourceIDsByRelationTarget(ctx, "doc", "parent", "folder", ids, "", 10))
		}},
		{name: "LookupDescendantResourceIDs", call: func(ctx context.Context) error {
			return err1(r.LookupDescendantResourceIDs(ctx, "doc", []string{"parent"}, "doc", ids, "", 10))
		}},

		{name: "Assign", call: func(ctx context.Context) error {
			return o.Assign(ctx, roles.Assignment{SubjectType: "user", SubjectID: "u1", Role: "admin"})
		}},
		{name: "Unassign", call: func(ctx context.Context) error {
			return o.Unassign(ctx, "user", "u1", "admin", "", "")
		}},
		{name: "HasExactRole", call: func(ctx context.Context) error {
			_, err := o.HasExactRole(ctx, "user", "u1", "admin", "", "")
			return err
		}},
		{name: "ListBySubject", call: func(ctx context.Context) error {
			return err1(o.ListBySubject(ctx, "user", "u1", list.Request{}))
		}},
		{name: "ListByResource", call: func(ctx context.Context) error {
			return err1(o.ListByResource(ctx, "tenant", "t1", list.Request{}))
		}},
		{name: "ListEffectiveByResource", call: func(ctx context.Context) error {
			return err1(o.ListEffectiveByResource(ctx, "tenant", "t1", list.Request{}))
		}},
		{name: "LookupResourceIDsBySubjectAndRoles", call: func(ctx context.Context) error {
			_, _, err := o.LookupResourceIDsBySubjectAndRoles(ctx, "user", "u1", "tenant", []string{"admin"}, "", 10)
			return err
		}},

		{name: "Apply", mutation: true, call: func(ctx context.Context) error {
			return err1(m.Apply(ctx, mutations.Command{}, nil))
		}},
		{name: "ApplyGuarded", mutation: true, call: func(ctx context.Context) error {
			return err1(m.ApplyGuarded(ctx, mutations.Command{}, func(context.Context, mutations.StoreDecisionView) error { return nil }, nil))
		}},
	}
}

// TestPortCallsCoverEveryPortMethod keeps the ambient-refusal table honest: the
// database methods and optional set reads remain covered when the ports change.
func TestPortCallsCoverEveryPortMethod(t *testing.T) {
	const want = 27
	if got := len(portCalls(authorization.Repositories{})); got != want {
		t.Fatalf("the ambient-refusal table drives %d port methods, want %d — add the new port method to portCalls", got, want)
	}
}
