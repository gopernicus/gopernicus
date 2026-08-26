// Live decoy tests for the WithSchema seam. They prove the option actually
// ROUTES the SQL — not merely that it compiles — by keeping a same-named decoy
// table in the default schema and asserting which one each construction reads
// and writes. Both run against POSTGRES_TEST_DSN in their OWN disposable schema
// (independent of POSTGRES_TEST_SCHEMA), and drop it on the way out. Absent a
// DSN they skip loudly, like the conformance suite.
package pgx

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	auth "github.com/gopernicus/gopernicus/features/authentication"
	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk"
)

// publicUsersDecoy is 0001_users.sql's DDL pinned to the default schema. It is
// created IF NOT EXISTS so a database the default conformance leg already
// migrated is reused as-is (row counts are taken as a baseline, never assumed
// zero).
const publicUsersDecoy = `CREATE TABLE IF NOT EXISTS users (
	id             TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
	display_name   TEXT NOT NULL DEFAULT '',
	auth_revision  BIGINT NOT NULL DEFAULT 0,
	created_at     TIMESTAMPTZ NOT NULL,
	updated_at     TIMESTAMPTZ NOT NULL
)`

// TestLive_WithSchema_Decoy proves both directions of the seam against a decoy
// users table in the default schema: constructed WithSchema every read and write
// goes to the schema (and the decoy stays untouched), constructed without it
// every write goes to the decoy.
func TestLive_WithSchema_Decoy(t *testing.T) {
	db := probeDial(t)
	ctx := context.Background()
	schema := disposableSchema(t, "auth_decoy")

	// A free-form DSN may itself pin search_path (exactly the wrapper this option
	// replaces), which would make the "unqualified writes land in the default
	// schema" half of the proof vacuous.
	assertNotOnSearchPath(t, db, schema)

	dropSchema(t, db, schema)
	t.Cleanup(func() { dropSchema(t, db, schema) })

	// The default schema carries the decoy AND the full canonical stream, so the
	// no-option construction below is a real one.
	if _, err := db.Exec(ctx, publicUsersDecoy); err != nil {
		t.Fatalf("create default-schema decoy: %v", err)
	}
	if err := pgxdb.RunMigrations(ctx, db, MigrationsFS, MigrationsDir); err != nil {
		t.Fatalf("migrate default schema: %v", err)
	}

	// BEFORE the schema is migrated, the probe must fail naming the QUALIFIED
	// table — the decoy in the default schema must not satisfy it.
	_, err := Repositories(db, WithSchema(schema))
	if err == nil {
		t.Fatal("Repositories succeeded against an unmigrated schema (the default-schema decoy answered for it)")
	}
	if !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("unmigrated schema: error does not wrap sdk.ErrNotFound: %v", err)
	}
	if want := schema.Table(usersTable); !strings.Contains(err.Error(), want) {
		t.Fatalf("unmigrated schema: error does not name %s: %v", want, err)
	}

	if err := pgxdb.RunMigrations(ctx, db, MigrationsFS, MigrationsDir, pgxdb.WithSchema(schema)); err != nil {
		t.Fatalf("migrate schema: %v", err)
	}
	scoped, err := Repositories(db, WithSchema(schema))
	if err != nil {
		t.Fatalf("Repositories on the migrated schema: %v", err)
	}

	defaultBefore := countRows(t, db, usersTable)
	if got := countRows(t, db, schema.Table(usersTable)); got != 0 {
		t.Fatalf("freshly migrated %s holds %d rows, want 0", schema.Table(usersTable), got)
	}

	createDecoyUser(t, scoped, "scoped")
	if got := countRows(t, db, schema.Table(usersTable)); got != 1 {
		t.Errorf("%s count = %d after a scoped write, want 1", schema.Table(usersTable), got)
	}
	if got := countRows(t, db, usersTable); got != defaultBefore {
		t.Errorf("default-schema users count = %d after a scoped write, want %d (unchanged)", got, defaultBefore)
	}

	// The negative direction: no option, so the write must land in the decoy.
	bare, err := Repositories(db)
	if err != nil {
		t.Fatalf("Repositories without options: %v", err)
	}
	createDecoyUser(t, bare, "bare")
	if got := countRows(t, db, usersTable); got != defaultBefore+1 {
		t.Errorf("default-schema users count = %d after an unqualified write, want %d", got, defaultBefore+1)
	}
	if got := countRows(t, db, schema.Table(usersTable)); got != 1 {
		t.Errorf("%s count = %d after an unqualified write, want 1 (unchanged)", schema.Table(usersTable), got)
	}
}

