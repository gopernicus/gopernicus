//go:build integration && !live

package firestore

import (
	"context"
	"slices"
	"testing"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// A2d: the two crud listings, the streamed distinct-id lookups, and the
// descendant BFS (A-D4). The shared conformance suite proves the port contract;
// what is asserted here is the part that only shows up at Firestore's own
// seams — natural-key cursor boundaries, deduplication across chunk
// and PAGE boundaries, and the DNF product that decides how a hop is chunked.

// TestListingsPageInNaturalTupleOrder covers forward and reverse cursor paging.
func TestListingsPageInNaturalTupleOrder(t *testing.T) {
	ctx := context.Background()
	_, s := newRelationships(t)

	const rows = 7
	tuples := make([]relationships.CreateRelationship, 0, rows)
	for i := 0; i < rows; i++ {
		tuples = append(tuples, ctf("doc", docID("d", i), "viewer", "user", "u1"))
	}
	if err := s.CreateRelationships(ctx, tuples); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Compare paged results with the complete natural ordering.
	all, err := s.ListRelationshipsBySubject(ctx, "user", "u1", relationships.SubjectRelationshipFilter{}, list.Request{Limit: 50})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all.Items) != rows {
		t.Fatalf("want %d rows, got %d", rows, len(all.Items))
	}

	// Page forward two at a time; every resource appears exactly once and the
	// concatenated pages reproduce the single-page order exactly.
	var walked []string
	cursor := ""
	var cursors []string
	for page := 0; page < rows; page++ {
		got, err := s.ListRelationshipsBySubject(ctx, "user", "u1", relationships.SubjectRelationshipFilter{}, list.Request{Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		cursors = append(cursors, cursor)
		for _, item := range got.Items {
			walked = append(walked, item.ResourceID)
		}
		if !got.HasMore {
			break
		}
		cursor = got.NextCursor
	}
	want := make([]string, 0, rows)
	for _, item := range all.Items {
		want = append(want, item.ResourceID)
	}
	if !slices.Equal(walked, want) {
		t.Fatalf("paged walk diverged from the single page:\n paged %v\n whole %v", walked, want)
	}

	// Page three, then BACK to page two: the reverse window must reproduce the
	// forward page byte for byte.
	if len(cursors) < 3 {
		t.Fatalf("want at least three pages, got %d", len(cursors))
	}
	third, err := s.ListRelationshipsBySubject(ctx, "user", "u1", relationships.SubjectRelationshipFilter{}, list.Request{Limit: 2, Cursor: cursors[2]})
	if err != nil {
		t.Fatalf("page three: %v", err)
	}
	if !third.HasPrev || third.PreviousCursor == "" {
		t.Fatalf("page three must offer a previous cursor: %+v", third)
	}
	back, err := s.ListRelationshipsBySubject(ctx, "user", "u1", relationships.SubjectRelationshipFilter{}, list.Request{Limit: 2, Cursor: third.PreviousCursor})
	if err != nil {
		t.Fatalf("page three -> two: %v", err)
	}
	if !slices.Equal(ids(back.Items), want[2:4]) {
		t.Fatalf("page three -> two returned %v, want %v", ids(back.Items), want[2:4])
	}
}

// ids projects a subject listing page to its resource ids.
func ids(items []relationships.SubjectRelationship) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.ResourceID)
	}
	return out
}

