package pgx

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk"
)

func TestSchemaProbe(t *testing.T) {
	ctx := context.Background()
	db, _ := liveRepos(t)
	if err := pgxdb.ProbeTable(ctx, db, qualify(t, "iam_audit")); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{`INSERT INTO iam_tuples VALUES(2,'doc','d','','user','u','')`, `INSERT INTO iam_tuples VALUES(2,'doc','','editor','user','u','')`} {
		if _, err := db.Exec(ctx, qualifySQL(t, statement)); err == nil {
			t.Fatalf("constraint accepted invalid fact: %s", statement)
		}
	}
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
func writeAudited(t *testing.T, db *pgxdb.DB, id string) {
	t.Helper()
	repos, err := testRepositories(t.Context(), db, WithAudit())
	if err != nil {
		t.Fatalf("bundle refused: %v", err)
	}
	if _, err := RelationshipRepository(t.Context(), db, WithAudit()); err != nil {
		t.Fatalf("facade refused: %v", err)
	}
	cmd := grantCmd(id, "viewer", "alice")
	cmd.OperationID = "op:" + id
	if _, err := repos.Mutations.Apply(auditContext(), cmd, nil); err != nil {
		t.Fatal(err)
	}
}

// migration0005Rows renders every audit and operation row with the columns 0005
// adds and whether its evidence columns are stamped.
func migration0005Rows(t *testing.T, db *pgxdb.DB) []string {
	t.Helper()
	rows, err := db.Query(t.Context(), `SELECT 'audit|' || resource_id || '|' || request_id || '|' || trace_id, seq IS NOT NULL AND recorded_at IS NOT NULL, seq IS NULL AND recorded_at IS NULL FROM iam_audit
UNION ALL SELECT 'operation|' || operation_id, recorded_at IS NOT NULL, recorded_at IS NULL FROM iam_operations ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var row string
		var stamped, unstamped bool
		if err := rows.Scan(&row, &stamped, &unstamped); err != nil {
			t.Fatal(err)
		}
		switch {
		case stamped:
			row += "|stamped"
		case unstamped:
			row += "|unstamped"
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestSchemaProbeToleratesMigration0005 admits a database before and after 0005:
// earlier rows stay unstamped and later writes take the new defaults.
func TestSchemaProbeToleratesMigration0005(t *testing.T) {
	db := emptyCanonicalFixture(t)
	if err := pgxdb.RunMigrations(t.Context(), db, migrationsThrough(t, "0004_iam_operations_fenced.sql"), MigrationsDir); err != nil {
		t.Fatal(err)
	}
	writeAudited(t, db, "before")
	if err := pgxdb.RunMigrations(t.Context(), db, MigrationsFS, MigrationsDir); err != nil {
		t.Fatal(err)
	}
	writeAudited(t, db, "after")
	want := []string{"audit|after|||stamped", "audit|before|||unstamped", "operation|op:after|stamped", "operation|op:before|unstamped"}
	if got := migration0005Rows(t, db); !slices.Equal(got, want) {
		t.Fatalf("0005 columns: got %v, want %v", got, want)
	}
}

// TestSchemaProbeRefusesMigration0005Variants keeps the optional columns exact:
// another column, any added constraint, uniqueness or a different shape is refused.
func TestSchemaProbeRefusesMigration0005Variants(t *testing.T) {
	for name, statement := range map[string]string{
		"fifth audit column":       `ALTER TABLE iam_audit ADD COLUMN host_note TEXT COLLATE "C" NOT NULL DEFAULT ''`,
		"audit check":              `ALTER TABLE iam_audit ADD CONSTRAINT ck_iam_audit_request_id CHECK (length(request_id) <= 128)`,
		"unique seq":               `CREATE UNIQUE INDEX uq_iam_audit_seq ON iam_audit (seq)`,
		"nullable request id":      `ALTER TABLE iam_audit ALTER COLUMN request_id DROP NOT NULL`,
		"request id collation":     `ALTER TABLE iam_audit ALTER COLUMN request_id TYPE text COLLATE "default"`,
		"integer seq":              `ALTER TABLE iam_audit ALTER COLUMN seq TYPE integer`,
		"second operations column": `ALTER TABLE iam_operations ADD COLUMN host_note TEXT COLLATE "C" NOT NULL DEFAULT ''`,
		"operations check":         `ALTER TABLE iam_operations ADD CONSTRAINT ck_iam_operations_recorded_at CHECK (recorded_at IS NOT NULL)`,
	} {
		t.Run(name, func(t *testing.T) {
			db := canonicalFixture(t, false)
			if _, err := db.Exec(t.Context(), statement); err != nil {
				t.Fatal(err)
			}
			if _, err := testRepositories(t.Context(), db); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("bundle accepted altered schema: %v", err)
			}
		})
	}
}

// TestMigration0005TableGrantsWriter proves 0005's PUBLIC sequence grant: a role
// holding table privileges only keeps writing audit rows, and cannot without it.
func TestMigration0005TableGrantsWriter(t *testing.T) {
	ctx := context.Background()
	admin := canonicalFixture(t, false)
	var schema string
	if err := admin.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	name := "iam_audit_writer_" + fmt.Sprint(time.Now().UnixNano())
	tupleExec(t, admin, "CREATE ROLE "+name+" NOLOGIN")
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP OWNED BY "+name+"; DROP ROLE "+name); err != nil {
			t.Error(err)
		}
	})
	tupleExec(t, admin, `GRANT USAGE ON SCHEMA "`+schema+`" TO `+name)
	tupleExec(t, admin, "GRANT SELECT, INSERT, UPDATE, DELETE ON iam_tuples, iam_audit, iam_operations TO "+name)
	db := cacheConnection(t, schema)
	tupleExec(t, db, "SET ROLE "+name)
	writeAudited(t, db, "granted")
	tupleExec(t, admin, "REVOKE USAGE ON SEQUENCE iam_audit_seq FROM PUBLIC")
	repos, err := testRepositories(ctx, db, WithAudit())
	if err != nil {
		t.Fatal(err)
	}
	cmd := grantCmd("revoked", "viewer", "alice")
	cmd.OperationID = "op:revoked"
	if _, err := repos.Mutations.Apply(auditContext(), cmd, nil); err == nil {
		t.Fatal("audit write succeeded without sequence usage")
	}
	want := []string{"audit|granted|||stamped", "operation|op:granted|stamped"}
	if got := migration0005Rows(t, admin); !slices.Equal(got, want) {
		t.Fatalf("rows: got %v, want %v", got, want)
	}
}
