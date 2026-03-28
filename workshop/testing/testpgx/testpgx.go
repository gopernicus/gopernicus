// Package testpgx provides test PostgreSQL setup utilities using testcontainers.
// This package centralizes all test database infrastructure for integration tests.
package testpgx

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/infrastructure/database/postgres/pgxdb"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tclog "github.com/testcontainers/testcontainers-go/log"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// noopLogger silences testcontainers output.
type noopLogger struct{}

func (n noopLogger) Printf(_ string, _ ...any) {}

func init() {
	// Silence testcontainers logging by default.
	// Set TESTCONTAINERS_VERBOSE=true to see container logs.
	if os.Getenv("TESTCONTAINERS_VERBOSE") != "true" {
		tclog.SetDefault(&noopLogger{})
	}
}

// MigrateFunc is a function that runs database migrations against a pool.
type MigrateFunc func(ctx context.Context, pool *pgxdb.Pool) error

// TestPGX represents a test PostgreSQL setup with cleanup.
type TestPGX struct {
	Pool      *pgxdb.Pool
	Container testcontainers.Container
	ConnStr   string
}

// config holds internal options for SetupTestPGX.
type config struct {
	postgresVersion string
	extensions      []string
	migrateFn       MigrateFunc
}

// Option configures the test PostgreSQL setup.
type Option func(*config)

// WithPostgresVersion sets the Postgres Docker image tag.
// Default: "postgres:17-alpine".
func WithPostgresVersion(version string) Option {
	return func(c *config) {
		c.postgresVersion = version
	}
}

// WithExtensions adds PostgreSQL extensions to enable after connection.
// Default: pg_trgm is always enabled.
func WithExtensions(extensions ...string) Option {
	return func(c *config) {
		c.extensions = append(c.extensions, extensions...)
	}
}

// WithMigrations sets a migration function to run after connecting.
func WithMigrations(fn MigrateFunc) Option {
	return func(c *config) {
		c.migrateFn = fn
	}
}

// SetupTestPGX creates a PostgreSQL container, connects, and returns a pool.
// It automatically registers cleanup with t.Cleanup().
func SetupTestPGX(t *testing.T, ctx context.Context, opts ...Option) *TestPGX {
	t.Helper()

	cfg := &config{
		postgresVersion: "postgres:17-alpine",
		extensions:      []string{"pg_trgm"},
	}
	for _, opt := range opts {
		opt(cfg)
	}

	// Start Postgres container.
	pgContainer, err := postgres.Run(ctx,
		cfg.postgresVersion,
		postgres.WithDatabase("test-db"),
		postgres.WithUsername("postgres"),
		postgres.WithPassword("postgres"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(30*time.Second)),
	)
	require.NoError(t, err, "failed to start Postgres container")

	// Get connection string.
	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err, "failed to get connection string")

	// Connect to the database.
	pool, err := pgxdb.NewTestDB(connStr)
	require.NoError(t, err, "failed to connect to database")

	// Enable extensions.
	for _, ext := range cfg.extensions {
		_, err = pool.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS "+ext)
		require.NoError(t, err, "failed to enable extension %s", ext)
	}

	// Run migrations if provided.
	if cfg.migrateFn != nil {
		err = cfg.migrateFn(ctx, pool)
		require.NoError(t, err, "failed to run migrations")
	}

	tpgx := &TestPGX{
		Pool:      pool,
		Container: pgContainer,
		ConnStr:   connStr,
	}

	t.Cleanup(func() {
		tpgx.Cleanup(t)
	})

	return tpgx
}

// Cleanup closes the pool and terminates the container.
func (tp *TestPGX) Cleanup(t *testing.T) {
	t.Helper()

	if tp.Pool != nil {
		tp.Pool.Close()
	}

	if tp.Container != nil {
		terminateCtx, terminateCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer terminateCancel()

		if err := tp.Container.Terminate(terminateCtx); err != nil {
			t.Logf("warning: failed to terminate Postgres container: %s", err)
		}
	}
}
