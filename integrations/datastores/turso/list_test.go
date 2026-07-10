package turso

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/crud"
)

// TestAppendCursorPredicate_Combos tables the tuple predicate for every
// direction × forPrevious combination — the operator table is the load-bearing
// oracle, and an inverted operator silently reverses pages. Time order values
// bind through FormatTime; positional args go [order value, pk].
func TestAppendCursorPredicate_Combos(t *testing.T) {
	ts := time.Date(2026, 7, 8, 12, 0, 0, 0, time.FixedZone("x", 3*3600))
	wantTS := FormatTime(ts)

	cases := []struct {
		name        string
		direction   string
		forPrevious bool
		wantOp      string
	}{
		{"asc_forward", crud.ASC, false, ">"},
		{"asc_previous", crud.ASC, true, "<"},
		{"desc_forward", crud.DESC, false, "<"},
		{"desc_previous", crud.DESC, true, ">"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf strings.Builder
			var args []any
			if err := appendCursorPredicate(&buf, &args, "created_at", "id", ts, "pk-1", tc.direction, tc.forPrevious, false); err != nil {
				t.Fatalf("appendCursorPredicate: %v", err)
			}

			want := ` WHERE ("created_at", "id") ` + tc.wantOp + " (?, ?)"
			if got := buf.String(); got != want {
				t.Fatalf("fragment =\n  %q\nwant\n  %q", got, want)
			}
			if len(args) != 2 || args[0] != wantTS || args[1] != "pk-1" {
				t.Fatalf("args = %#v, want [%q pk-1]", args, wantTS)
			}
		})
	}
}

// TestAppendCursorPredicate_AndWhenWherePresent: an existing WHERE makes the
// predicate an AND, and non-time order values bind unchanged in arg order.
func TestAppendCursorPredicate_AndWhenWherePresent(t *testing.T) {
	var buf strings.Builder
	buf.WriteString("SELECT id FROM widgets WHERE kind = ?")
	args := []any{"gadget"}
	if err := appendCursorPredicate(&buf, &args, "created_at", "id", int64(5), "pk-1", crud.ASC, false, false); err != nil {
		t.Fatalf("appendCursorPredicate: %v", err)
	}

	want := `SELECT id FROM widgets WHERE kind = ? AND ("created_at", "id") > (?, ?)`
	if got := buf.String(); got != want {
		t.Fatalf("fragment =\n  %q\nwant\n  %q", got, want)
	}
	if len(args) != 3 || args[0] != "gadget" || args[1] != int64(5) || args[2] != "pk-1" {
		t.Fatalf("args = %#v, want [gadget 5 pk-1]", args)
	}
}

// TestAppendCursorPredicate_CastLower: castLower wraps both sides of the order
// comparison in LOWER().
func TestAppendCursorPredicate_CastLower(t *testing.T) {
	var buf strings.Builder
	var args []any
	if err := appendCursorPredicate(&buf, &args, "name", "id", "Widget", "pk-1", crud.ASC, false, true); err != nil {
		t.Fatalf("appendCursorPredicate: %v", err)
	}
	want := ` WHERE (LOWER("name"), "id") > (LOWER(?), ?)`
	if got := buf.String(); got != want {
		t.Fatalf("fragment =\n  %q\nwant\n  %q", got, want)
	}
}

// TestAppendOrderBy_Combos: ORDER BY direction is the request direction forward
// and the flip for a previous-page probe; the pk tiebreaker shares the direction.
func TestAppendOrderBy_Combos(t *testing.T) {
	cases := []struct {
		name        string
		direction   string
		forPrevious bool
		wantDir     string
	}{
		{"asc_forward", crud.ASC, false, "ASC"},
		{"asc_previous", crud.ASC, true, "DESC"},
		{"desc_forward", crud.DESC, false, "DESC"},
		{"desc_previous", crud.DESC, true, "ASC"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf strings.Builder
			if err := appendOrderBy(&buf, "created_at", "id", tc.direction, tc.forPrevious, false); err != nil {
				t.Fatalf("appendOrderBy: %v", err)
			}
			want := ` ORDER BY "created_at" ` + tc.wantDir + `, "id" ` + tc.wantDir
			if got := buf.String(); got != want {
				t.Fatalf("clause =\n  %q\nwant\n  %q", got, want)
			}
		})
	}
}

