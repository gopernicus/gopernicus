//go:build integration && !live

package firestore

import (
	"context"
	"errors"
	"testing"

	gcfs "cloud.google.com/go/firestore"
	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	mutation "github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	authroles "github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/sdk"
	"google.golang.org/api/iterator"
)

// The store-integrity leg (A7 fold, review finding "data #7"). Everything here
// is about a property NO port assertion can see: this store keeps a relationship
// tuple in TWO documents (the row plus the subject claim that reproduce SQL
// constraints a deterministic document id cannot carry), so the port can report
// a perfectly correct answer while the collections behind it have drifted.
//
// A stranded subject claim blocks recreating a removed tuple; a row whose
// derived keys disagree with its own fields is invisible to every list and
// lookup. The conformance suite passes in all three states, which is exactly why
// these tests read the collections directly.

// TestPurgeAndTeardownDropBothClaimsForEveryRow walks the two whole-resource
// mutation operations through the claim lifecycle. They are the operations that
// remove the MOST rows at once, and the ones where a forgotten claim would be
// least visible: the resource ends up empty either way, and only an attempt to
// recreate one of its tuples would ever notice.
//
// Teardown additionally sweeps the resource's SCOPED role grants, and this pins
// the boundary the sweep must respect: a GLOBAL grant for the same subject and
// role is not the resource's to remove.
func TestPurgeAndTeardownDropBothClaimsForEveryRow(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		name      string
		operation mutation.Operation
		// guardian is the policy the case runs under. An ordinary PURGE honors
		// the guardian invariant, and DefaultGuardianPolicy protects `owner` on
		// every resource type with a minimum of one direct anchor — so under it
		// a purge of ANY resource returns ErrInvariantBlocked and removes nothing.
		// The purge case therefore runs under an empty policy, which is the
		// supported host configuration for unprotected resources; teardown is
		// the operation allowed to zero a protected scope and keeps the default.
		guardian mutation.GuardianPolicy
	}{
		{name: "purge", operation: mutation.OpPurge, guardian: mutation.GuardianPolicy{}},
		{name: "teardown", operation: mutation.OpTeardown, guardian: mutation.DefaultGuardianPolicy()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, _, rels := newMutations(t)
			m := newMutationStore(db, tc.guardian, false)
			roles := newRoleStore(db, false)

			// Three rows on the resource, one of them a userset, plus a scoped
			// role grant and a global one for the same subject and role. No
			// GUARDIAN-protected relation among them: an ordinary purge honors
			// the invariant and would be blocked, which is a different test
			// (storetest's guardian family) — what is under test here is the
			// claim lifecycle of the rows that DO get removed.
			seeded := []relationships.CreateRelationship{
				ctf("doc", "d1", "editor", "user", "u1"),
				ctf("doc", "d1", "viewer", "user", "u2"),
				ctfUserset("doc", "d1", "viewer", "group", "g1", "member"),
			}
			if err := rels.CreateRelationships(ctx, seeded); err != nil {
				t.Fatalf("seed: %v", err)
			}
			scoped := authroles.Assignment{SubjectType: "user", SubjectID: "u1", Role: "editor", ResourceType: "doc", ResourceID: "d1"}
			global := authroles.Assignment{SubjectType: "user", SubjectID: "u1", Role: "editor"}
			for _, a := range []authroles.Assignment{scoped, global} {
				if err := roles.Assign(ctx, a); err != nil {
					t.Fatalf("Assign %+v: %v", a, err)
				}
			}

			rows := storedResourceRows(t, db, "doc", "d1")
			if len(rows) != len(seeded) {
				t.Fatalf("seeded %d rows, found %d", len(seeded), len(rows))
			}
			for _, row := range rows {
				assertTupleDocs(t, db, row, true)
			}

			cmd := mutation.Command{Target: docScope("d1"), Operation: tc.operation}
			rcpt, err := m.Apply(ctx, cmd, nil)
			if err != nil {
				t.Fatalf("%s: %v", tc.operation, err)
			}
			if rcpt.Outcome != mutation.OutcomeApplied {
				t.Fatalf("%s outcome = %q, want applied — nothing was removed, so the claim assertions below would pass vacuously", tc.operation, rcpt.Outcome)
			}

			// Every removed row took BOTH of its claims with it.
			for _, row := range rows {
				assertTupleDocs(t, db, row, false)
			}
			if left := storedResourceRows(t, db, "doc", "d1"); len(left) != 0 {
				t.Fatalf("%s left %d rows", tc.operation, len(left))
			}

			// The role sweep is teardown's alone, and it never reaches a global
			// grant.
			scopedLeft := roleExistsAt(t, db, scoped)
			if want := tc.operation != mutation.OpTeardown; scopedLeft != want {
				t.Errorf("scoped role grant present=%v after %s, want %v", scopedLeft, tc.operation, want)
			}
			if !roleExistsAt(t, db, global) {
				t.Errorf("%s removed a GLOBAL role grant, which no resource owns", tc.operation)
			}

			// A tuple removed with its claims can be recreated. A stranded
			// claim is exactly what would make this fail or silently no-op.
			if err := rels.CreateRelationships(ctx, seeded); err != nil {
				t.Fatalf("recreate after %s: %v", tc.operation, err)
			}
			if again := storedResourceRows(t, db, "doc", "d1"); len(again) != len(seeded) {
				t.Fatalf("recreate after %s stored %d rows, want %d — a stranded claim swallowed one", tc.operation, len(again), len(seeded))
			}
		})
	}
}

