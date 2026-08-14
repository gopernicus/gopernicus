# gopernicus — crud.Transactor implementations in both datastore connectors

Status: COMPLETE 2026-08-14. Both connectors implement the seam; pgx semantics proven live (six tests green against postgres:17); tags integrations/datastores/{pgxdb,turso}/v0.2.0 pushed at 124e3cc; cold-cache `crud.Transactor = (*pgxdb.DB)` compile proof passed. Nesting ruled FAIL-LOUD (ErrNestedTransact); sdk contract comment updated (comment-only).
Date: 2026-08-14
Driver: Coordination-Hub PR #13 finding 1 — the timeline cascade is the first real
consumer of the sdk's SCAFFOLDED, UNCONSUMED `crud.Transactor` seam
(`sdk/foundation/crud/tx.go`), and neither connector implements it; the host's
outbound adapters (that PR's next slice) are blocked until one does.

## Contract facts (pinned by sdk/foundation/crud/tx.go — no sdk change needed)

Commit on nil; rollback + return fn's error UNWRAPPED; rollback + re-panic on
panic; tx handle rides ctx under an implementation-typed PRIVATE key with a
connector-owned typed helper (`pgxdb.TxFromContext` / `turso.TxFromContext`) —
deliberately no sdk-owned stash. `InTx` signatures stay; `Transact` is ADDED
(the tx.go implementation note says exactly this). `InTx` has no panic handling
— `Transact` supplies its own via defer-rollback (no recover: the deferred
rollback runs during unwind and the panic continues).

**Nesting ruling (was EXPLICITLY UNPINNED; the first consumer has now arrived):**
FAIL LOUD — `Transact` inside an active `Transact` ctx returns
`ErrNestedTransact`. The consumer's own invariant is exactly one transaction
(PR #13's `TestChangeDatesOpensExactlyOneTransaction`; compositions own the
Transact, `…InTx` twins assume it), so silent re-begin would split atomicity
undetected. An error can graduate into behavior later (minor); silent behavior
cannot become an error without breaking. Recorded in tx.go's contract comment.

## Deliverables (per connector: pgxdb AND turso — connector parity)

- `Transact(ctx, fn func(ctx) error) error` on `*DB` + compile-time
  `var _ crud.Transactor = (*DB)(nil)`.
- `TxFromContext(ctx) (*Tx, bool)` — the connector-owned typed helper.
- `QuerierFrom(ctx) Querier` on `*DB` — returns the ambient `*Tx` when ctx
  carries one, else the pool; the one-liner host repositories call so the same
  store code runs standalone or composed (the querier-seam convention
  coordination-hub's ARCHITECTURE already documents).
- Tests: nested-fail-loud + TxFromContext-absent (no DB needed; the nested check
  precedes Begin); live env-gated legs (POSTGRES_TEST_DSN / turso integration
  tag) for commit-on-nil, rollback-on-error-unwrapped, rollback-on-panic-and-
  re-panic, ambient-tx visibility through QuerierFrom.
- sdk `crud/tx.go` comment updated: seam CONSUMED (PR #13's cascade), nesting
  ruling recorded. Comment-only — no sdk API change, no sdk tag.
- RELEASING.md upgrade note; tags `integrations/datastores/pgxdb/v0.2.0` +
  `integrations/datastores/turso/v0.2.0` (additive minor).

## Coordination

The jobs-tenant-metadata window works the same repo (features/jobs + stores +
RELEASING.md). Disjoint modules; RELEASING.md is the only shared file — pull
--rebase before pushing, keep the note self-contained. Live pgx tests use a
throwaway postgres:17 on 5433, never coordination-hub's 5432 container.
