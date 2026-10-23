// Package pgx is the auth pocket's PostgreSQL store adapter — its own
// module so a host that brings a different datastore never pulls pgx into its
// module graph (the load-bearing opt-out property). It owns the SQL; the HOST owns its database lifecycle. It is the
// dialect sibling of pockets/authentication/stores/turso: same surface plus the
// Postgres-only WithSchema option — SQLite has no schemas — same migration version
// set (0001–0022 with 0017 absent, seventeen tables backing the 20-port bundle; 0014 adds the
// account-lifecycle columns to users rather than a table, and auth owns no
// delivery table), same port semantics — a host switches dialect by one import +
// one Open call. Secrets are persisted as digests, never plaintext (session
// refresh-token / challenge / api-key / invitation hashes). Repositories probes
// every canonical table at construction and returns an error naming a missing one
// (see Repositories); the contractual keyset id columns carry per-column
// COLLATE "C" for byte-order pagination parity (see the migrations).
//
// Migrations follow the scaffold model (matching pockets/authentication/stores/turso):
// the canonical *.sql live here, but the recommended path is to ExportMigrations
// into the host's own migrations dir and let the host's runner apply them
// pre-boot, alongside the host's other migrations, through one app-owned ledger.
// The framework never applies migrations behind the host's back.
package pgx

import (
	"context"
	"errors"
	"fmt"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/sdk"
)

// Option configures the store set at construction.
type Option func(*config)

type config struct {
	schema pgxdb.Schema
}

// WithSchema places every table these stores touch in s. The zero Schema is the
// default (unqualified), which renders exactly the SQL this store has always
// emitted. Build s with pgxdb.NewSchema at the host so a malformed name fails
// there, before any store is constructed — WithSchema itself never panics. Apply
// the migrations into the same schema with pgxdb.WithSchema; a store constructed
// for a schema its migrations never reached fails the boot-time probe below.
func WithSchema(s pgxdb.Schema) Option {
	return func(c *config) { c.schema = s }
}

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
var legacyProbeTables = []string{
	usersTable,
	passwordsTable,
	sessionsTable,
	oauthAccountsTable,
	oauthStatesTable,
	serviceAccountsTable,
	apiKeysTable,
	securityEventsTable,
	invitationsTable,
	identifiersTable,
	challengesTable,
	contactChangesTable,
	authGrantsTable,
}

var oauthProbeTables = []string{oauthClientsTable, oauthCodesTable, oauthHistoryTable}

// externalProbeTables are the tables migration 0020 creates. They are probed
// even for a host that wires no external provider, so a missed migration fails at
// boot rather than on the first external request.
var externalProbeTables = []string{externalSubjectsTable}
var probeTables = append(append(append([]string{}, legacyProbeTables...), oauthProbeTables...), externalProbeTables...)

// Repositories returns the auth repository set backed by db, WITHOUT touching
// migrations — AFTER verifying every canonical table (see probeTables) exists
// (the boot-time probe). It errors with sdk.ErrNotFound naming the specific
// missing table when the "authentication" migration source was not applied
// before boot, so the failure surfaces at wiring time rather than on the first
// query. This is the store half of the scaffold model: the host owns and
// applies the schema (see ExportMigrations) and the store just provides repos.
// db is the connector wrapper (error mapping + Tx), not a raw *pgxpool.Pool.
// WithSchema qualifies both the probe and every statement the stores run.
//
// Last it probes the runtime role's posture on AuditTables under the
// RuntimeMode db was opened with (pgxdb.DB.ProbeAppendOnly): in production it
// returns pgxdb.ErrAppendOnlyTableWritable for a role that could rewrite
// security_events (unless Config.AppendOnlyWritableAcknowledged) and
// pgxdb.ErrAppendOnlyTableUnwritable for one that cannot append to it.
func Repositories(ctx context.Context, db *pgxdb.DB, opts ...Option) (auth.Repositories, error) {
	if db == nil {
		return auth.Repositories{}, fmt.Errorf("authentication pgx: nil database: %w", sdk.ErrInvalidInput)
	}
	for _, opt := range opts {
		if opt == nil {
			return auth.Repositories{}, fmt.Errorf("authentication pgx: nil option: %w", sdk.ErrInvalidInput)
		}
	}
	s := qualified{schema: applyOptions(opts).schema}
	if err := s.probeTables(ctx, db, legacyProbeTables); err != nil {
		return auth.Repositories{}, err
	}
	for _, pc := range probeColumns {
		if err := s.probeColumn(ctx, db, pc.table, pc.column, pc.migration); err != nil {
			return auth.Repositories{}, err
		}
	}
	if err := s.probeTables(ctx, db, oauthProbeTables); err != nil {
		return auth.Repositories{}, err
	}
	if err := s.probeTables(ctx, db, externalProbeTables); err != nil {
		return auth.Repositories{}, err
	}
	if err := db.ProbeAppendOnly(ctx, AuditTables(opts...)...); err != nil {
		return auth.Repositories{}, err
	}
	oauth2Store := NewOAuth2Store(db, opts...)
	return auth.Repositories{
		OAuth2: oauth2Store, SessionManagement: oauth2Store,
		Users:                NewUserStore(db, opts...),
		Identifiers:          NewIdentifierStore(db, opts...),
		Passwords:            NewPasswordStore(db, opts...),
		Sessions:             NewSessionStore(db, opts...),
		OAuthAccounts:        NewOAuthAccountStore(db, opts...),
		OAuthStates:          NewOAuthStateStore(db, opts...),
		ServiceAccounts:      NewServiceAccountStore(db, opts...),
		APIKeys:              NewAPIKeyStore(db, opts...),
		SecurityEvents:       NewSecurityEventStore(db, opts...),
		Invitations:          NewInvitationStore(db, opts...),
		Challenges:           NewChallengeStore(db, opts...),
		PasswordResets:       NewPasswordResetStore(db, opts...),
		ContactChanges:       NewContactChangeStore(db, opts...),
		AuthenticationGrants: NewAuthGrantStore(db, opts...),
		CredentialMutations:  NewCredentialMutationStore(db, opts...),
		// The user-administration capability is ALWAYS supplied by this bundled
		// store. It does not by itself mount an admin HTTP surface — that requires
		// the host to wire Config.UserAdminCheck (CHAU-1.1) — so returning it here
		// costs a host nothing and leaves the decision where it belongs.
		UserAdmin:      NewUserAdminStore(db, opts...),
		ActiveSessions: NewActiveSessionStore(db, opts...),
		// The atomic magic-link redemption is always supplied too. It changes nothing
		// for a host that leaves Config.PasswordlessProvisionOnRedeem off — that flag
		// is what routes redemption through it (CHAU-6.1).
		Passwordless:     NewPasswordlessStore(db, opts...),
		ExternalSubjects: NewExternalSubjectStore(db, opts...),
	}, nil
}

