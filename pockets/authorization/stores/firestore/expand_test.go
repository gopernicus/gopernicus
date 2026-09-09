//go:build integration && !live

package firestore

import (
	"context"
	"errors"
	"slices"
	"testing"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/integrations/datastores/firestore/firestoretest"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/relationship"
)

// A2a: the snapshot-bound expansion (A-D2 / ruling R2) and the four reads built
// directly on it — the expanded check, the direct check, the relation targets,
// and the direct-only count.

// TestExpandBudgetThreshold pins the memstore's budget rule EXACTLY: overflow is
// "adding this state would push the distinct count PAST the budget", so a graph
// whose reachable set is exactly the budget succeeds and one state more fails.
// Never a truncated set, never a deny.
func TestExpandBudgetThreshold(t *testing.T) {
	db, _ := newRelationships(t)
	// u1 -> g1#member -> g2#member -> g3#member: four distinct states with the seed.
	seedTuples(t, db,
		ctf("group", "g1", "member", "user", "u1"),
		ctfUserset("group", "g2", "member", "group", "g1", "member"),
		ctfUserset("group", "g3", "member", "group", "g2", "member"),
	)
	const states = 4

	for _, budget := range []int{states, states + 1, 0, -1} {
		reached, err := expandUnderSnapshot(t, db, "user", "u1", budget)
		if err != nil {
			t.Fatalf("budget %d: want the full walk, got %v", budget, err)
		}
		if len(reached) != states {
			t.Fatalf("budget %d: reached %d states, want %d", budget, len(reached), states)
		}
	}
	for _, budget := range []int{states - 1, 2, 1} {
		if _, err := expandUnderSnapshot(t, db, "user", "u1", budget); !errors.Is(err, relationship.ErrExpansionBudgetExceeded) {
			t.Fatalf("budget %d: want ErrExpansionBudgetExceeded, got %v", budget, err)
		}
	}
}

// TestExpandBudgetSurfacesThroughTheCheck proves the sentinel is what the PORT
// answers, not something the walk swallows into a deny.
func TestExpandBudgetSurfacesThroughTheCheck(t *testing.T) {
	ctx := context.Background()
	db, s := newRelationships(t)
	seedTuples(t, db,
		ctf("group", "g1", "member", "user", "u1"),
		ctfUserset("group", "g2", "member", "group", "g1", "member"),
		ctfUserset("doc", "d1", "viewer", "group", "g2", "member"),
	)

	// Four distinct states: the seed, g1#member, g2#member, and — because the
	// grant is itself an edge out of g2#member — doc:d1#viewer.
	if ok, err := s.CheckRelationWithGroupExpansion(ctx, "doc", "d1", "viewer", "user", "u1", 4); err != nil || !ok {
		t.Fatalf("budget 4 fits the four states: ok=%v err=%v", ok, err)
	}
	ok, err := s.CheckRelationWithGroupExpansion(ctx, "doc", "d1", "viewer", "user", "u1", 3)
	if !errors.Is(err, relationship.ErrExpansionBudgetExceeded) {
		t.Fatalf("budget 3 must be indeterminate, got ok=%v err=%v", ok, err)
	}
	if ok {
		t.Fatalf("an overflowed check must never report allowed")
	}
	if _, err := s.FilterRelation(ctx, "doc", []string{"d1"}, "viewer", "user", "u1", 3); !errors.Is(err, relationship.ErrExpansionBudgetExceeded) {
		t.Fatalf("FilterRelation overflow must fail the whole call, got %v", err)
	}
	if _, err := s.CheckBatchDirect(ctx, "doc", []string{"d1"}, "viewer", "user", "u1", 3); !errors.Is(err, relationship.ErrExpansionBudgetExceeded) {
		t.Fatalf("CheckBatchDirect overflow must fail the whole call, got %v", err)
	}
}

