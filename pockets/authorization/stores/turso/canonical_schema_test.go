package turso

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

// preIntegrityAudit renders every iam_audit column that predates 0005.
const preIntegrityAudit = `SELECT id || '|' || event_id || '|' || occurred_at || '|' || actor_type || '|' || actor_id || '|' || system_source || '|' || reason || '|' || action || '|' || encoding || '|' || scope_kind || '|' || resource_type || '|' || resource_id || '|' || relation || '|' || subject_type || '|' || subject_id || '|' || subject_relation FROM main.iam_audit ORDER BY occurred_at, id`

const recordedAtStamp = `CASE WHEN recorded_at IS NULL THEN 'unstamped' WHEN recorded_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9]Z' THEN 'stamped' ELSE recorded_at END`

// migration0005Columns renders the columns 0005 adds to every audit and
// operation row, and whether recorded_at is stamped in the canonical format.
const migration0005Columns = `SELECT 'audit|' || resource_id || '|' || request_id || '|' || trace_id || '|' || ` + recordedAtStamp + ` FROM main.iam_audit
UNION ALL SELECT 'operation|' || operation_id || '|' || ` + recordedAtStamp + ` FROM main.iam_operations ORDER BY 1`

func TestCanonicalConstructorsRejectPartialSchemas(t *testing.T) {
	for _, tc := range []struct{ name, table, from, to string }{
		{"missing primary key", "iam_tuples", "PRIMARY KEY (scope_kind, resource_type, resource_id, relation, subject_type, subject_id, subject_relation),", ""},
		{"partial identity", "iam_tuples", "PRIMARY KEY (scope_kind, resource_type, resource_id, relation, subject_type, subject_id, subject_relation)", "PRIMARY KEY (scope_kind, resource_type, resource_id, subject_type, subject_id, subject_relation)"},
		{"missing column", "iam_audit", "encoding TEXT NOT NULL CONSTRAINT ck_iam_audit_encoding CHECK (encoding = 'tuple/v2'),", ""},
		{"wrong type", "iam_tuples", "scope_kind INTEGER", "scope_kind TEXT"},
		{"nullable column", "iam_tuples", "relation TEXT COLLATE BINARY NOT NULL", "relation TEXT COLLATE BINARY"},
		{"wrong collation", "iam_tuples", "COLLATE BINARY", "COLLATE NOCASE"},
		{"permissive refs", "iam_tuples", "CHECK (relation <> '' AND subject_type <> '' AND subject_id <> '')", "CHECK (true)"},
		{"permissive scope", "iam_tuples", "scope_kind = 2 AND resource_type <> '' AND resource_id <> ''", "scope_kind=2"},
		{"audit encoding check", "iam_audit", "CHECK (encoding = 'tuple/v2')", "CHECK (true)"},
		{"audit source check", "iam_audit", "actor_type<>'' AND actor_id<>'' AND system_source=''", "actor_type<>''"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := canonicalFixture(t, false)
			var ddl string
			if err := db.QueryRow(t.Context(), `SELECT sql FROM main.sqlite_schema WHERE name=?`, tc.table).Scan(&ddl); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(ddl, tc.from) {
				t.Fatalf("test replacement absent: %s", ddl)
			}
			ddl = strings.Replace(ddl, tc.from, tc.to, 1)
			if _, err := db.Exec(t.Context(), "DROP TABLE main."+tc.table+"; "+ddl); err != nil {
				t.Fatal(err)
			}
			if _, err := testRepositories(t.Context(), db); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("bundle accepted altered schema: %v", err)
			}
			if _, err := RelationshipRepository(t.Context(), db, WithAudit()); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("facade accepted altered schema: %v", err)
			}
		})
	}
	for name, statement := range map[string]string{
		"exclusive label index": `CREATE UNIQUE INDEX main.restrictive_exclusivity ON iam_tuples(scope_kind,resource_type,resource_id,subject_type,subject_id,subject_relation)`,
	} {
		t.Run(name, func(t *testing.T) {
			db := canonicalFixture(t, false)
			if _, err := db.Exec(t.Context(), statement); err != nil {
				t.Fatal(err)
			}
			if _, err := testRepositories(t.Context(), db); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("accepted restrictive uniqueness: %v", err)
			}
		})
	}
}

