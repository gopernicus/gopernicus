package pgxdb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"testing"
	"time"

	jackpgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gopernicus/gopernicus/sdk"
)

// TestClassifyUnavailable: connection-level failures gain sdk.ErrUnavailable
// with the original kept; cancellation, deadlines, query errors and no-rows
// are left exactly as they are.
func TestClassifyUnavailable(t *testing.T) {
	opErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	outage := map[string]error{
		"net.OpError":          opErr,
		"wrapped net error":    fmt.Errorf("query: %w", opErr),
		"unexpected EOF":       io.ErrUnexpectedEOF,
		"closed pool":          errors.New("closed pool"),
		"08006 failure":        &pgconn.PgError{Code: "08006"},
		"08001 cannot connect": &pgconn.PgError{Code: "08001"},
		"57P01 admin shutdown": &pgconn.PgError{Code: "57P01"},
		"57P03 starting up":    &pgconn.PgError{Code: "57P03"},
		"53300 too many":       &pgconn.PgError{Code: "53300"},
	}
	for name, err := range outage {
		got := classifyUnavailable(err)
		if !errors.Is(got, sdk.ErrUnavailable) || !errors.Is(got, err) {
			t.Errorf("%s: %v; want ErrUnavailable with the original kept", name, got)
		}
		if mapped := MapError(err); !errors.Is(mapped, sdk.ErrUnavailable) {
			t.Errorf("%s: MapError = %v", name, mapped)
		}
	}
	notOutage := map[string]error{
		"canceled":          context.Canceled,
		"deadline":          context.DeadlineExceeded,
		"wrapped canceled":  fmt.Errorf("query: %w", context.Canceled),
		"statement timeout": &pgconn.PgError{Code: "57014"},
		"unique violation":  &pgconn.PgError{Code: "23505"},
		"no rows":           jackpgx.ErrNoRows,
		"plain":             errors.New("boom"),
	}
	for name, err := range notOutage {
		if got := classifyUnavailable(err); got != err {
			t.Errorf("%s: classified as %v", name, got)
		}
	}
	// Existing MapError results are unchanged.
	for err, want := range map[error]error{
		jackpgx.ErrNoRows:              sdk.ErrNotFound,
		&pgconn.PgError{Code: "23505"}: sdk.ErrAlreadyExists,
		&pgconn.PgError{Code: "23503"}: sdk.ErrInvalidReference,
		&pgconn.PgError{Code: "23514"}: sdk.ErrInvalidInput,
		&pgconn.PgError{Code: "42501"}: sdk.ErrForbidden,
	} {
		if got := MapError(err); !errors.Is(got, want) || errors.Is(got, sdk.ErrUnavailable) {
			t.Errorf("MapError(%v) = %v, want %v", err, got, want)
		}
	}
	if classifyUnavailable(nil) != nil {
		t.Fatal("nil classified")
	}
	already := fmt.Errorf("x: %w", sdk.ErrUnavailable)
	if classifyUnavailable(already) != already {
		t.Fatal("an already-unavailable error was rewrapped")
	}
}

// TestLive_UnavailableOnDeadDatabase: a closed pool and an unreachable server
// both read as sdk.ErrUnavailable on every exit; a healthy pool's constraint
// violation does not.
func TestLive_UnavailableOnDeadDatabase(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set — outage classification NOT verified live")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	healthy, err := Open(ctx, Config{DSN: sdk.Secret(dsn)})
	if err != nil {
		t.Fatal(err)
	}
	table := fmt.Sprintf("unavail_probe_%d", time.Now().UnixNano())
	if _, err := healthy.Exec(ctx, "CREATE TABLE "+table+" (id int PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	defer healthy.Exec(context.Background(), "DROP TABLE IF EXISTS "+table)
	_, _ = healthy.Exec(ctx, "INSERT INTO "+table+" VALUES (1)")
	if _, err := healthy.Exec(ctx, "INSERT INTO "+table+" VALUES (1)"); !errors.Is(err, sdk.ErrAlreadyExists) || errors.Is(err, sdk.ErrUnavailable) {
		t.Fatalf("healthy duplicate = %v", err)
	}

	closed, err := Open(ctx, Config{DSN: sdk.Secret(dsn)})
	if err != nil {
		t.Fatal(err)
	}
	_ = closed.Close()
	checkAll(t, "closed pool", closed, ctx)

	// An unreachable server: Open validates connectivity, so build the DB on a
	// healthy pool's config pointed at a closed port, without Open's ping.
	cfg := healthy.Underlying().Config().Copy()
	cfg.ConnConfig.Port = 1
	cfg.ConnConfig.ConnectTimeout = 2 * time.Second
	pool, err := newPoolForTest(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	dead := &DB{pool: pool}
	defer dead.Close()
	checkAll(t, "unreachable server", dead, ctx)
	_ = healthy.Close()
}

func checkAll(t *testing.T, name string, db *DB, ctx context.Context) {
	t.Helper()
	var n int
	_, beginErr := db.Begin(ctx)
	_, readErr := db.BeginRead(ctx)
	_, execErr := db.Exec(ctx, "SELECT 1")
	for op, err := range map[string]error{
		"Begin":            beginErr,
		"BeginRead":        readErr,
		"TransactSnapshot": db.TransactSnapshot(ctx, func(context.Context) error { return nil }),
		"InTx":             db.InTx(ctx, func(*Tx) error { return nil }),
		"Exec":             execErr,
		"QueryRow.Scan":    db.QueryRow(ctx, "SELECT 1").Scan(&n),
		"Ping":             db.Ping(ctx),
		"StatusCheck":      StatusCheck(ctx, db),
	} {
		if !errors.Is(err, sdk.ErrUnavailable) {
			t.Errorf("%s: %s = %v, want sdk.ErrUnavailable", name, op, err)
		}
	}
}

// newPoolForTest builds a pool without Open's connectivity checks.
func newPoolForTest(ctx context.Context, cfg *pgxpool.Config) (*pgxpool.Pool, error) {
	return pgxpool.NewWithConfig(ctx, cfg)
}
