package pgxdb

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gopernicus/gopernicus/sdk"
)

// schemaLiveDSN returns POSTGRES_TEST_DSN or skips loudly: a silent green on
// the schema legs would be a false green, and every assertion here is about
// real Postgres namespace resolution.
func schemaLiveDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set — postgres schema-scoped migrations NOT verified")
	}
	return dsn
}

// openLive opens the throwaway database and closes it at test end.
func openLive(t *testing.T, dsn string) *DB {
	t.Helper()
	db, err := Open(Config{DSN: dsn})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// disposableSchema drops name before and after the test. Every live schema leg
// owns its own name, so parallel packages sharing the container never collide.
func disposableSchema(t *testing.T, db *DB, name string) Schema {
	t.Helper()
	ctx := context.Background()
	drop := func() {
		if _, err := db.Exec(ctx, `DROP SCHEMA IF EXISTS "`+name+`" CASCADE`); err != nil {
			t.Logf("drop schema %s: %v", name, err)
		}
	}
	drop()
	t.Cleanup(drop)
	s, err := NewSchema(name)
	if err != nil {
		t.Fatalf("NewSchema(%q): %v", name, err)
	}
	return s
}

// cleanDefaultLedger removes only the rows this test wrote to the shared
// public ledger. The container is a throwaway, but other packages' live tests
// use the same database, so the ledger is never dropped wholesale.
func cleanDefaultLedger(t *testing.T, db *DB, versions ...string) {
	t.Helper()
	clean := func() {
		ctx := context.Background()
		var reg *string
		if err := db.QueryRow(ctx, "SELECT to_regclass('public.schema_migrations')").Scan(&reg); err != nil || reg == nil {
			return
		}
		for _, v := range versions {
			if _, err := db.Exec(ctx,
				"DELETE FROM public.schema_migrations WHERE source = $1 AND version = $2",
				defaultMigrationSource, v); err != nil {
				t.Logf("clean ledger row %s: %v", v, err)
			}
		}
	}
	clean()
	t.Cleanup(clean)
}

func countRows(t *testing.T, db Querier, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count (%s): %v", query, err)
	}
	return n
}

func relationExists(t *testing.T, db Querier, qualified string) bool {
	t.Helper()
	var reg *string
	if err := db.QueryRow(context.Background(), "SELECT to_regclass($1)::text", qualified).Scan(&reg); err != nil {
		t.Fatalf("to_regclass(%s): %v", qualified, err)
	}
	return reg != nil
}

// TestLive_RunMigrations_WithSchema proves the schema-scoped stream: the DDL
// lands in the schema, the ledger lives beside it, public is untouched, a
// second run is a no-op, and the checksum guard still fires.
func TestLive_RunMigrations_WithSchema(t *testing.T) {
	dsn := schemaLiveDSN(t)
	ctx := context.Background()
	db := openLive(t, dsn)
	s := disposableSchema(t, db, "pgxdb_schema_basic")

	const version = "0001_pgxdb_schema_basic.sql"
	fsys := fstest.MapFS{
		"migrations/" + version: {Data: []byte("CREATE TABLE pgxdb_schema_widgets (id TEXT PRIMARY KEY);")},
	}

	if err := RunMigrations(ctx, db, fsys, "migrations", WithSchema(s)); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if !relationExists(t, db, `"pgxdb_schema_basic".pgxdb_schema_widgets`) {
		t.Fatal("migrated table is not in the schema")
	}
	if relationExists(t, db, "public.pgxdb_schema_widgets") {
		t.Fatal("migrated table leaked into public")
	}
	if !relationExists(t, db, `"pgxdb_schema_basic".schema_migrations`) {
		t.Fatal("ledger is not in the schema")
	}
	if n := countRows(t, db, `SELECT count(*) FROM "pgxdb_schema_basic".schema_migrations`); n != 1 {
		t.Fatalf("schema ledger rows = %d, want 1", n)
	}
	if relationExists(t, db, "public.schema_migrations") {
		if n := countRows(t, db,
			"SELECT count(*) FROM public.schema_migrations WHERE version = $1", version); n != 0 {
			t.Fatalf("public ledger rows for %s = %d, want 0", version, n)
		}
	}

	// Second run: idempotent, no new ledger row.
	if err := RunMigrations(ctx, db, fsys, "migrations", WithSchema(s)); err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	if n := countRows(t, db, `SELECT count(*) FROM "pgxdb_schema_basic".schema_migrations`); n != 1 {
		t.Fatalf("schema ledger rows after re-apply = %d, want 1", n)
	}

	// Checksum guard: the same filename with different bytes must fail.
	tampered := fstest.MapFS{
		"migrations/" + version: {Data: []byte("CREATE TABLE pgxdb_schema_widgets (id TEXT PRIMARY KEY, n INT);")},
	}
	err := RunMigrations(ctx, db, tampered, "migrations", WithSchema(s))
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("tampered re-apply err = %v, want checksum mismatch", err)
	}
}

