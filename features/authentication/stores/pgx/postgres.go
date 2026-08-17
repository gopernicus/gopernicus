// Package pgx is the auth feature's PostgreSQL store adapter — its own
// module so a host that brings a different datastore never pulls pgx into its
// module graph (the load-bearing opt-out property). It owns the SQL; the HOST owns its database lifecycle. It is the
// dialect sibling of features/authentication/stores/turso: same migration version
// set (0001–0014, thirteen tables backing the 17-port bundle; 0014 adds the
// account-lifecycle columns to users rather than a table, and auth owns no
// delivery table), same port semantics — a host switches dialect by one import +
// one Open call. Secrets are persisted as digests, never plaintext (session
// refresh-token / challenge / api-key / invitation hashes). Repositories probes
// every canonical table at construction and returns an error naming a missing one
// (see Repositories); the contractual keyset id columns carry per-column
// COLLATE "C" for byte-order pagination parity (see the migrations).
//
// Migrations follow the scaffold model (matching features/authentication/stores/turso):
// the canonical *.sql live here, but the recommended path is to ExportMigrations
// into the host's own migrations dir and let the host's runner apply them
// pre-boot, alongside the host's other migrations, through one app-owned ledger.
// The framework never applies migrations behind the host's back.
package pgx

import (
	"context"
	"fmt"

	auth "github.com/gopernicus/gopernicus/features/authentication"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk"
)

// probeTables are the 13 canonical tables (migrations 0001–0014, in file order)
// the constructor verifies exist before returning repos. Fourteen migration
// files define thirteen tables even though the bundle exposes 17 repository
// ports (PasswordResets/CredentialMutations/UserAdmin/ActiveSessions reuse
// existing tables, and 0014 adds columns rather than a table). The boot probe
// walks this list so a missing migration surfaces at wiring time, naming the
// specific table, rather than on the first query.
//
// The 0014 lifecycle COLUMNS are probed separately (probeUserStatusColumns): a
// table-only probe would pass on a host that copied 0001-0013 and skipped the
// new file, and the failure would then surface as a mid-flight query error
// instead of at boot.
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
// db is the connector wrapper (error mapping + Tx), not a raw *pgxpool.Pool.
func Repositories(db *pgxdb.DB) (auth.Repositories, error) {
	ctx := context.Background()
	for _, table := range probeTables {
		if err := probeTable(ctx, db, table); err != nil {
			return auth.Repositories{}, err
		}
	}
	for _, pc := range probeColumns {
		if err := probeColumn(ctx, db, pc.table, pc.column, pc.migration); err != nil {
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
		// The user-administration capability is ALWAYS supplied by this bundled
		// store. It does not by itself mount an admin HTTP surface — that requires
		// the host to wire Config.UserAdminCheck (CHAU-1.1) — so returning it here
		// costs a host nothing and leaves the decision where it belongs.
		UserAdmin:      NewUserAdminStore(db),
		ActiveSessions: NewActiveSessionStore(db),
		// The atomic magic-link redemption is always supplied too. It changes nothing
		// for a host that leaves Config.PasswordlessProvisionOnRedeem off — that flag
		// is what routes redemption through it (CHAU-6.1).
		Passwordless: NewPasswordlessStore(db),
	}, nil
}

// probeColumns are the columns added by an ALTER migration rather than a CREATE
// TABLE. They need their own probe because a table probe passes on a host that
// stopped at an earlier migration: the table exists, the column does not, and the
// failure would otherwise surface mid-request instead of at boot.
var probeColumns = []struct{ table, column, migration string }{
	{"users", "status", "0014_user_status.sql"},
	{"users", "status_changed_at", "0014_user_status.sql"},
	{"challenges", "subject_key", "0015_challenge_subject_keys.sql"},
}

// probeColumn verifies one ALTER-added column exists, naming the migration that
// adds it. An infrastructure failure is returned via MapError and is never
// misreported as a missing column.
func probeColumn(ctx context.Context, db *pgxdb.DB, table, column, migration string) error {
	var exists bool
	const q = `SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_name = $1 AND column_name = $2
	)`
	if err := db.QueryRow(ctx, q, table, column).Scan(&exists); err != nil {
		return pgxdb.MapError(err)
	}
	if !exists {
		return fmt.Errorf("authentication pgx store: %s.%s column missing — apply migration %s from the %q migration source before boot: %w", table, column, migration, "authentication", sdk.ErrNotFound)
	}
	return nil
}

// probeTable reports whether table exists, mapping its absence to a clear,
// stable error naming the table and the unapplied "authentication" migration
// source. to_regclass resolves the relation name to its qualified text, or NULL
// when no such table is visible on the search_path. An infrastructure/query
// failure is returned via MapError and is never misreported as a missing table.
func probeTable(ctx context.Context, db *pgxdb.DB, table string) error {
	var reg *string
	if err := db.QueryRow(ctx, `SELECT to_regclass($1)::text`, table).Scan(&reg); err != nil {
		return pgxdb.MapError(err)
	}
	if reg == nil {
		return fmt.Errorf("authentication pgx store: %s table missing — apply the %q migration source before boot: %w", table, "authentication", sdk.ErrNotFound)
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
