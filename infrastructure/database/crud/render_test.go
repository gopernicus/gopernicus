package crud

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk/fop"
)

// fakeQuerier captures the rendered SQL and args without a database.
type fakeQuerier struct {
	query string
	args  []any
	cols  []string
}

func (f *fakeQuerier) Query(_ context.Context, query string, args ...any) (Rows, error) {
	f.query, f.args = query, args
	return &fakeRows{cols: f.cols}, nil
}

func (f *fakeQuerier) Exec(_ context.Context, query string, args ...any) (int64, error) {
	f.query, f.args = query, args
	return 1, nil
}

type fakeRows struct{ cols []string }

func (r *fakeRows) Columns() ([]string, error) { return r.cols, nil }
func (r *fakeRows) Next() bool                 { return false }
func (r *fakeRows) Scan(...any) error          { return nil }
func (r *fakeRows) Err() error                 { return nil }
func (r *fakeRows) Close() error               { return nil }

type pgRec struct {
	ID        string    `db:"id"`
	Name      string    `db:"name"`
	Tags      []string  `db:"tags"`
	CreatedAt time.Time `db:"created_at"`
}

type pgFilter struct {
	Name          *string
	Tags          []string
	After         *time.Time
	SearchTerm    *string
	AuthorizedIDs []string
}

func pgSpec(search *SearchSpec) Spec[pgRec, pgFilter, struct{}, struct{}] {
	return Spec[pgRec, pgFilter, struct{}, struct{}]{
		Table:   "things",
		PK:      "id",
		Columns: []string{"id", "name", "tags", "created_at"},
		Filters: func(f pgFilter) []Pred {
			var preds []Pred
			if f.Name != nil {
				preds = append(preds, Pred{Col: "name", Op: OpEq, Val: *f.Name})
			}
			if f.Tags != nil {
				preds = append(preds, Pred{Col: "tags", Op: OpIn, Val: f.Tags})
			}
			if f.After != nil {
				preds = append(preds, Pred{Col: "created_at", Op: OpGT, Val: *f.After})
			}
			return preds
		},
		Search:        search,
		SearchTerm:    func(f pgFilter) *string { return f.SearchTerm },
		AuthorizedIDs: func(f pgFilter) []string { return f.AuthorizedIDs },
		OrderFields:   map[string]OrderField{"created_at": {Col: "created_at"}, "name": {Col: "name", CastLower: true}},
		DefaultOrder:  "created_at",
	}
}

func TestPostgresRenderList(t *testing.T) {
	q := &fakeQuerier{cols: []string{"id", "name", "tags", "created_at"}}
	store, err := NewStore(q, PostgresDialect{}, pgSpec(&SearchSpec{Strategy: SearchTSVector, Column: "search_vector"}))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	name := "widget"
	term := "blue widget"
	after := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err = store.List(context.Background(), pgFilter{
		Name:          &name,
		Tags:          []string{"a", "b"},
		After:         &after,
		SearchTerm:    &term,
		AuthorizedIDs: []string{"id1", "id2"},
	}, fop.NewOrder("created_at", fop.DESC), fop.PageStringCursor{Limit: 11}, false)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	for _, want := range []string{
		`"name" = $1`,
		`"tags" = ANY($2)`, // array membership via single array arg (jsonb/array columns ride the same path)
		`"created_at" > $3`,
		`"id" = ANY($4)`, // prefilter authorization
		`"search_vector" @@ websearch_to_tsquery($5::regconfig, $6)`,
		`ORDER BY "created_at" DESC, "id" DESC`,
		`LIMIT $7`,
	} {
		if !strings.Contains(q.query, want) {
			t.Errorf("rendered SQL missing %q:\n%s", want, q.query)
		}
	}
	if len(q.args) != 7 {
		t.Errorf("args = %d, want 7: %v", len(q.args), q.args)
	}
	// time.Time arg passes through natively on Postgres.
	if _, ok := q.args[2].(time.Time); !ok {
		t.Errorf("created_at arg = %T, want time.Time", q.args[2])
	}
	// Authorized IDs arrive as one array argument.
	if ids, ok := q.args[3].([]any); !ok || len(ids) != 2 {
		t.Errorf("authorized ids arg = %#v, want []any of 2", q.args[3])
	}
}

