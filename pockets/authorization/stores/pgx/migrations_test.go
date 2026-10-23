package pgx

import (
	"io/fs"
	"os"
	"strings"
	"testing"
)

// Both dialects install the same canonical tables and access paths.
var canonicalMigrations = []string{"0001_iam_tuples.sql", "0003_iam_operations.sql", "0004_iam_operations_fenced.sql", "0005_iam_audit_integrity.sql", "0006_iam_audit_immutable.sql", "0007_iam_operations_expiry.sql"}
var expectedTables = []string{"iam_tuples", "iam_audit", "iam_operations"}
var expectedConstraints = []string{
	"ck_iam_operations_id", "ck_iam_operations_encoding", "ck_iam_operations_fingerprint", "ck_iam_operations_outcome",
	"ck_iam_tuples_scope", "ck_iam_tuples_refs", "ck_iam_audit_action",
	"ck_iam_audit_encoding", "ck_iam_audit_scope", "ck_iam_audit_tuple", "ck_iam_audit_source",
}
var expectedIndexes = []string{
	"idx_iam_tuples_subject", "idx_iam_tuples_relation_resource",
	"idx_iam_audit_time", "idx_iam_audit_resource", "idx_iam_audit_subject", "idx_iam_audit_actor",
	"idx_iam_operations_committed",
}

// Hosts copy 0005 into their own streams and the runner checksums it, so both
// dialects' bodies are committed text: any edit must fail here first.
const pinnedPgxMigration0005 = `SET LOCAL lock_timeout = '5s';
ALTER TABLE iam_audit
    ADD COLUMN request_id TEXT COLLATE "C" NOT NULL DEFAULT '',
    ADD COLUMN trace_id TEXT COLLATE "C" NOT NULL DEFAULT '',
    ADD COLUMN seq BIGINT NULL,
    ADD COLUMN recorded_at TIMESTAMPTZ NULL;
CREATE SEQUENCE iam_audit_seq;
ALTER SEQUENCE iam_audit_seq OWNED BY iam_audit.seq;
ALTER TABLE iam_audit
    ALTER COLUMN seq SET DEFAULT nextval('iam_audit_seq'),
    ALTER COLUMN recorded_at SET DEFAULT now();
ALTER TABLE iam_operations ADD COLUMN recorded_at TIMESTAMPTZ NULL;
ALTER TABLE iam_operations ALTER COLUMN recorded_at SET DEFAULT now();
GRANT USAGE ON SEQUENCE iam_audit_seq TO PUBLIC;
`