// TestExpandTerminatesOnCycles: a membership cycle is walked once, not forever.
func TestExpandTerminatesOnCycles(t *testing.T) {
	db, s := newRelationships(t)
	ctx := context.Background()
	seedTuples(t, db,
		ctf("group", "a", "member", "user", "u1"),
		ctfUserset("group", "b", "member", "group", "a", "member"),
		ctfUserset("group", "a", "member", "group", "b", "member"),
		ctfUserset("doc", "d1", "viewer", "group", "b", "member"),
	)

	reached, err := expandUnderSnapshot(t, db, "user", "u1", 0)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	// seed, a#member, b#member, and doc:d1#viewer — the grant is an edge too.
	if len(reached) != 4 {
		t.Fatalf("cycle walked to %d states, want 4 (seed, a#member, b#member, d1#viewer)", len(reached))
	}
	if ok, err := s.CheckRelationWithGroupExpansion(ctx, "doc", "d1", "viewer", "user", "u1", 0); err != nil || !ok {
		t.Fatalf("cycle must not hide the grant: ok=%v err=%v", ok, err)
	}
}

// TestExpandChunksFrontierPastDisjunctionCap drives a frontier LARGER than
// Firestore's 30-disjunction cap through two hops. A store that put the whole
// frontier in one `in` would be refused by the server with InvalidArgument; a
// store that issued one query per frontier entry would need 70. The query count
// is asserted, so both failures are caught.
func TestExpandChunksFrontierPastDisjunctionCap(t *testing.T) {
	db, _ := newRelationships(t)
	const groups = 35

	tuples := make([]relationship.CreateRelationship, 0, 2*groups)
	for i := 0; i < groups; i++ {
		g := docID("g", i)
		tuples = append(tuples,
			ctf("group", g, "member", "user", "u1"),
			ctfUserset("org", docID("o", i), "member", "group", g, "member"),
		)
	}
	seedTuples(t, db, tuples...)

	var (
		reached map[string]struct{}
		counter *countingReader
	)
	if err := db.ReadSnapshot(context.Background(), func(ctx context.Context, r firestoredb.Reader) error {
		counter = &countingReader{reader: r}
		var err error
		reached, err = expand(ctx, db, counter, "user", "u1", 0)
		return err
	}); err != nil {
		t.Fatalf("expand: %v", err)
	}

	if want := 1 + 2*groups; len(reached) != want {
		t.Fatalf("reached %d states, want %d (seed + %d group#member + %d org#member)", len(reached), want, groups, groups)
	}
	// hop 1: one chunk (the seed). hop 2: ceil(35/30) = 2 chunks over the group
	// states. hop 3: 2 chunks over the org states, which reach nothing.
	if want := 5; counter.queries != want {
		t.Fatalf("expansion issued %d queries, want %d (one per frontier CHUNK, never one per state)", counter.queries, want)
	}
}

