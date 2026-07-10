# datastore-hardening — connector parity, strictness, and the scaffolded seams

Status: **RATIFIED 2026-07-09 (jrazmi, in-session) — Q1 FULL SWEEP · Q2
crud.Transactor (tx-in-context) · Q3 PURE OPT-IN · Q4 gate APPROVED (runs
before execution; findings folded on return). EXECUTING under the
2026-07-09 owner loop directive (autonomous while away).**
Origin: the 2026-07-09 post-authorization-v1 connector audit (in-session;
findings 1–9 with owner answers taken same session — recorded verbatim in
"Owner rulings" below). No design doc — the audit + this plan are the record.
Executor model policy (standing): implementation tasks `model: opus`;
design/doc-judgment tasks `model: fable`. Never sonnet.
Modules: **no count change** (34 stands). sdk gains pre-tag API (P6);
RELEASING's zero-tags posture means no version-bump obligation.

## Owner rulings (2026-07-09, in-session — the audit's findings 1–9)

1. Authorization order-allow-list rim divergence → **FIX** (P1).
2. turso List PK/identifier validation → **STRICTNESS** (P2 — accepting
   the consequence: the turso roles store's raw-expression tiebreak must
   rework to the pgx derived-column pattern).
3. StatusCheck/health routes in examples → **YES** (P3).
4. turso query-observability parity → **SCAFFOLD** (P4).
5. turso struct scanning without reflection → answered: **no such path
   short of codegen** (closed direction); reflection helper RECOMMENDED,
   confined to the connector, strict-only (P5; scope = Q1).
6. sdk transaction seam → **SCAFFOLD unconsumed + `Underlying()` guard**
   (P6; shape = Q2).
7. Retry stance → **configurable-with-default** — Config-level policy
   recommended over context-plumbed (P7; default = Q3).
