package pgx

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/maintenance"
)

// canonicalMigrations is the frozen auth-v3 canonical filename set (both dialect
// trees are byte-for-byte identical filename sets — the standing invariant). The
// turso sibling asserts this same slice, so the two trees cannot drift apart.
var canonicalMigrations = []string{
	"0001_users.sql",
	"0002_user_passwords.sql",
	"0003_sessions.sql",
	"0004_oauth_accounts.sql",
	"0005_oauth_states.sql",
	"0006_service_accounts.sql",
	"0007_api_keys.sql",
	"0008_security_events.sql",
	"0009_invitations.sql",
	"0010_user_identifiers.sql",
	"0011_challenges.sql",
	"0012_contact_changes.sql",
	"0013_authentication_grants.sql",
	// CHAU-1.2: append-only account-lifecycle columns on users. It adds no table,
	// so expectedTables is unchanged; expectedColumns/expectedIndexes below carry
	// its assertions.
	"0014_user_status.sql",
	// CHAU-6.1: append-only challenge subject keys. It adds no table; the column
	// and the replacement unique index are asserted below.
	"0015_challenge_subject_keys.sql",
	// invitation-metadata: append-only opaque host metadata column on invitations.
	// It adds no table; the column is asserted below.
	"0016_invitation_metadata.sql",
	"0018_invitation_acceptance.sql",
	"0019_oauth2_sessions.sql",
	"0020_external_subjects.sql",
	// SOC 2 S2: the audit rail's correlation and evidence columns, then its
	// immutability trigger pair. Neither adds a table.
	"0021_security_events_integrity.sql",
	"0022_security_events_immutable.sql",
	// SOC 2 S3: the retention indexes and the users.erased_at marker, then the
	// rail's floor and owner-only purge functions. Neither adds a table.
	"0023_retention.sql",
	"0024_security_events_retention.sql",
	// SOC 2 S4b: the passkey table.
	"0025_passkey_credentials.sql",
	// SOC 2 S4c: the TOTP credential and recovery-code tables.
	"0026_totp.sql",
	// SOC 2 S4d: the service-account status table.
	"0027_service_account_status.sql",
	// SOC 2 S4d part 2: sessions.last_rotated_at. It adds no table; the
	// column is asserted below. 0028 is reserved for S4c part 2.
	"0029_session_activity.sql",
}

// expectedTables are every CREATE TABLE the canonical set must define.
var expectedTables = []string{
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
	"oauth_clients", "oauth_authorization_codes", "oauth_refresh_history",
	"external_subjects",
	"passkey_credentials",
	"totp_credentials", "recovery_codes",
	"service_account_status",
}

// expectedIndexes are the auth-v3 indexes AV3-2.1 lands: the identifier claim /
// primary / active partial indexes, the challenge (user,purpose) and
// (purpose,secret_digest) uniques, the one-active contact-change unique, and the
// grant consume index.
var expectedIndexes = []string{
	"idx_sessions_user_created_at_id", "idx_oauth_refresh_history_session_id",
	"idx_external_subjects_user_id",
	"idx_invitations_kind_identifier",
	"idx_user_identifiers_auth_claim",
	"idx_user_identifiers_primary",
	"idx_user_identifiers_user_active",
	"idx_challenges_user_purpose",
	"idx_challenges_purpose_secret_digest",
	"idx_contact_changes_user_kind",
	"idx_authentication_grants_session_purpose_context",
	// CHAU-1.2: the operator directory's contractual (created_at, id) keyset.
	"idx_users_created_at_id",
	// CHAU-6.1: the challenge single-active claim moves to (subject_key, purpose).
	"idx_challenges_subject_purpose",
	// SOC 2 S3: the purge predicates' access paths (0023).
	"idx_sessions_expires_at", "idx_authentication_grants_expires_at", "idx_contact_changes_expires_at",
	"idx_oauth_states_expires_at", "idx_challenges_expires_at", "idx_oauth_authorization_codes_expires_at",
	"idx_oauth_clients_expires_at", "idx_oauth_refresh_history_expires_at",
	// SOC 2 S4b: the passkey lookups (0025).
	"idx_passkey_credentials_credential_id", "idx_passkey_credentials_user_created_id",
	// SOC 2 S4c: the recovery-code lookups (0026).
	"idx_recovery_codes_key_id",
}

// expectedColumns are the schema additions to existing tables this task lands.
var expectedColumns = []string{
	"session_profile", "delegation",
	"resolved_subject_type",  // invitations: durable acceptance binding
	"auth_revision",          // users
	"authenticated_at",       // sessions
	"authentication_methods", // sessions
	"assurance_level",        // sessions
	"status",                 // users (CHAU-1.2)
	"status_changed_at",      // users (CHAU-1.2)
	"subject_key",            // challenges (CHAU-6.1)
	"metadata",               // invitations (invitation-metadata)
	"request_id",             // security_events (0021)
	"trace_id",               // security_events (0021)
	"recorded_at",            // security_events (0021)
	"erased_at",              // users (0023)
	"last_rotated_at",        // sessions (0029)
}

