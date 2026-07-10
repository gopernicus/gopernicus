package crud

import "context"

// Transactor is the sdk-level transaction seam.
//
// SCAFFOLDED, UNCONSUMED (datastore-hardening P6, 2026-07-09): no feature
// consumes this interface yet. It exists so that when the consumer trigger
// fires — the third durable emitter, i.e. the first use-case spanning two
// repositories in one transaction (portability §8b) — the vocabulary is
// already agreed and nobody works around the missing seam by reaching for a
// connector's Underlying() handle (guard G9 bans exactly that).
//
// Contract, pinned now so implementations cannot diverge before the first
// consumer arrives:
//
//   - Transact begins a transaction, calls fn, and COMMITS when fn returns
//     nil.
//   - A non-nil error from fn ROLLS BACK and is returned unwrapped.
//   - A panic inside fn ROLLS BACK and re-panics.
//   - The context passed to fn carries the implementation's own transaction
//     handle under an implementation-typed private key (the tx-in-context
//     convention): each datastore connector exposes its OWN typed helper
//     (e.g. a future turso.TxFromContext / pgxdb.TxFromContext) that its
//     repositories use to find the transaction. Dialect types never cross
//     this package — there is deliberately NO sdk-owned context stash, no
//     WithTx(ctx, any), no TxFromContext(ctx) any: an untyped stash would be
//     a service-locator hole, the same workaround class G9 exists to ban.
//   - Nesting (Transact inside fn) is EXPLICITLY UNPINNED — neither allowed
//     nor forbidden here — and is decided when the first real consumer
//     arrives, not before.
//
// Implementation note: both datastore connectors already carry a
// dialect-typed InTx(ctx, fn func(*Tx) error). Those signatures stay; a
// connector satisfies this seam by ADDING a separate Transact method (the
// name differs from InTx deliberately — Go forbids two same-named methods,
// and renaming the 18 existing InTx call sites for an unconsumed seam would
// be churn without a consumer).
type Transactor interface {
	Transact(ctx context.Context, fn func(ctx context.Context) error) error
}
