//go:build integration && !live

package firestore

import (
	"context"
	"slices"
	"sort"
	"testing"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
)

// A2b: the two core v0.12.0 set reads (FilterRelation, RelationTargetsFor) and
// the batch direct check, all three over ONE shared expansion and one query per
// candidate CHUNK. The parity cases mirror storetest/setreads.go, which cannot
// run yet because it seeds through CreateRelationships (A2c).

// setReadIDUniverse is storetest's byte-order trap plus two ids that are not
// stored at all, so a store cannot pass by echoing its input.
var setReadIDUniverse = []string{"B", "a", "_x", "~z", "Z", "é", "absent1", "absent2"}

// TestFilterRelationIsSortedDistinctSubset pins the three shape guarantees.
func TestFilterRelationIsSortedDistinctSubset(t *testing.T) {
	db, s := newRelationships(t)
	ctx := context.Background()

	tuples := make([]relationships.CreateRelationship, 0, len(setReadIDUniverse))
	held := []string{"B", "a", "_x", "~z", "Z", "é"}
	for _, id := range held {
		tuples = append(tuples, ctf("doc", id, "viewer", "user", "u1"))
	}
	tuples = append(tuples, ctf("doc", "denied", "viewer", "user", "u2"), ctf("doc", "other", "editor", "user", "u1"))
	seedTuples(t, db, tuples...)

	in := append(slices.Clone(setReadIDUniverse), "denied", "other", "a", "B")
	got, err := s.FilterRelation(ctx, "doc", in, "viewer", "user", "u1", 0)
	if err != nil {
		t.Fatalf("FilterRelation: %v", err)
	}
	want := slices.Clone(held)
	sort.Strings(want)
	if !slices.Equal(got, want) {
		t.Fatalf("FilterRelation must return the held ids in BYTE order exactly once: want %v, got %v", want, got)
	}
	assertSubsetSortedDistinct(t, "FilterRelation", in, got)
}

// TestFilterRelationMatchesPerResourceCheck is the oracle parity storetest
// asserts: the set read answers exactly what a loop over
// CheckRelationWithGroupExpansion answers, on every id.
func TestFilterRelationMatchesPerResourceCheck(t *testing.T) {
	db, s := newRelationships(t)
	ctx := context.Background()
	seedTuples(t, db,
		ctf("group", "outer", "member", "user", "u1"),
		ctfUserset("group", "inner", "member", "group", "outer", "member"),
		ctfUserset("doc", "B", "viewer", "group", "inner", "member"),
		ctf("doc", "a", "viewer", "user", "u1"),
		ctfUserset("doc", "Z", "viewer", "group", "inner", "admin"),
		ctf("doc", "~z", "viewer", "group", "inner"),
	)

	got, err := s.FilterRelation(ctx, "doc", setReadIDUniverse, "viewer", "user", "u1", 0)
	if err != nil {
		t.Fatalf("FilterRelation: %v", err)
	}
	admitted := make(map[string]bool, len(got))
	for _, id := range got {
		admitted[id] = true
	}
	batch, err := s.CheckBatchDirect(ctx, "doc", setReadIDUniverse, "viewer", "user", "u1", 0)
	if err != nil {
		t.Fatalf("CheckBatchDirect: %v", err)
	}
	for _, id := range setReadIDUniverse {
		want, err := s.CheckRelationWithGroupExpansion(ctx, "doc", id, "viewer", "user", "u1", 0)
		if err != nil {
			t.Fatalf("CheckRelationWithGroupExpansion(%s): %v", id, err)
		}
		if admitted[id] != want {
			t.Fatalf("FilterRelation disagrees with the per-resource check on doc:%s: set says %v, per-resource says %v", id, admitted[id], want)
		}
		if batch[id] != want {
			t.Fatalf("CheckBatchDirect disagrees with the per-resource check on doc:%s: batch says %v, per-resource says %v", id, batch[id], want)
		}
	}
	if want := []string{"B", "a"}; !slices.Equal(got, want) {
		t.Fatalf("group expansion must admit exactly the member-reached and direct docs: want %v, got %v", want, got)
	}
}