// TestAppendOrderBy_PKIsOrder: when the order column is the pk, no duplicate
// tiebreaker term is emitted.
func TestAppendOrderBy_PKIsOrder(t *testing.T) {
	var buf strings.Builder
	if err := appendOrderBy(&buf, "id", "id", crud.ASC, false, false); err != nil {
		t.Fatalf("appendOrderBy: %v", err)
	}
	if got, want := buf.String(), ` ORDER BY "id" ASC`; got != want {
		t.Fatalf("clause = %q, want %q", got, want)
	}
}

// TestAppendOrderBy_RejectsBadIdentifier: a raw-expression / injected order
// column or pk fails loud with ErrInvalidInput — the pk is validated even when
// it equals the order column (page-1 path where no cursor predicate is added).
func TestAppendOrderBy_RejectsBadIdentifier(t *testing.T) {
	bad := []struct {
		name     string
		orderCol string
		pkCol    string
	}{
		{"order_expression", "a || char(0) || b", "id"},
		{"order_whitespace", "created at", "id"},
		{"order_quote", `name" OR "1"="1`, "id"},
		{"pk_expression", "created_at", "a || char(0) || b"},
		{"pk_semicolon", "created_at", "id; DROP TABLE t"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			var buf strings.Builder
			if err := appendOrderBy(&buf, tc.orderCol, tc.pkCol, crud.ASC, false, false); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("appendOrderBy err = %v, want ErrInvalidInput (buf=%q)", err, buf.String())
			}
		})
	}
}

// TestAppendCursorPredicate_RejectsBadIdentifier: the keyset predicate rejects a
// non-identifier order column or pk before writing any SQL.
func TestAppendCursorPredicate_RejectsBadIdentifier(t *testing.T) {
	bad := []struct {
		name     string
		orderCol string
		pkCol    string
	}{
		{"order_expression", "a || char(0) || b", "id"},
		{"pk_expression", "created_at", "a || char(0) || b"},
		{"pk_quote", "created_at", `id" OR "1"="1`},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			var buf strings.Builder
			var args []any
			if err := appendCursorPredicate(&buf, &args, tc.orderCol, tc.pkCol, int64(1), "pk", crud.ASC, false, false); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("appendCursorPredicate err = %v, want ErrInvalidInput", err)
			}
		})
	}
}

// listRow is a minimal record for the behavioral suite.
type listRow struct {
	ID        string
	CreatedAt time.Time
	Name      string
	N         int64
}

var listOrderFields = map[string]crud.OrderField{
	"created_at": {Column: "created_at"},
	"name":       {Column: "name"},
	"n":          {Column: "n"},
	"id":         {Column: "id"},
}

func scanListRow(s Scanner) (listRow, error) {
	var r listRow
	var ts string
	if err := s.Scan(&r.ID, &ts, &r.Name, &r.N); err != nil {
		return listRow{}, err
	}
	t, err := ParseTime(ts)
	if err != nil {
		return listRow{}, err
	}
	r.CreatedAt = t
	return r, nil
}

func listQueryFor(kind string) ListQuery[listRow] {
	return ListQuery[listRow]{
		BaseSQL:      "SELECT id, created_at, name, n FROM list_items WHERE kind = ?",
		Args:         []any{kind},
		OrderFields:  listOrderFields,
		DefaultOrder: crud.NewOrder("created_at", crud.DESC),
		PK:           "id",
		Scan:         scanListRow,
		OrderValueOf: func(r listRow, field string) any {
			switch field {
			case "name":
				return r.Name
			case "n":
				return r.N
			case "id":
				return r.ID
			default:
				return r.CreatedAt
			}
		},
		PKOf: func(r listRow) string { return r.ID },
	}
}

