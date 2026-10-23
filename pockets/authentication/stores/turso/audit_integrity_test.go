package turso

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// immutableMigration is the trigger migration AuditTables verifies against.
const immutableMigration = "0022_security_events_immutable.sql"

// TestCanonicalTriggersMatchMigration pins the trigger probe's canonical text to
// the embedded 0022: each trigger is dropped first, then created exactly as the
// constant spells it, so re-running the file is safe and the two cannot drift.
func TestCanonicalTriggersMatchMigration(t *testing.T) {
	b, err := fs.ReadFile(MigrationsFS, MigrationsDir+"/"+immutableMigration)
	if err != nil {
		t.Fatalf("read %s: %v", immutableMigration, err)
	}
	sql := string(b)
	want := "DROP TRIGGER IF EXISTS " + securityEventsImmutableUpdate + ";\n" + securityEventsImmutableUpdateSQL + "\n" +
		"DROP TRIGGER IF EXISTS " + securityEventsImmutableDelete + ";\n" + securityEventsImmutableDeleteSQL + "\n"
	if !strings.HasSuffix(sql, want) {
		t.Fatalf("%s statements differ from the canonical triggers:\nfile:\n%s\nwant suffix:\n%s", immutableMigration, sql, want)
	}
	for _, line := range strings.Split(strings.TrimSuffix(sql, want), "\n") {
		if line != "" && !strings.HasPrefix(line, "--") {
			t.Fatalf("%s carries a statement besides the trigger pair: %q", immutableMigration, line)
		}
	}
	tables := AuditTables()
	if len(tables) != 1 || tables[0].Name != "security_events" || len(tables[0].Triggers) != 2 {
		t.Fatalf("AuditTables() = %+v", tables)
	}
	for i, tr := range []struct{ name, sql string }{
		{securityEventsImmutableUpdate, securityEventsImmutableUpdateSQL},
		{securityEventsImmutableDelete, securityEventsImmutableDeleteSQL},
	} {
		got := tables[0].Triggers[i]
		if got.Name != tr.name || len(got.Canonical) != 1 || got.Canonical[0].SQL != tr.sql {
			t.Errorf("trigger %d = %+v, want %s", i, got, tr.name)
		}
	}
}

// integrityTarget is one database the integrity cases run against — a local
// file, or the live server: open connects under a RuntimeMode, reset empties
// it of the auth schema and its ledger.
type integrityTarget struct {
	open  func(t *testing.T, mode environment.Mode, logger *slog.Logger) *tursodb.DB
	reset func(t *testing.T, db *tursodb.DB)
}

// migrationsThrough is the embedded stream up to and including last.
func migrationsThrough(t *testing.T, last string) fstest.MapFS {
	t.Helper()
	prior := fstest.MapFS{}
	entries, err := fs.ReadDir(MigrationsFS, MigrationsDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || e.Name() > last {
			continue
		}
		data, err := fs.ReadFile(MigrationsFS, MigrationsDir+"/"+e.Name())
		if err != nil {
			t.Fatal(err)
		}
		prior[MigrationsDir+"/"+e.Name()] = &fstest.MapFile{Data: data}
	}
	return prior
}