// TestSetReadsEmptyInputPerformNoIO proves the early returns with a CLOSED
// database handle: any read would fail on the wire.
func TestSetReadsEmptyInputPerformNoIO(t *testing.T) {
	ctx := context.Background()
	s := newRelationshipStore(closedDB(t), false)

	for _, ids := range [][]string{nil, {}} {
		got, err := s.FilterRelation(ctx, "doc", ids, "viewer", "user", "u1", 0)
		if err != nil || len(got) != 0 {
			t.Fatalf("FilterRelation over %v must be an empty result and no I/O: got %v err=%v", ids, got, err)
		}
		targets, err := s.RelationTargetsFor(ctx, "space", ids, "parent")
		if err != nil || len(targets) != 0 {
			t.Fatalf("RelationTargetsFor over %v must be an empty map and no I/O: got %v err=%v", ids, targets, err)
		}
		batch, err := s.CheckBatchDirect(ctx, "doc", ids, "viewer", "user", "u1", 0)
		if err != nil || len(batch) != 0 {
			t.Fatalf("CheckBatchDirect over %v must be an empty map and no I/O: got %v err=%v", ids, batch, err)
		}
	}
}

// TestCheckBatchDirectAnswersEveryRequestedID: every requested id is a key of the
// result (default false), and a duplicated id costs nothing.
func TestCheckBatchDirectAnswersEveryRequestedID(t *testing.T) {
	db, s := newRelationships(t)
	ctx := context.Background()
	seedTuples(t, db, ctf("doc", "d1", "viewer", "user", "u1"), ctf("doc", "d2", "viewer", "user", "u2"))

	got, err := s.CheckBatchDirect(ctx, "doc", []string{"d1", "d2", "d1", "absent"}, "viewer", "user", "u1", 0)
	if err != nil {
		t.Fatalf("CheckBatchDirect: %v", err)
	}
	want := map[string]bool{"d1": true, "d2": false, "absent": false}
	if len(got) != len(want) {
		t.Fatalf("CheckBatchDirect = %v, want one entry per DISTINCT requested id %v", got, want)
	}
	for id, allowed := range want {
		if got[id] != allowed {
			t.Fatalf("CheckBatchDirect[%s] = %v, want %v", id, got[id], allowed)
		}
	}
}

// TestRelationTargetsForMatchesPerResource is the second oracle parity: the set
// form agrees with GetRelationTargets on every id, omits ids with no targets,
// folds duplicated ids, and returns usersets as stored.
func TestRelationTargetsForMatchesPerResource(t *testing.T) {
	db, s := newRelationships(t)
	ctx := context.Background()
	seedTuples(t, db,
		ctf("space", "a", "parent", "space", "root"),
		ctf("space", "B", "parent", "space", "root"),
		ctf("space", "B", "parent", "space", "second"),
		ctfUserset("space", "Z", "parent", "group", "g", "member"),
		ctf("space", "a", "viewer", "user", "u1"),
	)

	in := append(slices.Clone(setReadIDUniverse), "a", "B")
	got, err := s.RelationTargetsFor(ctx, "space", in, "parent")
	if err != nil {
		t.Fatalf("RelationTargetsFor: %v", err)
	}
	for _, id := range setReadIDUniverse {
		want, err := s.GetRelationTargets(ctx, "space", id, "parent")
		if err != nil {
			t.Fatalf("GetRelationTargets(%s): %v", id, err)
		}
		if !sameTargets(got[id], want) {
			t.Fatalf("RelationTargetsFor disagrees with GetRelationTargets on space:%s: set says %v, per-resource says %v", id, got[id], want)
		}
	}
	for id, targets := range got {
		if len(targets) == 0 {
			t.Fatalf("space:%s carries an entry with no targets: such an id must be ABSENT", id)
		}
	}
	if _, present := got["absent1"]; present {
		t.Fatalf("an id with no targets must be ABSENT from the map, got %v", got["absent1"])
	}
	if len(got["Z"]) != 1 || !got["Z"][0].IsUserset() {
		t.Fatalf("a userset target must be returned AS STORED, got %v", got["Z"])
	}
	if len(got["B"]) != 2 {
		t.Fatalf("a duplicated input id must carry ONE entry with both targets, got %v", got["B"])
	}
}

