//go:build integration && live

// The live-project leg of the A2c write family. It runs against a REAL
// Firestore database (firestoretest.OpenLive: FIRESTORE_LIVE_PROJECT_ID +
// FIRESTORE_LIVE_DATABASE_ID and credentials, refusing an emulator endpoint and
// the default database) and SKIPS loudly when that is not configured — unless
// FIRESTORE_LIVE_REQUIRED=1 turns the skip into the release-gate failure.
//
// Only the assertions the emulator cannot honestly make live here. The emulator
// implements transaction contention, but its locks are held for up to thirty
// seconds and it "does not implement all transaction behavior", so a WIDE
// concurrent fan-out on it proves timing, not serializability. The two-caller
// convergence case runs on the emulator (writes_test.go); the strict, wide one
// runs here.
//
// Task A6 owns the complete live entrypoint (the full conformance suite, the
// index probe against deployed indexes, and the skip audit). This file is the
// single A2c assertion that could not wait for it.
package firestore

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"

	gcfs "cloud.google.com/go/firestore"
	"github.com/gopernicus/gopernicus/integrations/datastores/firestore/firestoretest"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
)

// liveCollections are the collections a live reset is allowed to clear. Naming
// them explicitly is the live harness's contract: a live database is never
// emptied wholesale.
var liveCollections = []string{
	collectionRelationships,
	collectionSubjectClaims,
}

// TestSetRelationTargetsConcurrentDisjointSetsConvergeLive is the strict form of
// the serialization contract: FOUR callers, each with a different single-target
// desired state, all reconciling the same empty resource at once. Real
// Firestore aborts every commit that lost its race and re-runs the callback, so
// the final state must be exactly ONE caller's desired set — never a union,
// never a partial merge — and every loser's tuple must be gone with BOTH of its
// claims, or a later reconciliation could not repair the resource.
//
// The emulator cannot make this assertion honestly: its transaction locks are
// held for up to thirty seconds, so four contending callers exhaust the
// vendor's five attempts on timing rather than on serialization.
func TestSetRelationTargetsConcurrentDisjointSetsConvergeLive(t *testing.T) {
	db := firestoretest.OpenLive(t)
	firestoretest.ResetLive(t, db, liveCollections...)

	// The index probe is deliberately skipped: A5 deploys this store's manifest
	// and A6 owns the live entrypoint that proves the probe. What is under test
	// here is transaction serializability, which no index affects.
	s, err := RelationshipRepository(t.Context(), db, WithoutIndexProbe())
	if err != nil {
		t.Fatalf("RelationshipRepository: %v", err)
	}

	ctx := context.Background()
	callers := []string{"a", "b", "c", "d"}
	var wg sync.WaitGroup
	for _, id := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.SetRelationTargets(ctx, "space", "child", "parent", []relationships.CreateRelationship{
				{ResourceType: "space", ResourceID: "child", Relation: "parent", SubjectType: "space", SubjectID: id},
			}); err != nil {
				t.Errorf("set %s: %v", id, err)
			}
		}()
	}
	wg.Wait()

	targets, err := s.GetRelationTargets(ctx, "space", "child", "parent")
	if err != nil {
		t.Fatalf("GetRelationTargets: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("four concurrent desired states must converge on ONE winner, got %+v", targets)
	}
	winner := targets[0].ID
	if !slices.Contains(callers, winner) {
		t.Fatalf("winner %q is no caller's desired state", winner)
	}
	if targets[0].Type != "space" || targets[0].Relation != "" {
		t.Fatalf("the winning target must be the concrete subject it asked for, got %+v", targets[0])
	}

	// Every loser is gone completely — row and the subject claim.
	for _, id := range callers {
		if id == winner {
			continue
		}
		row := relationshipDoc{
			ResourceType: "space", ResourceID: "child", Relation: "parent",
			SubjectType: "space", SubjectID: id,
		}
		tuple, subject := claimRefs(db, row)
		snaps, err := db.ReaderFrom(ctx).GetAll(ctx, []*gcfs.DocumentRef{tuple, subject})
		if err != nil {
			t.Fatalf("GetAll: %v", err)
		}
		for i, what := range []string{"row", "subject claim"} {
			if snaps[i].Exists() {
				t.Fatalf("loser %s left its %s behind", id, what)
			}
		}
	}
}

// TestLargeBatchCommitsInOneTransactionLive proves the vendor fact the A7 review
// settled, against the real server rather than the emulator: Firestore has NO
// per-transaction write COUNT limit. The quotas page bounds a commit by the
// 10 MiB maximum API request size and by 500 field transformations per
// document — neither is a count of writes — and this store therefore enforces
// no client-side ceiling of its own.
//
// 200 tuples is 600 documents in ONE transaction, past the 500 this store used
// to refuse. The emulator agrees (writes_test.go), but the emulator "does not
// enforce all limits", so only this leg is evidence about production.
func TestLargeBatchCommitsInOneTransactionLive(t *testing.T) {
	db := firestoretest.OpenLive(t)
	firestoretest.ResetLive(t, db, liveCollections...)

	s, err := RelationshipRepository(t.Context(), db, WithoutIndexProbe())
	if err != nil {
		t.Fatalf("RelationshipRepository: %v", err)
	}

	ctx := context.Background()
	const tuples = 200
	batch := make([]relationships.CreateRelationship, 0, tuples)
	for i := 0; i < tuples; i++ {
		batch = append(batch, relationships.CreateRelationship{
			ResourceType: "doc", ResourceID: "live-big", Relation: "viewer",
			SubjectType: "user", SubjectID: fmt.Sprintf("u%04d", i),
		})
	}
	if err := s.CreateRelationships(ctx, batch); err != nil {
		t.Fatalf("a %d-tuple (%d-document) batch must commit in one transaction: %v", tuples, tuples*3, err)
	}
	n, err := s.CountByResourceAndRelation(ctx, "doc", "live-big", "viewer")
	if err != nil {
		t.Fatalf("CountByResourceAndRelation: %v", err)
	}
	if n != tuples {
		t.Fatalf("committed %d rows, want %d", n, tuples)
	}
	if err := s.DeleteResourceRelationships(ctx, "doc", "live-big"); err != nil {
		t.Fatalf("a %d-document delete must commit in one transaction: %v", tuples*3, err)
	}
}