// runSecurityEventsIntegrity proves migrations 0021 and 0022 on target: the
// rebuild keeps a pre-0021 row with NULL recorded_at, stamps new rows with the
// database clock and keeps the indexes; the triggers refuse UPDATE and DELETE;
// and the boot probe is silent over intact triggers and WARNs when one is
// missing.
func runSecurityEventsIntegrity(t *testing.T, target integrityTarget) {
	ctx := context.Background()
	db := target.open(t, "", nil)
	target.reset(t, db)
	t.Cleanup(func() { target.reset(t, db) })

	if err := tursodb.RunMigrations(ctx, db, migrationsThrough(t, "0020_external_subjects.sql"), MigrationsDir); err != nil {
		t.Fatal(err)
	}
	if _, err := Repositories(ctx, db); !errors.Is(err, sdk.ErrNotFound) || !strings.Contains(err.Error(), "0021_security_events_integrity.sql") {
		t.Fatalf("boot before 0021 = %v, want ErrNotFound naming 0021", err)
	}
	legacyAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	if _, err := db.Exec(ctx, `INSERT INTO security_events (id, user_id, event_type, event_status, created_at) VALUES ('legacy-row', 'u-legacy', 'login', 'success', ?)`, tursodb.FormatTime(legacyAt)); err != nil {
		t.Fatal(err)
	}
	if err := tursodb.RunMigrations(ctx, db, MigrationsFS, MigrationsDir); err != nil {
		t.Fatal(err)
	}

	repos, err := Repositories(ctx, db)
	if err != nil {
		t.Fatalf("boot after 0022: %v", err)
	}
	evt := securityevent.New(sdk.IDGenerator{}, securityevent.TypeLogin, securityevent.StatusSuccess, time.Now())
	evt.UserID, evt.RequestID, evt.TraceID = "u-new", "req-new", "trace-new"
	created, err := repos.SecurityEvents.Create(ctx, evt)
	if err != nil {
		t.Fatal(err)
	}

	recordedAt := func(id string) *string {
		t.Helper()
		var at *string
		if err := db.QueryRow(ctx, `SELECT recorded_at FROM security_events WHERE id = ?`, id).Scan(&at); err != nil {
			t.Fatal(err)
		}
		return at
	}
	if at := recordedAt("legacy-row"); at != nil {
		t.Fatalf("pre-0021 row recorded_at = %q, want NULL", *at)
	}
	if at := recordedAt(created.ID); at == nil || *at == "" {
		t.Fatal("a row inserted after 0021 has no recorded_at")
	}
	page, err := repos.SecurityEvents.List(ctx, securityevent.ListFilter{}, list.Request{Limit: 10})
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("List = %+v, %v", page.Items, err)
	}
	if got := page.Items[0]; got.ID != created.ID || got.RequestID != "req-new" || got.TraceID != "trace-new" {
		t.Fatalf("new row = %+v", got)
	}
	if got := page.Items[1]; got.ID != "legacy-row" || got.RequestID != "" || got.TraceID != "" || !got.CreatedAt.Equal(legacyAt) {
		t.Fatalf("legacy row = %+v", got)
	}

	rows, err := db.Query(ctx, `SELECT name FROM sqlite_schema WHERE type = 'index' AND tbl_name = 'security_events' AND sql IS NOT NULL`)
	if err != nil {
		t.Fatal(err)
	}
	indexes := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		indexes[name] = true
	}
	rows.Close()
	for _, want := range []string{"idx_security_events_created_at_id", "idx_security_events_user_id", "idx_security_events_event_type", "idx_security_events_event_status"} {
		if !indexes[want] {
			t.Errorf("index %s missing after the rebuild (have %v)", want, indexes)
		}
	}

	if _, err := db.Exec(ctx, `UPDATE security_events SET user_agent = 'rewritten' WHERE id = ?`, created.ID); !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("UPDATE = %v, want sdk.ErrForbidden", err)
	}
	if _, err := db.Exec(ctx, `DELETE FROM security_events WHERE id = ?`, created.ID); !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("DELETE = %v, want sdk.ErrForbidden", err)
	}
	var n int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM security_events`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("rows after refused rewrites = %d, %v; want 2", n, err)
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	production := target.open(t, environment.ModeProduction, logger)
	buf.Reset()
	if _, err := Repositories(ctx, production); err != nil {
		t.Fatalf("production boot over intact triggers: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("production boot over intact triggers logged:\n%s", buf.String())
	}
	if _, err := db.Exec(ctx, `DROP TRIGGER `+securityEventsImmutableDelete); err != nil {
		t.Fatal(err)
	}
	if _, err := Repositories(ctx, production); err != nil {
		t.Fatalf("production boot without a trigger must not refuse: %v", err)
	}
	for _, want := range []string{"level=WARN", "table=security_events", "trigger=" + securityEventsImmutableDelete, "reason=absent"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("missing-trigger WARN lacks %q:\n%s", want, buf.String())
		}
	}
	if err := execMigration(ctx, db, immutableMigration); err != nil {
		t.Fatalf("re-run 0022: %v", err)
	}
	buf.Reset()
	if _, err := Repositories(ctx, production); err != nil || buf.Len() != 0 {
		t.Fatalf("after re-running 0022: %v, logged %q", err, buf.String())
	}
}

// execMigration executes one embedded migration file outside the ledger.
func execMigration(ctx context.Context, db *tursodb.DB, name string) error {
	b, err := fs.ReadFile(MigrationsFS, MigrationsDir+"/"+name)
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, string(b))
	return err
}

// TestSecurityEventsIntegrity_LocalSQLite runs the integrity cases against a
// local file database.
func TestSecurityEventsIntegrity_LocalSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "integrity.db")
	runSecurityEventsIntegrity(t, integrityTarget{
		open: func(t *testing.T, mode environment.Mode, logger *slog.Logger) *tursodb.DB {
			db, err := tursodb.Open(t.Context(), tursodb.Config{URL: "file:" + path, RuntimeMode: mode, Logger: logger})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			return db
		},
		reset: func(*testing.T, *tursodb.DB) {},
	})
}