func migrationNames(t *testing.T) []string {
	t.Helper()
	entries, err := fs.ReadDir(MigrationsFS, MigrationsDir)
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		names = append(names, e.Name())
	}
	return names
}

// TestMigrationInventory asserts the embedded pgx tree is exactly the canonical
// filename set and that every expected table, index, and schema addition is present.
func TestMigrationInventory(t *testing.T) {
	names := migrationNames(t)
	if len(names) != len(canonicalMigrations) {
		t.Fatalf("migration count = %d, want %d (%v)", len(names), len(canonicalMigrations), names)
	}
	for i, want := range canonicalMigrations {
		if names[i] != want {
			t.Errorf("migration[%d] = %q, want %q", i, names[i], want)
		}
	}

	var all strings.Builder
	for _, name := range names {
		b, err := fs.ReadFile(MigrationsFS, MigrationsDir+"/"+name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		all.Write(b)
		all.WriteByte('\n')
	}
	sql := all.String()

	for _, tbl := range expectedTables {
		if !strings.Contains(sql, "CREATE TABLE IF NOT EXISTS "+tbl+" (") {
			t.Errorf("missing CREATE TABLE for %q", tbl)
		}
	}
	for _, idx := range expectedIndexes {
		if !strings.Contains(sql, idx) {
			t.Errorf("missing index %q", idx)
		}
	}
	for _, col := range expectedColumns {
		if !strings.Contains(sql, col) {
			t.Errorf("missing column %q", col)
		}
	}
}

// TestMigrationParity asserts the pgx and turso trees carry byte-for-byte identical
// filename SETS (the standing invariant), reading the sibling module's on-disk tree.
func TestMigrationParity(t *testing.T) {
	own := migrationNames(t)

	siblingEntries, err := os.ReadDir("../turso/" + MigrationsDir)
	if errors.Is(err, fs.ErrNotExist) {
		// A published module (the module cache, a vendored copy) carries no sibling tree.
		t.Skipf("sibling turso migrations dir is absent (../turso/%s); parity runs only inside the gopernicus repo", MigrationsDir)
	}
	if err != nil {
		t.Fatalf("read sibling turso migrations: %v", err)
	}
	sibling := make([]string, 0, len(siblingEntries))
	for _, e := range siblingEntries {
		if e.IsDir() {
			continue
		}
		sibling = append(sibling, e.Name())
	}

	if len(own) != len(sibling) {
		t.Fatalf("pgx has %d migrations, turso has %d", len(own), len(sibling))
	}
	for i := range own {
		if own[i] != sibling[i] {
			t.Errorf("filename mismatch at %d: pgx %q vs turso %q", i, own[i], sibling[i])
		}
	}
}

// readMigration returns one embedded migration's text.
func readMigration(t *testing.T, name string) string {
	t.Helper()
	b, err := fs.ReadFile(MigrationsFS, MigrationsDir+"/"+name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

// retentionIndexes are the eight indexes 0023 builds, in file order.
var retentionIndexes = []struct{ name, table string }{
	{"idx_sessions_expires_at", sessionsTable},
	{"idx_authentication_grants_expires_at", authGrantsTable},
	{"idx_contact_changes_expires_at", contactChangesTable},
	{"idx_oauth_states_expires_at", oauthStatesTable},
	{"idx_challenges_expires_at", challengesTable},
	{"idx_oauth_authorization_codes_expires_at", oauthCodesTable},
	{"idx_oauth_clients_expires_at", oauthClientsTable},
	{"idx_oauth_refresh_history_expires_at", oauthHistoryTable},
}

// TestSessionActivityMigrationShape pins 0029's lock discipline: the lock
// timeout first, then one re-runnable, nullable, default-free ADD COLUMN.
func TestSessionActivityMigrationShape(t *testing.T) {
	sql := readMigration(t, sessionActivityMigration)
	if !strings.HasPrefix(sql, "SET LOCAL lock_timeout = '5s';\n") {
		t.Errorf("%s must begin with SET LOCAL lock_timeout", sessionActivityMigration)
	}
	if !strings.HasSuffix(sql, "\nALTER TABLE sessions ADD COLUMN IF NOT EXISTS last_rotated_at TIMESTAMPTZ;\n") {
		t.Errorf("%s must end with the one nullable ADD COLUMN IF NOT EXISTS", sessionActivityMigration)
	}
}

// TestRetentionMigrationOrder pins 0023's lock discipline: the lock timeout
// first, every index IF NOT EXISTS under its exact name (a CONCURRENTLY
// pre-build then makes it a no-op), and the ACCESS EXCLUSIVE ADD COLUMN on
// users last, after every index build.
func TestRetentionMigrationOrder(t *testing.T) {
	sql := readMigration(t, retentionMigration)
	if !strings.HasPrefix(sql, "SET LOCAL lock_timeout = '5s';\n") {
		t.Errorf("%s must begin with SET LOCAL lock_timeout", retentionMigration)
	}
	const addColumn = "ALTER TABLE users ADD COLUMN IF NOT EXISTS erased_at TIMESTAMPTZ NULL;\n"
	if !strings.HasSuffix(sql, addColumn) {
		t.Errorf("%s must END with %q", retentionMigration, addColumn)
	}
	last := -1
	for _, idx := range retentionIndexes {
		stmt := fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s (expires_at);", idx.name, idx.table)
		at := strings.Index(sql, stmt)
		if at < 0 {
			t.Errorf("%s lacks %q", retentionMigration, stmt)
			continue
		}
		if at < last {
			t.Errorf("%s: %s is out of order", retentionMigration, idx.name)
		}
		last = at
	}
	if strings.Contains(sql, "ON invitations") {
		t.Errorf("%s indexes invitations; the plan gives them none", retentionMigration)
	}
}

// TestRailRetentionMigrationPins pins 0024 to the Go constants and its
// load-bearing shape: the floor literal is MinSecurityEventRetention less the
// day of clock-skew margin, the max_rows clamp is MaxBatchSize, the evidence
// row's type is the domain's, the assertions run before anything is created,
// the search_path pin is computed from the table's namespace with pg_temp last,
// and EXECUTE is revoked from PUBLIC.
func TestRailRetentionMigrationPins(t *testing.T) {
	sql := readMigration(t, railRetentionFile)
	floorDays := int((maintenance.MinSecurityEventRetention - 24*time.Hour) / (24 * time.Hour))
	for _, want := range []string{
		fmt.Sprintf("SELECT '%d days'::pg_catalog.interval", floorDays),
		fmt.Sprintf("LEAST(GREATEST(p_max_rows, 1), %d)", maintenance.MaxBatchSize),
		"'" + securityevent.TypeSecurityEventsPurged + "'::pg_catalog.text",
		"CREATE OR REPLACE FUNCTION " + floorFunction + "() RETURNS pg_catalog.interval LANGUAGE sql STABLE AS",
		"CREATE OR REPLACE FUNCTION " + purgeFunction + "(p_before pg_catalog.timestamptz, p_max_rows pg_catalog.int4)",
		"LANGUAGE plpgsql SECURITY DEFINER AS",
		"ORDER BY created_at, id LIMIT p_max_rows FOR UPDATE SKIP LOCKED",
		"USING ERRCODE = '" + belowFloorSQLState + "'",
		"IF p_before IS NULL THEN",
		"'maintenance'::pg_catalog.text",
		"SESSION_USER::pg_catalog.text",
		"'ALTER FUNCTION " + purgeFunction + purgeFunctionArgs + " SET search_path = pg_catalog, %I, pg_temp'",
		"WHERE c.oid OPERATOR(pg_catalog.=) 'security_events'::pg_catalog.regclass));",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("%s lacks %q", railRetentionFile, want)
		}
	}
	if !strings.HasPrefix(sql, "SET LOCAL lock_timeout = '5s';\n") {
		t.Errorf("%s must begin with SET LOCAL lock_timeout", railRetentionFile)
	}
	const revoke = "REVOKE EXECUTE ON FUNCTION " + purgeFunction + purgeFunctionArgs + " FROM PUBLIC;\n"
	if !strings.HasSuffix(sql, revoke) {
		t.Errorf("%s must END with %q", railRetentionFile, revoke)
	}
	if strings.Index(sql, "pg_catalog.current_schema() IS DISTINCT FROM") > strings.Index(sql, "CREATE OR REPLACE FUNCTION") {
		t.Errorf("%s: the assertion block must precede every CREATE", railRetentionFile)
	}
	for _, forbidden := range []string{"FROM CURRENT", "IMMUTABLE", "SECURITY INVOKER"} {
		if strings.Contains(sql, forbidden) {
			t.Errorf("%s contains %q", railRetentionFile, forbidden)
		}
	}
}

// TestAuditTablesListsPurgeFunction pins the one DefinerFunction AuditTables
// hands the posture probe, schema-qualified under WithSchema; the floor
// function is not a definer and is not listed.
func TestAuditTablesListsPurgeFunction(t *testing.T) {
	got := AuditTables()[0].Functions
	if len(got) != 1 || got[0].Signature != "security_events_purge_before(pg_catalog.timestamptz, pg_catalog.int4)" {
		t.Fatalf("AuditTables().Functions = %+v", got)
	}
	s := disposableSchema(t, "Auth_X")
	if got := AuditTables(WithSchema(s))[0].Functions; len(got) != 1 || got[0].Signature != `"Auth_X".security_events_purge_before(pg_catalog.timestamptz, pg_catalog.int4)` {
		t.Fatalf("AuditTables(WithSchema).Functions = %+v", got)
	}
}
