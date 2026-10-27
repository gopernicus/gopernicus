package pgxdb

import (
	"context"
	"errors"
	"testing"
)

// TestTxFrom_PerInstance needs no database: each *DB reads only the slot it
// stashed, so two instances' transactions layer on one context without
// shadowing each other, and a foreign transaction is invisible.
func TestTxFrom_PerInstance(t *testing.T) {
	a, b := &DB{}, &DB{}
	txA, txB := &Tx{}, &Tx{}
	onlyA := context.WithValue(t.Context(), txKey{a}, txA)
	both := context.WithValue(onlyA, txKey{b}, txB)

	if tx, ok := a.TxFrom(both); !ok || tx != txA {
		t.Fatalf("a.TxFrom(both) = (%p, %t), want (%p, true)", tx, ok, txA)
	}
	if tx, ok := b.TxFrom(both); !ok || tx != txB {
		t.Fatalf("b.TxFrom(both) = (%p, %t), want (%p, true)", tx, ok, txB)
	}
	if tx, ok := b.TxFrom(onlyA); ok || tx != nil {
		t.Fatalf("b.TxFrom(onlyA) = (%v, %t), want (nil, false)", tx, ok)
	}
	if q := a.QuerierFrom(onlyA); q != Querier(txA) {
		t.Fatalf("a.QuerierFrom(onlyA) = %T %p, want A's transaction", q, q)
	}
	if q := b.QuerierFrom(onlyA); q != Querier(b) {
		t.Fatalf("b.QuerierFrom(onlyA) = %T %p, want B's pool", q, q)
	}
	if q := b.QuerierFrom(both); q != Querier(txB) {
		t.Fatalf("b.QuerierFrom(both) = %T %p, want B's transaction", q, q)
	}

	for name, ctx := range map[string]context.Context{"onlyA": onlyA, "both": both} {
		for method, transact := range map[string]func(context.Context, func(context.Context) error) error{
			"Transact":         a.Transact,
			"TransactSnapshot": a.TransactSnapshot,
		} {
			err := transact(ctx, func(context.Context) error {
				t.Fatalf("%s/%s: nested transaction invoked callback", name, method)
				return nil
			})
			if !errors.Is(err, ErrNestedTransact) {
				t.Fatalf("%s/%s on A's own ctx = %v, want ErrNestedTransact", name, method, err)
			}
		}
	}
}