const pinnedTursoMigration0005 = `CREATE TABLE main.iam_audit_next (
    id TEXT COLLATE BINARY NOT NULL PRIMARY KEY,
    event_id TEXT COLLATE BINARY NOT NULL,
    occurred_at TEXT NOT NULL,
    actor_type TEXT COLLATE BINARY NOT NULL,
    actor_id TEXT COLLATE BINARY NOT NULL,
    system_source TEXT COLLATE BINARY NOT NULL,
    reason TEXT NOT NULL,
    action TEXT NOT NULL CONSTRAINT ck_iam_audit_action CHECK (action IN ('added','removed')),
    encoding TEXT NOT NULL CONSTRAINT ck_iam_audit_encoding CHECK (encoding = 'tuple/v2'),
    scope_kind INTEGER NOT NULL,
    resource_type TEXT COLLATE BINARY NOT NULL,
    resource_id TEXT COLLATE BINARY NOT NULL,
    relation TEXT COLLATE BINARY NOT NULL,
    subject_type TEXT COLLATE BINARY NOT NULL,
    subject_id TEXT COLLATE BINARY NOT NULL,
    subject_relation TEXT COLLATE BINARY NOT NULL,
    request_id TEXT NOT NULL DEFAULT '',
    trace_id TEXT NOT NULL DEFAULT '',
    recorded_at TEXT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    CONSTRAINT ck_iam_audit_scope CHECK (
        (scope_kind=1 AND resource_type='' AND resource_id='')
        OR (scope_kind=2 AND resource_type<>'' AND resource_id<>'')
    ),
    CONSTRAINT ck_iam_audit_tuple CHECK (relation<>'' AND subject_type<>'' AND subject_id<>''),
    CONSTRAINT ck_iam_audit_source CHECK
        ((actor_type<>'' AND actor_id<>'' AND system_source='') OR (actor_type='' AND actor_id='' AND system_source<>''))
);
INSERT INTO main.iam_audit_next (id, event_id, occurred_at, actor_type, actor_id, system_source, reason, action, encoding, scope_kind, resource_type, resource_id, relation, subject_type, subject_id, subject_relation, recorded_at)
SELECT id, event_id, occurred_at, actor_type, actor_id, system_source, reason, action, encoding, scope_kind, resource_type, resource_id, relation, subject_type, subject_id, subject_relation, NULL AS recorded_at FROM main.iam_audit ORDER BY occurred_at, id;
DROP TABLE main.iam_audit;
ALTER TABLE main.iam_audit_next RENAME TO iam_audit;
CREATE INDEX main.idx_iam_audit_time ON iam_audit (occurred_at DESC, id DESC);
CREATE INDEX main.idx_iam_audit_resource ON iam_audit (resource_type, resource_id, occurred_at DESC, id DESC);
CREATE INDEX main.idx_iam_audit_subject ON iam_audit (subject_type, subject_id, occurred_at DESC, id DESC);
CREATE INDEX main.idx_iam_audit_actor ON iam_audit (actor_type, actor_id, occurred_at DESC, id DESC);
CREATE TABLE main.iam_operations_next (
    operation_id TEXT COLLATE BINARY NOT NULL PRIMARY KEY,
    encoding TEXT COLLATE BINARY NOT NULL,
    fingerprint TEXT COLLATE BINARY NOT NULL,
    outcome TEXT COLLATE BINARY NOT NULL,
    committed_at TEXT NOT NULL,
    recorded_at TEXT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    CONSTRAINT ck_iam_operations_id CHECK (operation_id <> ''),
    CONSTRAINT ck_iam_operations_encoding CHECK (encoding = 'operation/v1'),
    CONSTRAINT ck_iam_operations_fingerprint CHECK (length(fingerprint) = 64 AND fingerprint NOT GLOB '*[^0-9a-f]*'),
    CONSTRAINT ck_iam_operations_outcome CHECK (outcome IN ('applied', 'no_change', 'not_found', 'fenced'))
);
INSERT INTO main.iam_operations_next (operation_id, encoding, fingerprint, outcome, committed_at, recorded_at)
SELECT operation_id, encoding, fingerprint, outcome, committed_at, NULL AS recorded_at FROM main.iam_operations ORDER BY committed_at, operation_id;
DROP TABLE main.iam_operations;
ALTER TABLE main.iam_operations_next RENAME TO iam_operations;
`

// 0007 is additive and admitted, not required: each dialect builds the expiry
// index only if it is absent, so a CONCURRENTLY pre-build makes it a no-op.
const pinnedPgxMigration0007 = `SET LOCAL lock_timeout = '5s';
CREATE INDEX IF NOT EXISTS idx_iam_operations_committed ON iam_operations (committed_at, operation_id);
`

const pinnedTursoMigration0007 = `CREATE INDEX IF NOT EXISTS main.idx_iam_operations_committed ON iam_operations (committed_at, operation_id);
`

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

// TestMigration0005Pinned holds both dialects' 0005 bodies byte-for-byte.
func TestMigration0005Pinned(t *testing.T) {
	own, err := fs.ReadFile(MigrationsFS, MigrationsDir+"/0005_iam_audit_integrity.sql")
	if err != nil {
		t.Fatalf("read pgx 0005: %v", err)
	}
	if string(own) != pinnedPgxMigration0005 {
		t.Errorf("pgx 0005 changed:\n%s", own)
	}
	sibling, err := os.ReadFile("../turso/" + MigrationsDir + "/0005_iam_audit_integrity.sql")
	if err != nil {
		t.Fatalf("read turso 0005: %v", err)
	}
	if string(sibling) != pinnedTursoMigration0005 {
		t.Errorf("turso 0005 changed:\n%s", sibling)
	}
}

// TestMigration0007Pinned holds both dialects' 0007 bodies byte-for-byte.
func TestMigration0007Pinned(t *testing.T) {
	own, err := fs.ReadFile(MigrationsFS, MigrationsDir+"/"+operationExpiryMigration)
	if err != nil {
		t.Fatalf("read pgx 0007: %v", err)
	}
	if string(own) != pinnedPgxMigration0007 {
		t.Errorf("pgx 0007 changed:\n%s", own)
	}
	sibling, err := os.ReadFile("../turso/" + MigrationsDir + "/" + operationExpiryMigration)
	if err != nil {
		t.Fatalf("read turso 0007: %v", err)
	}
	if string(sibling) != pinnedTursoMigration0007 {
		t.Errorf("turso 0007 changed:\n%s", sibling)
	}
}
