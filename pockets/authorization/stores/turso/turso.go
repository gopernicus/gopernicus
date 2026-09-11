// Package turso is the authorization pocket's Turso/libSQL store adapter — its
// own module so a host that brings a different datastore never pulls libsql into
// its module graph (the load-bearing opt-out property). It owns the SQL; the HOST
// owns its database lifecycle.
//
// The adapter fills BOTH kinds' ports — relationship.Storer (over
// iam_relationships) and role.Storer (over iam_roles) — and Repositories always
// returns both kinds wired. Kind selection is the HOST's wiring choice: a host
// wanting a single kind zeroes the other field after construction (or wires its
// own single-kind authorization.Repositories). The schema is NOT per-kind: both
// iam_* tables scaffold wholesale into every adopting host regardless of which
// kinds it wires (the §2.1 bounding rule applied intra-pocket).
//
// Group expansion (CheckRelationWithGroupExpansion) and descendant lookup
// (LookupDescendantResourceIDs) are recursive CTEs, cycle-safe by construction
// via UNION dedup and UNBOUNDED (no depth term — the engine's MaxThroughDepth is an
// engine-only bound and never reaches the store), mirroring the memstore's Go
// graph walk. CountByResourceAndRelation counts DIRECT tuples only (the security
// pin: it feeds last-owner protection).
//
// Migrations follow the scaffold model (matching the auth, cms, events, and jobs
// turso store modules): the canonical *.sql live here under migration source
// "authorization", but the recommended path is to ExportMigrations into the
// host's own migrations dir and let the host's runner apply them pre-boot through
// one app-owned ledger. The framework never applies migrations behind the host's
// back.
//
// Cross-source ordering hazard: the shared ledger keyed (source, version)
// expresses NO ordering between sources, so a host that scaffolds another
// pocket's migrations but not "authorization" would fail at runtime, not boot.
// Mitigation: Repositories probes all three tables at construction and errors —
// naming the specific missing table — before the host serves traffic; the README
// documents the prerequisite (including the roles-only adopter, which still
// applies the FULL "authorization" source, iam_relationships included).
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
	audit    bool
	guardian mutation.GuardianPolicy
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

// Repositories returns the authorization repository set backed by db — all fact, mutation and audit
// ports wired (relationship.Storer, role.Storer, and the atomic
// mutation.MutationRepository over the shared iam_* tables) — AFTER verifying the
// iam_relationships, iam_roles and iam_audit tables exist (the
// boot-time probe). It errors with sdk.ErrNotFound naming the specific missing
// table when the "authorization" migration source was not applied before boot, so
// the failure surfaces at wiring time rather than on the first query. It does NOT
// touch migrations: the host owns and applies the schema (see ExportMigrations). db
// is the connector wrapper (error mapping + Tx), not a raw *sql.DB. The mutation
// repository has no guardian rules unless WithGuardianPolicy supplies them.
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
	for _, table := range []string{"iam_relationships", "iam_roles", "iam_audit"} {
		if err := probeTable(ctx, db, table); err != nil {
			return authorization.Repositories{}, err
		}
	}
	return authorization.Repositories{
		Relationships: newRelationshipStore(db, cfg),
		Roles:         newRoleStore(db, cfg),
		Mutations:     newMutationStore(db, cfg),
		Audit:         &auditStore{db: db},
	}, nil
}

// RelationshipRepository returns only the relationship port after probing only
// iam_relationships. It is the direct constructor for a baseline-only host that
// intentionally does not wire the advanced mutation repository.
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
	if err := probeTable(ctx, db, "iam_relationships"); err != nil {
		return nil, err
	}
	if cfg.audit {
		if err := probeTable(ctx, db, "iam_audit"); err != nil {
			return nil, err
		}
	}
	return newRelationshipStore(db, cfg), nil
}

// probeTable reports whether table exists, mapping its absence to a clear, stable
// error naming the table and the unapplied "authorization" migration source.
func probeTable(ctx context.Context, db *tursodb.DB, table string) error {
	var name string
	err := db.QueryRow(ctx,
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
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
