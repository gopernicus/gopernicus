package pgxdb

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	jackpgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const defaultLimiterKeyPrefix = "ratelimit:"
const limiterKeyVersion = "v2:"
const limiterTable = "ratelimit_windows"
const undefinedTable = "42P01"
const undefinedLimiterColumn = "42703"

// INSERT/ON CONFLICT serializes admission across instances. An absent key gets
// an expired zero-count placeholder; Allow repeats once to admit in the conflict
// branch, which samples time after acquiring the row lock. This also handles an
// insert waiting behind a transaction that rolls back: proposed INSERT values
// are evaluated before that wait. MATERIALIZED evaluates the decision clock once.
// Invalid legacy/policy state preserves every stored column and consumes no quota.
const allowSQL = `
WITH admission AS (
    INSERT INTO ratelimit_windows AS w
        (key, window_start, request_count, prev_count, last_allowed, updated_at, expires_at, window_ms)
    VALUES (@key::text, TIMESTAMPTZ 'epoch', 0, 0, FALSE,
            TIMESTAMPTZ 'epoch', TIMESTAMPTZ 'epoch', @window_ms::bigint)
    ON CONFLICT (key) DO UPDATE SET
        (window_start, request_count, prev_count, last_allowed, updated_at, expires_at, window_ms) = (
            WITH decision AS MATERIALIZED (
                SELECT greatest(floor(extract(epoch FROM clock_timestamp()) * 1000)::bigint,
                                floor(extract(epoch FROM w.updated_at) * 1000)::bigint) AS now_ms
            ), state AS (
                SELECT d.now_ms,
                       floor(extract(epoch FROM w.window_start) * 1000)::bigint AS start_ms,
                       w.window_ms <= 0 OR w.window_ms > @max_window_ms::bigint AS incompatible,
                       w.expires_at <= TIMESTAMPTZ 'epoch' + d.now_ms * INTERVAL '1 millisecond' AS expired
                FROM decision d
            ), buckets AS (
                SELECT s.now_ms,
                       s.incompatible OR (NOT s.expired AND w.window_ms <> @window_ms::bigint) AS preserve,
                       CASE WHEN s.expired THEN s.now_ms
                            ELSE s.start_ms + ((s.now_ms - s.start_ms) / @window_ms::bigint) * @window_ms::bigint END AS start_ms,
                       CASE WHEN s.expired OR s.now_ms - s.start_ms >= @window_ms::bigint
                            THEN 0 ELSE w.request_count END AS current_count,
                       CASE WHEN s.expired THEN 0
                            WHEN s.now_ms - s.start_ms >= 2 * @window_ms::bigint THEN 0
                            WHEN s.now_ms - s.start_ms >= @window_ms::bigint THEN w.request_count
                            ELSE w.prev_count END AS previous_count
                FROM state s
            ), estimate AS (
                SELECT b.*,
                       CASE WHEN b.preserve THEN 0
                            ELSE b.current_count + b.previous_count *
                                 (@window_ms::bigint - (b.now_ms - b.start_ms)) / @window_ms::bigint END AS effective
                FROM buckets b
            )
            SELECT CASE WHEN e.preserve THEN w.window_start ELSE TIMESTAMPTZ 'epoch' + e.start_ms * INTERVAL '1 millisecond' END,
                   CASE WHEN e.preserve THEN w.request_count ELSE e.current_count + CASE WHEN e.effective < @limit::bigint THEN 1 ELSE 0 END END,
                   CASE WHEN e.preserve THEN w.prev_count ELSE e.previous_count END,
                   CASE WHEN e.preserve THEN w.last_allowed ELSE e.effective < @limit::bigint END,
                   CASE WHEN e.preserve THEN w.updated_at ELSE TIMESTAMPTZ 'epoch' + e.now_ms * INTERVAL '1 millisecond' END,
                   CASE WHEN e.preserve THEN w.expires_at ELSE TIMESTAMPTZ 'epoch' + (e.start_ms + 2 * @window_ms::bigint) * INTERVAL '1 millisecond' END,
                   CASE WHEN e.preserve THEN w.window_ms ELSE @window_ms::bigint END
            FROM estimate e
        )
    RETURNING w.window_start, w.request_count, w.prev_count, w.last_allowed, w.updated_at, w.expires_at, w.window_ms
)
SELECT CASE WHEN a.window_ms <= 0 OR a.window_ms > @max_window_ms::bigint THEN 2
            WHEN a.expires_at = TIMESTAMPTZ 'epoch' AND a.request_count = 0 THEN 3
            WHEN a.window_ms <> @window_ms::bigint THEN 1 ELSE 0 END AS marker,
       a.last_allowed,
       CASE WHEN a.window_ms = @window_ms::bigint AND a.last_allowed THEN greatest(
            @limit::bigint - a.request_count - a.prev_count *
            (a.window_ms - (floor(extract(epoch FROM a.updated_at) * 1000)::bigint -
                           floor(extract(epoch FROM a.window_start) * 1000)::bigint)) / a.window_ms, 0)
            ELSE 0 END AS remaining,
       CASE WHEN a.window_ms = @window_ms::bigint THEN a.window_start + a.window_ms * INTERVAL '1 millisecond'
            ELSE TIMESTAMPTZ 'epoch' END AS reset_at,
       CASE WHEN a.window_ms = @window_ms::bigint AND NOT a.last_allowed THEN
            a.window_ms - (floor(extract(epoch FROM a.updated_at) * 1000)::bigint -
                           floor(extract(epoch FROM a.window_start) * 1000)::bigint)
            ELSE 0 END AS retry_ms
FROM admission a`

