//go:build integration && !live

package firestore

import (
	"context"
	"errors"
	"sync"
	"testing"

	gcfs "cloud.google.com/go/firestore"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// A2c: the raw relationship writes and the transactional claim lifecycle
// (A-D3). The shared conformance suite covers the port's OUTCOMES; what is
// asserted here is the part only this family has — that a tuple's THREE
// documents move together, that a refused operation leaves none of them behind,
// and that the transaction budget is enforced before anything is written.

// assertTupleDocs asserts a tuple's row and BOTH claims are present (want) or
// all three absent. A row without its claims is uniqueness enforced by nothing;
// a claim without its row is a tuple that can never be recreated.
func assertTupleDocs(t *testing.T, db *firestoredb.DB, row relationshipDoc, want bool) {
	t.Helper()
	ctx := context.Background()
	tuple, subject, id := claimRefs(db, row)
	snaps, err := db.ReaderFrom(ctx).GetAll(ctx, []*gcfs.DocumentRef{tuple, subject, id})
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	for i, what := range []string{"row", "subject claim", "id claim"} {
		if got := snaps[i].Exists(); got != want {
			t.Fatalf("%s:%s#%s <- %s:%s: %s exists=%v, want %v",
				row.ResourceType, row.ResourceID, row.Relation, row.SubjectType, row.SubjectID, what, got, want)
		}
	}
}

// TestCreateRelationshipsClaimLifecycle walks a tuple's three documents through
// a create and each delete variant: they arrive together and they leave
// together, so no delete path can strand a claim that would block recreating
// the tuple.
func TestCreateRelationshipsClaimLifecycle(t *testing.T) {
	ctx := context.Background()
	db, s := newRelationships(t)

	rows := func(c relationship.CreateRelationship) relationshipDoc {
		t.Helper()
		page, err := s.ListRelationshipsByResource(ctx, c.ResourceType, c.ResourceID, relationship.ResourceRelationshipFilter{}, crud.ListRequest{Limit: 100})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, item := range page.Items {
			if item.SubjectType == c.SubjectType && item.SubjectID == c.SubjectID && item.Relation == c.Relation {
				return relationshipDoc{
					RelationshipID: item.ID, ResourceType: c.ResourceType, ResourceID: c.ResourceID,
					Relation: c.Relation, SubjectType: c.SubjectType, SubjectID: c.SubjectID, SubjectRelation: c.SubjectRelation,
				}
			}
		}
		t.Fatalf("no stored row for %+v", c)
		return relationshipDoc{}
	}

	for _, tc := range []struct {
		name   string
		tuple  relationship.CreateRelationship
		delete func(relationshipDoc) error
	}{
		{
			name:  "DeleteRelationship",
			tuple: ctf("doc", "d1", "owner", "user", "u1"),
			delete: func(row relationshipDoc) error {
				return s.DeleteRelationship(ctx, row.ResourceType, row.ResourceID, row.Relation, row.SubjectType, row.SubjectID)
			},
		},
		{
			name:  "DeleteRelationshipTarget",
			tuple: ctfUserset("doc", "d2", "viewer", "group", "eng", "member"),
			delete: func(row relationshipDoc) error {
				return s.DeleteRelationshipTarget(ctx, row.ResourceType, row.ResourceID, row.Relation,
					relationship.SubjectRef{Type: row.SubjectType, ID: row.SubjectID, Relation: row.SubjectRelation})
			},
		},
		{
			name:  "DeleteByResourceAndSubject",
			tuple: ctf("doc", "d3", "editor", "user", "u3"),
			delete: func(row relationshipDoc) error {
				return s.DeleteByResourceAndSubject(ctx, row.ResourceType, row.ResourceID, row.SubjectType, row.SubjectID)
			},
		},
		{
			name:  "DeleteResourceRelationships",
			tuple: ctf("doc", "d4", "owner", "user", "u4"),
			delete: func(row relationshipDoc) error {
				return s.DeleteResourceRelationships(ctx, row.ResourceType, row.ResourceID)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := s.CreateRelationships(ctx, []relationship.CreateRelationship{tc.tuple}); err != nil {
				t.Fatalf("create: %v", err)
			}
			row := rows(tc.tuple)
			assertTupleDocs(t, db, row, true)

			if err := tc.delete(row); err != nil {
				t.Fatalf("delete: %v", err)
			}
			assertTupleDocs(t, db, row, false)

			// Idempotent: the same delete on an absent tuple is nil and still
			// leaves nothing behind.
			if err := tc.delete(row); err != nil {
				t.Fatalf("repeat delete must be nil, got %v", err)
			}
			assertTupleDocs(t, db, row, false)

			// The claims really are released: the identical tuple can be
			// created again, which a stranded subject or id claim would block.
			if err := s.CreateRelationships(ctx, []relationship.CreateRelationship{tc.tuple}); err != nil {
				t.Fatalf("recreate after delete: %v", err)
			}
			assertTupleDocs(t, db, rows(tc.tuple), true)
		})
	}
}

// TestCreateRelationshipsResolvesDuplicateSubjectsInInputOrder pins the
// first-write-wins rule the SQL siblings get from a bare ON CONFLICT DO
// NOTHING, for collisions INSIDE one batch as well as against stored rows: the
// FIRST row claiming a subject wins and every later one is a silent no-op.
func TestCreateRelationshipsResolvesDuplicateSubjectsInInputOrder(t *testing.T) {
	ctx := context.Background()
	db, s := newRelationships(t)

	if err := s.CreateRelationships(ctx, []relationship.CreateRelationship{
		ctf("doc", "d1", "owner", "user", "u1"),  // wins
		ctf("doc", "d1", "member", "user", "u1"), // same subject, different relation
		ctf("doc", "d1", "owner", "user", "u1"),  // exact duplicate
	}); err != nil {
		t.Fatalf("a colliding batch must be a nil no-op, got %v", err)
	}
	if n, err := s.CountByResourceAndRelation(ctx, "doc", "d1", "owner"); err != nil || n != 1 {
		t.Fatalf("owner count = %d err=%v, want exactly 1", n, err)
	}
	if n, _ := s.CountByResourceAndRelation(ctx, "doc", "d1", "member"); n != 0 {
		t.Fatalf("the later relation for the same subject must be skipped, got %d member rows", n)
	}
	// A stored subject wins over a later CALL the same way.
	if err := s.CreateRelationships(ctx, []relationship.CreateRelationship{ctf("doc", "d1", "member", "user", "u1")}); err != nil {
		t.Fatalf("second relation for a stored subject must be nil, got %v", err)
	}
	if n, _ := s.CountByResourceAndRelation(ctx, "doc", "d1", "member"); n != 0 {
		t.Fatalf("the stored owner relation must be unchanged, got %d member rows", n)
	}
	// The skipped row left NO row document. Its SUBJECT claim is present on
	// purpose — the claim id excludes the relation, so it is the winning owner
	// row's claim, and that is precisely what makes the second relation a no-op.
	assertRowAbsent(t, db, relationshipDoc{
		ResourceType: "doc", ResourceID: "d1", Relation: "member", SubjectType: "user", SubjectID: "u1",
	})
}

// assertRowAbsent asserts a tuple's ROW document is absent, saying nothing about
// the claims (a skipped row's subject claim legitimately belongs to the row that
// won the subject).
func assertRowAbsent(t *testing.T, db *firestoredb.DB, row relationshipDoc) {
	t.Helper()
	ctx := context.Background()
	tuple, _, _ := claimRefs(db, row)
	snap, err := db.ReaderFrom(ctx).Get(ctx, tuple)
	if err != nil && !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("Get: %v", err)
	}
	if snap != nil && snap.Exists() {
		t.Fatalf("%s:%s#%s <- %s:%s: the row must not exist", row.ResourceType, row.ResourceID, row.Relation, row.SubjectType, row.SubjectID)
	}
}

