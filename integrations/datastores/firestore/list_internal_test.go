package firestore

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	gcfs "cloud.google.com/go/firestore"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// The hermetic half of C4. Everything here is a DECISION the List helper makes
// before any I/O: which field and direction the request resolves to, which
// callbacks must be present, and — through the vendor's own query
// serialization — exactly which OrderBy clauses and cursor values the helper
// builds. No RPC is issued: the client is constructed against an emulator
// endpoint that is never dialed (gRPC dials lazily), so building references and
// serializing queries is pure client-side work.
//
// The behavioral half (does the server actually page that way) is
// list_integration_test.go.

// listRow is the row type the hermetic cases parametrize ListQuery with.
type listRow struct {
	ID        string
	CreatedAt time.Time
	Name      string
}

// hermeticDB opens a client that never talks to anything.
func hermeticDB(t *testing.T) *DB {
	t.Helper()
	// An unroutable emulator endpoint: the vendor uses insecure credentials and
	// dials lazily, so Open does no I/O and neither does any query BUILDER.
	t.Setenv("FIRESTORE_EMULATOR_HOST", "127.0.0.1:1")

	db, err := Open(context.Background(), Config{ProjectID: "gopernicus-hermetic"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// hermeticQuery is a ListQuery with every required callback wired.
func hermeticQuery(t *testing.T, db *DB) ListQuery[listRow] {
	t.Helper()
	return ListQuery[listRow]{
		Query: db.Collection("list_items").Where("kind", "==", "a"),
		OrderFields: map[string]list.OrderField{
			"created_at": {Column: "created_at"},
			"name":       {Column: "name"},
			"id":         {Column: "id"},
			"doc_id":     {Column: gcfs.DocumentID},
		},
		DefaultOrder: list.NewOrder("created_at", list.DESC),
		PK:           "id",
		Decode:       func(*gcfs.DocumentSnapshot) (listRow, error) { return listRow{}, nil },
		OrderValueOf: func(r listRow, field string) any {
			if field == "name" {
				return r.Name
			}
			return r.CreatedAt
		},
		PKOf: func(r listRow) string { return r.ID },
	}
}

// TestResolveOrder covers the allow-list, the default, and the direction map.
func TestResolveOrder(t *testing.T) {
	db := hermeticDB(t)
	q := hermeticQuery(t, db)

	cases := []struct {
		name      string
		order     list.Order
		wantField string
		wantDir   gcfs.Direction
	}{
		{"explicit_desc", list.NewOrder("name", list.DESC), "name", gcfs.Desc},
		{"explicit_asc", list.NewOrder("name", list.ASC), "name", gcfs.Asc},
		{"blank_uses_default", list.Order{}, "created_at", gcfs.Desc},
		{"unknown_direction_is_asc", list.Order{Field: "name", Direction: "sideways"}, "name", gcfs.Asc},
		{"document_id_is_orderable", list.NewOrder(gcfs.DocumentID, list.ASC), gcfs.DocumentID, gcfs.Asc},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			field, direction, err := q.resolveOrder(tc.order)
			if err != nil {
				t.Fatalf("resolveOrder: %v", err)
			}
			if field != tc.wantField || direction != tc.wantDir {
				t.Fatalf("resolveOrder = (%q, %v), want (%q, %v)", field, direction, tc.wantField, tc.wantDir)
			}
		})
	}
}

// TestResolveOrderRejectsUnknownField: a field outside the allow-list never
// reaches a query.
func TestResolveOrderRejectsUnknownField(t *testing.T) {
	db := hermeticDB(t)
	q := hermeticQuery(t, db)

	if _, _, err := q.resolveOrder(list.NewOrder("password", list.ASC)); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("resolveOrder err = %v, want ErrInvalidInput", err)
	}
}

// TestResolveOrderRefusesCastLower: Firestore cannot fold case in an index, so
// a CastLower order field fails loud instead of silently serving byte order.
func TestResolveOrderRefusesCastLower(t *testing.T) {
	db := hermeticDB(t)
	q := hermeticQuery(t, db)
	q.OrderFields = map[string]list.OrderField{"name": {Column: "name", CastLower: true}}

	_, _, err := q.resolveOrder(list.NewOrder("name", list.ASC))
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("resolveOrder err = %v, want ErrInvalidInput", err)
	}
}