// A legacy collision is never deleted. Data-modifying CTEs execute even when
// their RETURNING rows are unused by the final compatibility check.
const resetSQL = `
WITH deleted AS (
    DELETE FROM ratelimit_windows
    WHERE key = @key AND window_ms > 0 AND window_ms <= @max_window_ms
    RETURNING key
)
SELECT EXISTS (
    SELECT 1 FROM ratelimit_windows
    WHERE key = @key AND (window_ms <= 0 OR window_ms > @max_window_ms)
)`

const limiterProbeSQL = `SELECT window_ms FROM ratelimit_windows LIMIT 0`

var _ ratelimiter.Limiter = (*Limiter)(nil)

// Limiter applies the shared two-window approximation in one atomic Postgres
// statement. The host owns the DB, reference schema and expired-row pruning.
type Limiter struct {
	db        *DB
	keyPrefix string
}

// LimiterOption configures construction of a Limiter. Options apply in order.
type LimiterOption func(*limiterConfig)

type limiterConfig struct {
	keyPrefix string
}

// WithLimiterKeyPrefix selects the host namespace (default "ratelimit:"). An
// internal "v2:" suffix is always appended, including to custom/empty prefixes.
func WithLimiterKeyPrefix(prefix string) LimiterOption {
	return func(cfg *limiterConfig) { cfg.keyPrefix = prefix }
}

// NewLimiter wraps the caller's DB. It creates no schema and owns no lifecycle.
// A nil option panics.
func NewLimiter(db *DB, opts ...LimiterOption) *Limiter {
	cfg := limiterConfig{keyPrefix: defaultLimiterKeyPrefix}
	for _, opt := range opts {
		if opt == nil {
			panic("pgxdb: nil LimiterOption")
		}
		opt(&cfg)
	}
	return &Limiter{db: db, keyPrefix: cfg.keyPrefix + limiterKeyVersion}
}

