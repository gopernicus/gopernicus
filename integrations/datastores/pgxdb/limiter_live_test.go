// Live limiter tests. They need a real Postgres because the whole contract lives
// in one server-side statement: atomic admission, server-time windows, and the
// sliding-window arithmetic. Absent POSTGRES_TEST_DSN they skip LOUDLY — a silent
// green here would claim ratelimiter.Limiter conformance with nothing verified.
//
//	docker run --rm -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:17
//	POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' go test -race ./...
package pgxdb

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	jackpgx "github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter/ratelimitertest"
)

// limiterTableDDL is the host-owned reference DDL, kept verbatim in sync with the
// module README. The connector creates nothing at runtime; these tests play the
// host that migrated the table.
const limiterTableDDL = `CREATE TABLE IF NOT EXISTS ratelimit_windows (
    key           TEXT        PRIMARY KEY,
    window_start  TIMESTAMPTZ NOT NULL,
    request_count BIGINT      NOT NULL,
    prev_count    BIGINT      NOT NULL,
    last_allowed  BOOLEAN     NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL,
    expires_at    TIMESTAMPTZ NOT NULL
)`

const limiterIndexDDL = `CREATE INDEX IF NOT EXISTS ratelimit_windows_expires_at_idx
    ON ratelimit_windows (expires_at)`

// windowRow is the persisted window state, read back to prove what the admission
// statement actually wrote.
type windowRow struct {
	WindowStart  time.Time
	RequestCount int64
	PrevCount    int64
	LastAllowed  bool
	UpdatedAt    time.Time
	ExpiresAt    time.Time
}

// limiterSkipWarning prints the not-verified banner once per test binary.
//
// t.Skip's message is invisible under `go test ./...` (no -v) — which is exactly
// how `make test` runs — so the whole limiter contract would otherwise look green
// with nothing verified. cmd/go buffers and drops a passing package's stdout and
// stderr too, so the banner also goes to the controlling terminal when there is
// one: that is the only sink `go test ./...` cannot swallow. There is no terminal
// in CI, where the banner falls back to stderr (and CI sets the DSN anyway).
var limiterSkipWarning sync.Once

func warnLimiterNotVerified(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	if tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0); err == nil {
		fmt.Fprintln(tty, msg)
		_ = tty.Close()
	}
}

// openLimiterDB opens a live connection and ensures the host-owned table exists,
// skipping loudly when POSTGRES_TEST_DSN is unset.
func openLimiterDB(t *testing.T) *DB {
	t.Helper()

	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		limiterSkipWarning.Do(func() {
			warnLimiterNotVerified("WARNING: POSTGRES_TEST_DSN not set — pgxdb ratelimiter.Limiter conformance NOT verified (atomic admission, server time, and the sliding window are all server-side; the hermetic tests cover none of them)")
		})
		t.Skip("POSTGRES_TEST_DSN not set — pgxdb ratelimiter.Limiter conformance NOT verified")
	}

	db, err := Open(Config{DSN: dsn})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	if _, err := db.Exec(ctx, limiterTableDDL); err != nil {
		t.Fatalf("create window table: %v", err)
	}
	if _, err := db.Exec(ctx, limiterIndexDDL); err != nil {
		t.Fatalf("create pruning index: %v", err)
	}
	return db
}

// livePrefix returns a per-test key namespace so window state never bleeds
// between subtests sharing one table.
func livePrefix(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("rltest:%d:", time.Now().UnixNano())
}

func readWindow(t *testing.T, db *DB, key string) windowRow {
	t.Helper()

	var row windowRow
	err := db.QueryRow(context.Background(),
		`SELECT window_start, request_count, prev_count, last_allowed, updated_at, expires_at
		   FROM ratelimit_windows WHERE key = @key`,
		jackpgx.NamedArgs{"key": key},
	).Scan(&row.WindowStart, &row.RequestCount, &row.PrevCount, &row.LastAllowed, &row.UpdatedAt, &row.ExpiresAt)
	if err != nil {
		t.Fatalf("read window row %q: %v", key, err)
	}
	return row
}

