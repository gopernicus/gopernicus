// Package pgxdb is the datastore connector for PostgreSQL: it bridges the
// pgx/v5 driver (pool via pgxpool) to a small wrapper (connection, tx,
// migrations). It is a reusable connector — it owns "how to talk to Postgres,"
// not any app's queries. App-specific repositories live in the app's
// providers/ and consume this package's *DB.
//
// It is its own module (github.com/gopernicus/gopernicus/integrations/datastores/pgxdb), depending
// only on sdk (for the errs sentinels MapError targets) and pgx/v5.
//
// Its exported surface mirrors the turso connector's by convention (Config /
// Open / DB / MapError / StatusCheck / RunMigrations). Nothing mechanically proves
// the two stay aligned — see the module README's non-guarantee note.
package pgxdb

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"time"

	"github.com/gopernicus/gopernicus/sdk"

	jackpgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgreSQL error codes MapError recognizes.
// See: https://www.postgresql.org/docs/current/errcodes-appendix.html
const (
	uniqueViolation     = "23505" // unique_violation
	foreignKeyViolation = "23503" // foreign_key_violation
	checkViolation      = "23514" // check_violation
	notNullViolation    = "23502" // not_null_violation
)

// Config holds the PostgreSQL connection settings. Hosts populate it directly
// or via env-tag helpers; Open never reads process environment itself. DSN wins
// over the split Host/Port/User/Password/Database/SSLMode fields.
//
// LogQueries, Logger, and Tracer are deliberate, interim exceptions to
// "no per-connector observability field": pgx exposes exactly one tracing
// seam (pgxpool.ConnConfig.Tracer), so Config forwards it directly rather
// than inventing an options wrapper for a single value. They hold until
// sdk/tracing lands. Query logging is symmetric with the turso connector: both
// carry an opt-in LogQueries/Logger with the same dev-only, args-verbatim
// posture — pgx installs it as a native ConnConfig.Tracer, turso threads it
// through its DB/Tx wrapper because database/sql exposes no tracer hook. Tracer
// has no turso analogue: it composes an external pgx.QueryTracer (e.g.
// OpenTelemetry) into that native seam, which SQLite's driver does not expose.
type Config struct {
	DSN      string `env:"DB_URL"`
	Host     string `env:"DB_HOST"`
	Port     string `env:"DB_PORT"`
	User     string `env:"DB_USER"`
	Password string `env:"DB_PASSWORD"`
	Database string `env:"DB_NAME"`
	SSLMode  string `env:"DB_SSLMODE"`

	MaxConns       int           `env:"DB_MAX_CONNS"`
	MinConns       int           `env:"DB_MIN_CONNS"`
	MaxLifetime    time.Duration `env:"DB_MAX_CONN_LIFETIME"`
	MaxIdleTime    time.Duration `env:"DB_MAX_CONN_IDLE_TIME"`
	ConnectTimeout time.Duration `env:"DB_CONNECT_TIMEOUT"`

	// HealthCheckPeriod sets pgxpool's idle-connection liveness check
	// interval. Applied only when non-zero, like MaxConns/MinConns.
	HealthCheckPeriod time.Duration `env:"DB_HEALTH_CHECK_PERIOD"`

	// LogQueries installs a LoggingQueryTracer. It logs SQL args verbatim, so
	// this is dev-only tooling.
	LogQueries bool `env:"DB_LOG_QUERIES" default:"false"`

	// Logger is used only when LogQueries is true. If nil, slog.Default() is
	// used. It is not populated by env parsers.
	Logger *slog.Logger

	// Tracer, when non-nil, is installed as poolConfig.ConnConfig.Tracer
	// before the pool is created. If LogQueries is also true, both tracers are
	// composed. See the asymmetry note above.
	Tracer jackpgx.QueryTracer

	// Retry, when its Attempts is > 1, makes Open verify boot connectivity with a
	// retried real round-trip (StatusCheck: Ping + SELECT 1) under a full-jitter
	// exponential backoff, targeting the orchestration race where the database is
	// not yet accepting connections. The zero value disables all retry: Open
	// pings exactly once (today's behavior). It is not populated by env parsers.
	//
	// This governs ONLY the boot connectivity check. No statement is ever
	// auto-retried by the connector — statement retry is store-owned, explicit,
	// and per-call, because a method verb does not encode idempotency
	// (Query/QueryRow carry RETURNING writes).
	Retry RetryPolicy
}