func TestPostgresRenderEmptyAuthorizedIDs(t *testing.T) {
	q := &fakeQuerier{cols: []string{"id", "name", "tags", "created_at"}}
	store, err := NewStore(q, PostgresDialect{}, pgSpec(nil))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	_, err = store.List(context.Background(), pgFilter{AuthorizedIDs: []string{}}, fop.Order{}, fop.PageStringCursor{Limit: 5}, false)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// Empty non-nil renders = ANY over an empty array — a no-match, never a
	// dropped predicate.
	if !strings.Contains(q.query, `"id" = ANY($1)`) {
		t.Errorf("rendered SQL missing empty-set authorization predicate:\n%s", q.query)
	}
}

func TestPostgresRenderWebSearch(t *testing.T) {
	q := &fakeQuerier{cols: []string{"id", "name", "tags", "created_at"}}
	store, err := NewStore(q, PostgresDialect{}, pgSpec(&SearchSpec{Strategy: SearchWebSearch, Fields: []string{"name"}}))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	term := "blue"
	_, err = store.List(context.Background(), pgFilter{SearchTerm: &term}, fop.Order{}, fop.PageStringCursor{Limit: 5}, false)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(q.query, `to_tsvector($1::regconfig, coalesce("name", '')) @@ websearch_to_tsquery($2::regconfig, $3)`) {
		t.Errorf("web_search render:\n%s", q.query)
	}
}

func TestPostgresRenderCursorTuple(t *testing.T) {
	q := &fakeQuerier{cols: []string{"id", "name", "tags", "created_at"}}
	store, err := NewStore(q, PostgresDialect{}, pgSpec(nil))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	cursor, err := fop.EncodeCursor("created_at", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), "row9")
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	_, err = store.List(context.Background(), pgFilter{}, fop.NewOrder("created_at", fop.ASC), fop.PageStringCursor{Limit: 5, Cursor: cursor}, false)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(q.query, `("created_at", "id") > ($1, $2)`) {
		t.Errorf("cursor tuple render:\n%s", q.query)
	}
	// The typed cursor restores time.Time, which Postgres receives natively.
	if _, ok := q.args[0].(time.Time); !ok {
		t.Errorf("cursor order value arg = %T, want time.Time", q.args[0])
	}
}

func TestSQLiteRenderDelta(t *testing.T) {
	q := &fakeQuerier{cols: []string{"id", "name", "tags", "created_at"}}
	store, err := NewStore(q, SQLiteDialect{}, pgSpec(&SearchSpec{Strategy: SearchContains, Fields: []string{"name"}}))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	term := "blue"
	after := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err = store.List(context.Background(), pgFilter{
		Tags:          []string{"a", "b"},
		After:         &after,
		SearchTerm:    &term,
		AuthorizedIDs: []string{},
	}, fop.Order{}, fop.PageStringCursor{Limit: 5}, false)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	for _, want := range []string{
		`"tags" IN (?, ?)`, // expanded placeholders, not array binding
		`0=1`,              // empty authorized set: constant false, never IN ()
		`"name" LIKE ?`,    // LIKE, not ILIKE
	} {
		if !strings.Contains(q.query, want) {
			t.Errorf("rendered SQL missing %q:\n%s", want, q.query)
		}
	}
	// time.Time arg is encoded as canonical RFC3339Nano text.
	var sawTimeText bool
	for _, a := range q.args {
		if s, ok := a.(string); ok && strings.HasPrefix(s, "2026-01-01T00:00:00") {
			sawTimeText = true
		}
	}
	if !sawTimeText {
		t.Errorf("expected TEXT-encoded timestamp arg, got %v", q.args)
	}
}
