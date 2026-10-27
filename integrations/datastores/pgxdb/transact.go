package pgxdb

import (
	"context"
	"errors"
	"fmt"

	"github.com/gopernicus/gopernicus/sdk/capabilities/transaction"
	jackpgx "github.com/jackc/pgx/v5"
)

// ErrNestedTransact reports a nested Transact call on the same *DB. Pass the
// active callback context to that instance's repositories instead of starting
// another transaction. Another instance's transaction never triggers it.
var ErrNestedTransact = errors.New("pgxdb: nested Transact — this database instance already holds a transaction; pass the ambient ctx to this instance's repositories instead")

// txKey scopes an ambient transaction to the *DB that began it. Two instances
// layer two values on one context; each TxFrom reads only its own. It is the
// implementation-typed PRIVATE context key the transaction.Transactor contract
// requires: dialect types never cross the sdk package, and only this connector
// can read or forge a slot.
type txKey struct{ db *DB }

// Compile-time proof the connector satisfies the sdk transaction seam.
var _ transaction.Transactor = (*DB)(nil)

// Transact implements sdk/capabilities/transaction.Transactor using InTx's commit and
// cleanup rules. The callback receives a context carrying the transaction for
// d.TxFrom and d.QuerierFrom. A nested Transact on the same instance is
// rejected; another instance's transaction in ctx is neither joined nor refused.
func (d *DB) Transact(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := d.TxFrom(ctx); ok {
		return ErrNestedTransact
	}
	return d.InTx(ctx, func(tx *Tx) error {
		return fn(context.WithValue(ctx, txKey{d}, tx))
	})
}

// TransactSnapshot runs fn in a read-write, repeatable-read transaction. Reads
// share the snapshot established by the first data statement and see this
// transaction's own writes. It uses Transact's ambient context and cleanup rules;
// nesting on the same instance is rejected and callbacks are never retried.
func (d *DB) TransactSnapshot(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := d.TxFrom(ctx); ok {
		return ErrNestedTransact
	}
	driver, err := d.pool.BeginTx(ctx, jackpgx.TxOptions{IsoLevel: jackpgx.RepeatableRead, AccessMode: jackpgx.ReadWrite})
	if err != nil {
		return fmt.Errorf("beginning snapshot transaction: %w", err)
	}
	tx := &Tx{tx: driver, ctx: ctx}
	return tx.run(func(tx *Tx) error {
		return fn(context.WithValue(ctx, txKey{d}, tx))
	})
}

// TxFrom returns d's ambient transaction, stashed by d.Transact or
// d.TransactSnapshot, or (nil, false) when ctx carries none for d — including
// when it carries another instance's. It is the connector-owned typed helper the
// transaction.Transactor contract prescribes in place of any sdk-owned untyped stash.
func (d *DB) TxFrom(ctx context.Context) (*Tx, bool) {
	tx, ok := ctx.Value(txKey{d}).(*Tx)
	return tx, ok
}

// QuerierFrom returns d's ambient transaction when ctx carries one and d's pool
// otherwise, so the same repository code runs unchanged standalone or inside a
// Transact-owned workflow. Another instance's transaction is invisible: the SQL
// runs on d.
func (d *DB) QuerierFrom(ctx context.Context) Querier {
	if tx, ok := d.TxFrom(ctx); ok {
		return tx
	}
	return d
}
