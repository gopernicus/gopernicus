// Package pgx is the CMS feature's PostgreSQL store adapter — its own module
// so a host that brings a different datastore never pulls pgx into its module
// graph (the load-bearing opt-out property, plan §2). It owns the SQL; the HOST owns its database lifecycle. It is the
// dialect sibling of features/cms/stores/turso: same migration version set (0009–0021, gaps at 0011/0012 reproduced), same port
// semantics — a host switches dialect by one import + one Open call.
//
// Migrations follow the scaffold model (matching gopernicus init's auth flow):
// the canonical *.sql live here, but the recommended path is to ExportMigrations
// into the host's own migrations dir and let the host's runner apply them
// pre-boot, alongside the host's other migrations, through one app-owned ledger.
// The framework never applies migrations behind the host's back.
package pgx

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/gopernicus/gopernicus/features/cms"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
)

// Repositories returns the CMS repository set backed by db, WITHOUT touching
// migrations. This is the store half of the scaffold model: the host owns and
// applies the schema (see ExportMigrations) and the store just provides repos.
// db is the connector wrapper (error mapping + Tx), not a raw *pgxpool.Pool.
func Repositories(db *pgxdb.DB) cms.Repositories {
	return cms.Repositories{
		Entries:   NewEntryStore(db),
		Terms:     NewTermStore(db),
		Menus:     NewMenuStore(db),
		Media:     NewAssetStore(db),
		Inquiries: NewInquiryStore(db),
	}
}

// ExportMigrations copies this store's canonical migration files into dst,
// creating dst if needed. It is the scaffold step — the analog of gopernicus
// init copying auth's 0001_auth.sql into the app: after export the files are the
// HOST's, applied by the host's own runner and extended with the host's own
// migrations in the same directory, under one app-owned schema_migrations ledger.
// The framework never reads or applies the host's copies.
func ExportMigrations(dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := fs.ReadDir(MigrationsFS, MigrationsDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := fs.ReadFile(MigrationsFS, path.Join(MigrationsDir, e.Name()))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}
