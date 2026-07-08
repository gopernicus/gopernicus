# Phase P2 — connector list toolkits (pgxdb full, turso semantic twin)

Status: **DRAFT — awaiting jrazmi ratification (cut 2026-07-08)**
Executor model: opus
Depends on: P1 (the crud semantics matrix is normative input)

## Context

`pgxdb.ListPage` (pagination.go) is forward-only, `created_at`-hardcoded,
positional-args, hand-scan. This phase builds the replacement toolkit
modeled on gopernicus-original's
`infrastructure/database/postgres/pgxdb/{cursor.go, fop.go, escape.go}` —
NamedArgs tuple-comparison keyset predicates, order-by builders with pk
tiebreaker + direction/forPrevious flipping + optional LOWER(), an
identifier allow-list — plus a new generic `List[T]` that implements the
full P1 matrix over `pgx.CollectRows`/`RowToStructByName`. turso gets a
semantically equivalent `List[T]` (hand-scan, `?` placeholders) — exactly
enough for the extended conformance suites, per the ratified turso-minimal
scope. **This phase is strictly additive**: legacy `ListPage` stays in
both connectors until P6 so every feature store keeps compiling; feature
call sites migrate per feature in P3–P5.

## Goal

Both connectors expose a shared `List[T]` helper implementing ordering,
bidirectional cursor paging, offset mode, and counts, hermetically
unit-tested, with the pgx side built on NamedArgs + struct-scan and the
legacy `ListPage` untouched.

## Definition of Done

- `pgxdb`: `QuoteIdentifier`, `ApplyCursorPagination`, `AddOrderByClause`,
  `AddLimitClause` (NamedArgs, direction/forPrevious/CastLower per the
  original's cursor.go:11-84 + fop.go:27-112) + `List[T]` implementing
  every row of the P1 matrix; cursor order values bound generically from
  the codec's type tags (time.Time UTC, int64, string at minimum —
  float64/bool ride free).
- `turso`: `List[T]` with identical observable semantics (order via
  allow-list membership, prev probe, offset, count; time order values via
  `FormatTime`).
- Hermetic unit tests: SQL-string + args assertions for the builders,
  injection-rejection cases, and (via the existing live_test pattern)
  behavior tests for List where a live DSN is present.
- Querier unchanged (Exec/Query/QueryRow) — the no-SendBatch decision
  recorded in the pgxdb README.
- Legacy `ListPage` (both connectors) byte-untouched; `make check` green.

## The pinned helper shape (names refinable at implementation, semantics not)

```go
// integrations/datastores/pgxdb — T is a store-local db-tagged row struct.
type ListQuery[T any] struct {
    BaseSQL      string                     // "SELECT cols FROM t [WHERE …]" — NO ORDER BY/LIMIT/OFFSET
    Args         pgx.NamedArgs              // the base WHERE's named args
    OrderFields  map[string]crud.OrderField // the aggregate's allow-list
    DefaultOrder crud.Order                 // applied when req.Order is zero
    PK           string                     // tiebreaker + cursor PK column
    OrderValueOf func(row T, field string) any // row's value for the resolved order key
    PKOf         func(row T) string
}

func List[T any](ctx context.Context, db Querier, q ListQuery[T], req crud.ListRequest) (crud.Page[T], error)
```

Behavior contract:

- `req.Validate()` first; resolve `req.Order` against `OrderFields`
  (unknown field → error wrapping `errs.ErrInvalidInput`; zero Order →
  `DefaultOrder`); every identifier that reaches SQL passes
  `QuoteIdentifier`.
- Cursor mode: decode (stale → first page), append the tuple-comparison
  predicate `(order_col, pk) OP (@cursor_order_value, @cursor_pk)` with
  the operator from direction × forPrevious (original cursor.go:73-84),
  ORDER BY order+pk with forPrevious flip, `LIMIT @limit` = n+1,
  `CollectRows(rows, RowToStructByName[T])`, `crud.TrimPage`. When
  `req.Cursor != ""`, run the forPrevious probe (limit n, reversed back)
  and `crud.MarkPrevPage`.
- Offset mode: ORDER BY same, `LIMIT @limit OFFSET @offset` (n+1
  over-fetch → HasMore; HasPrev = Offset > 0; no cursors).
- Count: when `req.WithCount`, run
  `SELECT COUNT(*) FROM (<BaseSQL>) AS list_count` with the same Args
  (the WHERE fragment is reused by construction — this is where
  fragment-reuse lives for helper users; hand-rolled Lists reuse their
  filter-builder funcs instead) and set `Total`.
- Cursor encoding uses the resolved order key as the field tag and
  `OrderValueOf` for the value — the codec's type tags carry it back.
- Errors through `MapError`.

turso twin (`integrations/datastores/turso`): same struct minus NamedArgs
(`Args []any`, `?` placeholders appended in arg order) plus
`Scan func(Scanner) (T, error)` (hand-scan stays — struct-scan is the
follow-up milestone); time order values bound via `FormatTime`; order
safety by allow-list membership (column strings are store-authored
constants). Semantics identical.

