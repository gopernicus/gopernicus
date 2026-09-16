# Authorization consistency and TupleCache hardening release — 2026-09-15

Status: IN PROGRESS. The owner requested release of the completed hardening and
test/benchmark suite. Publication is authorized; host adoption/deployment is separate.

## Scope and versions

| Module | Previous | Release |
| --- | --- | --- |
| `pockets/authorization` | v0.16.0 | v0.17.0 |
| `pockets/authorization/stores/turso` | v0.10.0 | v0.11.0 |
| `pockets/authorization/stores/pgx` | v0.11.0 | v0.12.0 |
| `pockets/authorization/stores/goredis` | v0.2.0 | v0.3.0 |

Core adds explicit rebuild, diagnostics and a capacity error, and applies durable
snapshots to ordinary relationship decisions. SQL stores adopt ID-only full
snapshots and pin the new core. Redis adds physical limits and requires
`ContextTimeoutEnabled=true`; its integration tests pin the new SQL adapters.
These host-contract and coordinated dependency changes warrant minor releases
under RELEASING.md. No connectors, Firestore adapter or schema migrations change.

## Preconditions and workflow

- Starting main `debfddfd1e5d1d4c8812fcb85efc840210359872`; verify remote main and
  absence of selected tags before publication. Preserve all previous tags.
- Preserve owner edits in `plans/cacher-design.md`,
  `plans/gps-360-go-audit-upgrade-handoff.md`, and untracked
  `plans/segovia-v2-audit-upgrade-handoff.md`; record hashes before release edits.
- Implementation and independent review passed. Full 43-module make check,
  core/SQL/Redis race tests, and actual PostgreSQL/SQLite → Redis tests passed;
  exact commands and limits are in the implementation plan.
- Selectively stage release-owned source/docs, pin dependencies, and reuse the
  previous release verifier to build exact dependency-ordered candidate archives.
  Verify independent tidy/build/test/vet and integration-tag compilation with
  GOWORK=off and no replacements. Candidate-only proxy supplies unpublished
  versions; public dependencies retain checksum verification.
- Exercise the retained real SQLite/Redis suite against versioned module copies;
  verify a standalone consumer's new API and deadline behavior. Rerun the
  workspace gate with release pins and check candidate source inventories.
- Commit/push source normally to main, then publish immutable annotated tags
  dependency-first. No force push or old-tag modification.
- Verify remote annotated tags and peeled commits, public module archives and
  normal Go checksums, independent checks and the standalone consumer again.
- Record results in this plan, manifest, RELEASING.md and AUDIT-039. Inspect the
  actual CI result; the preceding releases disclosed an unchanged Linux SDK
  RangeEdges failure, which this authorization change does not fix.

## Tasks

- [x] Prepare release notes, module pins and exact candidates.
- [x] Verify independent modules, standalone consumer and final workspace.
- [ ] Commit/push release source and publish four annotated tags.
- [ ] Verify public artifacts/consumer and record completion.

## Compatibility and verification boundaries

Upgrade core and the chosen SQL adapter together; cached hosts also upgrade
Redis and enable `redis.Options.ContextTimeoutEnabled`. Default cache limits
are 1 MiB per raw read and 4 MiB per delta budget; excess reads fall back to SQL,
while excess delta delivery can be recovered with `Rebuild(ctx)` if current
facts fit. No schema migration, namespace change or manual cache conversion is
required. Custom source consumers must accept ID-only Changes on Full snapshots.
Relation exclusivity and existing staleness/model/one-mirror constraints remain.

Remote Turso, Firestore emulator/live, PostgreSQL non-C locale and representative
production load remain unverified. The benchmark report contains local fixture
measurements only. No host files, services or caches will be changed.

## Changed files and evidence

Implementation inventory: [hardening plan](authorization-review-hardening.md).
Release additions: this plan/manifest, adapter dependency pins/sums, RELEASING.md
and AUDIT.md. Evidence root: `/tmp/gopernicus-authorization-hardening/release`.

## Execution record

- Remote main matched the starting commit; all four selected tag names were
  absent. Prior refs and owner-file hashes are saved under `remote-before.txt`
  and `owner-sha.json` in the evidence directory.
- Dependency-ordered candidate archives passed independent tidy, exact source
  and checksum inventory, module graph, build/test/vet and tagged compilation/vet
  with GOWORK=off and no replacements. Report: `candidate/results.json`.
- The retained actual SQLite → Redis integration test passed with race detection
  from the exact versioned archive copies. Report: `candidate/e2e-results.json`.
  PostgreSQL's full race and combined integration tests passed during the
  implementation phase; this archive leg intentionally reran local SQLite/Redis.
- Named platform/SRE release review found no blockers. The bundled
  `integrations/kvstores/goredis.Open` already enables context deadlines; the
  required new setting affects directly constructed borrowed Redis clients.
- Final release workspace `make check` passed all 43 modules' build/test/vet,
  integration/live-tag compilation/vet and architecture guards. No templ/UI
  artifact drift or go.work/go.work.sum changes. Log: `workspace-check.log`.
  External datastore settings were excluded from this gate.
- Standalone exact-version consumer passed with GOWORK=off, no replacements,
  GONOSUMDB empty and normal public checksum verification. It verified all four
  expected module hashes/versions, go mod verify/build/vet and actual SQLite/Redis
  deadline rejection, cold/warm decisions, revocation, capacity fallback,
  oversized-delta preservation and Rebuild acknowledgement/diagnostics.
  Report: `consumer/candidate-results.json`. Disposable fixtures were removed.