// TestListingFiltersAndResourceScope pins the two optional filters and the
// resource listing's scope: a resource listing is ONE equality clause on the
// derived resource_key, so a same-named resource of a DIFFERENT type must not
// leak into it.
func TestListingFiltersAndResourceScope(t *testing.T) {
	ctx := context.Background()
	_, s := newRelationships(t)

	if err := s.CreateRelationships(ctx, []relationships.CreateRelationship{
		ctf("doc", "x", "viewer", "user", "u1"),
		ctf("doc", "x", "owner", "group", "eng"),
		ctf("folder", "x", "viewer", "user", "u1"), // same id, other TYPE
		ctf("doc", "y", "editor", "user", "u1"),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	byResource, err := s.ListRelationshipsByResource(ctx, "doc", "x", relationships.ResourceRelationshipFilter{}, list.Request{Limit: 50})
	if err != nil {
		t.Fatalf("list by resource: %v", err)
	}
	if len(byResource.Items) != 2 {
		t.Fatalf("doc:x must carry exactly its own two rows, got %+v", byResource.Items)
	}

	subjectType, relation := "user", "viewer"
	filtered, err := s.ListRelationshipsByResource(ctx, "doc", "x", relationships.ResourceRelationshipFilter{SubjectType: &subjectType, Relation: &relation}, list.Request{Limit: 50})
	if err != nil {
		t.Fatalf("filtered list by resource: %v", err)
	}
	if len(filtered.Items) != 1 || filtered.Items[0].SubjectID != "u1" {
		t.Fatalf("both filters must narrow to u1's viewer row, got %+v", filtered.Items)
	}

	resourceType := "doc"
	narrow, err := s.ListRelationshipsBySubject(ctx, "user", "u1", relationships.SubjectRelationshipFilter{ResourceType: &resourceType}, list.Request{Limit: 50})
	if err != nil {
		t.Fatalf("filtered list by subject: %v", err)
	}
	if got := len(narrow.Items); got != 2 {
		t.Fatalf("the resource-type filter must keep u1's two doc rows, got %d: %+v", got, narrow.Items)
	}
	editor := "editor"
	one, err := s.ListRelationshipsBySubject(ctx, "user", "u1", relationships.SubjectRelationshipFilter{ResourceType: &resourceType, Relation: &editor}, list.Request{Limit: 50})
	if err != nil {
		t.Fatalf("two-filter list by subject: %v", err)
	}
	if len(one.Items) != 1 || one.Items[0].ResourceID != "y" {
		t.Fatalf("both filters must narrow to doc:y, got %+v", one.Items)
	}
}

// TestLookupDedupesAcrossChunksAndPages is the streamed lookups' central
// property: a resource matched many times — by several relations, by several of
// the subject's reached states, and by targets that land in DIFFERENT query
// chunks — is ONE id in the answer, and the deduplication happens BEFORE the
// limit, so a small page is never short.
func TestLookupDedupesAcrossChunksAndPages(t *testing.T) {
	ctx := context.Background()
	_, s := newRelationships(t)

	// 40 groups so the reached states span two expansion chunks, every one of
	// them granting `viewer` on the SAME doc; a second relation on the same doc
	// duplicates it again. The physical stream therefore carries dozens of
	// documents for one id, across more than one page.
	tuples := []relationships.CreateRelationship{ctf("doc", "shared", "editor", "user", "u1")}
	for i := 0; i < 40; i++ {
		g := docID("g", i)
		tuples = append(tuples,
			ctf("group", g, "member", "user", "u1"),
			ctfUserset("doc", "shared", "viewer", "group", g, "member"),
		)
	}
	tuples = append(tuples, ctf("doc", "other", "viewer", "user", "u1"))
	if err := s.CreateRelationships(ctx, tuples); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := s.LookupResourceIDs(ctx, "doc", []string{"viewer", "editor"}, "user", "u1", "", 100)
	if err != nil {
		t.Fatalf("LookupResourceIDs: %v", err)
	}
	if want := []string{"other", "shared"}; !slices.Equal(got, want) {
		t.Fatalf("duplicates spanning chunks and pages must fold to %v, got %v", want, got)
	}
	// limit 1 must return the FIRST distinct id, not the first document — a
	// store that limited the query would return "shared" repeatedly and stop.
	first, err := s.LookupResourceIDs(ctx, "doc", []string{"viewer", "editor"}, "user", "u1", "", 1)
	if err != nil {
		t.Fatalf("LookupResourceIDs(limit 1): %v", err)
	}
	if !slices.Equal(first, []string{"other"}) {
		t.Fatalf("limit 1 must yield the first DISTINCT id [other], got %v", first)
	}
	// after is exclusive and byte-ordered even when the id it names is the
	// heavily duplicated one.
	rest, err := s.LookupResourceIDs(ctx, "doc", []string{"viewer", "editor"}, "user", "u1", "other", 100)
	if err != nil {
		t.Fatalf("LookupResourceIDs(after other): %v", err)
	}
	if !slices.Equal(rest, []string{"shared"}) {
		t.Fatalf("after must skip exactly the named id, got %v", rest)
	}
	if empty, err := s.LookupResourceIDs(ctx, "doc", []string{"viewer", "editor"}, "user", "u1", "shared", 100); err != nil || len(empty) != 0 {
		t.Fatalf("after the last id must be empty: %v err=%v", empty, err)
	}
}

// TestLookupsExcludeForeignResourceTypesAndUsersets pins the two filters a
// lookup is easiest to get wrong: the resource TYPE (a same-named resource of
// another type must not appear) and, for the target lookup, the
// concrete-subject requirement — a stored userset with the same type and id
// does NOT match, because the subject key folds subject_relation in.
func TestLookupsExcludeForeignResourceTypesAndUsersets(t *testing.T) {
	ctx := context.Background()
	_, s := newRelationships(t)

	if err := s.CreateRelationships(ctx, []relationships.CreateRelationship{
		ctf("space", "s2", "parent", "space", "p1"),
		ctf("folder", "s3", "parent", "space", "p1"),                 // other resource type
		ctfUserset("space", "s4", "parent", "space", "p1", "member"), // a USERSET target
		ctf("space", "s5", "parent", "space", "p1"),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := s.LookupResourceIDsByRelationTarget(ctx, "space", "parent", "space", []string{"p1"}, "", 100)
	if err != nil {
		t.Fatalf("LookupResourceIDsByRelationTarget: %v", err)
	}
	if want := []string{"s2", "s5"}; !slices.Equal(got, want) {
		t.Fatalf("want %v (foreign type and userset excluded), got %v", want, got)
	}

	desc, err := s.LookupDescendantResourceIDs(ctx, "space", []string{"parent"}, "space", []string{"p1"}, "", 100)
	if err != nil {
		t.Fatalf("LookupDescendantResourceIDs: %v", err)
	}
	if want := []string{"s2", "s5"}; !slices.Equal(desc, want) {
		t.Fatalf("the descendant walk must apply the same two exclusions: want %v, got %v", want, desc)
	}
}

// TestDescendantWalkChunksByTheDNFProduct is the vendor cap made executable.
// Two relations against a frontier of 30+ ids is 60+ disjunctions after DNF
// expansion — an InvalidArgument, not a valid query — so the hop must chunk by
// the PRODUCT: with two relations, fifteen frontier entries per query.
func TestDescendantWalkChunksByTheDNFProduct(t *testing.T) {
	ctx := context.Background()
	db, s := newRelationships(t)

	// 31 children of one root, so the SECOND hop's frontier is 31 wide.
	const children = 31
	tuples := make([]relationships.CreateRelationship, 0, children+1)
	for i := 0; i < children; i++ {
		tuples = append(tuples, ctf("space", docID("c", i), "parent", "space", "root"))
	}
	// One grandchild, reached from a child by the OTHER relation of the union.
	tuples = append(tuples, ctf("space", "grand", "folder", "space", docID("c", 0)))
	if err := s.CreateRelationships(ctx, tuples); err != nil {
		t.Fatalf("create: %v", err)
	}

	want := make([]string, 0, children+1)
	for i := 0; i < children; i++ {
		want = append(want, docID("c", i))
	}
	want = append(want, "grand")
	slices.Sort(want)

	got, err := s.LookupDescendantResourceIDs(ctx, "space", []string{"parent", "folder"}, "space", []string{"root"}, "", 0)
	if err != nil {
		t.Fatalf("LookupDescendantResourceIDs: %v", err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("a frontier past the DNF product must be chunked and merged: got %d ids, want %d", len(got), len(want))
	}

	// The query budget, counted at the connector seam. Two relations mean
	// floor(30/2) = 15 frontier entries per query, so:
	//   hop 1: frontier {root}            -> 1 query
	//   hop 2: frontier of 31 children    -> ceil(31/15) = 3 queries
	//   hop 3: frontier {grand}           -> 1 query
	//   hop 4: empty frontier             -> 0 queries
	var counter *countingReader
	if err := db.ReadSnapshot(ctx, func(ctx context.Context, r firestoredb.Reader) error {
		counter = &countingReader{reader: r}
		_, err := descendantClosure(ctx, db, counter, "space", []string{"parent", "folder"}, "space", []string{"root"})
		return err
	}); err != nil {
		t.Fatalf("descendantClosure: %v", err)
	}
	if counter.queries != 5 {
		t.Fatalf("the walk issued %d queries, want 5 (1 + 3 chunks + 1)", counter.queries)
	}

	// chunkProduct is the rule those numbers come from; assert it directly so a
	// change to the constant cannot quietly make a query invalid.
	for _, pair := range chunkProduct([]string{"parent", "folder"}, want, maxDisjunctions) {
		if n := len(pair.primary) * len(pair.secondary); n > maxDisjunctions {
			t.Fatalf("chunkProduct emitted a %d-disjunction query, cap is %d", n, maxDisjunctions)
		}
		if len(pair.secondary) != 15 && len(pair.secondary) != len(want)%15 {
			t.Fatalf("two relations must chunk the frontier at 15, got %d", len(pair.secondary))
		}
	}
}

// TestDescendantWalkTerminatesOnCyclesAndPagesTheClosure covers the two
// remaining closure properties: a cycle terminates (and makes a root a genuine
// descendant), and after/limit page the sorted CLOSURE rather than the work —
// an unbounded limit returns all of it.
func TestDescendantWalkTerminatesOnCyclesAndPagesTheClosure(t *testing.T) {
	ctx := context.Background()
	_, s := newRelationships(t)

	// a -> b -> c -> a, plus a leaf off c.
	if err := s.CreateRelationships(ctx, []relationships.CreateRelationship{
		ctf("space", "b", "parent", "space", "a"),
		ctf("space", "c", "parent", "space", "b"),
		ctf("space", "a", "parent", "space", "c"),
		ctf("space", "leaf", "parent", "space", "c"),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	whole, err := s.LookupDescendantResourceIDs(ctx, "space", []string{"parent"}, "space", []string{"a"}, "", 0)
	if err != nil {
		t.Fatalf("unbounded walk: %v", err)
	}
	if want := []string{"a", "b", "c", "leaf"}; !slices.Equal(whole, want) {
		t.Fatalf("a cycle must terminate and make the root a genuine descendant: want %v, got %v", want, whole)
	}

	head, err := s.LookupDescendantResourceIDs(ctx, "space", []string{"parent"}, "space", []string{"a"}, "", 2)
	if err != nil {
		t.Fatalf("limited walk: %v", err)
	}
	if want := []string{"a", "b"}; !slices.Equal(head, want) {
		t.Fatalf("limit pages the closure: want %v, got %v", want, head)
	}
	tail, err := s.LookupDescendantResourceIDs(ctx, "space", []string{"parent"}, "space", []string{"a"}, "b", 100)
	if err != nil {
		t.Fatalf("after walk: %v", err)
	}
	if want := []string{"c", "leaf"}; !slices.Equal(tail, want) {
		t.Fatalf("after is exclusive on the closure: want %v, got %v", want, tail)
	}
}

// TestLookupAfterIsRawByteOrder is the contract the engine's k-way merge
// depends on: the ids come back in Go string order, and `after` cuts the same
// sequence — a collated order would interleave these six ids differently.
func TestLookupAfterIsRawByteOrder(t *testing.T) {
	ctx := context.Background()
	_, s := newRelationships(t)

	raw := []string{"B", "a", "_x", "~z", "Z", "é"}
	tuples := make([]relationships.CreateRelationship, 0, len(raw))
	for _, id := range raw {
		tuples = append(tuples, ctf("doc", id, "viewer", "user", "u1"))
	}
	if err := s.CreateRelationships(ctx, tuples); err != nil {
		t.Fatalf("create: %v", err)
	}
	want := slices.Clone(raw)
	slices.Sort(want)

	got, err := s.LookupResourceIDs(ctx, "doc", []string{"viewer"}, "user", "u1", "", 100)
	if err != nil {
		t.Fatalf("LookupResourceIDs: %v", err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("want raw byte order %v, got %v", want, got)
	}
	for i, after := range want {
		rest, err := s.LookupResourceIDs(ctx, "doc", []string{"viewer"}, "user", "u1", after, 100)
		if err != nil {
			t.Fatalf("after %q: %v", after, err)
		}
		if !slices.Equal(rest, want[i+1:]) {
			t.Fatalf("after %q: want %v, got %v", after, want[i+1:], rest)
		}
	}
}
