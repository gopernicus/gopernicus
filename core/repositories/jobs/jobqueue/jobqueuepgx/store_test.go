//go:build integration

// This file is created once by gopernicus and will NOT be overwritten.
// Add custom integration tests for the JobQueue store here.
//
// The setupTestStore() helper in generated_test.go provides a test database
// and store instance. Use it as the basis for custom tests:
//
//	func TestStore_MyCustomQuery(t *testing.T) {
//		ctx, db, store := setupTestStore(t)
//		pgxfixtures.TruncatePublicSchema(t, ctx, db.Pool)
//		// ... test custom store methods
//	}

package jobqueuepgx

import (
	"context"
	"os"

	"github.com/gopernicus/gopernicus/infrastructure/database/postgres/pgxdb"
	"github.com/gopernicus/gopernicus/workshop/testing/testenv"
	"github.com/gopernicus/gopernicus/workshop/testing/testpgx"
)

// migrateTestDB applies this project's migrations to the test database, so
// tests run against the same schema as 'gopernicus db migrate'. Replace it
// if this store's tests need a different schema setup. testenv.ProjectRoot
// resolves the module root, so the migrations path works at any test working
// directory.
func migrateTestDB(ctx context.Context, pool *pgxdb.Pool) error {
	root, err := testenv.ProjectRoot()
	if err != nil {
		return err
	}
	return pgxdb.RunMigrations(ctx, pool, os.DirFS(root), "workshop/migrations/primary")
}

// testPGXOptions provides extra options to testpgx.SetupTestPGX in the
// generated setupTestStore helper. Use it to pick a Postgres image with
// required extensions, e.g.:
//
//	var testPGXOptions = []testpgx.Option{
//		testpgx.WithPostgresVersion("pgvector/pgvector:pg17"),
//		testpgx.WithExtensions("vector", "pg_trgm"),
//	}
var testPGXOptions []testpgx.Option
