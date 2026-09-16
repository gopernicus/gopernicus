# Unified authorization release — 2026-09-16

Status: PUBLISHED AND PUBLICLY VERIFIED. The owner explicitly authorized pushing main, then release tags.

## Versions and scope

| Module | Previous | Release |
| --- | --- | --- |
| integrations/datastores/pgxdb | v0.8.1 | v0.9.0 |
| pockets/authorization | v0.17.0 | v0.18.0 |
| pockets/authorization/stores/pgx | v0.12.0 | v0.13.0 |
| pockets/authorization/stores/turso | v0.11.0 | v0.12.0 |
| pockets/authorization/stores/goredis | v0.3.0 | v0.4.0 |

One commit, five annotated tags, dependency-first. Breaking pre-v1 changes unify
facts, exact roles and audit; provide fresh SQL schemas, scoped protocol-2 cache,
coherent snapshots and one composable HTTP Require API. The authorization
Firestore adapter is removed; historical tags stay. Examples are pinned but untagged.

## Preconditions and workflow

- Starting main matches origin at 1e1308733cd8c23995497134052b2ef16c1f5b30.
- Preserve owner edits in plans/cacher-design.md,
  plans/gps-360-go-audit-upgrade-handoff.md and
  plans/segovia-v2-audit-upgrade-handoff.md. Exclude Python cache files.
- Preserve every old tag. Broad tag fetch reported historical local/remote
  conflicts; main-only fetch succeeded. Never force or replace old refs.
- Prior implementation verification: 42-module make check, owned PostgreSQL,
  SQLite and Redis race/conformance suites, docs build/typecheck, 12 runner tests,
  and 109 benchmark workloads with five samples each passed. Durable evidence:
  plans/authorization-one-middleware-verification.json and benchmark JSON.
- Prepare pins and dependency-ordered exact candidate archives; independently
  tidy/build/test/vet with GOWORK=off and no replacements. Candidate-only proxy
  supplies unpublished modules. Freeze module source inventories.
- Verify versioned SQLite/Redis integration and an external composition consumer;
  rerun the full workspace gate with final pins. Named platform/SRE review.
- Commit and push main normally. Confirm remote main before publishing all five
  annotated tags from that same commit, dependency-first; no force operations.
- Verify remote tags, public archive content, normal Go checksums and independent
  build/test/vet. Inspect release CI. Record completion in this plan, manifest,
  RELEASING.md and AUDIT.md; push the documentation receipt separately.

## Tasks

- [x] Prepare pins, release notes and exact candidates.
- [x] Verify candidates, consumer, integration and workspace.
- [x] Push main then publish five tags.
- [x] Verify public artifacts and record completion.

## Limits

Fresh schema only; no legacy migration promise. Host adoption/deployment is
separate. Remote Turso was not exercised. Prior GitHub Linux CI fails unrelated
SDK TestDisk_Conformance/RangeEdges (MaxInt64 seek EINVAL); inspect current result.
Evidence workspace: /tmp/gopernicus-authorization-unified-release.

## Execution record

- All five exact candidate archives passed independent tidy, source/checksum
  inventory, dependency graph, build/test/vet and integration/live-tag compile/vet.
- The external HTTP All/Any example produced 401/403/204/204 as expected;
  auth-CMS passed independent tidy, module verification, build/test/vet.
- Actual SQLite-to-Redis delivery/recovery, model-free roles and final-revocation
  ordering tests passed with race detection from versioned archives. An initial
  escaped test selector selected no tests; it was corrected, rerun and each
  expected subtest's pass is asserted in the verifier.
- Named platform/SRE release review: ship-ready, no concrete blockers.
- Benchmarks and owned PostgreSQL/non-C/named-schema proof are retained from the
  implementation verification; version-only edits do not require rerunning them.

- Final 42-module workspace make check passed, including generated-artifact drift,
  build/test/vet, tagged compilation and architecture guards.

## Publication and public verification

- Pushed main normally at `66d70c511a74b28b1019e526acfc1382044395ad`, then published all five annotated tags
  dependency-first at exactly that commit. All prior remote tags and owner files
  are preserved. Receipt and checksums are in
  [the release manifest](authorization-unified-release-manifest.json).
- All five public archives match the candidate inventories and Go checksums.
  Git origin/tag commit checks, independent build/test/vet and tagged compilation
  passed with GOWORK=off, no replacements and no checksum exemptions.
- The external HTTP example and versioned auth-CMS passed again against public
  dependencies. Actual SQLite/Redis delivery, model-free role and revocation-order
  race tests passed from public archives.
- An initial public build exhausted temporary disk space. Disposable caches from
  failed indexing attempts were removed; logs were retained and public verification
  was rerun from a fresh cache.
- Public checksum indexing initially returned unknown-revision errors; verification
  retried only propagation/download failures; resolving the exact published commit
  through the public proxy refreshed tag indexing. Verification then completed with
  normal sum.golang.org.
- GitHub main CI [35068756769](https://github.com/gopernicus/gopernicus/actions/runs/35068756769)
  failed the existing SDK TestDisk_Conformance/RangeEdges (Linux MaxInt64 seek
  returns EINVAL). SDK source is unchanged. All five tag checks failed the same confirmed SDK test;
  the automatic documentation deployment passed. Local full make check passed.

Release complete. Host adoption and remote Turso verification remain separate.
