package crud

import "context"

// Transactor is the sdk-level transaction seam.
//
// CONSUMED (transactor-connectors, 2026-08-14): the trigger named below fired
// — the first use-case spanning two repositories in one transaction is the
// Coordination-Hub timeline cascade (gpsimpact/Coordination-Hub#13), and both
// datastore connectors now implement the seam (pgxdb.Transact/TxFromContext/
// QuerierFrom and the turso mirror). Originally scaffolded unconsumed
// (datastore-hardening P6, 2026-07-09) so the vocabulary was agreed before
// anyone worked around the missing seam by reaching for a connector's
// Underlying() handle (guard G9 bans exactly that).
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
//   - Nesting (Transact inside fn) — RULED with the first consumer
//     (transactor-connectors, 2026-08-14): implementations FAIL LOUD, returning
//     a connector-typed sentinel (pgxdb.ErrNestedTransact / turso's mirror).
//     The consumer's invariant is exactly one transaction per workflow
//     (compositions own the Transact call; repositories' …InTx twins assume
//     it), so a silent nested begin would split the atomicity the caller
//     believes it has. The error may graduate into defined nesting behavior
//     later (a minor change); silent behavior could never become an error.
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
