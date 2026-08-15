package pgxdb

import (
	"context"
	"errors"
	"fmt"
	"time"

	jackpgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
)

// defaultLimiterKeyPrefix namespaces rate-limit keys inside the shared window
// table, mirroring the goredis limiter's prefix default.
const defaultLimiterKeyPrefix = "ratelimit:"

// limiterTable names the host-owned window table in diagnostics. The statements
// below spell it out literally: the name is fixed framework reference DDL, never
// composed into SQL.
const limiterTable = "ratelimit_windows"

// allowSQL is the whole admission decision: ONE statement, ONE row transition.
//
// The row is claimed by INSERT … ON CONFLICT DO UPDATE, so Postgres locks the
// existing row and re-evaluates the SET expressions against its latest committed
// version. Under N concurrent instances and a ceiling of K, exactly K calls can
// observe effective < limit — a read-then-check-then-write sequence over the same
// state would over-admit. A denied call rewrites the same counters (only the
// bookkeeping columns move), so it consumes no quota.
//
// Time comes from the server: clock_timestamp() is evaluated once, in the
// proposed row, and the conflict branch reads it back as excluded.updated_at.
// Window selection, ResetAt, and RetryAfter therefore never depend on the
// caller's clock. The stored updated_at carries that same server instant out to
// the final SELECT.
//
// The window itself is goredis's sliding approximation, column for column:
// window_start/request_count are the live window, prev_count is the decaying tail
// of the one before it (carried only when the new window opens within one window
// of the old one), and the effective count blends the tail by the fraction of the
// current window still unelapsed. prev_window_start is not stored — the Lua
// script only ever uses it as a "carry something" flag, which prev_count > 0
// already is.
//
// Every sliding weight is clamped into [0, 1] and retry_after into [0, win].
// goredis reads TIME inside its atomic Lua script and cannot go backwards; here
// clock_timestamp() is captured when the statement STARTS, before it waits on the
// row lock, so a statement that loses the race can carry a `now` that predates the
// window_start the winner just installed. Unclamped, that negative elapsed time
// makes the weight exceed 1 — floor(prev_count * weight) > prev_count over-counts
// the tail, and RetryAfter comes back longer than the window, contradicting the
// port's contract. The clamps bound both without changing any decision on the
// ordinary path, where the weight is already in range.
//
// Table: ratelimit_windows (fixed; host-owned reference DDL in the README).
const allowSQL = `
WITH admission AS (
    INSERT INTO ratelimit_windows AS w
        (key, window_start, request_count, prev_count, last_allowed, updated_at, expires_at)
    SELECT p.key, p.now, 1, 0, TRUE, p.now, p.now + p.win * 2.5
    FROM (
        SELECT @key::text AS key,
               (@window_us::bigint * INTERVAL '1 microsecond') AS win,
               clock_timestamp() AS now
    ) p
    ON CONFLICT (key) DO UPDATE SET
        (window_start, request_count, prev_count, last_allowed, updated_at, expires_at) = (
            SELECT s.window_start,
                   s.request_count + CASE WHEN s.effective < s.lim THEN 1 ELSE 0 END,
                   s.prev_count,
                   s.effective < s.lim,
                   s.now,
                   s.now + s.win * 2.5
            FROM (
                SELECT b.now, b.lim, b.win, b.window_start, b.request_count, b.prev_count,
                       b.request_count + floor(b.prev_count * least(greatest(
                           (extract(epoch FROM b.win) - extract(epoch FROM b.now - b.window_start))
                           / extract(epoch FROM b.win), 0), 1))::bigint AS effective
                FROM (
                    SELECT excluded.updated_at AS now, q.lim, q.win,
                           CASE WHEN excluded.updated_at > w.window_start + q.win
                                THEN excluded.updated_at ELSE w.window_start END AS window_start,
                           CASE WHEN excluded.updated_at > w.window_start + q.win
                                THEN 0 ELSE w.request_count END AS request_count,
                           CASE WHEN excluded.updated_at > w.window_start + q.win
                                THEN CASE WHEN excluded.updated_at < w.window_start + q.win + q.win
                                          THEN w.request_count ELSE 0 END
                                ELSE w.prev_count END AS prev_count
                    FROM (SELECT @limit::bigint AS lim,
                                 (@window_us::bigint * INTERVAL '1 microsecond') AS win) q
                ) b
            ) s
        )
    RETURNING w.window_start, w.request_count, w.prev_count, w.last_allowed, w.updated_at
)
SELECT a.last_allowed,
       CASE WHEN a.last_allowed THEN greatest(
                @limit::bigint - (a.request_count + floor(a.prev_count * least(greatest(
                    (extract(epoch FROM c.win) - extract(epoch FROM a.updated_at - a.window_start))
                    / extract(epoch FROM c.win), 0), 1))::bigint), 0)
            ELSE 0 END AS remaining,
       a.window_start + c.win AS reset_at,
       CASE WHEN a.last_allowed THEN 0
            ELSE least(greatest((extract(epoch FROM (a.window_start + c.win - a.updated_at)) * 1000000)::bigint, 0),
                       @window_us::bigint)
       END AS retry_after_us
FROM admission a, (SELECT @window_us::bigint * INTERVAL '1 microsecond' AS win) c`

// resetSQL drops a key's whole window state; the next Allow re-inserts it.
const resetSQL = `DELETE FROM ratelimit_windows WHERE key = @key`

// limiterProbeSQL asks the planner for the window table and fetches nothing:
// LIMIT 0 means no heap access, no index scan, no rows — just name resolution
// against the connection's search_path.
const limiterProbeSQL = `SELECT 1 FROM ratelimit_windows LIMIT 0`