// TestLive_RunMigrations_SchemaThenDefault_SamePool pins ledger isolation on a
// reused pooled connection: a schema-scoped call and a default call on the same
// *DB write to two different ledgers.
func TestLive_RunMigrations_SchemaThenDefault_SamePool(t *testing.T) {
	dsn := schemaLiveDSN(t)
	ctx := context.Background()
	db := openLive(t, dsn)
	s := disposableSchema(t, db, "pgxdb_schema_isolation")

	const (
		schemaVersion  = "0001_pgxdb_schema_isolation_scoped.sql"
		defaultVersion = "0001_pgxdb_schema_isolation_default.sql"
		defaultTable   = "pgxdb_schema_isolation_host"
	)
	cleanDefaultLedger(t, db, defaultVersion)
	dropDefaultTable := func() {
		if _, err := db.Exec(context.Background(), "DROP TABLE IF EXISTS public."+defaultTable); err != nil {
			t.Logf("drop %s: %v", defaultTable, err)
		}
	}
	dropDefaultTable()
	t.Cleanup(dropDefaultTable)

	scoped := fstest.MapFS{
		"scoped/" + schemaVersion: {Data: []byte("CREATE TABLE pgxdb_schema_isolation_feature (id TEXT PRIMARY KEY);")},
	}
	host := fstest.MapFS{
		"host/" + defaultVersion: {Data: []byte("CREATE TABLE " + defaultTable + " (id TEXT PRIMARY KEY);")},
	}

	if err := RunMigrations(ctx, db, scoped, "scoped", WithSchema(s)); err != nil {
		t.Fatalf("schema stream: %v", err)
	}
	if err := RunMigrations(ctx, db, host, "host"); err != nil {
		t.Fatalf("default stream: %v", err)
	}

	if n := countRows(t, db,
		`SELECT count(*) FROM "pgxdb_schema_isolation".schema_migrations WHERE version = $1`, schemaVersion); n != 1 {
		t.Fatalf("schema ledger rows for %s = %d, want 1", schemaVersion, n)
	}
	if n := countRows(t, db,
		`SELECT count(*) FROM "pgxdb_schema_isolation".schema_migrations WHERE version = $1`, defaultVersion); n != 0 {
		t.Fatalf("default version leaked into the schema ledger: %d rows", n)
	}
	if n := countRows(t, db,
		"SELECT count(*) FROM public.schema_migrations WHERE version = $1", defaultVersion); n != 1 {
		t.Fatalf("public ledger rows for %s = %d, want 1", defaultVersion, n)
	}
	if n := countRows(t, db,
		"SELECT count(*) FROM public.schema_migrations WHERE version = $1", schemaVersion); n != 0 {
		t.Fatalf("schema version leaked into the public ledger: %d rows", n)
	}
	if !relationExists(t, db, "public."+defaultTable) {
		t.Fatal("default stream table is not in public")
	}
	if relationExists(t, db, `"pgxdb_schema_isolation".`+defaultTable) {
		t.Fatal("default stream table leaked into the schema")
	}
}

