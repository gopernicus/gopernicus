package pgxdb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	jackpgx "github.com/jackc/pgx/v5"
)

func seedLimiterWindow(t *testing.T, db *DB, ctx context.Context, key string, row windowRow) {
	t.Helper()
	_, err := db.Exec(ctx, `INSERT INTO ratelimit_windows
		(key, window_start, request_count, prev_count, last_allowed, updated_at, expires_at, window_ms)
		VALUES (@key, @start, @count, @prev, @allowed, @updated, @expires, @window)`,
		jackpgx.NamedArgs{"key": key, "start": row.WindowStart, "count": row.RequestCount,
			"prev": row.PrevCount, "allowed": row.LastAllowed, "updated": row.UpdatedAt,
			"expires": row.ExpiresAt, "window": row.WindowMS})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = db.Exec(cleanupCtx, `DELETE FROM ratelimit_windows WHERE key = @key`, jackpgx.NamedArgs{"key": key})
	})
}

func TestLive_LimiterAnchoredBuckets(t *testing.T) {
	db := openLimiterDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Future updated_at pins exact decisions through the clock rollback clamp.
	start := serverNow(t, db).Add(time.Hour).Truncate(time.Millisecond)
	for _, tc := range []struct {
		name                 string
		elapsed, count, prev int64
		allowed              bool
		wantCount, wantPrev  int64
		advance, retry       int64
	}{
		{"exact boundary", 1000, 2, 0, false, 0, 2, 1000, 1000},
		{"weighted midpoint", 1500, 2, 0, true, 1, 2, 1000, 0},
		{"denial keeps anchor", 250, 1, 2, false, 1, 2, 0, 750},
		{"expiry clears both", 2000, 2, 2, true, 1, 0, 2000, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prefix := livePrefix(t)
			key := prefix + limiterKeyVersion + "key"
			seedLimiterWindow(t, db, ctx, key, windowRow{WindowStart: start,
				RequestCount: tc.count, PrevCount: tc.prev, LastAllowed: true,
				UpdatedAt: start.Add(time.Duration(tc.elapsed) * time.Millisecond),
				ExpiresAt: start.Add(2 * time.Second), WindowMS: 1000})
			got, err := NewLimiter(db, WithLimiterKeyPrefix(prefix)).Allow(ctx, "key", ratelimiter.Limit{Requests: 2, Window: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			remaining := 0
			if tc.elapsed == 2000 {
				remaining = 1
			}
			want := ratelimiter.Result{Allowed: tc.allowed, Remaining: remaining,
				ResetAt:    start.Add(time.Duration(tc.advance+1000) * time.Millisecond).UTC(),
				RetryAfter: time.Duration(tc.retry) * time.Millisecond}
			if got != want {
				t.Fatalf("decision = %+v, want %+v", got, want)
			}
			row := readWindow(t, db, key)
			if row.RequestCount != tc.wantCount || row.PrevCount != tc.wantPrev || row.LastAllowed != tc.allowed || row.WindowMS != 1000 ||
				!row.WindowStart.Equal(start.Add(time.Duration(tc.advance)*time.Millisecond)) ||
				!row.UpdatedAt.Equal(start.Add(time.Duration(tc.elapsed)*time.Millisecond)) ||
				!row.ExpiresAt.Equal(start.Add(time.Duration(tc.advance+2000)*time.Millisecond)) {
				t.Fatalf("persisted state = %+v", row)
			}
		})
	}
}

func TestLive_LimiterPolicyChangesPreserveCounts(t *testing.T) {
	db := openLimiterDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	prefix := livePrefix(t)
	key := prefix + limiterKeyVersion + "key"
	start := serverNow(t, db).Add(time.Hour).Truncate(time.Millisecond)
	seedLimiterWindow(t, db, ctx, key, windowRow{WindowStart: start, RequestCount: 1,
		LastAllowed: true, UpdatedAt: start, ExpiresAt: start.Add(2 * time.Minute), WindowMS: 60000})
	l := NewLimiter(db, WithLimiterKeyPrefix(prefix))
	before := readWindow(t, db, key)
	if _, err := l.Allow(ctx, "key", ratelimiter.PerSecond(1)); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("active window change = %v", err)
	}
	if after := readWindow(t, db, key); after != before {
		t.Fatalf("invalid policy changed state: before %+v, after %+v", before, after)
	}
	if got, err := l.Allow(ctx, "key", ratelimiter.PerMinute(2)); err != nil || !got.Allowed || got.Remaining != 0 {
		t.Fatalf("raised ceiling = %+v, %v; want retained count plus one", got, err)
	}
	if got, err := l.Allow(ctx, "key", ratelimiter.PerMinute(1)); err != nil || got.Allowed {
		t.Fatalf("lowered ceiling = %+v, %v; want denial", got, err)
	}
	if _, err := db.Exec(ctx, `UPDATE ratelimit_windows SET expires_at = TIMESTAMPTZ 'epoch' WHERE key = @key`, jackpgx.NamedArgs{"key": key}); err != nil {
		t.Fatal(err)
	}
	if got, err := l.Allow(ctx, "key", ratelimiter.PerSecond(1)); err != nil || !got.Allowed {
		t.Fatalf("unpruned expired window change = %+v, %v", got, err)
	}
	if row := readWindow(t, db, key); row.WindowMS != 1000 || row.RequestCount != 1 || row.PrevCount != 0 {
		t.Fatalf("expired row retained old policy/counts: %+v", row)
	}
}

