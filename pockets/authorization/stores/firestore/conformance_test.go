//go:build integration && !live

// Integration tests hit a Firestore EMULATOR. Run with:
//
//	FIRESTORE_EMULATOR_HOST=127.0.0.1:8080 go test -tags=integration -timeout 30m ./...
//
// Absent FIRESTORE_EMULATOR_HOST they skip loudly — a silent green here would
// claim a datastore-family conformance nothing verified. The suite opens the
// emulator's "authorization" database (firestoretest's isolation contract: every
// pocket store train owns its own database, because Reset clears a WHOLE
// database), and passes WithoutIndexProbe because the emulator keeps no index
// registry (R5).
//
// What an emulator green does NOT prove: composite index coverage (nothing here
// enforces one) and exact transaction/lock timing. Task A5's live query matrix
// and the live-project leg are the truth for both.
package firestore

import (
	"context"
	"errors"
	"testing"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/integrations/datastores/firestore/firestoretest"
	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/mutation"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/role"
	"github.com/gopernicus/gopernicus/pockets/authorization/storetest"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// emulatorDatabase is this store train's own emulator database. It is NOT the
// default database and NOT the connector's: firestoretest.Reset clears the whole
// selected database, so two suites sharing one would delete each other's rows
// and the failure would arrive as a flake.
const emulatorDatabase = "authorization"

// newRepos opens the emulator database, clears it, and constructs the full
// repository set — a FRESH, empty store per call, which is what every storetest
// family assumes.
func newRepos(t *testing.T) authorization.Repositories {
	t.Helper()
	db := firestoretest.OpenDatabase(t, emulatorDatabase)
	firestoretest.Reset(t, db)
	repos, err := Repositories(db, WithoutIndexProbe())
	if err != nil {
		t.Fatalf("Repositories: %v", err)
	}
	return repos
}

// TestConformance runs the shared authorization conformance suite (both kinds,
// the adversarial/budget/keyset/set-read families, and the mutation families)
// against the emulator. It is the executable form of all three ports' contracts.
func TestConformance(t *testing.T) {
	storetest.Run(t, newRepos)
}

// TestRunTransactional is ruling R1's loud skip. The Firestore store hands the
// harness NO crud.Transactor, because a Firestore transaction requires every
// read to precede every write and never observes its own pending writes — the
// exact property the ambient family proves from both sides. The harness's
// existing nil-transactor path (the memstore takes it too) skips the family
// loudly rather than reporting a green it did not earn.
func TestRunTransactional(t *testing.T) {
	storetest.RunTransactional(t, func(t *testing.T) (authorization.Repositories, crud.Transactor) {
		return newRepos(t), nil
	})
}

// TestAmbientTransactionRefused is R1's other half: a store method handed a
// context that carries a connector transaction FAILS LOUD rather than running on
// the client beside the host's transaction, which would silently split an atomic
// unit. Every public port method is driven, in both kinds of ambient
// transaction: a read-write Transact and a read-only ReadSnapshot.
func TestAmbientTransactionRefused(t *testing.T) {
	db := firestoretest.OpenDatabase(t, emulatorDatabase)
	firestoretest.Reset(t, db)
	repos, err := Repositories(db, WithoutIndexProbe())
	if err != nil {
		t.Fatalf("Repositories: %v", err)
	}

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
					if c.mutation && !errors.Is(err, mutation.ErrGuardedInsideTransaction) {
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

// portCalls enumerates every method of the three ports — 18 + 7 + 2 = 27. A port
// method missing from this list is a method that could silently join a host's
// transaction, so the count is asserted by the caller of the table below.
func portCalls(repos authorization.Repositories) []portCall {
	r, o, m := repos.Relationships, repos.Roles, repos.Mutations
	ref := relationship.SubjectRef{Type: "user", ID: "u1"}
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
			return err1(r.FilterRelation(ctx, "doc", ids, "owner", "user", "u1", 0))
		}},
		{name: "RelationTargetsFor", call: func(ctx context.Context) error {
			return err1(r.RelationTargetsFor(ctx, "doc", ids, "owner"))
		}},
		{name: "CheckRelationExists", call: func(ctx context.Context) error {
			_, err := r.CheckRelationExists(ctx, "doc", "d1", "owner", "user", "u1")
			return err
		}},
		{name: "CheckBatchDirect", call: func(ctx context.Context) error {
			return err1(r.CheckBatchDirect(ctx, "doc", ids, "owner", "user", "u1", 0))
		}},
		{name: "CreateRelationships", call: func(ctx context.Context) error {
			return r.CreateRelationships(ctx, []relationship.CreateRelationship{{
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
			return err1(r.ListRelationshipsBySubject(ctx, "user", "u1", relationship.SubjectRelationshipFilter{}, crud.ListRequest{}))
		}},
		{name: "ListRelationshipsByResource", call: func(ctx context.Context) error {
			return err1(r.ListRelationshipsByResource(ctx, "doc", "d1", relationship.ResourceRelationshipFilter{}, crud.ListRequest{}))
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
			return o.Assign(ctx, role.Assignment{SubjectType: "user", SubjectID: "u1", Role: "admin"})
		}},
		{name: "Unassign", call: func(ctx context.Context) error {
			return o.Unassign(ctx, "user", "u1", "admin", "", "")
		}},
		{name: "HasExactRole", call: func(ctx context.Context) error {
			_, err := o.HasExactRole(ctx, "user", "u1", "admin", "", "")
			return err
		}},
		{name: "ListBySubject", call: func(ctx context.Context) error {
			return err1(o.ListBySubject(ctx, "user", "u1", crud.ListRequest{}))
		}},
		{name: "ListByResource", call: func(ctx context.Context) error {
			return err1(o.ListByResource(ctx, "tenant", "t1", crud.ListRequest{}))
		}},
		{name: "ListEffectiveByResource", call: func(ctx context.Context) error {
			return err1(o.ListEffectiveByResource(ctx, "tenant", "t1", crud.ListRequest{}))
		}},
		{name: "LookupResourceIDsBySubjectAndRoles", call: func(ctx context.Context) error {
			_, _, err := o.LookupResourceIDsBySubjectAndRoles(ctx, "user", "u1", "tenant", []string{"admin"}, "", 10)
			return err
		}},

		{name: "Apply", mutation: true, call: func(ctx context.Context) error {
			return err1(m.Apply(ctx, mutation.Command{}, nil))
		}},
		{name: "ApplyGuarded", mutation: true, call: func(ctx context.Context) error {
			return err1(m.ApplyGuarded(ctx, mutation.Command{}, func(context.Context, mutation.StoreDecisionView) error { return nil }, nil))
		}},
	}
}

// TestPortCallsCoverEveryPortMethod keeps the ambient-refusal table honest: the
// three ports declare 18 + 7 + 2 methods at core v0.12.0, and a method missing
// from the table is a method whose refusal nothing asserts.
func TestPortCallsCoverEveryPortMethod(t *testing.T) {
	const want = 27
	if got := len(portCalls(authorization.Repositories{})); got != want {
		t.Fatalf("the ambient-refusal table drives %d port methods, want %d — add the new port method to portCalls", got, want)
	}
}
