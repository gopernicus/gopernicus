# Phase P6 — legacy deletion + docs + records

Status: **DRAFT — awaiting jrazmi ratification (cut 2026-07-08)**
Executor model: opus (task-1) / fable (tasks 2–3)
Depends on: P2–P5 (every `ListPage` call site migrated)

## Context

P2 was additive by design; with P3–P5 landed, `pgxdb.ListPage` and
`turso.ListPage` have zero callers and come out. Then the documentation
surface syncs: connector READMEs, sdk/README's crud row, and the
cross-milestone amendment note onto authorization-v1's DRAFT store phases
(which cite "keyset `ListPage[T]`" as their list contract). Milestone
records land in NOTES.md with the live-run artifacts.

## Goal

Zero legacy list helpers, docs that describe the shipped standards, and
the milestone closed with recorded live parity runs.

## Definition of Done

- `ListPage` gone from both connectors (code + tests), zero callers by
  grep, `make check` + `make guard` green at 30 modules.
- pgxdb README documents the list toolkit (List/ListQuery, builders,
  QuoteIdentifier, the no-SendBatch Querier decision from P2) and the
  row-struct + toDomain store convention; turso README notes the
  semantic-twin `List` and names the turso idiom-parity follow-up.
- sdk/README crud row + `features/README.md` (if it references list
  behavior) updated; the standard query-param vocabulary
  (`limit`/`cursor`/`offset`/`count`/`order`) documented once, pointed to
  from the feature READMEs that expose list endpoints.
- authorization-v1 `00-overview.md`/`02a-store-turso.md`/`02b-store-pgx.md`
  carry a dated cross-milestone note: their store phases execute against
  the pgx-crud-v1 list standards (`pgxdb.List`/`turso.List`, order
  allow-lists, the six-case storetest family), superseding their
  `ListPage` citations. A note, not a rewrite — that plan stays DRAFT
  under its own ratification.
- NOTES.md milestone entry with the dated live artifacts (one
  `make test-stores` run green post-P5, plus the per-phase pgx/turso leg
  records), and the declared follow-ups: turso idiom parity
  (`turso-crud-parity`), any wanted-index log entries from P3–P5.

## Out of scope

- Any semantic change anywhere — this phase deletes and documents.
- Executing anything in authorization-v1.

## Risks

1. A missed `ListPage` caller (or a doc/test referencing it) turns
   deletion red — the grep in task-1's verify runs before the delete.

## Tasks

### task-1: delete legacy ListPage from both connectors

- **depends_on:** []
- **model:** opus
- **files:** [integrations/datastores/pgxdb/pagination.go, integrations/datastores/pgxdb/pagination_test.go, integrations/datastores/turso/pagination.go, integrations/datastores/turso/pagination_test.go]
- **verify:** `grep -rn "ListPage" --include='*.go' . | grep -v _test.go` → only the definitions before deleting; after: `grep -rn "ListPage" --include='*.go' .` → empty; `cd integrations/datastores/pgxdb && go build ./... && go test ./... && go vet ./...`; same for turso (+ `go vet -tags=integration ./...`); then `make check` and `make guard`
- **description:** Remove `ListPage` and its tests from both connector
  modules (pagination.go files go away entirely if nothing else lives in
  them; keep anything unrelated). Surgical — no drive-by refactors of the
  new list.go files.

### task-2: connector + sdk + feature docs

- **depends_on:** [task-1]
- **model:** fable
- **files:** [integrations/datastores/pgxdb/README.md, integrations/datastores/turso/README.md, sdk/README.md, features/README.md]
- **verify:** `make check` (docs only — proves nothing broke); manual read-through against the shipped code
- **description:** Document the list standards where developers will look:
  the pgxdb README's toolkit section (ListQuery contract, the mode
  matrix by reference to the sdk/crud package doc, filter-builder
  convention, UNNEST bulk-write convention, the Querier no-SendBatch
  rationale if P2 hasn't already landed it), the turso README's
  semantic-twin note + named follow-up milestone, sdk/README's crud
  facility row (ordering, bidirectional cursor + offset modes, counts),
  and the query-param vocabulary in features/README.md if that charter
  is where feature HTTP conventions live (follow the existing doc
  structure — do not invent a new doc home).

### task-3: cross-milestone note + NOTES record

- **depends_on:** [task-1]
- **model:** fable
- **files:** [.claude/plans/authorization-v1/00-overview.md, .claude/plans/authorization-v1/02a-store-turso.md, .claude/plans/authorization-v1/02b-store-pgx.md, NOTES.md]
- **verify:** manual read-through; `git diff` shows only appended dated notes on the authorization-v1 files (no task/scope edits)
- **description:** Append a dated "pgx-crud-v1 landed" note to the three
  authorization-v1 files: store phases execute against `pgxdb.List` /
  `turso.List` + order allow-lists + the extended storetest family; the
  `ListPage`/D2–D6 citations read accordingly. Write the NOTES.md
  milestone entry: what shipped, the live-run artifacts (dated, per
  dialect, incl. one full `make test-stores`), the declared follow-ups
  (turso-crud-parity; any wanted-index log from P3–P5), and the
  breaking-API note (sdk/crud ParseListRequest signature, connector
  ListPage removal — zero tags existed, no consumers broken).

## Acceptance

```sh
grep -rn "ListPage" --include='*.go' .   # → empty
make check && make guard                  # 30 modules, seven guards
POSTGRES_TEST_DSN=… make test-stores      # one recorded full live run (turso legs need TURSO_* too)
```

## Real-interaction check

Standing check: `make check` green; boot `examples/minimal` (:8081) →
200s; `make run` (examples/cms) → admin entries list pages both
directions (the P4 protocol, spot-checked once more post-deletion); kill,
ports free.

## Execution log

(append dated entries here)
