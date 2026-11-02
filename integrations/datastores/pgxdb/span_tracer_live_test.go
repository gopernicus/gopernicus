package pgxdb

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	jackpgx "github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/sdk"
)

// TestLive_SpanTracerAndStats runs a SpanTracer on a real pool: a traced
// parameterized SELECT, a unique violation and a batch make child spans with
// no argument values; an untraced ping and query make none; Stats moves.
func TestLive_SpanTracerAndStats(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set — SpanTracer and Stats NOT verified live")
	}
	rt := &recTracer{}
	db, err := Open(context.Background(), Config{DSN: sdk.Secret(dsn), MaxConns: 3, Tracer: NewSpanTracer(rt, SpanTracerConfig{})})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	table := fmt.Sprintf("span_probe_%d", time.Now().UnixNano())
	if _, err := db.Exec(ctx, "CREATE TABLE "+table+" (id text CONSTRAINT "+table+"_pk PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Exec(context.Background(), "DROP TABLE IF EXISTS "+table) })
	before := db.Stats()
	untraced := len(rt.spans)
	if err := db.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	if len(rt.spans) != untraced {
		t.Fatalf("untraced work made %d spans", len(rt.spans)-untraced)
	}

	const secret = "argument-value-never-recorded"
	traced := sdk.WithTraceID(ctx, "4bf92f3577b34da6a3ce929d0e0e4736")
	if _, err := db.Exec(traced, "INSERT INTO "+table+" (id) VALUES ($1)", secret); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(traced, "INSERT INTO "+table+" (id) VALUES ($1)", secret); err == nil {
		t.Fatal("duplicate insert succeeded")
	}
	var n int
	if err := db.QueryRow(traced, "SELECT count(*) FROM "+table+" WHERE id = $1", secret).Scan(&n); err != nil || n != 1 {
		t.Fatalf("select: %d, %v", n, err)
	}
	b := &jackpgx.Batch{}
	b.Queue("SELECT 1")
	b.Queue("SELECT $1::text", secret)
	if err := db.Underlying().SendBatch(traced, b).Close(); err != nil {
		t.Fatal(err)
	}

	spans := rt.spans[untraced:]
	var names []string
	for _, s := range spans {
		names = append(names, s.name)
		for k, v := range s.attrs {
			if strings.Contains(v, secret) {
				t.Errorf("span %s attribute %s leaks the argument", s.name, k)
			}
		}
		for _, e := range s.errs {
			if strings.Contains(e, secret) || strings.Contains(e, "duplicate") {
				t.Errorf("span %s error leaks %q", s.name, e)
			}
		}
		if !s.finished {
			t.Errorf("span %s not finished", s.name)
		}
	}
	if strings.Join(names, ",") != "INSERT,INSERT,SELECT,db.batch" {
		t.Fatalf("span names = %v", names)
	}
	dup := spans[1]
	if dup.attrs["db.response.status_code"] != "23505" || dup.attrs["db.postgresql.constraint"] != table+"_pk" || len(dup.errs) != 1 || dup.errs[0] != "sql error 23505" {
		t.Fatalf("duplicate span = %+v", dup)
	}
	if !strings.Contains(spans[2].attrs["db.query.text"], "$1") {
		t.Fatalf("query text lost its placeholder: %q", spans[2].attrs["db.query.text"])
	}

	after := db.Stats()
	if after.MaxConns != 3 || after.AcquireCount <= before.AcquireCount || after.TotalConns < 1 || after.AcquiredConns != 0 {
		t.Fatalf("stats before %+v after %+v", before, after)
	}
}
