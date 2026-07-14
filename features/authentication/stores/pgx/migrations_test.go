package pgx

import (
	"io/fs"
	"os"
	"strings"
	"testing"
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
}

// expectedIndexes are the auth-v3 indexes AV3-2.1 lands: the identifier claim /
// primary / active partial indexes, the challenge (user,purpose) and
// (purpose,secret_digest) uniques, the one-active contact-change unique, and the
// grant consume index.
var expectedIndexes = []string{
	"idx_invitations_kind_identifier",
	"idx_user_identifiers_auth_claim",
	"idx_user_identifiers_primary",
	"idx_user_identifiers_user_active",
	"idx_challenges_user_purpose",
	"idx_challenges_purpose_secret_digest",
	"idx_contact_changes_user_kind",
	"idx_authentication_grants_session_purpose_context",
}

// expectedColumns are the schema additions to existing tables this task lands.
var expectedColumns = []string{
	"auth_revision",          // users
	"authenticated_at",       // sessions
	"authentication_methods", // sessions
	"assurance_level",        // sessions
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
