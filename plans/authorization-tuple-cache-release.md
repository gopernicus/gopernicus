# Authorization TupleCache release — 2026-09-15

Status: PUBLISHED FROM MAIN AND PUBLICLY VERIFIED. The owner explicitly requested release, followed by a prompt
for Segovia adoption and benchmarking. This authorizes module publication; it
does not authorize host migrations, production mutations or deployment here.

## Scope and versions

Release the completed raw TupleCache and Through batching changes from main.
Both Turso and PostgreSQL remain authoritative; the new Redis adapter maintains
raw relationships. Firestore retires the previous generation-cache API and stays
durable-only. Preserve all existing tags and unrelated owner plan edits.

| Module | Previous | Selected |
|---|---|---|
| pockets/authorization | v0.14.1 | v0.15.0 |
| pockets/authorization/stores/turso | v0.8.1 | v0.9.0 |
| pockets/authorization/stores/pgx | v0.9.1 | v0.10.0 |
| pockets/authorization/stores/firestore | v0.2.1 | v0.3.0 |
| pockets/authorization/stores/goredis | new | v0.1.0 |

All stores pin core v0.15.0. Turso store pins the already released connector
v0.6.0 used by its tested local-file profile. Other unchanged connectors and SDK
keep their existing released pins. No unrelated module retag is required.

## Preconditions and workflow

- Local main and remote main both `4fb076155d98a58bc46e08a97154f1259ed938e9`.
  Remote tag inventory saved before release; selected tags do not yet exist.
- Record the completed source, plan and release documents in a selective commit;
  exclude `plans/cacher-design.md`, GPS-360 audit handoff and Segovia audit handoff
  owner edits from this release commit. Never stash/reset them.
- Verify exact dependency-ordered candidate module archives with GOWORK=off,
  versioned sibling requirements, no replacements, independent tidy/build/test/vet
  and integration/live compile checks. Candidate-only local proxy avoids looking
  up unpublished versions publicly; normal checksum verification stays enabled
  for published dependencies. Published verification has no checksum exemptions.
- Run make check against the final candidate versions. Prior real SQLite, PG17,
  Redis and relevant race proofs are retained for unchanged production source;
  actual Segovia and hosted infrastructure measurements belong to adoption.
- Recheck main; publish the tested commit using the normal repository workflow,
  never force-push or rewrite remote history. Cut immutable annotated nested tags
  at that exact main commit, dependency-first. Verify remote tag objects/commits.
- Download the published versions through the normal public proxy and sumdb,
  compare archive inventories/checksums/origins to candidates, and repeat isolated
  consumer build/test/vet. Record release commit, versions and evidence in a
  checked-in manifest, RELEASING and AUDIT-037.
- Write a standalone Segovia adoption/benchmark prompt with exact pins, source
  authority, migration cutover, supervised polling and post-commit Notify,
  freshness policy, baseline capture, concurrent sustained writes, revocation,
  recovery and honest SQL/Redis/latency/allocation measurements.

## Known verification limits

The owner's release request follows disclosure of remote Turso/replica routing,
non-C PostgreSQL locale, live Firestore cleanup, application concurrent load and
large-set capacity gaps. These remain unverified, not represented as passed.
The local implementation proof is in [authorization-tuple-cache.md](authorization-tuple-cache.md).
The mirror is one Redis hash/shard; whole sets are decoded before graph budgets.
Lookups/enumeration and direct relationship/role services remain durable. No
DecisionCache, no immediate-revocation claim before asynchronous delivery.

## Execution record

- Named platform/SRE review found no blocking migration/dependency issue; it
  required genuinely concurrent application benchmarks and explicit fallback
  measurement. These are included in the Segovia prompt.
- All five exact candidate archives passed independent tidy, versioned dependency
  graph validation, build/test/vet and integration/live compilation with GOWORK=off
  and no replacements. The new Redis adapter's generated sums were completed
  before the final candidate pass. No public lookup of unpublished versions was
  used. Evidence: `/private/tmp/tuple-cache-candidate-20260915-2/results.json`.
- [Release manifest](authorization-tuple-cache-release-manifest.json) records the
  candidate versions, archive checksums and source inventories.
- Final repository `make check` passed all 43 modules, generated-artifact checks,
  integration/live compilation and architecture guards using the candidate proxy.
  Log: `/private/tmp/tuple-cache-release-make-check.log`. Candidate inventories
  were rechecked unchanged. Unrelated owner plans remain unstaged.

## Publication

- Source commit `83d48957714e53e6c1dbcc1d2a62ef4650c448e8` was fast-forwarded to
  main, followed by all five dependency-ordered annotated tags at that commit.
  Existing remote tags were checked unchanged. All original owner plan contents
  were hash-verified preserved and remain uncommitted.
- Evidence: `/private/tmp/tuple-cache-publication-20260915/publication.json`.
- Initial public verification resolved the exact release origin but sum.golang.org
  had not indexed the new revision. This remains pending; no checksum bypass or
  candidate cache was used for public verification.

## Remote CI observation

The GitHub Linux `check` run 35011217829 fails in unchanged SDK
`filestorage.TestDisk_Conformance/RangeEdges`: seeking to MaxInt64 returns EINVAL.
SDK has no diff between baseline `4fb07615` and release `83d48957`; the source
last changed in `c3f8b4ad`. The local macOS full gate passed. This remote global
CI failure is not reported as green, and no unrelated SDK patch or retag is
folded into the five-module authorization release. Log:
`/private/tmp/tuple-cache-release-ci-failed.log`.

## Final public verification

All five module archives passed normal public proxy/sumdb verification. ZIP and
go.mod checksums, complete file inventories and origins match the verified
candidate and main release commit `83d48957714e53e6c1dbcc1d2a62ef4650c448e8`.
Independent versioned module graphs, build/test/vet and integration/live compile
checks passed with GOWORK=off, no replacements and no checksum exemptions.
The initial checksum-service indexing delay cleared. Evidence:
`/private/tmp/tuple-cache-public-20260915-2/results.json`.

A standalone versioned consumer also tidied, built, vetted and exercised actual
SQLite and Redis with the workspace disabled. Its 366-resource warm checks and
100 serial unrelated mutation/publication cycles used zero SQL during checks;
revocation, disposed outbox, old-Redis restoration and empty-mirror rebuild passed.
The temporary Redis process stopped. This does not replace the concurrent host
benchmark required by the Segovia prompt. Evidence:
`/private/tmp/tuple-cache-release-consumer/results.json` and `behavior.log`.

The five-module release and handoff are complete. The unchanged SDK Linux CI
failure above and deployment/application-scale checks remain explicitly open.
