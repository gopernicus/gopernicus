package turso

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenCanceledBeforeOpeningFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "database.sqlite")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, attempts := range []int{0, 2} {
		db, err := Open(ctx, Config{URL: "file:" + path, Retry: RetryPolicy{Attempts: attempts}})
		if db != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("Open with attempts=%d = %v, %v; want nil, context.Canceled", attempts, db, err)
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("canceled startup touched the database file: %v", err)
		}
	}
}

func TestOpenCancelsHTTPStartupAndRetry(t *testing.T) {
	for _, refuse := range []bool{false, true} {
		t.Run(fmt.Sprint(refuse), func(t *testing.T) {
			entered := make(chan struct{}, 1)
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				entered <- struct{}{}
				if refuse {
					w.WriteHeader(http.StatusServiceUnavailable)
					_, _ = w.Write([]byte(`{"error":"fixture refusal"}`))
					return
				}
				select {
				case <-r.Context().Done():
				case <-release:
				}
			}))
			defer server.Close()
			defer close(release)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				db, err := Open(ctx, Config{URL: server.URL, Retry: RetryPolicy{Attempts: 3, MinBackoff: 5 * time.Second}})
				if db != nil {
					_ = db.Close()
					err = fmt.Errorf("Open returned a DB: %v", err)
				}
				done <- err
			}()
			select {
			case <-entered:
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
				t.Fatal("Open did not cancel startup before the retry delay")
			}
		})
	}
}

func TestOpenContextDoesNotOwnDatabaseLifetime(t *testing.T) {
	for _, attempts := range []int{0, 2} {
		t.Run(fmt.Sprint(attempts), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			db, err := Open(ctx, Config{
				URL:   "file:" + filepath.Join(t.TempDir(), "database.sqlite"),
				Retry: RetryPolicy{Attempts: attempts},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			cancel()
			// The default idle limit closes startup's connection, so this query
			// verifies a fresh connection also survives startup cancellation.
			queryCtx, queryCancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer queryCancel()
			var got int
			if err := db.QueryRow(queryCtx, "SELECT 1").Scan(&got); err != nil || got != 1 {
				t.Fatalf("query after startup cancellation = %d, %v", got, err)
			}
		})
	}
}
