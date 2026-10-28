package turso

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk/pkg/workers"
)

type recordingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

// TestTupleSourceRecreatedDatabaseStopsTheRelay recreates the local file's
// schema under a live source: every table, the migration ledger included.
func TestTupleSourceRecreatedDatabaseStopsTheRelay(t *testing.T) {
	ctx := t.Context()
	db, cfg := cacheFixture(t, true)
	repos, err := Repositories(ctx, db, cacheOptions(cfg)...)
	if err != nil {
		t.Fatal(err)
	}
	cache, err := tuplecache.New(repos.TupleSource, memory.NewTupleCache())
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.Poll(ctx); err != nil && !errors.Is(err, workers.ErrNoWork) {
		t.Fatal(err)
	}
	if cache.Stats().Publications != 1 {
		t.Fatalf("first poll did not publish: %+v", cache.Stats())
	}
	before, err := repos.TupleSource.Snapshot(ctx, "")
	if err != nil {
		t.Fatal(err)
	}

	recreateDatabase(t, db)

	logs := &recordingHandler{}
	pool := workers.NewPool(cache.Poll, workers.WithName("authorization.tuplecache"), workers.WithWorkerCount(1), workers.WithPollInterval(time.Millisecond), workers.WithLogger(slog.New(logs)))
	done := make(chan error, 1)
	go func() { done <- pool.Run(ctx) }()
	select {
	case err := <-done:
		if !errors.Is(err, tuplecache.ErrSourceIdentity) {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("pool did not stop after the database was recreated")
	}
	errorsLogged := logs.matching(slog.LevelError, "")
	if len(errorsLogged) != 1 || errorsLogged[0].Message != "worker requesting pool shutdown" {
		t.Fatalf("want one shutdown ERROR, got %d: %+v", len(errorsLogged), errorsLogged)
	}
	var cause error
	errorsLogged[0].Attrs(func(a slog.Attr) bool {
		if a.Key == "error" {
			cause, _ = a.Value.Any().(error)
		}
		return true
	})
	if !errors.Is(cause, tuplecache.ErrSourceIdentity) {
		t.Fatalf("shutdown ERROR error attribute: %v", cause)
	}
	if stopped := logs.matching(slog.LevelInfo, "worker pool stopped"); len(stopped) != 1 {
		t.Fatalf("want one pool-stopped INFO, got %d", len(stopped))
	}
	if stats := cache.Stats(); !stats.Detached || stats.PollFailures != 1 {
		t.Fatalf("relay not detached: %+v", stats)
	}
	if _, err := repos.TupleSource.Snapshot(ctx, ""); !errors.Is(err, tuplecache.ErrSourceIdentity) {
		t.Fatalf("snapshot after recreate: %v", err)
	}
	if err := repos.TupleSource.Acknowledge(ctx, before.Receipt, "next", nil); !errors.Is(err, tuplecache.ErrSourceIdentity) {
		t.Fatalf("acknowledgement after recreate: %v", err)
	}

	fresh, err := Repositories(ctx, db, cacheOptions(cfg)...)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := tuplecache.New(fresh.TupleSource, memory.NewTupleCache())
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.Poll(ctx); err != nil && !errors.Is(err, workers.ErrNoWork) {
		t.Fatalf("fresh process poll: %v", err)
	}
	if stats := restarted.Stats(); stats.Publications != 1 || stats.Detached {
		t.Fatalf("fresh process did not publish: %+v", stats)
	}
}

func recreateDatabase(t *testing.T, db *tursodb.DB) {
	t.Helper()
	ctx := context.Background()
	rows, err := db.Query(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		t.Fatal(err)
	}
	var drops []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		drops = append(drops, `DROP TABLE "`+name+`"`)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "PRAGMA foreign_keys=OFF; "+strings.Join(drops, "; ")+"; PRAGMA foreign_keys=ON"); err != nil {
		t.Fatal(err)
	}
	if err := tursodb.RunMigrations(ctx, db, MigrationsFS, MigrationsDir); err != nil {
		t.Fatal(err)
	}
	installTupleCache(t, db)
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}
func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(string) slog.Handler      { return h }
func (h *recordingHandler) matching(level slog.Level, message string) []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []slog.Record
	for _, r := range h.records {
		if r.Level == level && (message == "" || r.Message == message) {
			out = append(out, r)
		}
	}
	return out
}
