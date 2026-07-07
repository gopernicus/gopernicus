package turso

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

	_ "modernc.org/sqlite" // pure-Go sqlite driver, test-only
)

// newMemDB returns a turso.DB backed by a single-connection in-memory SQLite.
// libSQL speaks SQLite's dialect, so the migration runner's SQL (PRAGMA,
// sqlite_master, table rebuild) exercises the same paths a live Turso DB would.
func newMemDB(t *testing.T) *DB {
	t.Helper()
	sqldb, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqldb.SetMaxOpenConns(1) // keep one in-memory database across the pool
	t.Cleanup(func() { _ = sqldb.Close() })
	return &DB{db: sqldb}
}

func sqlFile(body string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(body)} }

func sum(body string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(body))) }

func tableExists(t *testing.T, db *DB, name string) bool {
	t.Helper()
	var got string
	err := db.QueryRow(context.Background(),
		"SELECT name FROM sqlite_master WHERE type='table' AND name=?", name).Scan(&got)
	if err == sql.ErrNoRows {
		return false
	}
	if err != nil {
		t.Fatalf("tableExists(%s): %v", name, err)
	}
	return got == name
}

// TestRunMigrations_ReapplyIsIdempotent confirms a second run re-runs nothing.
func TestRunMigrations_ReapplyIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db := newMemDB(t)
	fsys := fstest.MapFS{"m/0001_init.sql": sqlFile("CREATE TABLE alpha (id TEXT);")}

	apply := func() error {
		return RunMigrations(ctx, db, fsys, "m")
	}
	if err := apply(); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if err := apply(); err != nil {
		t.Fatalf("second apply (should be no-op): %v", err)
	}
	var n int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("rows = %d, want 1 after idempotent re-apply", n)
	}
}

// TestRunMigrations_ChecksumMismatch confirms modifying an applied migration errors.
func TestRunMigrations_ChecksumMismatch(t *testing.T) {
	ctx := context.Background()
	db := newMemDB(t)

	if err := RunMigrations(ctx, db, fstest.MapFS{"m/0001_init.sql": sqlFile("CREATE TABLE alpha (id TEXT);")}, "m"); err != nil {
		t.Fatalf("apply: %v", err)
	}

	err := RunMigrations(ctx, db, fstest.MapFS{"m/0001_init.sql": sqlFile("CREATE TABLE alpha (id TEXT, extra TEXT);")}, "m")
	if err == nil || !strings.Contains(err.Error(), "modified after being applied") {
		t.Fatalf("want checksum-mismatch error, got %v", err)
	}
}

// TestRunMigrations_LegacyTableMigratedAndAdopted is the table-migration path: an
// old-shape schema_migrations (version PK only) with an already-applied row is
// rebuilt to (source, version) and the row is adopted by checksum — the DDL is
// NOT re-run (the target table already exists, so a re-run would error).
func TestRunMigrations_LegacyTableMigratedAndAdopted(t *testing.T) {
	ctx := context.Background()
	db := newMemDB(t)

	ddl := "CREATE TABLE articles (id TEXT PRIMARY KEY);"

	// Simulate the pre-B1 world: old table shape, one recorded migration, and the
	// DDL already applied (so re-running it would fail with "already exists").
	if _, err := db.Exec(ctx, `CREATE TABLE schema_migrations (
		version TEXT PRIMARY KEY, checksum TEXT NOT NULL, raw_sql TEXT, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx,
		`INSERT INTO schema_migrations (version, checksum, raw_sql, applied_at) VALUES (?,?,?,?)`,
		"0001_articles.sql", sum(ddl), ddl, "2020-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, ddl); err != nil {
		t.Fatal(err)
	}

	// Now boot the new runner with the host-owned stream shipping the same file.
	if err := RunMigrations(ctx, db, fstest.MapFS{"migrations/0001_articles.sql": sqlFile(ddl)}, "migrations"); err != nil {
		t.Fatalf("apply over legacy table: %v", err)
	}

	// Table migrated to new shape (has source column).
	_, hasSource, err := func() (bool, bool, error) {
		var e bool
		var hs bool
		var ierr error
		ierr = db.InTx(ctx, func(tx *Tx) error {
			e, hs, ierr = inspectMigrationsTable(ctx, tx)
			return ierr
		})
		return e, hs, ierr
	}()
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if !hasSource {
		t.Fatal("schema_migrations should carry the source column after migration")
	}

	// Legacy row adopted into the default source (not left as _legacy), still 1 row.
	var source string
	var n int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx,
		"SELECT source FROM schema_migrations WHERE version=?", "0001_articles.sql").Scan(&source); err != nil {
		t.Fatal(err)
	}
	if n != 1 || source != defaultMigrationSource {
		t.Fatalf("legacy adoption failed: rows=%d source=%q, want rows=1 source=%s", n, source, defaultMigrationSource)
	}
}
