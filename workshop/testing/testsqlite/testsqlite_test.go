package testsqlite

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"

	"github.com/gopernicus/gopernicus/infrastructure/database/crud"
	"github.com/gopernicus/gopernicus/infrastructure/database/sqlite/moderncdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testMigrations = fstest.MapFS{
	"0001_init.sql": &fstest.MapFile{Data: []byte(`
		CREATE TABLE parents (
			id   TEXT PRIMARY KEY,
			name TEXT NOT NULL
		);
		CREATE TABLE children (
			id        TEXT PRIMARY KEY,
			parent_id TEXT NOT NULL REFERENCES parents(id)
		);
	`)},
	// Reflect artifact — must be skipped by the runner.
	"_public.sql": &fstest.MapFile{Data: []byte(`CREATE TABLE should_not_exist (id TEXT);`)},
}

func setup(t *testing.T, ctx context.Context) *TestSQLite {
	t.Helper()
	return SetupTestSQLite(t, ctx, WithMigrations(func(ctx context.Context, db *moderncdb.DB) error {
		return moderncdb.RunMigrations(ctx, db, testMigrations, ".")
	}))
}

func TestSetupMigratesAndEnforcesForeignKeys(t *testing.T) {
	ctx := context.Background()
	ts := setup(t, ctx)

	_, err := ts.DB.Exec(ctx, `INSERT INTO parents (id, name) VALUES ('p1', 'one')`)
	require.NoError(t, err)

	// Reflect artifact skipped.
	var count int
	require.NoError(t, ts.DB.QueryRow(ctx,
		`SELECT count(*) FROM sqlite_master WHERE name = 'should_not_exist'`).Scan(&count))
	assert.Equal(t, 0, count, "_public.sql must not be applied")

	// FK enforcement is real.
	_, err = ts.DB.Exec(ctx, `INSERT INTO children (id, parent_id) VALUES ('c1', 'missing')`)
	assert.Error(t, err, "FK violation must fail")

	// Re-running migrations is a no-op (ledger + checksums).
	require.NoError(t, moderncdb.RunMigrations(ctx, ts.DB, testMigrations, "."))
}

func TestQuerierDialectAndTxRunner(t *testing.T) {
	ctx := context.Background()
	ts := setup(t, ctx)

	q := ts.Querier()
	args := crud.NewArgs(ts.Dialect())
	_, err := q.Exec(ctx, "INSERT INTO parents (id, name) VALUES ("+args.Add("p1")+", "+args.Add("one")+")", args.Values()...)
	require.NoError(t, err)

	// Committed transaction persists.
	err = ts.TxRunner()(ctx, func(txq crud.Querier) error {
		a := crud.NewArgs(ts.Dialect())
		_, err := txq.Exec(ctx, "INSERT INTO parents (id, name) VALUES ("+a.Add("p2")+", "+a.Add("two")+")", a.Values()...)
		return err
	})
	require.NoError(t, err)

	// Rolled-back transaction does not.
	sentinel := errors.New("rollback")
	err = ts.TxRunner()(ctx, func(txq crud.Querier) error {
		a := crud.NewArgs(ts.Dialect())
		if _, err := txq.Exec(ctx, "INSERT INTO parents (id, name) VALUES ("+a.Add("p3")+", "+a.Add("three")+")", a.Values()...); err != nil {
			return err
		}
		return sentinel
	})
	require.ErrorIs(t, err, sentinel)

	var count int
	require.NoError(t, ts.DB.QueryRow(ctx, "SELECT count(*) FROM parents").Scan(&count))
	assert.Equal(t, 2, count, "p1 + committed p2, rolled-back p3 absent")
}

func TestCleanTables(t *testing.T) {
	ctx := context.Background()
	ts := setup(t, ctx)

	_, err := ts.DB.Exec(ctx, `INSERT INTO parents (id, name) VALUES ('p1', 'one')`)
	require.NoError(t, err)
	_, err = ts.DB.Exec(ctx, `INSERT INTO children (id, parent_id) VALUES ('c1', 'p1')`)
	require.NoError(t, err)

	ts.CleanTables(t, ctx)

	var parents, children, ledger int
	require.NoError(t, ts.DB.QueryRow(ctx, "SELECT count(*) FROM parents").Scan(&parents))
	require.NoError(t, ts.DB.QueryRow(ctx, "SELECT count(*) FROM children").Scan(&children))
	require.NoError(t, ts.DB.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&ledger))
	assert.Equal(t, 0, parents)
	assert.Equal(t, 0, children)
	assert.Equal(t, 1, ledger, "schema_migrations ledger must survive CleanTables")

	// FK enforcement restored after cleanup.
	_, err = ts.DB.Exec(ctx, `INSERT INTO children (id, parent_id) VALUES ('c2', 'missing')`)
	assert.Error(t, err, "FK enforcement must be back on")
}