// TestSetReadsChunkPastDisjunctionCapAndIssueNoPerCandidateReads drives a
// candidate set larger than Firestore's 30-disjunction cap. It asserts BOTH that
// the chunks are merged (not truncated, not rejected) and — through a counting
// Reader at the connector seam — that the number of queries is one per chunk
// plus the shared expansion, never one per candidate.
func TestSetReadsChunkPastDisjunctionCapAndIssueNoPerCandidateReads(t *testing.T) {
	db, s := newRelationships(t)
	ctx := context.Background()

	const candidates = 65
	ids := make([]string, 0, 2*candidates)
	var want []string
	tuples := make([]relationships.CreateRelationship, 0, candidates)
	for i := 0; i < candidates; i++ {
		id := docID("d", i)
		ids = append(ids, id, id) // every id twice: the fold is proven at scale
		holder := "u2"
		if i%2 == 0 {
			holder = "u1"
			want = append(want, id)
		}
		tuples = append(tuples, ctf("doc", id, "viewer", "user", holder))
	}
	seedTuples(t, db, tuples...)
	sort.Strings(want)

	got, err := s.FilterRelation(ctx, "doc", ids, "viewer", "user", "u1", 0)
	if err != nil {
		t.Fatalf("FilterRelation over %d ids: %v", len(ids), err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("an id list past the 30-disjunction cap must be CHUNKED and MERGED: got %d ids, want %d", len(got), len(want))
	}
	assertSubsetSortedDistinct(t, "FilterRelation", ids, got)

	targets, err := s.RelationTargetsFor(ctx, "doc", ids, "viewer")
	if err != nil {
		t.Fatalf("RelationTargetsFor over %d ids: %v", len(ids), err)
	}
	if len(targets) != candidates {
		t.Fatalf("a chunked set read must answer every DISTINCT id once: got %d entries, want %d", len(targets), candidates)
	}

	batch, err := s.CheckBatchDirect(ctx, "doc", ids, "viewer", "user", "u1", 0)
	if err != nil {
		t.Fatalf("CheckBatchDirect over %d ids: %v", len(ids), err)
	}
	for _, id := range want {
		if !batch[id] {
			t.Fatalf("CheckBatchDirect[%s] = false, want true", id)
		}
	}

	// The read budget. u1's expansion reaches the seed plus the 33 doc#viewer
	// states its own grants create, so the walk is 1 + ceil(33/30) = 3 queries;
	// the candidate set is ceil(65/30) = 3 more. Six reads for 65 candidates —
	// a per-candidate implementation would need 65.
	distinct := distinctSortedIDs(ids)
	var counter *countingReader
	if err := db.ReadSnapshot(ctx, func(ctx context.Context, r firestoredb.Reader) error {
		counter = &countingReader{reader: r}
		_, err := filterRelation(ctx, db, counter, "doc", distinct, "viewer", "user", "u1", 0)
		return err
	}); err != nil {
		t.Fatalf("filterRelation: %v", err)
	}
	if counter.queries != 6 {
		t.Fatalf("FilterRelation issued %d queries over %d candidates, want 6 (3 expansion chunks + 3 candidate chunks)", counter.queries, len(distinct))
	}

	counter = nil
	if err := db.ReadSnapshot(ctx, func(ctx context.Context, r firestoredb.Reader) error {
		counter = &countingReader{reader: r}
		return scanCandidates(ctx, db, counter, "doc", distinct, "viewer", func(relationshipDoc) {})
	}); err != nil {
		t.Fatalf("scanCandidates: %v", err)
	}
	if counter.queries != 3 {
		t.Fatalf("the candidate scan issued %d queries over %d candidates, want 3 chunks", counter.queries, len(distinct))
	}
}

// assertSubsetSortedDistinct is storetest's shape assertion, local because the
// suite cannot run against this store until its write side lands.
func assertSubsetSortedDistinct(t *testing.T, name string, in, got []string) {
	t.Helper()
	if !sort.StringsAreSorted(got) {
		t.Fatalf("%s output must be sorted in byte order, got %v", name, got)
	}
	seen := make(map[string]bool, len(got))
	input := make(map[string]bool, len(in))
	for _, id := range in {
		input[id] = true
	}
	for _, id := range got {
		if seen[id] {
			t.Fatalf("%s returned %q twice: the output is DISTINCT", name, id)
		}
		seen[id] = true
		if !input[id] {
			t.Fatalf("%s returned %q, which is not in the input: the output is a SUBSET", name, id)
		}
	}
}
