//go:build integration && !live

// The C4 emulator leg: List[T] against a real Firestore server. Skips LOUDLY
// without FIRESTORE_EMULATOR_HOST (see emulator_integration_test.go for the
// container command).
//
// The cases fall into three groups:
//
//   - the turso behavior suite, ported (forward traversal per order, the full
//     and partial reverse windows, offset↔cursor equivalence, count under a
//     filter, stale cursor as first page, limit clamping, empty page);
//   - the C4 additions (page-three→page-two round trips in BOTH directions,
//     tied order values broken by the PK, a PK that IS the order field, the
//     document-id PK, and the whole PostFilter family: offset over matches,
//     reverse windows, count, the multi-pull page fill, and the cursor pointing
//     at the last RETURNED match);
//   - the search group, which is a PostFilter built from crud.MatchesSearch and
//     mirrors the pocket storetest search expectations (LiteralSubstringOracle,
//     BlankTermIsUnfiltered, SearchIsScopedToTheParent, CountReflectsTheSearch,
//     SearchWithCursorPaging).
//
// What the emulator CANNOT prove is composite-index availability: it enforces
// no indexes at all, so every ordered query here would need a composite index
// in production. That is list_live_test.go's job.
package firestore_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	gcfs "cloud.google.com/go/firestore"

	"github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// TestListBehavior is the turso behavior suite, ported: everything in it is a
