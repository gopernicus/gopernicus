//go:build integration && live

// The C4 live leg. It carries the ONE thing about List that production can do
// differently from the emulator: COMPOSITE INDEXES.
//
// Every List query orders by a field and filters on another — a parent scope
// plus `ORDER BY created_at, id` — and in production that combination requires
// a composite index; without one the server answers FAILED_PRECONDITION and
// MapError turns it into *MissingIndexError. The emulator has no index registry
// and enforces nothing, so an emulator-green List proves the ORDERING, never
// the INDEX. The reverse probe doubles the requirement: it flips every
// direction, and a composite index is direction-specific, so a store's manifest
// needs BOTH orderings of the same field tuple (C-D7).
//
// The database this runs against must therefore have these indexes on
// firestore_c4_list_live, READY before the run:
//
//	group ASC, created_at ASC,  id ASC
//	group ASC, created_at DESC, id DESC
//	group ASC, n ASC,           id ASC
//	group ASC, n DESC,          id DESC
//
//	FIRESTORE_EMULATOR_HOST= FIRESTORE_LIVE_PROJECT_ID=<project> \
//	  FIRESTORE_LIVE_DATABASE_ID=<run-owned-db> \
//	  GOOGLE_APPLICATION_CREDENTIALS=<sa.json> \
//	  go test -tags='integration,live' -run 'Live$' -timeout 30m ./...
//
// NOT RUN as of C4: no live GCP project existed in the session that wrote this.
// It compiles (go vet -tags='integration,live'), skips loudly without
// configuration, and fails under FIRESTORE_LIVE_REQUIRED=1.
package firestore_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/integrations/datastores/firestore/firestoretest"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// liveListCollection is the ONLY collection this leg writes or clears.
const liveListCollection = "firestore_c4_list_live"

// TestListUnderCompositeIndexesLive pages a filtered, ordered list in BOTH
// directions against a real database, so a missing composite index — for the
// forward query OR for the reverse probe's flipped one — fails here instead of
// on a host's first production request. It also proves the server-side count
// aggregation under the same filter, which the emulator answers from a
// different code path.
func TestListUnderCompositeIndexesLive(t *testing.T) {
	ctx, db := liveListFixture(t)
	group := livePrefix(t)

	for i := 1; i <= 6; i++ {
		seedLiveListItem(t, ctx, db, group, listItem{
			ID:        fmt.Sprintf("%sr%d", group, i),
			CreatedAt: listBase.Add(time.Duration(i) * time.Minute),
			Name:      fmt.Sprintf("row-%d", i),
			N:         int64(i),
		})
	}

	r := db.ReaderFrom(ctx)
	q := liveListQuery(db, group)

	for _, tc := range []struct {
		name  string
		order crud.Order
		want  [][]string
	}{
		{"asc", crud.NewOrder("created_at", crud.ASC), [][]string{
			{group + "r1", group + "r2"}, {group + "r3", group + "r4"}, {group + "r5", group + "r6"},
		}},
		{"desc", crud.NewOrder("created_at", crud.DESC), [][]string{
			{group + "r6", group + "r5"}, {group + "r4", group + "r3"}, {group + "r2", group + "r1"},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p1 := mustList(t, ctx, r, q, crud.ListRequest{Limit: 2, Order: tc.order})
			eqListIDs(t, listIDs(p1.Items), tc.want[0], "page 1")
			p2 := mustList(t, ctx, r, q, crud.ListRequest{Limit: 2, Cursor: p1.NextCursor, Order: tc.order})
			eqListIDs(t, listIDs(p2.Items), tc.want[1], "page 2")

			// Page three is the one whose reverse probe needs the FLIPPED
			// composite index, and whose PreviousCursor must address page two.
			p3 := mustList(t, ctx, r, q, crud.ListRequest{Limit: 2, Cursor: p2.NextCursor, Order: tc.order})
			eqListIDs(t, listIDs(p3.Items), tc.want[2], "page 3")
			if !p3.HasPrev || p3.PreviousCursor == "" {
				t.Fatalf("page 3 HasPrev=%v prevCursor=%q, want true/set", p3.HasPrev, p3.PreviousCursor)
			}
			back := mustList(t, ctx, r, q, crud.ListRequest{Limit: 2, Cursor: p3.PreviousCursor, Order: tc.order})
			eqListIDs(t, listIDs(back.Items), tc.want[1], "page 3 → page 2")
		})
	}

	t.Run("count", func(t *testing.T) {
		page := mustList(t, ctx, r, q, crud.ListRequest{Limit: 2, WithCount: true, Order: crud.NewOrder("n", crud.DESC)})
		if page.Total == nil || *page.Total != 6 {
			t.Fatalf("Total = %v, want 6", page.Total)
		}
	})
}

// TestListPostFilterLive runs the R4 path against production: the predicate is
// Go code, but the underlying pulls, their cursors, and the iterate-and-filter
// count all still go through the same indexed query.
func TestListPostFilterLive(t *testing.T) {
	ctx, db := liveListFixture(t)
	group := livePrefix(t)

	for i := 1; i <= 12; i++ {
		seedLiveListItem(t, ctx, db, group, listItem{
			ID:        fmt.Sprintf("%sp%02d", group, i),
			CreatedAt: listBase.Add(time.Duration(i) * time.Minute),
			Name:      fmt.Sprintf("row-%02d", i),
			N:         int64(i),
		})
	}

	r := db.ReaderFrom(ctx)
	q := liveListQuery(db, group)
	q.PostFilter = func(row listItem) bool { return row.N%2 == 0 }
	asc := crud.NewOrder("created_at", crud.ASC)

	want := make([]string, 0, 6)
	for i := 2; i <= 12; i += 2 {
		want = append(want, fmt.Sprintf("%sp%02d", group, i))
	}
	eqListIDs(t, traverseListIDs(t, ctx, r, q, asc, 2), want, "postfiltered traversal")

	page := mustList(t, ctx, r, q, crud.ListRequest{Limit: 2, WithCount: true, Order: asc})
	if page.Total == nil || *page.Total != 6 {
		t.Fatalf("Total = %v, want the 6 matches", page.Total)
	}
}

// liveListFixture opens the live database (skipping loudly, or failing under
// FIRESTORE_LIVE_REQUIRED=1) and clears the one collection this leg owns,
// before and after.
func liveListFixture(t *testing.T) (context.Context, *firestore.DB) {
	t.Helper()
	db := firestoretest.OpenLive(t)
	firestoretest.ResetLive(t, db, liveListCollection)
	t.Cleanup(func() { firestoretest.ResetLive(t, db, liveListCollection) })
	return context.Background(), db
}

// liveListQuery is listQueryFor against the live collection. The shared
// collection is partitioned by a per-test group, which takes the place of the
// emulator fixture's kind field.
func liveListQuery(db *firestore.DB, group string) firestore.ListQuery[listItem] {
	q := listQueryFor(db, liveListCollection, group)
	q.Query = db.Collection(liveListCollection).Where("group", "==", group)
	return q
}

// seedLiveListItem writes one fixture document into the collection this leg
// owns, tagged with the group its test queries.
func seedLiveListItem(t *testing.T, ctx context.Context, db *firestore.DB, group string, row listItem) {
	t.Helper()
	data := map[string]any{
		"id":         row.ID,
		"created_at": firestore.TruncateTime(row.CreatedAt),
		"name":       row.Name,
		"n":          row.N,
		"group":      group,
	}
	if err := db.WriterFrom(ctx).Create(ctx, db.Doc(liveListCollection, row.ID), data); err != nil {
		t.Fatalf("seeding %s: %v", row.ID, err)
	}
}
