// Package sqlitelimiter provides a SQLite-backed rate limiter implementation.
// Suitable for single-instance production deployments where rate limit state should
// persist across restarts but distributed enforcement is not required.
//
// The store uses a sliding window algorithm and manages its own table creation
// and background cleanup of expired entries.
//
// For multi-instance deployments, use goredislimiter instead.
package sqlitelimiter

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/gopernicus/gopernicus/infrastructure/ratelimiter"
)

// SQLExecutor defines the minimal SQL interface needed by this store.
// Satisfied by *sql.DB, *sql.Tx, and infrastructure/database/sqlite/moderncdb.DB.
type SQLExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

const defaultTableName = "rate_limits"
const defaultCleanupInterval = time.Minute

const createTableSQL = `
CREATE TABLE IF NOT EXISTS %s (
    key TEXT PRIMARY KEY,
    count INTEGER NOT NULL DEFAULT 0,
    window_start INTEGER NOT NULL,
    window_size_ns INTEGER NOT NULL,
    prev_count INTEGER NOT NULL DEFAULT 0,
    prev_window_start INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL
)`

const createIndexSQL = `CREATE INDEX IF NOT EXISTS idx_%s_updated_at ON %s(updated_at)`

// Limiter implements ratelimiter.Storer using SQLite.
type Limiter struct {
	db        SQLExecutor
	tableName string
	log       *slog.Logger

	mu              sync.Mutex
	closed          bool
	stopCleanup     chan struct{}
	cleanupInterval time.Duration
}

// Option configures the Limiter.
type Option func(*Limiter)

// WithTableName sets the table name for rate limit entries. Default: "rate_limits".
func WithTableName(name string) Option {
	return func(l *Limiter) {
		l.tableName = name
	}
}

// WithCleanupInterval sets how often expired entries are removed. Default: 1 minute.
func WithCleanupInterval(d time.Duration) Option {
	return func(l *Limiter) {
		l.cleanupInterval = d
	}
}

// WithLogger sets a logger for background cleanup errors. If not set, errors are silently ignored.
func WithLogger(log *slog.Logger) Option {
	return func(l *Limiter) {
		l.log = log
	}
}

// New creates a new SQLite-backed rate limiter and ensures the table exists.
func New(db SQLExecutor, opts ...Option) (*Limiter, error) {
	l := &Limiter{
		db:              db,
		tableName:       defaultTableName,
		cleanupInterval: defaultCleanupInterval,
		stopCleanup:     make(chan struct{}),
	}
	for _, opt := range opts {
		opt(l)
	}
	if err := l.ensureTable(context.Background()); err != nil {
		return nil, fmt.Errorf("sqlitelimiter: creating table: %w", err)
	}
	go l.cleanup()
	return l, nil
}

