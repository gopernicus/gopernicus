package pgxdb

import (
	"context"
	"os"
	"testing"
)

func TestLive_DefaultPoolReusesConnections(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set — default connection reuse NOT verified")
	}
	db, err := Open(context.Background(), Config{DSN: dsn, MaxConns: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	var first int
	if err := db.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&first); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		var pid int
		if err := db.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
			t.Fatal(err)
		}
		if pid != first {
			t.Fatalf("default lifetime discarded reusable connection: first PID=%d, next=%d", first, pid)
		}
	}
}
