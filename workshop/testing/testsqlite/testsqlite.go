// Package testsqlite provides test SQLite setup utilities — the spec-store
// counterpart of testpgx. No containers: each setup opens an isolated,
// file-backed database under t.TempDir() and applies the project's
// migrations, yielding the crud wiring (Querier, Dialect, TxRunner) the
// generated spec stores accept.
//
// File-backed (not in-memory) is deliberate: in-memory SQLite has WAL
// disabled, so it cannot exercise the production single-writer/WAL path.
// The temp file (plus -wal/-shm siblings) is removed with t.TempDir().
package testsqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/gopernicus/gopernicus/infrastructure/database/crud"
	"github.com/gopernicus/gopernicus/infrastructure/database/crud/sqliteq"
	"github.com/gopernicus/gopernicus/infrastructure/database/sqlite/moderncdb"
	"github.com/stretchr/testify/require"
)

// MigrateFunc is a function that runs database migrations against the db.
type MigrateFunc func(ctx context.Context, db *moderncdb.DB) error

// TxRunner matches the generated spec composites' transaction-runner shape.
type TxRunner func(ctx context.Context, fn func(crud.Querier) error) error

// TestSQLite is a test SQLite setup with cleanup registered on t.
type TestSQLite struct {
	DB   *moderncdb.DB
	Path string
}

// config holds internal options for SetupTestSQLite.
type config struct {
	migrateFn MigrateFunc
}

// Option configures the test SQLite setup.
type Option func(*config)

// WithMigrations sets a migration function to run after opening.
func WithMigrations(fn MigrateFunc) Option {
	return func(c *config) {
		c.migrateFn = fn
	}
}

// SetupTestSQLite opens an isolated file-backed database in t.TempDir(),
// asserts foreign-key enforcement, runs migrations when provided, and
// registers cleanup with t.Cleanup().
func SetupTestSQLite(t *testing.T, ctx context.Context, opts ...Option) *TestSQLite {
	t.Helper()

	cfg := &config{}
	for _, opt := range opts {
		opt(cfg)
	}

	path := filepath.Join(t.TempDir(), "test.db")

	// Explicit modernc DSN: NewFile(barePath) emits `_foreign_keys=on`, which
	// the modernc driver does not recognize — it reads pragmas only from
	// `_pragma=` query parameters — so a bare path would silently leave
	// foreign keys OFF.
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"

	db, err := moderncdb.NewFile(dsn)
	require.NoError(t, err, "failed to open file-backed sqlite db")

	// A silently-off FK pragma would let cross-FK fixtures and isolation
	// assertions pass spuriously — fail loudly instead.
	var fkEnabled int
	require.NoError(t, db.QueryRow(ctx, "PRAGMA foreign_keys").Scan(&fkEnabled),
		"failed to read PRAGMA foreign_keys")
	require.Equal(t, 1, fkEnabled, "foreign keys not enabled")

	if cfg.migrateFn != nil {
		require.NoError(t, cfg.migrateFn(ctx, db), "failed to run migrations")
	}

	ts := &TestSQLite{DB: db, Path: path}
	t.Cleanup(func() {
		_ = db.Close()
	})

	return ts
}

// Querier adapts the database to the crud.Querier the spec stores accept.
func (ts *TestSQLite) Querier() sqliteq.Querier {
	return sqliteq.New(ts.DB)
}

// Dialect returns the SQLite crud dialect.
func (ts *TestSQLite) Dialect() crud.Dialect {
	return crud.SQLiteDialect{}
}

// TxRunner returns a transaction runner in the generated composites' shape.
func (ts *TestSQLite) TxRunner() TxRunner {
	return func(ctx context.Context, fn func(crud.Querier) error) error {
		return ts.DB.InTx(ctx, func(tx *moderncdb.Tx) error {
			return fn(sqliteq.New(tx))
		})
	}
}

// CleanTables deletes every row from every user table (sqlite has no
// TRUNCATE), leaving the schema and the schema_migrations ledger intact.
// Foreign-key enforcement is suspended for the duration so deletes need not
// honor reference order, then restored on every exit path.
func (ts *TestSQLite) CleanTables(t *testing.T, ctx context.Context) {
	t.Helper()

	rows, err := ts.DB.Query(ctx,
		`SELECT name FROM sqlite_master
		 WHERE type = 'table'
		   AND name NOT LIKE 'sqlite_%'
		   AND name != 'schema_migrations'`)
	require.NoError(t, err, "failed to list tables")
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name), "failed to scan table name")
		tables = append(tables, name)
	}
	require.NoError(t, rows.Err(), "failed iterating tables")

	_, err = ts.DB.Exec(ctx, "PRAGMA foreign_keys=OFF")
	require.NoError(t, err, "failed to disable foreign keys")
	defer func() {
		_, err := ts.DB.Exec(ctx, "PRAGMA foreign_keys=ON")
		require.NoError(t, err, "failed to re-enable foreign keys")
	}()

	for _, table := range tables {
		_, err := ts.DB.Exec(ctx, `DELETE FROM "`+table+`"`)
		require.NoError(t, err, "failed to clean table %s", table)
	}
}
