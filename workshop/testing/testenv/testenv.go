// Package testenv provides a composite test environment that wires together
// all infrastructure for integration tests: database, cache, event bus, and logger.
package testenv

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/infrastructure/cache"
	"github.com/gopernicus/gopernicus/infrastructure/cache/memorycache"
	"github.com/gopernicus/gopernicus/infrastructure/database/postgres/pgxdb"
	"github.com/gopernicus/gopernicus/infrastructure/events"
	"github.com/gopernicus/gopernicus/infrastructure/events/memorybus"
	"github.com/gopernicus/gopernicus/workshop/testing/testpgx"
	"github.com/gopernicus/gopernicus/workshop/testing/testredis"
	"github.com/gopernicus/gopernicus/sdk/logger"
)

// TestEnv holds a fully wired test environment for integration tests.
type TestEnv struct {
	PGX      *testpgx.TestPGX
	Redis    *testredis.TestRedis // nil if Redis not enabled
	EventBus events.Bus
	Cache    cache.Cacher
	Log      *slog.Logger
}

// config holds internal options for New.
type config struct {
	migrateFn       testpgx.MigrateFunc
	enableRedis     bool
	seedFn          func(*pgxdb.Pool)
	logLevel        string
	postgresVersion string
	extensions      []string
}

// Option configures the test environment.
type Option func(*config)

// WithMigrations sets a migration function to run after database setup.
func WithMigrations(fn testpgx.MigrateFunc) Option {
	return func(c *config) {
		c.migrateFn = fn
	}
}

// WithRedis enables a Redis container for the test environment.
// When disabled (default), uses in-memory cache.
func WithRedis() Option {
	return func(c *config) {
		c.enableRedis = true
	}
}

// WithSeed runs a function to seed data after migrations complete.
func WithSeed(fn func(*pgxdb.Pool)) Option {
	return func(c *config) {
		c.seedFn = fn
	}
}

// WithLogLevel sets the logger level. Default: "ERROR".
func WithLogLevel(level string) Option {
	return func(c *config) {
		c.logLevel = level
	}
}

// WithPostgresVersion sets the Postgres Docker image tag.
func WithPostgresVersion(version string) Option {
	return func(c *config) {
		c.postgresVersion = version
	}
}

// WithExtensions adds PostgreSQL extensions beyond the default (pg_trgm).
func WithExtensions(extensions ...string) Option {
	return func(c *config) {
		c.extensions = append(c.extensions, extensions...)
	}
}

// New creates a fully wired test environment with database, event bus, cache, and logger.
// All cleanup is registered via t.Cleanup() automatically.
func New(t *testing.T, ctx context.Context, opts ...Option) *TestEnv {
	t.Helper()

	cfg := &config{
		logLevel: "ERROR",
	}
	for _, opt := range opts {
		opt(cfg)
	}

	// Create logger.
	log := logger.New(logger.Options{Level: cfg.logLevel})

	// Setup PostgreSQL via testcontainers.
	var pgxOpts []testpgx.Option
	if cfg.migrateFn != nil {
		pgxOpts = append(pgxOpts, testpgx.WithMigrations(cfg.migrateFn))
	}
	if cfg.postgresVersion != "" {
		pgxOpts = append(pgxOpts, testpgx.WithPostgresVersion(cfg.postgresVersion))
	}
	if len(cfg.extensions) > 0 {
		pgxOpts = append(pgxOpts, testpgx.WithExtensions(cfg.extensions...))
	}

	pgx := testpgx.SetupTestPGX(t, ctx, pgxOpts...)

	// Seed data if provided.
	if cfg.seedFn != nil {
		cfg.seedFn(pgx.Pool)
	}

	// Setup Redis if enabled.
	var testRedis *testredis.TestRedis
	if cfg.enableRedis {
		testRedis = testredis.SetupTestRedis(t, ctx)
	}

	// Create event bus (memory, synchronous-capable for deterministic tests).
	eventBus := memorybus.New(log)

	// Create cache (always in-memory for integration tests).
	memoryCacheStore := memorycache.New(memorycache.Config{MaxEntries: 10000})

	// Register cache and event bus cleanup.
	t.Cleanup(func() {
		memoryCacheStore.Close()
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		eventBus.Close(closeCtx)
	})

	return &TestEnv{
		PGX:      pgx,
		Redis:    testRedis,
		EventBus: eventBus,
		Cache:    memoryCacheStore,
		Log:      log,
	}
}
