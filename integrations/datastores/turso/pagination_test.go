package turso

import (
	"context"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk/crud"
)

// pageRow is a minimal (created_at, id) record standing in for any paginated
// store entity, so the connector's keyset ListPage is exercised end-to-end
// against the same SQLite dialect a live Turso database speaks.
type pageRow struct {
	ID        string
	CreatedAt time.Time
}

func scanPageRow(s Scanner) (pageRow, error) {
	var r pageRow
	var ts string
	if err := s.Scan(&r.ID, &ts); err != nil {
		return pageRow{}, err
	}
	t, err := ParseTime(ts)
	if err != nil {
		return pageRow{}, err
	}
	r.CreatedAt = t
	return r, nil
}

func seedItems(t *testing.T, db *DB, rows []pageRow) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.Exec(ctx, `CREATE TABLE items (id TEXT PRIMARY KEY, created_at TEXT NOT NULL)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	for _, r := range rows {
		if _, err := db.Exec(ctx, `INSERT INTO items (id, created_at) VALUES (?, ?)`, r.ID, FormatTime(r.CreatedAt)); err != nil {
			t.Fatalf("insert %s: %v", r.ID, err)
		}
	}
}

func listItems(t *testing.T, db *DB, req crud.ListRequest) crud.Page[pageRow] {
	t.Helper()
	page, err := ListPage(context.Background(), db, "id, created_at", "items", "WHERE 1 = 1", nil, "created_at", "id", req,
		scanPageRow,
		func(r pageRow) (time.Time, string) { return r.CreatedAt, r.ID },
	)
	if err != nil {
		t.Fatalf("ListPage: %v", err)
	}
	return page
}

// TestListPage_EmptyPage: an empty result set yields an empty page — no items,
// no next cursor, HasMore false.
func TestListPage_EmptyPage(t *testing.T) {
	db := newMemDB(t)
	seedItems(t, db, nil)

	page := listItems(t, db, crud.ListRequest{Limit: 10})
	if len(page.Items) != 0 || page.HasMore || page.NextCursor != "" {
		t.Fatalf("empty page = %d items, hasMore=%v cursor=%q; want 0/false/empty",
			len(page.Items), page.HasMore, page.NextCursor)
	}
}

// TestListPage_OverFetchTrimAndExactLimit: five distinct-timestamp rows paged at
// limit 2 give 2+2+1 — the first two pages over-fetch, trim to the limit, and
// report HasMore with a next cursor; the final page holds the remainder with
// HasMore false. A four-row run then hits the exact-limit boundary: the last
// full page (2 == limit) must still report HasMore false, not a spurious page.
func TestListPage_OverFetchTrimAndExactLimit(t *testing.T) {
	base := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)

	t.Run("over_fetch_and_remainder", func(t *testing.T) {
		db := newMemDB(t)
		var rows []pageRow
		for i := 0; i < 5; i++ {
			rows = append(rows, pageRow{ID: "id-" + string(rune('a'+i)), CreatedAt: base.Add(time.Duration(i) * time.Minute)})
		}
		seedItems(t, db, rows)

		p1 := listItems(t, db, crud.ListRequest{Limit: 2})
		if len(p1.Items) != 2 || !p1.HasMore || p1.NextCursor == "" {
			t.Fatalf("page1 = %d items hasMore=%v cursor=%q; want 2/true/set", len(p1.Items), p1.HasMore, p1.NextCursor)
		}
		p2 := listItems(t, db, crud.ListRequest{Limit: 2, Cursor: p1.NextCursor})
		if len(p2.Items) != 2 || !p2.HasMore || p2.NextCursor == "" {
			t.Fatalf("page2 = %d items hasMore=%v; want 2/true", len(p2.Items), p2.HasMore)
		}
		p3 := listItems(t, db, crud.ListRequest{Limit: 2, Cursor: p2.NextCursor})
		if len(p3.Items) != 1 || p3.HasMore || p3.NextCursor != "" {
			t.Fatalf("page3 = %d items hasMore=%v cursor=%q; want 1/false/empty", len(p3.Items), p3.HasMore, p3.NextCursor)
		}
	})

	t.Run("exact_limit_boundary", func(t *testing.T) {
		db := newMemDB(t)
		var rows []pageRow
		for i := 0; i < 4; i++ {
			rows = append(rows, pageRow{ID: "id-" + string(rune('a'+i)), CreatedAt: base.Add(time.Duration(i) * time.Minute)})
		}
		seedItems(t, db, rows)

		p1 := listItems(t, db, crud.ListRequest{Limit: 2})
		if len(p1.Items) != 2 || !p1.HasMore {
			t.Fatalf("page1 = %d items hasMore=%v; want 2/true", len(p1.Items), p1.HasMore)
		}
		p2 := listItems(t, db, crud.ListRequest{Limit: 2, Cursor: p1.NextCursor})
		if len(p2.Items) != 2 || p2.HasMore || p2.NextCursor != "" {
			t.Fatalf("page2 (exact limit) = %d items hasMore=%v cursor=%q; want 2/false/empty",
				len(p2.Items), p2.HasMore, p2.NextCursor)
		}
	})
}

// TestListPage_CursorRoundTripTieBreak: three rows share an identical created_at
// and two share another. Paging at limit 2 forces a page boundary inside a
// same-created_at group, so correctness hinges on the (created_at, id) tie-break
// and on the cursor encoding the STORED created_at read back on the prior page.
// The full traversal must return every id exactly once in (created_at, id) DESC
// order.
func TestListPage_CursorRoundTripTieBreak(t *testing.T) {
	db := newMemDB(t)
	t0 := time.Date(2026, 7, 8, 9, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Minute)
	rows := []pageRow{
		{ID: "a", CreatedAt: t0},
		{ID: "b", CreatedAt: t0},
		{ID: "c", CreatedAt: t0},
		{ID: "d", CreatedAt: t1},
		{ID: "e", CreatedAt: t1},
	}
	seedItems(t, db, rows)

	// Expected order: t1 group (id DESC: e, d), then t0 group (id DESC: c, b, a).
	want := []string{"e", "d", "c", "b", "a"}

	var got []string
	seen := map[string]bool{}
	cursor := ""
	for i := 0; i < 100; i++ {
		page := listItems(t, db, crud.ListRequest{Limit: 2, Cursor: cursor})
		if len(page.Items) > 2 {
			t.Fatalf("page %d over limit: %d items", i, len(page.Items))
		}
		for _, r := range page.Items {
			if seen[r.ID] {
				t.Fatalf("id %q returned on more than one page", r.ID)
			}
			seen[r.ID] = true
			got = append(got, r.ID)
		}
		if !page.HasMore {
			break
		}
		if page.NextCursor == "" {
			t.Fatal("HasMore true but NextCursor empty")
		}
		cursor = page.NextCursor
	}

	if len(got) != len(want) {
		t.Fatalf("traversal = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("traversal order = %v, want %v", got, want)
		}
	}
}
