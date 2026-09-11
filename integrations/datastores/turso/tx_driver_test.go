package turso

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// txDriver injects lifecycle failures while database/sql owns real pooling and
// Conn.Raw disposal. It deliberately leaves failed transactions active.
type txDriver struct {
	mu                  sync.Mutex
	connections         []*txDriverConn
	beginErr, commitErr error
	rollbackErr         error
	blockRollback       bool
}

type txDriverConn struct {
	owner           *txDriver
	active, closed  bool
	rollbackAlive   bool
	rollbackBounded bool
	commitContext   context.Context
}

func (d *txDriver) Driver() driver.Driver            { return d }
func (d *txDriver) Open(string) (driver.Conn, error) { return d.Connect(context.Background()) }
func (d *txDriver) Connect(context.Context) (driver.Conn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	conn := &txDriverConn{owner: d}
	d.connections = append(d.connections, conn)
	return conn, nil
}
func (c *txDriverConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected Prepare")
}
func (c *txDriverConn) Begin() (driver.Tx, error) { return nil, errors.New("unexpected driver Begin") }
func (c *txDriverConn) Close() error {
	c.owner.mu.Lock()
	defer c.owner.mu.Unlock()
	c.closed = true
	return nil
}
func (c *txDriverConn) ExecContext(ctx context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	d := c.owner
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch query {
	case "BEGIN IMMEDIATE":
		c.active = true
		if d.beginErr != nil {
			return nil, d.beginErr
		}
	case "COMMIT":
		c.commitContext = ctx
		if d.commitErr != nil {
			return nil, d.commitErr
		}
		c.active = false
	case "ROLLBACK":
		deadline, ok := ctx.Deadline()
		c.rollbackAlive = ctx.Err() == nil
		c.rollbackBounded = ok && time.Until(deadline) > 0 && time.Until(deadline) <= rollbackTimeout
		if d.blockRollback {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		if d.rollbackErr != nil {
			return nil, d.rollbackErr
		}
		c.active = false
	}
	return driver.RowsAffected(0), nil
}

func newDriverDB(t *testing.T, d *txDriver) *DB {
	t.Helper()
	pool := sql.OpenDB(d)
	pool.SetMaxOpenConns(1)
	t.Cleanup(func() { pool.Close() })
	return &DB{db: pool}
}

func assertConnectionState(t *testing.T, db *DB, d *txDriver, discarded bool) {
	t.Helper()
	if db.db.Stats().InUse != 0 {
		t.Fatal("transaction kept its pooled connection")
	}
	conn, err := db.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.Raw(func(raw any) error {
		d.mu.Lock()
		defer d.mu.Unlock()
		first := d.connections[0]
		current := raw.(*txDriverConn)
		if discarded {
			if !first.closed || current == first {
				t.Error("unresolved physical connection was reused")
			}
		} else if first.closed || first.active || current != first {
			t.Error("successful cleanup did not release a reusable connection")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestTransactionDriverCleanupFailures(t *testing.T) {
	commitErr := errors.New("commit failed")
	rollbackErr := errors.New("rollback failed")
	callbackErr := errors.New("business rule refused")
	for _, mode := range []string{"InTx", "Transact"} {
		for _, scenario := range []string{"callback rollback failure", "commit failure", "commit and rollback failure", "panic rollback failure"} {
			t.Run(mode+"/"+scenario, func(t *testing.T) {
				d := &txDriver{}
				switch scenario {
				case "callback rollback failure", "panic rollback failure":
					d.rollbackErr = rollbackErr
				case "commit failure":
					d.commitErr = commitErr
				case "commit and rollback failure":
					d.commitErr, d.rollbackErr = commitErr, rollbackErr
				}
				db := newDriverDB(t, d)
				panicValue := new(int)
				var err error
				var panicked any
				func() {
					defer func() { panicked = recover() }()
					err = runTransaction(t, db, mode, context.Background(), func(*Tx) error {
						if scenario == "callback rollback failure" {
							return callbackErr
						}
						if scenario == "panic rollback failure" {
							panic(panicValue)
						}
						return nil
					})
				}()
				if scenario == "panic rollback failure" {
					if panicked != panicValue {
						t.Fatalf("panic changed after cleanup failure: %v", panicked)
					}
				} else {
					if panicked != nil {
						t.Fatalf("unexpected panic: %v", panicked)
					}
					primary := commitErr
					if scenario == "callback rollback failure" {
						primary = callbackErr
					}
					if !errors.Is(err, primary) || (d.rollbackErr != nil && !errors.Is(err, rollbackErr)) {
						t.Fatalf("error lost primary or cleanup cause: %v", err)
					}
				}
				assertConnectionState(t, db, d, d.rollbackErr != nil)
			})
		}
	}
}

func TestTransactionDriverBeginFailureDiscards(t *testing.T) {
	beginErr := errors.New("BEGIN response lost")
	d := &txDriver{beginErr: beginErr}
	db := newDriverDB(t, d)
	if _, err := db.Begin(context.Background()); !errors.Is(err, beginErr) {
		t.Fatalf("Begin error = %v, want original", err)
	}
	assertConnectionState(t, db, d, true)
}

func TestTransactionDriverContexts(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "cancel"}[canceled], func(t *testing.T) {
			d := &txDriver{}
			db := newDriverDB(t, d)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			tx, err := db.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if canceled {
				cancel()
			}
			err = tx.Commit()
			if canceled && !errors.Is(err, context.Canceled) || !canceled && err != nil {
				t.Fatalf("Commit = %v, canceled=%t", err, canceled)
			}
			d.mu.Lock()
			first := d.connections[0]
			if canceled {
				if first.commitContext != nil || !first.rollbackAlive || !first.rollbackBounded {
					t.Error("cancellation did not use independent bounded rollback")
				}
			} else if first.commitContext != ctx {
				t.Error("Commit did not receive the Begin context")
			}
			d.mu.Unlock()
			assertConnectionState(t, db, d, false)
		})
	}
}

func TestTransactionDriverRollbackTimeoutDiscards(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := &txDriver{blockRollback: true}
		db := newDriverDB(t, d)
		tx, err := db.Begin(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		if err := tx.Rollback(); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Rollback = %v, want DeadlineExceeded", err)
		}
		if elapsed := time.Since(start); elapsed != rollbackTimeout {
			t.Fatalf("cleanup lasted %v, want %v", elapsed, rollbackTimeout)
		}
		assertConnectionState(t, db, d, true)
	})
}