func serverNow(t *testing.T, db *DB) time.Time {
	t.Helper()

	var now time.Time
	if err := db.QueryRow(context.Background(), "SELECT clock_timestamp()").Scan(&now); err != nil {
		t.Fatalf("read server clock: %v", err)
	}
	return now
}

// TestLive_ConformanceLimiter runs the shared ratelimiter.Limiter conformance
// suite (allow/deny/reset/refill/independent keys/idempotent close) against live
// Postgres. Each fresh Limiter gets its own key prefix.
func TestLive_ConformanceLimiter(t *testing.T) {
	db := openLimiterDB(t)

	ratelimitertest.Run(t, func(t *testing.T) ratelimiter.Limiter {
		prefix := livePrefix(t)
		t.Cleanup(func() {
			_, _ = db.Exec(context.Background(),
				"DELETE FROM ratelimit_windows WHERE key LIKE @p", jackpgx.NamedArgs{"p": prefix + "%"})
		})
		return NewLimiter(db, WithLimiterKeyPrefix(prefix))
	})
}

// TestLive_LimiterAdmitsExactlyLimitUnderConcurrency is the atomicity proof: four
// independent connection pools (standing in for four application instances) race
// 40 goroutines at a ceiling of 8. A read-then-check-then-write limiter
// over-admits here; a single atomic row transition cannot. The persisted counter
// must also equal the ceiling — denied calls consume no quota.
//
// It is also where the returned Result is bounds-checked, because only a
// concurrent caller can produce the stale clock_timestamp() that made the sliding
// weight exceed 1: a statement captures `now` before it waits on the row lock, so
// a loser can hold a `now` earlier than the window_start the winner installed.
// Unclamped, that returns a RetryAfter longer than the window. A sequential test
// cannot reach the state at all.
func TestLive_LimiterAdmitsExactlyLimitUnderConcurrency(t *testing.T) {
	db := openLimiterDB(t)

	const (
		instances        = 4
		callers          = 40
		ceiling          = 8
		connsPerInstance = 4
		raceDeadline     = 30 * time.Second
	)

	prefix := livePrefix(t)
	fullKey := prefix + "race"
	t.Cleanup(func() {
		_, _ = db.Exec(context.Background(),
			"DELETE FROM ratelimit_windows WHERE key = @key", jackpgx.NamedArgs{"key": fullKey})
	})

	dsn := os.Getenv("POSTGRES_TEST_DSN")
	limiters := make([]*Limiter, instances)
	for i := range limiters {
		// MaxConns is capped so the four pools cannot open 40 server connections
		// between them: the point of the test is row-level contention, and an
		// unbounded pool would trade that for connection-slot exhaustion on a
		// small server.
		instance, err := Open(Config{DSN: dsn, MaxConns: connsPerInstance})
		if err != nil {
			t.Fatalf("open instance %d: %v", i, err)
		}
		t.Cleanup(func() { _ = instance.Close() })
		limiters[i] = NewLimiter(instance, WithLimiterKeyPrefix(prefix))
	}

	// Every racing call carries a deadline: capped pools plus row locks mean a
	// wedged server would otherwise block goroutines (and the test) forever.
	ctx, cancel := context.WithTimeout(context.Background(), raceDeadline)
	defer cancel()

	limit := ratelimiter.Limit{Requests: ceiling, Window: time.Minute}
	var (
		allowed atomic.Int64
		start   = make(chan struct{})
		wg      sync.WaitGroup
		errs    = make(chan error, callers)
		results = make(chan ratelimiter.Result, callers)
	)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(l *Limiter) {
			defer wg.Done()
			<-start
			res, err := l.Allow(ctx, "race", limit)
			if err != nil {
				errs <- err
				return
			}
			results <- res
			if res.Allowed {
				allowed.Add(1)
			}
		}(limiters[i%instances])
	}
	close(start)
	wg.Wait()
	close(errs)
	close(results)
	for err := range errs {
		t.Fatalf("concurrent Allow: %v", err)
	}

	if got := allowed.Load(); got != ceiling {
		t.Fatalf("allowed under %d concurrent callers = %d, want exactly %d", callers, got, ceiling)
	}

	for res := range results {
		if res.RetryAfter < 0 || res.RetryAfter > limit.Window {
			t.Errorf("RetryAfter = %v under concurrency, want within [0, %v] — the sliding weight escaped its clamp on a stale server clock",
				res.RetryAfter, limit.Window)
		}
		if res.Remaining < 0 || res.Remaining > ceiling {
			t.Errorf("Remaining = %d under concurrency, want within [0, %d]", res.Remaining, ceiling)
		}
		if !res.Allowed && res.RetryAfter <= 0 {
			t.Errorf("denied Result carries RetryAfter = %v, want positive", res.RetryAfter)
		}
	}
	if row := readWindow(t, db, fullKey); row.RequestCount != ceiling {
		t.Errorf("persisted request_count = %d, want %d (denied calls must consume no quota)", row.RequestCount, ceiling)
	}
}

