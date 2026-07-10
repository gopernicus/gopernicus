package turso

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

// bufLogger returns a logger writing JSON records to buf, so tests can assert on
// the exact query and args a trace emits.
func bufLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

func seedLogTable(t *testing.T, db *DB) {
	t.Helper()
	if _, err := db.Exec(context.Background(),
		`CREATE TABLE logtest (id TEXT PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
}

// TestLogging_SilentAtDefaults proves a DB with no tracer (Config.LogQueries
// unset) emits nothing on any DB or Tx path, even when a process-default logger
// is capturing — the opt-in is the only thing that turns logging on.
func TestLogging_SilentAtDefaults(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(bufLogger(&buf))
	t.Cleanup(func() { slog.SetDefault(prev) })

	db := newMemDB(t) // tracer nil — this is the default posture
	ctx := context.Background()
	seedLogTable(t, db)

	if _, err := db.Exec(ctx, `INSERT INTO logtest (id, v) VALUES (?, ?)`, "a", "x"); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	rows, err := db.Query(ctx, `SELECT id FROM logtest WHERE id = ?`, "a")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	rows.Close()
	var id string
	if err := db.QueryRow(ctx, `SELECT id FROM logtest WHERE id = ?`, "a").Scan(&id); err != nil {
		t.Fatalf("QueryRow: %v", err)
	}

	if err := db.InTx(ctx, func(tx *Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE logtest SET v = ? WHERE id = ?`, "y", "a"); err != nil {
			return err
		}
		r, err := tx.Query(ctx, `SELECT id FROM logtest`)
		if err != nil {
			return err
		}
		r.Close()
		var got string
		return tx.QueryRow(ctx, `SELECT v FROM logtest WHERE id = ?`, "a").Scan(&got)
	}); err != nil {
		t.Fatalf("InTx: %v", err)
	}

	if buf.Len() != 0 {
		t.Fatalf("expected zero log output at defaults, got:\n%s", buf.String())
	}
}

// TestLogging_OptedIn_DBPath proves the opted-in tracer logs each DB-path query
// with its SQL and its args verbatim.
func TestLogging_OptedIn_DBPath(t *testing.T) {
	var buf bytes.Buffer
	db := newMemDB(t)
	db.tracer = newLoggingQueryTracer(bufLogger(&buf))
	ctx := context.Background()
	seedLogTable(t, db)

	buf.Reset()
	if _, err := db.Exec(ctx, `INSERT INTO logtest (id, v) VALUES (?, ?)`, "a", "secret-value"); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	assertLogged(t, &buf, "INSERT INTO logtest", "secret-value")

	buf.Reset()
	rows, err := db.Query(ctx, `SELECT id FROM logtest WHERE id = ?`, "a")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	rows.Close()
	assertLogged(t, &buf, "SELECT id FROM logtest", "a")

	buf.Reset()
	var v string
	if err := db.QueryRow(ctx, `SELECT v FROM logtest WHERE id = ?`, "a").Scan(&v); err != nil {
		t.Fatalf("QueryRow: %v", err)
	}
	assertLogged(t, &buf, "SELECT v FROM logtest", "a")
}

// TestLogging_OptedIn_TxPath proves the tracer is inherited by transactions, so
// transaction-path statements log verbatim too — a query log that dropped them
// would be a debugging trap.
func TestLogging_OptedIn_TxPath(t *testing.T) {
	var buf bytes.Buffer
	db := newMemDB(t)
	db.tracer = newLoggingQueryTracer(bufLogger(&buf))
	ctx := context.Background()
	seedLogTable(t, db)
	if _, err := db.Exec(ctx, `INSERT INTO logtest (id, v) VALUES (?, ?)`, "a", "x"); err != nil {
		t.Fatalf("seed row: %v", err)
	}

	buf.Reset()
	if err := db.InTx(ctx, func(tx *Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE logtest SET v = ? WHERE id = ?`, "tx-secret", "a"); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id FROM logtest WHERE id = ?`, "tx-arg")
		if err != nil {
			return err
		}
		rows.Close()
		var v string
		return tx.QueryRow(ctx, `SELECT v FROM logtest WHERE id = ?`, "a").Scan(&v)
	}); err != nil {
		t.Fatalf("InTx: %v", err)
	}

	out := buf.String()
	for _, want := range []string{
		"UPDATE logtest", "tx-secret", // tx.Exec sql + arg verbatim
		"SELECT id FROM logtest", "tx-arg", // tx.Query sql + arg verbatim
		"SELECT v FROM logtest", // tx.QueryRow sql
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("tx-path log missing %q; got:\n%s", want, out)
		}
	}
}

func assertLogged(t *testing.T, buf *bytes.Buffer, wantSQL, wantArg string) {
	t.Helper()
	out := buf.String()
	if !strings.Contains(out, wantSQL) {
		t.Fatalf("log missing SQL %q; got:\n%s", wantSQL, out)
	}
	if !strings.Contains(out, wantArg) {
		t.Fatalf("log missing verbatim arg %q; got:\n%s", wantArg, out)
	}
}