// TestCollectionsHaveNoOrphansAfterTheWholeWritePortSurface is the sweep the
// review asked for: drive every write shape the three ports expose — through the
// PORTS, never the private helpers — and then assert the three collections agree
// with each other.
//
// Three invariants, and each one has a failure mode the conformance suite cannot
// see:
//
//  1. one subject claim per row, no more and no fewer. A
//     surplus claim blocks a future create; a missing one enforces nothing.
//  2. every claim's tuple_id resolves to a row that exists. A claim pointing at
//     a deleted row is an orphan that outlives it.
//  3. every row's derived keys recompute from its own fields. A row whose
//     resource_key disagrees with its resource_type/resource_id is invisible to
//     every list, lookup and check that filters on the key.
func TestCollectionsHaveNoOrphansAfterTheWholeWritePortSurface(t *testing.T) {
	ctx := context.Background()
	db, _, rels := newMutations(t)
	m := newMutationStore(db, mutation.GuardianPolicy{}, false)

	// create
	if err := rels.CreateRelationships(ctx, []relationships.CreateRelationship{
		ctf("doc", "d1", "owner", "user", "u1"),
		ctf("doc", "d1", "viewer", "user", "u2"),
		ctf("doc", "d2", "owner", "user", "u1"),
		ctfUserset("doc", "d2", "viewer", "group", "g1", "member"),
		ctf("group", "g1", "member", "user", "u3"),
		ctf("doc", "survivor", "viewer", "user", "retained"),
	}); err != nil {
		t.Fatalf("CreateRelationships: %v", err)
	}
	// set (reconciliation: one surplus removed, one missing added)
	if err := rels.SetRelationTargets(ctx, "doc", "d1", "viewer", []relationships.CreateRelationship{
		ctf("doc", "d1", "viewer", "user", "u4"),
	}); err != nil {
		t.Fatalf("SetRelationTargets: %v", err)
	}
	// grant + replace + revoke through the mutation port
	if _, err := m.Apply(ctx, mutation.Command{
		Target: docScope("d2"), Operation: mutation.OpGrant,
		Relationships: []mutation.RelationshipRow{{Relation: "viewer", Subject: user("u5")}},
	}, nil); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if _, err := m.Apply(ctx, mutation.Command{
		Target: docScope("d2"), Operation: mutation.OpReplace,
		Relationships: []mutation.RelationshipRow{{Relation: "editor", Subject: user("u5")}},
	}, nil); err != nil {
		t.Fatalf("replace: %v", err)
	}
	if _, err := m.Apply(ctx, mutation.Command{
		Target: docScope("d2"), Operation: mutation.OpRevoke,
		Relationships: []mutation.RelationshipRow{{Relation: "editor", Subject: user("u5")}},
	}, nil); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	// purge one resource entirely, and delete another by resource
	purged, err := m.Apply(ctx, mutation.Command{
		Target: docScope("d2"), Operation: mutation.OpPurge,
	}, nil)
	if err != nil || purged == nil || purged.Outcome != mutation.OutcomeApplied {
		t.Fatalf("purge must remove the seeded rows: result=%+v error=%v", purged, err)
	}
	if err := rels.DeleteResourceRelationships(ctx, "group", "g1"); err != nil {
		t.Fatalf("DeleteResourceRelationships: %v", err)
	}
	// and the two narrower deletes
	if err := rels.DeleteRelationship(ctx, "doc", "d1", "viewer", "user", "u4"); err != nil {
		t.Fatalf("DeleteRelationship: %v", err)
	}
	if err := rels.DeleteByResourceAndSubject(ctx, "doc", "d1", "user", "u1"); err != nil {
		t.Fatalf("DeleteByResourceAndSubject: %v", err)
	}

	assertNoOrphans(t, db)
}

