package pgxdb

import (
	"context"
	"errors"

	"github.com/gopernicus/gopernicus/sdk/capabilities/transaction"
)

// ErrNestedTransact reports a nested Transact call. Pass the active callback
// context to participating repositories instead of starting another transaction.
var ErrNestedTransact = errors.New("pgxdb: nested Transact — the workflow already holds a transaction; pass the ambient ctx to repositories instead")

// txCtxKey is the implementation-typed PRIVATE context key the transaction.Transactor
// contract requires: dialect types never cross the sdk package, and only this
// connector's own TxFromContext can retrieve the handle.
type txCtxKey struct{}

// Compile-time proof the connector satisfies the sdk transaction seam.
var _ transaction.Transactor = (*DB)(nil)

// Transact implements sdk/capabilities/transaction.Transactor using InTx's commit and
// cleanup rules. The callback receives a context carrying the transaction for
// TxFromContext and QuerierFrom. Nested Transact calls are rejected.
func (d *DB) Transact(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := TxFromContext(ctx); ok {
		return ErrNestedTransact
	}
	return d.InTx(ctx, func(tx *Tx) error {
		return fn(context.WithValue(ctx, txCtxKey{}, tx))
	})
}

// TxFromContext returns the transaction stashed by an enclosing Transact, or
// (nil, false) outside one. It is the connector-owned typed helper the
// transaction.Transactor contract prescribes in place of any sdk-owned untyped stash.
func TxFromContext(ctx context.Context) (*Tx, bool) {
	tx, ok := ctx.Value(txCtxKey{}).(*Tx)
	return tx, ok
}

// QuerierFrom returns the ambient transaction when ctx carries one and the
// pool otherwise, so the same repository code runs unchanged standalone or
// inside a Transact-owned workflow. The DB must be the same instance that
// began the transaction; the context handle is not an instance-ownership check.
func (d *DB) QuerierFrom(ctx context.Context) Querier {
	if tx, ok := TxFromContext(ctx); ok {
		return tx
	}
	return d
}