// TestSnapshotBoundExpansionDoesNotStraddleRevokeAndGrant is the interleaving
// A-D2 names: read membership, then have another writer revoke that membership
// and grant the group access, then finish the check. No single instant ever
// authorized u1, so a snapshot-bound check must deny.
//
// The unsnapshotted arm runs the SAME handshake through the client reader and
// asserts the anomaly it produces — otherwise a green would not distinguish "the
// snapshot held" from "the interleaving never happened".
func TestSnapshotBoundExpansionDoesNotStraddleRevokeAndGrant(t *testing.T) {
	db, _ := newRelationships(t)
	ctx := context.Background()

	// u1 is a member of g. The doc grant does NOT exist yet.
	membership := seedTuples(t, db, ctf("group", "g", "member", "user", "u1"))
	grant := ctfUserset("doc", "d1", "viewer", "group", "g", "member")

	// interleave runs expansion, hands control to a writer that revokes the
	// membership and adds the grant, and only then runs the final match.
	interleave := func(ctx context.Context, r firestoredb.Reader) bool {
		t.Helper()
		reached, err := expand(ctx, db, r, "user", "u1", 0)
		if err != nil {
			t.Fatalf("expand: %v", err)
		}
		if len(reached) != 2 {
			t.Fatalf("the walk must observe the membership before the interleave, reached %d states", len(reached))
		}

		done := make(chan struct{})
		go func() {
			defer close(done)
			dropTuples(t, db, membership...)
			seedTuples(t, db, grant)
		}()
		<-done

		ok, err := anyTupleWithSubject(ctx, db, r, "doc", "d1", "viewer", reached)
		if err != nil {
			t.Fatalf("match: %v", err)
		}
		return ok
	}

	var snapshotted bool
	if err := db.ReadSnapshot(ctx, func(ctx context.Context, r firestoredb.Reader) error {
		snapshotted = interleave(ctx, r)
		return nil
	}); err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	if snapshotted {
		t.Fatalf("a snapshot-bound check authorized a path that existed at no single instant")
	}

	// Restore the fixture and run the same interleaving with the CLIENT reader,
	// whose every read is its own instant. It must show the anomaly — otherwise
	// the arm above would pass on a store that never interleaved at all.
	firestoretest.Reset(t, db)
	membership = seedTuples(t, db, ctf("group", "g", "member", "user", "u1"))
	if unsnapshotted := interleave(ctx, db.ReaderFrom(ctx)); !unsnapshotted {
		t.Fatalf("the unsnapshotted arm did not reproduce the straddle, so the snapshot arm proves nothing")
	}
}

// TestCheckRelationExpansionIsRelationAware: a group#admin grant is never
// satisfied by group#member membership, and a CONCRETE group grant is satisfied
// only by the group entity itself.
func TestCheckRelationExpansionIsRelationAware(t *testing.T) {
	db, s := newRelationships(t)
	ctx := context.Background()
	seedTuples(t, db,
		ctf("group", "g", "member", "user", "u1"),
		ctfUserset("doc", "member_doc", "viewer", "group", "g", "member"),
		ctfUserset("doc", "admin_doc", "viewer", "group", "g", "admin"),
		ctf("doc", "concrete_doc", "viewer", "group", "g"),
		ctf("doc", "direct_doc", "viewer", "user", "u1"),
	)

	for _, tc := range []struct {
		doc  string
		want bool
	}{
		{"member_doc", true},
		{"admin_doc", false},
		{"concrete_doc", false},
		{"direct_doc", true},
		{"absent_doc", false},
	} {
		got, err := s.CheckRelationWithGroupExpansion(ctx, "doc", tc.doc, "viewer", "user", "u1", 0)
		if err != nil {
			t.Fatalf("doc:%s: %v", tc.doc, err)
		}
		if got != tc.want {
			t.Fatalf("CheckRelationWithGroupExpansion(doc:%s) = %v, want %v", tc.doc, got, tc.want)
		}
	}
}

