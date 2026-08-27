// Package pgx is the authorization feature's PostgreSQL store adapter — its own
// module so a host that brings a different datastore never pulls pgx into its
// module graph (the load-bearing opt-out property). It owns the SQL; the HOST
// owns its database lifecycle. It is the dialect sibling of
// features/authorization/stores/turso: same surface plus the Postgres-only
// WithSchema option — SQLite has no schemas — same migration version set
// (identical filenames), same port semantics — a host switches dialect by one
// import + one Open call.
//
// The adapter fills BOTH kinds' ports — relationship.Storer (over
// iam_relationships) and role.Storer (over iam_roles) — and Repositories always
// returns both kinds wired. Kind selection is the HOST's wiring choice: a host
// wanting a single kind zeroes the other field after construction (or wires its
// own single-kind authorization.Repositories). The schema is NOT per-kind: both
// iam_* tables scaffold wholesale into every adopting host regardless of which
// kinds it wires (the §2.1 bounding rule applied intra-feature).
//
// Group expansion (CheckRelationWithGroupExpansion) and descendant lookup
// (LookupDescendantResourceIDs) are recursive CTEs, cycle-safe by construction
// via UNION dedup and UNBOUNDED (no depth term — the engine's MaxThroughDepth is an
// engine-only bound and never reaches the store), mirroring the memstore's Go
// graph walk and the turso sibling. CountByResourceAndRelation counts DIRECT
// tuples only (the security pin: it feeds last-owner protection).
//
// Migrations follow the scaffold model (matching the auth, cms, events, and jobs
// pgx store modules): the canonical *.sql live here under migration source
// "authorization", but the recommended path is to ExportMigrations into the
// host's own migrations dir and let the host's runner apply them pre-boot through
// one app-owned ledger. The framework never applies migrations behind the host's
// back.
//
// Cross-source ordering hazard: the shared ledger keyed (source, version)
// expresses NO ordering between sources, so a host that scaffolds another
// feature's migrations but not "authorization" would fail at runtime, not boot.
// Mitigation: Repositories probes all four tables at construction and errors —
// naming the specific missing table — before the host serves traffic; the README
// documents the prerequisite (including the roles-only adopter, which still
// applies the FULL "authorization" source, iam_relationships included).
package pgx

import (
	"context"
	"embed"
	"fmt"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/mutation"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/relationship"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
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

// storeTables is the feature's table inventory, probed at construction in this
// order. Every statement in the package renders these names through a store's
// table method so a schema-scoped store qualifies them.
var storeTables = []string{"iam_relationships", "iam_roles", "iam_scopes", "iam_mutations"}

// Option configures the store set at construction.
type Option func(*config)

type config struct {
	guardian mutation.GuardianPolicy
	schema   pgxdb.Schema
}

// WithGuardianPolicy overrides the default guardian invariant (owner protected on
// every resource type, minimum one direct anchor) the atomic mutation repository
// enforces under its scope lock. Supply an empty policy to declare no invariant, or
// a narrower rule set to protect specific resource types. It mirrors the reference
// memstore's WithGuardianPolicy so the guardian contract is wired identically
// across dialects.
func WithGuardianPolicy(p mutation.GuardianPolicy) Option {
	return func(c *config) { c.guardian = p }
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

// Repositories returns the authorization repository set backed by db — ALL THREE
// ports wired (relationship.Storer, role.Storer, and the atomic
// mutation.MutationRepository over the shared iam_* tables) — AFTER verifying the
// iam_relationships, iam_roles, iam_scopes, AND iam_mutations tables exist (the
// boot-time probe). It errors with sdk.ErrNotFound naming the specific missing
// table when the "authorization" migration source was not applied before boot, so
// the failure surfaces at wiring time rather than on the first query. It does NOT
// touch migrations: the host owns and applies the schema (see ExportMigrations). db
// is the connector wrapper (error mapping + Tx), not a raw pool. The mutation
// repository defaults to the ratified guardian policy unless WithGuardianPolicy
// overrides it. WithSchema qualifies both the probes and every statement the
// stores run.
func Repositories(db *pgxdb.DB, opts ...Option) (authorization.Repositories, error) {
	cfg := config{guardian: mutation.DefaultGuardianPolicy()}
	for _, o := range opts {
		o(&cfg)
	}
	ctx := context.Background()
	for _, table := range storeTables {
		if err := probe(ctx, db, cfg.schema.Table(table)); err != nil {
			return authorization.Repositories{}, err
		}
	}
	return authorization.Repositories{
		Relationships: newRelationshipStore(db, cfg),
		Roles:         newRoleStore(db, cfg),
		Mutations:     newMutationStore(db, cfg),
	}, nil
}

// RelationshipRepository returns only the relationship port after probing only
// iam_relationships. It is the direct constructor for a baseline-only host that
// intentionally does not wire the advanced mutation repository. It takes the same
// options as Repositories so a host composing its own repository set gets the same
// WithSchema seam; WithGuardianPolicy is accepted and ignored (no mutation
// repository is built).
func RelationshipRepository(db *pgxdb.DB, opts ...Option) (relationship.Storer, error) {
	var cfg config
	for _, o := range opts {
		o(&cfg)
	}
	if err := probe(context.Background(), db, cfg.schema.Table("iam_relationships")); err != nil {
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
