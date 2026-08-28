package pgxdb

import (
	"context"
	"errors"
	"testing"
	"time"

	jackpgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// errCapture short-circuits a capture Querier right after it hands the
// assembled query to the driver, so a hermetic test can assert the SQL text and
// args the helper builds — without a live database.
var errCapture = errors.New("captured")

// listRow is a minimal db-tagged row struct standing in for a store aggregate.
type listRow struct {
	ID        string    `db:"id"`
	CreatedAt time.Time `db:"created_at"`
}

// listCapture short-circuits List right where it hands the assembled main query
// to the Querier, so a hermetic test can assert the SQL text and NamedArgs the
// helper builds — without a live database.
type listCapture struct {
	query string
	args  jackpgx.NamedArgs
}

func (c *listCapture) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errCapture
}

func (c *listCapture) Query(_ context.Context, query string, args ...any) (jackpgx.Rows, error) {
	c.query = query
	if len(args) == 1 {
		c.args, _ = args[0].(jackpgx.NamedArgs)
	}
	return nil, errCapture
}

func (c *listCapture) QueryRow(context.Context, string, ...any) jackpgx.Row { return nil }

var listOrderFields = map[string]crud.OrderField{
	"created_at": {Column: "created_at"},
	"id":         {Column: "id"},
}

func newListQuery() ListQuery[listRow] {
	return ListQuery[listRow]{
		BaseSQL:      "SELECT id, created_at FROM widgets WHERE kind = @kind",
		Args:         jackpgx.NamedArgs{"kind": "gadget"},
		OrderFields:  listOrderFields,
		DefaultOrder: crud.NewOrder("created_at", crud.DESC),
		PK:           "id",
		OrderValueOf: func(r listRow, _ string) any { return r.CreatedAt },
		PKOf:         func(r listRow) string { return r.ID },
	}
}

// TestList_FirstPageSQL: the first page (no cursor, no offset) carries the
// DefaultOrder ORDER BY and LIMIT n+1 with no keyset predicate.
func TestList_FirstPageSQL(t *testing.T) {
	cq := &listCapture{}
	_, err := List(context.Background(), cq, newListQuery(), crud.ListRequest{Limit: 2})
	if !errors.Is(err, errCapture) {
		t.Fatalf("err = %v, want errCapture", err)
	}
	want := `SELECT id, created_at FROM widgets WHERE kind = @kind ORDER BY "created_at" DESC, "id" DESC LIMIT @limit`
	if cq.query != want {
		t.Fatalf("query =\n  %q\nwant\n  %q", cq.query, want)
	}
	if cq.args["kind"] != "gadget" || cq.args["limit"] != 3 {
		t.Fatalf("args = %#v, want kind=gadget limit=3", cq.args)
	}
}

// TestList_LimitsClamp: a ListQuery with a custom Limits caps the over-fetch —
// a request Limit above the resource's Max is clamped to Max, so the bound LIMIT
// arg is Max+1 (the +1 over-fetch), not the request value.
func TestList_LimitsClamp(t *testing.T) {
	q := newListQuery()
	q.Limits = crud.Limits{Max: 5}

	cq := &listCapture{}
	_, err := List(context.Background(), cq, q, crud.ListRequest{Limit: 6})
	if !errors.Is(err, errCapture) {
		t.Fatalf("err = %v, want errCapture", err)
	}
	if cq.args["limit"] != 6 {
		t.Fatalf("limit arg = %v, want 6 (Max 5 + 1 over-fetch)", cq.args["limit"])
	}
}

// TestList_CursorPageSQL: a cursor appends the keyset tuple predicate as an AND
// and binds the cursor order value (UTC time) + pk.
func TestList_CursorPageSQL(t *testing.T) {
	ct := time.Date(2026, 7, 8, 12, 0, 0, 0, time.FixedZone("x", 2*3600))
	token, err := crud.EncodeCursor("created_at", ct, "pk-9")
	if err != nil {
		t.Fatalf("EncodeCursor: %v", err)
	}

	cq := &listCapture{}
	_, err = List(context.Background(), cq, newListQuery(), crud.ListRequest{Limit: 2, Cursor: token})
	if !errors.Is(err, errCapture) {
		t.Fatalf("err = %v, want errCapture", err)
	}
	want := `SELECT id, created_at FROM widgets WHERE kind = @kind ` +
		`AND ("created_at", "id") < (@cursor_order_value, @cursor_pk) ` +
		`ORDER BY "created_at" DESC, "id" DESC LIMIT @limit`
	if cq.query != want {
		t.Fatalf("query =\n  %q\nwant\n  %q", cq.query, want)
	}
	gotTS, ok := cq.args["cursor_order_value"].(time.Time)
	if !ok || !gotTS.Equal(ct.UTC()) {
		t.Fatalf("cursor_order_value = %v, want %v", cq.args["cursor_order_value"], ct.UTC())
	}
	if cq.args["cursor_pk"] != "pk-9" {
		t.Errorf("cursor_pk = %v, want pk-9", cq.args["cursor_pk"])
	}
}