// TestCheckRelationExistsIsDirectAndConcrete: no expansion, and a stored USERSET
// tuple with the same type/id does not satisfy a concrete probe.
func TestCheckRelationExistsIsDirectAndConcrete(t *testing.T) {
	db, s := newRelationships(t)
	ctx := context.Background()
	seedTuples(t, db,
		ctf("doc", "d1", "owner", "user", "u1"),
		ctf("group", "g", "member", "user", "u2"),
		ctfUserset("doc", "d1", "viewer", "group", "g", "member"),
	)

	for _, tc := range []struct {
		name                                      string
		rt, rid, relation, subjectType, subjectID string
		want                                      bool
	}{
		{"direct tuple", "doc", "d1", "owner", "user", "u1", true},
		{"other relation", "doc", "d1", "viewer", "user", "u1", false},
		{"other subject", "doc", "d1", "owner", "user", "u2", false},
		{"absent resource", "doc", "d2", "owner", "user", "u1", false},
		{"no expansion", "doc", "d1", "viewer", "user", "u2", false},
		{"userset tuple is not a concrete tuple", "doc", "d1", "viewer", "group", "g", false},
	} {
		got, err := s.CheckRelationExists(ctx, tc.rt, tc.rid, tc.relation, tc.subjectType, tc.subjectID)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != tc.want {
			t.Fatalf("%s: CheckRelationExists = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestGetRelationTargetsPreservesUsersets: targets come back as stored, so a
// userset reference keeps its relation and a concrete subject keeps its empty one.
func TestGetRelationTargetsPreservesUsersets(t *testing.T) {
	db, s := newRelationships(t)
	ctx := context.Background()
	seedTuples(t, db,
		ctf("space", "child", "parent", "space", "root"),
		ctfUserset("space", "child", "parent", "group", "g", "member"),
		ctf("space", "child", "viewer", "user", "u1"),
		ctf("space", "other", "parent", "space", "elsewhere"),
	)

	got, err := s.GetRelationTargets(ctx, "space", "child", "parent")
	if err != nil {
		t.Fatalf("GetRelationTargets: %v", err)
	}
	want := []relationship.RelationTarget{
		{Type: "space", ID: "root"},
		{Type: "group", ID: "g", Relation: "member"},
	}
	if !sameTargets(got, want) {
		t.Fatalf("GetRelationTargets = %v, want %v as a set", got, want)
	}
	if empty, err := s.GetRelationTargets(ctx, "space", "absent", "parent"); err != nil || len(empty) != 0 {
		t.Fatalf("an absent resource has no targets: %v err=%v", empty, err)
	}
}

// TestCountByResourceAndRelationCountsDirectTuplesOnly is the last-owner security
// pin: expanded membership must never inflate the count.
func TestCountByResourceAndRelationCountsDirectTuplesOnly(t *testing.T) {
	db, s := newRelationships(t)
	ctx := context.Background()
	seedTuples(t, db,
		ctf("doc", "d1", "owner", "user", "u1"),
		ctf("doc", "d1", "owner", "user", "u2"),
		ctfUserset("doc", "d1", "owner", "group", "g", "member"),
		ctf("group", "g", "member", "user", "u3"),
		ctf("group", "g", "member", "user", "u4"),
		ctf("doc", "d1", "viewer", "user", "u5"),
		ctf("doc", "d2", "owner", "user", "u1"),
	)

	n, err := s.CountByResourceAndRelation(ctx, "doc", "d1", "owner")
	if err != nil {
		t.Fatalf("CountByResourceAndRelation: %v", err)
	}
	if n != 3 {
		t.Fatalf("count = %d, want 3 direct tuples (never the two expanded group members)", n)
	}
	if n, err := s.CountByResourceAndRelation(ctx, "doc", "absent", "owner"); err != nil || n != 0 {
		t.Fatalf("absent resource count = %d err=%v, want 0", n, err)
	}
}

// expandUnderSnapshot runs one walk inside its own snapshot, the way every public
// caller does.
func expandUnderSnapshot(t *testing.T, db *firestoredb.DB, subjectType, subjectID string, budget int) (map[string]struct{}, error) {
	t.Helper()
	var reached map[string]struct{}
	err := db.ReadSnapshot(context.Background(), func(ctx context.Context, r firestoredb.Reader) error {
		var err error
		reached, err = expand(ctx, db, r, subjectType, subjectID, budget)
		return err
	})
	return reached, err
}

// sameTargets compares two target lists as SETS: the port fixes no order within
// one resource's targets.
func sameTargets(a, b []relationship.RelationTarget) bool {
	if len(a) != len(b) {
		return false
	}
	remaining := slices.Clone(b)
	for _, want := range a {
		i := slices.Index(remaining, want)
		if i < 0 {
			return false
		}
		remaining = slices.Delete(remaining, i, i+1)
	}
	return true
}
