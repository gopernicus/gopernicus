package turso

import (
	"context"
	"errors"
	"fmt"

	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// ErrNestedTransact is returned when Transact is called from inside an active
// Transact-owned transaction. Nesting was left EXPLICITLY UNPINNED by the seam
// until its first consumer arrived; that consumer's invariant is exactly one
// transaction per workflow (compositions own the Transact call, repositories'
// …InTx twins assume it), so a nested begin would silently split the atomicity
// the caller believes it has. Fail loud: an error can graduate into defined
// nesting behavior later without breaking anyone; silent behavior cannot
// become an error.
var ErrNestedTransact = errors.New("turso: nested Transact — the workflow already holds a transaction; pass the ambient ctx to repositories instead")

// txCtxKey is the implementation-typed PRIVATE context key the crud.Transactor
// contract requires: dialect types never cross the sdk package, and only this
// connector's own TxFromContext can retrieve the handle.
type txCtxKey struct{}

// Compile-time proof the connector satisfies the sdk transaction seam.
var _ crud.Transactor = (*DB)(nil)

// Transact implements sdk/foundation/crud.Transactor: it begins a write-intent
// (BEGIN IMMEDIATE) transaction, stashes the *Tx in the context under the
// connector's private key, and calls fn. A nil return COMMITS; a non-nil error
// ROLLS BACK and is returned unwrapped; a panic inside fn ROLLS BACK and
// re-panics (the deferred rollback runs during unwind — no recover).
// Repositories inside fn reach the transaction through TxFromContext or
// QuerierFrom.
func (d *DB) Transact(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := TxFromContext(ctx); ok {
		return ErrNestedTransact
	}
	tx, err := d.Begin(ctx)
	if err != nil {
		return err
	}
	completed := false
	defer func() {
		if !completed {
			_ = tx.Rollback() // panic path: roll back, then let the panic continue
		}
	}()

	if err := fn(context.WithValue(ctx, txCtxKey{}, tx)); err != nil {
		completed = true
		if rbErr := tx.Rollback(); rbErr != nil {
			return fmt.Errorf("rollback failed: %v (original error: %w)", rbErr, err)
		}
		return err
	}

	completed = true
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit failed: %w", err)
	}
	return nil
}

// TxFromContext returns the transaction stashed by an enclosing Transact, or
// (nil, false) outside one. It is the connector-owned typed helper the
// crud.Transactor contract prescribes in place of any sdk-owned untyped stash.
func TxFromContext(ctx context.Context) (*Tx, bool) {
	tx, ok := ctx.Value(txCtxKey{}).(*Tx)
	return tx, ok
}

// QuerierFrom returns the ambient transaction when ctx carries one and the
// pool otherwise, so the same repository code runs unchanged standalone or
// inside a Transact-owned workflow.
func (d *DB) QuerierFrom(ctx context.Context) Querier {
	if tx, ok := TxFromContext(ctx); ok {
		return tx
	}
	return d
}