// contract the SQL connectors already keep, restated against documents.
func TestListBehavior(t *testing.T) {
	ctx, db, collection := listFixture(t)
	seedListItems(t, ctx, db, collection)
	r := db.ReaderFrom(ctx)
	q := listQueryFor(db, collection, "a")

	t.Run("forward_created_desc", func(t *testing.T) {
		eqListIDs(t, traverseListIDs(t, ctx, r, q, crud.NewOrder("created_at", crud.DESC), 2),
			[]string{"e5", "e4", "e3", "e2", "e1"}, "created desc")
	})
	t.Run("forward_name_asc", func(t *testing.T) {
		eqListIDs(t, traverseListIDs(t, ctx, r, q, crud.NewOrder("name", crud.ASC), 2),
			[]string{"e1", "e2", "e3", "e4", "e5"}, "name asc")
	})
	t.Run("forward_n_asc", func(t *testing.T) {
		eqListIDs(t, traverseListIDs(t, ctx, r, q, crud.NewOrder("n", crud.ASC), 2),
			[]string{"e2", "e3", "e1", "e4", "e5"}, "n asc")
	})

	t.Run("default_order_when_blank", func(t *testing.T) {
		// The ListQuery's DefaultOrder is created_at DESC.
		page := mustList(t, ctx, r, q, crud.ListRequest{Limit: 2})
		eqListIDs(t, listIDs(page.Items), []string{"e5", "e4"}, "default order")
	})

	t.Run("prev_probe_full_window", func(t *testing.T) {
		desc := crud.NewOrder("created_at", crud.DESC)
		p1 := mustList(t, ctx, r, q, crud.ListRequest{Limit: 2, Order: desc})
		if p1.HasPrev {
			t.Fatal("first page HasPrev = true, want false")
		}
		p2 := mustList(t, ctx, r, q, crud.ListRequest{Limit: 2, Cursor: p1.NextCursor, Order: desc})
		p3 := mustList(t, ctx, r, q, crud.ListRequest{Limit: 2, Cursor: p2.NextCursor, Order: desc})
		if !p3.HasPrev || p3.PreviousCursor == "" {
			t.Fatalf("p3 HasPrev=%v prevCursor=%q, want true/set (full window)", p3.HasPrev, p3.PreviousCursor)
		}
		back := mustList(t, ctx, r, q, crud.ListRequest{Limit: 2, Cursor: p3.PreviousCursor, Order: desc})
		eqListIDs(t, listIDs(back.Items), []string{"e3", "e2"}, "previous cursor round-trip")
	})

	t.Run("prev_probe_partial_window", func(t *testing.T) {
		desc := crud.NewOrder("created_at", crud.DESC)
		p1 := mustList(t, ctx, r, q, crud.ListRequest{Limit: 3, Order: desc})
		p2 := mustList(t, ctx, r, q, crud.ListRequest{Limit: 3, Cursor: p1.NextCursor, Order: desc})
		if !p2.HasPrev || p2.PreviousCursor != "" {
			t.Fatalf("p2 HasPrev=%v prevCursor=%q, want true/empty (partial window)", p2.HasPrev, p2.PreviousCursor)
		}
	})

	t.Run("offset_matches_cursor", func(t *testing.T) {
		desc := crud.NewOrder("created_at", crud.DESC)
		var got []string
		for off := 0; off < 6; off += 2 {
			page := mustList(t, ctx, r, q, crud.ListRequest{Limit: 2, Offset: off, Order: desc, Strategy: crud.StrategyOffset})
			if wantPrev := off > 0; page.HasPrev != wantPrev {
				t.Errorf("offset %d HasPrev = %v, want %v", off, page.HasPrev, wantPrev)
			}
			if page.NextCursor != "" || page.PreviousCursor != "" {
				t.Errorf("offset %d emitted cursors: next=%q prev=%q", off, page.NextCursor, page.PreviousCursor)
			}
			got = append(got, listIDs(page.Items)...)
		}
		eqListIDs(t, got, []string{"e5", "e4", "e3", "e2", "e1"}, "offset traversal")
	})

	t.Run("count_under_filter", func(t *testing.T) {
		page := mustList(t, ctx, r, q, crud.ListRequest{Limit: 2, WithCount: true})
		if page.Total == nil || *page.Total != 5 {
			t.Fatalf("Total = %v, want 5", page.Total)
		}
		other := mustList(t, ctx, r, listQueryFor(db, collection, "b"), crud.ListRequest{Limit: 10, WithCount: true})
		if other.Total == nil || *other.Total != 2 {
			t.Fatalf("Total(b) = %v, want 2", other.Total)
		}
	})

	t.Run("stale_cursor_is_first_page", func(t *testing.T) {
		token, err := crud.EncodeCursor("name", "delta", "e4")
		if err != nil {
			t.Fatalf("EncodeCursor: %v", err)
		}
		page := mustList(t, ctx, r, q, crud.ListRequest{Limit: 2, Cursor: token, Order: crud.NewOrder("created_at", crud.DESC)})
		eqListIDs(t, listIDs(page.Items), []string{"e5", "e4"}, "stale cursor first page")
		if page.HasPrev {
			t.Error("stale-cursor first page HasPrev = true, want false")
		}
	})

	t.Run("malformed_cursor_is_rejected", func(t *testing.T) {
		if _, err := firestore.List(ctx, r, q, crud.ListRequest{Limit: 2, Cursor: "not-base64!!"}); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("malformed cursor err = %v, want ErrInvalidInput", err)
		}
	})

	t.Run("limits_clamp", func(t *testing.T) {
		clamped := q
		clamped.Limits = crud.Limits{Max: 2}
		page := mustList(t, ctx, r, clamped, crud.ListRequest{Limit: 3, Order: crud.NewOrder("created_at", crud.DESC)})
		eqListIDs(t, listIDs(page.Items), []string{"e5", "e4"}, "limits clamp to Max 2")
		if !page.HasMore {
			t.Error("HasMore = false, want true (Max+1 over-fetch proves a next page)")
		}
	})

	t.Run("unknown_order_field", func(t *testing.T) {
		_, err := firestore.List(ctx, r, q, crud.ListRequest{Limit: 2, Order: crud.NewOrder("password", crud.ASC)})
		if !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("unknown order err = %v, want ErrInvalidInput", err)
		}
	})

	t.Run("empty_page_items_non_nil", func(t *testing.T) {
		page := mustList(t, ctx, r, listQueryFor(db, collection, "no-such-kind"), crud.ListRequest{Limit: 10})
		if page.Items == nil || len(page.Items) != 0 {
			t.Fatalf("Items = %#v, want empty non-nil", page.Items)
		}
		encoded, err := json.Marshal(page)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		if !strings.Contains(string(encoded), `"items":[]`) {
			t.Fatalf("json = %s, want it to carry \"items\":[]", encoded)
		}
	})
}

