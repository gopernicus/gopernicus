# P3 — `features/cms/stores/postgres` (the EAV spine, ported)

Status: RATIFIED (cut from design §7 / DP6, ratified R7: build now)
Executor model: opus
Depends on: P1 + P2.
Design doc: `.claude/plans/roadmap/datastore-portability.md` §7 (port rules),
§5 (dialect-delta table — read the whole table; the precision trap row is
this phase's sharpest hazard), §6 (migration invariant), §4.3 (gating).
Read `features/cms/stores/turso` file-for-file first — it is the template;
this module is its postgres sibling, symmetric in exported surface.

## Goal

cms's second dialect store: the frozen EAV spine and the four typed domains
implemented over the P1 connector, with migrations mirroring turso's exact
version set, passing the P2 suite against live Postgres.

## Work items

1. Module scaffold: `gopernicus/features/cms/stores/postgres`; requires
   `gopernicus/{sdk, features/cms, integrations/datastores/postgres}`;
   go.work + Makefile MODULES.
2. Migrations: **filenames mirror turso's tree exactly — 0009–0021 with the
   gaps at 0011/0012 reproduced** (design §6's version-set invariant: same
   filename = same logical schema step; do NOT renumber from 0001). Content
   in postgres idiom: `TIMESTAMPTZ`, native `BOOLEAN`, `JSONB` ONLY where
   turso used TEXT-JSON for the same column... subject to the spine rule:
   **representation may change; structure may not.** `entry_fields.value`
   as typed value columns, or ANY reshaping of `entries`/`entry_fields`, is
   a frozen-spine redesign and forbidden (§7). Port the schema, don't
   "improve" it. Same UNIQUE/FK/index structure as turso's tree.
3. Five stores (entries incl. spine tx-writes via the connector's `InTx` —
   entry + EAV fields atomically, field replace-on-update, term joins;
   terms; menus; media; inquiries), using `DB`/`MapError` exactly as the
   turso stores use turso's. Identical port semantics: same sentinel for
   the same violation, same ordering, same page shape. **Cursors encode
   from STORED values, never in-memory ones** — the µs-truncation keyset
   trap (§5) is why the P2 precision case exists; if it fails here, the
   store is wrong, not the test.
4. Exported trio mirroring the turso module:
   `Repositories(db) cms.Repositories`, `ExportMigrations(dst string) error`,
   `Register(m feature.Mount, db) (…, error)` + `MigrationsFS`/`MigrationsDir`
   — a host switches dialect by one import + one `Open` call (§2 item 3).
5. Tests: the P2 suite (`storetest.Run`) env-gated on `POSTGRES_TEST_DSN`
   per §4.3 (loud skip message verbatim; `newRepos` truncates cms tables
   via `t.Cleanup`); unit tests for mapping where the turso stores have
   them.
6. `make test-stores` target added to the Makefile: runs the store modules'
   tests EXPECTING the env vars, failing loudly if absent (`make check`
   stays hermetic). README: docker one-liner + gating rule.
7. NO new example host (`examples/cms` stays turso) — the live conformance
   run below is the proof (a postgres example host is a design non-goal).

## Acceptance (design §10 P3 row)

```sh
cd features/cms/stores/postgres && go build ./... && go vet ./... && go test ./...
diff <(ls features/cms/stores/turso/migrations) \
     <(ls features/cms/stores/postgres/migrations)     # empty — identical version set
make check                                              # green; hermetic skip is loud
make test-stores                                        # passing with DSNs set
```

- **The suite passes against local dockerized Postgres, recorded as a dated
  NOTES.md artifact** (suite, dialect, DSN class, result — §4.3's
  anti-false-green rule). A hermetic skip does NOT satisfy this phase; if
  no Postgres is reachable (check env + `docker ps` — do not ask), STOP
  the leg and ask jrazmi for the environment rather than closing.

## Real-interaction check

Standing check (a) from `00-overview.md`, PLUS this phase's own gate: the
live-Postgres `storetest` run above.

## Execution log

### 2026-07-02 — P3 executed (loop leg 4; implementer on opus) — LIVE-VERIFIED

Shipped `features/cms/stores/postgres` (8th module — count corrected by
P4's fresh-eyes pass): migrations with EXACT
turso filenames (0009–0021, 0011/0012 absent — `diff` of the two migration
dirs is empty), postgres idiom (TIMESTAMPTZ; `BIGINT` for asset size; spine
structure unchanged — no JSONB reshaping, no boolean columns exist in the
cms schema so that plan line had no applicable column); five stores
(entries: spine+EAV atomic via `InTx`, fields replace-on-update, term
joins, `(created_at, id)` DESC keyset with cursors encoded from the
STORED read-back TIMESTAMPTZ — the precision-trap rule); exported trio +
MigrationsFS/Dir mirroring turso's; env-gated conformance test (loud skip
verbatim; migrations applied via the P1 Registrar; TRUNCATE...CASCADE
cleanup); README with the docker one-liner. Makefile gained
`STORE_MODULES` + `test-stores` (fails loudly without DSN); go.work +
MODULES updated.

Divergences (logged, sound): postgres conformance gated by env var only,
no build tag (design §4.3's postgres rule; turso keeps its
`-tags=integration` pattern); assets.size INTEGER→BIGINT (representation,
not structure).

Acceptance (re-run FIRSTHAND by the loop leg, -count=1): module
build/vet/test PASS (hermetic skip loud); migration filename diff EMPTY;
`make check` → "all checks passed" (8 modules, 4 guards). **LIVE
conformance GREEN firsthand**: dockerized postgres:17 on :55432, full
storetest suite incl. Entries/TimestampPrecision and Entries/
CursorPagination passed (`ok ... 0.964s`); container removed. The
implementer's own live run also passed every subtest verbose (none
skipped). `make test-stores` with DSN: postgres green; turso leg
loud-skips (TURSO_* absent).

Real-interaction: `GET http://localhost:8081/` → 200,
`GET /products/widget-3000` → 200; killed; port 8081 free.

Unverified: turso conformance still pending creds (P2 flag stands) — the
ONLY open item for milestone close besides P4 docs.
