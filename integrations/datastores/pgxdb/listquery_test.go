package pgxdb

import (
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// TestApplyCursorPagination_Combos tables the tuple predicate for every
// direction × forPrevious combination — the operator table is the load-bearing
// oracle, and an inverted operator silently reverses pages. It also asserts the
// two named args are bound (order value UTC-normalized, pk verbatim).
func TestApplyCursorPagination_Combos(t *testing.T) {
	// A cursor order value in a non-UTC zone; the builder must normalize to UTC.
	ts := time.Date(2026, 7, 8, 12, 0, 0, 0, time.FixedZone("x", 3*3600))
	wantTS := ts.UTC()

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
			args := pgx.NamedArgs{}
			if err := ApplyCursorPagination(&buf, args, "created_at", "id", ts, "pk-1", tc.direction, tc.forPrevious, false); err != nil {
				t.Fatalf("ApplyCursorPagination: %v", err)
			}

			want := ` WHERE ("created_at", "id") ` + tc.wantOp + ` (@cursor_order_value, @cursor_pk)`
			if got := buf.String(); got != want {
				t.Fatalf("fragment =\n  %q\nwant\n  %q", got, want)
			}

			gotTS, ok := args[cursorOrderValueArg].(time.Time)
			if !ok {
				t.Fatalf("cursor_order_value = %T, want time.Time", args[cursorOrderValueArg])
			}
			if !gotTS.Equal(wantTS) || gotTS.Location() != time.UTC {
				t.Errorf("cursor_order_value = %v (%v), want %v UTC", gotTS, gotTS.Location(), wantTS)
			}
			if args[cursorPKArg] != "pk-1" {
				t.Errorf("cursor_pk = %v, want pk-1", args[cursorPKArg])
			}
		})
	}
}

// TestApplyCursorPagination_AndWhenWherePresent: an existing WHERE clause makes
// the predicate an AND rather than a fresh WHERE.
func TestApplyCursorPagination_AndWhenWherePresent(t *testing.T) {
	var buf strings.Builder
	buf.WriteString("SELECT id FROM widgets WHERE kind = @kind")
	args := pgx.NamedArgs{"kind": "gadget"}
	if err := ApplyCursorPagination(&buf, args, "created_at", "id", int64(5), "pk-1", crud.ASC, false, false); err != nil {
		t.Fatalf("ApplyCursorPagination: %v", err)
	}
	want := `SELECT id FROM widgets WHERE kind = @kind AND ("created_at", "id") > (@cursor_order_value, @cursor_pk)`
	if got := buf.String(); got != want {
		t.Fatalf("fragment =\n  %q\nwant\n  %q", got, want)
	}
	if args[cursorOrderValueArg] != int64(5) {
		t.Errorf("cursor_order_value = %v, want int64(5)", args[cursorOrderValueArg])
	}
}

// TestApplyCursorPagination_CastLower: castLower wraps both sides of the order
// comparison in LOWER().
func TestApplyCursorPagination_CastLower(t *testing.T) {
	var buf strings.Builder
	args := pgx.NamedArgs{}
	if err := ApplyCursorPagination(&buf, args, "name", "id", "Widget", "pk-1", crud.ASC, false, true); err != nil {
		t.Fatalf("ApplyCursorPagination: %v", err)
	}
	want := ` WHERE (LOWER("name"), "id") > (LOWER(@cursor_order_value), @cursor_pk)`
	if got := buf.String(); got != want {
		t.Fatalf("fragment =\n  %q\nwant\n  %q", got, want)
	}
}

// TestApplyCursorPagination_RejectsBadIdentifier: an injection in the order
// column is rejected by QuoteIdentifier before any SQL is written.
func TestApplyCursorPagination_RejectsBadIdentifier(t *testing.T) {
	var buf strings.Builder
	args := pgx.NamedArgs{}
	if err := ApplyCursorPagination(&buf, args, "created_at; DROP", "id", int64(1), "pk", crud.ASC, false, false); err == nil {
		t.Fatal("expected error for injection in order column")
	}
}

// TestAddOrderByClause_Combos: ORDER BY direction is the request direction for
// forward paging and the flip for a previous-page probe; the pk tiebreaker
// shares the direction.
func TestAddOrderByClause_Combos(t *testing.T) {
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
			if err := AddOrderByClause(&buf, "created_at", "id", tc.direction, tc.forPrevious, false); err != nil {
				t.Fatalf("AddOrderByClause: %v", err)
			}
			want := ` ORDER BY "created_at" ` + tc.wantDir + `, "id" ` + tc.wantDir
			if got := buf.String(); got != want {
				t.Fatalf("clause =\n  %q\nwant\n  %q", got, want)
			}
		})
	}
}

// TestAddOrderByClause_PKIsOrder: when the order column is the pk, no duplicate
// tiebreaker term is emitted.
func TestAddOrderByClause_PKIsOrder(t *testing.T) {
	var buf strings.Builder
	if err := AddOrderByClause(&buf, "id", "id", crud.ASC, false, false); err != nil {
		t.Fatalf("AddOrderByClause: %v", err)
	}
	if got, want := buf.String(), ` ORDER BY "id" ASC`; got != want {
		t.Fatalf("clause = %q, want %q", got, want)
	}
}

// TestAddOrderByClause_CastLower: castLower wraps the order column in LOWER().
func TestAddOrderByClause_CastLower(t *testing.T) {
	var buf strings.Builder
	if err := AddOrderByClause(&buf, "name", "id", crud.ASC, false, true); err != nil {
		t.Fatalf("AddOrderByClause: %v", err)
	}
	if got, want := buf.String(), ` ORDER BY LOWER("name") ASC, "id" ASC`; got != want {
		t.Fatalf("clause = %q, want %q", got, want)
	}
}

// TestAddLimitClause: appends LIMIT @limit and binds the arg.
func TestAddLimitClause(t *testing.T) {
	var buf strings.Builder
	args := pgx.NamedArgs{}
	AddLimitClause(&buf, args, 26)
	if got, want := buf.String(), " LIMIT @limit"; got != want {
		t.Fatalf("clause = %q, want %q", got, want)
	}
	if args[limitArg] != 26 {
		t.Errorf("limit arg = %v, want 26", args[limitArg])
	}
}

// TestNormalizeOrderValue: time values are UTC-normalized; other types pass
// through unchanged.
func TestNormalizeOrderValue(t *testing.T) {
	ts := time.Date(2026, 7, 8, 9, 0, 0, 0, time.FixedZone("x", -5*3600))
	got, ok := normalizeOrderValue(ts).(time.Time)
	if !ok {
		t.Fatalf("normalizeOrderValue(time) = %T, want time.Time", normalizeOrderValue(ts))
	}
	if !got.Equal(ts.UTC()) || got.Location() != time.UTC {
		t.Errorf("normalizeOrderValue(time) = %v, want %v UTC", got, ts.UTC())
	}

	if v := normalizeOrderValue(int64(7)); v != int64(7) {
		t.Errorf("normalizeOrderValue(int64) = %v, want 7", v)
	}
	if v := normalizeOrderValue("abc"); v != "abc" {
		t.Errorf("normalizeOrderValue(string) = %v, want abc", v)
	}
}
