package pgx

import (
	"io/fs"
	"os"
	"strings"
	"testing"
)

// Both dialects install the same canonical tables and access paths.
var canonicalMigrations = []string{"0001_iam_tuples.sql", "0003_iam_operations.sql", "0004_iam_operations_fenced.sql", "0005_iam_audit_integrity.sql", "0006_iam_audit_immutable.sql", "0007_iam_operations_expiry.sql", "0008_iam_audit_retention.sql"}
var expectedTables = []string{"iam_tuples", "iam_audit", "iam_operations"}
var expectedConstraints = []string{
	"ck_iam_operations_id", "ck_iam_operations_encoding", "ck_iam_operations_fingerprint", "ck_iam_operations_outcome",
	"ck_iam_tuples_scope", "ck_iam_tuples_refs", "ck_iam_audit_action",
	"ck_iam_audit_encoding", "ck_iam_audit_scope", "ck_iam_audit_tuple", "ck_iam_audit_source",
	"ck_iam_audit_purges_purged",
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

// 0008 is additive and admitted: the definer purge, its floor and the
// append-only iam_audit_purges evidence table (pgx); the floor row and the same
// evidence table (turso). Hosts copy it into their own streams and the runner
// checksums it, so both bodies are committed text.
const pinnedPgxMigration0008 = `SET LOCAL lock_timeout = '5s';
-- Audit retention. The runtime role holds no DELETE on iam_audit (0006), so the
-- purge runs through a SECURITY DEFINER function owned by the table's owner:
-- the 0006 trigger exempts the owner, and the runtime role needs only EXECUTE,
-- granted by the host:
--   GRANT EXECUTE ON FUNCTION iam_audit_purge_before(pg_catalog.timestamptz, pg_catalog.int4) TO <runtime role>;
-- The function refuses a cutoff younger than iam_audit_retention_floor()
-- (365 days, a framework invariant: no procedure lowers it) and appends one
-- iam_audit_purges row per call, a zero count included, in the same
-- transaction. iam_audit itself takes no summary row: every iam_audit row is a
-- tuple change. Every function and operator in the bodies is
-- pg_catalog-qualified. Re-running this file is safe for the role that owns
-- the functions.
--
-- The assertions run first: the function is pinned to the schema that holds
-- iam_audit, which must be the schema this file runs in, and its owner (the
-- migrator) must be a member of the table's owner or the definer cannot delete
-- past the trigger.
DO $iam_audit_retention_assert$
DECLARE
  table_schema pg_catalog.name;
  table_owner pg_catalog.oid;
BEGIN
  SELECT n.nspname, c.relowner INTO table_schema, table_owner
    FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid OPERATOR(pg_catalog.=) c.relnamespace
    WHERE c.oid OPERATOR(pg_catalog.=) 'iam_audit'::pg_catalog.regclass;
  IF pg_catalog.current_schema() IS DISTINCT FROM table_schema THEN
    RAISE EXCEPTION 'iam_audit lives in schema %, migration runs in %', table_schema, pg_catalog.current_schema();
  END IF;
  IF NOT pg_catalog.pg_has_role(CURRENT_USER, table_owner, 'MEMBER') THEN
    RAISE EXCEPTION 'the migrator must be a member of the owner of iam_audit; fix: GRANT % TO %',
      pg_catalog.quote_ident(pg_catalog.pg_get_userbyid(table_owner)), pg_catalog.quote_ident(CURRENT_USER::pg_catalog.text);
  END IF;
END
$iam_audit_retention_assert$;

-- One row per purge call: the evidence a summary row in iam_audit would fake.
-- Append-only by the 0006 trigger function; the runtime role is granted nothing.
CREATE TABLE IF NOT EXISTS iam_audit_purges (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    cutoff TIMESTAMPTZ NOT NULL,
    purged BIGINT NOT NULL CONSTRAINT ck_iam_audit_purges_purged CHECK (purged >= 0),
    floor_days INTEGER NOT NULL,
    max_rows INTEGER NOT NULL,
    session_role TEXT NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
REVOKE ALL ON TABLE iam_audit_purges FROM PUBLIC;
CREATE OR REPLACE TRIGGER iam_audit_purges_immutable BEFORE UPDATE OR DELETE ON iam_audit_purges FOR EACH ROW EXECUTE FUNCTION iam_audit_refuse_write();
CREATE OR REPLACE TRIGGER iam_audit_purges_no_truncate BEFORE TRUNCATE ON iam_audit_purges FOR EACH STATEMENT EXECUTE FUNCTION iam_audit_refuse_write();
ALTER TABLE iam_audit_purges ENABLE ALWAYS TRIGGER iam_audit_purges_immutable;
ALTER TABLE iam_audit_purges ENABLE ALWAYS TRIGGER iam_audit_purges_no_truncate;

CREATE OR REPLACE FUNCTION iam_audit_retention_floor() RETURNS pg_catalog.interval LANGUAGE sql STABLE AS $iam_audit_retention_floor$
SELECT '365 days'::pg_catalog.interval
$iam_audit_retention_floor$;

CREATE OR REPLACE FUNCTION iam_audit_purge_before(p_before pg_catalog.timestamptz, p_max_rows pg_catalog.int4)
RETURNS TABLE (cutoff pg_catalog.timestamptz, purged pg_catalog.int8)
LANGUAGE plpgsql SECURITY DEFINER AS $iam_audit_purge_before$
DECLARE
  n pg_catalog.int8;
  floor_interval pg_catalog.interval;
BEGIN
  IF p_before IS NULL THEN
    RAISE EXCEPTION 'iam_audit_purge_before: the cutoff is NULL' USING ERRCODE = '22023';
  END IF;
  floor_interval := iam_audit_retention_floor();
  IF p_before OPERATOR(pg_catalog.>) (pg_catalog.now() OPERATOR(pg_catalog.-) floor_interval) THEN
    RAISE EXCEPTION 'iam_audit_purge_before: cutoff % is below the retention floor of %', p_before, floor_interval USING ERRCODE = '22023';
  END IF;
  p_max_rows := LEAST(GREATEST(p_max_rows, 1), 10000);
  DELETE FROM iam_audit WHERE id IN (
    SELECT id FROM iam_audit WHERE occurred_at OPERATOR(pg_catalog.<=) p_before
    ORDER BY occurred_at, id LIMIT p_max_rows FOR UPDATE SKIP LOCKED);
  GET DIAGNOSTICS n = ROW_COUNT;
  INSERT INTO iam_audit_purges (cutoff, purged, floor_days, max_rows, session_role, recorded_at)
  VALUES (p_before, n, pg_catalog.date_part('day'::pg_catalog.text, floor_interval)::pg_catalog.int4, p_max_rows, SESSION_USER::pg_catalog.text, pg_catalog.now());
  cutoff := p_before;
  purged := n;
  RETURN NEXT;
END
$iam_audit_purge_before$;

-- Pin the definer's search_path to the table's schema. The schema is read
-- from the table, never current_schema(), and pg_temp is listed LAST so a
-- caller's temporary table cannot stand in for iam_audit. After a restore into
-- a renamed schema, re-run this block.
DO $iam_audit_retention_pin$
BEGIN
  EXECUTE pg_catalog.format('ALTER FUNCTION iam_audit_purge_before(pg_catalog.timestamptz, pg_catalog.int4) SET search_path = pg_catalog, %I, pg_temp',
    (SELECT n.nspname FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid OPERATOR(pg_catalog.=) c.relnamespace
      WHERE c.oid OPERATOR(pg_catalog.=) 'iam_audit'::pg_catalog.regclass));
END
$iam_audit_retention_pin$;

REVOKE EXECUTE ON FUNCTION iam_audit_purge_before(pg_catalog.timestamptz, pg_catalog.int4) FROM PUBLIC;
`

const pinnedTursoMigration0008 = `-- Audit retention. Row 1 of iam_audit_retention is the floor: the store refuses
-- a purge cutoff younger than now - floor_days. It is a footgun guard, not a
-- control — SQLite has no roles, and the connection that can purge can also
-- drop the triggers — and the floor is a framework invariant: no procedure
-- lowers it. A missing row is a refusal, never a default of zero.
-- iam_audit_purges takes one row per purge call, a zero count included, in the
-- purge's own transaction; iam_audit itself takes no summary row. Re-running
-- this file is safe and keeps an existing floor row.
CREATE TABLE IF NOT EXISTS main.iam_audit_retention (
    id         INTEGER PRIMARY KEY CHECK (id = 1),
    floor_days INTEGER NOT NULL CHECK (floor_days >= 1)
);
INSERT OR IGNORE INTO main.iam_audit_retention (id, floor_days) VALUES (1, 365);
CREATE TABLE IF NOT EXISTS main.iam_audit_purges (
    id          INTEGER PRIMARY KEY,
    cutoff      TEXT NOT NULL,
    purged      INTEGER NOT NULL CONSTRAINT ck_iam_audit_purges_purged CHECK (purged >= 0),
    floor_days  INTEGER NOT NULL,
    max_rows    INTEGER NOT NULL,
    recorded_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
DROP TRIGGER IF EXISTS main.iam_audit_purges_immutable_update;
CREATE TRIGGER main.iam_audit_purges_immutable_update BEFORE UPDATE ON iam_audit_purges
BEGIN
    SELECT RAISE(ABORT, 'iam_audit_purges is append-only');
END;
DROP TRIGGER IF EXISTS main.iam_audit_purges_immutable_delete;
CREATE TRIGGER main.iam_audit_purges_immutable_delete BEFORE DELETE ON iam_audit_purges
BEGIN
    SELECT RAISE(ABORT, 'iam_audit_purges is append-only');
END;
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

// TestMigration0008Pinned holds both dialects' 0008 bodies byte-for-byte.
func TestMigration0008Pinned(t *testing.T) {
	own, err := fs.ReadFile(MigrationsFS, MigrationsDir+"/"+auditRetentionMigration)
	if err != nil {
		t.Fatalf("read pgx 0008: %v", err)
	}
	if string(own) != pinnedPgxMigration0008 {
		t.Errorf("pgx 0008 changed:\n%s", own)
	}
	sibling, err := os.ReadFile("../turso/" + MigrationsDir + "/" + auditRetentionMigration)
	if err != nil {
		t.Fatalf("read turso 0008: %v", err)
	}
	if string(sibling) != pinnedTursoMigration0008 {
		t.Errorf("turso 0008 changed:\n%s", sibling)
	}
}