// probeColumns are the columns added by an ALTER migration rather than a CREATE
// TABLE. They need their own probe because a table probe passes on a host that
// stopped at an earlier migration: the table exists, the column does not, and the
// failure would otherwise surface mid-request instead of at boot.
var probeColumns = []struct{ table, column, migration string }{
	{usersTable, "status", "0014_user_status.sql"},
	{usersTable, "status_changed_at", "0014_user_status.sql"},
	{challengesTable, "subject_key", "0015_challenge_subject_keys.sql"},
	{invitationsTable, "metadata", "0016_invitation_metadata.sql"},
	{invitationsTable, "resolved_subject_type", "0018_invitation_acceptance.sql"},
	{sessionsTable, "session_profile", "0019_oauth2_sessions.sql"},
	{sessionsTable, "delegation", "0019_oauth2_sessions.sql"},
	{securityEventsTable, "request_id", "0021_security_events_integrity.sql"},
}

// securityEventsRefuseWrite is the trigger function migration 0022 creates, and
// securityEventsRefuseWriteBody its body exactly as the catalog's prosrc holds
// it — the text between the migration's dollar quotes. A test pins it to the
// embedded 0022, so the posture probe verifies the trigger the store ships.
const (
	securityEventsRefuseWrite     = "security_events_refuse_write"
	securityEventsRefuseWriteBody = `
BEGIN
  IF pg_catalog.pg_has_role(current_user, (SELECT c.relowner FROM pg_catalog.pg_class c WHERE c.oid = TG_RELID), 'MEMBER') THEN
    IF TG_OP = 'UPDATE' THEN RETURN NEW; END IF;
    RETURN OLD;
  END IF;
  RAISE EXCEPTION 'security_events is append-only' USING ERRCODE = '42501';
END
`
)

// AuditTables are the append-only tables this store owns, as the posture probe
// judges them: security_events, under the schema WithSchema selects, verified
// against the trigger function migration 0022 ships. Repositories probes them
// with pgxdb.DB.ProbeAppendOnly; a migrator preflights the runtime role with
// pgxdb.InspectTablePrivileges over the same list, so both decide identically.
// A nil option panics, as it does in the store constructors.
func AuditTables(opts ...Option) []pgxdb.AppendOnlyTable {
	s := qualified{schema: applyOptions(opts).schema}
	return []pgxdb.AppendOnlyTable{{
		Name:      s.table(securityEventsTable),
		Canonical: []pgxdb.TriggerBody{{Function: securityEventsRefuseWrite, Body: securityEventsRefuseWriteBody}},
	}}
}

// probeColumnSQL is the default column probe. It is deliberately NOT filtered by
// table_schema: that is the query this store has always run, and tightening the
// default would be a behaviour change for hosts whose search_path resolves it.
const probeColumnSQL = `SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_name = $1 AND column_name = $2
	)`

// probeColumnSchemaSQL is the schema-scoped column probe, used only when the
// store set was constructed WithSchema: a same-named table in another schema can
// then never satisfy the probe.
const probeColumnSchemaSQL = `SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_name = $1 AND column_name = $2 AND table_schema = $3
	)`

// probeColumn verifies one ALTER-added column exists, naming the migration that
// adds it. An infrastructure failure is returned via MapError and is never
// misreported as a missing column.
func (s qualified) probeColumn(ctx context.Context, db *pgxdb.DB, table, column, migration string) error {
	q, args := probeColumnSQL, []any{table, column}
	if !s.schema.IsZero() {
		q, args = probeColumnSchemaSQL, []any{table, column, s.schema.String()}
	}
	var exists bool
	if err := db.QueryRow(ctx, q, args...).Scan(&exists); err != nil {
		return pgxdb.MapError(err)
	}
	if !exists {
		return fmt.Errorf("authentication pgx store: %s.%s column missing — apply migration %s from the %q migration source before boot: %w", s.table(table), column, migration, "authentication", sdk.ErrNotFound)
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

func (s qualified) probeTables(ctx context.Context, db *pgxdb.DB, tables []string) error {
	for _, table := range tables {
		name := s.table(table)
		if err := pgxdb.ProbeTable(ctx, db, name); err != nil {
			if errors.Is(err, sdk.ErrNotFound) {
				return fmt.Errorf("authentication pgx store: %s table missing — apply the %q migration source before boot: %w", name, "authentication", err)
			}
			return err
		}
	}
	return nil
}
