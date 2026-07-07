# P4 — docs + policy sync

Status: RATIFIED (cut from design §10 P4 + §2's charter amendments + R6)
Executor model: fable (doc judgment)
Depends on: P1–P3.
Design doc: `.claude/plans/roadmap/datastore-portability.md` §2 (charter
amendment text), §1 (the boundary principle), §8/§8b (applied amendments +
Transactor addendum). Also `.claude/plans/roadmap/00-intersections.md` §1–§2
(the taxonomy + degraded-mode matrix R6 lands in the durable docs).

## Goal

The written record catches up with the shipped policy in the same milestone
— the charter states the dialect-set rule, ARCHITECTURE.md states the
taxonomy, and no doc makes an aspirational claim the tree doesn't back.

## Work items

1. `features/README.md` (charter) amendments per design §2:
   - §3 rules gain the DP1 policy: supported dialect set **{turso,
     postgres}** as a named, amendable list; parity gates feature-v1
     milestone close (not per-phase); uniform store-adapter surface (the
     `Repositories`/`ExportMigrations`/`Register` trio); every port set
     ships a `storetest` suite run by all implementations.
   - §8 checklist gains items 10 ("a `storetest` conformance package exists
     and the reference in-memory implementation passes it in the feature's
     own `go test ./...`") and 11 ("every `stores/<dialect>` in the
     supported set exists and passes `storetest`, live run recorded per
     §4.3") — design §2's wording.
   - **Item 12 (R6):** every optional `Repositories`/`Config`/`Mount` port
     documents its nil semantics (the degraded-mode row — safe degradation
     defaults silently, unsafe degradation errors loudly at construction;
     cite the auth Hasher/Mailer precedent).
   - Record the R3 memory-store placement rule where §2's anatomy table
     describes stores: test-scoped reference in `storetest` by default; an
     in-core public memstore package only when substantial and host-needed
     (jobs is the named case); never a `stores/memory` module.
2. `ARCHITECTURE.md` (R6 + design §1): a compact "kinds of module" taxonomy
   table (sdk facility / integration / feature / store module, with the two
   litmus tests: changes-what-the-host-migrates → store module per dialect;
   needs-own-migrations-or-routes → feature) and the dialect-set sentence
   where the module layout is described. Keep its one-line-per-module
   style; add the two new modules to the layout block.
3. `RELEASING.md`: add `integrations/datastores/postgres` and
   `features/cms/stores/postgres` to the tagging list (store-module
   `replace` directives stay workspace-dev-only per C4).
4. Makefile: verify `MODULES` carries both new modules (P1/P3 added them —
   confirm, don't duplicate); `test-stores` target has a comment naming the
   env vars and the docker one-liner.
5. NOTES.md: dated milestone entry — what shipped, the live-run artifacts
   (cms×postgres from P3; cms×turso status from P2), and a pointer to the
   design doc's §8b Transactor addendum as the standing follow-up trigger.
6. Verify (do NOT re-apply) the auth-v1 amendments: confirm
   `.claude/plans/auth-v1/00-overview.md` carries amended A2 + phase 7 and
   that `07-auth-store-postgres.md` exists — they were applied at
   ratification (design §8's status line). Log the confirmation.
7. Fresh-eyes cross-check (restructure phase-1 W8 protocol): a clean reader
   takes only the updated docs and verifies every checkable claim against
   the tree. Zero contradictions.

## Acceptance (design §10 P4 row)

```sh
make check    # green
```

- Docs match the shipped tree — no aspirational claims (the fresh-eyes pass
  reports zero contradictions).
- Charter carries items 10–12 + the dialect-set rule; ARCHITECTURE.md
  carries the taxonomy table.

## Real-interaction check

Standing check (a) from `00-overview.md` — the milestone's closing
verification.

## Execution log

### 2026-07-02 — P4 executed (loop leg 5; fable, direct)

Docs synced: `features/README.md` — §3 gained the multi-datastore-out-of-
the-box rule (dialect set, milestone-close parity gate, uniform trio,
version-set invariant, live-gating summary); §2 anatomy table gained the
`storetest/` row + the R3 memory-store placement paragraph; §8 gained
checklist items 10–12 (suite exists / dialects pass with recorded live runs
/ nil-semantics documentation — the no-feature-variants rule).
`ARCHITECTURE.md` — layout block + "Eight modules today" + the R6
kinds-of-module taxonomy table with both litmus tests + dialect-set
sentence in the Features section. `RELEASING.md` — eight-module list.
Makefile — two stale "6/six modules" comments corrected (fresh-eyes
finding). Module-count errors in P2/P3 execution logs corrected (7 and 8th,
not 8 and 9th); auth-v1 acceptance de-hardcoded to the MODULES list.
auth-v1 amendments confirmed applied (07 file exists, A2 amended) — not
re-applied.

**Turso conformance resolved without touching jrazmi's data:** the root
`.env` carries real TURSO_* creds, but the suite TRUNCATES cms tables —
running it against that db would wipe the dev site. Deliberately NOT done.
Instead: scratch harness (zero repo changes) ran the full `storetest` suite
against the REAL `features/cms/stores/turso` store over `file:` URLs
through the libsql driver (modernc sqlite registered; fresh db per
newRepos call). **FULL PASS** — all Entries/Terms/Menus/Media/Inquiries
subtests incl. TimestampPrecision; Terms/CRUDRoundTrip's Delete exercises
the P2 `entry_terms` fix against a real libsql database (the old
`post_terms` SQL would have errored "no such table"). DSN class recorded
honestly as **local file (libsql embedded)** — a real-remote-Turso run
remains YOUR CALL (needs a disposable database, not the .env one).

Fresh-eyes cross-check (clean-context subagent over the four updated docs
vs the tree): zero contradictions in the docs; two stale Makefile comments
found and fixed (above).

Acceptance: `make check` → "all checks passed" (8 modules, 4 guards).
Real-interaction: `GET http://localhost:8081/` → 200,
`GET /products/widget-3000` → 200; killed; port 8081 free.

Unverified: real-remote Turso conformance (deliberate — destructive against
the only known creds; flagged to jrazmi). Everything else green.

**Addendum, same day:** jrazmi authorized the playground database
(`libsql://gopernicus-cms-playground-gps-impact.aws-us-east-2.turso.io` —
that URL specifically, not ".env creds" in general). Real-remote run
executed after verifying the .env URL matched: `TestConformance_Turso`
**PASS (76.12s)** against real Turso. The flag is RESOLVED; milestone
closed with no caveats. Nothing remains unverified.
