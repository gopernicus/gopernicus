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
)

func TestOpenCanceledBeforeConnecting(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, attempts := range []int{0, 2} {
		db, err := Open(ctx, Config{DSN: testDSN, Retry: RetryPolicy{Attempts: attempts}})
		if db != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("Open with attempts=%d = %v, %v; want nil, context.Canceled", attempts, db, err)
		}
	}
}

func TestOpenCancelsInFlightStartup(t *testing.T) {
	for _, attempts := range []int{0, 2} {
		t.Run(fmt.Sprint(attempts), func(t *testing.T) {
			dsn, connected := stalledPostgres(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				db, err := Open(ctx, Config{DSN: dsn, Retry: RetryPolicy{Attempts: attempts}})
				if db != nil {
					_ = db.Close()
					err = fmt.Errorf("Open returned a DB: %v", err)
				}
				done <- err
			}()
			select {
			case <-connected:
			case <-time.After(3 * time.Second):
				t.Fatal("startup never reached the server")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("Open = %v, want context.Canceled", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("Open did not cancel its in-flight connection and cleanup")
			}
		})
	}
}

func TestOpenHonorsEarlierStartupDeadline(t *testing.T) {
	for _, hostFirst := range []bool{true, false} {
		t.Run(fmt.Sprint(hostFirst), func(t *testing.T) {
			dsn, _ := stalledPostgres(t)
			hostTimeout, connectionTimeout := 3*time.Second, 50*time.Millisecond
			if hostFirst {
				hostTimeout, connectionTimeout = connectionTimeout, hostTimeout
			}
			ctx, cancel := context.WithTimeout(context.Background(), hostTimeout)
			defer cancel()
			started := time.Now()
			db, err := Open(ctx, Config{DSN: dsn, ConnectTimeout: connectionTimeout})
			if db != nil || !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("Open = %v, %v; want nil, context.DeadlineExceeded", db, err)
			}
			if time.Since(started) >= 2*time.Second {
				t.Fatal("Open ignored the earlier startup deadline")
			}
		})
	}
}

// stalledPostgres accepts TCP but never completes the PostgreSQL handshake.
func stalledPostgres(t *testing.T) (string, <-chan struct{}) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	connected := make(chan struct{})
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		close(connected)
		_, _ = io.Copy(io.Discard, conn)
	}()
	return "postgres://test:test@" + listener.Addr().String() + "/test?sslmode=disable", connected
}

func TestLive_OpenContextDoesNotOwnDatabaseLifetime(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set — post-startup database lifetime NOT verified")
	}
	for _, attempts := range []int{0, 2} {
		t.Run(fmt.Sprint(attempts), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			db, err := Open(ctx, Config{DSN: dsn, MaxConns: 1, Retry: RetryPolicy{Attempts: attempts}})
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			cancel()
			// Replacing all connections also proves future dials do not inherit
			// the context used during startup.
			db.Underlying().Reset()
			queryCtx, queryCancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer queryCancel()
			var got int
			if err := db.QueryRow(queryCtx, "SELECT 1").Scan(&got); err != nil || got != 1 {
				t.Fatalf("query after startup cancellation = %d, %v", got, err)
			}
		})
	}
}
