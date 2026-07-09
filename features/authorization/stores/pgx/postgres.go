// Package pgx is the authorization feature's PostgreSQL store adapter — its own
// module so a host that brings a different datastore never pulls pgx into its
// module graph (the load-bearing opt-out property). It owns the SQL; the HOST
// owns its database lifecycle. It is the dialect sibling of
// features/authorization/stores/turso: same exported surface, same migration
// version set (identical filenames), same port semantics — a host switches
// dialect by one import + one Open call.
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
// via UNION dedup and UNBOUNDED (no depth term — MaxTraversalDepth is an
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
// Mitigation: Repositories probes BOTH tables at construction and errors —
// naming the specific missing table — before the host serves traffic; the README
// documents the prerequisite (including the roles-only adopter, which still
// applies the FULL "authorization" source, iam_relationships included).
package pgx

import (
	"context"
	"embed"
	"fmt"

	"github.com/gopernicus/gopernicus/features/authorization"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk/errs"
)

// MigrationsFS holds the embedded canonical schema (migration source
// "authorization"). A host scaffolds it via ExportMigrations and applies it with
// its own runner.
//
//go:embed migrations/*.sql
var MigrationsFS embed.FS

// MigrationsDir is the directory within MigrationsFS holding the .sql files.
const MigrationsDir = "migrations"

// Repositories returns the authorization repository set backed by db — BOTH
// kinds wired — AFTER verifying the iam_relationships AND iam_roles tables exist
// (the boot-time probe). It errors with errs.ErrNotFound naming the specific
// missing table when the "authorization" migration source was not applied before
// boot, so the failure surfaces at wiring time rather than on the first query. It
// does NOT touch migrations: the host owns and applies the schema (see
// ExportMigrations). db is the connector wrapper (error mapping + Tx), not a raw
// pool.
func Repositories(db *pgxdb.DB) (authorization.Repositories, error) {
	ctx := context.Background()
	for _, table := range []string{"iam_relationships", "iam_roles"} {
		if err := probeTable(ctx, db, table); err != nil {
			return authorization.Repositories{}, err
		}
	}
	return authorization.Repositories{
		Relationships: newRelationshipStore(db),
		Roles:         newRoleStore(db),
	}, nil
}

// probeTable reports whether table exists, mapping its absence to a clear, stable
// error naming the table and the unapplied "authorization" migration source.
// to_regclass resolves the relation name to its qualified text, or NULL when no
// such table is visible on the search_path.
func probeTable(ctx context.Context, db *pgxdb.DB, table string) error {
	var reg *string
	if err := db.QueryRow(ctx, `SELECT to_regclass($1)::text`, table).Scan(&reg); err != nil {
		return pgxdb.MapError(err)
	}
	if reg == nil {
		return fmt.Errorf("authorization pgx store: %s table missing — apply the %q migration source before boot: %w", table, "authorization", errs.ErrNotFound)
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