// TestLive_LimiterUsesServerTime proves every timestamp and duration originates
// from Postgres, not the caller. The stored instant is bracketed by two
// server-clock reads (a skewed caller clock would fall outside that bracket), and
// ResetAt/RetryAfter are exact arithmetic over that same server instant — no
// round-trip slop, which is what a caller-clock computation would show.
func TestLive_LimiterUsesServerTime(t *testing.T) {
	db := openLimiterDB(t)

	prefix := livePrefix(t)
	fullKey := prefix + "clock"
	t.Cleanup(func() {
		_, _ = db.Exec(context.Background(),
			"DELETE FROM ratelimit_windows WHERE key = @key", jackpgx.NamedArgs{"key": fullKey})
	})

	limiter := NewLimiter(db, WithLimiterKeyPrefix(prefix))
	const window = 30 * time.Second
	limit := ratelimiter.Limit{Requests: 1, Window: window}

	before := serverNow(t, db)
	res, err := limiter.Allow(context.Background(), "clock", limit)
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	after := serverNow(t, db)
	if !res.Allowed {
		t.Fatalf("first Allow Allowed = false, want true")
	}

	row := readWindow(t, db, fullKey)
	if row.UpdatedAt.Before(before) || row.UpdatedAt.After(after) {
		t.Errorf("stored updated_at %v outside the server-clock bracket [%v, %v] — the instant is not server-derived",
			row.UpdatedAt, before, after)
	}
	if !row.WindowStart.Equal(row.UpdatedAt) {
		t.Errorf("window_start %v != updated_at %v on a fresh window", row.WindowStart, row.UpdatedAt)
	}
	if got := res.ResetAt.Sub(row.WindowStart); got != window {
		t.Errorf("ResetAt - window_start = %v, want exactly %v (server arithmetic, not caller clock)", got, window)
	}
	if got := row.ExpiresAt.Sub(row.UpdatedAt); got != window*5/2 {
		t.Errorf("expires_at - updated_at = %v, want %v (2.5 windows of retention)", got, window*5/2)
	}

	denied, err := limiter.Allow(context.Background(), "clock", limit)
	if err != nil {
		t.Fatalf("second Allow: %v", err)
	}
	if denied.Allowed {
		t.Fatal("second Allow Allowed = true, want false (over limit)")
	}
	deniedRow := readWindow(t, db, fullKey)
	if want := denied.ResetAt.Sub(deniedRow.UpdatedAt); denied.RetryAfter != want {
		t.Errorf("RetryAfter = %v, want exactly %v (ResetAt - server now)", denied.RetryAfter, want)
	}
	if denied.RetryAfter <= 0 || denied.RetryAfter > window {
		t.Errorf("RetryAfter = %v, want within (0, %v]", denied.RetryAfter, window)
	}
	if deniedRow.RequestCount != 1 {
		t.Errorf("request_count after a denial = %d, want 1 (denials consume no quota)", deniedRow.RequestCount)
	}
}

