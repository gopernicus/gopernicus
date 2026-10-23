package turso

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

const immutableMigration = "0006_iam_audit_immutable.sql"

// auditTriggerNames lists every immutability trigger migration 0006 installs.
func auditTriggerNames() []string {
	var names []string
	for _, table := range AuditTables(WithAudit()) {
		for _, trigger := range table.Triggers {
			names = append(names, trigger.Name)
		}
	}
	return names
}

// dropAuditTriggers removes the immutability triggers so a test fixture can
// reset or rewrite audit rows; restoreAuditTriggers re-executes the embedded
// 0006, which drops and re-creates them. SQLite has no owner to exempt.
func dropAuditTriggers(t testing.TB, db *tursodb.DB) {
	t.Helper()
	for _, name := range auditTriggerNames() {
		if _, err := db.Exec(context.Background(), "DROP TRIGGER IF EXISTS main."+name); err != nil {
			t.Fatalf("drop trigger %s: %v", name, err)
		}
	}
}

func restoreAuditTriggers(t testing.TB, db *tursodb.DB) {
	t.Helper()
	data, err := MigrationsFS.ReadFile(MigrationsDir + "/" + immutableMigration)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.InTx(context.Background(), func(tx *tursodb.Tx) error {
		_, err := tx.Exec(context.Background(), string(data))
		return err
	}); err != nil {
		t.Fatalf("restore audit triggers: %v", err)
	}
}

// TestAuditTablesMatchMigration0006 keeps the probe's canonical trigger text and
// the shipped migration identical: SQLite stores each CREATE TRIGGER without
// the main. qualifier and the final semicolon.
func TestAuditTablesMatchMigration0006(t *testing.T) {
	data, err := MigrationsFS.ReadFile(MigrationsDir + "/" + immutableMigration)
	if err != nil {
		t.Fatal(err)
	}
	stored := strings.ReplaceAll(string(data), " main.", " ")
	if n := strings.Count(stored, "CREATE TRIGGER "); n != 4 {
		t.Fatalf("0006 creates %d triggers, want 4", n)
	}
	tables := AuditTables(WithAudit())
	if len(tables) != 2 || tables[0].Name != "iam_audit" || tables[1].Name != "iam_operations" {
		t.Fatalf("audit tables: %+v", tables)
	}
	for _, table := range tables {
		for _, trigger := range table.Triggers {
			if len(trigger.Canonical) != 1 {
				t.Fatalf("%s: %d canonical statements", trigger.Name, len(trigger.Canonical))
			}
			statement := "DROP TRIGGER IF EXISTS " + trigger.Name + ";\n" + trigger.Canonical[0].SQL + ";\n"
			if !strings.Contains(stored, statement) || !strings.HasPrefix(trigger.Canonical[0].SQL, "CREATE TRIGGER "+trigger.Name+" ") {
				t.Errorf("%s drifted from 0006:\n%s", trigger.Name, trigger.Canonical[0].SQL)
			}
		}
	}
	if plain := AuditTables(); len(plain) != 1 || plain[0].Name != "iam_operations" {
		t.Fatalf("AuditTables without WithAudit: %+v", plain)
	}
}