// Allow normalizes the policy and records a request for a nonempty key. The
// decision and returned retry checkpoint use the database's millisecond clock.
func (l *Limiter) Allow(ctx context.Context, key string, limit ratelimiter.Limit) (ratelimiter.Result, error) {
	if err := ctx.Err(); err != nil {
		return ratelimiter.Result{}, err
	}
	if key == "" {
		return ratelimiter.Result{}, fmt.Errorf("pgxdb: rate limit key is empty: %w", sdk.ErrInvalidInput)
	}
	normalized, err := limit.Normalize()
	if err != nil {
		return ratelimiter.Result{}, err
	}
	args := jackpgx.NamedArgs{"key": l.keyPrefix + key, "limit": int64(normalized.Requests + normalized.Burst), "window_ms": normalized.Window.Milliseconds(), "max_window_ms": ratelimiter.MaxWindow.Milliseconds()}
	// Initialization consumes no quota. A concurrent Reset/pruner may remove
	// the placeholder; bound that race to one repeat rather than spinning.
	for range 2 {
		if err := ctx.Err(); err != nil {
			return ratelimiter.Result{}, err
		}
		var marker int
		var allowed bool
		var remaining, retry int64
		var resetAt time.Time
		err = l.db.QueryRow(ctx, allowSQL, args).Scan(&marker, &allowed, &remaining, &resetAt, &retry)
		if ctx.Err() != nil {
			return ratelimiter.Result{}, ctx.Err()
		}
		if err != nil {
			return ratelimiter.Result{}, fmt.Errorf("pgxdb: evaluating rate limit: %w", MapError(err))
		}
		if marker == 3 {
			continue
		}
		if marker == 2 {
			return ratelimiter.Result{}, fmt.Errorf("pgxdb: incompatible rate limit state: %w", sdk.ErrConflict)
		}
		if marker == 1 {
			return ratelimiter.Result{}, fmt.Errorf("pgxdb: active rate limit window changed: %w", sdk.ErrInvalidInput)
		}
		ceiling := int64(normalized.Requests + normalized.Burst)
		if marker != 0 || remaining < 0 || remaining >= ceiling || retry < 0 || retry > normalized.Window.Milliseconds() || (allowed && retry != 0) || (!allowed && (remaining != 0 || retry == 0)) {
			return ratelimiter.Result{}, fmt.Errorf("pgxdb: invalid rate limit decision")
		}
		return ratelimiter.Result{Allowed: allowed, Remaining: int(remaining), ResetAt: resetAt.UTC(), RetryAfter: time.Duration(retry) * time.Millisecond}, nil
	}
	return ratelimiter.Result{}, fmt.Errorf("pgxdb: rate limit initialization interrupted: %w", sdk.ErrConflict)
}

// Reset clears compatible state for a nonempty key. A colliding legacy row is
// left intact and returns sdk.ErrConflict; an absent key succeeds.
func (l *Limiter) Reset(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if key == "" {
		return fmt.Errorf("pgxdb: rate limit key is empty: %w", sdk.ErrInvalidInput)
	}
	var incompatible bool
	err := l.db.QueryRow(ctx, resetSQL, jackpgx.NamedArgs{"key": l.keyPrefix + key, "max_window_ms": ratelimiter.MaxWindow.Milliseconds()}).Scan(&incompatible)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("pgxdb: resetting rate limit: %w", MapError(err))
	}
	if incompatible {
		return fmt.Errorf("pgxdb: incompatible rate limit state: %w", sdk.ErrConflict)
	}
	return nil
}

// StatusCheck probes the host-owned table and its required window_ms column.
// Missing schema reports sdk.ErrNotFound. An undeadlined context gets one second.
func (l *Limiter) StatusCheck(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if l.db == nil {
		return fmt.Errorf("pgxdb: rate limiter has no database connection: %w", sdk.ErrInvalidInput)
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Second)
		defer cancel()
	}
	_, err := l.db.Exec(ctx, limiterProbeSQL)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && (pgErr.Code == undefinedTable || pgErr.Code == undefinedLimiterColumn) {
			return fmt.Errorf("pgxdb: rate limit table %q or window_ms column is missing; apply the reference migration: %w", limiterTable, sdk.ErrNotFound)
		}
		return fmt.Errorf("pgxdb: checking rate limit table: %w", MapError(err))
	}
	return nil
}
