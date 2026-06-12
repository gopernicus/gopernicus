// gopernicus:bootstrap kind=bridge-e2e/e2e_test.go template=f3f0bd6ea4c3
//go:build e2e

// This file is created once by gopernicus and will NOT be overwritten.
// Add custom e2e tests for VerificationToken routes here. The
// setupE2EServer() helper in generated_e2e_test.go provides a fresh stack.

package verificationtokensbridge

import (
	"context"
	"github.com/gopernicus/gopernicus/infrastructure/database/postgres/pgxdb"
	"github.com/gopernicus/gopernicus/workshop/testing/testenv"
	"os"
)

// migrateE2EDB applies this project's migrations to the e2e test database.
// testenv.ProjectRoot resolves the module root, so the migrations path works
// at any test working directory.
func migrateE2EDB(ctx context.Context, pool *pgxdb.Pool) error {
	root, err := testenv.ProjectRoot()
	if err != nil {
		return err
	}
	return pgxdb.RunMigrations(ctx, pool, os.DirFS(root), "workshop/migrations/primary")
}
