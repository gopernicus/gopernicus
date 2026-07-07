//go:build integration

// Integration tests hit a live Turso / libSQL database. Run with:
//
//	go test -tags=integration ./stores/turso/...
//
// They require TURSO_DATABASE_URL and TURSO_AUTH_TOKEN in the environment (or a
// .env loaded by the caller). Absent those, the tests skip loudly — a silent
// green here would claim dialect conformance nothing verified.
package turso

import (
	"context"
	"os"
	"testing"

	"github.com/gopernicus/gopernicus/features/cms"
	"github.com/gopernicus/gopernicus/features/cms/storetest"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
)

// cmsTables are the feature's tables in child-before-parent order, so a
// truncation pass respects the foreign keys.
var cmsTables = []string{
	"entry_terms",
	"entry_fields",
	"entries",
	"menu_items",
	"menus",
	"terms",
	"assets",
	"inquiries",
}

// TestConformance_Turso runs the shared cms storetest suite against a live
// Turso/libSQL database. Each newRepos call opens a connection, applies the
// canonical migrations, and truncates the feature's tables so every leaf subtest
// starts from a clean, isolated Repositories (the SQL harness half of the
// newRepos contract).
func TestConformance_Turso(t *testing.T) {
	url := os.Getenv("TURSO_DATABASE_URL")
	token := os.Getenv("TURSO_AUTH_TOKEN")
	if url == "" || token == "" {
		t.Skip("TURSO_DATABASE_URL/TURSO_AUTH_TOKEN not set — turso conformance NOT verified")
	}

	storetest.Run(t, func(t *testing.T) cms.Repositories {
		db, err := tursodb.Open(tursodb.Config{URL: url, AuthToken: token})
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		t.Cleanup(func() { db.Close() })

		if err := tursodb.RunMigrations(context.Background(), db, MigrationsFS, MigrationsDir); err != nil {
			t.Fatalf("migrate: %v", err)
		}
		truncate(t, db)
		t.Cleanup(func() { truncate(t, db) })

		return Repositories(db)
	})
}

// truncate clears every cms table so a Repositories starts empty.
func truncate(t *testing.T, db *tursodb.DB) {
	t.Helper()
	ctx := context.Background()
	for _, tbl := range cmsTables {
		if _, err := db.Exec(ctx, "DELETE FROM "+tbl); err != nil {
			t.Fatalf("truncate %s: %v", tbl, err)
		}
	}
}