// TestLive_RunMigrations_PerSchemaTransactions pins the documented boundary:
// one call is one transaction and two streams are two transactions, so a
// failing second stream leaves the committed first one alone.
func TestLive_RunMigrations_PerSchemaTransactions(t *testing.T) {
	dsn := schemaLiveDSN(t)
	ctx := context.Background()
	db := openLive(t, dsn)
	a := disposableSchema(t, db, "pgxdb_schema_txn_a")
	b := disposableSchema(t, db, "pgxdb_schema_txn_b")

	streamA := fstest.MapFS{
		"a/0001_a.sql": {Data: []byte("CREATE TABLE pgxdb_schema_txn_table (id TEXT PRIMARY KEY);")},
	}
	brokenB := fstest.MapFS{
		"b/0001_b.sql": {Data: []byte("CREATE TABLE pgxdb_schema_txn_table (id TEXT PRIMARY KEY);")},
		"b/0002_b.sql": {Data: []byte("CREATE TABLE pgxdb_schema_txn_table (this is not sql);")},
	}
	fixedB := fstest.MapFS{
		"b/0001_b.sql": {Data: []byte("CREATE TABLE pgxdb_schema_txn_table (id TEXT PRIMARY KEY);")},
		"b/0002_b.sql": {Data: []byte("ALTER TABLE pgxdb_schema_txn_table ADD COLUMN n INT;")},
	}

	if err := RunMigrations(ctx, db, streamA, "a", WithSchema(a)); err != nil {
		t.Fatalf("stream A: %v", err)
	}
	if err := RunMigrations(ctx, db, brokenB, "b", WithSchema(b)); err == nil {
		t.Fatal("broken stream B succeeded, want error")
	}

	// A is committed and untouched; B rolled back entirely — including the
	// CREATE SCHEMA, which ran inside the same transaction.
	if !relationExists(t, db, `"pgxdb_schema_txn_a".pgxdb_schema_txn_table`) {
		t.Fatal("committed stream A was rolled back by B's failure")
	}
	if n := countRows(t, db, `SELECT count(*) FROM "pgxdb_schema_txn_a".schema_migrations`); n != 1 {
		t.Fatalf("stream A ledger rows = %d, want 1", n)
	}
	if n := countRows(t, db,
		"SELECT count(*) FROM pg_namespace WHERE nspname = $1", "pgxdb_schema_txn_b"); n != 0 {
		t.Fatalf("failed stream B left its schema behind (%d namespaces)", n)
	}

	// Recovery: correct the failing stream and rerun it. A is not replayed.
	if err := RunMigrations(ctx, db, fixedB, "b", WithSchema(b)); err != nil {
		t.Fatalf("corrected stream B: %v", err)
	}
	if n := countRows(t, db, `SELECT count(*) FROM "pgxdb_schema_txn_b".schema_migrations`); n != 2 {
		t.Fatalf("stream B ledger rows = %d, want 2", n)
	}
	if n := countRows(t, db, `SELECT count(*) FROM "pgxdb_schema_txn_a".schema_migrations`); n != 1 {
		t.Fatalf("stream A ledger rows after B rerun = %d, want 1", n)
	}
}

