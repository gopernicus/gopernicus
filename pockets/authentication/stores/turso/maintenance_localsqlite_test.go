package turso

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/maintenance"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	listing "github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// The rail-purge file cases (SOC 2 S3), untagged so `make check` runs them:
// each opens its own SQLite file, so they prove the capture-drop-restore
// transaction against the local driver without a libSQL server. The gated
// server cases live in maintenance_integration_test.go.

// railDay is one calendar day.
const railDay = 24 * time.Hour

// errInjected is the fault the hook returns.
var errInjected = errors.New("injected fault")

// railLog is a WARN-and-above log sink safe for concurrent writers.
type railLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *railLog) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *railLog) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *railLog) logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(b, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

// captureRailDefaultLog routes slog.Default — where the store logs
// rail_triggers_changed — to a buffer for the rest of the test.
func captureRailDefaultLog(t *testing.T) *railLog {
	t.Helper()
	buf := &railLog{}
	prev := slog.Default()
	slog.SetDefault(buf.logger())
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

// railFile opens a fresh SQLite file under mode, migrated with fsys, and
// returns the database and its path.
func railFile(t *testing.T, fsys fs.FS, mode environment.Mode, logger *slog.Logger) (*tursodb.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rail.db")
	db := openRailFile(t, path, mode, logger)
	if err := tursodb.RunMigrations(t.Context(), db, fsys, MigrationsDir); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db, path
}

func openRailFile(t *testing.T, path string, mode environment.Mode, logger *slog.Logger) *tursodb.DB {
	t.Helper()
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	db, err := tursodb.Open(t.Context(), tursodb.Config{URL: sdk.Secret("file:" + path), RuntimeMode: mode, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// withoutMigration is the embedded tree with name removed — a ledger copy
// whose host declined that file.
func withoutMigration(t *testing.T, name string) fstest.MapFS {
	t.Helper()
	out := fstest.MapFS{}
	entries, err := fs.ReadDir(MigrationsFS, MigrationsDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() == name {
			continue
		}
		b, err := fs.ReadFile(MigrationsFS, MigrationsDir+"/"+e.Name())
		if err != nil {
			t.Fatal(err)
		}
		out[MigrationsDir+"/"+e.Name()] = &fstest.MapFile{Data: b}
	}
	return out
}

// railTriggerSQL reads the trigger set on security_events as sqlite_schema
// stores it, in name order.
func railTriggerSQL(t *testing.T, db *tursodb.DB) []string {
	t.Helper()
	triggers, err := railTriggers(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(triggers))
	for i, tr := range triggers {
		out[i] = tr.name + "\n" + tr.sql
	}
	return out
}

// seedRail appends n rows age old and one fresh row through repos.
func seedRail(t *testing.T, repos auth.Repositories, n int, age time.Duration) {
	t.Helper()
	at := time.Now().UTC().Add(-age)
	for i := range n {
		evt := securityevent.New(sdk.IDGenerator{}, securityevent.TypeLogin, securityevent.StatusSuccess, at.Add(time.Duration(i)*time.Second))
		evt.UserID = "u-old"
		if _, err := repos.SecurityEvents.Create(t.Context(), evt); err != nil {
			t.Fatal(err)
		}
	}
	fresh := securityevent.New(sdk.IDGenerator{}, securityevent.TypeLogin, securityevent.StatusSuccess, time.Now())
	if _, err := repos.SecurityEvents.Create(t.Context(), fresh); err != nil {
		t.Fatal(err)
	}
}

// railCount counts rail rows of eventType ("" for every row).
func railCount(t *testing.T, db *tursodb.DB, eventType string) int {
	t.Helper()
	q, args := `SELECT count(*) FROM security_events`, []any{}
	if eventType != "" {
		q, args = q+` WHERE event_type = ?`, append(args, eventType)
	}
	var n int
	if err := db.QueryRow(t.Context(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func mustRepos(t *testing.T, db *tursodb.DB) auth.Repositories {
	t.Helper()
	repos, err := Repositories(t.Context(), db)
	if err != nil {
		t.Fatalf("Repositories: %v", err)
	}
	return repos
}

// TestRailPurge_LocalSQLite_TriggersRestored proves the canonical case: a purge
// deletes the batch, restores the 0022 pair byte-identical, appends one
// evidence row with floor_days and no session_user, logs no ERROR, and a
// production boot afterwards logs zero WARNs.
func TestRailPurge_LocalSQLite_TriggersRestored(t *testing.T) {
	defaultLog := captureRailDefaultLog(t)
	bootLog := &railLog{}
	db, _ := railFile(t, MigrationsFS, environment.ModeProduction, bootLog.logger())
	repos := mustRepos(t, db)
	before := railTriggerSQL(t, db)
	if len(before) != 2 {
		t.Fatalf("0022 triggers = %d, want 2", len(before))
	}
	seedRail(t, repos, 5, 500*railDay)

	res, err := repos.Maintenance.PurgeSecurityEventsBefore(t.Context(), time.Now().Add(-400*railDay), 3)
	if err != nil || res.Count != 3 {
		t.Fatalf("purge = %+v, %v; want 3", res, err)
	}
	if after := railTriggerSQL(t, db); strings.Join(after, "\n--\n") != strings.Join(before, "\n--\n") {
		t.Fatalf("triggers after the purge:\n%v\nbefore:\n%v", after, before)
	}
	page, err := repos.SecurityEvents.List(t.Context(), securityevent.ListFilter{EventType: securityevent.TypeSecurityEventsPurged}, listRequest())
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("purge rows = %v, %v", page.Items, err)
	}
	d := page.Items[0].Details
	if page.Items[0].Actor != (securityevent.Principal{Type: "system", ID: "maintenance"}) || d["count"] != float64(3) || d["floor_days"] != float64(365) || d["max_rows"] != float64(3) {
		t.Errorf("purge row = %+v", page.Items[0])
	}
	if _, ok := d["session_user"]; ok {
		t.Error("the turso purge row names a session_user")
	}
	if _, err := tursodb.ExecAffecting(t.Context(), db, `DELETE FROM security_events`); !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("a DELETE after the purge = %v, want the restored trigger to refuse it", err)
	}
	if defaultLog.String() != "" {
		t.Fatalf("the purge logged:\n%s", defaultLog)
	}
	if _, err := Repositories(t.Context(), db); err != nil || bootLog.String() != "" {
		t.Fatalf("production boot after the purge: %v, logged:\n%s", err, bootLog)
	}
}

// TestRailPurge_LocalSQLite_Declined0022 proves a host that declined the
// trigger file keeps no trigger: the purge captures nothing and re-creates
// nothing.
func TestRailPurge_LocalSQLite_Declined0022(t *testing.T) {
	defaultLog := captureRailDefaultLog(t)
	db, _ := railFile(t, withoutMigration(t, immutableMigration), "", nil)
	repos := mustRepos(t, db)
	seedRail(t, repos, 2, 500*railDay)
	if res, err := repos.Maintenance.PurgeSecurityEventsBefore(t.Context(), time.Now().Add(-400*railDay), 10); err != nil || res.Count != 2 {
		t.Fatalf("purge = %+v, %v", res, err)
	}
	if got := railTriggerSQL(t, db); len(got) != 0 {
		t.Fatalf("a purge re-armed a declined trigger file: %v", got)
	}
	if railCount(t, db, securityevent.TypeSecurityEventsPurged) != 1 || defaultLog.String() != "" {
		t.Fatalf("purge rows = %d; logged %s", railCount(t, db, securityevent.TypeSecurityEventsPurged), defaultLog)
	}
}

// TestRailPurge_LocalSQLite_FaultInjection proves the transaction: a fault
// after the DROP TRIGGERs and a context cancelled after the evidence row,
// before COMMIT, each roll back to the byte-identical trigger set with every
// row in place and no evidence written.
func TestRailPurge_LocalSQLite_FaultInjection(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stage string
		fault func(cancel context.CancelFunc) error
		want  error
	}{
		{"ErrorAfterDrop", stageDropped, func(context.CancelFunc) error { return errInjected }, errInjected},
		{"CancelBeforeCommit", stageRecorded, func(cancel context.CancelFunc) error { cancel(); return nil }, context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, _ := railFile(t, MigrationsFS, "", nil)
			repos := mustRepos(t, db)
			seedRail(t, repos, 4, 500*railDay)
			before, rows := railTriggerSQL(t, db), railCount(t, db, "")
			store := NewMaintenanceStore(db)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			store.fault = func(_ context.Context, _ *tursodb.Tx, stage string) error {
				if stage == tc.stage {
					return tc.fault(cancel)
				}
				return nil
			}
			if _, err := store.PurgeSecurityEventsBefore(ctx, time.Now().Add(-400*railDay), 10); !errors.Is(err, tc.want) {
				t.Fatalf("purge = %v, want %v", err, tc.want)
			}
			if after := railTriggerSQL(t, db); strings.Join(after, "\n") != strings.Join(before, "\n") {
				t.Fatalf("triggers after the rollback:\n%v\nbefore:\n%v", after, before)
			}
			if got := railCount(t, db, ""); got != rows || railCount(t, db, securityevent.TypeSecurityEventsPurged) != 0 {
				t.Fatalf("rows after the rollback = %d (want %d), purge rows %d", got, rows, railCount(t, db, securityevent.TypeSecurityEventsPurged))
			}
		})
	}
}

// TestRailPurge_LocalSQLite_HostAfterInsertTrigger proves a host's own AFTER
// INSERT trigger on the rail is captured and restored byte-identical — and
// restored before the evidence row, so it fires for that row.
func TestRailPurge_LocalSQLite_HostAfterInsertTrigger(t *testing.T) {
	db, _ := railFile(t, MigrationsFS, "", nil)
	for _, stmt := range []string{
		`CREATE TABLE host_mirror (id TEXT NOT NULL, event_type TEXT NOT NULL)`,
		`CREATE TRIGGER Host_Mirror AFTER INSERT ON Security_Events
BEGIN
  INSERT INTO host_mirror (id, event_type) VALUES (NEW.id, NEW.event_type);
END`,
	} {
		if _, err := db.Exec(t.Context(), stmt); err != nil {
			t.Fatal(err)
		}
	}
	repos := mustRepos(t, db)
	seedRail(t, repos, 2, 500*railDay)
	before := railTriggerSQL(t, db)
	if len(before) != 3 {
		t.Fatalf("captured %d triggers, want the 0022 pair and the host's (tbl_name matched without case)", len(before))
	}
	if _, err := repos.Maintenance.PurgeSecurityEventsBefore(t.Context(), time.Now().Add(-400*railDay), 10); err != nil {
		t.Fatal(err)
	}
	if after := railTriggerSQL(t, db); strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Fatalf("triggers after the purge:\n%v\nbefore:\n%v", after, before)
	}
	var mirrored int
	if err := db.QueryRow(t.Context(), `SELECT count(*) FROM host_mirror WHERE event_type = ?`, securityevent.TypeSecurityEventsPurged).Scan(&mirrored); err != nil || mirrored != 1 {
		t.Fatalf("the host trigger saw %d purge rows (%v), want 1", mirrored, err)
	}
}

// TestRailPurge_LocalSQLite_TriggerSetChanged proves the post-commit check: a
// trigger that appears inside the purge transaction, outside the capture, is
// reported as ERROR rail_triggers_changed once the purge commits.
func TestRailPurge_LocalSQLite_TriggerSetChanged(t *testing.T) {
	defaultLog := captureRailDefaultLog(t)
	db, _ := railFile(t, MigrationsFS, "", nil)
	repos := mustRepos(t, db)
	seedRail(t, repos, 1, 500*railDay)
	store := NewMaintenanceStore(db)
	store.fault = func(ctx context.Context, tx *tursodb.Tx, stage string) error {
		if stage != stageRecorded {
			return nil
		}
		_, err := tx.Exec(ctx, `CREATE TRIGGER rail_sneak AFTER INSERT ON security_events BEGIN SELECT 1; END`)
		return err
	}
	if res, err := store.PurgeSecurityEventsBefore(t.Context(), time.Now().Add(-400*railDay), 10); err != nil || res.Count != 1 {
		t.Fatalf("purge = %+v, %v", res, err)
	}
	if !strings.Contains(defaultLog.String(), "level=ERROR") || !strings.Contains(defaultLog.String(), "error_kind="+railTriggersChangedKey) ||
		!strings.Contains(defaultLog.String(), "rail_sneak") {
		t.Fatalf("a changed trigger set logged:\n%s", defaultLog)
	}
}

// TestRailPurge_LocalSQLite_Floor proves the floor row: a missing row or table
// refuses construction and every purge naming 0024 — never a default of zero;
// floor_days is read from the row (a raised floor refuses a cutoff the default
// would admit); the CHECK refuses floor_days below 1; and re-running 0024
// keeps the existing row.
func TestRailPurge_LocalSQLite_Floor(t *testing.T) {
	ctx := t.Context()
	db, _ := railFile(t, MigrationsFS, "", nil)
	repos := mustRepos(t, db)
	store := NewMaintenanceStore(db)
	if _, err := db.Exec(ctx, `UPDATE `+retentionFloorTable+` SET floor_days = 400 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PurgeSecurityEventsBefore(ctx, time.Now().Add(-366*railDay), 10); !errors.Is(err, maintenance.ErrBelowRetentionFloor) {
		t.Fatalf("366 days under a 400-day floor = %v, want ErrBelowRetentionFloor", err)
	}
	if _, err := repos.Maintenance.PurgeSecurityEventsBefore(ctx, time.Now().Add(-401*railDay), 10); err != nil {
		t.Fatalf("401 days under a 400-day floor: %v", err)
	}
	if _, err := db.Exec(ctx, `UPDATE `+retentionFloorTable+` SET floor_days = 0 WHERE id = 1`); err == nil {
		t.Fatal("floor_days = 0 was accepted")
	}
	if err := execMigration(ctx, db, railRetentionFile); err != nil {
		t.Fatalf("re-run 0024: %v", err)
	}
	var days int
	if err := db.QueryRow(ctx, `SELECT floor_days FROM `+retentionFloorTable+` WHERE id = 1`).Scan(&days); err != nil || days != 400 {
		t.Fatalf("floor_days after re-running 0024 = %d, %v; want the existing 400", days, err)
	}

	for _, drop := range []string{`DELETE FROM ` + retentionFloorTable, `DROP TABLE ` + retentionFloorTable} {
		if _, err := db.Exec(ctx, drop); err != nil {
			t.Fatal(err)
		}
		if _, err := Repositories(ctx, db); !errors.Is(err, sdk.ErrNotFound) || !strings.Contains(err.Error(), railRetentionFile) {
			t.Fatalf("Repositories after %q = %v, want ErrNotFound naming %s", drop, err, railRetentionFile)
		}
		_, err := store.PurgeSecurityEventsBefore(ctx, time.Now().Add(-500*railDay), 10)
		if !errors.Is(err, sdk.ErrNotFound) || !strings.Contains(err.Error(), railRetentionFile) {
			t.Fatalf("purge after %q = %v, want ErrNotFound naming %s", drop, err, railRetentionFile)
		}
	}
	if err := execMigration(ctx, db, railRetentionFile); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `SELECT floor_days FROM `+retentionFloorTable+` WHERE id = 1`).Scan(&days); err != nil || days != 365 {
		t.Fatalf("floor_days after re-creating the table = %d, %v; want 365", days, err)
	}
	if _, err := Repositories(ctx, db); err != nil {
		t.Fatal(err)
	}
}

// TestRailPurge_LocalSQLite_ConcurrentReader proves a purge commits while
// another connection holds a WAL read snapshot of the rail: the reader keeps
// its snapshot and a later read sees the purge.
func TestRailPurge_LocalSQLite_ConcurrentReader(t *testing.T) {
	ctx := t.Context()
	db, path := railFile(t, MigrationsFS, "", nil)
	repos := mustRepos(t, db)
	seedRail(t, repos, 3, 500*railDay)
	reader := openRailFile(t, path, "", nil)
	tx, err := reader.BeginRead(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var seen int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM security_events`).Scan(&seen); err != nil || seen != 4 {
		t.Fatalf("reader snapshot = %d, %v", seen, err)
	}
	if res, err := repos.Maintenance.PurgeSecurityEventsBefore(ctx, time.Now().Add(-400*railDay), 10); err != nil || res.Count != 3 {
		t.Fatalf("purge beside an open reader = %+v, %v", res, err)
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM security_events`).Scan(&seen); err != nil || seen != 4 {
		t.Fatalf("the reader's snapshot moved: %d, %v", seen, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got := railCount(t, reader, ""); got != 2 {
		t.Fatalf("rows after the purge = %d, want the fresh row and the evidence row", got)
	}
}

func listRequest() listing.Request { return listing.Request{Limit: 100} }