func TestLive_LimiterLegacyKeyCollisionIsPreserved(t *testing.T) {
	db := openLimiterDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, window := range []int64{0, -1, ratelimiter.MaxWindow.Milliseconds() + 1} {
		t.Run(fmt.Sprint(window), func(t *testing.T) {
			prefix := livePrefix(t)
			// Old logical key "v2:key" collides with new logical key "key".
			key := prefix + "v2:key"
			old := time.Unix(1, 0).UTC()
			seedLimiterWindow(t, db, ctx, key, windowRow{WindowStart: old,
				RequestCount: 7, PrevCount: 2, LastAllowed: true,
				UpdatedAt: old, ExpiresAt: old, WindowMS: window})
			before := readWindow(t, db, key)
			l := NewLimiter(db, WithLimiterKeyPrefix(prefix))
			if _, err := l.Allow(ctx, "key", ratelimiter.PerMinute(1)); !errors.Is(err, sdk.ErrConflict) {
				t.Errorf("Allow collision = %v, want conflict", err)
			}
			if err := l.Reset(ctx, "key"); !errors.Is(err, sdk.ErrConflict) {
				t.Errorf("Reset collision = %v, want conflict", err)
			}
			if after := readWindow(t, db, key); after != before {
				t.Fatalf("legacy state changed: before %+v, after %+v", before, after)
			}
		})
	}
}

