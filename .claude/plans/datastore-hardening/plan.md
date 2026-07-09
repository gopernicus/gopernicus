# datastore-hardening — connector parity, strictness, and the scaffolded seams

Status: **DRAFT 2026-07-09 — awaiting jrazmi ratification (Q1–Q4 below)**
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
| P5 | turso struct-scan helper + hand-scan sweep | M | P2 | opus |
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
  store modules build/vet, `make check` + `make guard`. Behavior change:
  NONE (created_at-only, default DESC — the declarations move, values
  identical; storetest is the proof).
- Additive public rim API (pre-tag, no obligation). Mirror
  `features/authentication/domain/apikey/order.go` naming exactly.

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
  (`grep -rn 'PK:' features/*/stores/turso/` — audit expectation: roles
  is the only one)
- **verify:** connector unit tests incl. NEW rejection cases
  (expression/whitespace/quote in PK or order column → loud error);
  affected store hermetic; `make check`/`make guard`; the milestone-close
  turso live leg re-proves `Roles/ListPagination` on the reworked
  tiebreak.
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

- **files:** integrations/datastores/turso/{db.go,tracers.go (new),
  redact.go (new)} + tests
- **verify:** connector unit tests (opt-in only — zero output at
  defaults; redaction of string args in logs); `make check`.
- The ~30-line redaction helper is DUPLICATED from pgxdb (separate
  modules; sdk is not its home — it's connector-log plumbing, not
  vocabulary). Log the duplication as conscious.

### P5 — turso struct scanning + the hand-scan sweep (scope: Q1)

Ruling 5 answered: name-based scanning without reflection requires
codegen (a closed direction here); the only reflection-free alternative
is the status-quo hand-written scan callbacks — the drift surface this
phase exists to delete. The helper mirrors what pgx already does (pgx's
`RowToStructByName` IS reflection); ~80 lines confined to one connector
file, matching `db` tags against `rows.Columns()`, **strict-only** —
error on any unmatched column OR field; no Lax variant is offered, and
G10 (P6) guards the pgx one.

- **files:** integrations/datastores/turso/scan.go (new: `ScanStruct[T]`
  or equivalent; `ListQuery[T].Scan` becomes optional — nil ⇒
  struct-scan) + tests; then the sweep per Q1's answer across
  features/{authentication,cms,events,jobs,authorization}/stores/turso/
  hand-scan callbacks
- **verify:** connector unit tests (strict rejection cases); every swept
  module hermetic; `make check`/`make guard`; the milestone-close turso
  live leg (all five features' conformance suites are the net — this is
  a mechanical rewrite with storetest as proof).

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
  both prove-can-fail, `make guard` → runs all TEN), README.md guard
  count, features/README.md one-line pointer
- **verify:** `make guard` green + both prove-can-fail records;
  `make check`; sdk builds stdlib-only (G1 unaffected — the seam is an
  interface + context helpers, zero imports).
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
  operations retry (**connection acquisition + read paths only; never
  `Exec`** — blanket write-retry on non-idempotent statements is a
  correctness hazard; stores wanting write-retry own it explicitly),
  bounded attempts + jittered backoff, and the Q3 default.
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
