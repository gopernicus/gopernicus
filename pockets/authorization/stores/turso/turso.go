// Package turso stores authorization facts in one canonical SQL tuple authority.
// Hosts own connection lifecycle and apply exported migrations before construction.
// Raw tuples, exact roles, graph reads, atomic mutations, audit and TupleCache
// share these facts; the core never imports this datastore-specific module.
package turso

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"slices"
	"strings"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
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

// Option configures the store set at construction.
type Option func(*config)

type config struct {
	tupleCache   bool
	tupleBinding string
	audit        bool
	guardian     mutation.GuardianPolicy
}

// WithAudit enables atomic recording of actual authorization fact changes.
// Every write then requires an explicit valid audit source on its context.
func WithAudit() Option { return func(c *config) { c.audit = true } }

// WithGuardianPolicy installs the host's relationship invariants. The option
// snapshots its input; the store defaults to an empty policy. NewService checks
// the repository's policy against the host relationship model.
func WithGuardianPolicy(p mutation.GuardianPolicy) Option {
	p.Rules = slices.Clone(p.Rules)
	return func(c *config) { c.guardian = mutation.GuardianPolicy{Rules: slices.Clone(p.Rules)} }
}

// Repositories probes the canonical tuple and audit tables and returns all ports.
// WithTupleCache additionally validates optional protocol 2 capture and exposes the
// matching source. No constructor applies migrations or starts a worker.
func Repositories(ctx context.Context, db *tursodb.DB, opts ...Option) (authorization.Repositories, error) {
	if db == nil {
		return authorization.Repositories{}, fmt.Errorf("authorization turso: nil database: %w", sdk.ErrInvalidInput)
	}
	cfg := config{}
	for _, o := range opts {
		if o == nil {
			return authorization.Repositories{}, fmt.Errorf("authorization store: nil option: %w", sdk.ErrInvalidInput)
		}
		o(&cfg)
	}
	var source *tupleSource
	if cfg.tupleCache {
		var err error
		source, err = prepareTupleSource(ctx, db, &cfg)
		if err != nil {
			return authorization.Repositories{}, err
		}
	}
	for _, table := range []string{"iam_tuples", "iam_audit"} {
		if err := probeTable(ctx, db, table); err != nil {
			return authorization.Repositories{}, err
		}
	}
	if err := probeCanonicalSchema(ctx, db, true); err != nil {
		return authorization.Repositories{}, err
	}
	repos := authorization.Repositories{
		Tuples:    newTupleStore(db, cfg),
		Mutations: newMutationStore(db, cfg),
		Audit:     &auditStore{db: db},
	}
	if source != nil {
		repos.TupleSource = source
	}
	return repos, nil
}

// RelationshipRepository constructs the relationship facade over canonical facts.
// It probes iam_tuples and, when recording is enabled, iam_audit. WithTupleCache
// requires the complete Repositories bundle and is rejected here.
func RelationshipRepository(ctx context.Context, db *tursodb.DB, opts ...Option) (relationships.Storer, error) {
	if db == nil {
		return nil, fmt.Errorf("authorization turso: nil database: %w", sdk.ErrInvalidInput)
	}
	var cfg config
	for _, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("authorization store: nil option: %w", sdk.ErrInvalidInput)
		}
		opt(&cfg)
	}
	if cfg.tupleCache {
		return nil, fmt.Errorf("authorization: WithTupleCache requires Repositories bundle: %w", sdk.ErrInvalidInput)
	}
	if err := probeTable(ctx, db, "iam_tuples"); err != nil {
		return nil, err
	}
	if cfg.audit {
		if err := probeTable(ctx, db, "iam_audit"); err != nil {
			return nil, err
		}
	}
	if err := probeCanonicalSchema(ctx, db, cfg.audit); err != nil {
		return nil, err
	}
	return newRelationshipStore(db, cfg), nil
}

// probeTable reports whether table exists, mapping its absence to a clear, stable
// error naming the table and the unapplied "authorization" migration source.
func probeTable(ctx context.Context, db *tursodb.DB, table string) error {
	var name string
	err := db.QueryRow(ctx,
		`SELECT name FROM main.sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("authorization turso store: %s table missing — apply the %q migration source before boot: %w", table, "authorization", sdk.ErrNotFound)
	}
	if err != nil {
		return tursodb.MapError(err)
	}
	return nil
}

// ExportMigrations copies this store's canonical migration files into dst,
// creating dst if needed. It is the scaffold step: after export the files are the
// HOST's, applied by the host's own runner and extended with the host's own
// migrations in the same directory, under one app-owned schema_migrations ledger.
// The framework never reads or applies the host's copies.
func ExportMigrations(dst string) error {
	return tursodb.ExportMigrations(MigrationsFS, MigrationsDir, dst)
}

// inClause renders a positional `IN (?, ?, …)` list for n placeholders. Callers
// guard n > 0 (an empty IN is never emitted).
func inClause(n int) string {
	return "(" + strings.Repeat("?, ", n-1) + "?)"
}

// queryStrings runs a single-column string SELECT on db (the pool or the
// ambient transaction — callers pass s.db.QuerierFrom(ctx)) and collects the rows.
func queryStrings(ctx context.Context, db tursodb.Querier, query string, args ...any) ([]string, error) {
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, tursodb.MapError(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, tursodb.MapError(err)
	}
	return out, nil
}

// existsQuery scans a `SELECT EXISTS(...)` (always exactly one 0/1 row) on db
// (the pool or the ambient transaction — callers pass s.db.QuerierFrom(ctx)) to
// a bool.
func existsQuery(ctx context.Context, db tursodb.Querier, query string, args ...any) (bool, error) {
	var n int
	if err := db.QueryRow(ctx, query, args...).Scan(&n); err != nil {
		return false, tursodb.MapError(err)
	}
	return n != 0, nil
}
