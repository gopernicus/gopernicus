// Package pgx is the jobs feature's PostgreSQL store adapter — its own
// module so a host that brings a different datastore never pulls pgx into its
// module graph (the load-bearing opt-out property). It owns the SQL; the HOST owns its database
// lifecycle. It is the dialect sibling of features/jobs/stores/turso: same
// surface plus the Postgres-only WithSchema option — SQLite has no schemas —
// same migration version set (identical filenames), same port semantics — a host
// switches dialect by one import + one Open call.
//
// Option naming is three-way across the jobs stores and stays that way by
// decision, not oversight: the memstore names it Option, this store names it
// Option with QueueOption kept as an alias, and stores/turso still names it
// QueueOption. Renaming turso's would cost a retag this train exists to avoid.
//
// Migrations follow the scaffold model (matching features/jobs/stores/turso):
// the canonical *.sql live here, but the recommended path is to ExportMigrations
// into the host's own migrations dir and let the host's runner apply them
// pre-boot, alongside the host's other migrations, through one app-owned ledger.
// The framework never applies migrations behind the host's back.
//
// The two stores implement the feature's ports over the connector's DB/InTx/
// MapError: Queue's Claim is one UPDATE ... WHERE job_id=(SELECT ... FOR UPDATE
// SKIP LOCKED) ... RETURNING statement (contention-free concurrent claiming, N
// workers each locking a different row; the lease-expiry reclaim arm is folded
// in), and Schedules' ClaimDue is a pure value compare-and-set — byte-identical
// semantics to the turso store.
package pgx

import (
	"context"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/features/jobs"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
)

// probeTables is the inventory of relations this package's stores read and
// write — the set StatusCheck probes under the configured schema.
var probeTables = []string{"job_queue", "job_schedules", "fenced_job_queue"}

// Option configures the stores this package constructs.
type Option func(*config)

// QueueOption is the pre-schema name for Option, kept as an alias so the
// WithLease call sites that named it keep compiling. Compatibility: the values
// WithLease(d) returns are unchanged, but a caller that constructs, converts,
// invokes, returns, or wraps its OWN QueueOption breaks at compile time — the
// underlying function type is no longer func(*Queue) but func(*config) over this
// package's unexported config.
type QueueOption = Option

type config struct {
	schema pgxdb.Schema
	lease  time.Duration
}

// newConfig applies opts over the package defaults: no schema (unqualified SQL)
// and DefaultLease.
func newConfig(opts []Option) config {
	cfg := config{lease: DefaultLease}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

// WithSchema places every table these stores touch in s. The zero Schema is the
// default (unqualified), which renders exactly the SQL this store has always
// emitted. Build s with pgxdb.NewSchema at the host so a malformed name fails
// there, before any store is constructed — WithSchema itself never panics. Apply
// the migrations into the same schema with pgxdb.WithSchema and call StatusCheck
// at boot: a store constructed for a schema its migrations never reached would
// otherwise read and write the host's own unqualified tables.
func WithSchema(s pgxdb.Schema) Option {
	return func(c *config) { c.schema = s }
}

// StatusCheck verifies every jobs table exists under the schema opts configure —
// the boot gate for WithSchema, since Repositories itself is probe-less and
// error-less. It errors with sdk.ErrNotFound (reachable through errors.Is)
// naming the qualified table when the "jobs" migration source was not applied
// into that schema. A host that configures a schema calls it before serving
// traffic; without a schema it is the same cheap existence check against the
// connection's default namespace.
func StatusCheck(ctx context.Context, db *pgxdb.DB, opts ...Option) error {
	cfg := newConfig(opts)
	for _, name := range probeTables {
		table := cfg.schema.Table(name)
		if err := pgxdb.ProbeTable(ctx, db, table); err != nil {
			return fmt.Errorf("jobs store: %s table missing — apply the %q migration source before boot: %w",
				table, "jobs", err)
		}
	}
	return nil
}

// Repositories returns the jobs repository set backed by db, WITHOUT touching
// migrations. This is the store half of the scaffold model: the host owns and
// applies the schema (see ExportMigrations) and the store just provides repos.
// opts configure both stores (WithLease, WithSchema); db is the connector wrapper
// (error mapping + Tx), not a raw *pgxpool.Pool.
func Repositories(db *pgxdb.DB, opts ...QueueOption) jobs.Repositories {
	return jobs.Repositories{
		Queue:     NewQueueStore(db, opts...),
		Schedules: NewScheduleStore(db, opts...),
	}
}

// ExportMigrations copies this store's canonical migration files into dst,
// creating dst if needed. It is the scaffold step: after export the files are the
// HOST's, applied by the host's own runner and extended with the host's own
// migrations in the same directory, under one app-owned schema_migrations ledger.
// The framework never reads or applies the host's copies.
func ExportMigrations(dst string) error {
	return pgxdb.ExportMigrations(MigrationsFS, MigrationsDir, dst)
}