// TestListReverseRoundTripsBothDirections is the C4 requirement the reverse
// probe exists for: from page three, PreviousCursor must address page two
// exactly — ascending AND descending. A probe that fetched one row, or that
// forgot to restore forward order, answers HasPrev correctly and this
// assertion wrongly.
func TestListReverseRoundTripsBothDirections(t *testing.T) {
	ctx, db, collection := listFixture(t)
	// Six rows so limit 2 makes three FULL pages in either direction.
	for i := 1; i <= 6; i++ {
		seedListItem(t, ctx, db, collection, listItem{
			ID:        fmt.Sprintf("r%d", i),
			CreatedAt: listBase.Add(time.Duration(i) * time.Minute),
			Name:      fmt.Sprintf("row-%d", i),
			N:         int64(i),
		}, "a")
	}
	r := db.ReaderFrom(ctx)
	q := listQueryFor(db, collection, "a")

	for _, tc := range []struct {
		name  string
		order crud.Order
		want  [][]string
	}{
		{"asc", crud.NewOrder("created_at", crud.ASC), [][]string{{"r1", "r2"}, {"r3", "r4"}, {"r5", "r6"}}},
		{"desc", crud.NewOrder("created_at", crud.DESC), [][]string{{"r6", "r5"}, {"r4", "r3"}, {"r2", "r1"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p1 := mustList(t, ctx, r, q, crud.ListRequest{Limit: 2, Order: tc.order})
			eqListIDs(t, listIDs(p1.Items), tc.want[0], "page 1")
			p2 := mustList(t, ctx, r, q, crud.ListRequest{Limit: 2, Cursor: p1.NextCursor, Order: tc.order})
			eqListIDs(t, listIDs(p2.Items), tc.want[1], "page 2")
			p3 := mustList(t, ctx, r, q, crud.ListRequest{Limit: 2, Cursor: p2.NextCursor, Order: tc.order})
			eqListIDs(t, listIDs(p3.Items), tc.want[2], "page 3")

			if !p3.HasPrev || p3.PreviousCursor == "" {
				t.Fatalf("page 3 HasPrev=%v prevCursor=%q, want true/set", p3.HasPrev, p3.PreviousCursor)
			}
			back := mustList(t, ctx, r, q, crud.ListRequest{Limit: 2, Cursor: p3.PreviousCursor, Order: tc.order})
			eqListIDs(t, listIDs(back.Items), tc.want[1], "page 3 → page 2")

			// One step further back is page ONE, which no cursor addresses: the
			// probe from page two finds only the single row strictly before its
			// cursor (the boundary row itself is excluded), so the window is
			// partial — HasPrev without a PreviousCursor, exactly as
			// crud.MarkPrevPage specifies and the SQL connectors behave.
			if !back.HasPrev || back.PreviousCursor != "" {
				t.Fatalf("page 2 HasPrev=%v prevCursor=%q, want true/empty (partial window)", back.HasPrev, back.PreviousCursor)
			}
		})
	}
}

// TestListTiedOrderValuesPageOnThePK: three rows share one timestamp, so the
// order field alone cannot address a position. The PK tiebreaker is what makes
// the cursor stable — without it a page boundary inside the tie would repeat or
// skip rows.
func TestListTiedOrderValuesPageOnThePK(t *testing.T) {
	ctx, db, collection := listFixture(t)
	tied := listBase.Add(time.Minute)
	rows := []listItem{
		{ID: "t1", CreatedAt: tied, Name: "tied-1", N: 1},
		{ID: "t2", CreatedAt: tied, Name: "tied-2", N: 2},
		{ID: "t3", CreatedAt: tied, Name: "tied-3", N: 3},
		{ID: "t4", CreatedAt: listBase.Add(2 * time.Minute), Name: "later", N: 4},
		{ID: "t0", CreatedAt: listBase, Name: "earlier", N: 0},
	}
	for _, row := range rows {
		seedListItem(t, ctx, db, collection, row, "a")
	}
	r := db.ReaderFrom(ctx)
	q := listQueryFor(db, collection, "a")

	t.Run("asc_limit_1", func(t *testing.T) {
		eqListIDs(t, traverseListIDs(t, ctx, r, q, crud.NewOrder("created_at", crud.ASC), 1),
			[]string{"t0", "t1", "t2", "t3", "t4"}, "tied asc, one at a time")
	})
	t.Run("asc_limit_2_boundary_inside_the_tie", func(t *testing.T) {
		eqListIDs(t, traverseListIDs(t, ctx, r, q, crud.NewOrder("created_at", crud.ASC), 2),
			[]string{"t0", "t1", "t2", "t3", "t4"}, "tied asc, boundary inside the tie")
	})
	t.Run("desc_limit_2", func(t *testing.T) {
		eqListIDs(t, traverseListIDs(t, ctx, r, q, crud.NewOrder("created_at", crud.DESC), 2),
			[]string{"t4", "t3", "t2", "t1", "t0"}, "tied desc")
	})
	// The reverse probe's own boundary sits INSIDE the tie: page three's cursor
	// addresses t3, and the two rows before it (t2, t1) share t3's timestamp,
	// so only the PK tiebreaker can place them.
	t.Run("reverse_probe_inside_the_tie", func(t *testing.T) {
		asc := crud.NewOrder("created_at", crud.ASC)
		p1 := mustList(t, ctx, r, q, crud.ListRequest{Limit: 2, Order: asc})
		p2 := mustList(t, ctx, r, q, crud.ListRequest{Limit: 2, Cursor: p1.NextCursor, Order: asc})
		eqListIDs(t, listIDs(p2.Items), []string{"t2", "t3"}, "page 2 inside the tie")
		p3 := mustList(t, ctx, r, q, crud.ListRequest{Limit: 2, Cursor: p2.NextCursor, Order: asc})
		eqListIDs(t, listIDs(p3.Items), []string{"t4"}, "page 3")
		if !p3.HasPrev || p3.PreviousCursor == "" {
			t.Fatalf("page 3 HasPrev=%v prevCursor=%q, want true/set (a full window across the tie)", p3.HasPrev, p3.PreviousCursor)
		}
		back := mustList(t, ctx, r, q, crud.ListRequest{Limit: 2, Cursor: p3.PreviousCursor, Order: asc})
		eqListIDs(t, listIDs(back.Items), []string{"t2", "t3"}, "back inside the tie")
	})
}

// TestListPKIsTheOrderField: when the request orders by the PK itself the tuple
// collapses to one clause and one cursor value. Firestore rejects a duplicated
// OrderBy, and a two-value cursor against a one-clause order is a client-side
// error, so this case would fail loudly rather than subtly if the helper
// emitted the field twice.
func TestListPKIsTheOrderField(t *testing.T) {
	ctx, db, collection := listFixture(t)
	seedListItems(t, ctx, db, collection)
	r := db.ReaderFrom(ctx)

	t.Run("document_field_pk", func(t *testing.T) {
		q := listQueryFor(db, collection, "a") // PK "id", ordering by "id"
		eqListIDs(t, traverseListIDs(t, ctx, r, q, crud.NewOrder("id", crud.ASC), 2),
			[]string{"e1", "e2", "e3", "e4", "e5"}, "id asc")
		eqListIDs(t, traverseListIDs(t, ctx, r, q, crud.NewOrder("id", crud.DESC), 2),
			[]string{"e5", "e4", "e3", "e2", "e1"}, "id desc")
	})

	t.Run("document_id_pk", func(t *testing.T) {
		q := listQueryFor(db, collection, "a")
		q.PK = "" // the document id
		eqListIDs(t, traverseListIDs(t, ctx, r, q, crud.NewOrder(gcfs.DocumentID, crud.ASC), 2),
			[]string{"e1", "e2", "e3", "e4", "e5"}, "__name__ asc")

		// And the reverse probe over the same single-clause ordering.
		asc := crud.NewOrder(gcfs.DocumentID, crud.ASC)
		p1 := mustList(t, ctx, r, q, crud.ListRequest{Limit: 2, Order: asc})
		p2 := mustList(t, ctx, r, q, crud.ListRequest{Limit: 2, Cursor: p1.NextCursor, Order: asc})
		p3 := mustList(t, ctx, r, q, crud.ListRequest{Limit: 2, Cursor: p2.NextCursor, Order: asc})
		eqListIDs(t, listIDs(p3.Items), []string{"e5"}, "__name__ page 3")
		if !p3.HasPrev || p3.PreviousCursor == "" {
			t.Fatalf("page 3 HasPrev=%v prevCursor=%q, want true/set", p3.HasPrev, p3.PreviousCursor)
		}
		back := mustList(t, ctx, r, q, crud.ListRequest{Limit: 2, Cursor: p3.PreviousCursor, Order: asc})
		eqListIDs(t, listIDs(back.Items), []string{"e3", "e4"}, "__name__ reverse round trip")
	})
}

// TestListDocumentIDTiebreakUnderATimestampOrder pins the OTHER document-id
// path: an ordinary order field with the document id as the tiebreaker, which
// is what a store that keeps no id FIELD gets. The cursor's PK travels as a
// document ID STRING and the vendor resolves it against the collection.
func TestListDocumentIDTiebreakUnderATimestampOrder(t *testing.T) {
	ctx, db, collection := listFixture(t)
	tied := listBase.Add(time.Minute)
	for _, row := range []listItem{
		{ID: "d1", CreatedAt: tied, Name: "one", N: 1},
		{ID: "d2", CreatedAt: tied, Name: "two", N: 2},
		{ID: "d3", CreatedAt: tied, Name: "three", N: 3},
	} {
		seedListItem(t, ctx, db, collection, row, "a")
	}
	r := db.ReaderFrom(ctx)
	q := listQueryFor(db, collection, "a")
	q.PK = ""

	eqListIDs(t, traverseListIDs(t, ctx, r, q, crud.NewOrder("created_at", crud.ASC), 1),
		[]string{"d1", "d2", "d3"}, "document-id tiebreak under a tie")
}

// TestListPostFilterPaging is the R4 family: a client-side predicate, and every
// page/probe/count path filling itself from it.
func TestListPostFilterPaging(t *testing.T) {
	ctx, db, collection := listFixture(t)
	// Alternating matches and non-matches, so no page boundary can be answered
	// from a single underlying window without filtering.
	for i := 1; i <= 12; i++ {
		seedListItem(t, ctx, db, collection, listItem{
			ID:        fmt.Sprintf("p%02d", i),
			CreatedAt: listBase.Add(time.Duration(i) * time.Minute),
			Name:      fmt.Sprintf("row-%02d", i),
			N:         int64(i),
		}, "a")
	}
	r := db.ReaderFrom(ctx)
	// The matches: even N — p02, p04, p06, p08, p10, p12.
	filtered := listQueryFor(db, collection, "a")
	filtered.PostFilter = func(row listItem) bool { return row.N%2 == 0 }
	asc := crud.NewOrder("created_at", crud.ASC)
	matches := []string{"p02", "p04", "p06", "p08", "p10", "p12"}

	t.Run("forward_traversal_returns_only_matches", func(t *testing.T) {
		eqListIDs(t, traverseListIDs(t, ctx, r, filtered, asc, 2), matches, "postfiltered traversal")
	})

	t.Run("next_cursor_is_the_last_returned_match", func(t *testing.T) {
		page := mustList(t, ctx, r, filtered, crud.ListRequest{Limit: 2, Order: asc})
		eqListIDs(t, listIDs(page.Items), []string{"p02", "p04"}, "page 1")
		if !page.HasMore || page.NextCursor == "" {
			t.Fatalf("page 1 HasMore=%v NextCursor=%q, want more", page.HasMore, page.NextCursor)
		}
		cursor, err := crud.DecodeCursor(page.NextCursor, "created_at")
		if err != nil || cursor == nil {
			t.Fatalf("DecodeCursor = %v, %v", cursor, err)
		}
		// Not p06 (the extra match that proved HasMore) and not p05 (the last
		// document scanned): the LAST RETURNED match.
		if cursor.PK != "p04" {
			t.Fatalf("NextCursor addresses %q, want the last RETURNED match p04", cursor.PK)
		}
		next := mustList(t, ctx, r, filtered, crud.ListRequest{Limit: 2, Cursor: page.NextCursor, Order: asc})
		eqListIDs(t, listIDs(next.Items), []string{"p06", "p08"}, "page 2")
	})

	t.Run("filtered_reverse_windows", func(t *testing.T) {
		p1 := mustList(t, ctx, r, filtered, crud.ListRequest{Limit: 2, Order: asc})
		p2 := mustList(t, ctx, r, filtered, crud.ListRequest{Limit: 2, Cursor: p1.NextCursor, Order: asc})
		p3 := mustList(t, ctx, r, filtered, crud.ListRequest{Limit: 2, Cursor: p2.NextCursor, Order: asc})
		eqListIDs(t, listIDs(p3.Items), []string{"p10", "p12"}, "page 3")
		if !p3.HasPrev || p3.PreviousCursor == "" {
			t.Fatalf("page 3 HasPrev=%v prevCursor=%q, want true/set (a full window of MATCHES)", p3.HasPrev, p3.PreviousCursor)
		}
		back := mustList(t, ctx, r, filtered, crud.ListRequest{Limit: 2, Cursor: p3.PreviousCursor, Order: asc})
		eqListIDs(t, listIDs(back.Items), []string{"p06", "p08"}, "page 3 → page 2")

		// A partial reverse window sets HasPrev with no cursor: from page two of
		// a three-per-page traversal only two matches precede it.
		big1 := mustList(t, ctx, r, filtered, crud.ListRequest{Limit: 4, Order: asc})
		big2 := mustList(t, ctx, r, filtered, crud.ListRequest{Limit: 4, Cursor: big1.NextCursor, Order: asc})
		if !big2.HasPrev || big2.PreviousCursor != "" {
			t.Fatalf("page 2 HasPrev=%v prevCursor=%q, want true/empty (partial window)", big2.HasPrev, big2.PreviousCursor)
		}
	})

	t.Run("offset_counts_matches_not_scanned_documents", func(t *testing.T) {
		page := mustList(t, ctx, r, filtered, crud.ListRequest{
			Limit: 2, Offset: 2, Order: asc, Strategy: crud.StrategyOffset,
		})
		// Offset 2 skips two MATCHES (p02, p04) — not two documents, which would
		// have started at p04 — and reports no cursors.
		eqListIDs(t, listIDs(page.Items), []string{"p06", "p08"}, "postfiltered offset")
		if !page.HasPrev {
			t.Error("HasPrev = false at offset 2, want true")
		}
		if !page.HasMore {
			t.Error("HasMore = false, want true (two more matches follow)")
		}
		if page.NextCursor != "" || page.PreviousCursor != "" {
			t.Errorf("the offset strategy emitted cursors: next=%q prev=%q", page.NextCursor, page.PreviousCursor)
		}

		// The tail: offset past every match returns an empty, non-nil page.
		tail := mustList(t, ctx, r, filtered, crud.ListRequest{
			Limit: 2, Offset: 6, Order: asc, Strategy: crud.StrategyOffset,
		})
		if len(tail.Items) != 0 || tail.Items == nil {
			t.Fatalf("offset 6 items = %#v, want empty non-nil", tail.Items)
		}
		if tail.HasMore {
			t.Error("offset 6 HasMore = true, want false")
		}
	})

	t.Run("count_with_and_without_the_postfilter", func(t *testing.T) {
		unfiltered := mustList(t, ctx, r, listQueryFor(db, collection, "a"),
			crud.ListRequest{Limit: 2, WithCount: true, Order: asc})
		if unfiltered.Total == nil || *unfiltered.Total != 12 {
			t.Fatalf("unfiltered Total = %v, want 12 (the server aggregation)", unfiltered.Total)
		}
		counted := mustList(t, ctx, r, filtered, crud.ListRequest{Limit: 2, WithCount: true, Order: asc})
		if counted.Total == nil || *counted.Total != 6 {
			t.Fatalf("filtered Total = %v, want the 6 MATCHES", counted.Total)
		}
		if len(counted.Items) != 2 {
			t.Fatalf("filtered page has %d items, want 2 — the count must not disturb the page", len(counted.Items))
		}
	})
}

// TestListPostFilterFillsAcrossUnderlyingPulls proves the page-fill LOOP, not
// just the filter: the matches start after the first underlying pull
// (scanPageSize = 50 documents), so a helper that filtered one window and
// stopped would return an empty page over a population that has eight matches.
func TestListPostFilterFillsAcrossUnderlyingPulls(t *testing.T) {
	ctx, db, collection := listFixture(t)
	const population = 60
	for i := range population {
		seedListItem(t, ctx, db, collection, listItem{
			ID:        fmt.Sprintf("s%02d", i),
			CreatedAt: listBase.Add(time.Duration(i) * time.Minute),
			Name:      fmt.Sprintf("row-%02d", i),
			N:         int64(i),
		}, "a")
	}
	r := db.ReaderFrom(ctx)

	// Matches live at 45,47,…,59: only three of them fall inside the first
	// 50-document pull, so filling a page of four forces a second pull.
	q := listQueryFor(db, collection, "a")
	q.PostFilter = func(row listItem) bool { return row.N >= 45 && row.N%2 == 1 }
	asc := crud.NewOrder("created_at", crud.ASC)
	want := []string{"s45", "s47", "s49", "s51", "s53", "s55", "s57", "s59"}

	eqListIDs(t, traverseListIDs(t, ctx, r, q, asc, 3), want, "matches across pulls")

	page := mustList(t, ctx, r, q, crud.ListRequest{Limit: 3, WithCount: true, Order: asc})
	eqListIDs(t, listIDs(page.Items), want[:3], "first filled page")
	if page.Total == nil || *page.Total != int64(len(want)) {
		t.Fatalf("Total = %v, want %d (the iterate-and-filter count also pulls twice)", page.Total, len(want))
	}
}

// TestListWithCountInsideATransaction: the count aggregation is unavailable in
// any transaction — a read-write Transact and a read-only ReadSnapshot alike.
// Rather than failing the call, List falls back to counting by iteration, so a
// transaction-bound page and its total come from the same instant. Without the
// fallback this returns ErrCountInTransaction.
func TestListWithCountInsideATransaction(t *testing.T) {
	ctx, db, collection := listFixture(t)
	seedListItems(t, ctx, db, collection)
	q := listQueryFor(db, collection, "a")

	direct := mustList(t, ctx, db.ReaderFrom(ctx), q, crud.ListRequest{Limit: 2, WithCount: true})
	if direct.Total == nil || *direct.Total != 5 {
		t.Fatalf("Total outside a snapshot = %v, want 5", direct.Total)
	}

	// The same fallback carries a read-write transaction: reads there are also
	// transaction-scoped, and the aggregation is equally unavailable.
	if err := db.Transact(ctx, func(txCtx context.Context) error {
		page, err := firestore.List(txCtx, db.ReaderFrom(txCtx), q, crud.ListRequest{Limit: 2, WithCount: true})
		if err != nil {
			return err
		}
		if page.Total == nil || *page.Total != 5 {
			t.Errorf("Total inside a Transact = %v, want 5", page.Total)
		}
		return nil
	}); err != nil {
		t.Fatalf("Transact: %v", err)
	}

	err := db.ReadSnapshot(ctx, func(snapCtx context.Context, r firestore.Reader) error {
		// The Reader refuses the aggregation outright…
		if _, err := r.Count(snapCtx, db.Collection(collection).Where("kind", "==", "a")); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("Reader.Count inside a snapshot = %v, want ErrCountInTransaction", err)
		}
		// …and List still answers WithCount, by iterating.
		page, err := firestore.List(snapCtx, r, q, crud.ListRequest{Limit: 2, WithCount: true})
		if err != nil {
			return err
		}
		if page.Total == nil || *page.Total != 5 {
			t.Errorf("Total inside a snapshot = %v, want 5", page.Total)
		}
		eqListIDs(t, listIDs(page.Items), []string{"e5", "e4"}, "snapshot page")

		// The postfiltered count iterates in both worlds.
		filtered := q
		filtered.PostFilter = func(row listItem) bool { return row.N >= 30 }
		filteredPage, err := firestore.List(snapCtx, r, filtered, crud.ListRequest{Limit: 2, WithCount: true})
		if err != nil {
			return err
		}
		if filteredPage.Total == nil || *filteredPage.Total != 3 {
			t.Errorf("filtered Total inside a snapshot = %v, want 3", filteredPage.Total)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
}

// searchNames mirrors the pocket storetest search fixture: names chosen so a
// naive `"%" + term + "%"` implementation fails.
var searchNames = []string{
	"deploy-bot",
	"Deploy-Admin",
	"ci runner",
	"100% coverage",
	"a_c naming",
	"abc naming",
	"renaming task",
	`back\slash`,
	"発注書",
}

// TestListSearchAsAPostFilter is R4 proved end to end: the store composes a
// PostFilter from crud.MatchesSearch over its SearchFields, and the helper
// makes that behave like the SQL connectors' LIKE/ILIKE predicate. The
// expectations mirror the pocket storetest search group — LiteralSubstringOracle,
// BlankTermIsUnfiltered, SearchIsScopedToTheParent, CountReflectsTheSearch,
// SearchWithCursorPaging — against the same oracle: crud.MatchesSearch itself.
func TestListSearchAsAPostFilter(t *testing.T) {
	ctx, db, collection := listFixture(t)
	for i, name := range searchNames {
		seedListItem(t, ctx, db, collection, listItem{
			ID:        fmt.Sprintf("k%02d", i),
			CreatedAt: listBase.Add(time.Duration(i) * time.Minute),
			Name:      name,
			N:         int64(i),
		}, "sa-search")
	}
	// A row under a DIFFERENT parent carrying a matching name, so a helper that
	// lost the parent predicate fails loudly (R4: search is parent-scoped).
	seedListItem(t, ctx, db, collection, listItem{
		ID: "foreign", CreatedAt: listBase, Name: "deploy-bot", N: 99,
	}, "sa-other")

	r := db.ReaderFrom(ctx)
	// searchFields is the store's allow-list; the PostFilter is exactly what a
	// Firestore store writes (R4).
	searchFields := []crud.SearchField{{Column: "name"}}
	searchQuery := func(parent, term string) firestore.ListQuery[listItem] {
		q := listQueryFor(db, collection, parent)
		q.DefaultOrder = crud.NewOrder("created_at", crud.ASC)
		q.PostFilter = func(row listItem) bool {
			for _, f := range searchFields {
				if f.Column == "name" && crud.MatchesSearch(row.Name, term) {
					return true
				}
			}
			return false
		}
		return q
	}
	namesFor := func(t *testing.T, term string) []string {
		t.Helper()
		page := mustList(t, ctx, r, searchQuery("sa-search", term), crud.ListRequest{Limit: 50, Search: term})
		out := make([]string, 0, len(page.Items))
		for _, row := range page.Items {
			out = append(out, row.Name)
		}
		return out
	}

	t.Run("LiteralSubstringOracle", func(t *testing.T) {
		terms := []string{
			"deploy", "DEPLOY", "Deploy-Admin", "runner",
			"100%", "%", "a_c", "_", `back\s`, `\`,
			"発注", "nothing-here", "  deploy  ",
		}
		for _, term := range terms {
			t.Run("term="+strings.ReplaceAll(term, "/", "_"), func(t *testing.T) {
				got := namesFor(t, term)

				var want []string
				for _, name := range searchNames {
					if crud.MatchesSearch(name, term) {
						want = append(want, name)
					}
				}
				if len(got) != len(want) {
					t.Fatalf("search %q returned %v, want %v (the crud.MatchesSearch oracle)", term, got, want)
				}
				set := map[string]bool{}
				for _, n := range got {
					set[n] = true
				}
				for _, n := range want {
					if !set[n] {
						t.Errorf("search %q missed %q; got %v", term, n, got)
					}
				}
			})
		}
	})

	t.Run("BlankTermIsUnfiltered", func(t *testing.T) {
		for _, term := range []string{"", "   "} {
			if got := namesFor(t, term); len(got) != len(searchNames) {
				t.Errorf("blank search %q returned %d rows, want all %d", term, len(got), len(searchNames))
			}
		}
	})

	t.Run("SearchIsScopedToTheParent", func(t *testing.T) {
		if got := namesFor(t, "deploy-bot"); len(got) != 1 {
			t.Fatalf("search returned %v, want exactly the one row under this parent", got)
		}
		foreign := mustList(t, ctx, r, searchQuery("sa-other", "deploy-bot"),
			crud.ListRequest{Limit: 50, Search: "deploy-bot"})
		if len(foreign.Items) != 1 {
			t.Errorf("the foreign parent returned %d rows, want 1", len(foreign.Items))
		}
	})

	t.Run("CountReflectsTheSearch", func(t *testing.T) {
		const term = "deploy"
		page := mustList(t, ctx, r, searchQuery("sa-search", term),
			crud.ListRequest{Limit: 50, WithCount: true, Search: term})
		want := 0
		for _, name := range searchNames {
			if crud.MatchesSearch(name, term) {
				want++
			}
		}
		if len(page.Items) != want {
			t.Fatalf("page has %d items, want %d", len(page.Items), want)
		}
		if page.Total == nil || int(*page.Total) != want {
			t.Errorf("Total = %v, want the SEARCHED total %d — the count ignored the search", page.Total, want)
		}
	})

	t.Run("SearchWithCursorPaging", func(t *testing.T) {
		const term = "naming"
		matching := 0
		for _, name := range searchNames {
			if crud.MatchesSearch(name, term) {
				matching++
			}
		}
		if matching < 3 {
			t.Fatalf("the seed carries %d names matching %q; this case needs at least 3", matching, term)
		}

		q := searchQuery("sa-search", term)
		first := mustList(t, ctx, r, q, crud.ListRequest{Limit: 2, Search: term})
		if len(first.Items) != 2 {
			t.Fatalf("first page has %d items, want 2", len(first.Items))
		}
		if !first.HasMore || first.NextCursor == "" {
			t.Fatalf("first page HasMore=%v NextCursor=%q, want more", first.HasMore, first.NextCursor)
		}
		if first.HasPrev {
			t.Error("the first page reports a previous page")
		}

		second := mustList(t, ctx, r, q, crud.ListRequest{Limit: 2, Cursor: first.NextCursor, Search: term})
		if len(second.Items) != matching-2 {
			t.Fatalf("second page has %d items, want %d", len(second.Items), matching-2)
		}
		seen := map[string]bool{}
		for _, row := range append(append([]listItem{}, first.Items...), second.Items...) {
			if !crud.MatchesSearch(row.Name, term) {
				t.Errorf("a paged row %q does not match the search", row.Name)
			}
			if seen[row.ID] {
				t.Errorf("row %q appeared on both pages", row.ID)
			}
			seen[row.ID] = true
		}
		if !second.HasPrev {
			t.Error("the second page reports no previous page; the reverse probe lost the predicate")
		}
	})
}

// listFixture opens the emulator and returns a collection name unique to this
// test. A subtest's name carries a slash and a collection id may not, so it is
// sanitized (the C3 finding: an illegal path yields a NIL reference whose first
// use reads like a connector bug).
func listFixture(t *testing.T) (context.Context, *firestore.DB, string) {
	t.Helper()
	requireEmulator(t)

	ctx := context.Background()
	db, err := firestore.Open(ctx, firestore.Config{
		ProjectID:      emulatorProject(),
		ConnectTimeout: 15 * time.Second,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	name := strings.ReplaceAll(t.Name(), "/", "_")
	return ctx, db, fmt.Sprintf("c4_%s_%d", name, time.Now().UnixNano())
}

// seedListItems writes the five-row "a" population the ported turso cases page
// over, plus two rows under a second parent that must never appear.
func seedListItems(t *testing.T, ctx context.Context, db *firestore.DB, collection string) {
	t.Helper()
	rows := []listItem{
		{ID: "e1", CreatedAt: listBase.Add(0 * time.Minute), Name: "alpha", N: 30},
		{ID: "e2", CreatedAt: listBase.Add(1 * time.Minute), Name: "bravo", N: 10},
		{ID: "e3", CreatedAt: listBase.Add(2 * time.Minute), Name: "charlie", N: 20},
		{ID: "e4", CreatedAt: listBase.Add(3 * time.Minute), Name: "delta", N: 40},
		{ID: "e5", CreatedAt: listBase.Add(4 * time.Minute), Name: "echo", N: 50},
	}
	for _, row := range rows {
		seedListItem(t, ctx, db, collection, row, "a")
	}
	for _, id := range []string{"b1", "b2"} {
		seedListItem(t, ctx, db, collection, listItem{ID: id, CreatedAt: listBase, Name: id, N: 1}, "b")
	}
}

// seedListItem writes one fixture document. The id is BOTH the document id and
// an "id" field, so the same fixture serves a document-field PK and a
// document-id PK.
func seedListItem(t *testing.T, ctx context.Context, db *firestore.DB, collection string, row listItem, kind string) {
	t.Helper()
	data := map[string]any{
		"id":         row.ID,
		"created_at": firestore.TruncateTime(row.CreatedAt),
		"name":       row.Name,
		"n":          row.N,
		"kind":       kind,
	}
	if err := db.WriterFrom(ctx).Create(ctx, db.Doc(collection, row.ID), data); err != nil {
		t.Fatalf("seeding %s: %v", row.ID, err)
	}
}
