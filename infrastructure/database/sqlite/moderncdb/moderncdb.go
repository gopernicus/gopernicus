// Package moderncdb provides a SQLite database wrapper for the infrastructure layer.
// It uses modernc.org/sqlite which is a pure Go implementation (no CGO required).
// This is useful for single-instance deployments, edge computing, and testing.
package moderncdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite" // SQLite driver
)

// Common errors.
var (
	ErrNotFound         = sql.ErrNoRows
	ErrDuplicateEntry   = errors.New("duplicate entry")
	ErrConstraintFailed = errors.New("constraint failed")
)

// DB wraps the SQLite database connection with optional tracing.
type DB struct {
	db     *sql.DB
	tracer *OTelTracer
}

// Options represents the exportable SQLite configuration.
type Options struct {
	// Path to the SQLite database file.
	// Use ":memory:" for in-memory database.
	// Use "file::memory:?cache=shared" for shared in-memory database.
	Path string

	// Maximum number of open connections.
	MaxOpenConns int

	// Maximum number of idle connections.
	MaxIdleConns int

	// Maximum lifetime of a connection.
	ConnMaxLifetime time.Duration

	// Enable WAL mode for better concurrent reads.
	WALMode bool

	// Enable foreign key constraints.
	ForeignKeys bool

	// Busy timeout in milliseconds.
	BusyTimeout int
}

// options holds the internal runtime configuration.
type options struct {
	path            string
	maxOpenConns    int
	maxIdleConns    int
	connMaxLifetime time.Duration
	walMode         bool
	foreignKeys     bool
	busyTimeout     int
	connectTimeout  time.Duration
	tracer          *OTelTracer
}

// Option is a function that configures the SQLite options.
type Option func(*options)

// WithPath overrides the database path.
func WithPath(path string) Option {
	return func(o *options) { o.path = path }
}

// WithMaxOpenConns sets the maximum number of open connections.
func WithMaxOpenConns(n int) Option {
	return func(o *options) { o.maxOpenConns = n }
}

// WithMaxIdleConns sets the maximum number of idle connections.
func WithMaxIdleConns(n int) Option {
	return func(o *options) { o.maxIdleConns = n }
}

// WithConnMaxLifetime sets the maximum connection lifetime.
func WithConnMaxLifetime(d time.Duration) Option {
	return func(o *options) { o.connMaxLifetime = d }
}

// WithWALMode enables or disables WAL mode.
func WithWALMode(enabled bool) Option {
	return func(o *options) { o.walMode = enabled }
}

// WithForeignKeys enables or disables foreign key constraints.
func WithForeignKeys(enabled bool) Option {
	return func(o *options) { o.foreignKeys = enabled }
}

// WithBusyTimeout sets the busy timeout in milliseconds.
func WithBusyTimeout(ms int) Option {
	return func(o *options) { o.busyTimeout = ms }
}

// WithConnectTimeout sets the connection timeout.
func WithConnectTimeout(d time.Duration) Option {
	return func(o *options) { o.connectTimeout = d }
}

// WithTracer sets the OpenTelemetry tracer for query tracing.
func WithTracer(tracer *OTelTracer) Option {
	return func(o *options) { o.tracer = tracer }
}

// New creates a new SQLite database connection with given config and applies options.
func New(cfg Options, opts ...Option) (*DB, error) {
	internalOpts := &options{
		path:            cfg.Path,
		maxOpenConns:    cfg.MaxOpenConns,
		maxIdleConns:    cfg.MaxIdleConns,
		connMaxLifetime: cfg.ConnMaxLifetime,
		walMode:         cfg.WALMode,
		foreignKeys:     cfg.ForeignKeys,
		busyTimeout:     cfg.BusyTimeout,
		connectTimeout:  10 * time.Second,
	}

	for _, opt := range opts {
		opt(internalOpts)
	}

	return openDatabase(internalOpts)
}

// NewInMemory creates a new in-memory SQLite database.
func NewInMemory(opts ...Option) (*DB, error) {
	cfg := Options{
		Path:         ":memory:",
		MaxOpenConns: 1,
		MaxIdleConns: 1,
		WALMode:      false, // WAL mode doesn't work with in-memory
		ForeignKeys:  true,
		BusyTimeout:  5000,
	}
	return New(cfg, opts...)
}

// NewFile creates a new file-based SQLite database.
func NewFile(path string, opts ...Option) (*DB, error) {
	cfg := Options{
		Path:         path,
		MaxOpenConns: 1, // SQLite works best with single writer
		MaxIdleConns: 1,
		WALMode:      true,
		ForeignKeys:  true,
		BusyTimeout:  5000,
	}
	return New(cfg, opts...)
}

