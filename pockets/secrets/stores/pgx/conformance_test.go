// Conformance tests hit a live PostgreSQL database. Run with:
//
//	docker run --rm -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:17
//	POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' go test ./...
//
// Absent POSTGRES_TEST_DSN the suite skips loudly. POSTGRES_TEST_SCHEMA is
// optional: set it and the whole suite runs in that schema, migrated with
// pgxdb.WithSchema and constructed with WithSchema, which proves every
// executed statement is qualified.
package pgx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/secrets/logic/vault"
	"github.com/gopernicus/gopernicus/pockets/secrets/stores/storetest"
	"github.com/gopernicus/gopernicus/sdk"
)

// tables are cleared before each newRepo call, children first.
var tables = []string{"secrets_audit", "secrets_values", "secrets_owner_keys"}

func TestConformance(t *testing.T) {
	dsn := requireDSN(t)
	storetest.RunRepository(t, func(t *testing.T) vault.Repository {
		db, opts := openAndMigrate(t, dsn)
		return NewRepository(db, opts...)
	})
}

// TestLive_StatusCheck pins the boot gate: a schema the migration never
// reached fails with sdk.ErrNotFound naming the qualified table.
func TestLive_StatusCheck(t *testing.T) {
	dsn := requireDSN(t)
	db, opts := openAndMigrate(t, dsn)
	ctx := context.Background()
	if err := StatusCheck(ctx, db, opts...); err != nil {
		t.Fatalf("migrated schema: %v", err)
	}
	absent, err := pgxdb.NewSchema("secrets_statuscheck_absent")
	if err != nil {
		t.Fatal(err)
	}
	err = StatusCheck(ctx, db, WithSchema(absent))
	if !errors.Is(err, sdk.ErrNotFound) || !strings.Contains(err.Error(), absent.Table("secrets_owner_keys")) {
		t.Fatalf("absent schema: %v", err)
	}
}

func requireDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set — postgres conformance NOT verified")
	}
	return dsn
}

func testSchema(t *testing.T) pgxdb.Schema {
	t.Helper()
	name := os.Getenv("POSTGRES_TEST_SCHEMA")
	if name == "" {
		return pgxdb.Schema{}
	}
	s, err := pgxdb.NewSchema(name)
	if err != nil {
		t.Fatalf("POSTGRES_TEST_SCHEMA=%q is not a valid schema: %v", name, err)
	}
	return s
}

// openAndMigrate opens a connection, applies the canonical migration and
// truncates the tables, returning the options that place the store in the
// same schema the migration reached.
func openAndMigrate(t *testing.T, dsn string) (*pgxdb.DB, []Option) {
	t.Helper()
	db, err := pgxdb.Open(context.Background(), pgxdb.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	schema := testSchema(t)
	var (
		opts        []Option
		migrateOpts []pgxdb.MigrateOption
	)
	if !schema.IsZero() {
		if _, err := db.Exec(context.Background(), fmt.Sprintf(`DROP SCHEMA IF EXISTS "%s" CASCADE`, schema)); err != nil {
			t.Fatalf("drop schema %s: %v", schema, err)
		}
		opts = append(opts, WithSchema(schema))
		migrateOpts = append(migrateOpts, pgxdb.WithSchema(schema))
	}
	if err := pgxdb.RunMigrations(context.Background(), db, MigrationsFS, MigrationsDir, migrateOpts...); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	truncate(t, db, schema)
	t.Cleanup(func() { truncate(t, db, schema) })
	return db, opts
}

func truncate(t *testing.T, db *pgxdb.DB, schema pgxdb.Schema) {
	t.Helper()
	qualified := make([]string, len(tables))
	for i, table := range tables {
		qualified[i] = schema.Table(table)
	}
	if _, err := db.Exec(context.Background(), "TRUNCATE "+strings.Join(qualified, ", ")+" CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
}