func seedListItems(t *testing.T, db *DB) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.Exec(ctx, `CREATE TABLE list_items (
		id TEXT PRIMARY KEY, created_at TEXT NOT NULL, name TEXT NOT NULL, n INTEGER NOT NULL, kind TEXT NOT NULL
	)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	base := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	rows := []listRow{
		{ID: "e1", CreatedAt: base.Add(0 * time.Minute), Name: "alpha", N: 30},
		{ID: "e2", CreatedAt: base.Add(1 * time.Minute), Name: "bravo", N: 10},
		{ID: "e3", CreatedAt: base.Add(2 * time.Minute), Name: "charlie", N: 20},
		{ID: "e4", CreatedAt: base.Add(3 * time.Minute), Name: "delta", N: 40},
		{ID: "e5", CreatedAt: base.Add(4 * time.Minute), Name: "echo", N: 50},
	}
	for _, r := range rows {
		if _, err := db.Exec(ctx, `INSERT INTO list_items (id, created_at, name, n, kind) VALUES (?, ?, ?, ?, ?)`,
			r.ID, FormatTime(r.CreatedAt), r.Name, r.N, "a"); err != nil {
			t.Fatalf("insert %s: %v", r.ID, err)
		}
	}
	for _, id := range []string{"b1", "b2"} {
		if _, err := db.Exec(ctx, `INSERT INTO list_items (id, created_at, name, n, kind) VALUES (?, ?, ?, ?, ?)`,
			id, FormatTime(base), id, int64(1), "b"); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
}

func traverseIDs(t *testing.T, db *DB, q ListQuery[listRow], order crud.Order, limit int) []string {
	t.Helper()
	ctx := context.Background()
	seen := map[string]bool{}
	var ids []string
	cursor := ""
	for i := 0; i < 1000; i++ {
		p, err := List(ctx, db, q, crud.ListRequest{Limit: limit, Cursor: cursor, Order: order})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		for _, r := range p.Items {
			if seen[r.ID] {
				t.Fatalf("id %q on more than one page", r.ID)
			}
			seen[r.ID] = true
			ids = append(ids, r.ID)
		}
		if !p.HasMore {
			break
		}
		cursor = p.NextCursor
	}
	return ids
}

func idsOf(rows []listRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out
}

func eqIDs(t *testing.T, got, want []string, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s = %v, want %v", label, got, want)
		}
	}
}

// TestList_Behavior drives List against in-memory SQLite (libSQL's dialect):
// forward traversal per order, prev-probe full/partial windows, offset↔cursor
// equivalence, count under a filter, and stale-cursor-as-first-page.
func TestList_Behavior(t *testing.T) {
	db := newMemDB(t)
	seedListItems(t, db)
	ctx := context.Background()
	q := listQueryFor("a")

	t.Run("forward_created_desc", func(t *testing.T) {
		eqIDs(t, traverseIDs(t, db, q, crud.NewOrder("created_at", crud.DESC), 2), []string{"e5", "e4", "e3", "e2", "e1"}, "created desc")
	})
	t.Run("forward_name_asc", func(t *testing.T) {
		eqIDs(t, traverseIDs(t, db, q, crud.NewOrder("name", crud.ASC), 2), []string{"e1", "e2", "e3", "e4", "e5"}, "name asc")
	})
	t.Run("forward_n_asc", func(t *testing.T) {
		eqIDs(t, traverseIDs(t, db, q, crud.NewOrder("n", crud.ASC), 2), []string{"e2", "e3", "e1", "e4", "e5"}, "n asc")
	})

	t.Run("prev_probe_full_window", func(t *testing.T) {
		desc := crud.NewOrder("created_at", crud.DESC)
		p1, _ := List(ctx, db, q, crud.ListRequest{Limit: 2, Order: desc})
		if p1.HasPrev {
			t.Fatal("first page HasPrev = true, want false")
		}
		p2, _ := List(ctx, db, q, crud.ListRequest{Limit: 2, Cursor: p1.NextCursor, Order: desc})
		p3, err := List(ctx, db, q, crud.ListRequest{Limit: 2, Cursor: p2.NextCursor, Order: desc})
		if err != nil {
			t.Fatalf("p3: %v", err)
		}
		if !p3.HasPrev || p3.PreviousCursor == "" {
			t.Fatalf("p3 HasPrev=%v prevCursor=%q, want true/set (full window)", p3.HasPrev, p3.PreviousCursor)
		}
		back, _ := List(ctx, db, q, crud.ListRequest{Limit: 2, Cursor: p3.PreviousCursor, Order: desc})
		eqIDs(t, idsOf(back.Items), []string{"e3", "e2"}, "previous cursor round-trip")
	})

	t.Run("prev_probe_partial_window", func(t *testing.T) {
		desc := crud.NewOrder("created_at", crud.DESC)
		p1, _ := List(ctx, db, q, crud.ListRequest{Limit: 3, Order: desc})
		p2, err := List(ctx, db, q, crud.ListRequest{Limit: 3, Cursor: p1.NextCursor, Order: desc})
		if err != nil {
			t.Fatalf("p2: %v", err)
		}
		if !p2.HasPrev || p2.PreviousCursor != "" {
			t.Fatalf("p2 HasPrev=%v prevCursor=%q, want true/empty (partial window)", p2.HasPrev, p2.PreviousCursor)
		}
	})

	t.Run("offset_matches_cursor", func(t *testing.T) {
		desc := crud.NewOrder("created_at", crud.DESC)
		var got []string
		for off := 0; off < 6; off += 2 {
			p, err := List(ctx, db, q, crud.ListRequest{Limit: 2, Offset: off, Order: desc, Strategy: crud.StrategyOffset})
			if err != nil {
				t.Fatalf("offset %d: %v", off, err)
			}
			if wantPrev := off > 0; p.HasPrev != wantPrev {
				t.Errorf("offset %d HasPrev = %v, want %v", off, p.HasPrev, wantPrev)
			}
			// Offset strategy emits no cursors at any offset — the caller does the
			// offset arithmetic.
			if p.NextCursor != "" || p.PreviousCursor != "" {
				t.Errorf("offset %d emitted cursors: next=%q prev=%q", off, p.NextCursor, p.PreviousCursor)
			}
			got = append(got, idsOf(p.Items)...)
		}
		eqIDs(t, got, []string{"e5", "e4", "e3", "e2", "e1"}, "offset traversal")
	})

	t.Run("count_under_filter", func(t *testing.T) {
		p, _ := List(ctx, db, q, crud.ListRequest{Limit: 2, WithCount: true})
		if p.Total == nil || *p.Total != 5 {
			t.Fatalf("Total = %v, want 5", p.Total)
		}
		pb, _ := List(ctx, db, listQueryFor("b"), crud.ListRequest{Limit: 10, WithCount: true})
		if pb.Total == nil || *pb.Total != 2 {
			t.Fatalf("Total(b) = %v, want 2", pb.Total)
		}
	})

	t.Run("stale_cursor_is_first_page", func(t *testing.T) {
		token, _ := crud.EncodeCursor("name", "delta", "e4")
		p, err := List(ctx, db, q, crud.ListRequest{Limit: 2, Cursor: token, Order: crud.NewOrder("created_at", crud.DESC)})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		eqIDs(t, idsOf(p.Items), []string{"e5", "e4"}, "stale cursor first page")
		if p.HasPrev {
			t.Error("stale-cursor first page HasPrev = true, want false")
		}
	})

	// limits_clamp: a ListQuery with a custom Max caps an over-large request — a
	// Limit of 3 against Max 2 returns the Max-2 window (plus HasMore from the
	// Max+1 over-fetch), where the default vocabulary would have returned 3.
	t.Run("limits_clamp", func(t *testing.T) {
		desc := crud.NewOrder("created_at", crud.DESC)
		clamped := q
		clamped.Limits = crud.Limits{Max: 2}
		p, err := List(ctx, db, clamped, crud.ListRequest{Limit: 3, Order: desc})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		eqIDs(t, idsOf(p.Items), []string{"e5", "e4"}, "limits clamp to Max 2")
		if !p.HasMore {
			t.Error("HasMore = false, want true (Max+1 over-fetch proves a next page)")
		}
	})
}

// TestList_RejectsInvalid: an invalid request and an unknown order field are
// rejected with ErrInvalidInput before any query runs.
func TestList_RejectsInvalid(t *testing.T) {
	db := newMemDB(t)
	seedListItems(t, db)
	ctx := context.Background()
	q := listQueryFor("a")

	if _, err := List(ctx, db, q, crud.ListRequest{Limit: 2, Cursor: "x", Offset: 3}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("cursor+offset err = %v, want ErrInvalidInput", err)
	}
	if _, err := List(ctx, db, q, crud.ListRequest{Limit: 2, Order: crud.NewOrder("password", crud.ASC)}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("unknown order err = %v, want ErrInvalidInput", err)
	}
}

// TestList_RejectsRawExpressionPKOnFirstPage: a ListQuery whose PK is a raw SQL
// expression (not a plain column) is rejected with ErrInvalidInput on page 1 —
// no cursor is present, so the guard must come from appendOrderBy's
// unconditional pk validation, not the cursor predicate. This is the guard that
// forced the roles store off its `subject_type || char(0) || …` tiebreak.
func TestList_RejectsRawExpressionPKOnFirstPage(t *testing.T) {
	db := newMemDB(t)
	seedListItems(t, db)
	ctx := context.Background()

	q := listQueryFor("a")
	q.PK = "id || char(0) || name"

	if _, err := List(ctx, db, q, crud.ListRequest{Limit: 2}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("raw-expression PK err = %v, want ErrInvalidInput", err)
	}
}