// openDatabase creates the actual database connection.
func openDatabase(opts *options) (*DB, error) {
	dsn := opts.path
	if dsn != ":memory:" && !strings.HasPrefix(dsn, "file:") {
		dsn = "file:" + dsn
	}

	// Add query parameters for pragmas
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}

	pragmas := fmt.Sprintf("%s_busy_timeout=%d", sep, opts.busyTimeout)
	if opts.foreignKeys {
		pragmas += "&_foreign_keys=on"
	}
	dsn += pragmas

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite database: %w", err)
	}

	db.SetMaxOpenConns(opts.maxOpenConns)
	db.SetMaxIdleConns(opts.maxIdleConns)
	db.SetConnMaxLifetime(opts.connMaxLifetime)

	ctx, cancel := context.WithTimeout(context.Background(), opts.connectTimeout)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("pinging sqlite database: %w", err)
	}

	// Enable WAL mode if requested (must be done on existing connection)
	if opts.walMode && opts.path != ":memory:" {
		if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
			db.Close()
			return nil, fmt.Errorf("enabling WAL mode: %w", err)
		}
	}

	return &DB{db: db, tracer: opts.tracer}, nil
}

// Close closes the database connection.
func (d *DB) Close() error {
	return d.db.Close()
}

// Ping checks if the database is reachable.
func (d *DB) Ping(ctx context.Context) error {
	return d.db.PingContext(ctx)
}

// Underlying returns the underlying sql.DB for advanced operations.
func (d *DB) Underlying() *sql.DB {
	return d.db
}

// StatusCheck returns nil if it can successfully talk to the database.
func StatusCheck(ctx context.Context, db *DB) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Second)
		defer cancel()
	}
	return db.Ping(ctx)
}

// =============================================================================
// Query Operations
// =============================================================================

// Exec executes a query that doesn't return rows.
func (d *DB) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if d.tracer != nil && d.tracer.tracer != nil {
		operation := extractSQLOperation(query)
		var span spanHandle
		ctx, span = d.tracer.startSpan(ctx, operation, query)
		defer span.End()

		result, err := d.db.ExecContext(ctx, query, args...)
		if err != nil {
			span.RecordError(err)
			return nil, handleSQLiteError(err)
		}

		if rowsAffected, raErr := result.RowsAffected(); raErr == nil && rowsAffected > 0 {
			span.SetRowsAffected(rowsAffected)
		}

		return result, nil
	}

	result, err := d.db.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, handleSQLiteError(err)
	}
	return result, nil
}

// Query executes a query that returns rows.
func (d *DB) Query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if d.tracer != nil && d.tracer.tracer != nil {
		operation := extractSQLOperation(query)
		var span spanHandle
		ctx, span = d.tracer.startSpan(ctx, operation, query)
		defer span.End()

		rows, err := d.db.QueryContext(ctx, query, args...)
		if err != nil {
			span.RecordError(err)
			return nil, handleSQLiteError(err)
		}
		return rows, nil
	}

	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, handleSQLiteError(err)
	}
	return rows, nil
}

// QueryRow executes a query that returns at most one row.
func (d *DB) QueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	if d.tracer != nil && d.tracer.tracer != nil {
		operation := extractSQLOperation(query)
		var span spanHandle
		ctx, span = d.tracer.startSpan(ctx, operation, query)
		defer span.End()

		return d.db.QueryRowContext(ctx, query, args...)
	}

	return d.db.QueryRowContext(ctx, query, args...)
}

// =============================================================================
// Schema Operations
// =============================================================================

// ExecSchema executes multiple SQL statements (for migrations/setup).
func (d *DB) ExecSchema(ctx context.Context, schema string) error {
	_, err := d.db.ExecContext(ctx, schema)
	if err != nil {
		return fmt.Errorf("executing schema: %w", err)
	}
	return nil
}

// TableExists checks if a table exists in the database.
func (d *DB) TableExists(ctx context.Context, tableName string) (bool, error) {
	var name string
	err := d.db.QueryRowContext(ctx,
		"SELECT name FROM sqlite_master WHERE type='table' AND name=?",
		tableName,
	).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking table exists: %w", err)
	}
	return true, nil
}

// =============================================================================
// Error Handling
// =============================================================================

// handleSQLiteError converts SQLite errors to application errors.
func handleSQLiteError(err error) error {
	if err == nil {
		return nil
	}

	errMsg := err.Error()

	if strings.Contains(errMsg, "UNIQUE constraint failed") {
		return ErrDuplicateEntry
	}
	if strings.Contains(errMsg, "FOREIGN KEY constraint failed") {
		return ErrConstraintFailed
	}
	if strings.Contains(errMsg, "CHECK constraint failed") {
		return ErrConstraintFailed
	}
	if strings.Contains(errMsg, "NOT NULL constraint failed") {
		return ErrConstraintFailed
	}

	return err
}
