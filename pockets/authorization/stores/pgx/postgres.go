// Package pgx stores authorization facts in one canonical SQL tuple authority.
// Hosts own connection lifecycle and apply exported migrations before construction.
// Raw tuples, exact roles, graph reads, atomic mutations, audit and TupleCache
// share these facts; the core never imports this datastore-specific module.
package pgx

import (
	"context"
	"embed"
	"fmt"
	"slices"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authorization"
	mutation "github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/sdk"
)

// MigrationsFS holds the embedded canonical schema (migration source
// "authorization"). A host scaffolds it via ExportMigrations and applies it with
// its own runner.
//
//go:embed migrations/*.sql
var MigrationsFS embed.FS

// MigrationsDir is the directory within MigrationsFS holding the .sql files.
const MigrationsDir = "migrations"

// migrationSource is the ledger source the canonical files are recorded under —
// the name the boot probe points a misconfigured host at.
const migrationSource = "authorization"

// storeTables is the pocket's table inventory, probed at construction in this
// order. Every statement in the package renders these names through a store's
// table method so a schema-scoped store qualifies them.
var storeTables = []string{"iam_tuples", "iam_audit"}

// Option configures the store set at construction.
type Option func(*config)

type config struct {
	tupleCache   bool
	tupleBinding string
	audit        bool
	integrity    mutation.IntegrityPolicy
	schema       pgxdb.Schema
}

// WithAudit enables atomic recording of actual authorization fact changes.
// Every write then requires an explicit valid audit source on its context.
func WithAudit() Option { return func(c *config) { c.audit = true } }

// WithIntegrityPolicy installs the host's relationship invariants. The option
// snapshots its input; the store defaults to an empty policy. NewService checks
// the repository's policy against the host relationship model.
func WithIntegrityPolicy(p mutation.IntegrityPolicy) Option {
	p.Rules = slices.Clone(p.Rules)
	return func(c *config) { c.integrity = mutation.IntegrityPolicy{Rules: slices.Clone(p.Rules)} }
}

// WithSchema places every table this store touches in s. The zero Schema is the
// default (unqualified), which renders exactly the SQL this store has always
// emitted. Build s with pgxdb.NewSchema at the host so a malformed name fails
// there, before any store is constructed — WithSchema itself never panics. Apply
// the migrations into the same schema with pgxdb.WithSchema; a store constructed
// for a schema its migrations never reached fails the boot-time probe. Per-kind
// schemas are out of scope: one schema holds the whole iam_* set.
func WithSchema(s pgxdb.Schema) Option {
	return func(c *config) { c.schema = s }
}

// Repositories probes the canonical tuple and audit tables and returns all ports.
// WithTupleCache additionally validates optional protocol 2 capture and exposes the
// matching source. No constructor applies migrations or starts a worker.
func Repositories(ctx context.Context, db *pgxdb.DB, opts ...Option) (authorization.Repositories, error) {
	if db == nil {
		return authorization.Repositories{}, fmt.Errorf("authorization pgx: nil database: %w", sdk.ErrInvalidInput)
	}
	cfg := config{}
	for _, o := range opts {
		if o == nil {
			return authorization.Repositories{}, fmt.Errorf("authorization store: nil option: %w", sdk.ErrInvalidInput)
		}
		o(&cfg)
	}
	if err := cfg.integrity.Validate(); err != nil {
		return authorization.Repositories{}, err
	}
	var source *tupleSource
	if cfg.tupleCache {
		var err error
		source, err = prepareTupleSource(ctx, db, &cfg)
		if err != nil {
			return authorization.Repositories{}, err
		}
	}
	for _, table := range storeTables {
		if err := probe(ctx, db, cfg.schema.Table(table)); err != nil {
			return authorization.Repositories{}, err
		}
	}
	if err := probeCanonicalSchema(ctx, db, cfg.schema, true); err != nil {
		return authorization.Repositories{}, err
	}
	repos := authorization.Repositories{
		Tuples:    newTupleStore(db, cfg),
		Mutations: newMutationStore(db, cfg),
		Audit:     &auditStore{db: db, schema: cfg.schema},
	}
	if source != nil {
		repos.TupleSource = source
	}
	return repos, nil
}

// RelationshipRepository constructs the relationship facade over canonical facts.
// It probes iam_tuples and, when recording is enabled, iam_audit. WithTupleCache
// requires the complete Repositories bundle and is rejected here.
func RelationshipRepository(ctx context.Context, db *pgxdb.DB, opts ...Option) (relationships.Storer, error) {
	if db == nil {
		return nil, fmt.Errorf("authorization pgx: nil database: %w", sdk.ErrInvalidInput)
	}
	var cfg config
	for _, o := range opts {
		if o == nil {
			return nil, fmt.Errorf("authorization store: nil option: %w", sdk.ErrInvalidInput)
		}
		o(&cfg)
	}
	if err := cfg.integrity.Validate(); err != nil {
		return nil, err
	}
	if cfg.tupleCache {
		return nil, fmt.Errorf("authorization: WithTupleCache requires Repositories bundle: %w", sdk.ErrInvalidInput)
	}
	if err := probe(ctx, db, cfg.schema.Table("iam_tuples")); err != nil {
		return nil, err
	}
	if cfg.audit {
		if err := probe(ctx, db, cfg.schema.Table("iam_audit")); err != nil {
			return nil, err
		}
	}
	if err := probeCanonicalSchema(ctx, db, cfg.schema, cfg.audit); err != nil {
		return nil, err
	}
	return newRelationshipStore(db, cfg), nil
}

// probe reports whether the (already schema-qualified) table exists, wrapping its
// absence in a stable error naming the table and the unapplied "authorization"
// migration source. sdk.ErrNotFound stays reachable through errors.Is.
func probe(ctx context.Context, db *pgxdb.DB, table string) error {
	if err := pgxdb.ProbeTable(ctx, db, table); err != nil {
		return fmt.Errorf("authorization pgx store: %s table missing — apply the %q migration source before boot: %w", table, migrationSource, err)
	}
	return nil
}

// ExportMigrations copies this store's canonical migration files into dst,
// creating dst if needed. It is the scaffold step: after export the files are the
// HOST's, applied by the host's own runner and extended with the host's own
// migrations in the same directory, under one app-owned schema_migrations ledger.
// The framework never reads or applies the host's copies.
func ExportMigrations(dst string) error {
	return pgxdb.ExportMigrations(MigrationsFS, MigrationsDir, dst)
}
