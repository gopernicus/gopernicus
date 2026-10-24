package turso

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/logging"
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
// with its SQL, argument count and types, and the context's request_id, never
// an argument value.
func TestLogging_OptedIn_DBPath(t *testing.T) {
	var buf bytes.Buffer
	db := newMemDB(t)
	db.tracer = newLoggingQueryTracer(Config{Logger: slog.New(logging.NewContextHandler(slog.NewJSONHandler(&buf, nil)))})
	ctx := sdk.WithRequestID(context.Background(), "req-turso")
	seedLogTable(t, db)

	buf.Reset()
	if _, err := db.Exec(ctx, `INSERT INTO logtest (id, v) VALUES (?, ?)`, "a", "secret-value"); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	assertArity(t, &buf, "INSERT INTO logtest", `"arg_count":2,"arg_types":["string","string"]`, "secret-value")

	buf.Reset()
	rows, err := db.Query(ctx, `SELECT id FROM logtest WHERE id = ?`, "query-arg")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	rows.Close()
	assertArity(t, &buf, "SELECT id FROM logtest", `"arg_count":1,"arg_types":["string"]`, "query-arg")

	buf.Reset()
	var v string
	if err := db.QueryRow(ctx, `SELECT v FROM logtest WHERE id = ?`, "a").Scan(&v); err != nil {
		t.Fatalf("QueryRow: %v", err)
	}
	assertArity(t, &buf, "SELECT v FROM logtest", `"arg_count":1,"arg_types":["string"]`, "")
}

// TestLogging_OptedIn_TxPath proves the tracer is inherited by transactions, so
// transaction-path statements log too — a query log that dropped them would be
// a debugging trap — with values only under LogQueryArgs.
func TestLogging_OptedIn_TxPath(t *testing.T) {
	for _, logArgs := range []bool{false, true} {
		var buf bytes.Buffer
		db := newMemDB(t)
		db.tracer = newLoggingQueryTracer(Config{Logger: bufLogger(&buf), LogQueryArgs: logArgs})
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
		for _, want := range []string{"UPDATE logtest", "SELECT id FROM logtest", "SELECT v FROM logtest", `"arg_count":2`} {
			if !strings.Contains(out, want) {
				t.Fatalf("logArgs=%v: tx-path log missing %q; got:\n%s", logArgs, want, out)
			}
		}
		for _, value := range []string{"tx-secret", "tx-arg"} {
			if strings.Contains(out, value) != logArgs {
				t.Fatalf("logArgs=%v: tx-path log carries %q = %v; got:\n%s", logArgs, value, !logArgs, out)
			}
		}
	}
}

func assertArity(t *testing.T, buf *bytes.Buffer, wantSQL, wantArity, value string) {
	t.Helper()
	out := buf.String()
	for _, want := range []string{wantSQL, wantArity, `"request_id":"req-turso"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("log missing %s; got:\n%s", want, out)
		}
	}
	if strings.Contains(out, `"args"`) || (value != "" && strings.Contains(out, value)) {
		t.Fatalf("log carries an argument value; got:\n%s", out)
	}
}
