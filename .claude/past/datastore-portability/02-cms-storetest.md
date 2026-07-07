# P2 — `features/cms/storetest` (the conformance pattern, proven on cms)

Status: RATIFIED (cut from design §4 / DP2, as amended by R3)
Executor model: opus
Depends on: nothing (parallel with P1).
Design doc: `.claude/plans/roadmap/datastore-portability.md` §4 (suite shape,
runners, gating), §5 (the precision trap the mandatory case exists for).
Read `sdk/cacher/cachertest` (the `Run(t, newImpl)` pattern this scales up),
the five cms port doc comments (`features/cms/{content,taxonomy,menus,media,
messaging}`), `examples/minimal/internal/memstore` (+ its tests), and
`features/cms/stores/turso`'s existing integration test
(`entries_integration_test.go`) before writing anything.

## Goal

The per-feature conformance-suite pattern, instantiated on cms: one public
test-support package `features/cms/storetest` exporting
`Run(t *testing.T, newRepos func(t *testing.T) cms.Repositories)`, plus the
reference in-memory implementation inside the package, run by three runners
(reference impl, examples/minimal memstore, stores/turso live-gated).

## Work items

1. `features/cms/storetest`: `Run` fans out per-port subtests (`Entries`,
   `Terms`, `Menus`, `Media`, `Inquiries`) over a FULLY WIRED
   `cms.Repositories` (cross-table behavior — entry↔term association,
   cascade on entry delete — is testable; ports are not tested in
   isolation). `newRepos` contract: a CLEAN, isolated `Repositories` per
   call (SQL harnesses truncate via `t.Cleanup`; memory harnesses return a
   fresh instance) — state it in the doc comment.
2. Contract cases per port, derived from the port doc comments (the port
   comment is the spec; the suite is its executable form): CRUD
   round-trips; absent → `errs.ErrNotFound`; uniqueness →
   `errs.ErrAlreadyExists` (entries `(type, slug)`, terms `(kind, slug)`,
   menu slugs); referential behavior where promised
   (`errs.ErrInvalidReference` / observed cascade); cursor pagination —
   full traversal across ≥2 page boundaries, no skipped/duplicated rows,
   stable `(created_at, id)` ordering, stale/empty cursor per
   `sdk/repository.DecodeCursor`.
3. **The mandatory timestamp-precision case (§4.1/§5 — the whole parity
   net hinges on this):** rows created with sub-microsecond `created_at`
   spacing; assert pagination remains correct when the store truncates to
   its native precision (memory ns / turso ns-TEXT / postgres µs). Assert
   ordering + identity — NEVER that nanosecond fidelity survives a round
   trip.
4. The reference in-memory implementation, inside `storetest` itself (DP2:
   this is what lets the feature module self-verify under G2; stdlib-only;
   test-scoped, deliberately NOT a host-facing dev store — R3's in-core
   memstore option is for jobs, not cms). It must hand-enforce the
   uniqueness/FK semantics SQL gives for free (the phase-2-W7 memstore
   honesty lesson). cms's own `go test ./...` runs `Run` against it.
5. `examples/minimal/internal/memstore` tests add one `storetest.Run` call
   (the host store keeps its pedagogical role, gains a drift net). Fix or
   flag any divergences the suite exposes — do not weaken the suite to
   pass them.
6. `features/cms/stores/turso` runs the suite under the EXISTING
   `-tags=integration` + `TURSO_*` skip gating, REPLACING bespoke per-store
   flows where they overlap (implementer's call, flagged in the design's
   open questions: keep genuinely turso-specific extras — e.g. libsql
   error-string `MapError` coverage — as bespoke additions).

## Acceptance (design §10 P2 row)

```sh
cd features/cms && go build ./... && go vet ./... && go test ./...   # executes the suite vs the reference impl
cd examples/minimal && go test ./...                                  # memstore passes the suite (or divergences fixed/flagged)
make check                                                            # green; guard G2 still green (suite imports no drivers)
```

## Real-interaction check

Standing check (a) from `00-overview.md`. If `TURSO_*` creds are present
(check — do not ask), run the turso leg (`-tags=integration`) and record;
otherwise state plainly it is unverified pending the milestone-close live
run.

## Execution log

### 2026-07-02 — P2 executed (loop leg 3; implementer on opus)

Shipped `features/cms/storetest`: `storetest.go` (public `Run(t, newRepos)`
fanning out per-port subtests over a fully wired `cms.Repositories`;
contract cases per port doc comments — CRUD/ErrNotFound/ErrAlreadyExists
uniqueness/term-association cascade/cursor pagination incl. stale+empty
cursors; the MANDATORY precision case: 6 rows 1ns apart off a µs-aligned
base, asserting identity + self-consistent DESC order, never ns fidelity —
authored to catch a store encoding cursors from in-memory ns values) and
`reference_test.go` (stdlib-only reference in-memory Repositories +
`TestReference` so cms self-verifies under G2; go.mod gained zero requires).
Runners wired: `examples/minimal` memstore `TestConformance`; turso store's
integration leg now runs the suite under the existing `-tags=integration` +
`TURSO_*` loud-skip gating (bespoke entry-lifecycle flow subsumed).

**Two real divergences exposed and fixed — the suite doing its job:**
1. turso `TermStore.Delete` deleted from `post_terms`, a table no migration
   creates (stale posts→entries rename) — would fail live on any fresh db.
   Fixed to `entry_terms` (migration 0021). Live-unverified (no TURSO_*
   creds); flagged for the milestone-close turso run.
2. memstore `entryPageOf` ignored cursors entirely and lacked the id
   tie-break — violated `EntryRepository.List`'s contract. Rewrote with the
   shared `sdk/repository` codec + `(created_at, id)` DESC.
Minor flag (not changed, minimal diff): memstore media/inquiry List sorts
lack turso's id tie-break; suite doesn't expose it (distinct timestamps).

Acceptance (re-run firsthand by the loop leg): `features/cms` build/vet/
test PASS (suite green vs reference impl); `examples/minimal` tests PASS;
`make check` → "all checks passed" (7 modules at the time — count corrected
by P4's fresh-eyes pass; guards green, G2 green — no drivers in the suite). Real-interaction: `GET http://localhost:8081/`
→ 200, `GET /products/widget-3000` → 200, killed, port free.

Unverified: turso conformance leg compiled + loud-skipped (`TURSO_*` and
`POSTGRES_TEST_DSN` both absent) — pending the milestone-close live run,
which now ALSO verifies the `entry_terms` fix against real libSQL.