// TestListValidate: the three required callbacks and the search rule are
// checked before any document is read.
func TestListValidate(t *testing.T) {
	db := hermeticDB(t)
	ctx := context.Background()

	t.Run("missing_callbacks", func(t *testing.T) {
		for _, tc := range []struct {
			name    string
			breakIt func(*ListQuery[listRow])
		}{
			{"decode", func(q *ListQuery[listRow]) { q.Decode = nil }},
			{"order_value_of", func(q *ListQuery[listRow]) { q.OrderValueOf = nil }},
			{"pk_of", func(q *ListQuery[listRow]) { q.PKOf = nil }},
		} {
			t.Run(tc.name, func(t *testing.T) {
				q := hermeticQuery(t, db)
				tc.breakIt(&q)
				if _, err := List(ctx, db.ReaderFrom(ctx), q, list.Request{Limit: 2}); !errors.Is(err, sdk.ErrInvalidInput) {
					t.Fatalf("List err = %v, want ErrInvalidInput", err)
				}
			})
		}
	})

	t.Run("search_without_a_postfilter_is_refused", func(t *testing.T) {
		q := hermeticQuery(t, db)
		if _, err := List(ctx, db.ReaderFrom(ctx), q, list.Request{Limit: 2, Search: "gear"}); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("List err = %v, want ErrInvalidInput (an unfiltered page must not answer a search)", err)
		}
	})

	t.Run("blank_search_is_not_a_search", func(t *testing.T) {
		q := hermeticQuery(t, db)
		for _, term := range []string{"", "   "} {
			if err := q.validate(list.Request{Search: term}); err != nil {
				t.Fatalf("validate(search=%q) = %v, want nil", term, err)
			}
		}
	})

	t.Run("search_with_a_postfilter_is_accepted", func(t *testing.T) {
		q := hermeticQuery(t, db)
		q.PostFilter = func(listRow) bool { return true }
		if err := q.validate(list.Request{Search: "gear"}); err != nil {
			t.Fatalf("validate = %v, want nil", err)
		}
	})

	// An invalid request is rejected by crud before the ListQuery is inspected.
	t.Run("cursor_and_offset_together", func(t *testing.T) {
		q := hermeticQuery(t, db)
		if _, err := List(ctx, db.ReaderFrom(ctx), q, list.Request{Limit: 2, Cursor: "x", Offset: 3}); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("List err = %v, want ErrInvalidInput", err)
		}
	})
}

// TestPKDefaultsToTheDocumentID pins the empty-PK meaning.
func TestPKDefaultsToTheDocumentID(t *testing.T) {
	db := hermeticDB(t)
	q := hermeticQuery(t, db)

	if got := q.pk(); got != "id" {
		t.Fatalf("pk() = %q, want %q", got, "id")
	}
	q.PK = ""
	if got := q.pk(); got != gcfs.DocumentID {
		t.Fatalf("pk() = %q, want %q", got, gcfs.DocumentID)
	}
}

// TestOrderedClauses is the query-construction oracle: the query the helper
// builds is compared, through the vendor's own serialization, with a
// hand-written query. It proves the tuple order, the direction flip, and that a
// PK equal to the order field is emitted ONCE — a duplicate clause would both
// be rejected by the server and desynchronize the cursor arity.
func TestOrderedClauses(t *testing.T) {
	db := hermeticDB(t)
	q := hermeticQuery(t, db)
	base := db.Collection("list_items").Where("kind", "==", "a")

	cases := []struct {
		name    string
		field   string
		dir     gcfs.Direction
		reverse bool
		pk      string
		want    gcfs.Query
	}{
		{
			name:  "field_then_pk_desc",
			field: "created_at", dir: gcfs.Desc, pk: "id",
			want: base.OrderBy("created_at", gcfs.Desc).OrderBy("id", gcfs.Desc),
		},
		{
			name:  "field_then_pk_asc",
			field: "created_at", dir: gcfs.Asc, pk: "id",
			want: base.OrderBy("created_at", gcfs.Asc).OrderBy("id", gcfs.Asc),
		},
		{
			name:  "reverse_flips_both_clauses",
			field: "created_at", dir: gcfs.Desc, reverse: true, pk: "id",
			want: base.OrderBy("created_at", gcfs.Asc).OrderBy("id", gcfs.Asc),
		},
		{
			name:  "pk_equals_order_field_emits_one_clause",
			field: "id", dir: gcfs.Asc, pk: "id",
			want: base.OrderBy("id", gcfs.Asc),
		},
		{
			name:  "empty_pk_orders_by_document_id",
			field: "created_at", dir: gcfs.Desc, pk: "",
			want: base.OrderBy("created_at", gcfs.Desc).OrderBy(gcfs.DocumentID, gcfs.Desc),
		},
		{
			name:  "document_id_order_with_empty_pk_emits_one_clause",
			field: gcfs.DocumentID, dir: gcfs.Asc, pk: "",
			want: base.OrderBy(gcfs.DocumentID, gcfs.Asc),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q.PK = tc.pk
			got := q.ordered(tc.field, tc.dir, tc.reverse)
			assertSameQuery(t, got, tc.want)
		})
	}
}

