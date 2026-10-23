package pgx

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

const (
	immutableMigration    = "0006_iam_audit_immutable.sql"
	immutableRolePassword = "authorization_immutable_pw"
)

// TestAuditTablesMatchMigration0006 keeps the probe's canonical function bodies
// and the shipped migration identical: each body is the text between its
// function's own dollar quotes, exactly as pg_proc.prosrc holds it.
func TestAuditTablesMatchMigration0006(t *testing.T) {
	data, err := MigrationsFS.ReadFile(MigrationsDir + "/" + immutableMigration)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.HasPrefix(text, "SET LOCAL lock_timeout = '5s';\n") {
		t.Fatal("0006 does not begin with its lock timeout")
	}
	schema, err := pgxdb.NewSchema("tenant")
	if err != nil {
		t.Fatal(err)
	}
	tables := AuditTables(WithAudit(), WithSchema(schema))
	if len(tables) != 2 || tables[0].Name != `"tenant".iam_audit` || tables[0].AllowDelete || tables[1].Name != `"tenant".iam_operations` || !tables[1].AllowDelete {
		t.Fatalf("audit tables: %+v", tables)
	}
	for i, table := range []string{"iam_audit", "iam_operations"} {
		canonical := tables[i].Canonical
		if len(canonical) != 1 || canonical[0].Function != table+"_refuse_write" {
			t.Fatalf("%s canonical functions: %+v", table, canonical)
		}
		fn := canonical[0].Function
		parts := strings.Split(text, "$"+fn+"$")
		if len(parts) != 3 || parts[1] != canonical[0].Body {
			t.Fatalf("%s body drifted from 0006:\n%q\nwant\n%q", fn, parts, canonical[0].Body)
		}
		for _, statement := range []string{
			"CREATE OR REPLACE FUNCTION " + fn + "() RETURNS trigger LANGUAGE plpgsql SET search_path = pg_catalog, pg_temp AS $" + fn + "$",
			"RAISE EXCEPTION '" + table + " is append-only' USING ERRCODE = '42501';",
			"CREATE OR REPLACE TRIGGER " + table + "_immutable BEFORE UPDATE OR DELETE ON " + table + " FOR EACH ROW EXECUTE FUNCTION " + fn + "();",
			"CREATE OR REPLACE TRIGGER " + table + "_no_truncate BEFORE TRUNCATE ON " + table + " FOR EACH STATEMENT EXECUTE FUNCTION " + fn + "();",
			"ALTER TABLE " + table + " ENABLE ALWAYS TRIGGER " + table + "_immutable;",
			"ALTER TABLE " + table + " ENABLE ALWAYS TRIGGER " + table + "_no_truncate;",
		} {
			if !strings.Contains(text, statement) {
				t.Errorf("0006 lacks %s", statement)
			}
		}
	}
	if strings.Contains(text, " WHEN ") {
		t.Fatal("0006 tests a row in a WHEN clause; the probe refuses one")
	}
	if plain := AuditTables(); len(plain) != 1 || plain[0].Name != "iam_operations" || !plain[0].AllowDelete {
		t.Fatalf("AuditTables without WithAudit: %+v", plain)
	}
}

// immutableFixture is a migrated canonical schema whose tables a NOLOGIN
// migrator role owns, and a LOGIN runtime role (both unique per run) holding
// table grants. Both are dropped with everything granted to them at cleanup.
type immutableFixture struct {
	admin    *pgxdb.DB
	schema   string
	migrator string
	role     string
}

func newImmutableFixture(t *testing.T, sequenceUsage bool) immutableFixture {
	t.Helper()
	admin := canonicalFixture(t, false)
	f := immutableFixture{admin: admin}
	if err := admin.QueryRow(t.Context(), `SELECT current_schema()`).Scan(&f.schema); err != nil {
		t.Fatal(err)
	}
	id := fmt.Sprintf("iam_immutable_%d", time.Now().UnixNano())
	f.migrator, f.role = id+"_migrator", id+"_runtime"
	tupleExec(t, admin, `CREATE ROLE "`+f.migrator+`" NOLOGIN`)
	tupleExec(t, admin, `CREATE ROLE "`+f.role+`" LOGIN PASSWORD '`+immutableRolePassword+`'`)
	t.Cleanup(func() {
		for _, role := range []string{f.role, f.migrator} {
			if _, err := admin.Exec(context.Background(), `DROP OWNED BY "`+role+`" CASCADE`); err != nil {
				t.Errorf("drop owned by %s: %v", role, err)
			}
			if _, err := admin.Exec(context.Background(), `DROP ROLE "`+role+`"`); err != nil {
				t.Errorf("drop role %s: %v", role, err)
			}
		}
	})
	for _, statement := range []string{
		`GRANT USAGE ON SCHEMA "` + f.schema + `" TO "` + f.migrator + `", "` + f.role + `"`,
		`ALTER TABLE iam_tuples OWNER TO "` + f.migrator + `"`,
		`ALTER TABLE iam_audit OWNER TO "` + f.migrator + `"`,
		`ALTER TABLE iam_operations OWNER TO "` + f.migrator + `"`,
		`GRANT SELECT, INSERT, UPDATE, DELETE, TRUNCATE ON iam_tuples, iam_audit, iam_operations TO "` + f.role + `"`,
	} {
		tupleExec(t, admin, statement)
	}
	if sequenceUsage {
		tupleExec(t, admin, `GRANT USAGE ON SEQUENCE iam_audit_seq TO "`+f.role+`"`)
	}
	return f
}