8. `RowToStructByNameLax` guard → **YES** (folded into P6's guard task).
9. exhaustruct completeness linting → **DECLINED** (owner: no tooling
   overhead). Recorded; not cut into any phase.

## Phases

| Phase | What | Size | Depends | Model |
|---|---|---|---|---|
| P1 | authorization order allow-lists → domain rims | S | — | opus |
| P2 | turso List identifier strictness + roles-tiebreak rework | M | — | opus |
| P3 | health routes on the four example hosts | S | — | opus |
| P4 | turso query logging + redaction parity | S–M | — | opus |
| P5 | turso struct-scan helper + row-struct sweep | **L** (resized at the gate fold — lead 5: ~20 row-struct + toDomain pairs authored, not a callback deletion) | P2 | opus |
| P6 | sdk transaction seam scaffold + G9/G10 guards | S–M | — | fable (design) → opus (guards) |
| P7 | connector retry policy (design-first) | M | — | fable (design) → opus |

Sequencing: P1/P3/P4/P6/P7 are independent. P2 before P5 (both rewrite
turso-store list plumbing; strictness first so the sweep lands on the
final contract). **One turso live-conformance leg at milestone close**
covers P1+P2+P5's store changes together (playground discipline below);
pgx stores are touched only by P1 (wiring) — the pgx live leg re-runs at
close for parity per DP1 discipline.

### P1 — authorization order allow-lists move to the domain rims

The only feature violating the Q1 standard (authentication ×4, cms ×1,
jobs ×2 all declare `map[string]crud.OrderField` + default `crud.Order`
in `domain/<agg>/order.go`; authorization's live store-locally in both
stores + implicitly in memstore).

- **files:** features/authorization/domain/relationship/order.go (new),
  features/authorization/domain/role/order.go (new),
  features/authorization/{memstore/memstore.go,
  stores/turso/turso.go, stores/pgx/postgres.go} (consume the rim
  declarations; delete the store-local copies)
- **verify:** `cd features/authorization && go build ./... && go test
  ./... && go vet ./...` (memstore conformance hermetic-green), both
  store modules build/vet, `make check` + `make guard`.
- Additive public rim API (pre-tag, no obligation). Mirror
  `features/authentication/domain/apikey/order.go` naming exactly.
- **Gate fold (lead 7):** the memstore currently IGNORES `req.Order`
  (hardcoded created_at sort) while the SQL stores reject unknown order
  fields with `ErrInvalidInput` — a live backend divergence. P1 also
  makes the memstore VALIDATE `req.Order` against the rim allow-lists
  (unknown ⇒ `ErrInvalidInput`), and adds a storetest case asserting the
  unknown-order-field rejection identically across all three backends.
  The earlier "behavior change: NONE" claim is superseded — this is a
  deliberate parity fix.

### P2 — turso List identifier strictness (owner ruling: STRICT)

`turso.appendOrderBy`/`appendCursorPredicate` interpolate `q.PK` and the
resolved order column unvalidated (pgxdb routes both through
`QuoteIdentifier` with error returns). Strictness closes the asymmetry
that let the two roles stores diverge mechanically.

- **files:** integrations/datastores/turso/list.go (+ a small
  identifier.go mirroring pgxdb's, adapted to SQLite quoting),
  integrations/datastores/turso/list_test.go,
  features/authorization/stores/turso/roles.go (the consequence: the
  `subject_type || char(0) || …` raw-expression PK is rejected under
  strictness — rework to the pgx derived-column pattern: a `role_key`
  column via a wrapping subquery, `PKOf` echoing the DB-scanned value;
  keep `char(1)`-vs-`char(0)` freedom — cursors are backend-local),
  plus any other raw-expression PK the executor's sweep finds
  (**gate fold, steward 7: sweep BOTH axes —
  `grep -rn 'PK:\|OrderFields' features/*/stores/turso/`**; lead
  verified pre-execution: roles' PK is the only raw expression, every
  order column is plain created_at)
- **verify:** connector unit tests incl. NEW rejection cases
  (expression/whitespace/quote in PK or order column → loud error);
  affected store hermetic; `make check`/`make guard`; the milestone-close
  turso live leg re-proves `Roles/ListPagination` on the reworked
  tiebreak.
- **Gate fold (lead 3+4) — the rework's real mechanics:**
  (a) the reworked turso `rolesBaseSQL` MUST terminate in an outer
  `WHERE 1 = 1` sentinel — turso's `appendCursorPredicate` picks
  WHERE-vs-AND by substring, the inner subquery's WHERE trips it, and
  the appended `AND (…)` is a syntax error without the outer sentinel
  (the pgx precedent at `stores/pgx/roles.go:53` carries it for exactly
  this reason); (b) introduce a `roleRow` with `RoleKey string`, switch
  to `ListQuery[roleRow]` + `crud.MapPage(page, roleRow.toDomain)`, and
  delete the now-dead `roleAssignmentKey` Go recompute — `role.Assignment`
  has no field to carry the DB-computed key; (c) in the connector,
  `appendOrderBy`/`appendCursorPredicate` gain `error` returns (mirror
  pgxdb's `AddOrderByClause`/`ApplyCursorPagination`), rippling through
  `listCursor`/`listOffset`/`markPrev`; validate `pkCol`
  UNCONDITIONALLY in `appendOrderBy` (even when the pk term is omitted)
  so a bad PK fails on page 1, as pgxdb does.
- Divergence note for the log: this consciously supersedes the Z2b
  "turso's helper allowed one [raw expression]" state.

### P3 — health routes on the example hosts

`StatusCheck` exists in both connectors with ZERO consumers; no host has
any health route.

- **files:** examples/cms/cmd/server/… (`GET /healthz` — DB-backed:
  `turso.StatusCheck` result → 200/503), examples/minimal,
  examples/auth-cms, examples/jobs-minimal (memory-backed hosts: plain
  200 liveness — no DB to probe; one comment saying exactly that),
  the four host READMEs (one route-table line each)
- **verify:** per-host build/vet; run-and-look: boot each host, `curl
  /healthz` → 200 (and for examples/cms, stop the DB path → 503 if
  cheaply drivable, else assert the 200 path only and note it);
  `make check`.
- Host wiring only (rule 8). Route name `/healthz` (unclaimed by any
  feature namespace).

### P4 — turso query-observability parity

pgxdb has opt-in `LoggingQueryTracer` (slog; `Config.LogQueries` /
`Config.Tracer`, never-default) + `redact.go`; turso has nothing.
database/sql exposes no tracer hook — implement in the `DB` wrapper's
own `Exec/Query/QueryRow` (it already owns them).

- **files:** integrations/datastores/turso/{db.go,**tx.go**,tracers.go
  (new), redact.go (new)} + tests; **integrations/datastores/pgxdb/
  {postgres.go,README.md}** (the "no turso analogue" asymmetry record at
  postgres.go:44-50 + the README note become FALSE the moment this
  lands — both updated in the same phase)
- **verify:** connector unit tests (opt-in only — zero output at
  defaults; the tx path logs too); `make check`.
- **Gate fold (steward 1) — the parity posture, picked:** mirror pgxdb
  EXACTLY. turso's `redact.go` is a `RedactDSN` twin (DSN password
  masking on open/error paths — pgxdb's redact.go does NOT redact query
  args); query logging logs args VERBATIM behind the same dev-only
  opt-in-with-WARNING posture as pgxdb's `LoggingQueryTracer`. No
  arg-redaction feature is invented in either connector.
- **Gate fold (steward 6):** the tracer threads through
  `Tx.Exec/Query/QueryRow` too (turso's Tx bypasses the DB wrapper) —
  a query log that silently drops all transaction-path statements is a
  debugging trap.
- The ~25-line `RedactDSN` is DUPLICATED from pgxdb (separate modules;
  sdk is not its home — connector-log plumbing, not vocabulary).
  Conscious; promotion trigger: a third connector needing the identical
  helper.

### P5 — turso struct scanning + the hand-scan sweep (scope: Q1)

Ruling 5 answered: name-based scanning without reflection requires
codegen (a closed direction here); the only reflection-free alternative
is the status-quo hand-written scan callbacks — the drift surface this
phase exists to delete. The helper mirrors what pgx already does (pgx's
`RowToStructByName` IS reflection); ~80 lines confined to one connector
file, matching `db` tags against `rows.Columns()`, **strict-only** —
error on any unmatched column OR field; no Lax variant is offered, and
G10 (P6) guards the pgx one.

- **files:** integrations/datastores/turso/scan.go (new: `ScanStruct[T]`;
  `ListQuery[T].Scan` becomes optional — nil ⇒ struct-scan) + **scan-side
  `sql.Scanner` wrapper types** (`turso.Time`, `turso.NullTime`, a bool
  type over 0/1 INTEGER) + tests; then the FULL sweep (Q1) across
  features/{authentication,cms,events,jobs,authorization}/stores/turso/
- **Gate fold (lead 5) — NOT a mechanical rewrite; resized M → L.**
  turso stores TEXT timestamps parsed via `ParseTime` and 0/1 bools —
  every hand-scan does real conversion, and a strict name-matched
  `&field` bind to `time.Time`/`bool` would bypass the connector's own
  conversion discipline. So the helper only works with DECLARATIVE row
  structs whose field types carry the conversion (`CreatedAt turso.Time
  \x60db:"created_at"\x60` — the Scanner runs ParseTime inside
  `rows.Scan`). The sweep therefore AUTHORS ~20 db-tagged row-struct +
  `toDomain` pairs (the pgx-store discipline brought to turso), it does
  not merely delete callbacks. Row-struct rules: nullable columns MUST
  use `sql.Null*`/pointer/Scanner types (strict scan errors on NULL into
  a plain field — correct, author around it); `db:"-"` skips; an
  untagged exported field is a loud error. **(lead 6):** nil-Scan
  requires `T` be a db-tagged row struct — stores keep returning domain
  entities via `crud.MapPage(page, row.toDomain)`, never db tags on
  domain types.
- **verify:** connector unit tests (strict rejection cases incl. NULL
  into plain field, unmatched column, unmatched field, untagged
  exported); every swept module hermetic; `make check`/`make guard`; the
  milestone-close turso live leg (all five features' conformance suites
  are the net).

### P6 — sdk transaction seam scaffold + guards G9/G10 (shape: Q2)

Owner direction: cash the §8b deferral's *vocabulary* early, unconsumed,
and make `Underlying()` a hard no-go for feature code so nobody works
around the missing seam. Guard baseline verified clean 2026-07-09 (zero
`Underlying()` call sites outside the connectors).

- **files:** sdk/crud/tx.go (new — the Q2-ratified shape, doc-comment
  pinned as SCAFFOLDED-UNCONSUMED with the third-durable-emitter
  consumer trigger named), Makefile (G9 `guard-no-underlying`: no
  `.Underlying()` outside `integrations/datastores/*`; G10
  `guard-no-lax-scan`: no `RowToStructByNameLax` anywhere;
  both prove-can-fail, `make guard` → runs all TEN — and the guard-block
  header comment "runs all eight" updates to ten), README.md guard
  count, features/README.md one-line pointer
- **Gate fold (lead 1, MAJOR) — the method is `Transact`, NOT `InTx`.**
  Both connectors already carry `InTx(ctx, fn func(*Tx) error)` at 18
  live call sites; Go forbids a second same-named method with a
  different signature, so a `crud.Transactor` named `InTx` would be
  unimplementable by either DB without an 18-site breaking rename —
  hidden until the first consumer, because the scaffold ships
  unconsumed. Seam: `Transact(ctx context.Context, fn
  func(context.Context) error) error`. Migration path, stated:
  connectors keep `InTx(func(*Tx))` untouched; each adds `Transact`
  ADDITIVELY when the seam is first consumed. (The Q2 ratification named
  the shape, not the method name — logged as the closest correct thing.)
- **Gate fold (steward 3):** NO sdk-owned context stash of any kind —
  an sdk `WithTx(ctx, any)`/`TxFromContext(ctx) any` is a
  service-locator hole, the same workaround class `Underlying()` is
  being banned for. sdk owns the interface + the documented convention
  ONLY; typed ctx keys/helpers (`turso.TxFromContext`,
  `pgxdb.TxFromContext`) land per connector at first consumption.
- **Gate fold (steward 4):** the doc comment pins observable semantics,
  not just a name: commit on nil return, rollback on error (and on
  panic), **nesting explicitly UNPINNED** until the consumer trigger
  fires — so the two existing `InTx` implementations cannot silently
  diverge into the contract.
- **Gate fold (steward 5):** G9's comment carries the G6-style
  named-exception discipline: "a legitimate future host/`cmd` hit gets a
  named per-line exception HERE citing audit ruling 6 — never a regex
  weakening." (Baseline verified clean by both reviewers: zero
  `.Underlying()` sites, zero `Lax` sites.)
- **verify:** `make guard` green + both prove-can-fail records;
  `make check`; sdk builds stdlib-only (G1 unaffected — the seam is one
  interface, zero imports, no ctx helpers).
- fable designs the seam (Q2), opus lands the guards.

### P7 — connector retry policy (design-first; default: Q3)

Ruling 7 answered: **Config-level policy over context-plumbed.** A
context-carried retry policy is hidden control flow — invisible at the
wiring site, silently lost across goroutine hops, and contrary to the
repo's enumerated-Config discipline. `Config.Retry` on each connector's
`Config` is visible, per-DB, and testable; a per-call context escape
hatch stays DEFERRED until a real caller needs one (named trigger).

- **task-1 (fable):** the design note IN THIS FILE's execution log —
  transient-error classification per dialect (pgx: `pgconn` error
  classes + net errors; turso: driver/net + SQLITE_BUSY), which
  operations retry, bounded attempts + jittered backoff, and the Q3
  default. **Gate fold (steward 2 + lead 2, convergent MAJORs — the
  original "read paths only; never Exec" axis was UNSOUND):** the
  stores' non-idempotent writes flow through `Query`/`QueryRow` via
  `RETURNING` (job claim `UPDATE…RETURNING`, consume-once
  `DELETE…RETURNING`, `INSERT…RETURNING id` — verified at three named
  sites), and the wrapper cannot see the statement verb, so
  method-classified retry retries writes. Pinned instead: wrapper-level
  auto-retry applies to **CONNECTION ACQUISITION ONLY** (pool
  acquire/driver connect; note database/sql's own bounded bad-conn
  retry overlap); **NO statement is ever auto-retried** — statement
  retry (even pure SELECT) is per-call, store-owned, explicit opt-in,
  because method does not encode idempotency.
- **task-2 (opus):** implement `Config.Retry` on both wrappers per the
  ratified design; unit tests with injected transient failures; README
  rows in both connectors.
- **verify:** connector unit tests; `make check`; zero behavior change
  at defaults if Q3 = opt-in.

## Open questions — FOR RATIFICATION (jrazmi)

1. **Q1 — P5 sweep scope.** Recommend **helper + FULL sweep** (all five
   turso store modules): the drift surface is the point, storetest is
   the net, and a half-swept convention is worse than either endpoint.
   Alternative: helper + authorization-only proof, sweep opportunistic.
2. **Q2 — the sdk transaction seam shape.** Recommend the minimal
   tx-in-context form: `crud.Transactor` interface
   (`InTx(ctx, fn func(ctx) error) error`) + doc'd convention that
   implementations stash their dialect Tx in ctx for their own
   repositories to find — dialect types never cross sdk. Alternative:
   doc-only scaffold (a named reserved seam, no interface) if even an
   unconsumed interface feels premature. Either way `Underlying()` goes
   guard-banned (that half is unconditional).
3. **Q3 — retry default.** Recommend **pure opt-in** (zero-value
   `Config.Retry` = no retries anywhere — today's behavior, no silent
   change for existing hosts); the safe-defaults-on-reads alternative
   (3 attempts, jittered, transient-only) is one Config line away for
   hosts that want it.
4. **Q4 — review gate.** Recommend **RUN** architecture-steward +
   lead-backend-engineer on this DRAFT before ratification (house
   convention; P6 touches sdk API and P2/P5 touch connector contracts —
   the exact review classes those agents exist for). Owner may waive
   given the audit provenance.

## Live-store gates

One turso leg at milestone close (P1+P2+P5 accumulated): the ONLY
authorized database is
`libsql://gopernicus-cms-playground-gps-impact.aws-us-east-2.turso.io`
(URL asserted pre-run; single-executor caution). One pgx leg (docker
postgres:17) same close. All five features' conformance suites, loud
sub-runner names recorded in this file's execution log.

## Acceptance (milestone)

```sh
make check    # 34 modules; TEN guards after P6
make guard    # G9 + G10 prove-can-fail records in the log
```

Plus: rim order.go files exist and both authorization stores + memstore
consume them; turso List rejects non-identifier PK/order inputs (unit
cases); `/healthz` drivable on all four hosts; turso query logging
opt-in-silent at defaults; zero hand-scan callbacks remain in swept
stores (Q1 scope); sdk/crud tx seam doc-pinned unconsumed; both live
legs recorded.

## Real-interaction check

Standing check (a) per phase commit: `make check` green;
`examples/minimal` :8081 → 200s; kill; port free. P3 adds the four
`/healthz` drives. Milestone close: both live conformance legs + one
`examples/cms` boot (the only DB-backed host — its listing pages render
after P5's sweep).

## Execution log

(append dated entries here)

### 2026-07-09 — review-gate fold (Q4): both reviews returned, all findings folded

**architecture-steward: ALIGNED-WITH-EDITS (9). lead-backend-engineer:
SHIP-WITH-EDITS (7).** All majors + minors folded in place (each phase
carries its "Gate fold" bullets); the record:

- **Convergent MAJOR (steward 2 ≡ lead 2):** the P7 retry axis was
  unsound — `Query`/`QueryRow` carry `RETURNING` writes throughout the
  stores, so method-classified "read retry" retries writes. Re-pinned:
  connection-acquisition-only auto-retry; statement retry is per-call
  store opt-in, always.
- **Lead MAJOR 1:** `crud.Transactor`'s method renamed `InTx` →
  **`Transact`** — both connectors already carry an incompatible `InTx`
  at 18 sites; the collision would hide until first consumption. Q2's
  ratified SHAPE is unchanged; the name is the closest correct thing.
- **Lead MAJOR 5:** P5 resized M → L and reframed — the turso
  TEXT-timestamp/0-1-bool conversions mean the sweep authors ~20
  declarative row-struct + toDomain pairs over new connector Scanner
  types (`turso.Time`/`NullTime`/bool), not a callback deletion.
- **Steward MAJOR 1:** P4's "parity" pinned to pgxdb's REAL posture
  (RedactDSN twin + verbatim-args dev-only opt-in logging; no invented
  arg-redaction) and P4 now sweeps pgxdb's "no turso analogue"
  doc/README records that the phase falsifies.
- Minors/notes folded: P1 memstore order-validation parity + the
  cross-backend unknown-order storetest case (lead 7 — decided FIX, the
  DP1 principle); P2 WHERE-1=1 sentinel, roleRow+MapPage, error-return
  ripple, unconditional pkCol validation (lead 3+4), sweep widened to
  OrderFields (steward 7); P4 tx-path tracer threading (steward 6) +
  RedactDSN promotion trigger named (steward 8); P6 no-sdk-ctx-stash
  (steward 3), Transact semantics doc-pinned with nesting UNPINNED
  (steward 4), G9 named-exception discipline (steward 5); guard/count
  bookkeeping verified (steward 9 / lead cross-check: eight today → ten
  at P6). Both reviewers independently verified the G9/G10 baselines
  clean and P1's "authorization is the only divergence" claim.

### 2026-07-09 — P1 CLOSED (order allow-lists → domain rims)

`domain/relationship/order.go` + `domain/role/order.go` authored (apikey
mirror: exported `OrderFields` created_at-only + `DefaultOrder` DESC);
store-local copies deleted from both stores (consumption sites in each
store's relationships.go/roles.go updated — necessary compile scope
beyond the named files, logged); memstore's `pageMem` now validates
`req.Order` by rim-allow-list membership; NEW storetest cases
`Relationship/RejectsUnknownOrderField` + `Roles/RejectsUnknownOrderField`
green across all three backends on one `errors.Is(err,
errs.ErrInvalidInput)` assertion. **Premise-correction (logged):** the
gate fold's "memstore IGNORES req.Order" was partly false — pageMem
already rejected non-created_at fields with the same error kind, so this
is single-source-of-truth consolidation, not a behavior fix; observable
behavior unchanged. Verify: authorization module + both stores
build/test/vet green (incl. `-tags=integration` vet), `make check` +
`make guard` green, gofmt clean; standing check `examples/minimal` 200,
port freed. Committed CI-green. **Next: P2.**

### 2026-07-09 — P2 CLOSED (turso List identifier strictness + roles rework)

Connector: new `identifier.go` (`QuoteIdentifier`, pgxdb-mirror semantics
in SQLite quoting); `appendOrderBy`/`appendCursorPredicate` gain error
returns, both columns routed through validation, `pkCol` checked
UNCONDITIONALLY (a raw-expression PK now fails on page 1, no cursor
needed); ripple contained entirely in list.go. Roles store reworked to
the pgx derived-column pattern: `role_key` (char(1) separator) via the
wrapping subquery WITH the load-bearing `WHERE 1 = 1` sentinel,
`roleRow{RoleKey}` + `crud.MapPage`, `PKOf` echoes the DB-scanned key;
dead `roleTiebreak`/`roleAssignmentKey`/`scanAssignment` deleted.
Rejection matrix green at three layers (QuoteIdentifier / appendOrderBy /
appendCursorPredicate / List-integration — incl. the exact
`a || char(0) || b` shape). Sweep confirmed the gate's pre-verification:
roles was the ONLY raw-expression PK; other four turso stores took ZERO
source changes. Extra hermetic confidence: the reworked SQL shape
reproduced against in-memory SQLite through strict List (page 1, keyset
page 2, COUNT wrap) — the milestone-close live leg re-proves it on the
playground. `make check` + `make guard` green; standing check 200/port
freed. Committed CI-green. **Next: P3.**

### 2026-07-09 — P3 CLOSED (health routes) + StatusCheck round-trip amendment

`GET /healthz` on all four hosts (root router, no middleware, outside
every gated group — each host's Use stack verified auth-free):
examples/cms DB-backed via `turso.StatusCheck` (200 `{"status":"ok"}` /
503 `{"status":"unavailable"}`); the three memory-backed hosts plain-200
liveness with the no-DB-to-probe comment. READMEs updated (cms/auth-cms/
jobs-minimal). Run-and-look: all four hosts booted, /healthz 200 recorded,
ports freed; the cms happy path pinged the authorized playground live.

**Run-and-look catch → connector amendment (in-milestone-spirit, applied
by the coordinator):** the 503 path was UNREACHABLE — `StatusCheck` was
Ping-only and the remote libSQL driver's Ping is LAZY (nil without a
network round-trip), so a dead DB could never fail readiness. Both
connectors' `StatusCheck` now Ping + `SELECT 1` round-trip (the canonical
pattern; pgxdb kept symmetric deliberately); hermetic
`turso.TestStatusCheck` added (live memDB nil / closed DB error). The
503 path then DRIVEN LIVE: examples/cms booted against a nonexistent
turso URL → `/healthz` → **503 `{"status":"unavailable"}`**, clean
shutdown, port freed. Note carried to open flags: `tursodb.Open`'s eager
boot ping is equally lazy — boot-time DB validation still rests on the
feature stores' table probes, not the connector open.

**Premise-false (logged):** `examples/minimal` has NO README — the
route-table line landed in the other three only; whether minimal gets a
README at all is a jrazmi call (open flag, not invented here).

Verify: all four hosts + both connectors build/test/vet green;
`make check` + `make guard` green; gofmt clean. Committed CI-green.
**Next: P4.**

### 2026-07-09 — P4 CLOSED (turso query-observability parity)

turso gains `RedactDSN` (userinfo password + the `authToken` query param
masked narrowly — DSN redaction, not the arg redaction the gate fold
forbids; innocuous params survive), `Config.LogQueries`/`Config.Logger`
with the pgxdb dev-only/args-VERBATIM/WARNING posture exactly, threaded
through DB.Exec/Query/QueryRow AND Tx (inherited via Begin — the tx path
logs). Zero-at-defaults + opted-in DB/Tx-path + redaction unit tests
green. pgxdb's now-false "no turso analogue" records rewritten
(postgres.go Config doc + README) to the symmetric reality — only
`Config.Tracer` external composition remains pgx-only, because
database/sql exposes no injection seam; turso's tracer is deliberately
UNEXPORTED (exporting it would be dead surface) — the honest asymmetry,
documented in both READMEs. Note carried forward: like pgxdb, a raw DSN
inside a wrapped DRIVER error is not scrubbed (hosts use
`Config.Redacted()`); a shared concern for future tracing work, named
not fixed. Both connectors + `make check`/`make guard` green; standing
check 200/port freed. Committed CI-green. **Next: P5.**

### 2026-07-09 — P5 CLOSED (turso struct-scan + the full Q1 sweep)

Connector: `types.go` (`turso.Time`/`turso.NullTime`/`turso.Bool` —
scan-side Scanner wrappers byte-identical to the ParseTime/ParseNullTime
semantics), `scan.go` (`ScanStruct[T]`, STRICT-only: unmatched
column/field, untagged exported, NULL-into-plain all loud; `db:"-"` +
unexported skipped), `ListQuery.Scan` optional (nil ⇒ struct-scan).
Strict matrix + wrapper round-trips + nil-Scan List + the
composite-store-row shape all proven against in-memory SQLite at the
connector. **The full sweep:** ~23 row structs authored across the five
turso stores; ALL hand-scan callbacks/helpers deleted (per-store table in
the executor report); every ListQuery.Scan → nil-Scan + `crud.MapPage`;
single-row reads through a per-store `queryOne[T]` (pgx-mirror; routes
QueryRow-shaped reads through Query so Columns() feeds the strict scan —
same SQL/semantics, each RETURNING site matches ≤1 row, job Claim's
SQLITE_BUSY retry preserved). Scalar scans + pgx-mirrored inline sites
deliberately left (logged). **Findings:** (1) `turso.NullTime` name
collision with the write-side helper — write helpers renamed
`FormatNullTime`/`FormatNullTimePtr` (pairs with FormatTime; pre-tag, no
version obligation); (2) no store-level hermetic scan path exists
(structural — stores carry no sqlite driver), so the in-memory proof
lives at the connector and the live conformance suites at milestone
close are the store-level net, as the plan states. Verify: connector +
all five stores build/test/vet green (incl. -tags=integration vet),
`make check` + `make guard` green, gofmt clean — coordinator re-verified
all builds independently (editor diagnostics were stale mid-sweep
state). Committed CI-green. **Next: P6.**

### 2026-07-09 — P6 CLOSED (crud.Transactor scaffold + guards G9/G10)

Executor note: P6 ran INLINE by the coordinator (fable) including the
Makefile guard mechanics (specced opus) — same cost-directive deviation
as Z5's, logged. `sdk/crud/tx.go` landed: `Transactor` with
**`Transact(ctx, fn func(ctx) error) error`** (the gate-fold rename — no
InTx collision), doc pinning SCAFFOLDED-UNCONSUMED + the
third-durable-emitter trigger + commit-on-nil / rollback-on-error /
rollback-and-repanic + tx-in-context with PER-CONNECTOR typed helpers
(NO sdk ctx stash — named as the service-locator hole) + nesting
EXPLICITLY UNPINNED + the additive-Transact migration path for the 18
InTx sites. **G9 `guard-no-underlying`** (no `.Underlying()` outside
integrations; named-exception discipline in the comment) and **G10
`guard-no-lax-scan`** (no RowToStructByNameLax anywhere) wired into the
aggregate — `make guard` runs ALL TEN; both proven-can-fail (a
`pool.Underlying()` probe in examples/minimal and a Lax string in the
pgx store each failed loudly, reverted, green). README guard counts
eight → ten (both spots); features/README checklist item 5 gains the
G9/Transactor pointer. sdk builds stdlib-only (G1 structural);
`make check` green. Committed CI-green. **Next: P7.**