// TestCursorArityMatchesTheOrderClauses is the pairing invariant, proved by the
// vendor: it rejects a StartAfter whose value count differs from the OrderBy
// count, so a query that serializes cleanly has exactly one cursor value per
// clause. The negative row shows the same check failing when the pairing is
// broken on purpose.
func TestCursorArityMatchesTheOrderClauses(t *testing.T) {
	db := hermeticDB(t)
	q := hermeticQuery(t, db)
	cursor := &list.Cursor{OrderField: "created_at", OrderValue: time.Now().UTC(), PK: "e4"}

	t.Run("distinct_field_and_pk", func(t *testing.T) {
		position := q.cursorPosition(cursor, "created_at")
		if len(position) != 2 {
			t.Fatalf("cursorPosition = %v, want two values", position)
		}
		if _, err := q.ordered("created_at", gcfs.Desc, false).StartAfter(position...).Serialize(); err != nil {
			t.Fatalf("Serialize: %v", err)
		}
	})

	t.Run("pk_is_the_order_field", func(t *testing.T) {
		idCursor := &list.Cursor{OrderField: "id", OrderValue: "e4", PK: "e4"}
		position := q.cursorPosition(idCursor, "id")
		if len(position) != 1 || position[0] != "e4" {
			t.Fatalf("cursorPosition = %v, want the single pk value", position)
		}
		if _, err := q.ordered("id", gcfs.Asc, false).StartAfter(position...).Serialize(); err != nil {
			t.Fatalf("Serialize: %v", err)
		}
	})

	t.Run("document_id_cursor_is_the_id_string", func(t *testing.T) {
		q.PK = ""
		idCursor := &list.Cursor{OrderField: gcfs.DocumentID, OrderValue: "e4", PK: "e4"}
		position := q.cursorPosition(idCursor, gcfs.DocumentID)
		// The vendor resolves a document-id cursor value from a plain string
		// relative to the query's own collection, so no *DocumentRef is needed.
		if _, err := q.ordered(gcfs.DocumentID, gcfs.Asc, false).StartAfter(position...).Serialize(); err != nil {
			t.Fatalf("Serialize: %v", err)
		}
	})

	t.Run("a_broken_pairing_is_rejected", func(t *testing.T) {
		if _, err := q.ordered("created_at", gcfs.Desc, false).StartAfter("only-one-value").Serialize(); err == nil {
			t.Fatal("a one-value cursor against a two-clause order serialized without error")
		}
	})
}

// TestFlipDirection is the two-row truth table the reverse probe rests on.
func TestFlipDirection(t *testing.T) {
	if got := flipDirection(gcfs.Asc); got != gcfs.Desc {
		t.Errorf("flipDirection(Asc) = %v, want Desc", got)
	}
	if got := flipDirection(gcfs.Desc); got != gcfs.Asc {
		t.Errorf("flipDirection(Desc) = %v, want Asc", got)
	}
}

// assertSameQuery compares two queries through the vendor's serialization, the
// only handle a test has on a Query's private clauses.
func assertSameQuery(t *testing.T, got, want gcfs.Query) {
	t.Helper()
	gotBytes, err := got.Serialize()
	if err != nil {
		t.Fatalf("serializing the built query: %v", err)
	}
	wantBytes, err := want.Serialize()
	if err != nil {
		t.Fatalf("serializing the expected query: %v", err)
	}
	if !bytes.Equal(gotBytes, wantBytes) {
		t.Fatalf("the built query differs from the expected one:\n got %x\nwant %x", gotBytes, wantBytes)
	}
}
