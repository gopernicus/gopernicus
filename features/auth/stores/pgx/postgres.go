// Package pgx is the auth feature's PostgreSQL store adapter — its own
// module so a host that brings a different datastore never pulls pgx into its
// module graph (the load-bearing opt-out property). It owns the SQL; the HOST owns its database lifecycle. It is the
// dialect sibling of features/auth/stores/turso: same migration version set (0001–0005), same port semantics — a host switches
// dialect by one import + one Open call.
//
// Migrations follow the scaffold model (matching features/auth/stores/turso):
// the canonical *.sql live here, but the recommended path is to ExportMigrations
// into the host's own migrations dir and let the host's runner apply them
// pre-boot, alongside the host's other migrations, through one app-owned ledger.
// The framework never applies migrations behind the host's back.
package pgx

import (
	"github.com/gopernicus/gopernicus/features/auth"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
)

// Repositories returns the auth repository set backed by db, WITHOUT touching
// migrations. This is the store half of the scaffold model: the host owns and
// applies the schema (see ExportMigrations) and the store just provides repos.
// db is the connector wrapper (error mapping + Tx), not a raw *pgxpool.Pool.
func Repositories(db *pgxdb.DB) auth.Repositories {
	return auth.Repositories{
		Users:              NewUserStore(db),
		Passwords:          NewPasswordStore(db),
		Sessions:           NewSessionStore(db),
		VerificationCodes:  NewCodeStore(db),
		VerificationTokens: NewTokenStore(db),
		OAuthAccounts:      NewOAuthAccountStore(db),
		OAuthStates:        NewOAuthStateStore(db),
		ServiceAccounts:    NewServiceAccountStore(db),
		APIKeys:            NewAPIKeyStore(db),
		SecurityEvents:     NewSecurityEventStore(db),
		Invitations:        NewInvitationStore(db),
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