// assertAuditImmutable proves the 0006 triggers on a migrated database:
// iam_audit refuses UPDATE and DELETE; iam_operations refuses UPDATE and the
// DELETE of a fenced row, and admits deleting any other row. recorded_at is
// stamped by the database on every row the store writes.
func assertAuditImmutable(t *testing.T, db *tursodb.DB) {
	t.Helper()
	ctx := audit.WithSource(t.Context(), audit.Source{System: "immutability-test"})
	repos, err := testRepositories(ctx, db, WithAudit())
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"first", "second"} {
		if i > 0 {
			time.Sleep(5 * time.Millisecond)
		}
		cmd := mutations.Command{Target: mutations.Target{Kind: mutations.TargetResource, Type: "doc", ID: id}, Operation: mutations.OpGrant, Relationships: []mutations.RelationshipRow{{Relation: "viewer", Subject: relationships.SubjectRef{Type: "user", ID: "alice"}}}, OperationID: "op:" + id}
		if _, err := repos.Mutations.Apply(ctx, cmd, nil); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := repos.Operations.Fence(ctx, "op:fenced"); err != nil || got.Outcome != mutations.OutcomeFenced {
		t.Fatalf("fence: %+v %v", got, err)
	}
	// resource_id orders "first" before "second", the write order.
	stamps, err := queryStrings(ctx, db, `SELECT coalesce(recorded_at, 'NULL') FROM main.iam_audit ORDER BY resource_id`)
	if err != nil || len(stamps) != 2 || stamps[0] == "NULL" || stamps[0] >= stamps[1] {
		t.Fatalf("recorded_at not stamped in write order: %v %v", stamps, err)
	}
	if stamps, err := queryStrings(ctx, db, `SELECT coalesce(recorded_at, 'NULL') FROM main.iam_operations`); err != nil || len(stamps) != 3 || strings.Contains(strings.Join(stamps, ","), "NULL") {
		t.Fatalf("operation recorded_at: %v %v", stamps, err)
	}
	for _, statement := range []string{
		`UPDATE main.iam_audit SET reason = 'rewritten'`,
		`DELETE FROM main.iam_audit WHERE resource_id = 'first'`,
		`DELETE FROM main.iam_audit`,
		`UPDATE main.iam_operations SET committed_at = committed_at WHERE operation_id = 'op:first'`,
		`DELETE FROM main.iam_operations WHERE operation_id = 'op:fenced'`,
	} {
		if _, err := db.Exec(ctx, statement); !errors.Is(err, sdk.ErrForbidden) {
			t.Fatalf("%s: %v; want sdk.ErrForbidden", statement, err)
		}
	}
	if n, err := queryStrings(ctx, db, `SELECT count(*) || '/' || sum(reason = '') FROM main.iam_audit`); err != nil || len(n) != 1 || n[0] != "2/2" {
		t.Fatalf("audit rows changed: %v %v", n, err)
	}
	if _, err := db.Exec(ctx, `DELETE FROM main.iam_operations WHERE operation_id = 'op:first'`); err != nil {
		t.Fatalf("non-fenced operation delete refused: %v", err)
	}
	if ids, err := queryStrings(ctx, db, `SELECT operation_id FROM main.iam_operations ORDER BY operation_id`); err != nil || strings.Join(ids, ",") != "op:fenced,op:second" {
		t.Fatalf("operations after delete: %v %v", ids, err)
	}
}

func TestAuditTriggersRefuseRewrites(t *testing.T) {
	assertAuditImmutable(t, canonicalFixture(t, false))
}

// TestAuditTriggerProbeWarnsNeverRefuses opens the database in production: an
// intact trigger set is silent, a missing trigger is one WARN naming it, and
// construction never fails for it.
func TestAuditTriggerProbeWarnsNeverRefuses(t *testing.T) {
	var logs bytes.Buffer
	db, err := tursodb.Open(t.Context(), tursodb.Config{URL: "file:" + filepath.Join(t.TempDir(), "probe.sqlite"), RuntimeMode: environment.ModeProduction, Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := tursodb.RunMigrations(t.Context(), db, MigrationsFS, MigrationsDir); err != nil {
		t.Fatal(err)
	}
	logs.Reset()
	if _, err := testRepositories(t.Context(), db, WithAudit()); err != nil || strings.Contains(logs.String(), "immutability") {
		t.Fatalf("intact triggers: %v %s", err, logs.String())
	}
	if _, err := db.Exec(t.Context(), `DROP TRIGGER main.iam_audit_immutable_delete`); err != nil {
		t.Fatal(err)
	}
	if _, err := testRepositories(t.Context(), db); err != nil || strings.Contains(logs.String(), "immutability") {
		t.Fatalf("iam_audit probed without WithAudit: %v %s", err, logs.String())
	}
	if _, err := testRepositories(t.Context(), db, WithAudit()); err != nil {
		t.Fatalf("missing trigger refused construction: %v", err)
	}
	got := logs.String()
	if strings.Count(got, "level=WARN") != 1 || !strings.Contains(got, "audit table has no immutability triggers") || !strings.Contains(got, "trigger=iam_audit_immutable_delete") || !strings.Contains(got, "reason=absent") {
		t.Fatalf("missing trigger warning: %s", got)
	}
	restoreAuditTriggers(t, db)
	logs.Reset()
	if _, err := testRepositories(t.Context(), db, WithAudit()); err != nil || logs.Len() != 0 {
		t.Fatalf("restored triggers: %v %s", err, logs.String())
	}
}