// TestSetRelationTargetsConflictWritesNothing is the rollback case in its
// sharpest form: the desired set mixes a conflicting target with a BRAND NEW
// one, so a store that wrote as it went would leave the new tuple's three
// documents behind. sdk.ErrConflict must leave the resource exactly as it was.
func TestSetRelationTargetsConflictWritesNothing(t *testing.T) {
	ctx := context.Background()
	db, s := newRelationships(t)

	if err := s.CreateRelationships(ctx, []relationship.CreateRelationship{
		ctf("space", "child", "parent", "space", "keep"),
		ctf("space", "child", "owner", "space", "occupied"),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	err := s.SetRelationTargets(ctx, "space", "child", "parent", []relationship.CreateRelationship{
		ctf("space", "child", "parent", "space", "keep"),
		ctf("space", "child", "parent", "space", "fresh"),    // would be created
		ctf("space", "child", "parent", "space", "occupied"), // holds `owner`
	})
	if !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("a target holding another relation must be sdk.ErrConflict, got %v", err)
	}

	targets, err := s.GetRelationTargets(ctx, "space", "child", "parent")
	if err != nil || len(targets) != 1 || targets[0].ID != "keep" {
		t.Fatalf("the rolled-back reconciliation changed state: %+v err=%v", targets, err)
	}
	// The would-be new tuple left NO document behind — row or claim.
	assertTupleDocs(t, db, relationshipDoc{
		ResourceType: "space", ResourceID: "child", Relation: "parent", SubjectType: "space", SubjectID: "fresh",
		RelationshipID: "never-minted",
	}, false)
	if n, _ := s.CountByResourceAndRelation(ctx, "space", "child", "owner"); n != 1 {
		t.Fatalf("the occupied target's own relation must survive, got %d owner rows", n)
	}
}

// TestSetRelationTargetsConcurrentDisjointSetsConverge is the serialization
// case from an EMPTY resource, where a store without real atomicity produces a
// UNION rather than a winner. Two callers, so the run is bounded; the emulator
// aborts the loser's commit and the vendor re-runs its callback, which then
// sees the winner's row as surplus and removes it.
func TestSetRelationTargetsConcurrentDisjointSetsConverge(t *testing.T) {
	ctx := context.Background()
	db, s := newRelationships(t)

	var wg sync.WaitGroup
	for _, id := range []string{"a", "b"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.SetRelationTargets(ctx, "space", "child", "parent", []relationship.CreateRelationship{
				ctf("space", "child", "parent", "space", id),
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
		t.Fatalf("concurrent disjoint desired states unioned instead of converging: %+v", targets)
	}
	winner, loser := targets[0].ID, "b"
	if winner == "b" {
		loser = "a"
	}
	if winner != "a" && winner != "b" {
		t.Fatalf("winner %q is neither caller's desired state", winner)
	}
	// The loser's tuple is gone COMPLETELY — a surviving claim would make the
	// state unrepairable by a later reconciliation.
	assertTupleDocs(t, db, relationshipDoc{
		ResourceType: "space", ResourceID: "child", Relation: "parent", SubjectType: "space", SubjectID: loser,
		RelationshipID: "never-minted",
	}, false)
}

// TestWriteLimitRefusesBeforeWriting proves the transaction budget is a
// PRECONDITION, not a partial commit: an oversized create writes nothing, and
// an oversized delete leaves every row in place. Both answer ErrTupleWriteLimit
// (sdk.ErrInvalidInput) rather than splitting the operation.
func TestWriteLimitRefusesBeforeWriting(t *testing.T) {
	ctx := context.Background()
	_, s := newRelationships(t)

	oversized := make([]relationship.CreateRelationship, 0, maxTuplesPerTransaction+1)
	for i := 0; i <= maxTuplesPerTransaction; i++ {
		oversized = append(oversized, ctf("doc", "big", "viewer", "user", docID("u", i)))
	}
	err := s.CreateRelationships(ctx, oversized)
	if !errors.Is(err, ErrTupleWriteLimit) || !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("oversized create: want ErrTupleWriteLimit wrapping sdk.ErrInvalidInput, got %v", err)
	}
	if n, _ := s.CountByResourceAndRelation(ctx, "doc", "big", "viewer"); n != 0 {
		t.Fatalf("a refused batch wrote %d rows", n)
	}

	// Seed one tuple past the limit in two accepted batches, then prove the
	// delete refuses the whole resource rather than removing part of it.
	if err := s.CreateRelationships(ctx, oversized[:maxTuplesPerTransaction]); err != nil {
		t.Fatalf("seed batch 1: %v", err)
	}
	if err := s.CreateRelationships(ctx, oversized[maxTuplesPerTransaction:]); err != nil {
		t.Fatalf("seed batch 2: %v", err)
	}
	total := len(oversized)
	if n, _ := s.CountByResourceAndRelation(ctx, "doc", "big", "viewer"); n != total {
		t.Fatalf("seeded %d rows, want %d", n, total)
	}

	if err := s.DeleteResourceRelationships(ctx, "doc", "big"); !errors.Is(err, ErrTupleWriteLimit) {
		t.Fatalf("oversized delete: want ErrTupleWriteLimit, got %v", err)
	}
	if n, _ := s.CountByResourceAndRelation(ctx, "doc", "big", "viewer"); n != total {
		t.Fatalf("a refused delete changed the state: %d rows left, want %d", n, total)
	}
	// A delete that FITS still works on the same resource, so the refusal is
	// about size and nothing else.
	if err := s.DeleteRelationship(ctx, "doc", "big", "viewer", "user", docID("u", 0)); err != nil {
		t.Fatalf("in-budget delete: %v", err)
	}
	if n, _ := s.CountByResourceAndRelation(ctx, "doc", "big", "viewer"); n != total-1 {
		t.Fatalf("in-budget delete removed %d rows", total-n)
	}
}