// runtime connects as the runtime role under mode, on the fixture's schema.
func (f immutableFixture) runtime(t *testing.T, mode environment.Mode) *pgxdb.DB {
	t.Helper()
	parsed, err := url.Parse(os.Getenv("POSTGRES_TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	parsed.User = url.UserPassword(f.role, immutableRolePassword)
	query := parsed.Query()
	query.Set("search_path", f.schema)
	parsed.RawQuery = query.Encode()
	db, err := pgxdb.Open(t.Context(), pgxdb.Config{DSN: parsed.String(), MaxConns: 2, RuntimeMode: mode, TLSDisabledAcknowledged: mode != ""})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// owner is one connection acting as the migrator that owns the tables.
func (f immutableFixture) owner(t *testing.T) *pgxdb.DB {
	t.Helper()
	db := cacheConnection(t, f.schema)
	tupleExec(t, db, `SET ROLE "`+f.migrator+`"`)
	return db
}

func expectForbidden(t *testing.T, db *pgxdb.DB, statements ...string) {
	t.Helper()
	for _, statement := range statements {
		if _, err := db.Exec(t.Context(), statement); !errors.Is(err, sdk.ErrForbidden) {
			t.Fatalf("%s: %v; want sdk.ErrForbidden", statement, err)
		}
	}
}

func queryText(t *testing.T, db *pgxdb.DB, query string) string {
	t.Helper()
	var out string
	if err := db.QueryRow(t.Context(), query).Scan(&out); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return out
}

// TestImmutableTriggersLive proves migration 0006 against a real database: a
// non-owner cannot change or remove audit rows or fenced operations, the owner
// and a migrator-owned SECURITY DEFINER function pass, and the store stamps
// seq and recorded_at on every row.
func TestImmutableTriggersLive(t *testing.T) {
	f := newImmutableFixture(t, true)
	runtime := f.runtime(t, "")
	repos, err := testRepositories(t.Context(), runtime, WithAudit())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"first", "second", "purged"} {
		cmd := grantCmd(id, "viewer", "alice")
		cmd.OperationID = "op:" + id
		if _, err := repos.Mutations.Apply(auditContext(), cmd, nil); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := repos.Operations.Fence(t.Context(), "op:fenced"); err != nil || got.Outcome != mutations.OutcomeFenced || got.Recorded {
		t.Fatalf("fence: %+v %v", got, err)
	}
	if got := queryText(t, f.admin, `SELECT string_agg(resource_id || ':' || (seq IS NOT NULL AND recorded_at IS NOT NULL)::text, ',' ORDER BY seq) FROM iam_audit`); got != "first:true,second:true,purged:true" {
		t.Fatalf("seq order and stamps: %s", got)
	}
	if got := queryText(t, f.admin, `SELECT bool_and(a.seq < b.seq AND a.recorded_at < b.recorded_at)::text FROM iam_audit a JOIN iam_audit b ON a.resource_id = 'first' AND b.resource_id = 'second'`); got != "true" {
		t.Fatal("seq and recorded_at do not increase in write order")
	}
	if got := queryText(t, f.admin, `SELECT count(*) FILTER (WHERE recorded_at IS NULL)::text || '/' || count(*)::text FROM iam_operations`); got != "0/4" {
		t.Fatalf("operation recorded_at: %s", got)
	}

	expectForbidden(t, runtime,
		`UPDATE iam_audit SET reason = 'rewritten'`,
		`DELETE FROM iam_audit WHERE resource_id = 'first'`,
		`TRUNCATE iam_audit`,
		`UPDATE iam_operations SET committed_at = committed_at WHERE operation_id = 'op:first'`,
		`DELETE FROM iam_operations WHERE operation_id = 'op:fenced'`,
		`TRUNCATE iam_operations`,
	)
	if tag, err := runtime.Exec(t.Context(), `DELETE FROM iam_operations WHERE operation_id = 'op:first'`); err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("non-fenced operation delete: %v %v", tag, err)
	}
	if got := queryText(t, f.admin, `SELECT count(*)::text || '/' || count(*) FILTER (WHERE reason = 'test change')::text FROM iam_audit`); got != "3/3" {
		t.Fatalf("audit rows changed by a refused write: %s", got)
	}

	tupleExec(t, f.admin, `CREATE FUNCTION purge_audit_fixture() RETURNS bigint LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $purge$
DECLARE removed bigint;
BEGIN
  DELETE FROM "`+f.schema+`".iam_audit WHERE resource_id = 'purged';
  GET DIAGNOSTICS removed = ROW_COUNT;
  RETURN removed;
END
$purge$`)
	tupleExec(t, f.admin, `ALTER FUNCTION purge_audit_fixture() OWNER TO "`+f.migrator+`"`)
	tupleExec(t, f.admin, `REVOKE ALL ON FUNCTION purge_audit_fixture() FROM PUBLIC`)
	tupleExec(t, f.admin, `GRANT EXECUTE ON FUNCTION purge_audit_fixture() TO "`+f.role+`"`)
	var removed int64
	if err := runtime.QueryRow(t.Context(), `SELECT purge_audit_fixture()`).Scan(&removed); err != nil || removed != 1 {
		t.Fatalf("migrator-owned SECURITY DEFINER purge as the runtime role: %d %v", removed, err)
	}

	owner := f.owner(t)
	if tag, err := owner.Exec(t.Context(), `UPDATE iam_audit SET reason = 'owner backfill' WHERE resource_id = 'first'`); err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("owner update: %v %v", tag, err)
	}
	if got := queryText(t, f.admin, `SELECT reason FROM iam_audit WHERE resource_id = 'first'`); got != "owner backfill" {
		t.Fatalf("owner update did not persist: %q", got)
	}
	if tag, err := owner.Exec(t.Context(), `DELETE FROM iam_audit WHERE resource_id = 'second'`); err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("owner delete: %v %v", tag, err)
	}
	if tag, err := owner.Exec(t.Context(), `DELETE FROM iam_operations WHERE operation_id = 'op:fenced'`); err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("owner fenced delete: %v %v", tag, err)
	}
	if got := queryText(t, f.admin, `SELECT string_agg(resource_id, ',' ORDER BY seq) FROM iam_audit`); got != "first" {
		t.Fatalf("audit rows after owner and purge: %s", got)
	}
}

