package pgxdb

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgreSQL error codes.
// See: https://www.postgresql.org/docs/current/errcodes-appendix.html
const (
	uniqueViolation     = "23505" // UNIQUE constraint violation
	foreignKeyViolation = "23503" // FOREIGN KEY constraint violation
	checkViolation      = "23514" // CHECK constraint violation
	notNullViolation    = "23502" // NOT NULL constraint violation
	undefinedTable      = "42P01" // Table does not exist
)

// Infrastructure-level error sentinels. Stores check these and convert
// them to repository-specific (domain) errors.
var (
	ErrDBNotFound            = pgx.ErrNoRows
	ErrDBDuplicatedEntry     = errors.New("duplicate entry")
	ErrDBForeignKeyViolation = errors.New("foreign key violation")
	ErrDBCheckViolation      = errors.New("check constraint violation")
	ErrDBNotNullViolation    = errors.New("not null violation")
	ErrUndefinedTable        = errors.New("undefined table")
)

// Pool is a type alias for pgxpool.Pool so callers don't need to import pgxpool directly.
type Pool = pgxpool.Pool

// Options holds the exportable database configuration.
// Use sdk/environment.ParseEnvTags to populate from environment variables.
//
//	var cfg pgxdb.Options
//	environment.ParseEnvTags("MYAPP", &cfg) // reads MYAPP_DB_DATABASE_URL, etc.
type Options struct {
	DatabaseURL string        `env:"DB_DATABASE_URL" required:"true"`
	MaxConns    int           `env:"DB_MAX_CONNS" default:"25"`
	MinConns    int           `env:"DB_MIN_CONNS" default:"5"`
	MaxLifetime time.Duration `env:"DB_MAX_LIFETIME" default:"1h"`
	MaxIdleTime time.Duration `env:"DB_MAX_IDLE_TIME" default:"30m"`
	HealthCheck time.Duration `env:"DB_HEALTH_CHECK" default:"1m"`
}

// options holds internal runtime configuration built from Options + functional overrides.
type options struct {
	databaseURL    string
	maxConns       int
	minConns       int
	maxLifetime    time.Duration
	maxIdleTime    time.Duration
	healthCheck    time.Duration
	logger         *slog.Logger
	tracer         pgx.QueryTracer
	connectTimeout time.Duration
	logQueries     bool
}

// Option is a function that configures internal options.
type Option func(*options)

// WithLogger sets a custom logger for the database.
func WithLogger(logger *slog.Logger) Option {
	return func(o *options) {
		o.logger = logger
	}
}

// WithTracer sets a custom query tracer.
func WithTracer(tracer pgx.QueryTracer) Option {
	return func(o *options) {
		o.tracer = tracer
	}
}

// WithDatabaseURL overrides the database URL from Options.
func WithDatabaseURL(url string) Option {
	return func(o *options) {
		o.databaseURL = url
	}
}

// WithMaxConns sets the maximum number of connections in the pool.
func WithMaxConns(max int) Option {
	return func(o *options) {
		o.maxConns = max
	}
}

// WithMinConns sets the minimum number of idle connections in the pool.
func WithMinConns(min int) Option {
	return func(o *options) {
		o.minConns = min
	}
}

// WithMaxLifetime sets the maximum lifetime of a connection before it is recycled.
func WithMaxLifetime(lifetime time.Duration) Option {
	return func(o *options) {
		o.maxLifetime = lifetime
	}
}

// WithMaxIdleTime sets the maximum idle time before a connection is closed.
func WithMaxIdleTime(idleTime time.Duration) Option {
	return func(o *options) {
		o.maxIdleTime = idleTime
	}
}

// WithHealthCheck sets the health check period for idle connections.
func WithHealthCheck(period time.Duration) Option {
	return func(o *options) {
		o.healthCheck = period
	}
}

// WithConnectTimeout sets the timeout for establishing the initial connection.
func WithConnectTimeout(timeout time.Duration) Option {
	return func(o *options) {
		o.connectTimeout = timeout
	}
}

// WithLogQueries enables or disables automatic query logging via LoggingQueryTracer.
func WithLogQueries(enable bool) Option {
	return func(o *options) {
		o.logQueries = enable
	}
}

// New creates a new connection pool from the given configuration and options.
func New(cfg Options, opts ...Option) (*Pool, error) {
	internalOpts := &options{
		databaseURL:    cfg.DatabaseURL,
		maxConns:       cfg.MaxConns,
		minConns:       cfg.MinConns,
		maxLifetime:    cfg.MaxLifetime,
		maxIdleTime:    cfg.MaxIdleTime,
		healthCheck:    cfg.HealthCheck,
		connectTimeout: 10 * time.Second,
		logQueries:     false,
	}

	for _, opt := range opts {
		opt(internalOpts)
	}

	if internalOpts.logger == nil {
		internalOpts.logger = slog.Default()
	}

	if internalOpts.tracer == nil && internalOpts.logQueries {
		internalOpts.tracer = NewMultiQueryTracer(
			NewLoggingQueryTracer(internalOpts.logger),
		)
	}

	return openPool(internalOpts)
}

// NewTestDB creates a connection pool with relaxed settings for tests.
func NewTestDB(connString string, opts ...Option) (*Pool, error) {
	cfg := Options{
		DatabaseURL: connString,
		MaxConns:    25,
		MinConns:    5,
		MaxLifetime: time.Hour,
		MaxIdleTime: time.Hour,
		HealthCheck: time.Hour,
	}
	return New(cfg, opts...)
}

func openPool(opts *options) (*Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(opts.databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parsing connection string: %w", err)
	}

	poolConfig.MaxConns = int32(opts.maxConns)
	poolConfig.MinConns = int32(opts.minConns)
	poolConfig.MaxConnLifetime = opts.maxLifetime
	poolConfig.MaxConnIdleTime = opts.maxIdleTime
	poolConfig.HealthCheckPeriod = opts.healthCheck

	if opts.tracer != nil {
		poolConfig.ConnConfig.Tracer = opts.tracer
	}

	ctx, cancel := context.WithTimeout(context.Background(), opts.connectTimeout)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("creating connection pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	return pool, nil
}

// StatusCheck returns nil if it can successfully talk to the database.
func StatusCheck(ctx context.Context, pool *Pool) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Second)
		defer cancel()
	}
	return pool.Ping(ctx)
}

// HandlePgError converts PostgreSQL errors to infrastructure-level sentinels.
// Stores call this and then map the result to domain errors.
func HandlePgError(err error) error {
	if err == nil {
		return nil
	}

	var pqerr *pgconn.PgError
	if errors.As(err, &pqerr) {
		switch pqerr.Code {
		case uniqueViolation:
			return ErrDBDuplicatedEntry
		case foreignKeyViolation:
			return ErrDBForeignKeyViolation
		case checkViolation:
			return ErrDBCheckViolation
		case notNullViolation:
			return ErrDBNotNullViolation
		case undefinedTable:
			return ErrUndefinedTable
		}
	}

	if errors.Is(err, pgx.ErrNoRows) {
		return ErrDBNotFound
	}

	return err
}
