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
	"context"
	"database/sql"
	"errors"
	"fmt"

	auth "github.com/gopernicus/gopernicus/features/authentication"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk"
)

// probeTables are the 13 canonical tables (migrations 0001–0013, in file order)
// the constructor verifies exist before returning repos. Thirteen migration
// files define thirteen tables even though the bundle exposes 15 repository
// ports (PasswordResets/CredentialMutations reuse existing tables). The boot
// probe walks this list so a missing migration surfaces at wiring time, naming
// the specific table, rather than on the first query.
var probeTables = []string{
	"users",
	"user_passwords",
	"sessions",
	"oauth_accounts",
	"oauth_states",
	"service_accounts",
	"api_keys",
	"security_events",
	"invitations",
	"user_identifiers",
	"challenges",
	"contact_changes",
	"authentication_grants",
}

// Repositories returns the auth repository set backed by db, WITHOUT touching
// migrations — AFTER verifying every canonical table (see probeTables) exists
// (the boot-time probe). It errors with sdk.ErrNotFound naming the specific
// missing table when the "authentication" migration source was not applied
// before boot, so the failure surfaces at wiring time rather than on the first
// query. This is the store half of the scaffold model: the host owns and
// applies the schema (see ExportMigrations) and the store just provides repos.
// db is the connector wrapper (error mapping + Tx), not a raw *sql.DB.
func Repositories(db *tursodb.DB) (auth.Repositories, error) {
	ctx := context.Background()
	for _, table := range probeTables {
		if err := probeTable(ctx, db, table); err != nil {
			return auth.Repositories{}, err
		}
	}
	return auth.Repositories{
		Users:                NewUserStore(db),
		Identifiers:          NewIdentifierStore(db),
		Passwords:            NewPasswordStore(db),
		Sessions:             NewSessionStore(db),
		OAuthAccounts:        NewOAuthAccountStore(db),
		OAuthStates:          NewOAuthStateStore(db),
		ServiceAccounts:      NewServiceAccountStore(db),
		APIKeys:              NewAPIKeyStore(db),
		SecurityEvents:       NewSecurityEventStore(db),
		Invitations:          NewInvitationStore(db),
		Challenges:           NewChallengeStore(db),
		PasswordResets:       NewPasswordResetStore(db),
		ContactChanges:       NewContactChangeStore(db),
		AuthenticationGrants: NewAuthGrantStore(db),
		CredentialMutations:  NewCredentialMutationStore(db),
	}, nil
}

// probeTable reports whether table exists, mapping its absence to a clear,
// stable error naming the table and the unapplied "authentication" migration
// source. An infrastructure/query failure is returned via MapError and is never
// misreported as a missing table.
func probeTable(ctx context.Context, db *tursodb.DB, table string) error {
	var name string
	err := db.QueryRow(ctx,
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("authentication turso store: %s table missing — apply the %q migration source before boot: %w", table, "authentication", sdk.ErrNotFound)
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
