package turso

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/maintenance"
)

// canonicalMigrations is the frozen auth-v3 canonical filename set (both dialect
// trees are byte-for-byte identical filename sets — the standing invariant). The
// pgx sibling asserts this same slice, so the two trees cannot drift apart.
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
	// SOC 2 S2: the audit rail's correlation and evidence columns (a rebuild
	// here), then its immutability trigger pair. Neither adds a table.
	"0021_security_events_integrity.sql",
	"0022_security_events_immutable.sql",
	// SOC 2 S3: the retention indexes and the users.erased_at marker, then the
	// rail's floor table. 0024 adds the one table maintenance reads.
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
	"oauth_clients",
	"oauth_authorization_codes",
	"oauth_refresh_history",
	"external_subjects",
	"security_events_retention",
	"passkey_credentials",
	"totp_credentials", "recovery_codes",
	"service_account_status",
}

// expectedIndexes are the auth-v3 indexes AV3-2.1 lands: the identifier claim /
// primary / active partial indexes, the challenge (user,purpose) and
// (purpose,secret_digest) uniques, the one-active contact-change unique, and the
// grant consume index.
var expectedIndexes = []string{
	"idx_sessions_user_created_id",
	"idx_oauth_authorization_codes_expiry",
	"idx_oauth_refresh_history_session",
	"idx_oauth_refresh_history_expiry",
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
	// SOC 2 S3: the purge predicates' access paths (0023; the OAuth2 codes and
	// refresh history keep their 0019 expiry indexes).
	"idx_sessions_expires_at", "idx_authentication_grants_expires_at", "idx_contact_changes_expires_at",
	"idx_oauth_states_expires_at", "idx_challenges_expires_at", "idx_oauth_clients_expires_at",
	// SOC 2 S4b: the passkey lookups (0025).
	"idx_passkey_credentials_credential_id", "idx_passkey_credentials_user_created_id",
	// SOC 2 S4c: the recovery-code lookups (0026).
	"idx_recovery_codes_key_id",
}

// expectedColumns are the schema additions to existing tables this task lands.
var expectedColumns = []string{
	"session_profile",
	"delegation",
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

// TestMigrationInventory asserts the embedded turso tree is exactly the canonical
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

// TestMigrationParity asserts the turso and pgx trees carry byte-for-byte identical
// filename SETS (the standing invariant), reading the sibling module's on-disk tree.
func TestMigrationParity(t *testing.T) {
	own := migrationNames(t)

	siblingEntries, err := os.ReadDir("../pgx/" + MigrationsDir)
	if errors.Is(err, fs.ErrNotExist) {
		// A published module (the module cache, a vendored copy) carries no sibling tree.
		t.Skipf("sibling pgx migrations dir is absent (../pgx/%s); parity runs only inside the gopernicus repo", MigrationsDir)
	}
	if err != nil {
		t.Fatalf("read sibling pgx migrations: %v", err)
	}
	sibling := make([]string, 0, len(siblingEntries))
	for _, e := range siblingEntries {
		if e.IsDir() {
			continue
		}
		sibling = append(sibling, e.Name())
	}

	if len(own) != len(sibling) {
		t.Fatalf("turso has %d migrations, pgx has %d", len(own), len(sibling))
	}
	for i := range own {
		if own[i] != sibling[i] {
			t.Errorf("filename mismatch at %d: turso %q vs pgx %q", i, own[i], sibling[i])
		}
	}
}

// TestRetentionMigrationShape pins 0023's order (indexes first, the users
// column last) and 0024's floor: re-runnable (IF NOT EXISTS + INSERT OR
// IGNORE, so a re-run keeps an existing row), floor_days >= 1, and the 365-day
// row pinned to MinSecurityEventRetention less the day of clock-skew margin.
func TestRetentionMigrationShape(t *testing.T) {
	b, err := fs.ReadFile(MigrationsFS, MigrationsDir+"/"+retentionMigration)
	if err != nil {
		t.Fatal(err)
	}
	const addColumn = "ALTER TABLE users ADD COLUMN erased_at TEXT NULL;\n"
	if sql := string(b); !strings.HasSuffix(sql, addColumn) || strings.Index(sql, "CREATE INDEX") > strings.Index(sql, addColumn) {
		t.Errorf("%s must build its indexes first and END with %q", retentionMigration, addColumn)
	}
	b, err = fs.ReadFile(MigrationsFS, MigrationsDir+"/"+railRetentionFile)
	if err != nil {
		t.Fatal(err)
	}
	floorDays := int((maintenance.MinSecurityEventRetention - 24*time.Hour) / (24 * time.Hour))
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS " + retentionFloorTable + " (",
		"id         INTEGER PRIMARY KEY CHECK (id = 1),",
		"floor_days INTEGER NOT NULL CHECK (floor_days >= 1)",
		fmt.Sprintf("INSERT OR IGNORE INTO %s (id, floor_days) VALUES (1, %d);", retentionFloorTable, floorDays),
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("%s lacks %q", railRetentionFile, want)
		}
	}
}