// undefinedTable is SQLSTATE 42P01. It is mapped here rather than in MapError
// because "this relation does not exist" is a deployment fault for the limiter
// (the host never ran the reference DDL) but is not a meaningful sdk kind for the
// connector's general Exec/Query surface, where it would only ever mean a bug.
const undefinedTable = "42P01"

var _ ratelimiter.Limiter = (*Limiter)(nil)

// Limiter is a Postgres-backed ratelimiter.Limiter that enforces one sliding
// window across every application instance sharing the database. Each Allow is a
// single atomic statement against the host-owned ratelimit_windows table (see the
// module README for the reference DDL and the pruning statement — this connector
// creates and migrates nothing). The caller supplies and owns the *DB: Close is a
// no-op and never closes the pool.
type Limiter struct {
	db        *DB
	keyPrefix string
}

// LimiterOption configures a Limiter.
type LimiterOption func(*Limiter)

// WithLimiterKeyPrefix sets the prefix prepended to every rate-limit key, for
// namespacing inside a shared window table. Default: "ratelimit:".
func WithLimiterKeyPrefix(prefix string) LimiterOption {
	return func(l *Limiter) {
		l.keyPrefix = prefix
	}
}

// NewLimiter creates a Postgres rate limiter over the caller's connection (the
// same *DB feeding the host's repositories). The caller owns the connection.
func NewLimiter(db *DB, opts ...LimiterOption) *Limiter {
	l := &Limiter{
		db:        db,
		keyPrefix: defaultLimiterKeyPrefix,
	}
	for _, opt := range opts {
		opt(l)
	}
	return l
}

// Allow checks and records a request against key's sliding window. Limit.Burst is
// added to Limit.Requests to form the effective ceiling. Every returned duration
// and timestamp is derived from Postgres server time, not the caller's clock.
//
// A non-positive window or a non-positive ceiling is a configuration error, not a
// limit: both return sdk.ErrInvalidInput rather than denying every request
// silently, because a zero ceiling on a login path is a misconfiguration the host
// needs to see at the call site.
func (l *Limiter) Allow(ctx context.Context, key string, limit ratelimiter.Limit) (ratelimiter.Result, error) {
	if err := ctx.Err(); err != nil {
		return ratelimiter.Result{}, err
	}
	if limit.Window <= 0 {
		return ratelimiter.Result{}, fmt.Errorf("pgxdb: rate limit window must be positive: %w", sdk.ErrInvalidInput)
	}
	ceiling := limit.Requests + limit.Burst
	if ceiling <= 0 {
		return ratelimiter.Result{}, fmt.Errorf("pgxdb: rate limit ceiling (Requests+Burst) must be positive, got %d: %w", ceiling, sdk.ErrInvalidInput)
	}

	args := jackpgx.NamedArgs{
		"key":       l.keyPrefix + key,
		"limit":     int64(ceiling),
		"window_us": limit.Window.Microseconds(),
	}

	var (
		allowed      bool
		remaining    int64
		resetAt      time.Time
		retryAfterUS int64
	)
	if err := l.db.QueryRow(ctx, allowSQL, args).Scan(&allowed, &remaining, &resetAt, &retryAfterUS); err != nil {
		return ratelimiter.Result{}, fmt.Errorf("pgxdb: evaluating rate limit: %w", MapError(err))
	}

	return ratelimiter.Result{
		Allowed:    allowed,
		Remaining:  int(remaining),
		ResetAt:    resetAt,
		RetryAfter: time.Duration(retryAfterUS) * time.Microsecond,
	}, nil
}

// Reset clears the sliding-window state for key. Its failures map through
// MapError exactly as Allow's do, so both halves of the port report the same
// stable sdk kinds. (DB.Exec already maps, so this is idempotent today; Allow's
// mapping is load-bearing because QueryRow's Scan error is raw. Mapping at both
// call sites is what keeps the two symmetric under either layer changing.)
func (l *Limiter) Reset(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := l.db.Exec(ctx, resetSQL, jackpgx.NamedArgs{"key": l.keyPrefix + key}); err != nil {
		return fmt.Errorf("pgxdb: resetting rate limit: %w", MapError(err))
	}
	return nil
}

// StatusCheck verifies the limiter's one schema precondition: that the
// host-owned ratelimit_windows table is reachable on this connection. Hosts
// should call it at boot, before serving, and refuse to start on error.
//
// This is not ceremony. The connector creates and migrates nothing, so a host
// that skipped the reference DDL — or pointed at the wrong database or
// search_path — gets an error from every Allow, and
// sdk/capabilities/ratelimiter.Middleware swallows limiter errors and lets the
// request through. The failure mode is therefore silent and open: a healthy-looking
// deployment serving completely unthrottled traffic. One boot probe converts that
// into a startup failure.
//
// A missing table reports sdk.ErrNotFound; other failures map through MapError.
// Like the package-level StatusCheck, an undeadlined context gets one second.
func (l *Limiter) StatusCheck(ctx context.Context) error {
	if l.db == nil {
		return fmt.Errorf("pgxdb: rate limiter has no database connection: %w", sdk.ErrInvalidInput)
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Second)
		defer cancel()
	}

	if _, err := l.db.Exec(ctx, limiterProbeSQL); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == undefinedTable {
			return fmt.Errorf("pgxdb: rate limit table %q is missing — apply the reference DDL from the pgxdb README before serving: %w", limiterTable, sdk.ErrNotFound)
		}
		return fmt.Errorf("pgxdb: checking rate limit table: %w", MapError(err))
	}
	return nil
}

// Close is a no-op: window rows are pruned by the host's own scheduled statement
// and the connection lifecycle belongs to the caller. It is idempotent, so
// repeated calls remain safe, and it never closes the shared pool.
func (l *Limiter) Close() error {
	return nil
}