func TestCanonicalUncachedIgnoresTempShadows(t *testing.T) {
	db, _ := cacheFixture(t, false, 1)
	if _, err := db.Exec(t.Context(), `CREATE TEMP TABLE iam_tuples AS SELECT * FROM main.iam_tuples;
INSERT INTO temp.iam_tuples VALUES (1,'','','admin','user','shadow','');`); err != nil {
		t.Fatal(err)
	}
	repos, err := testRepositories(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	fact := tuples.Tuple{Scope: tuples.Global(), Relation: "admin", Subject: tuples.SubjectRef{Type: "user", ID: "shadow"}}
	if allowed, err := repos.Tuples.Contains(t.Context(), fact); err != nil || allowed {
		t.Fatalf("TEMP granted authority: %v/%v", allowed, err)
	}
	if _, err := db.Exec(t.Context(), `DROP TABLE main.iam_tuples; CREATE TABLE main.iam_tuples(scope_kind INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := testRepositories(t.Context(), db); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("TEMP masked malformed main: %v", err)
	}
	if _, err := db.Exec(t.Context(), `DROP TABLE main.iam_tuples`); err != nil {
		t.Fatal(err)
	}
	if _, err := testRepositories(t.Context(), db); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("TEMP masked missing main: %v", err)
	}
}

func TestOperationSchemaRequiredOnlyForBundle(t *testing.T) {
	unfenced, err := MigrationsFS.ReadFile(MigrationsDir + "/0003_iam_operations.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"DROP TABLE main.iam_operations",
		"DROP TABLE main.iam_operations; CREATE TABLE main.iam_operations(operation_id TEXT)",
		"DROP TABLE main.iam_operations; " + string(unfenced),
	} {
		t.Run(statement, func(t *testing.T) {
			db := canonicalFixture(t, false)
			if _, err := db.Exec(t.Context(), statement); err != nil {
				t.Fatal(err)
			}
			if _, err := testRepositories(t.Context(), db); err == nil {
				t.Fatal("bundle accepted missing or incompatible operations table")
			}
			if _, err := RelationshipRepository(t.Context(), db, WithAudit()); err != nil {
				t.Fatalf("facade requires ledger: %v", err)
			}
		})
	}
}

// TestCanonicalSchemaAdmitsMigration0007 boots with and without 0007's expiry
// index: the probe refuses only extra unique indexes, so the index is admitted,
// not required, Expire works either way, and 0007 over an index that already
// exists is a no-op.
func TestCanonicalSchemaAdmitsMigration0007(t *testing.T) {
	db := emptyCanonicalFixture(t)
	if err := tursodb.RunMigrations(t.Context(), db, migrationsThrough(t, "0006_iam_audit_immutable.sql"), MigrationsDir); err != nil {
		t.Fatal(err)
	}
	writeAudited(t, db, "before")
	repos, err := testRepositories(t.Context(), db)
	if err != nil {
		t.Fatalf("bundle refused a database without 0007: %v", err)
	}
	if n, err := repos.OperationExpiry.Expire(t.Context(), time.Now().Add(time.Second), 10); err != nil || n != 1 {
		t.Fatalf("Expire without the index: %d %v", n, err)
	}
	data, err := MigrationsFS.ReadFile(MigrationsDir + "/0007_iam_operations_expiry.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(t.Context(), string(data)); err != nil {
		t.Fatalf("pre-build the index: %v", err)
	}
	if err := tursodb.RunMigrations(t.Context(), db, MigrationsFS, MigrationsDir); err != nil {
		t.Fatalf("0007 over an existing index: %v", err)
	}
	if got, err := queryStrings(t.Context(), db, `SELECT name FROM main.sqlite_schema WHERE type='index' AND tbl_name='iam_operations' AND name LIKE 'idx_%'`); err != nil || !slices.Equal(got, []string{"idx_iam_operations_committed"}) {
		t.Fatalf("0007 index: %v %v", got, err)
	}
	writeAudited(t, db, "after")
}

// migrationsThrough is the embedded stream up to and including last: the
// database a host has before it copies a later file.
func migrationsThrough(t *testing.T, last string) fstest.MapFS {
	t.Helper()
	stream := fstest.MapFS{}
	for _, name := range migrationNames(t) {
		if name > last {
			break
		}
		data, err := MigrationsFS.ReadFile(MigrationsDir + "/" + name)
		if err != nil {
			t.Fatal(err)
		}
		stream[MigrationsDir+"/"+name] = &fstest.MapFile{Data: data}
	}
	return stream
}

// writeAudited admits the bundle and the audited facade, then records one
// audited, ledgered grant on doc id.
func writeAudited(t *testing.T, db *tursodb.DB, id string) {
	t.Helper()
	repos, err := testRepositories(t.Context(), db, WithAudit())
	if err != nil {
		t.Fatalf("bundle refused: %v", err)
	}
	if _, err := RelationshipRepository(t.Context(), db, WithAudit()); err != nil {
		t.Fatalf("facade refused: %v", err)
	}
	ctx := audit.WithSource(t.Context(), audit.Source{System: "migration-0005-test"})
	cmd := mutations.Command{Target: mutations.Target{Kind: mutations.TargetResource, Type: "doc", ID: id}, Operation: mutations.OpGrant, Relationships: []mutations.RelationshipRow{{Relation: "viewer", Subject: relationships.SubjectRef{Type: "user", ID: "alice"}}}, OperationID: "op:" + id}
	if _, err := repos.Mutations.Apply(ctx, cmd, nil); err != nil {
		t.Fatal(err)
	}
}

// insertPre0005Rows writes one audit and one operation row with the columns
// that predate 0005, as an earlier store version did.
func insertPre0005Rows(t *testing.T, db *tursodb.DB) {
	t.Helper()
	at := tursodb.FormatTime(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	if _, err := db.Exec(t.Context(), `INSERT INTO main.iam_audit (id, event_id, occurred_at, actor_type, actor_id, system_source, reason, action, encoding, scope_kind, resource_type, resource_id, relation, subject_type, subject_id, subject_relation)
VALUES ('before:00000000000000000001', 'before', ?, '', '', 'migration-0005-test', '', 'added', 'tuple/v2', 2, 'doc', 'before', 'viewer', 'user', 'alice', '')`, at); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(t.Context(), `INSERT INTO main.iam_operations (operation_id, encoding, fingerprint, outcome, committed_at) VALUES ('op:before', 'operation/v1', ?, 'applied', ?)`, strings.Repeat("a", 64), at); err != nil {
		t.Fatal(err)
	}
}

// assertMigration0005Required starts from a database migrated through 0004:
// the bundle and the audited facade refuse it, the rebuild keeps every earlier
// row with recorded_at NULL and later writes take the new defaults.
func assertMigration0005Required(t *testing.T, db *tursodb.DB) {
	t.Helper()
	if _, err := testRepositories(t.Context(), db, WithAudit()); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("bundle admitted a database without 0005: %v", err)
	}
	if _, err := RelationshipRepository(t.Context(), db, WithAudit()); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("audited facade admitted a database without 0005: %v", err)
	}
	insertPre0005Rows(t, db)
	before, err := queryStrings(t.Context(), db, preIntegrityAudit)
	if err != nil {
		t.Fatal(err)
	}
	if err := tursodb.RunMigrations(t.Context(), db, MigrationsFS, MigrationsDir); err != nil {
		t.Fatal(err)
	}
	if after, err := queryStrings(t.Context(), db, preIntegrityAudit); err != nil || !slices.Equal(after, before) || len(after) != 1 {
		t.Fatalf("rebuild changed audit rows: before=%v after=%v %v", before, after, err)
	}
	assertFreshIndexes(t, db)
	writeAudited(t, db, "after")
	want := []string{"audit|after|||stamped", "audit|before|||unstamped", "operation|op:after|stamped", "operation|op:before|unstamped"}
	if got, err := queryStrings(t.Context(), db, migration0005Columns); err != nil || !slices.Equal(got, want) {
		t.Fatalf("0005 columns: got %v, want %v %v", got, want, err)
	}
}

func TestCanonicalSchemaRequiresMigration0005(t *testing.T) {
	db := emptyCanonicalFixture(t)
	if err := tursodb.RunMigrations(t.Context(), db, migrationsThrough(t, "0004_iam_operations_fenced.sql"), MigrationsDir); err != nil {
		t.Fatal(err)
	}
	assertMigration0005Required(t, db)
}

// TestCanonicalSchemaRefusesMigration0005Variants keeps the 0005 bodies exact:
// another column, an added constraint or a nullable id is refused.
func TestCanonicalSchemaRefusesMigration0005Variants(t *testing.T) {
	for _, tc := range []struct{ name, table, from, to string }{
		{"fifth audit column", "iam_audit", "    recorded_at TEXT NULL", "    host_note TEXT NOT NULL DEFAULT '',\n    recorded_at TEXT NULL"},
		{"audit check", "iam_audit", "    CONSTRAINT ck_iam_audit_scope", "    CONSTRAINT ck_iam_audit_request_id CHECK (length(request_id) <= 128),\n    CONSTRAINT ck_iam_audit_scope"},
		{"nullable request id", "iam_audit", "request_id TEXT NOT NULL", "request_id TEXT"},
		{"second operations column", "iam_operations", "    recorded_at TEXT NULL", "    host_note TEXT NOT NULL DEFAULT '',\n    recorded_at TEXT NULL"},
		{"operations check", "iam_operations", "    CONSTRAINT ck_iam_operations_id", "    CONSTRAINT ck_iam_operations_recorded_at CHECK (recorded_at IS NOT NULL),\n    CONSTRAINT ck_iam_operations_id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := canonicalFixture(t, false)
			var ddl string
			if err := db.QueryRow(t.Context(), `SELECT sql FROM main.sqlite_schema WHERE name=?`, tc.table).Scan(&ddl); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(ddl, tc.from) {
				t.Fatalf("test replacement absent: %s", ddl)
			}
			ddl = strings.Replace(ddl, tc.from, tc.to, 1)
			if _, err := db.Exec(t.Context(), "DROP TABLE main."+tc.table+"; "+ddl); err != nil {
				t.Fatal(err)
			}
			if _, err := testRepositories(t.Context(), db); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("bundle accepted altered schema: %v", err)
			}
		})
	}
}
