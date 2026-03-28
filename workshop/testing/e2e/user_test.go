//go:build e2e

// This file is created once by gopernicus and will NOT be overwritten.
// Add custom E2E tests here.
//
// setupTestServer() is shared across all E2E tests in this package.
// Implement it with your app's server factory:
//
//	func setupTestServer(t *testing.T) (context.Context, *testpgx.TestPGX, *testserver.TestServer) {
//		t.Helper()
//		ctx := context.Background()
//		cfg := testserver.DefaultConfig()
//		cfg.MigrateFn = migrateTestDB
//		ts := testserver.New(t, ctx, cfg, func(ctx context.Context, log *slog.Logger, infra testserver.Infrastructure) (http.Handler, error) {
//			return yourapp.NewHandler(ctx, log, infra.Pool)
//		})
//		return ctx, ts.PGX, ts
//	}

package e2e

import (
	"context"
	"testing"

	"github.com/gopernicus/gopernicus/workshop/testing/testpgx"
	"github.com/gopernicus/gopernicus/workshop/testing/testserver"
)

func setupTestServer(t *testing.T) (context.Context, *testpgx.TestPGX, *testserver.TestServer) {
	t.Helper()
	// TODO: Wire up your application server.
	panic("setupTestServer not implemented — see comments above")
}
