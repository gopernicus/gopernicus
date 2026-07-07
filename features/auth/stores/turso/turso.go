// Package turso is the auth feature's Turso/libSQL store adapter — its own module
// so a host that brings a different datastore never pulls libsql into its module
// graph (the load-bearing opt-out property). It owns the SQL; the HOST owns its database lifecycle.
//
// Migrations follow the scaffold model (matching features/cms/stores/turso): the
// canonical *.sql live here, but the recommended path is to ExportMigrations into
// the host's own migrations dir and let the host's runner apply them pre-boot,
// alongside the host's other migrations, through one app-owned ledger. The
// framework never applies migrations behind the host's back.
package turso

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/gopernicus/gopernicus/features/auth"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
)

// Repositories returns the auth repository set backed by db, WITHOUT touching
// migrations. This is the store half of the scaffold model: the host owns and
// applies the schema (see ExportMigrations) and the store just provides repos.
// db is the connector wrapper (error mapping + Tx), not a raw *sql.DB.
func Repositories(db *tursodb.DB) auth.Repositories {
	return auth.Repositories{
		Users:              NewUserStore(db),
		Passwords:          NewPasswordStore(db),
		Sessions:           NewSessionStore(db),
		VerificationCodes:  NewCodeStore(db),
		VerificationTokens: NewTokenStore(db),
	}
}

// ExportMigrations copies this store's canonical migration files into dst,
// creating dst if needed. It is the scaffold step: after export the files are the
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
