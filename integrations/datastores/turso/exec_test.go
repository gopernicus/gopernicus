package turso

import (
	"context"
	"testing"
)

// TestExecAffecting exercises the (int64, error) normalization against the same
// in-proc SQLite dialect a live Turso database speaks: a matching write reports
// its row count, a non-matching write reports zero (the adapter, not the
// connector, maps zero to sdk.ErrNotFound), a broad write reports many, and a
// driver error propagates unchanged rather than being normalized to a count.
func TestExecAffecting(t *testing.T) {
	db := newMemDB(t)
	ctx := context.Background()
	if _, err := db.Exec(ctx, `CREATE TABLE t (id TEXT PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO t (id, v) VALUES ('a', 'x'), ('b', 'y')`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	t.Run("affects_one", func(t *testing.T) {
		n, err := ExecAffecting(ctx, db, `UPDATE t SET v = ? WHERE id = ?`, "z", "a")
		if err != nil {
			t.Fatalf("ExecAffecting: %v", err)
		}
		if n != 1 {
			t.Fatalf("n = %d, want 1", n)
		}
	})

	t.Run("affects_none", func(t *testing.T) {
		n, err := ExecAffecting(ctx, db, `UPDATE t SET v = ? WHERE id = ?`, "z", "missing")
		if err != nil {
			t.Fatalf("ExecAffecting: %v", err)
		}
		if n != 0 {
			t.Fatalf("n = %d, want 0 (connector does not map zero to ErrNotFound)", n)
		}
	})

	t.Run("affects_many", func(t *testing.T) {
		n, err := ExecAffecting(ctx, db, `UPDATE t SET v = ?`, "q")
		if err != nil {
			t.Fatalf("ExecAffecting: %v", err)
		}
		if n != 2 {
			t.Fatalf("n = %d, want 2", n)
		}
	})

	t.Run("exec_error_propagates", func(t *testing.T) {
		_, err := ExecAffecting(ctx, db, `UPDATE no_such_table SET v = ?`, "z")
		if err == nil {
			t.Fatal("ExecAffecting: want a driver error, got nil")
		}
	})
}