// TestAppendOnlyPostureLive is the sequence trap: once USAGE on iam_audit_seq
// is revoked from PUBLIC, a runtime role with table grants only cannot append,
// and the production constructor refuses it exactly as the migrator's
// preflight reports; with USAGE it boots. The owner is refused in production.
func TestAppendOnlyPostureLive(t *testing.T) {
	f := newImmutableFixture(t, false)
	tupleExec(t, f.admin, `REVOKE USAGE ON SEQUENCE iam_audit_seq FROM PUBLIC`)
	schema, err := pgxdb.NewSchema(f.schema)
	if err != nil {
		t.Fatal(err)
	}
	preflight := func() []pgxdb.TablePrivilege {
		t.Helper()
		report, err := pgxdb.InspectTablePrivileges(t.Context(), f.admin, f.role, AuditTables(WithAudit())...)
		if err != nil || report.Role != f.role || len(report.Tables) != 2 {
			t.Fatalf("preflight: %+v %v", report, err)
		}
		return report.Tables
	}
	tables := preflight()
	if !tables[0].CannotAppend() || tables[0].Writable() || tables[1].CannotAppend() || tables[1].Writable() {
		t.Fatalf("preflight before USAGE: %+v", tables)
	}
	runtime := f.runtime(t, environment.ModeProduction)
	for _, opts := range [][]Option{{WithAudit()}, {WithAudit(), WithSchema(schema)}} {
		_, err := Repositories(t.Context(), runtime, opts...)
		if !errors.Is(err, pgxdb.ErrAppendOnlyTableUnwritable) || errors.Is(err, pgxdb.ErrAppendOnlyTableWritable) || !strings.Contains(err.Error(), "iam_audit") {
			t.Fatalf("table grants only: %v; want ErrAppendOnlyTableUnwritable", err)
		}
	}
	if _, err := Repositories(t.Context(), runtime); err != nil {
		t.Fatalf("without WithAudit iam_audit is not probed: %v", err)
	}
	tupleExec(t, f.admin, `GRANT USAGE ON SEQUENCE iam_audit_seq TO "`+f.role+`"`)
	for _, p := range preflight() {
		if p.CannotAppend() || p.Writable() || p.ProtectedBy(false) != "trigger" || !p.ImmutableTrigger {
			t.Fatalf("preflight with USAGE: %+v", p)
		}
	}
	for _, opts := range [][]Option{{WithAudit()}, {WithAudit(), WithSchema(schema)}} {
		if _, err := Repositories(t.Context(), runtime, opts...); err != nil {
			t.Fatalf("runtime role with USAGE refused: %v", err)
		}
	}
	parsed, err := url.Parse(os.Getenv("POSTGRES_TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", f.schema)
	parsed.RawQuery = query.Encode()
	superuser, err := pgxdb.Open(t.Context(), pgxdb.Config{DSN: parsed.String(), MaxConns: 2, RuntimeMode: environment.ModeProduction, TLSDisabledAcknowledged: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { superuser.Close() })
	if _, err := Repositories(t.Context(), superuser, WithAudit()); !errors.Is(err, pgxdb.ErrAppendOnlyTableWritable) {
		t.Fatalf("superuser in production: %v; want ErrAppendOnlyTableWritable", err)
	}
}
