//go:build integration

// Live schema-probe tests hit the same Turso/libSQL database as the conformance
// suite (TURSO_DATABASE_URL, with TURSO_AUTH_TOKEN optional for a local sqld
// that runs without auth). They prove the boot-time constructor probe fails
// loudly — naming the specific missing table and wrapping sdk.ErrNotFound —
// before the host serves traffic when a canonical migration was not applied,
// and that the constructor itself never creates schema. Absent a URL they skip
// loudly, like the conformance suite.
package turso

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk"
)

// probeDial opens a live connection, skipping loudly without TURSO_DATABASE_URL.
// TURSO_AUTH_TOKEN is read but optional: a local sqld container serves without
// auth, so an empty token is expected there.
func probeDial(t *testing.T) *tursodb.DB {
	t.Helper()
	url := os.Getenv("TURSO_DATABASE_URL")
	if url == "" {
		t.Skip("TURSO_DATABASE_URL not set — turso store probe NOT verified")
	}
	db, err := tursodb.Open(tursodb.Config{URL: url, AuthToken: os.Getenv("TURSO_AUTH_TOKEN")})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// probeDropAll drops every canonical table and the migration ledger so the
// database holds no auth schema at all. SQLite has no CASCADE keyword and the
// connector leaves foreign_keys OFF, so single-table drops in any order succeed.
func probeDropAll(t *testing.T, db *tursodb.DB) {
	t.Helper()
	ctx := context.Background()
	for _, tbl := range append(append([]string{}, probeTables...), "schema_migrations") {
		if _, err := db.Exec(ctx, "DROP TABLE IF EXISTS "+tbl); err != nil {
			t.Fatalf("drop %s: %v", tbl, err)
		}
	}
}

// probeResetSchema returns the database to the full, clean canonical schema: it
// drops everything then re-applies the canonical migrations. It is the
// drop/recreate reset the probe subtests build on.
func probeResetSchema(t *testing.T, db *tursodb.DB) {
	t.Helper()
	probeDropAll(t, db)
	if err := tursodb.RunMigrations(context.Background(), db, MigrationsFS, MigrationsDir); err != nil {
		t.Fatalf("migrate: %v", err)
	}
}

// TestSchemaProbe_FullSchema proves the constructor succeeds once every
// canonical migration has been applied.
func TestSchemaProbe_FullSchema(t *testing.T) {
	db := probeDial(t)
	probeResetSchema(t, db)
	t.Cleanup(func() { probeResetSchema(t, db) })

	if _, err := Repositories(db); err != nil {
		t.Fatalf("Repositories on full schema: %v", err)
	}
}

// TestSchemaProbe_MissingTable proves the constructor fails — naming exactly the
// dropped table and wrapping sdk.ErrNotFound — for every one of the 13 canonical
// tables (Risk 6: all tables, not just users/sessions).
func TestSchemaProbe_MissingTable(t *testing.T) {
	db := probeDial(t)
	t.Cleanup(func() { probeResetSchema(t, db) })

	for _, table := range probeTables {
		t.Run(table, func(t *testing.T) {
			probeResetSchema(t, db)
			if _, err := db.Exec(context.Background(), "DROP TABLE IF EXISTS "+table); err != nil {
				t.Fatalf("drop %s: %v", table, err)
			}

			_, err := Repositories(db)
			if err == nil {
				t.Fatalf("Repositories succeeded with %s missing", table)
			}
			if !errors.Is(err, sdk.ErrNotFound) {
				t.Fatalf("missing %s: error does not wrap sdk.ErrNotFound: %v", table, err)
			}
			if !strings.Contains(err.Error(), table) {
				t.Fatalf("missing %s: error does not name the table: %v", table, err)
			}
		})
	}
}

// TestSchemaProbe_InfraFailureNotMisclassified proves a query/connection failure
// is surfaced as-is, never misreported as a missing table wrapping ErrNotFound.
func TestSchemaProbe_InfraFailureNotMisclassified(t *testing.T) {
	db := probeDial(t)
	probeResetSchema(t, db)

	// Close the connection so every subsequent probe query fails at the infra layer.
	db.Close()

	_, err := Repositories(db)
	if err == nil {
		t.Fatal("Repositories succeeded against a closed connection")
	}
	if errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("infra failure misclassified as missing table: %v", err)
	}
}

// TestSchemaProbe_CreatesNoSchema proves the constructor never applies schema:
// against an empty database it fails and leaves every canonical table absent.
func TestSchemaProbe_CreatesNoSchema(t *testing.T) {
	db := probeDial(t)
	probeDropAll(t, db)
	t.Cleanup(func() { probeResetSchema(t, db) })

	if _, err := Repositories(db); err == nil {
		t.Fatal("Repositories succeeded against an empty database")
	}

	ctx := context.Background()
	for _, table := range probeTables {
		var name string
		err := db.QueryRow(ctx,
			`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
		if err == nil {
			t.Fatalf("constructor created table %s", table)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("inspect %s after construct: %v", table, err)
		}
	}
}
