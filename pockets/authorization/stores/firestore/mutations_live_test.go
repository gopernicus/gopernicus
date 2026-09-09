//go:build integration && live

// The live-project leg of the A4 mutation family. It runs against a REAL
// Firestore database (firestoretest.OpenLive) and SKIPS loudly when that is not
// configured — unless FIRESTORE_LIVE_REQUIRED=1 turns the skip into the
// release-gate failure.
//
// Only the assertion the emulator cannot make honestly lives here. The emulator
// implements transaction contention, but it holds locks for up to thirty seconds
// and "does not implement all transaction behavior", so which side of a
// dependency race wins there is a timing fact. Real Firestore ABORTS the commit
// whose read set moved and re-runs the callback, so the outcome is determined:
// a guarded write whose dependency was revoked before it committed re-evaluates
// on the post-revoke state and is DENIED, never a stale allow. The emulator leg
// (mutations_test.go) asserts the portable invariant instead.
//
// Task A6 owns the complete live entrypoint (the full conformance suite, the
// index probe, the skip audit). This file is the single A4 assertion that could
// not wait for it.
package firestore

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/integrations/datastores/firestore/firestoretest"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/mutation"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/sdk"
)

// liveMutationCollections are the collections this leg's reset may clear. A live
// database is never emptied wholesale.
var liveMutationCollections = []string{
	collectionRelationships,
	collectionSubjectClaims,
	collectionIDClaims,
	collectionRoles,
	collectionScopes,
	collectionMutations,
}

// TestGuardedMutationDependencyRevokeIsDeterminedLive is the strict form of the
// cross-scope dependency race: over several rounds, a guarded write whose
// authority flows THROUGH a group membership races a trusted revoke of that
// membership. Real Firestore serializes them, so exactly one of two determined
// results holds every round, and the forbidden third — a receipt built on a
// dependency that had already moved — can never appear:
//
//   - the guarded write serialized FIRST: it applied, its row is present, and
//     the revoke committed after it;
//   - the revoke serialized first: the guarded attempt's commit aborted, the
//     vendor re-ran the callback, the guard re-read a state where alice is no
//     longer an editor, and the write is DENIED with no receipt and no row.
//
// The revoke always commits (group:g keeps its own owner).
func TestGuardedMutationDependencyRevokeIsDeterminedLive(t *testing.T) {
	ctx := context.Background()
	db := firestoretest.OpenLive(t)
	firestoretest.ResetLive(t, db, liveMutationCollections...)

	// The index probe is deliberately skipped: A5 deploys the manifest and A6
	// owns the live entrypoint that proves the probe. What is under test here is
	// transaction serializability, which no index affects.
	repos, err := Repositories(db, WithoutIndexProbe())
	if err != nil {
		t.Fatalf("Repositories: %v", err)
	}
	m, rel := repos.Mutations, repos.Relationships

	const rounds = 8
	for round := 0; round < rounds; round++ {
		suffix := strconv.Itoa(round)
		grp := mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "group", ID: "g" + suffix}
		doc := mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "doc", ID: "d" + suffix}

		seed := func(scope mutation.ScopeKey, relation string, subject relationship.SubjectRef) {
			t.Helper()
			id, err := mutation.NewMutationID()
			if err != nil {
				t.Fatalf("NewMutationID: %v", err)
			}
			rcpt, err := m.Apply(ctx, mutation.Command{
				MutationID: id, Scope: scope, Operation: mutation.OpGrant,
				Relationships: []mutation.RelationshipRow{{Relation: relation, Subject: subject}},
			}, nil)
			if err != nil || rcpt.Outcome != mutation.OutcomeApplied {
				t.Fatalf("seeding %s#%s: rcpt=%+v err=%v", scope, relation, rcpt, err)
			}
		}
		seed(grp, "owner", relationship.SubjectRef{Type: "user", ID: "gowner"})
		seed(grp, "member", relationship.SubjectRef{Type: "user", ID: "alice"})
		seed(doc, "owner", relationship.SubjectRef{Type: "user", ID: "downer"})
		seed(doc, "editor", relationship.SubjectRef{Type: "group", ID: "g" + suffix, Relation: "member"})

		guard := func(gctx context.Context, view mutation.StoreDecisionView) error {
			ok, err := view.CheckRelation(gctx, doc, "editor", "user", "alice")
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("alice is not an editor: %w", sdk.ErrForbidden)
			}
			return nil
		}

		guardedID, err := mutation.NewMutationID()
		if err != nil {
			t.Fatalf("NewMutationID: %v", err)
		}
		revokeID, err := mutation.NewMutationID()
		if err != nil {
			t.Fatalf("NewMutationID: %v", err)
		}

		var (
			wg                      sync.WaitGroup
			guardedRcpt, revokeRcpt *mutation.Receipt
			guardedErr, revokeErr   error
		)
		wg.Add(2)
		go func() {
			defer wg.Done()
			guardedRcpt, guardedErr = m.ApplyGuarded(ctx, mutation.Command{
				MutationID: guardedID, Scope: doc, Operation: mutation.OpGrant,
				Relationships: []mutation.RelationshipRow{{Relation: "viewer", Subject: relationship.SubjectRef{Type: "user", ID: "reader"}}},
			}, guard, nil)
		}()
		go func() {
			defer wg.Done()
			revokeRcpt, revokeErr = m.Apply(ctx, mutation.Command{
				MutationID: revokeID, Scope: grp, Operation: mutation.OpRevoke,
				Relationships: []mutation.RelationshipRow{{Relation: "member", Subject: relationship.SubjectRef{Type: "user", ID: "alice"}}},
			}, nil)
		}()
		wg.Wait()

		if revokeErr != nil || revokeRcpt.Outcome != mutation.OutcomeApplied {
			t.Fatalf("round %d: the membership revoke must commit: rcpt=%+v err=%v", round, revokeRcpt, revokeErr)
		}
		present, err := rel.CheckRelationExists(ctx, "doc", "d"+suffix, "viewer", "user", "reader")
		if err != nil {
			t.Fatalf("round %d: CheckRelationExists: %v", round, err)
		}
		switch {
		case guardedErr == nil:
			if guardedRcpt == nil || guardedRcpt.Outcome != mutation.OutcomeApplied || !present {
				t.Fatalf("round %d: a nil-error guarded write must be applied with its row: rcpt=%+v row=%v", round, guardedRcpt, present)
			}
		case errors.Is(guardedErr, sdk.ErrForbidden) || errors.Is(guardedErr, sdk.ErrConflict):
			if guardedRcpt != nil || present {
				t.Fatalf("round %d: a re-evaluated denial must leave no receipt and no row: rcpt=%+v row=%v", round, guardedRcpt, present)
			}
			if liveReceiptExists(t, db, guardedID) {
				t.Fatalf("round %d: a denied guarded write must persist no receipt", round)
			}
		default:
			t.Fatalf("round %d: the guarded write must be applied or denied; got rcpt=%+v err=%v", round, guardedRcpt, guardedErr)
		}
	}
}

// liveReceiptExists reports whether a receipt document exists for id — the
// forensic the port cannot make, because every port-level anchor probe is itself
// a command.
func liveReceiptExists(t *testing.T, db *firestoredb.DB, id mutation.MutationID) bool {
	t.Helper()
	ctx := context.Background()
	snap, err := db.ReaderFrom(ctx).Get(ctx, db.Doc(collectionMutations, mutationDocID(string(id))))
	if err != nil && !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("reading the receipt for %s: %v", id, err)
	}
	return snap != nil && snap.Exists()
}