// pinnedSearchPathDB opens a second pool whose DSN pins search_path to
// "<schema>,public" — gps-360-go's wrapper, reproduced so the adoption legs
// start from the real pre-adoption state (tables in the schema, ledger rows in
// public).
func pinnedSearchPathDB(t *testing.T, dsn, schema string) *DB {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	q := u.Query()
	q.Set("options", "-csearch_path="+schema+",public")
	u.RawQuery = q.Encode()
	db, err := Open(Config{DSN: u.String()})
	if err != nil {
		t.Fatalf("open pinned pool: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// adoptionFS is the pre-adoption stream both adoption legs replay: the first
// file is IF NOT EXISTS-safe, the second carries a backfill that would visibly
// repeat if the runner re-applied it.
func adoptionFS(table string) (fstest.MapFS, []string) {
	files := []string{"0001_pgxdb_adopt_init.sql", "0002_pgxdb_adopt_backfill.sql"}
	return fstest.MapFS{
		"migrations/" + files[0]: {Data: []byte(
			"CREATE TABLE IF NOT EXISTS " + table + " (id TEXT PRIMARY KEY, n INT NOT NULL DEFAULT 0);")},
		"migrations/" + files[1]: {Data: []byte(
			"INSERT INTO " + table + " (id, n) VALUES ('seed', 1) ON CONFLICT (id) DO UPDATE SET n = " + table + ".n + 1;")},
	}, files
}

// TestLive_RunMigrations_AdoptPublicLedger executes the documented
// ledger-relocation preflight against a database whose feature tables already
// live in the schema while its ledger rows live in public, then proves the
// schema-scoped runner skips every copied file.
func TestLive_RunMigrations_AdoptPublicLedger(t *testing.T) {
	dsn := schemaLiveDSN(t)
	ctx := context.Background()
	db := openLive(t, dsn)
	const schemaName = "pgxdb_schema_adopt"
	s := disposableSchema(t, db, schemaName)

	const table = "pgxdb_adopt_widgets"
	fsys, manifest := adoptionFS(table)
	cleanDefaultLedger(t, db, manifest...)

	// Pre-adoption state: the schema exists, the ledger lives in public, and
	// the DSN pin routes the DDL into the schema.
	if _, err := db.Exec(ctx, `CREATE SCHEMA "`+schemaName+`"`); err != nil {
		t.Fatalf("pre-create schema: %v", err)
	}
	if _, err := db.Exec(ctx, `CREATE TABLE IF NOT EXISTS public.schema_migrations (
		source TEXT NOT NULL, version TEXT NOT NULL, checksum TEXT NOT NULL,
		raw_sql TEXT, applied_at TIMESTAMPTZ NOT NULL, PRIMARY KEY (source, version))`); err != nil {
		t.Fatalf("ensure public ledger: %v", err)
	}
	pinned := pinnedSearchPathDB(t, dsn, schemaName)
	if err := RunMigrations(ctx, pinned, fsys, "migrations"); err != nil {
		t.Fatalf("pre-adoption run: %v", err)
	}
	if !relationExists(t, db, `"`+schemaName+`".`+table) {
		t.Fatal("pre-adoption run did not put the table in the schema")
	}
	if relationExists(t, db, `"`+schemaName+`".schema_migrations`) {
		t.Fatal("pre-adoption run created a schema ledger; the fixture must leave the rows in public")
	}
	for _, v := range manifest {
		if n := countRows(t, db,
			"SELECT count(*) FROM public.schema_migrations WHERE source = $1 AND version = $2",
			defaultMigrationSource, v); n != 1 {
			t.Fatalf("public ledger rows for %s = %d, want 1", v, n)
		}
	}
	if n := countRows(t, db, `SELECT n FROM "`+schemaName+`".`+table+` WHERE id = 'seed'`); n != 1 {
		t.Fatalf("backfill counter = %d, want 1 before adoption", n)
	}

	// The documented one-time preflight, in one explicit transaction.
	inList := quotedList(manifest)
	if err := db.InTx(ctx, func(tx *Tx) error {
		if _, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS "`+schemaName+`".schema_migrations (
			source TEXT NOT NULL,
			version TEXT NOT NULL,
			checksum TEXT NOT NULL,
			raw_sql TEXT,
			applied_at TIMESTAMPTZ NOT NULL,
			PRIMARY KEY (source, version)
		)`); err != nil {
			return fmt.Errorf("create target ledger: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO "`+schemaName+`".schema_migrations
			(source, version, checksum, raw_sql, applied_at)
			SELECT source, version, checksum, raw_sql, applied_at
			FROM public.schema_migrations
			WHERE source = 'default' AND version IN (`+inList+`)
			ON CONFLICT (source, version) DO NOTHING`); err != nil {
			return fmt.Errorf("copy ledger rows: %w", err)
		}
		var copied int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM "`+schemaName+`".schema_migrations
			WHERE source = 'default' AND version IN (`+inList+`)`).Scan(&copied); err != nil {
			return err
		}
		if copied != len(manifest) {
			return fmt.Errorf("copied %d of %d manifest rows", copied, len(manifest))
		}
		var missing int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM (
			SELECT source, version, checksum FROM public.schema_migrations
			  WHERE source = 'default' AND version IN (`+inList+`)
			EXCEPT
			SELECT source, version, checksum FROM "`+schemaName+`".schema_migrations
			  WHERE source = 'default' AND version IN (`+inList+`)
		) AS d`).Scan(&missing); err != nil {
			return err
		}
		if missing != 0 {
			return fmt.Errorf("%d manifest rows missing or checksum-mismatched in the target ledger", missing)
		}
		return nil
	}); err != nil {
		t.Fatalf("ledger relocation preflight: %v", err)
	}

	// The schema-scoped runner must now skip every copied file.
	if err := RunMigrations(ctx, db, fsys, "migrations", WithSchema(s)); err != nil {
		t.Fatalf("post-preflight run: %v", err)
	}
	if n := countRows(t, db, `SELECT n FROM "`+schemaName+`".`+table+` WHERE id = 'seed'`); n != 1 {
		t.Fatalf("backfill counter = %d, want 1 — the runner re-applied a copied file", n)
	}
	if n := countRows(t, db, `SELECT count(*) FROM "`+schemaName+`".schema_migrations`); n != len(manifest) {
		t.Fatalf("schema ledger rows = %d, want %d", n, len(manifest))
	}
}

// TestLive_RunMigrations_WithSchema_LedgerInPublic is the documented
// safe-but-expensive fallback: skip the copy and the schema-scoped runner
// re-runs the whole stream against tables that already exist.
func TestLive_RunMigrations_WithSchema_LedgerInPublic(t *testing.T) {
	dsn := schemaLiveDSN(t)
	ctx := context.Background()
	db := openLive(t, dsn)
	const schemaName = "pgxdb_schema_norelocate"
	s := disposableSchema(t, db, schemaName)

	const table = "pgxdb_norelocate_widgets"
	fsys, manifest := adoptionFS(table)
	cleanDefaultLedger(t, db, manifest...)

	if _, err := db.Exec(ctx, `CREATE SCHEMA "`+schemaName+`"`); err != nil {
		t.Fatalf("pre-create schema: %v", err)
	}
	if _, err := db.Exec(ctx, `CREATE TABLE IF NOT EXISTS public.schema_migrations (
		source TEXT NOT NULL, version TEXT NOT NULL, checksum TEXT NOT NULL,
		raw_sql TEXT, applied_at TIMESTAMPTZ NOT NULL, PRIMARY KEY (source, version))`); err != nil {
		t.Fatalf("ensure public ledger: %v", err)
	}
	pinned := pinnedSearchPathDB(t, dsn, schemaName)
	if err := RunMigrations(ctx, pinned, fsys, "migrations"); err != nil {
		t.Fatalf("pre-adoption run: %v", err)
	}

	// No copy: the schema ledger starts empty, so every file re-runs.
	if err := RunMigrations(ctx, db, fsys, "migrations", WithSchema(s)); err != nil {
		t.Fatalf("re-run without relocation: %v", err)
	}
	if n := countRows(t, db, `SELECT count(*) FROM "`+schemaName+`".schema_migrations`); n != len(manifest) {
		t.Fatalf("schema ledger rows = %d, want %d", n, len(manifest))
	}
	// The backfill repeated — the documented cost of skipping the preflight.
	if n := countRows(t, db, `SELECT n FROM "`+schemaName+`".`+table+` WHERE id = 'seed'`); n != 2 {
		t.Fatalf("backfill counter = %d, want 2 (the re-run repeats the backfill)", n)
	}
}

func quotedList(versions []string) string {
	out := make([]string, len(versions))
	for i, v := range versions {
		out[i] = "'" + v + "'"
	}
	return strings.Join(out, ", ")
}

// disposableRole creates a LOGIN role owned by the throwaway container and
// returns a DSN that connects as it. DROP OWNED BY drops the grants first, so
// DROP ROLE cannot fail on a dependent privilege.
func disposableRole(t *testing.T, db *DB, dsn, role string) string {
	t.Helper()
	ctx := context.Background()
	const password = "pgxdb_schema_role_pw"

	drop := func() {
		if _, err := db.Exec(context.Background(), `DROP OWNED BY "`+role+`" CASCADE`); err != nil {
			t.Logf("drop owned by %s: %v", role, err)
		}
		if _, err := db.Exec(context.Background(), `DROP ROLE IF EXISTS "`+role+`"`); err != nil {
			t.Logf("drop role %s: %v", role, err)
		}
	}
	if _, err := db.Exec(ctx, `DROP ROLE IF EXISTS "`+role+`"`); err != nil {
		t.Logf("pre-drop role %s: %v", role, err)
	}
	if _, err := db.Exec(ctx, `CREATE ROLE "`+role+`" LOGIN PASSWORD '`+password+`'`); err != nil {
		t.Fatalf("create role %s: %v", role, err)
	}
	t.Cleanup(drop)

	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	u.User = url.UserPassword(role, password)
	return u.String()
}

// TestLive_RunMigrations_SchemaPrivilegeMatrix drives the four documented
// privilege outcomes with disposable roles: no CREATE ON DATABASE against an
// absent schema, a DBA-precreated schema with schema grants, missing USAGE,
// and missing schema CREATE.
func TestLive_RunMigrations_SchemaPrivilegeMatrix(t *testing.T) {
	dsn := schemaLiveDSN(t)
	ctx := context.Background()
	admin := openLive(t, dsn)

	fsys := fstest.MapFS{
		"migrations/0001_priv.sql": {Data: []byte("CREATE TABLE pgxdb_priv_widgets (id TEXT PRIMARY KEY);")},
	}

	t.Run("absent schema without CREATE ON DATABASE", func(t *testing.T) {
		const schemaName = "pgxdb_schema_priv_absent"
		s := disposableSchema(t, admin, schemaName)
		roleDSN := disposableRole(t, admin, dsn, "pgxdb_role_absent")
		roleDB := openLive(t, roleDSN)

		err := RunMigrations(ctx, roleDB, fsys, "migrations", WithSchema(s))
		if err == nil {
			t.Fatal("migration succeeded without CREATE ON DATABASE, want a named error")
		}
		if !strings.Contains(err.Error(), "does not exist and could not be created") ||
			!strings.Contains(err.Error(), "CREATE ON DATABASE") {
			t.Fatalf("err = %v, want the named schema-creation error", err)
		}
		if !errors.Is(err, sdk.ErrForbidden) {
			t.Fatalf("err = %v, want wrapping sdk.ErrForbidden", err)
		}
		if n := countRows(t, admin, "SELECT count(*) FROM pg_namespace WHERE nspname = $1", schemaName); n != 0 {
			t.Fatalf("schema was created anyway (%d namespaces)", n)
		}
	})

	t.Run("precreated schema with USAGE and CREATE", func(t *testing.T) {
		const schemaName = "pgxdb_schema_priv_granted"
		s := disposableSchema(t, admin, schemaName)
		roleDSN := disposableRole(t, admin, dsn, "pgxdb_role_granted")
		if _, err := admin.Exec(ctx, `CREATE SCHEMA "`+schemaName+`"`); err != nil {
			t.Fatalf("pre-create schema: %v", err)
		}
		if _, err := admin.Exec(ctx, `GRANT USAGE, CREATE ON SCHEMA "`+schemaName+`" TO "pgxdb_role_granted"`); err != nil {
			t.Fatalf("grant: %v", err)
		}
		roleDB := openLive(t, roleDSN)

		// The role still has no CREATE ON DATABASE: success proves the runner
		// probed pg_namespace and skipped CREATE SCHEMA entirely.
		if err := RunMigrations(ctx, roleDB, fsys, "migrations", WithSchema(s)); err != nil {
			t.Fatalf("migration into a DBA-precreated schema: %v", err)
		}
		if !relationExists(t, admin, `"`+schemaName+`".pgxdb_priv_widgets`) {
			t.Fatal("table was not created in the precreated schema")
		}
		if !relationExists(t, admin, `"`+schemaName+`".schema_migrations`) {
			t.Fatal("ledger was not created in the precreated schema")
		}
	})

	t.Run("existing schema without USAGE", func(t *testing.T) {
		const schemaName = "pgxdb_schema_priv_nousage"
		s := disposableSchema(t, admin, schemaName)
		roleDSN := disposableRole(t, admin, dsn, "pgxdb_role_nousage")
		if _, err := admin.Exec(ctx, `CREATE SCHEMA "`+schemaName+`"`); err != nil {
			t.Fatalf("pre-create schema: %v", err)
		}
		roleDB := openLive(t, roleDSN)

		err := RunMigrations(ctx, roleDB, fsys, "migrations", WithSchema(s))
		if err == nil {
			t.Fatal("migration succeeded without USAGE, want a named error")
		}
		if !strings.Contains(err.Error(), "lacks USAGE") || !strings.Contains(err.Error(), "grant USAGE ON SCHEMA") {
			t.Fatalf("err = %v, want the named USAGE error", err)
		}
		if !errors.Is(err, sdk.ErrForbidden) {
			t.Fatalf("err = %v, want wrapping sdk.ErrForbidden", err)
		}
	})

	t.Run("existing schema without CREATE", func(t *testing.T) {
		const schemaName = "pgxdb_schema_priv_nocreate"
		s := disposableSchema(t, admin, schemaName)
		roleDSN := disposableRole(t, admin, dsn, "pgxdb_role_nocreate")
		if _, err := admin.Exec(ctx, `CREATE SCHEMA "`+schemaName+`"`); err != nil {
			t.Fatalf("pre-create schema: %v", err)
		}
		if _, err := admin.Exec(ctx, `GRANT USAGE ON SCHEMA "`+schemaName+`" TO "pgxdb_role_nocreate"`); err != nil {
			t.Fatalf("grant usage: %v", err)
		}
		roleDB := openLive(t, roleDSN)

		err := RunMigrations(ctx, roleDB, fsys, "migrations", WithSchema(s))
		if err == nil {
			t.Fatal("migration succeeded without CREATE on the schema, want a named error")
		}
		if !strings.Contains(err.Error(), "lacks CREATE") || !strings.Contains(err.Error(), "grant CREATE ON SCHEMA") {
			t.Fatalf("err = %v, want the named schema-CREATE error", err)
		}
		if !errors.Is(err, sdk.ErrForbidden) {
			t.Fatalf("err = %v, want wrapping sdk.ErrForbidden", err)
		}
	})
}