// TestLive_ProbeColumn_SchemaFilter is the only direction that proves the
// probe's table_schema filter: the default schema carries the 0014 lifecycle
// columns and the store's own schema does not, so an unfiltered
// information_schema.columns read would pass and hide a half-migrated schema.
func TestLive_ProbeColumn_SchemaFilter(t *testing.T) {
	db := probeDial(t)
	ctx := context.Background()
	schema := disposableSchema(t, "auth_decoy_columns")

	dropSchema(t, db, schema)
	t.Cleanup(func() { dropSchema(t, db, schema) })

	// The default schema is fully migrated — it HAS users.status.
	if err := pgxdb.RunMigrations(ctx, db, MigrationsFS, MigrationsDir); err != nil {
		t.Fatalf("migrate default schema: %v", err)
	}
	// The store's schema gets every table but loses the 0014 columns, the shape a
	// host that stopped at 0013 would have.
	if err := pgxdb.RunMigrations(ctx, db, MigrationsFS, MigrationsDir, pgxdb.WithSchema(schema)); err != nil {
		t.Fatalf("migrate schema: %v", err)
	}
	if _, err := db.Exec(ctx, `ALTER TABLE `+schema.Table(usersTable)+
		` DROP COLUMN status, DROP COLUMN status_changed_at`); err != nil {
		t.Fatalf("drop lifecycle columns: %v", err)
	}

	_, err := Repositories(db, WithSchema(schema))
	if err == nil {
		t.Fatal("Repositories succeeded with the lifecycle columns missing (the default schema answered the column probe)")
	}
	if !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("missing column: error does not wrap sdk.ErrNotFound: %v", err)
	}
	if !strings.Contains(err.Error(), usersTable+".status") {
		t.Fatalf("missing column: error does not name users.status: %v", err)
	}
}

// disposableSchema builds a validated Schema this test owns outright: it is
// dropped before and after the test, so it never collides with the fixture-wide
// POSTGRES_TEST_SCHEMA.
func disposableSchema(t *testing.T, name string) pgxdb.Schema {
	t.Helper()
	s, err := pgxdb.NewSchema(name)
	if err != nil {
		t.Fatalf("NewSchema(%q): %v", name, err)
	}
	return s
}

// assertNotOnSearchPath fails unless the connection resolves unqualified names
// outside s. current_schemas(true) is read rather than to_regclass text, which
// renders unqualified for any relation already on the path.
func assertNotOnSearchPath(t *testing.T, db *pgxdb.DB, s pgxdb.Schema) {
	t.Helper()
	var schemas []string
	if err := db.QueryRow(context.Background(), "SELECT current_schemas(true)").Scan(&schemas); err != nil {
		t.Fatalf("read current_schemas: %v", err)
	}
	for _, name := range schemas {
		if name == s.String() {
			t.Fatalf("POSTGRES_TEST_DSN pins search_path to %v, which contains the decoy schema %q — the proof would be vacuous", schemas, s)
		}
	}
}

// countRows counts a table by its rendered name (qualified or bare).
func countRows(t *testing.T, db *pgxdb.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(context.Background(), `SELECT count(*) FROM `+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// createDecoyUser writes one user + primary identifier through the repository
// set under test. The address is unique per call so a rerun against a database
// that kept its default-schema rows cannot lose the authentication claim.
func createDecoyUser(t *testing.T, repos auth.Repositories, label string) {
	t.Helper()
	now := time.Now().UTC()
	address := fmt.Sprintf("%s-%d@decoy.test", label, now.UnixNano())
	_, _, err := repos.Users.CreateWithPrimaryIdentifier(context.Background(),
		user.User{DisplayName: label, Status: user.StatusActive, CreatedAt: now, UpdatedAt: now},
		identifier.Identifier{
			Kind:            identifier.KindEmail,
			NormalizedValue: address,
			VerifiedAt:      now,
			LoginEnabled:    true,
			IsPrimary:       true,
			CreatedAt:       now,
			UpdatedAt:       now,
		})
	if err != nil {
		t.Fatalf("create %s user: %v", label, err)
	}
}
