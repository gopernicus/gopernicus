package pgx

import (
	"io/fs"
	"os"
	"strings"
	"testing"
)

// canonicalMigrations is the frozen authorization-v3 canonical filename set. Both
// dialect trees carry byte-for-byte identical filename SETS (the standing
// invariant); the turso sibling asserts this same slice, so the two trees cannot
// drift apart.
var canonicalMigrations = []string{
	"0001_iam_relationships.sql",
	"0002_iam_roles.sql",
	"0003_iam_scopes.sql",
	"0004_iam_mutations.sql",
}

// expectedTables are every CREATE TABLE the canonical set must define.
var expectedTables = []string{
	"iam_relationships",
	"iam_roles",
	"iam_scopes",
	"iam_mutations",
}

// expectedConstraints are the named CHECK/consistency constraints AZ3-2.1 lands:
// non-empty structural columns on every table, the valid-scope-kind and
// nonnegative-revision anchors on iam_scopes/iam_mutations, the persisted-outcome
// set on iam_mutations, and the consistent global/scoped role pair on iam_roles.
var expectedConstraints = []string{
	"ck_iam_relationships_nonempty",
	"ck_iam_roles_nonempty",
	"ck_iam_roles_scope_pair",
	"ck_iam_scopes_kind",
	"ck_iam_scopes_nonempty",
	"ck_iam_scopes_revision",
	"ck_iam_mutations_kind",
	"ck_iam_mutations_outcome",
	"ck_iam_mutations_revision",
	"ck_iam_mutations_nonempty",
}

// expectedIndexes are the relation-aware access-path indexes the ratified reads
// depend on: the exact-tuple unique, the one-relation-per-exact-SubjectRef unique
// (WITHOUT relation, so a subject holds one relation but usersets stay distinct),
// the resource/subject/type-relation secondaries feeding the recursive-CTE reads
// (AZ3-1.1), and the roles unique/subject/resource secondaries feeding the
// effective-role GROUP BY (AZ3-1.5).
var expectedIndexes = []string{
	"idx_iam_relationships_unique_tuple",
	"idx_iam_relationships_unique_subject",
	"idx_iam_relationships_resource",
	"idx_iam_relationships_subject",
	"idx_iam_relationships_type_relation",
	"idx_iam_roles_unique",
	"idx_iam_roles_subject",
	"idx_iam_roles_resource",
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

// TestMigrationInventory asserts the embedded pgx tree is exactly the canonical
// filename set and that every expected table, constraint, and access-path index is
// present. It proves the shape of iam_scopes and iam_mutations even though no Go
// consumer wires them yet.
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
		if !strings.Contains(sql, "CREATE TABLE IF NOT EXISTS "+tbl+" (") {
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