// TestLive_LimiterBurstExtendsCeiling proves Burst is added to Requests to form
// the effective ceiling, matching the goredis limiter.
func TestLive_LimiterBurstExtendsCeiling(t *testing.T) {
	db := openLimiterDB(t)

	prefix := livePrefix(t)
	t.Cleanup(func() {
		_, _ = db.Exec(context.Background(),
			"DELETE FROM ratelimit_windows WHERE key LIKE @p", jackpgx.NamedArgs{"p": prefix + "%"})
	})

	limiter := NewLimiter(db, WithLimiterKeyPrefix(prefix))
	limit := ratelimiter.PerMinute(2).WithBurst(3)

	for i := 0; i < 5; i++ {
		res, err := limiter.Allow(context.Background(), "burst", limit)
		if err != nil {
			t.Fatalf("Allow %d: %v", i+1, err)
		}
		if !res.Allowed {
			t.Fatalf("Allow %d Allowed = false, want true (2 requests + 3 burst)", i+1)
		}
		if want := 4 - i; res.Remaining != want {
			t.Errorf("Allow %d Remaining = %d, want %d", i+1, res.Remaining, want)
		}
	}

	res, err := limiter.Allow(context.Background(), "burst", limit)
	if err != nil {
		t.Fatalf("Allow past burst: %v", err)
	}
	if res.Allowed {
		t.Error("Allow past the burst ceiling Allowed = true, want false")
	}
}

// TestLive_LimiterKeyPrefixIsolates proves the configurable prefix namespaces one
// key across two limiters sharing the same table.
func TestLive_LimiterKeyPrefixIsolates(t *testing.T) {
	db := openLimiterDB(t)

	prefixA, prefixB := livePrefix(t)+"a:", livePrefix(t)+"b:"
	t.Cleanup(func() {
		for _, p := range []string{prefixA, prefixB} {
			_, _ = db.Exec(context.Background(),
				"DELETE FROM ratelimit_windows WHERE key LIKE @p", jackpgx.NamedArgs{"p": p + "%"})
		}
	})

	limit := ratelimiter.PerMinute(1)
	a := NewLimiter(db, WithLimiterKeyPrefix(prefixA))
	b := NewLimiter(db, WithLimiterKeyPrefix(prefixB))

	if res, err := a.Allow(context.Background(), "shared", limit); err != nil || !res.Allowed {
		t.Fatalf("a.Allow = %+v, err %v, want Allowed=true", res, err)
	}
	if res, err := a.Allow(context.Background(), "shared", limit); err != nil || res.Allowed {
		t.Fatalf("second a.Allow = %+v, err %v, want Allowed=false", res, err)
	}
	if res, err := b.Allow(context.Background(), "shared", limit); err != nil || !res.Allowed {
		t.Fatalf("b.Allow = %+v, err %v, want Allowed=true (a different prefix is a different budget)", res, err)
	}
}

// TestLive_LimiterCloseLeavesDatabaseUsable proves Close never touches the shared
// pool: the caller-owned *DB still serves statements (and the limiter itself
// still works) after a repeated Close.
func TestLive_LimiterCloseLeavesDatabaseUsable(t *testing.T) {
	db := openLimiterDB(t)

	prefix := livePrefix(t)
	t.Cleanup(func() {
		_, _ = db.Exec(context.Background(),
			"DELETE FROM ratelimit_windows WHERE key LIKE @p", jackpgx.NamedArgs{"p": prefix + "%"})
	})

	limiter := NewLimiter(db, WithLimiterKeyPrefix(prefix))
	if res, err := limiter.Allow(context.Background(), "close", ratelimiter.PerMinute(5)); err != nil || !res.Allowed {
		t.Fatalf("Allow before Close = %+v, err %v, want Allowed=true", res, err)
	}
	if err := limiter.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := limiter.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	if err := StatusCheck(context.Background(), db); err != nil {
		t.Fatalf("StatusCheck after limiter Close: %v — the limiter closed the caller's pool", err)
	}
	if res, err := limiter.Allow(context.Background(), "close", ratelimiter.PerMinute(5)); err != nil || !res.Allowed {
		t.Fatalf("Allow after Close = %+v, err %v, want Allowed=true", res, err)
	}
}

// TestLive_LimiterHonorsContextCancellation proves a canceled context aborts
// before any state is written.
func TestLive_LimiterHonorsContextCancellation(t *testing.T) {
	db := openLimiterDB(t)

	prefix := livePrefix(t)
	fullKey := prefix + "canceled"
	limiter := NewLimiter(db, WithLimiterKeyPrefix(prefix))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := limiter.Allow(ctx, "canceled", ratelimiter.PerMinute(5)); err == nil {
		t.Fatal("Allow with canceled ctx error = nil, want non-nil")
	}
	if err := limiter.Reset(ctx, "canceled"); err == nil {
		t.Fatal("Reset with canceled ctx error = nil, want non-nil")
	}

	var n int
	if err := db.QueryRow(context.Background(),
		"SELECT count(*) FROM ratelimit_windows WHERE key = @key", jackpgx.NamedArgs{"key": fullKey}).Scan(&n); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if n != 0 {
		t.Errorf("rows written under a canceled context = %d, want 0", n)
	}
}