// assertNoOrphans is the cross-collection audit the sweep above rests on.
func assertNoOrphans(t *testing.T, db *firestoredb.DB) {
	t.Helper()
	ctx := context.Background()
	r := db.ReaderFrom(ctx)

	rowsByID := map[string]relationshipDoc{}
	forEachDoc(t, r, db.Collection(collectionRelationships).Query, func(snap *gcfs.DocumentSnapshot) {
		var row relationshipDoc
		if err := snap.DataTo(&row); err != nil {
			t.Fatalf("decoding a row: %v", err)
		}
		// (3) the derived keys recompute from the row's own fields.
		if want := resourceKey(row.ResourceType, row.ResourceID); row.ResourceKey != want {
			t.Errorf("row %s: resource_key %q does not match its own %s:%s", snap.Ref.ID, row.ResourceKey, row.ResourceType, row.ResourceID)
		}
		if want := subjectKey(row.SubjectType, row.SubjectID, row.SubjectRelation); row.SubjectKey != want {
			t.Errorf("row %s: subject_key %q does not match its own %s:%s#%s", snap.Ref.ID, row.SubjectKey, row.SubjectType, row.SubjectID, row.SubjectRelation)
		}
		if got := tupleID(row); got != snap.Ref.ID {
			t.Errorf("row stored at %s but its own fields hash to %s", snap.Ref.ID, got)
		}
		rowsByID[snap.Ref.ID] = row
	})

	subjectClaims := 0
	forEachDoc(t, r, db.Collection(collectionSubjectClaims).Query, func(snap *gcfs.DocumentSnapshot) {
		subjectClaims++
		var claim subjectClaimDoc
		if err := snap.DataTo(&claim); err != nil {
			t.Fatalf("decoding a subject claim: %v", err)
		}
		// (2) the claim's tuple_id resolves.
		row, ok := rowsByID[claim.TupleID]
		if !ok {
			t.Errorf("subject claim %s points at tuple %s, which does not exist", snap.Ref.ID, claim.TupleID)
			return
		}
		if claim.Relation != row.Relation {
			t.Errorf("subject claim %s records relation %q, its row holds %q", snap.Ref.ID, claim.Relation, row.Relation)
		}
	})

	// (1) one of each claim per row — counted, so a SURPLUS claim fails here
	// even though every claim above resolved.
	if len(rowsByID) != subjectClaims {
		t.Fatalf("collections disagree: %d rows, %d subject claims", len(rowsByID), subjectClaims)
	}
	if len(rowsByID) == 0 {
		t.Fatal("the sweep found no rows — it would pass vacuously")
	}
}

// storedResourceRows reads a resource's rows straight from the collection,
// bypassing every port.
func storedResourceRows(t *testing.T, db *firestoredb.DB, resourceType, resourceID string) []relationshipDoc {
	t.Helper()
	ctx := context.Background()
	rows, err := queryRelationships(ctx, db.ReaderFrom(ctx),
		db.Collection(collectionRelationships).Where("resource_key", "==", resourceKey(resourceType, resourceID)))
	if err != nil {
		t.Fatalf("reading %s:%s rows: %v", resourceType, resourceID, err)
	}
	return rows
}

// roleExistsAt reports whether an exact role grant document is present.
func roleExistsAt(t *testing.T, db *firestoredb.DB, a authroles.Assignment) bool {
	t.Helper()
	ctx := context.Background()
	snap, err := db.ReaderFrom(ctx).Get(ctx, roleRef(db, a.SubjectType, a.SubjectID, a.Role, a.ResourceType, a.ResourceID))
	if err != nil && !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("reading role %+v: %v", a, err)
	}
	return snap != nil && snap.Exists()
}

// forEachDoc walks every document of a collection.
func forEachDoc(t *testing.T, r firestoredb.Reader, q gcfs.Query, visit func(*gcfs.DocumentSnapshot)) {
	t.Helper()
	ctx := context.Background()
	it := r.Documents(ctx, q)
	defer it.Stop()
	for {
		snap, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return
		}
		if err != nil {
			t.Fatalf("scanning a collection: %v", err)
		}
		visit(snap)
	}
}
