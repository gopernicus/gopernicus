package storetest

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
)

// setReadIDs is the id universe the set-read cases page over. It is deliberately
// the keyset universe's byte-order trap (B, Z, _x, a, ~z, é) plus two ids that
// are NOT stored at all, so a store cannot pass by returning its input.
var setReadIDs = append(slices.Clone(keysetIDs), "absent1", "absent2")

// overBoundIDs is larger than any store's per-statement id bound (the turso
// adapter chunks at 500; the pgx adapter binds one array and needs no chunk) and
// larger than a page a host would realistically filter, so a chunking store must
// merge its chunks correctly to pass. It stays under DefaultMaxBatchSize so the
// engine-level ceiling is not what is being proven here.
const overBoundIDs = 900

// seedChunk bounds one CreateRelationships call in the over-bound fixtures: the
// multi-row INSERT dialects bind a handful of parameters per row, so the FIXTURE
// chunks even where the read under test does not.
const seedChunk = 100

// runRelationshipSetReads is the Relationship/SetReads family: the contract of
// the optional RelationSetReader methods and their parity with the required
// per-resource siblings. Only these optional subtests skip when unsupported;
// the ordinary relationship, model-scoping and budget families still run.
func runRelationshipSetReads(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	ctx := context.Background()

	t.Run("FilterRelationIsSortedDistinctSubset", func(t *testing.T) {
		s := newRepos(t).Relationships
		sets := requireRelationSetReader(t, s)
		tuples := make([]relationships.CreateRelationship, 0, len(keysetIDs))
		for _, id := range keysetIDs {
			tuples = append(tuples, ct("doc", id, "viewer", "user", "u1"))
		}
		// A doc the principal does NOT hold the relation on, and a doc held under
		// ANOTHER relation: neither may be admitted.
		tuples = append(tuples, ct("doc", "denied", "viewer", "user", "u2"), ct("doc", "other", "editor", "user", "u1"))
		mustCreate(t, s, tuples...)

		in := append(slices.Clone(setReadIDs), "denied", "other")
		got, err := sets.FilterRelation(ctx, "doc", in, "viewer", "user", "u1", 0)
		if err != nil {
			t.Fatalf("FilterRelation: %v", err)
		}
		if want := keysetOrder(); !slices.Equal(got, want) {
			t.Fatalf("FilterRelation must return the held ids in BYTE order: want %v, got %v", want, got)
		}
		assertSubsetSortedDistinct(t, "FilterRelation", in, got)
	})

	t.Run("FilterRelationMatchesPerResourceCheck", func(t *testing.T) {
		s := newRepos(t).Relationships
		sets := requireRelationSetReader(t, s)
		// A userset grant reached through nested groups, a concrete grant, an
		// exact-userset mismatch (the #admin grant u1 cannot satisfy), and ids with
		// no tuple at all: the set read must agree with the per-resource check on
		// every one of them.
		mustCreate(t, s,
			ct("group", "outer", "member", "user", "u1"),
			ctUserset("group", "inner", "member", "group", "outer", "member"),
			ctUserset("doc", "B", "viewer", "group", "inner", "member"),
			ct("doc", "a", "viewer", "user", "u1"),
			ctUserset("doc", "Z", "viewer", "group", "inner", "admin"),
			ct("doc", "~z", "viewer", "group", "inner"),
		)

		got, err := sets.FilterRelation(ctx, "doc", setReadIDs, "viewer", "user", "u1", 0)
		if err != nil {
			t.Fatalf("FilterRelation: %v", err)
		}
		admitted := make(map[string]bool, len(got))
		for _, id := range got {
			admitted[id] = true
		}
		for _, id := range setReadIDs {
			want, err := s.CheckRelationWithGroupExpansion(ctx, "doc", id, "viewer", "user", "u1", 0)
			if err != nil {
				t.Fatalf("CheckRelationWithGroupExpansion(%s): %v", id, err)
			}
			if admitted[id] != want {
				t.Fatalf("FilterRelation disagrees with CheckRelationWithGroupExpansion on doc:%s: set says %v, per-resource says %v", id, admitted[id], want)
			}
		}
		if want := []string{"B", "a"}; !slices.Equal(got, want) {
			t.Fatalf("group expansion must admit exactly the member-reached and direct docs: want %v, got %v", want, got)
		}
	})

	t.Run("FilterRelationEmptyInput", func(t *testing.T) {
		s := newRepos(t).Relationships
		sets := requireRelationSetReader(t, s)
		mustCreate(t, s, ct("doc", "d1", "viewer", "user", "u1"))
		got, err := sets.FilterRelation(ctx, "doc", nil, "viewer", "user", "u1", 0)
		if err != nil || len(got) != 0 {
			t.Fatalf("FilterRelation over no ids must be an empty result: got %v err=%v", got, err)
		}
		got, err = sets.FilterRelation(ctx, "doc", []string{}, "viewer", "user", "u1", 0)
		if err != nil || len(got) != 0 {
			t.Fatalf("FilterRelation over an empty slice must be an empty result: got %v err=%v", got, err)
		}
	})

	t.Run("FilterRelationFoldsDuplicateIDs", func(t *testing.T) {
		s := newRepos(t).Relationships
		sets := requireRelationSetReader(t, s)
		mustCreate(t, s, ct("doc", "d1", "viewer", "user", "u1"), ct("doc", "d2", "viewer", "user", "u1"))
		in := []string{"d2", "d1", "d2", "d1", "d1", "absent", "absent"}
		got, err := sets.FilterRelation(ctx, "doc", in, "viewer", "user", "u1", 0)
		if err != nil {
			t.Fatalf("FilterRelation: %v", err)
		}
		if want := []string{"d1", "d2"}; !slices.Equal(got, want) {
			t.Fatalf("a repeated input id must be answered ONCE: want %v, got %v", want, got)
		}
		assertSubsetSortedDistinct(t, "FilterRelation", in, got)
	})

	t.Run("FilterRelationOverBatchBound", func(t *testing.T) {
		s := newRepos(t).Relationships
		sets := requireRelationSetReader(t, s)
		ids, want := seedOverBoundDocs(t, s)
		got, err := sets.FilterRelation(ctx, "doc", ids, "viewer", "user", "u1", 0)
		if err != nil {
			t.Fatalf("FilterRelation over %d ids: %v", len(ids), err)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("an id list past the store's statement bound must be chunked and MERGED, not truncated: got %d ids, want %d", len(got), len(want))
		}
		assertSubsetSortedDistinct(t, "FilterRelation", ids, got)
	})

	t.Run("RelationTargetsForMatchesPerResource", func(t *testing.T) {
		s := newRepos(t).Relationships
		sets := requireRelationSetReader(t, s)
		mustCreate(t, s,
			ct("space", "a", "parent", "space", "root"),
			ct("space", "B", "parent", "space", "root"),
			ct("space", "B", "parent", "space", "second"),
			ctUserset("space", "Z", "parent", "group", "g", "member"),
			ct("space", "a", "viewer", "user", "u1"),
		)

		got, err := sets.RelationTargetsFor(ctx, "space", setReadIDs, "parent")
		if err != nil {
			t.Fatalf("RelationTargetsFor: %v", err)
		}
		for _, id := range setReadIDs {
			want, err := s.GetRelationTargets(ctx, "space", id, "parent")
			if err != nil {
				t.Fatalf("GetRelationTargets(%s): %v", id, err)
			}
			if !sameTargetSet(got[id], want) {
				t.Fatalf("RelationTargetsFor disagrees with GetRelationTargets on space:%s: set says %v, per-resource says %v", id, got[id], want)
			}
		}
		for id := range got {
			if len(got[id]) == 0 {
				t.Fatalf("space:%s carries an entry with no targets: an id with no targets must be ABSENT", id)
			}
		}
		if _, present := got["absent1"]; present {
			t.Fatalf("an id with no targets must be ABSENT from the map, got %v", got["absent1"])
		}
		if len(got["Z"]) != 1 || !got["Z"][0].IsUserset() {
			t.Fatalf("a userset target must be returned AS STORED, got %v", got["Z"])
		}
	})

	t.Run("RelationTargetsForEmptyInput", func(t *testing.T) {
		s := newRepos(t).Relationships
		sets := requireRelationSetReader(t, s)
		mustCreate(t, s, ct("space", "s2", "parent", "space", "s1"))
		got, err := sets.RelationTargetsFor(ctx, "space", nil, "parent")
		if err != nil || len(got) != 0 {
			t.Fatalf("RelationTargetsFor over no ids must be an empty map: got %v err=%v", got, err)
		}
	})

	t.Run("RelationTargetsForFoldsDuplicateIDsOverBatchBound", func(t *testing.T) {
		s := newRepos(t).Relationships
		sets := requireRelationSetReader(t, s)
		ids := make([]string, 0, 2*overBoundIDs)
		tuples := make([]relationships.CreateRelationship, 0, overBoundIDs)
		for i := 0; i < overBoundIDs; i++ {
			id := fmt.Sprintf("s%04d", i)
			ids = append(ids, id, id) // every id twice: the fold is proven at scale
			tuples = append(tuples, ct("space", id, "parent", "space", "root"))
		}
		for start := 0; start < len(tuples); start += seedChunk {
			mustCreate(t, s, tuples[start:min(start+seedChunk, len(tuples))]...)
		}

		got, err := sets.RelationTargetsFor(ctx, "space", ids, "parent")
		if err != nil {
			t.Fatalf("RelationTargetsFor over %d ids: %v", len(ids), err)
		}
		if len(got) != overBoundIDs {
			t.Fatalf("a chunked set read must answer every DISTINCT id once: got %d entries, want %d", len(got), overBoundIDs)
		}
		for id, targets := range got {
			if len(targets) != 1 || targets[0].ID != "root" {
				t.Fatalf("space:%s targets = %v, want exactly [space:root]", id, targets)
			}
		}
	})
}