// TestLive_LimiterPruningStatement proves the README's retention story: expired
// window rows are removable by the host's scheduled DELETE, and a pruned key
// starts a fresh budget.
func TestLive_LimiterPruningStatement(t *testing.T) {
	db := openLimiterDB(t)

	prefix := livePrefix(t)
	fullKey := prefix + "prune"
	t.Cleanup(func() {
		_, _ = db.Exec(context.Background(),
			"DELETE FROM ratelimit_windows WHERE key = @key", jackpgx.NamedArgs{"key": fullKey})
	})

	limiter := NewLimiter(db, WithLimiterKeyPrefix(prefix))
	limit := ratelimiter.Limit{Requests: 1, Window: 50 * time.Millisecond}

	if res, err := limiter.Allow(context.Background(), "prune", limit); err != nil || !res.Allowed {
		t.Fatalf("Allow = %+v, err %v, want Allowed=true", res, err)
	}

	// The row's retention is 2.5 windows; wait past it, then run the README's
	// pruning statement verbatim.
	time.Sleep(200 * time.Millisecond)
	tag, err := db.Exec(context.Background(), "DELETE FROM ratelimit_windows WHERE expires_at < now()")
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if tag.RowsAffected() == 0 {
		t.Error("pruning removed 0 rows, want the expired window row")
	}

	var n int
	if err := db.QueryRow(context.Background(),
		"SELECT count(*) FROM ratelimit_windows WHERE key = @key", jackpgx.NamedArgs{"key": fullKey}).Scan(&n); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if n != 0 {
		t.Errorf("rows for the expired key after pruning = %d, want 0", n)
	}
}

// errMapSchema holds a deliberately hostile copy of the reference table. The
// limiter's SQL names ratelimit_windows unqualified, so a pool whose search_path
// points here hits constraints instead of the real table — the real one is never
// altered, and no other leg is disturbed.
const errMapSchema = "pgxdb_limiter_errmap"

