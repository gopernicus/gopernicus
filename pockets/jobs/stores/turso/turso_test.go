package turso

import (
	"bytes"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	_ "github.com/gopernicus/gopernicus/integrations/datastores/turso/localfile"
	"github.com/gopernicus/gopernicus/pockets/jobs"
	"github.com/gopernicus/gopernicus/sdk"
)

// TestExportMigrations verifies the scaffold step copies every canonical
// migration file into the target dir verbatim — the host then owns the copies.
func TestExportMigrations(t *testing.T) {
	dst := t.TempDir()
	if err := ExportMigrations(dst); err != nil {
		t.Fatalf("ExportMigrations: %v", err)
	}

	srcEntries, err := fs.ReadDir(MigrationsFS, MigrationsDir)
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	if len(srcEntries) == 0 {
		t.Fatal("no embedded migrations found")
	}

	for _, e := range srcEntries {
		if e.IsDir() {
			continue
		}
		want, err := fs.ReadFile(MigrationsFS, MigrationsDir+"/"+e.Name())
		if err != nil {
			t.Fatalf("read embedded %s: %v", e.Name(), err)
		}
		got, err := os.ReadFile(filepath.Join(dst, e.Name()))
		if err != nil {
			t.Fatalf("read exported %s: %v", e.Name(), err)
		}
		if string(got) != string(want) {
			t.Errorf("exported %s differs from canonical source", e.Name())
		}
	}
}

// openLocalFile opens a fresh local SQLite file through the pure-Go localfile
// driver, optionally applying the canonical migrations.
func openLocalFile(t *testing.T, migrate bool) *tursodb.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "jobs.db")
	db, err := tursodb.Open(t.Context(), tursodb.Config{URL: sdk.Secret("file:" + path)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if migrate {
		if err := tursodb.RunMigrations(t.Context(), db, MigrationsFS, MigrationsDir); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}
	return db
}

// captureDefaultLog swaps slog.Default for a buffer for the test's duration.
func captureDefaultLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// TestRepositoriesFillsFencedQueue pins that Repositories fills the fenced port
// with this package's store over the same database.
func TestRepositoriesFillsFencedQueue(t *testing.T) {
	db := &tursodb.DB{}
	repos := Repositories(db)
	fenced, ok := repos.FencedQueue.(*FencedQueue)
	if !ok || fenced == nil {
		t.Fatalf("Repositories FencedQueue = %T; want non-nil *FencedQueue", repos.FencedQueue)
	}
	if fenced.db != db {
		t.Error("FencedQueue did not retain the supplied database")
	}
}

// TestRepositoriesFencedQueue_LocalFile pins that jobs.New over Repositories
// boots with the fenced queue: a keyed enqueue lands in fenced_job_queue and a
// second admission of the same key returns the active generation.
func TestRepositoriesFencedQueue_LocalFile(t *testing.T) {
	db := openLocalFile(t, true)
	ctx := t.Context()
	parts, err := jobs.New(Repositories(db))
	if err != nil {
		t.Fatalf("jobs.New: %v", err)
	}
	first, err := parts.Queue.EnqueueOnce(ctx, "email", "repos-fenced-key", []byte(`{}`))
	if err != nil {
		t.Fatalf("EnqueueOnce through Repositories: %v", err)
	}
	again, err := parts.Queue.EnqueueOnce(ctx, "email", "repos-fenced-key", []byte(`{}`))
	if err != nil {
		t.Fatalf("second EnqueueOnce: %v", err)
	}
	if again != first {
		t.Errorf("second EnqueueOnce = %q; want the active generation %q", again, first)
	}
	var n int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM fenced_job_queue WHERE logical_key = ?", "repos-fenced-key").Scan(&n); err != nil {
		t.Fatalf("count fenced rows: %v", err)
	}
	if n != 1 {
		t.Errorf("fenced_job_queue rows for the key = %d; want 1", n)
	}
}

func TestStatusCheckRejectsNilDatabase(t *testing.T) {
	if err := StatusCheck(t.Context(), nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("StatusCheck nil database: %v", err)
	}
}

// TestStatusCheck_LocalFile pins the boot gate against a real SQLite file: a
// migrated ledger passes silently, an unmigrated one and one missing any single
// jobs table (the skipped-0003 host) fail with sdk.ErrNotFound naming the table,
// and a missing retention index only WARNs.
func TestStatusCheck_LocalFile(t *testing.T) {
	t.Run("migrated", func(t *testing.T) {
		db := openLocalFile(t, true)
		logs := captureDefaultLog(t)
		if err := StatusCheck(t.Context(), db); err != nil {
			t.Fatalf("StatusCheck on a migrated ledger: %v", err)
		}
		if logs.Len() != 0 {
			t.Errorf("StatusCheck on a migrated ledger logged: %s", logs)
		}
	})
	t.Run("unmigrated", func(t *testing.T) {
		err := StatusCheck(t.Context(), openLocalFile(t, false))
		if !errors.Is(err, sdk.ErrNotFound) || !strings.Contains(err.Error(), "job_queue table missing") {
			t.Fatalf("StatusCheck on an unmigrated ledger = %v; want sdk.ErrNotFound naming job_queue", err)
		}
	})
	// Literal, not probeTables: a table dropped from the inventory must fail here.
	for _, table := range []string{"job_queue", "job_schedules", "fenced_job_queue", "job_schedule_occurrences"} {
		t.Run("missing "+table, func(t *testing.T) {
			db := openLocalFile(t, true)
			if _, err := db.Exec(t.Context(), "DROP TABLE "+table); err != nil {
				t.Fatalf("drop %s: %v", table, err)
			}
			err := StatusCheck(t.Context(), db)
			if !errors.Is(err, sdk.ErrNotFound) {
				t.Fatalf("StatusCheck without %s = %v; want sdk.ErrNotFound", table, err)
			}
			if !strings.Contains(err.Error(), table+" table missing") || !strings.Contains(err.Error(), `"jobs" migration source`) {
				t.Errorf("StatusCheck error %q does not name %s and the jobs migration source", err, table)
			}
		})
	}
	t.Run("missing retention index", func(t *testing.T) {
		db := openLocalFile(t, true)
		if _, err := db.Exec(t.Context(), "DROP INDEX "+terminalPurgeIndex); err != nil {
			t.Fatalf("drop index: %v", err)
		}
		logs := captureDefaultLog(t)
		if err := StatusCheck(t.Context(), db); err != nil {
			t.Fatalf("StatusCheck without the retention index = %v; want nil", err)
		}
		if out := logs.String(); !strings.Contains(out, "level=WARN") || !strings.Contains(out, terminalPurgeIndex) || !strings.Contains(out, terminalPurgeMigration) {
			t.Errorf("StatusCheck without the retention index logged %q; want a WARN naming the index and file", out)
		}
	})
}