// TestList_OffsetPageSQL: offset mode appends LIMIT n+1 OFFSET off and no
// keyset predicate.
func TestList_OffsetPageSQL(t *testing.T) {
	cq := &listCapture{}
	_, err := List(context.Background(), cq, newListQuery(), crud.ListRequest{Limit: 2, Offset: 4, Strategy: crud.StrategyOffset})
	if !errors.Is(err, errCapture) {
		t.Fatalf("err = %v, want errCapture", err)
	}
	want := `SELECT id, created_at FROM widgets WHERE kind = @kind ORDER BY "created_at" DESC, "id" DESC LIMIT @limit OFFSET @offset`
	if cq.query != want {
		t.Fatalf("query =\n  %q\nwant\n  %q", cq.query, want)
	}
	if cq.args["offset"] != 4 || cq.args["limit"] != 3 {
		t.Fatalf("args = %#v, want offset=4 limit=3", cq.args)
	}
}

// TestList_StaleCursorIsFirstPage: a token whose order field no longer matches
// the resolved order decodes to a first page — no keyset predicate.
func TestList_StaleCursorIsFirstPage(t *testing.T) {
	token, err := crud.EncodeCursor("name", "Widget", "pk-1")
	if err != nil {
		t.Fatalf("EncodeCursor: %v", err)
	}
	cq := &listCapture{}
	_, err = List(context.Background(), cq, newListQuery(), crud.ListRequest{Limit: 2, Cursor: token})
	if !errors.Is(err, errCapture) {
		t.Fatalf("err = %v, want errCapture", err)
	}
	want := `SELECT id, created_at FROM widgets WHERE kind = @kind ORDER BY "created_at" DESC, "id" DESC LIMIT @limit`
	if cq.query != want {
		t.Fatalf("query =\n  %q\nwant\n  %q (stale cursor should be first page)", cq.query, want)
	}
}

// TestList_ValidateRejectsBadRequest: an invalid request (cursor+offset) is
// rejected by Validate before any SQL is built.
func TestList_ValidateRejectsBadRequest(t *testing.T) {
	cq := &listCapture{}
	_, err := List(context.Background(), cq, newListQuery(), crud.ListRequest{Limit: 2, Cursor: "x", Offset: 3})
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if cq.query != "" {
		t.Fatalf("query built despite invalid request: %q", cq.query)
	}
}

// TestList_UnknownOrderRejected: an order field outside the allow-list is
// rejected with ErrInvalidInput before any SQL is built.
func TestList_UnknownOrderRejected(t *testing.T) {
	cq := &listCapture{}
	req := crud.ListRequest{Limit: 2, Order: crud.NewOrder("password", crud.ASC)}
	_, err := List(context.Background(), cq, newListQuery(), req)
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if cq.query != "" {
		t.Fatalf("query built despite unknown order: %q", cq.query)
	}
}

// newFixedOrderQuery is a store whose order is the product's, not the caller's:
// no OrderFields, a composite FixedOrder with NULLS LAST and the pk tiebreak
// included by the store itself.
func newFixedOrderQuery() ListQuery[listRow] {
	return ListQuery[listRow]{
		BaseSQL:    "SELECT id, created_at FROM widgets WHERE kind = @kind",
		Args:       jackpgx.NamedArgs{"kind": "gadget"},
		FixedOrder: "closing_date DESC NULLS LAST, name ASC, id ASC",
		PK:         "id",
		PKOf:       func(r listRow) string { return r.ID },
	}
}

