package pgxdb

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"

	jackpgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// probeQuerier is a hermetic Querier stub for the to_regclass probe: it answers
// from a set of relations it "has", records the probe order, and can fail the
// query outright, so ProbeTables' short-circuit can be asserted without a live
// Postgres.
type probeQuerier struct {
	have    map[string]bool
	scanErr error

	probed []string
}

func (p *probeQuerier) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("unexpected Exec")
}

func (p *probeQuerier) Query(context.Context, string, ...any) (jackpgx.Rows, error) {
	return nil, errors.New("unexpected Query")
}

func (p *probeQuerier) QueryRow(_ context.Context, _ string, args ...any) jackpgx.Row {
	table, _ := args[0].(string)
	p.probed = append(p.probed, table)
	return probeRow{table: table, present: p.have[table], err: p.scanErr}
}

// probeRow replays to_regclass's answer: the relation's name for a present
// relation, SQL NULL (a nil *string) for an absent one.
type probeRow struct {
	table   string
	present bool
	err     error
}

func (r probeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	reg, ok := dest[0].(**string)
	if !ok {
		return errors.New("scan: destination is not **string")
	}
	if r.present {
		name := r.table
		*reg = &name
	}
	return nil
}

// TestProbeTables: every named relation present is a nil error with each probed
// in order; the first absent relation stops the loop and comes back naming it,
// wrapping sdk.ErrNotFound; a query failure travels through MapError and also
// stops the loop; no tables at all is a nil error.
func TestProbeTables(t *testing.T) {
	ctx := context.Background()

	t.Run("all_present", func(t *testing.T) {
		pq := &probeQuerier{have: map[string]bool{"plans": true, "gps.tiles": true, "reports": true}}
		if err := ProbeTables(ctx, pq, "plans", "gps.tiles", "reports"); err != nil {
			t.Fatalf("ProbeTables: %v", err)
		}
		if got := strings.Join(pq.probed, ","); got != "plans,gps.tiles,reports" {
			t.Fatalf("probed = %q, want the tables in order", got)
		}
	})

	t.Run("first_absent_names_it_and_stops", func(t *testing.T) {
		pq := &probeQuerier{have: map[string]bool{"plans": true, "reports": true}}
		err := ProbeTables(ctx, pq, "plans", "gps.tiles", "reports")
		if !errors.Is(err, sdk.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		if !strings.Contains(err.Error(), "gps.tiles") {
			t.Fatalf("err = %v, want the first failing table named", err)
		}
		if got := strings.Join(pq.probed, ","); got != "plans,gps.tiles" {
			t.Fatalf("probed = %q, want probing to stop at the first failure", got)
		}
	})

	t.Run("query_failure_mapped_and_stops", func(t *testing.T) {
		pq := &probeQuerier{have: map[string]bool{"plans": true}, scanErr: &pgconn.PgError{Code: "42501"}}
		err := ProbeTables(ctx, pq, "plans", "reports")
		if errors.Is(err, sdk.ErrNotFound) {
			t.Fatalf("err = %v, a query failure must never be reported as a missing table", err)
		}
		if err == nil {
			t.Fatal("ProbeTables = nil, want the query failure")
		}
		if len(pq.probed) != 1 {
			t.Fatalf("probed = %v, want probing to stop at the first failure", pq.probed)
		}
	})

	t.Run("no_tables", func(t *testing.T) {
		pq := &probeQuerier{}
		if err := ProbeTables(ctx, pq); err != nil {
			t.Fatalf("ProbeTables() with no tables = %v, want nil", err)
		}
	})
}
