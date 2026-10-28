package pgx

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk/pkg/workers"
)

type recordingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

// TestTupleSourceRecreatedDatabaseStopsTheRelay recreates only this test's own
// schema: the CI DSN is shared, so it never drops a database or another schema.
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

	schema := cfg.schema.Table("")[:len(cfg.schema.Table(""))-1]
	if _, err := db.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE; CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	if err := pgxdb.RunMigrations(ctx, db, MigrationsFS, MigrationsDir, pgxdb.WithSchema(cfg.schema)); err != nil {
		t.Fatal(err)
	}
	installTupleCache(t, db, cfg.schema)

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
		t.Fatal("pool did not stop after the schema was recreated")
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
