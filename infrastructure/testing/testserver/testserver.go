// Package testserver provides a base test server for E2E tests.
// It wires up the full application stack with testcontainers infrastructure
// and exposes URL helpers for making HTTP requests against the test server.
//
// Apps extend this by embedding TestServer and adding domain-specific helpers
// (e.g., CreateTestUser, CreatePlatformAdmin) in their own testserver package.
package testserver

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gopernicus/gopernicus/infrastructure/database/postgres/pgxdb"
	"github.com/gopernicus/gopernicus/infrastructure/testing/testpgx"
	"github.com/gopernicus/gopernicus/infrastructure/testing/testredis"
	"github.com/gopernicus/gopernicus/sdk/logger"
	"github.com/stretchr/testify/require"
)

// ServerFactory creates an HTTP handler from the given infrastructure.
// Apps implement this to wire up their server with the test infrastructure.
type ServerFactory func(ctx context.Context, log *slog.Logger, infra Infrastructure) (http.Handler, error)

// Infrastructure holds the test infrastructure provided to the ServerFactory.
type Infrastructure struct {
	Pool *pgxdb.Pool
	// RedisAddr is the Redis address, empty if Redis is not enabled.
	RedisAddr string
}

// TestServer holds a fully wired test server for E2E testing.
type TestServer struct {
	Server *httptest.Server
	PGX    *testpgx.TestPGX
	Redis  *testredis.TestRedis // nil if Redis not enabled
	Log    *slog.Logger
}

// Config holds configuration for the test server.
type Config struct {
	// EnableRedis enables a Redis container.
	// When false (default), uses in-memory stores for faster test setup.
	EnableRedis bool
	// MigrateFn runs database migrations after connecting.
	MigrateFn testpgx.MigrateFunc
}

// DefaultConfig returns the default test server configuration.
func DefaultConfig() Config {
	return Config{
		EnableRedis: false,
	}
}

// New creates a new test server with the full application stack.
// The serverFn is called with test infrastructure to create the HTTP handler.
func New(t *testing.T, ctx context.Context, cfg Config, serverFn ServerFactory) *TestServer {
	t.Helper()

	// Create logger (silent for tests unless debugging).
	log := logger.New(logger.Options{Level: "ERROR"})

	// Set up test database with testcontainers.
	var pgxOpts []testpgx.Option
	if cfg.MigrateFn != nil {
		pgxOpts = append(pgxOpts, testpgx.WithMigrations(cfg.MigrateFn))
	}
	pgx := testpgx.SetupTestPGX(t, ctx, pgxOpts...)

	// Set up Redis if enabled.
	var testRedis *testredis.TestRedis
	infra := Infrastructure{
		Pool: pgx.Pool,
	}
	if cfg.EnableRedis {
		testRedis = testredis.SetupTestRedis(t, ctx)
		infra.RedisAddr = testRedis.Addr
	}

	// Create HTTP handler via the app's server factory.
	handler, err := serverFn(ctx, log, infra)
	require.NoError(t, err, "failed to create server")

	// Create httptest server.
	httpServer := httptest.NewServer(handler)

	t.Cleanup(func() {
		httpServer.Close()
	})

	return &TestServer{
		Server: httpServer,
		PGX:    pgx,
		Redis:  testRedis,
		Log:    log,
	}
}

// URL returns the base URL of the test server.
func (ts *TestServer) URL() string {
	return ts.Server.URL
}

// APIURL returns the full URL for an API endpoint (e.g., /api/v1{path}).
func (ts *TestServer) APIURL(path string) string {
	return fmt.Sprintf("%s/api/v1%s", ts.Server.URL, path)
}

// AuthURL returns the full URL for an authentication endpoint (e.g., /api/v1/auth{path}).
func (ts *TestServer) AuthURL(path string) string {
	return fmt.Sprintf("%s/api/v1/auth%s", ts.Server.URL, path)
}
