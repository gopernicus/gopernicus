// Package pgx is the events feature's PostgreSQL outbox store adapter — its own
// module so a host that brings a different datastore never pulls pgx into its
// module graph (the load-bearing opt-out property). It owns the SQL; the HOST
// owns its database lifecycle. It is the dialect sibling of
// features/events/stores/turso: same surface plus the Postgres-only WithSchema
// option — SQLite has no schemas — same migration version set (identical
// filenames), same port semantics — a host switches dialect by one import + one
// Open call.
//
// Migrations follow the scaffold model (matching the auth, cms, and jobs pgx
// store modules): the canonical *.sql live here under migration source "events",
// but the recommended path is to ExportMigrations into the host's own migrations
// dir and let the host's runner apply them pre-boot through one app-owned ledger.
// The framework never applies migrations behind the host's back.
//
// Cross-source ordering hazard (design §5, risk 2): the shared ledger keyed
// (source, version) expresses NO ordering between sources, so a host that
// scaffolds another feature's migrations but not "events" would fail at runtime,
// not boot. Mitigation (b): New probes the outbox table at construction and
// errors before the host serves traffic; the README documents the prerequisite
// (mitigation a).
package pgx

import (
	"context"
	"embed"
	"fmt"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
)

// MigrationsFS holds the embedded canonical schema (migration source "events").
// A host scaffolds it via ExportMigrations and applies it with its own runner.
//
//go:embed migrations/*.sql
var MigrationsFS embed.FS

// MigrationsDir is the directory within MigrationsFS holding the .sql files.
const MigrationsDir = "migrations"

// Option configures the store at construction.
type Option func(*config)

type config struct {
	schema pgxdb.Schema
}

// WithSchema places every table this store touches in s. The zero Schema is the
// default (unqualified), which renders exactly the SQL this store has always
// emitted. Build s with pgxdb.NewSchema at the host so a malformed name fails
// there, before any store is constructed — WithSchema itself never panics. Apply
// the migrations into the same schema with pgxdb.WithSchema; a store constructed
// for a schema its migrations never reached fails New's boot-time probe.
func WithSchema(s pgxdb.Schema) Option {
	return func(c *config) { c.schema = s }
}

// New returns the outbox Store backed by db, AFTER verifying the event_outbox
// table exists (design §5 mitigation b: the boot-time probe). It errors with
// sdk.ErrNotFound when the table is absent — the "events" migration source was
// not applied before boot — so the failure surfaces at wiring time, before the
// host serves traffic, rather than on the poller's first read. It does NOT touch
// migrations: the host owns and applies the schema (see ExportMigrations).
// WithSchema qualifies both the probe and every statement the store runs.
func New(db *pgxdb.DB, opts ...Option) (*Store, error) {
	var cfg config
	for _, o := range opts {
		o(&cfg)
	}
	s := &Store{db: db, schema: cfg.schema}
	if err := pgxdb.ProbeTable(context.Background(), db, s.table(outboxTable)); err != nil {
		return nil, fmt.Errorf("events outbox store: %s table missing — apply the %q migration source before boot: %w",
			s.table(outboxTable), "events", err)
	}
	return s, nil
}

// ExportMigrations copies this store's canonical migration files into dst,
// creating dst if needed. It is the scaffold step: after export the files are the
// HOST's, applied by the host's own runner and extended with the host's own
// migrations in the same directory, under one app-owned schema_migrations ledger.
// The framework never reads or applies the host's copies.
func ExportMigrations(dst string) error {
	return pgxdb.ExportMigrations(MigrationsFS, MigrationsDir, dst)
}