// Allow checks if a request should be allowed for the given key.
func (l *Limiter) Allow(ctx context.Context, key string, limit ratelimiter.Limit) (ratelimiter.Result, error) {
	if err := ctx.Err(); err != nil {
		return ratelimiter.Result{}, err
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return ratelimiter.Result{}, ratelimiter.ErrLimiterClosed
	}

	now := time.Now()
	nowUnix := now.UnixNano()
	effectiveLimit := limit.Requests + limit.Burst
	windowNs := limit.Window.Nanoseconds()

	var count, prevCount int
	var windowStart, prevWindowStart, windowSizeNs int64

	err := l.db.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT count, window_start, window_size_ns, prev_count, prev_window_start FROM %s WHERE key = ?`, l.tableName),
		key,
	).Scan(&count, &windowStart, &windowSizeNs, &prevCount, &prevWindowStart)

	if err == sql.ErrNoRows {
		_, err = l.db.ExecContext(ctx,
			fmt.Sprintf(`INSERT INTO %s (key, count, window_start, window_size_ns, prev_count, prev_window_start, updated_at) VALUES (?, 1, ?, ?, 0, 0, ?)`, l.tableName),
			key, nowUnix, windowNs, nowUnix,
		)
		if err != nil {
			return ratelimiter.Result{}, fmt.Errorf("sqlitelimiter: inserting entry: %w", err)
		}
		return ratelimiter.Result{
			Allowed:   true,
			Remaining: effectiveLimit - 1,
			ResetAt:   now.Add(limit.Window),
		}, nil
	}
	if err != nil {
		return ratelimiter.Result{}, fmt.Errorf("sqlitelimiter: querying entry: %w", err)
	}

	windowEnd := windowStart + windowNs
	if nowUnix > windowEnd {
		if nowUnix < windowEnd+windowNs {
			prevCount = count
			prevWindowStart = windowStart
		} else {
			prevCount = 0
			prevWindowStart = 0
		}
		count = 0
		windowStart = nowUnix
		windowEnd = nowUnix + windowNs
	}

	effectiveCount := count
	if prevWindowStart > 0 {
		elapsed := nowUnix - windowStart
		weight := float64(windowNs-elapsed) / float64(windowNs)
		if weight > 0 {
			effectiveCount += int(float64(prevCount) * weight)
		}
	}

	if effectiveCount >= effectiveLimit {
		_, err = l.db.ExecContext(ctx,
			fmt.Sprintf(`UPDATE %s SET count = ?, window_start = ?, window_size_ns = ?, prev_count = ?, prev_window_start = ?, updated_at = ? WHERE key = ?`, l.tableName),
			count, windowStart, windowNs, prevCount, prevWindowStart, nowUnix, key,
		)
		if err != nil {
			return ratelimiter.Result{}, fmt.Errorf("sqlitelimiter: updating entry: %w", err)
		}
		retryAfter := time.Duration(windowEnd - nowUnix)
		if retryAfter < 0 {
			retryAfter = 0
		}
		return ratelimiter.Result{
			Allowed:    false,
			Remaining:  0,
			ResetAt:    time.Unix(0, windowEnd),
			RetryAfter: retryAfter,
		}, nil
	}

	count++
	_, err = l.db.ExecContext(ctx,
		fmt.Sprintf(`UPDATE %s SET count = ?, window_start = ?, window_size_ns = ?, prev_count = ?, prev_window_start = ?, updated_at = ? WHERE key = ?`, l.tableName),
		count, windowStart, windowNs, prevCount, prevWindowStart, nowUnix, key,
	)
	if err != nil {
		return ratelimiter.Result{}, fmt.Errorf("sqlitelimiter: updating entry: %w", err)
	}

	remaining := effectiveLimit - effectiveCount - 1
	if remaining < 0 {
		remaining = 0
	}

	return ratelimiter.Result{
		Allowed:   true,
		Remaining: remaining,
		ResetAt:   time.Unix(0, windowEnd),
	}, nil
}

// Reset clears the rate limit state for a key.
func (l *Limiter) Reset(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return ratelimiter.ErrLimiterClosed
	}

	_, err := l.db.ExecContext(ctx,
		fmt.Sprintf(`DELETE FROM %s WHERE key = ?`, l.tableName),
		key,
	)
	if err != nil {
		return fmt.Errorf("sqlitelimiter: deleting entry: %w", err)
	}
	return nil
}

// Close stops the background cleanup goroutine.
func (l *Limiter) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return nil
	}
	l.closed = true
	close(l.stopCleanup)
	return nil
}

func (l *Limiter) ensureTable(ctx context.Context) error {
	if _, err := l.db.ExecContext(ctx, fmt.Sprintf(createTableSQL, l.tableName)); err != nil {
		return err
	}
	_, err := l.db.ExecContext(ctx, fmt.Sprintf(createIndexSQL, l.tableName, l.tableName))
	return err
}

func (l *Limiter) cleanup() {
	ticker := time.NewTicker(l.cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-l.stopCleanup:
			return
		case <-ticker.C:
			l.cleanupExpired()
		}
	}
}

func (l *Limiter) cleanupExpired() {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	now := time.Now().UnixNano()
	_, err := l.db.ExecContext(ctx,
		fmt.Sprintf(`DELETE FROM %s WHERE (window_start + 2 * window_size_ns) < ?`, l.tableName),
		now,
	)
	if err != nil && l.log != nil {
		l.log.Warn("sqlitelimiter: cleanup failed", "error", err, "table", l.tableName)
	}
}

// Compile-time interface check.
var _ ratelimiter.Storer = (*Limiter)(nil)
