// Live schema-probe tests hit the same PostgreSQL database as the conformance
// suite (POSTGRES_TEST_DSN). They prove the boot-time constructor probe fails
// loudly — naming the specific missing table and wrapping sdk.ErrNotFound —
// before the host serves traffic when a canonical migration was not applied,
// and that the constructor itself never creates schema. Absent a DSN they skip
// loudly, like the conformance suite.
package pgx

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk"
)

// probeDial opens a live pool, skipping loudly without POSTGRES_TEST_DSN.
func probeDial(t *testing.T) *pgxdb.DB {
	t.Helper()
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set — postgres store probe NOT verified")
	}
	db, err := pgxdb.Open(pgxdb.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// probeDropAll drops every canonical table and the migration ledger so the
// database holds no auth schema at all.
func probeDropAll(t *testing.T, db *pgxdb.DB) {
	t.Helper()
	q := "DROP TABLE IF EXISTS " + strings.Join(probeTables, ", ") + ", schema_migrations CASCADE"
	if _, err := db.Exec(context.Background(), q); err != nil {
		t.Fatalf("drop schema: %v", err)
	}
}

// probeResetSchema returns the database to the full, clean canonical schema: it
// drops everything then re-applies the canonical migrations. It is the
// drop/recreate reset the probe subtests build on.
func probeResetSchema(t *testing.T, db *pgxdb.DB) {
	t.Helper()
	probeDropAll(t, db)
	if err := pgxdb.RunMigrations(context.Background(), db, MigrationsFS, MigrationsDir); err != nil {
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
			if _, err := db.Exec(context.Background(), "DROP TABLE IF EXISTS "+table+" CASCADE"); err != nil {
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

	// Close the pool so every subsequent probe query fails at the infra layer.
	_ = db.Close()

	_, err := Repositories(db)
	if err == nil {
		t.Fatal("Repositories succeeded against a closed pool")
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

	for _, table := range probeTables {
		var reg *string
		if err := db.QueryRow(context.Background(), `SELECT to_regclass($1)::text`, table).Scan(&reg); err != nil {
			t.Fatalf("inspect %s after construct: %v", table, err)
		}
		if reg != nil {
			t.Fatalf("constructor created table %s", table)
		}
	}
}
