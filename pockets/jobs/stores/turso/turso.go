// Package turso is the jobs pocket's Turso/libSQL store adapter — its own module
// so a host that brings a different datastore never pulls libsql into its module
// graph (the load-bearing opt-out property). It owns the SQL; the HOST owns its database lifecycle.
//
// Migrations follow the scaffold model (matching the auth and cms turso
// store modules): the canonical *.sql live here, but the recommended
// path is to ExportMigrations into the host's own migrations dir and let the
// host's runner apply them pre-boot through one app-owned ledger. The framework
// never applies migrations behind the host's back.
//
// The two stores implement the pocket's ports over the connector's DB/MapError:
// Queue's Claim is one UPDATE ... WHERE job_id=(SELECT ... LIMIT 1) ... RETURNING
// statement (SQLite's single-writer model makes double-claim impossible; the
// lease-expiry reclaim arm is folded in). Schedules atomically admit pending
// occurrences. Bounded retry-on-busy handles transient write contention.
package turso

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/jobs"
	"github.com/gopernicus/gopernicus/sdk"
)

// probeTables is the inventory of tables this package's stores read and write —
// the set StatusCheck probes, the same set the pgx sibling probes.
var probeTables = []string{"job_queue", "job_schedules", "fenced_job_queue", "job_schedule_occurrences"}

// terminalPurgeIndex is the ADMITTED, not required, retention index migration
// terminalPurgeMigration creates: StatusCheck WARNs when it is absent and never
// fails for it, because Queue.PurgeTerminal still runs without it.
const (
	terminalPurgeIndex     = "idx_job_queue_terminal"
	terminalPurgeMigration = "0005_job_queue_terminal_purge.sql"
)

// Repositories returns the jobs repository set backed by db, WITHOUT touching
// migrations. This is the store half of the scaffold model: the host owns and
// applies the schema (see ExportMigrations) and the store just provides repos.
// It fills all three ports — Queue, FencedQueue and Schedules — so jobs.New
// boots with the fenced queue; a host that wants only the ordinary queue builds
// NewQueueStore itself. opts configure the queue store (WithLease); the fenced
// store's lease is per-claim. db is the connector wrapper (error mapping + Tx),
// not a raw *sql.DB. Call StatusCheck at boot so a ledger missing a jobs table
// fails there rather than at first enqueue.
// It panics if db is nil; the caller owns the database lifecycle.
func Repositories(db *tursodb.DB, opts ...QueueOption) jobs.Repositories {
	return jobs.Repositories{
		Queue:       NewQueueStore(db, opts...),
		FencedQueue: NewFencedQueueStore(db),
		Schedules:   NewScheduleStore(db),
	}
}

// StatusCheck verifies every jobs table exists — the boot gate, since
// Repositories itself is probe-less and error-less. It errors with
// sdk.ErrNotFound (reachable through errors.Is) naming the table when the "jobs"
// migration source was not applied; a query failure comes back through MapError
// and is never misreported as a missing table. A missing idx_job_queue_terminal
// (migration 0005) is admitted: StatusCheck logs a WARN through slog.Default
// naming the index and the file, and returns nil.
func StatusCheck(ctx context.Context, db *tursodb.DB) error {
	if db == nil {
		return fmt.Errorf("jobs turso: nil database: %w", sdk.ErrInvalidInput)
	}
	for _, table := range probeTables {
		found, err := schemaObjectExists(ctx, db, "table", table)
		if err != nil {
			return fmt.Errorf("jobs store: probe %s: %w", table, err)
		}
		if !found {
			return fmt.Errorf("jobs store: %s table missing — apply the %q migration source before boot: %w",
				table, "jobs", sdk.ErrNotFound)
		}
	}
	found, err := schemaObjectExists(ctx, db, "index", terminalPurgeIndex)
	if err != nil {
		return fmt.Errorf("jobs store: probe %s: %w", terminalPurgeIndex, err)
	}
	if !found {
		slog.Default().WarnContext(ctx, "jobs store: retention index missing — PurgeTerminalJobs sorts every terminal job_queue row per batch; apply the migration",
			"index_missing", terminalPurgeIndex, "file", terminalPurgeMigration, "relation", terminalPurgeIndex)
	}
	return nil
}

// schemaObjectExists reports whether sqlite_schema holds an object of kind
// ("table" or "index") named name. The name is bound, never concatenated.
func schemaObjectExists(ctx context.Context, db *tursodb.DB, kind, name string) (bool, error) {
	var got string
	err := db.QueryRow(ctx, `SELECT name FROM sqlite_schema WHERE type = ? AND name = ?`, kind, name).Scan(&got)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, tursodb.MapError(err)
	}
	return true, nil
}

// ExportMigrations copies this store's canonical migration files into dst,
// creating dst if needed. It is the scaffold step: after export the files are the
// HOST's, applied by the host's own runner and extended with the host's own
// migrations in the same directory, under one app-owned schema_migrations ledger.
// The framework never reads or applies the host's copies.
func ExportMigrations(dst string) error {
	return tursodb.ExportMigrations(MigrationsFS, MigrationsDir, dst)
}
