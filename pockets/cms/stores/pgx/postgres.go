// Package pgx is the CMS pocket's PostgreSQL store adapter — its own module
// so a host that brings a different datastore never pulls pgx into its module
// graph (the load-bearing opt-out property, plan §2). It owns the SQL; the HOST owns its database lifecycle. It is the
// dialect sibling of pockets/cms/stores/turso: same surface plus the
// Postgres-only WithSchema option — SQLite has no schemas — same migration
// version set (0009–0021, gaps at 0011/0012 reproduced), same port
// semantics — a host switches dialect by one import + one Open call.
//
// Migrations follow the scaffold model (matching gopernicus init's auth flow):
// the canonical *.sql live here, but the recommended path is to ExportMigrations
// into the host's own migrations dir and let the host's runner apply them
// pre-boot, alongside the host's other migrations, through one app-owned ledger.
// The framework never applies migrations behind the host's back.
package pgx

import (
	"context"
	"fmt"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/cms"
)

// The pocket's table names. Every statement in this package renders one of
// these through a store's table method, so a schema-scoped store qualifies it.
const (
	entriesTable     = "entries"
	entryFieldsTable = "entry_fields"
	entryTermsTable  = "entry_terms"
	termsTable       = "terms"
	menusTable       = "menus"
	menuItemsTable   = "menu_items"
	assetsTable      = "assets"
	inquiriesTable   = "inquiries"
)

// migrationSource names this store's migrations in the boot-check message, so a
// host reading a StatusCheck failure knows which stream it forgot to apply.
const migrationSource = "cms"

// probeTables is the inventory StatusCheck verifies — every table this store
// reads or writes.
var probeTables = []string{
	entriesTable,
	entryFieldsTable,
	entryTermsTable,
	termsTable,
	menusTable,
	menuItemsTable,
	assetsTable,
	inquiriesTable,
}

// Option configures the store set at construction.
type Option func(*config)

type config struct {
	schema pgxdb.Schema
}

// WithSchema places every table these stores touch in s. The zero Schema is the
// default (unqualified), which renders exactly the SQL this store has always
// emitted. Build s with pgxdb.NewSchema at the host so a malformed name fails
// there, before any store is constructed — WithSchema itself never panics. Apply
// the migrations into the same schema with pgxdb.WithSchema, and call
// StatusCheck at boot: this store has no constructor probe, and a store pointed
// at a schema its migrations never reached would silently read and write the
// host's own entries/terms/assets/menus.
func WithSchema(s pgxdb.Schema) Option {
	return func(c *config) { c.schema = s }
}

// newConfig folds opts into the package's construction config.
func newConfig(opts []Option) config {
	var cfg config
	for _, o := range opts {
		o(&cfg)
	}
	return cfg
}

// Repositories returns the CMS repository set backed by db, WITHOUT touching
// migrations. This is the store half of the scaffold model: the host owns and
// applies the schema (see ExportMigrations) and the store just provides repos.
// db is the connector wrapper (error mapping + Tx), not a raw *pgxpool.Pool.
// It runs no probe; a host that passes WithSchema should call StatusCheck.
func Repositories(db *pgxdb.DB, opts ...Option) cms.Repositories {
	return cms.Repositories{
		Entries:   NewEntryStore(db, opts...),
		Terms:     NewTermStore(db, opts...),
		Menus:     NewMenuStore(db, opts...),
		Media:     NewAssetStore(db, opts...),
		Inquiries: NewInquiryStore(db, opts...),
	}
}

// StatusCheck verifies every table this store touches exists under the
// configured schema — the boot gate for a host that wires Repositories with
// WithSchema. It errors with sdk.ErrNotFound naming the qualified table when one
// is absent, so a schema mismatch (migrated into one schema, constructed with
// another, or migrated with a schema and constructed without) fails at boot
// instead of quietly using the host's own tables. Repositories itself stays
// probe-less; call this once, pre-boot, with the same options.
func StatusCheck(ctx context.Context, db *pgxdb.DB, opts ...Option) error {
	cfg := newConfig(opts)
	for _, t := range probeTables {
		name := cfg.schema.Table(t)
		if err := pgxdb.ProbeTable(ctx, db, name); err != nil {
			return fmt.Errorf("cms store: %s table missing — apply the %q migrations before boot: %w",
				name, migrationSource, err)
		}
	}
	return nil
}

// ExportMigrations copies this store's canonical migration files into dst,
// creating dst if needed. It is the scaffold step — the analog of gopernicus
// init copying auth's 0001_auth.sql into the app: after export the files are the
// HOST's, applied by the host's own runner and extended with the host's own
// migrations in the same directory, under one app-owned schema_migrations ledger.
// The framework never reads or applies the host's copies.
func ExportMigrations(dst string) error {
	return pgxdb.ExportMigrations(MigrationsFS, MigrationsDir, dst)
}