func (cfg Config) connectionString() (string, error) {
	if cfg.DSN != "" {
		return cfg.DSN, nil
	}
	if !cfg.hasSplitConnectionFields() {
		return "", fmt.Errorf("postgres: empty DSN")
	}

	host := cfg.Host
	if host == "" {
		host = "localhost"
	}
	port := cfg.Port
	if port == "" {
		port = "5432"
	}
	database := cfg.Database
	if database == "" {
		database = "postgres"
	}

	u := url.URL{
		Scheme: "postgres",
		Host:   net.JoinHostPort(host, port),
		Path:   database,
	}
	if cfg.User != "" {
		if cfg.Password != "" {
			u.User = url.UserPassword(cfg.User, cfg.Password)
		} else {
			u.User = url.User(cfg.User)
		}
	}
	if cfg.SSLMode != "" {
		q := u.Query()
		q.Set("sslmode", cfg.SSLMode)
		u.RawQuery = q.Encode()
	}

	return u.String(), nil
}

func (cfg Config) hasSplitConnectionFields() bool {
	return cfg.Host != "" ||
		cfg.Port != "" ||
		cfg.User != "" ||
		cfg.Password != "" ||
		cfg.Database != "" ||
		cfg.SSLMode != ""
}

// Redacted returns the connection target with any URL password masked.
func (cfg Config) Redacted() string {
	dsn, err := cfg.connectionString()
	if err != nil {
		return redactedDSN
	}
	return RedactDSN(dsn)
}

func (cfg Config) queryTracer() jackpgx.QueryTracer {
	tracer := cfg.Tracer
	if !cfg.LogQueries {
		return tracer
	}

	loggingTracer := NewLoggingQueryTracer(cfg.Logger)
	if tracer == nil {
		return loggingTracer
	}
	return NewMultiQueryTracer(tracer, loggingTracer)
}

// Open connects to a PostgreSQL database via a pgxpool and verifies it with a
// ping. Pool sizes are applied only when non-zero, leaving pgx's own defaults
// in place otherwise.
func Open(cfg Config) (*DB, error) {
	dsn, err := cfg.connectionString()
	if err != nil {
		return nil, err
	}
	if cfg.ConnectTimeout == 0 {
		cfg.ConnectTimeout = 10 * time.Second
	}

	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parsing connection string: %w", err)
	}
	if cfg.MaxConns > 0 {
		poolConfig.MaxConns = int32(cfg.MaxConns)
	}
	if cfg.MinConns > 0 {
		poolConfig.MinConns = int32(cfg.MinConns)
	}
	poolConfig.MaxConnLifetime = cfg.MaxLifetime
	poolConfig.MaxConnIdleTime = cfg.MaxIdleTime
	if cfg.HealthCheckPeriod > 0 {
		poolConfig.HealthCheckPeriod = cfg.HealthCheckPeriod
	}
	if tracer := cfg.queryTracer(); tracer != nil {
		poolConfig.ConnConfig.Tracer = tracer
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.ConnectTimeout)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("creating connection pool: %w", err)
	}
	db := &DB{pool: pool}

	// Retry runs the boot connectivity check (StatusCheck: Ping + SELECT 1) under
	// the policy's jittered backoff, targeting the orchestration race where the
	// pool cannot yet acquire a connection. The zero value keeps the single pool
	// ping exactly.
	if cfg.Retry.Attempts > 1 {
		if err := retry(ctx, cfg.Retry, func(ctx context.Context) error {
			return StatusCheck(ctx, db)
		}); err != nil {
			pool.Close()
			return nil, fmt.Errorf("verifying database: %w", err)
		}
		return db, nil
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}
	return db, nil
}

// MapError converts a pgx / PostgreSQL driver error into an sdk/errs sentinel.
// Detection is by SQLSTATE code via pgconn.PgError (vs turso's substring match
// on SQLite messages). Unrecognized errors pass through unchanged. Callers map
// both query errors and Scan errors (jackpgx.ErrNoRows → ErrNotFound) through this.
func MapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, jackpgx.ErrNoRows) {
		return sdk.ErrNotFound
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case uniqueViolation:
			return sdk.ErrAlreadyExists
		case foreignKeyViolation:
			return sdk.ErrInvalidReference
		case checkViolation, notNullViolation:
			return sdk.ErrInvalidInput
		}
	}
	return err
}
