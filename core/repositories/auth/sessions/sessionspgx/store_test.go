//go:build integration

// This file is created once by gopernicus and will NOT be overwritten.
// Add custom integration tests for the Session store here.
//
// The setupTestStore() helper in generated_test.go provides a test database
// and store instance. Use it as the basis for custom tests:
//
//	func TestStore_MyCustomQuery(t *testing.T) {
//		ctx, db, store := setupTestStore(t)
//		pgxfixtures.TruncatePublicSchema(t, ctx, db.Pool)
//		// ... test custom store methods
//	}

package sessionspgx

import (
	"context"
	"os"
	"path/filepath"
	"runtime"

	"github.com/gopernicus/gopernicus/infrastructure/database/postgres/pgxdb"
	"github.com/gopernicus/gopernicus/workshop/testing/testpgx"
)

// migrateTestDB applies this project's migrations to the test database, so
// tests run against the same schema as 'gopernicus db migrate'. Replace it
// if this store's tests need a different schema setup.
func migrateTestDB(ctx context.Context, pool *pgxdb.Pool) error {
	return pgxdb.RunMigrations(ctx, pool, os.DirFS(projectRoot()), "workshop/migrations/primary")
}

// projectRoot walks up from this source file to the directory containing
// go.mod, so the migrations path resolves at any test working directory.
func projectRoot() string {
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

// testPGXOptions provides extra options to testpgx.SetupTestPGX in the
// generated setupTestStore helper. Use it to pick a Postgres image with
// required extensions, e.g.:
//
//	var testPGXOptions = []testpgx.Option{
//		testpgx.WithPostgresVersion("pgvector/pgvector:pg17"),
//		testpgx.WithExtensions("vector", "pg_trgm"),
//	}
var testPGXOptions []testpgx.Option