## Out of scope

- Deleting/altering legacy `ListPage` (P6).
- Feature call-site migration (P3–P5).
- SendBatch/Begin on Querier; pgx.Batch anywhere.
- turso struct scanning or named-arg emulation (follow-up milestone).
- The original's `$conditions`/`$search` placeholder-replacement scheme —
  filter builders in this repo are plain funcs appending to
  NamedArgs/args (adopted per store in P3–P5, not connector API).

## Module / API impact

Both connector modules gain exported API (`List`, `ListQuery`,
`QuoteIdentifier`, the three builders). No new modules; no go.mod changes
(pgx v5 and the libsql driver are already the modules' single deps).
Zero tags — no release obligation.

## Risks

1. Operator/direction/forPrevious flip errors invert pages silently —
   the builder unit tests must table-test all four
   direction × forPrevious combinations (original cursor.go:73-84 is the
   oracle), and P3's conformance cases are the end-to-end backstop.
2. Tuple comparison `(a, b) < (x, y)` requires comparable types both
   sides — the codec's restored `any` must bind correctly for time
   (UTC-normalized), int64, and string; a type-mismatch test per tag is
   required.

## Tasks

### task-1: pgxdb identifier + NamedArgs builders

- **depends_on:** []
- **model:** opus
- **files:** [integrations/datastores/pgxdb/identifier.go, integrations/datastores/pgxdb/listquery.go, integrations/datastores/pgxdb/identifier_test.go, integrations/datastores/pgxdb/listquery_test.go]
- **verify:** `cd integrations/datastores/pgxdb && go build ./... && go test ./... && go vet ./...`
- **description:** Port `QuoteIdentifier` (original escape.go — the
  regex allow-list; the alias forms may be dropped if unused) and the
  builder trio `ApplyCursorPagination` (tuple comparison into NamedArgs,
  direction/forPrevious operator table, CastLower option),
  `AddOrderByClause` (order col + pk tiebreaker, forPrevious flip,
  CastLower), `AddLimitClause` (`@limit`). Add generic cursor-value
  binding: a helper normalizing the codec's restored values (time.Time →
  UTC) before they enter NamedArgs. Table-test SQL text + args for all
  direction×forPrevious combos and injection rejection
  (`"created_at; DROP"` → error).

### task-2: pgxdb List[T]

- **depends_on:** [task-1]
- **model:** opus
- **files:** [integrations/datastores/pgxdb/list.go, integrations/datastores/pgxdb/list_test.go, integrations/datastores/pgxdb/live_test.go]
- **verify:** `cd integrations/datastores/pgxdb && go build ./... && go test ./... && go vet ./...` (hermetic); live leg (executor-local): `docker run --rm -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:17` then `POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' go test ./...` — List behavior cases green; container removed
- **description:** Implement `List[T]`/`ListQuery[T]` per the pinned
  behavior contract: Validate → order resolution → mode branch → keyset
  predicate/probe or offset → CollectRows+RowToStructByName → TrimPage /
  MarkPrevPage → optional count via the subquery wrap → MapError. Extend
  live_test.go with a scratch-table behavior suite covering: forward
  traversal per order asc/desc, prev-probe full/partial/first-page
  windows, offset traversal equivalence with cursor traversal, count
  correctness under a WHERE filter, stale-cursor-as-first-page, and
  string + int order-value cursors. Legacy `ListPage` untouched.

### task-3: turso List[T] semantic twin

- **depends_on:** [task-2]
- **model:** opus
- **files:** [integrations/datastores/turso/list.go, integrations/datastores/turso/list_test.go]
- **verify:** `cd integrations/datastores/turso && go build ./... && go test ./... && go vet ./... && go vet -tags=integration ./...` then `make check`
- **description:** Implement the turso `List[T]` with observable
  semantics identical to task-2 (order allow-list membership check,
  tuple-comparison predicate with `?` placeholders, prev probe +
  reversal, offset mode, count subquery wrap, `FormatTime` for time
  order values, hand-scan callback). Hermetic tests assert SQL text +
  arg ordering for the same combo table as pgx; behavioral parity is
  proven per feature by the shared storetest suites in P3–P5, not
  duplicated here. Legacy turso `ListPage` untouched. Record the
  no-SendBatch Querier decision in integrations/datastores/pgxdb/README.md
  (one paragraph; part of this task to keep P6's docs pass thin).

## Acceptance

```sh
cd integrations/datastores/pgxdb && go build ./... && go vet ./... && go test ./...
cd integrations/datastores/turso && go build ./... && go vet ./... && go test ./...
make check    # 30 modules — proves additivity (all feature stores still on ListPage)
make guard
git diff --stat integrations/datastores/pgxdb/pagination.go integrations/datastores/turso/pagination.go   # → empty (legacy untouched)
```

## Real-interaction check

Standing check: `make check` green; boot `examples/minimal` (:8081),
`GET /` → 200, kill, port free. The live pgx leg in task-2 is this
phase's real-behavior evidence.

## Execution log

(append dated entries here)
