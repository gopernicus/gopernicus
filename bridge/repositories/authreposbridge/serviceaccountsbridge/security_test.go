//go:build security

// This file is created once by gopernicus and will NOT be overwritten.

package serviceaccountsbridge

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/gopernicus/gopernicus/core/repositories/auth/serviceaccounts"
	"github.com/gopernicus/gopernicus/core/repositories/auth/serviceaccounts/serviceaccountspgx"

	"github.com/gopernicus/gopernicus/infrastructure/database/postgres/pgxdb"
	"github.com/gopernicus/gopernicus/infrastructure/ratelimiter"
	"github.com/gopernicus/gopernicus/infrastructure/ratelimiter/memorylimiter"
	"github.com/gopernicus/gopernicus/sdk/logger"
	"github.com/gopernicus/gopernicus/sdk/web"
	"github.com/gopernicus/gopernicus/workshop/testing/testauth"
	"github.com/gopernicus/gopernicus/workshop/testing/testhttp"
	"github.com/gopernicus/gopernicus/workshop/testing/testpgx"
)

// setupSecurityServer boots the full HTTP stack for ServiceAccount WITH the
// real authentication/authorization middleware (via testauth), so the
// generated probes exercise enforcement. Edit it if your routes need a
// different auth setup.
func setupSecurityServer(t *testing.T) *testhttp.Client {
	t.Helper()

	if testing.Short() {
		t.Skip("skipping security test")
	}

	ctx := context.Background()
	db := testpgx.SetupTestPGX(t, ctx, testpgx.WithMigrations(migrateSecurityDB))
	store := serviceaccountspgx.NewStore(logger.NewNoop(), db.Pool)
	repo := serviceaccounts.NewRepository(serviceaccounts.NewCacheStore(store, nil))

	limiter := ratelimiter.New(memorylimiter.New(), ratelimiter.NewDefaultResolver())
	authenticator, _ := testauth.Authenticator("securitytest")
	authorizer := testauth.Authorizer()

	bridge := NewBridge(logger.NewNoop(), repo, limiter, authenticator, authorizer)

	handler := web.NewWebHandler()
	bridge.AddHttpRoutes(handler.Group(""))

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return testhttp.New(srv.URL)
}

// migrateSecurityDB applies this project's migrations to the test database.
func migrateSecurityDB(ctx context.Context, pool *pgxdb.Pool) error {
	return pgxdb.RunMigrations(ctx, pool, os.DirFS(securityProjectRoot()), "workshop/migrations/primary")
}

// securityProjectRoot walks up to the directory containing go.mod.
func securityProjectRoot() string {
	_, file, _, _ := runtime.Caller(0)
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "."
		}
		dir = parent
	}
}
