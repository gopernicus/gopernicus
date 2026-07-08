package pgxdb

import (
	"context"
	"errors"
	"testing"

	jackpgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// execQuerier is a hermetic Querier stub: it records the query/args passed
// through and returns a configured CommandTag/error, so ExecAffecting's
// normalization can be asserted without a live Postgres.
type execQuerier struct {
	tag      pgconn.CommandTag
	err      error
	gotQuery string
	gotArgs  []any
}

func (e *execQuerier) Exec(_ context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	e.gotQuery = query
	e.gotArgs = args
	return e.tag, e.err
}
func (e *execQuerier) Query(context.Context, string, ...any) (jackpgx.Rows, error) { return nil, nil }
func (e *execQuerier) QueryRow(context.Context, string, ...any) jackpgx.Row        { return nil }

// TestExecAffecting: the connector reports the tag's rows-affected count and
// passes query/args straight through; a non-matching write is (0, nil) — the
// adapter, not the connector, owns the zero → ErrNotFound mapping — and a driver
// error propagates unchanged.
func TestExecAffecting(t *testing.T) {
	ctx := context.Background()

	t.Run("normalizes_count_and_passes_through", func(t *testing.T) {
		eq := &execQuerier{tag: pgconn.NewCommandTag("UPDATE 3")}
		n, err := ExecAffecting(ctx, eq, "UPDATE t SET v = $1 WHERE id = $2", "z", "a")
		if err != nil {
			t.Fatalf("ExecAffecting: %v", err)
		}
		if n != 3 {
			t.Fatalf("n = %d, want 3", n)
		}
		if eq.gotQuery != "UPDATE t SET v = $1 WHERE id = $2" {
			t.Fatalf("query = %q", eq.gotQuery)
		}
		if len(eq.gotArgs) != 2 || eq.gotArgs[0] != "z" || eq.gotArgs[1] != "a" {
			t.Fatalf("args = %#v, want [z a]", eq.gotArgs)
		}
	})

	t.Run("zero_rows_not_mapped", func(t *testing.T) {
		eq := &execQuerier{tag: pgconn.NewCommandTag("DELETE 0")}
		n, err := ExecAffecting(ctx, eq, "DELETE FROM t WHERE id = $1", "missing")
		if err != nil {
			t.Fatalf("ExecAffecting: %v", err)
		}
		if n != 0 {
			t.Fatalf("n = %d, want 0 (connector does not map zero to ErrNotFound)", n)
		}
	})

	t.Run("exec_error_propagates", func(t *testing.T) {
		sentinel := errors.New("boom")
		eq := &execQuerier{err: sentinel}
		if _, err := ExecAffecting(ctx, eq, "UPDATE t SET v = $1", "z"); !errors.Is(err, sentinel) {
			t.Fatalf("err = %v, want sentinel", err)
		}
	})
}
