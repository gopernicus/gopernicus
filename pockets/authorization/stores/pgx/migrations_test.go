package pgx

import (
	"io/fs"
	"os"
	"strings"
	"testing"
)

// Both dialects install the same canonical tables and access paths.
var canonicalMigrations = []string{"0001_iam_tuples.sql"}
var expectedTables = []string{"iam_tuples", "iam_audit"}
var expectedConstraints = []string{
	"ck_iam_tuples_scope", "ck_iam_tuples_refs", "ck_iam_audit_action",
	"ck_iam_audit_encoding", "ck_iam_audit_scope", "ck_iam_audit_tuple", "ck_iam_audit_source",
}
var expectedIndexes = []string{
	"idx_iam_tuples_subject", "idx_iam_tuples_relation_resource",
	"idx_iam_audit_time", "idx_iam_audit_resource", "idx_iam_audit_subject", "idx_iam_audit_actor",
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

func migrationsSQL(t *testing.T) string {
	t.Helper()
	var all strings.Builder
	for _, name := range migrationNames(t) {
		b, err := fs.ReadFile(MigrationsFS, MigrationsDir+"/"+name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		all.Write(b)
		all.WriteByte('\n')
	}
	return all.String()
}

// TestMigrationInventory pins the complete fresh schema, constraints and indexes.
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

	sql := migrationsSQL(t)
	for _, tbl := range expectedTables {
		if !strings.Contains(sql, "CREATE TABLE "+tbl+" (") {
			t.Errorf("missing CREATE TABLE for %q", tbl)
		}
	}
	for _, con := range expectedConstraints {
		if !strings.Contains(sql, con) {
			t.Errorf("missing constraint %q", con)
		}
	}
	for _, idx := range expectedIndexes {
		if !strings.Contains(sql, idx) {
			t.Errorf("missing index %q", idx)
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

// TestMigrationConstraintParity asserts both dialect trees express the SAME named
// constraints (the acceptance bar: "dialect trees express the same constraints").
// It reads the sibling turso SQL and requires every expected constraint name to
// appear in both.
func TestMigrationConstraintParity(t *testing.T) {
	ownSQL := migrationsSQL(t)

	var siblingSQL strings.Builder
	entries, err := os.ReadDir("../turso/" + MigrationsDir)
	if err != nil {
		t.Fatalf("read sibling turso migrations: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := os.ReadFile("../turso/" + MigrationsDir + "/" + e.Name())
		if err != nil {
			t.Fatalf("read sibling %s: %v", e.Name(), err)
		}
		siblingSQL.Write(b)
		siblingSQL.WriteByte('\n')
	}
	turso := siblingSQL.String()

	for _, con := range expectedConstraints {
		if !strings.Contains(ownSQL, con) {
			t.Errorf("pgx tree missing constraint %q", con)
		}
		if !strings.Contains(turso, con) {
			t.Errorf("turso tree missing constraint %q", con)
		}
	}
}
