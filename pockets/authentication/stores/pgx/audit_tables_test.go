package pgx

import (
	"io/fs"
	"strings"
	"testing"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
)

// immutableMigration is the trigger migration AuditTables verifies against.
const immutableMigration = "0022_security_events_immutable.sql"

// TestCanonicalTriggerBodyMatchesMigration pins the posture probe's canonical
// body to the text between 0022's dollar quotes — exactly what the catalog's
// prosrc holds once the file is applied — so the two cannot drift.
func TestCanonicalTriggerBodyMatchesMigration(t *testing.T) {
	b, err := fs.ReadFile(MigrationsFS, MigrationsDir+"/"+immutableMigration)
	if err != nil {
		t.Fatalf("read %s: %v", immutableMigration, err)
	}
	sql := string(b)
	if !strings.HasPrefix(sql, "SET LOCAL lock_timeout = '5s';\n") {
		t.Errorf("%s must begin with SET LOCAL lock_timeout", immutableMigration)
	}
	tag := "$" + securityEventsRefuseWrite + "$"
	parts := strings.Split(sql, tag)
	if len(parts) != 3 {
		t.Fatalf("%s carries %d %s quotes, want 2", immutableMigration, len(parts)-1, tag)
	}
	if parts[1] != securityEventsRefuseWriteBody {
		t.Fatalf("0022 body differs from securityEventsRefuseWriteBody:\nfile:\n%q\nconst:\n%q", parts[1], securityEventsRefuseWriteBody)
	}
	for _, want := range []string{
		"CREATE OR REPLACE FUNCTION " + securityEventsRefuseWrite + "() RETURNS trigger LANGUAGE plpgsql SET search_path = pg_catalog, pg_temp AS " + tag,
		"CREATE OR REPLACE TRIGGER security_events_immutable BEFORE UPDATE OR DELETE ON security_events FOR EACH ROW EXECUTE FUNCTION " + securityEventsRefuseWrite + "();",
		"CREATE OR REPLACE TRIGGER security_events_no_truncate BEFORE TRUNCATE ON security_events FOR EACH STATEMENT EXECUTE FUNCTION " + securityEventsRefuseWrite + "();",
		"ALTER TABLE security_events ENABLE ALWAYS TRIGGER security_events_immutable;",
		"ALTER TABLE security_events ENABLE ALWAYS TRIGGER security_events_no_truncate;",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("%s lacks %q", immutableMigration, want)
		}
	}
}

// TestIntegrityMigrationShape pins 0021's load-bearing statements: the lock
// timeout first, nullable evidence columns defaulted after the add, the
// sequence owned by its column, and the closing USAGE grant.
func TestIntegrityMigrationShape(t *testing.T) {
	b, err := fs.ReadFile(MigrationsFS, MigrationsDir+"/0021_security_events_integrity.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(b)
	if !strings.HasPrefix(sql, "SET LOCAL lock_timeout = '5s';\n") {
		t.Error("0021 must begin with SET LOCAL lock_timeout")
	}
	if !strings.HasSuffix(sql, "GRANT USAGE ON SEQUENCE security_events_seq TO PUBLIC;\n") {
		t.Error("0021 must end with the sequence USAGE grant")
	}
	for _, want := range []string{
		"ADD COLUMN request_id TEXT NOT NULL DEFAULT ''",
		"ADD COLUMN trace_id TEXT NOT NULL DEFAULT ''",
		"ADD COLUMN seq BIGINT NULL",
		"ADD COLUMN recorded_at TIMESTAMPTZ NULL",
		"CREATE SEQUENCE security_events_seq;",
		"ALTER SEQUENCE security_events_seq OWNED BY security_events.seq;",
		"ALTER COLUMN seq SET DEFAULT nextval('security_events_seq')",
		"ALTER COLUMN recorded_at SET DEFAULT now()",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("0021 lacks %q", want)
		}
	}
	for _, forbidden := range []string{"CONSTRAINT", "INDEX", "UNIQUE"} {
		if strings.Contains(strings.ToUpper(sql), forbidden) {
			t.Errorf("0021 adds %s; it must add none", forbidden)
		}
	}
}

// TestAuditTables pins the posture spec: security_events under the store's
// schema, verified against the 0022 function.
func TestAuditTables(t *testing.T) {
	bare := AuditTables()
	if len(bare) != 1 || bare[0].Name != securityEventsTable || bare[0].AllowDelete {
		t.Fatalf("AuditTables() = %+v", bare)
	}
	if got := bare[0].Canonical; len(got) != 1 || got[0] != (pgxdb.TriggerBody{Function: securityEventsRefuseWrite, Body: securityEventsRefuseWriteBody}) {
		t.Fatalf("canonical = %+v", got)
	}
	s, err := pgxdb.NewSchema("auth_x")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := AuditTables(WithSchema(s))[0].Name, `"auth_x".`+securityEventsTable; got != want {
		t.Fatalf("AuditTables(WithSchema) name = %q, want %q", got, want)
	}
}