// openScopedToSchema opens a pool whose search_path is one scratch schema, which
// is how these legs put the limiter in front of a different (or missing)
// ratelimit_windows without touching the real one.
func openScopedToSchema(t *testing.T, schema string) *DB {
	t.Helper()

	u, err := url.Parse(os.Getenv("POSTGRES_TEST_DSN"))
	if err != nil {
		t.Skipf("POSTGRES_TEST_DSN is not URL-form (%v) — this leg needs a search_path override", err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()

	scoped, err := Open(Config{DSN: u.String(), MaxConns: 2})
	if err != nil {
		t.Fatalf("open pool scoped to schema %q: %v", schema, err)
	}
	t.Cleanup(func() { _ = scoped.Close() })
	return scoped
}

// errMapDDL makes both halves of the port fail with a SQLSTATE MapError knows:
// the CHECK rejects Allow's insert of request_count = 1 (23514 → ErrInvalidInput)
// and the ON DELETE RESTRICT child row rejects Reset's DELETE (23503 →
// ErrInvalidReference). The seeded row uses a count the CHECK permits.
var errMapDDL = []string{
	`CREATE SCHEMA IF NOT EXISTS ` + errMapSchema,
	`CREATE TABLE IF NOT EXISTS ` + errMapSchema + `.ratelimit_windows (
	    key           TEXT        PRIMARY KEY,
	    window_start  TIMESTAMPTZ NOT NULL,
	    request_count BIGINT      NOT NULL,
	    prev_count    BIGINT      NOT NULL,
	    last_allowed  BOOLEAN     NOT NULL,
	    updated_at    TIMESTAMPTZ NOT NULL,
	    expires_at    TIMESTAMPTZ NOT NULL,
	    CONSTRAINT ratelimit_windows_count_never_one CHECK (request_count <> 1)
	)`,
	`CREATE TABLE IF NOT EXISTS ` + errMapSchema + `.ratelimit_holds (
	    key TEXT PRIMARY KEY REFERENCES ` + errMapSchema + `.ratelimit_windows (key) ON DELETE RESTRICT
	)`,
}

// TestLive_LimiterMapsDatabaseErrorsOnBothPaths proves Allow and Reset report the
// same stable sdk kinds, so a caller never has to branch on which method produced
// a raw pgx error.
//
// The two halves are guarded at different depths, deliberately: the Allow
// assertion fails without Allow's own MapError (QueryRow hands back a raw Scan
// error), while Reset's error is already mapped by DB.Exec one layer down — that
// assertion pins the port contract, not the call-site line, and would catch the
// connector dropping its mapping.
func TestLive_LimiterMapsDatabaseErrorsOnBothPaths(t *testing.T) {
	db := openLimiterDB(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for _, stmt := range errMapDDL {
		if _, err := db.Exec(ctx, stmt); err != nil {
			t.Fatalf("build error-mapping schema: %v", err)
		}
	}
	t.Cleanup(func() {
		_, _ = db.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+errMapSchema+` CASCADE`)
	})

	scoped := openScopedToSchema(t, errMapSchema)

	prefix := livePrefix(t)
	limiter := NewLimiter(scoped, WithLimiterKeyPrefix(prefix))

	if _, err := limiter.Allow(ctx, "check", ratelimiter.PerMinute(5)); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("Allow() against a violated CHECK error = %v, want sdk.ErrInvalidInput", err)
	}

	held := prefix + "held"
	if _, err := scoped.Exec(ctx,
		`INSERT INTO ratelimit_windows (key, window_start, request_count, prev_count, last_allowed, updated_at, expires_at)
		 VALUES (@key, now(), 7, 0, TRUE, now(), now() + INTERVAL '1 hour')`,
		jackpgx.NamedArgs{"key": held}); err != nil {
		t.Fatalf("seed held window row: %v", err)
	}
	if _, err := scoped.Exec(ctx, `INSERT INTO ratelimit_holds (key) VALUES (@key)`,
		jackpgx.NamedArgs{"key": held}); err != nil {
		t.Fatalf("seed hold row: %v", err)
	}

	if err := limiter.Reset(ctx, "held"); !errors.Is(err, sdk.ErrInvalidReference) {
		t.Errorf("Reset() against a restricting reference error = %v, want sdk.ErrInvalidReference", err)
	}
}

// emptySchema stands in for the host that never ran the reference DDL: it exists,
// it is on the search_path, and it contains no ratelimit_windows.
const emptySchema = "pgxdb_limiter_noddl"

// TestLive_LimiterStatusCheckDetectsMissingTable is the boot gate for the one
// precondition this connector cannot create: without the host-owned table every
// Allow errors, and sdk/capabilities/ratelimiter.Middleware fails OPEN and silent
// — an unthrottled deployment with a green health check. StatusCheck must turn
// that into a startup failure, and must not cry wolf when the table is there.
func TestLive_LimiterStatusCheckDetectsMissingTable(t *testing.T) {
	db := openLimiterDB(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := NewLimiter(db).StatusCheck(ctx); err != nil {
		t.Errorf("StatusCheck() with the reference table present error = %v, want nil", err)
	}

	if _, err := db.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS `+emptySchema); err != nil {
		t.Fatalf("create empty schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+emptySchema+` CASCADE`)
	})

	missing := NewLimiter(openScopedToSchema(t, emptySchema))
	err := missing.StatusCheck(ctx)
	if !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("StatusCheck() without the reference table error = %v, want sdk.ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), "ratelimit_windows") {
		t.Errorf("StatusCheck() error %q does not name the missing table — a boot failure has to say what to migrate", err)
	}

	// The failure StatusCheck is standing in front of: without the probe, this is
	// all the host would get, one error per request, behind a middleware that
	// swallows it.
	if _, err := missing.Allow(ctx, "unmigrated", ratelimiter.PerMinute(5)); err == nil {
		t.Error("Allow() without the reference table error = nil, want non-nil")
	}
}
