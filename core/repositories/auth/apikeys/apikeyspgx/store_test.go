//go:build integration

// This file is created once by gopernicus and will NOT be overwritten.
// Add custom integration tests for the APIKey store here.
//
// The setupTestStore() helper in generated_test.go provides a test database
// and store instance. Use it as the basis for custom tests:
//
//	func TestStore_MyCustomQuery(t *testing.T) {
//		ctx, db, store := setupTestStore(t)
//		pgxfixtures.TruncatePublicSchema(t, ctx, db.Pool)
//		// ... test custom store methods
//	}

package apikeyspgx

import (
	"context"

	"github.com/gopernicus/gopernicus/infrastructure/database/postgres/pgxdb"
)

// migrateTestDB runs migrations for the test database.
// TODO: Update this to use your project's migration embed.
//
// Example with embedded migrations:
//
//	//go:embed ../../../../workshop/migrations/primary/*.sql
//	var testMigrations embed.FS
//
//	func migrateTestDB(ctx context.Context, pool *pgxdb.Pool) error {
//		return pgxdb.RunMigrations(ctx, pool, testMigrations, "workshop/migrations/primary")
//	}
func migrateTestDB(_ context.Context, _ *pgxdb.Pool) error {
	// Replace with actual migration logic.
	return nil
}
