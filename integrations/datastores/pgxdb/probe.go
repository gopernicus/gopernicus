package pgxdb

import (
	"context"
	"fmt"

	"github.com/gopernicus/gopernicus/sdk"
)

// ProbeTable reports whether the relation named table exists — the boot-time
// check a store runs in its constructor so a host pointed at a database
// without its tables fails before serving traffic, naming the table, instead
// of 500ing on the first query. The name may be schema-qualified
// ("gps.organizations") or bare (resolved on the connection's search_path);
// it is bound as a parameter, never concatenated.
//
// Existence is resolved by to_regclass, which yields NULL for an absent
// relation rather than raising. An absent table wraps sdk.ErrNotFound; a
// query or infrastructure failure comes back through MapError and is never
// misreported as a missing table. db may be a *DB pool or a *Tx.
func ProbeTable(ctx context.Context, db Querier, table string) error {
	var reg *string
	if err := db.QueryRow(ctx, `SELECT to_regclass($1)::text`, table).Scan(&reg); err != nil {
		return MapError(err)
	}
	if reg == nil {
		return fmt.Errorf("pgxdb: relation %s does not exist: %w", table, sdk.ErrNotFound)
	}
	return nil
}

// ProbeTables is ProbeTable over the relations one store owns — the boot-time
// check a repository spanning several tables runs in its constructor, instead
// of looping ProbeTable and re-wrapping the error itself. Probing stops at the
// FIRST failure and returns it unchanged: an absent relation already wraps
// sdk.ErrNotFound naming it, and a query or infrastructure failure is not about
// any one table, so neither is renamed here. No tables is a nil error. db may
// be a *DB pool or a *Tx.
func ProbeTables(ctx context.Context, db Querier, tables ...string) error {
	for _, table := range tables {
		if err := ProbeTable(ctx, db, table); err != nil {
			return err
		}
	}
	return nil
}