// requireRelationSetReader is used only inside exclusively optional subtests.
// Mixed required/optional assertions must use a conditional assertion instead.
func requireRelationSetReader(t *testing.T, value any) relationships.RelationSetReader {
	t.Helper()
	sets, ok := value.(relationships.RelationSetReader)
	if !ok {
		t.Skip("optional relationship.RelationSetReader not implemented")
	}
	return sets
}

// seedOverBoundDocs seeds more viewer docs than any store's per-statement id
// bound, half of them held by the principal, and returns the id list to filter
// (in a scrambled order, so a store cannot pass by returning its input) together
// with the byte-order sorted answer.
func seedOverBoundDocs(t *testing.T, s relationships.Storer) (ids, want []string) {
	t.Helper()
	tuples := make([]relationships.CreateRelationship, 0, overBoundIDs)
	for i := 0; i < overBoundIDs; i++ {
		id := fmt.Sprintf("d%04d", i)
		ids = append(ids, id)
		holder := "u2"
		if i%2 == 0 {
			holder = "u1"
			want = append(want, id)
		}
		tuples = append(tuples, ct("doc", id, "viewer", "user", holder))
	}
	for start := 0; start < len(tuples); start += seedChunk {
		mustCreate(t, s, tuples[start:min(start+seedChunk, len(tuples))]...)
	}
	slices.Reverse(ids)
	sort.Strings(want)
	return ids, want
}

// assertSubsetSortedDistinct pins the three shape guarantees every FilterRelation
// answer carries, independently of which ids the fixture expects.
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

// sameTargetSet compares two relation-target lists as SETS: the port fixes no
// order within one resource's targets, only which subjects are named.
func sameTargetSet(a, b []relationships.RelationTarget) bool {
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
