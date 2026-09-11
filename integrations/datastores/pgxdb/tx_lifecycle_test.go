package pgxdb

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	jackpgx "github.com/jackc/pgx/v5"
)

// lifecycleTx exercises the connector's transaction ownership against pgx's Tx
// interface. PostgreSQL pool/wire behavior remains covered by the live tests.
type lifecycleTx struct {
	jackpgx.Tx
	closed, closeOnCommitError bool
	blockRollback              bool
	commitErr, rollbackErr     error
	calls                      []string
	commitContext              context.Context
	rollbackAlive              bool
	rollbackBounded            bool
}

func (t *lifecycleTx) Commit(ctx context.Context) error {
	if t.closed {
		return jackpgx.ErrTxClosed
	}
	t.calls = append(t.calls, "commit")
	t.commitContext = ctx
	if t.commitErr != nil {
		t.closed = t.closeOnCommitError
		return t.commitErr
	}
	t.closed = true
	return nil
}

func (t *lifecycleTx) Rollback(ctx context.Context) error {
	if t.closed {
		return jackpgx.ErrTxClosed
	}
	t.calls = append(t.calls, "rollback")
	t.closed = true
	deadline, ok := ctx.Deadline()
	t.rollbackAlive = ctx.Err() == nil
	t.rollbackBounded = ok && time.Until(deadline) > 0 && time.Until(deadline) <= rollbackTimeout
	if t.blockRollback {
		<-ctx.Done()
		return ctx.Err()
	}
	return t.rollbackErr
}

func TestTransactionLifecycle(t *testing.T) {
	callbackErr := errors.New("business rule refused")
	commitErr := errors.New("commit failed")
	rollbackErr := errors.New("rollback failed")
	panicValue := new(int)
	for _, scenario := range []string{
		"commit", "error", "panic", "canceled commit", "canceled error",
		"commit failure", "callback rollback failure", "commit and rollback failure", "panic rollback failure",
	} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			driver := &lifecycleTx{}
			switch scenario {
			case "commit failure":
				driver.commitErr = commitErr
			case "callback rollback failure", "panic rollback failure":
				driver.rollbackErr = rollbackErr
			case "commit and rollback failure":
				driver.commitErr, driver.rollbackErr = commitErr, rollbackErr
			}
			tx := &Tx{tx: driver, ctx: ctx}
			var err error
			var panicked any
			func() {
				defer func() { panicked = recover() }()
				err = tx.run(func(*Tx) error {
					switch scenario {
					case "error", "callback rollback failure":
						return callbackErr
					case "panic", "panic rollback failure":
						panic(panicValue)
					case "canceled commit":
						cancel()
					case "canceled error":
						cancel()
						return callbackErr
					}
					return nil
				})
			}()
			wantCalls := []string{"rollback"}
			switch scenario {
			case "commit":
				wantCalls = []string{"commit"}
				if err != nil {
					t.Fatalf("commit: %v", err)
				}
			case "error", "canceled error":
				if err != callbackErr {
					t.Fatalf("callback error = %v, want identical original", err)
				}
			case "canceled commit":
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("commit error = %v, want context.Canceled", err)
				}
			case "commit failure", "commit and rollback failure":
				wantCalls = []string{"commit", "rollback"}
				if !errors.Is(err, commitErr) {
					t.Fatalf("commit error lost: %v", err)
				}
			case "callback rollback failure":
				if !errors.Is(err, callbackErr) {
					t.Fatalf("callback error lost: %v", err)
				}
			case "panic", "panic rollback failure":
				if panicked != panicValue {
					t.Fatalf("panic changed: %v", panicked)
				}
			}
			if driver.rollbackErr != nil && scenario != "panic rollback failure" && !errors.Is(err, rollbackErr) {
				t.Fatalf("cleanup error lost: %v", err)
			}
			if scenario != "panic" && scenario != "panic rollback failure" && panicked != nil {
				t.Fatalf("unexpected panic: %v", panicked)
			}
			if !driver.closed || !slices.Equal(driver.calls, wantCalls) {
				t.Fatalf("closed=%t, calls=%v; want %v", driver.closed, driver.calls, wantCalls)
			}
			if slices.Contains(wantCalls, "rollback") && (!driver.rollbackAlive || !driver.rollbackBounded) {
				t.Error("rollback did not receive an independent bounded context")
			}
			if slices.Contains(wantCalls, "commit") && driver.commitContext != ctx {
				t.Error("commit did not receive Begin context")
			}
		})
	}
}

func TestTransactionCommitAlreadyFinalizedFailure(t *testing.T) {
	commitErr := errors.New("driver commit failed and closed transaction")
	driver := &lifecycleTx{commitErr: commitErr, closeOnCommitError: true}
	tx := &Tx{tx: driver, ctx: context.Background()}
	if err := tx.Commit(); err != commitErr {
		t.Fatalf("error = %v, want original commit error", err)
	}
	if !slices.Equal(driver.calls, []string{"commit"}) {
		t.Fatalf("finalized transaction received extra SQL: %v", driver.calls)
	}
}

func TestTransactionRollbackTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		driver := &lifecycleTx{blockRollback: true}
		tx := &Tx{tx: driver, ctx: context.Background()}
		start := time.Now()
		if err := tx.Rollback(); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Rollback = %v, want DeadlineExceeded", err)
		}
		if elapsed := time.Since(start); elapsed != rollbackTimeout {
			t.Fatalf("cleanup lasted %v, want %v", elapsed, rollbackTimeout)
		}
	})
}