// TestList_FixedOrderSQL: under the offset strategy a FixedOrder is written
// verbatim as the ORDER BY — no quoting, no appended pk tiebreak — and the
// offset flow is otherwise unchanged: LIMIT n+1 OFFSET off.
func TestList_FixedOrderSQL(t *testing.T) {
	cq := &listCapture{}
	_, err := List(context.Background(), cq, newFixedOrderQuery(), crud.ListRequest{Limit: 2, Offset: 4, Strategy: crud.StrategyOffset})
	if !errors.Is(err, errCapture) {
		t.Fatalf("err = %v, want errCapture", err)
	}
	want := `SELECT id, created_at FROM widgets WHERE kind = @kind ORDER BY closing_date DESC NULLS LAST, name ASC, id ASC LIMIT @limit OFFSET @offset`
	if cq.query != want {
		t.Fatalf("query =\n  %q\nwant\n  %q", cq.query, want)
	}
	if cq.args["kind"] != "gadget" || cq.args["offset"] != 4 || cq.args["limit"] != 3 {
		t.Fatalf("args = %#v, want kind=gadget offset=4 limit=3", cq.args)
	}
}

// TestList_FixedOrderSearchFolded: the search predicate is folded into the base
// BEFORE the fixed ORDER BY, exactly as on the OrderFields path.
func TestList_FixedOrderSearchFolded(t *testing.T) {
	q := newFixedOrderQuery()
	q.SearchFields = []crud.SearchField{{Column: "name"}}

	cq := &listCapture{}
	_, err := List(context.Background(), cq, q, crud.ListRequest{Limit: 2, Strategy: crud.StrategyOffset, Search: "gizmo"})
	if !errors.Is(err, errCapture) {
		t.Fatalf("err = %v, want errCapture", err)
	}
	want := `SELECT id, created_at FROM widgets WHERE kind = @kind AND (("name" COLLATE "C") ILIKE @list_search ESCAPE '\') ` +
		`ORDER BY closing_date DESC NULLS LAST, name ASC, id ASC LIMIT @limit OFFSET @offset`
	if cq.query != want {
		t.Fatalf("query =\n  %q\nwant\n  %q", cq.query, want)
	}
	if cq.args["list_search"] != "%gizmo%" {
		t.Fatalf("list_search arg = %v, want %%gizmo%%", cq.args["list_search"])
	}
}

// TestList_FixedOrderRefusals: each FixedOrder rule is enforced before any SQL
// is built, and every refusal wraps sdk.ErrInvalidInput — a request Order (the
// order is not the caller's), the cursor strategy (default or explicit, and a
// cursor token), and OrderFields set alongside FixedOrder (a programming error).
func TestList_FixedOrderRefusals(t *testing.T) {
	both := newFixedOrderQuery()
	both.OrderFields = listOrderFields

	cases := []struct {
		name string
		q    ListQuery[listRow]
		req  crud.ListRequest
	}{
		{"request order", newFixedOrderQuery(), crud.ListRequest{Limit: 2, Strategy: crud.StrategyOffset, Order: crud.NewOrder("id", crud.ASC)}},
		{"default (cursor) strategy", newFixedOrderQuery(), crud.ListRequest{Limit: 2}},
		{"explicit cursor strategy", newFixedOrderQuery(), crud.ListRequest{Limit: 2, Strategy: crud.StrategyCursor}},
		{"cursor token", newFixedOrderQuery(), crud.ListRequest{Limit: 2, Cursor: "abc"}},
		{"OrderFields alongside FixedOrder", both, crud.ListRequest{Limit: 2, Strategy: crud.StrategyOffset}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cq := &listCapture{}
			_, err := List(context.Background(), cq, tc.q, tc.req)
			if !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
			if cq.query != "" {
				t.Fatalf("query built despite refusal: %q", cq.query)
			}
		})
	}
}

// TestList_FixedOrderZeroValueKeepsOrderFields: a query without FixedOrder is
// byte-identical to before — the OrderFields path and DefaultOrder still apply.
func TestList_FixedOrderZeroValueKeepsOrderFields(t *testing.T) {
	cq := &listCapture{}
	_, err := List(context.Background(), cq, newListQuery(), crud.ListRequest{Limit: 2, Strategy: crud.StrategyOffset})
	if !errors.Is(err, errCapture) {
		t.Fatalf("err = %v, want errCapture", err)
	}
	want := `SELECT id, created_at FROM widgets WHERE kind = @kind ORDER BY "created_at" DESC, "id" DESC LIMIT @limit OFFSET @offset`
	if cq.query != want {
		t.Fatalf("query =\n  %q\nwant\n  %q", cq.query, want)
	}
}
