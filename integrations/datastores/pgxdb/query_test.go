package pgxdb

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"

	jackpgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// collectRow is a minimal db-tagged row struct standing in for an aggregate's
// child rows.
type collectRow struct {
	ID   string `db:"id"`
	Name string `db:"name"`
}

// collectQuerier is a hermetic Querier stub over an in-memory result set: it
// records the query/args it was handed and replays columns/values as a
// pgx.Rows, so Collect's scanning, empty-result, and error mapping can be
// asserted without a live Postgres.
type collectQuerier struct {
	columns  []string
	values   [][]any
	queryErr error // returned by Query, before any row is read
	rowsErr  error // reported by Rows.Err, after the rows are drained

	gotQuery string
	gotArgs  []any
}

func (c *collectQuerier) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("unexpected Exec")
}

func (c *collectQuerier) Query(_ context.Context, query string, args ...any) (jackpgx.Rows, error) {
	c.gotQuery = query
	c.gotArgs = args
	if c.queryErr != nil {
		return nil, c.queryErr
	}
	return &fakeRows{columns: c.columns, values: c.values, err: c.rowsErr}, nil
}

func (c *collectQuerier) QueryRow(context.Context, string, ...any) jackpgx.Row { return nil }

// fakeRows replays a fixed result set through the pgx.Rows interface. Scan
// assigns each column value to its destination by reflection, which is enough
// for the plain Go field types a row struct carries.
type fakeRows struct {
	columns []string
	values  [][]any
	err     error
	idx     int
	closed  bool
}

func (r *fakeRows) Close()                        { r.closed = true }
func (r *fakeRows) Err() error                    { return r.err }
func (r *fakeRows) CommandTag() pgconn.CommandTag { return pgconn.CommandTag{} }

func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription {
	fds := make([]pgconn.FieldDescription, len(r.columns))
	for i, c := range r.columns {
		fds[i] = pgconn.FieldDescription{Name: c}
	}
	return fds
}

func (r *fakeRows) Next() bool {
	if r.err != nil || r.idx >= len(r.values) {
		return false
	}
	r.idx++
	return true
}

func (r *fakeRows) Scan(dest ...any) error {
	row := r.values[r.idx-1]
	if len(dest) != len(row) {
		return errors.New("scan: destination count mismatch")
	}
	for i, v := range row {
		reflect.ValueOf(dest[i]).Elem().Set(reflect.ValueOf(v))
	}
	return nil
}

func (r *fakeRows) Values() ([]any, error) { return r.values[r.idx-1], nil }
func (r *fakeRows) RawValues() [][]byte    { return nil }
func (r *fakeRows) Conn() *jackpgx.Conn    { return nil }

// TestCollect covers the helper's whole contract hermetically: rows scan into
// db-tagged structs with the query and args passed through verbatim, no rows is
// an empty NON-NIL slice, the strict (never Lax) scanner rejects a row missing
// a struct field, and both the query error and the post-scan rows error travel
// through MapError.
func TestCollect(t *testing.T) {
	ctx := context.Background()

	t.Run("scans_rows_and_passes_through", func(t *testing.T) {
		cq := &collectQuerier{
			columns: []string{"id", "name"},
			values:  [][]any{{"c1", "alpha"}, {"c2", "bravo"}},
		}
		got, err := Collect[collectRow](ctx, cq, "SELECT id, name FROM child WHERE parent_id = @parent",
			jackpgx.NamedArgs{"parent": "p1"})
		if err != nil {
			t.Fatalf("Collect: %v", err)
		}
		want := []collectRow{{ID: "c1", Name: "alpha"}, {ID: "c2", Name: "bravo"}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("rows = %#v, want %#v", got, want)
		}
		if cq.gotQuery != "SELECT id, name FROM child WHERE parent_id = @parent" {
			t.Fatalf("query = %q", cq.gotQuery)
		}
		if len(cq.gotArgs) != 1 {
			t.Fatalf("args = %#v, want one NamedArgs", cq.gotArgs)
		}
		named, ok := cq.gotArgs[0].(jackpgx.NamedArgs)
		if !ok || named["parent"] != "p1" {
			t.Fatalf("args[0] = %#v, want NamedArgs{parent:p1}", cq.gotArgs[0])
		}
	})

	t.Run("no_rows_is_empty_not_nil", func(t *testing.T) {
		cq := &collectQuerier{columns: []string{"id", "name"}}
		got, err := Collect[collectRow](ctx, cq, "SELECT id, name FROM child WHERE parent_id = @parent",
			jackpgx.NamedArgs{"parent": "absent"})
		if err != nil {
			t.Fatalf("Collect: %v", err)
		}
		if got == nil {
			t.Fatal("no rows returned a nil slice, want an empty non-nil []T")
		}
		if len(got) != 0 {
			t.Fatalf("rows = %#v, want empty", got)
		}
	})

	t.Run("strict_scan_rejects_missing_field", func(t *testing.T) {
		cq := &collectQuerier{columns: []string{"id"}, values: [][]any{{"c1"}}}
		if _, err := Collect[collectRow](ctx, cq, "SELECT id FROM child", nil); err == nil {
			t.Fatal("a row missing the name column scanned anyway — the scanner is not strict")
		}
	})

	t.Run("query_error_mapped", func(t *testing.T) {
		cq := &collectQuerier{queryErr: &pgconn.PgError{Code: "23505"}}
		got, err := Collect[collectRow](ctx, cq, "SELECT id, name FROM child", nil)
		if !errors.Is(err, sdk.ErrAlreadyExists) {
			t.Fatalf("err = %v, want ErrAlreadyExists through MapError", err)
		}
		if got != nil {
			t.Fatalf("rows = %#v, want nil on error", got)
		}
	})

	t.Run("collect_error_mapped", func(t *testing.T) {
		cq := &collectQuerier{columns: []string{"id", "name"}, rowsErr: jackpgx.ErrNoRows}
		if _, err := Collect[collectRow](ctx, cq, "SELECT id, name FROM child", nil); !errors.Is(err, sdk.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound through MapError", err)
		}
	})
}