func TestLive_LimiterDecisionTimeAfterRowLock(t *testing.T) {
	db := openLimiterDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Keep the one observed backend alive across the PID query and admission.
	// Config's zero lifetime otherwise retires a connection on pool release.
	waitingDB, err := Open(context.Background(), Config{DSN: os.Getenv("POSTGRES_TEST_DSN"), MaxConns: 1,
		MaxLifetime: time.Minute, MaxIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer waitingDB.Close()
	var pid int
	if err := waitingDB.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	prefix := livePrefix(t)
	key := prefix + limiterKeyVersion + "key"
	start := serverNow(t, db).Truncate(time.Millisecond)
	seedLimiterWindow(t, db, ctx, key, windowRow{WindowStart: start, RequestCount: 1,
		LastAllowed: true, UpdatedAt: start, ExpiresAt: start.Add(2 * time.Second), WindowMS: 1000})
	blocker, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	if _, err := blocker.Exec(ctx, `SELECT key FROM ratelimit_windows WHERE key = @key FOR UPDATE`, jackpgx.NamedArgs{"key": key}); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		result ratelimiter.Result
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := NewLimiter(waitingDB, WithLimiterKeyPrefix(prefix)).Allow(ctx, "key", ratelimiter.PerSecond(1))
		done <- outcome{result, err}
	}()
	// Observe the actual backend waiting on the row lock before advancing past
	// expiry. A goroutine-start signal alone cannot prove when SQL sampled time.
	ticker := time.NewTicker(2 * time.Millisecond)
	defer ticker.Stop()
	for {
		var blocked bool
		if err := db.QueryRow(ctx, `SELECT coalesce(wait_event_type = 'Lock', false) FROM pg_stat_activity WHERE pid = @pid`, jackpgx.NamedArgs{"pid": pid}).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case got := <-done:
			t.Fatalf("Allow finished before lock release: %+v, %v", got.result, got.err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
	if !serverNow(t, db).Before(start.Add(time.Second)) {
		t.Fatal("test did not establish contention inside the original bucket")
	}
	if _, err := db.Exec(ctx, `SELECT pg_sleep(greatest(extract(epoch FROM (@expires::timestamptz - clock_timestamp())), 0)::double precision + 0.02)`, jackpgx.NamedArgs{"expires": start.Add(2 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	releasedAt := serverNow(t, db).Truncate(time.Millisecond)
	if err := blocker.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if got.err != nil || !got.result.Allowed {
			t.Fatalf("Allow after expiration while blocked = %+v, %v", got.result, got.err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if row := readWindow(t, db, key); row.UpdatedAt.Before(releasedAt) || row.WindowStart.Before(releasedAt) || row.RequestCount != 1 || row.PrevCount != 0 {
		t.Fatalf("decision reused a pre-lock timestamp: %+v; release at %v", row, releasedAt)
	}
}

func TestLive_LimiterStatusCheckRequiresWindowMigration(t *testing.T) {
	db := openLimiterDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	schema := fmt.Sprintf("pgxdb_limiter_migration_%d", time.Now().UnixNano())
	if _, err := db.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`) })
	scoped := openScopedToSchema(t, schema)
	if _, err := scoped.Exec(ctx, strings.Replace(limiterTableDDL, ",\n    window_ms     BIGINT      NOT NULL DEFAULT 0", "", 1)); err != nil {
		t.Fatal(err)
	}
	l := NewLimiter(scoped)
	if err := l.StatusCheck(ctx); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("missing window_ms = %v, want not found", err)
	}
	if _, err := scoped.Exec(ctx, `ALTER TABLE ratelimit_windows ADD COLUMN window_ms BIGINT NOT NULL DEFAULT 0`); err != nil {
		t.Fatal(err)
	}
	if err := l.StatusCheck(ctx); err != nil {
		t.Fatalf("after reference column migration: %v", err)
	}
}

func TestLive_LimiterDecisionTimeAfterRolledBackInsert(t *testing.T) {
	db := openLimiterDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Keep the one observed backend alive across the PID query and admission.
	// Config's zero lifetime otherwise retires a connection on pool release.
	waitingDB, err := Open(context.Background(), Config{DSN: os.Getenv("POSTGRES_TEST_DSN"), MaxConns: 1,
		MaxLifetime: time.Minute, MaxIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer waitingDB.Close()
	var pid int
	if err := waitingDB.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	prefix := livePrefix(t)
	key := prefix + limiterKeyVersion + "key"
	start := serverNow(t, db).Truncate(time.Millisecond)
	blocker, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	if _, err := blocker.Exec(ctx, `INSERT INTO ratelimit_windows
		(key, window_start, request_count, prev_count, last_allowed, updated_at, expires_at, window_ms)
		VALUES (@key, @start, 1, 0, true, @start, @expires, 1000)`,
		jackpgx.NamedArgs{"key": key, "start": start, "expires": start.Add(2 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(context.Background(), `DELETE FROM ratelimit_windows WHERE key = @key`, jackpgx.NamedArgs{"key": key})
	})
	type outcome struct {
		result ratelimiter.Result
		err    error
	}
	done := make(chan outcome, 1)
	l := NewLimiter(waitingDB, WithLimiterKeyPrefix(prefix))
	go func() {
		result, err := l.Allow(ctx, "key", ratelimiter.PerSecond(1))
		done <- outcome{result, err}
	}()
	ticker := time.NewTicker(2 * time.Millisecond)
	defer ticker.Stop()
	for {
		var blocked bool
		if err := db.QueryRow(ctx, `SELECT coalesce(wait_event_type = 'Lock', false) FROM pg_stat_activity WHERE pid = @pid`, jackpgx.NamedArgs{"pid": pid}).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case got := <-done:
			t.Fatalf("Allow finished before rollback: %+v, %v", got.result, got.err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
	if _, err := db.Exec(ctx, `SELECT pg_sleep(greatest(extract(epoch FROM (@expires::timestamptz - clock_timestamp())), 0)::double precision + 0.02)`, jackpgx.NamedArgs{"expires": start.Add(2 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	releasedAt := serverNow(t, db).Truncate(time.Millisecond)
	if err := blocker.Rollback(); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if got.err != nil || !got.result.Allowed || !got.result.ResetAt.After(releasedAt) {
			t.Fatalf("Allow after conflicting insert rollback = %+v, %v; release at %v", got.result, got.err, releasedAt)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if row := readWindow(t, db, key); row.UpdatedAt.Before(releasedAt) || row.WindowStart.Before(releasedAt) || row.RequestCount != 1 || row.PrevCount != 0 {
		t.Fatalf("insert reused a pre-lock timestamp: %+v; release at %v", row, releasedAt)
	}
	if got, err := l.Allow(ctx, "key", ratelimiter.PerSecond(1)); err != nil || got.Allowed {
		t.Fatalf("next Allow = %+v, %v; want first admission charged to current budget", got, err)
	}
}

// This driver hook interrupts only completed admission statements, reproducing
// a Reset/pruner between placeholder insertion and the second statement.
type limiterAfterQuery struct {
	after func(context.Context)
}

type limiterQueryKey struct{}

func (l limiterAfterQuery) TraceQueryStart(ctx context.Context, _ *jackpgx.Conn, data jackpgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, limiterQueryKey{}, strings.Contains(data.SQL, "WITH admission AS"))
}

func (l limiterAfterQuery) TraceQueryEnd(ctx context.Context, _ *jackpgx.Conn, data jackpgx.TraceQueryEndData) {
	if admission, _ := ctx.Value(limiterQueryKey{}).(bool); admission && data.Err == nil {
		l.after(ctx)
	}
}

func TestLive_LimiterInitializationInterruptionIsBounded(t *testing.T) {
	db := openLimiterDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	prefix := livePrefix(t)
	key := prefix + limiterKeyVersion + "key"
	var counts []int64
	var hookErr error
	tracer := limiterAfterQuery{after: func(ctx context.Context) {
		var count int64
		err := db.QueryRow(ctx, `DELETE FROM ratelimit_windows WHERE key = @key RETURNING request_count`, jackpgx.NamedArgs{"key": key}).Scan(&count)
		if err != nil {
			hookErr = err
		}
		counts = append(counts, count)
	}}
	traced, err := Open(context.Background(), Config{DSN: os.Getenv("POSTGRES_TEST_DSN"), Tracer: tracer})
	if err != nil {
		t.Fatal(err)
	}
	defer traced.Close()
	if _, err := NewLimiter(traced, WithLimiterKeyPrefix(prefix)).Allow(ctx, "key", ratelimiter.PerMinute(1)); !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("interrupted initialization = %v, want conflict", err)
	}
	if hookErr != nil || len(counts) != 2 || counts[0] != 0 || counts[1] != 0 {
		t.Fatalf("initialization commands consumed counts %v, hook error %v; want two uncharged placeholders", counts, hookErr)
	}
}

func TestLive_LimiterCancellationAfterInitializationDoesNotAdmit(t *testing.T) {
	db := openLimiterDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	prefix := livePrefix(t)
	key := prefix + limiterKeyVersion + "key"
	t.Cleanup(func() {
		_, _ = db.Exec(context.Background(), `DELETE FROM ratelimit_windows WHERE key = @key`, jackpgx.NamedArgs{"key": key})
	})
	calls := 0
	traced, err := Open(context.Background(), Config{DSN: os.Getenv("POSTGRES_TEST_DSN"), Tracer: limiterAfterQuery{after: func(context.Context) {
		calls++
		cancel()
	}}})
	if err != nil {
		t.Fatal(err)
	}
	defer traced.Close()
	if _, err := NewLimiter(traced, WithLimiterKeyPrefix(prefix)).Allow(ctx, "key", ratelimiter.PerMinute(1)); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled after initialization = %v", err)
	}
	if row := readWindow(t, db, key); calls != 1 || row.RequestCount != 0 || row.PrevCount != 0 || !row.ExpiresAt.Equal(time.Unix(0, 0)) {
		t.Fatalf("cancellation admitted quota: %d calls, %+v", calls, row)
	}
}
