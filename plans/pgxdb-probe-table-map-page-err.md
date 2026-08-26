# pgxdb.ProbeTable + crud.MapPageErr

**Status: EXECUTED 2026-08-25** (ratified in-session by Josh, same day; owner
cuts the tags). Origin: gps-360-go, the first app-local host built on the pgx
store conventions. Its second repository showed two blocks copied verbatim
between every store — and already copied into three framework feature stores.

## Decision

Two additive helpers, one release, no pin moves:

1. `integrations/datastores/pgxdb.ProbeTable(ctx, q Querier, table string) error`
   — the boot-time table-existence probe (`to_regclass`, name bound as a
   parameter, bare or schema-qualified). Absent → wraps `sdk.ErrNotFound`
   naming the relation; a query failure maps through `MapError` and is never
   misreported as missing. Accepts pool or tx.
2. `sdk/foundation/crud.MapPageErr(p Page[T], fn func(T) (U, error)) (Page[U], error)`
   — `MapPage` for a fallible row→domain mapper (stores that VALIDATE on read
   so a drifted stored vocabulary fails loud). Fail-fast, error unwrapped,
   zero Page on failure; page fields copied unchanged on success.

Both are patch-scoped by the RELEASING.md rule (narrowly scoped additive,
zero-value-preserving, no schema, no sibling bump, no config) — **owner
ruling on patch vs minor**, the `sdk/v0.4.1` precedent.

## Changes

1. `integrations/datastores/pgxdb/probe.go` — `ProbeTable`.
2. `integrations/datastores/pgxdb/live_test.go` — `TestLive_ProbeTable`
   (present bare + qualified, absent, inside a tx); env-gated like its siblings.
3. `integrations/datastores/pgxdb/README.md` — surface-table row.
4. `sdk/foundation/crud/pagination.go` — `MapPageErr`.
5. `sdk/foundation/crud/crud_test.go` — `TestMapPageErr` (copy, fail-fast,
   zero page, nil preservation).
6. `RELEASING.md` — summary line + two next-tag upgrade notes.

## Deliberately NOT in this release

The three private `probeTable` copies (`features/{authentication,
authorization}/stores/pgx/postgres.go`, `features/events/stores/pgx/postgres.go`)
stay. Replacing them means each store module pins the new pgxdb tag and retags;
that adoption rides each store's next tag for its own reasons. Their wrapping
messages (naming the migration source) become `fmt.Errorf("…: %w",
pgxdb.ProbeTable(…))` when they do.
