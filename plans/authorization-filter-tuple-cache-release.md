# Authorization filter and readable Redis key release — 2026-09-15

Status: PUBLISHED AND PUBLICLY VERIFIED. The owner explicitly requested publication so Segovia can
adopt the completed fixes. Host edits and deployment remain separate.

## Scope and versions

| Module | Previous | Release |
|---|---|---|
| `pockets/authorization` | v0.15.0 | v0.15.1 |
| `pockets/authorization/stores/goredis` | v0.1.0 | v0.2.0 |

Core fixes relationship-owned `FilterAuthorized` routing through TupleCache.
The Redis adapter pins core v0.15.1, uses `tuplecache:{<namespace>}`, and accepts
ASCII letters, digits and `:._-/` in a verbatim namespace. Its namespace/key
contract change warrants a pre-v1 minor. No SQL migration is added.
The Turso adapter has a regression-test change only; consumers retain v0.9.0.
Other unchanged modules and tags remain unchanged.

## Preconditions and workflow

- Starting main: `43112566`. Check remote main and tags before publication.
- Preserve owner changes in `plans/cacher-design.md`,
  `plans/gps-360-go-audit-upgrade-handoff.md` and untracked
  `plans/segovia-v2-audit-upgrade-handoff.md`; their hashes are recorded under
  `/tmp/gopernicus-filter-tuple-cache/release-owner-sha.json`.
- Prior implementation checks passed: full `make check`, core race suite,
  SQLite filter regression with race detection, and isolated Redis race suite.
  The implementation plan records exact commands and limits.
- Prepare dependency-ordered candidate archives and dependency sums. Independently
  tidy/build/test/vet with GOWORK=off, versioned requirements and no replacements.
  A candidate-only file proxy supplies unpublished versions; public dependencies
  keep normal checksum verification.
- Reuse the existing release verification script with just the two selected
  modules. Verify a standalone SQLite/Redis consumer using Turso v0.9.0, including
  warm filtering, revocation and rebuilding the readable key.
- Run final `make check` with the candidate dependencies, check source inventories,
  selectively commit release files, and use ordinary fast-forward main publication.
- Publish immutable annotated module tags at the verified main commit, core first.
  Verify remote tag objects/commits and preserve prior tags.
- Download through the public Go proxy and checksum service with no exemptions,
  compare candidate archives, and repeat independent checks and the consumer.
- Record the release and verification in RELEASING, AUDIT-037 and a release manifest.

## Tasks

- [x] Prepare release notes, module pins and exact candidate verification.
- [x] Verify final workspace and standalone consumer.
- [x] Commit/push release source and annotated tags.
- [x] Verify public downloads, source/checksums and consumer; record evidence.

## Verification limits and adoption

The owner confirmed one Segovia dev server and no production cache. After adopting
both versions, restart that server; its normal relay poll automatically rebuilds
the new readable hash. No manual conversion or SQL migration is required.
Old hashes are left untouched. Live remote databases and Segovia's benchmark are
not run by this release. The previous release disclosed an unchanged Linux SDK
file-storage extreme-range CI failure; inspect the release CI without claiming
that previous failure is fixed here.

## Changed files and evidence

Implementation files: see [implementation plan](authorization-filter-tuple-cache.md).
Release additions: this plan, `authorization-filter-tuple-cache-release-manifest.json`,
Redis `go.mod`/`go.sum`, `RELEASING.md`, and `AUDIT.md`.
Task evidence root: `/tmp/gopernicus-filter-tuple-cache`.

## Execution record

- Remote main is `43112566a35ef9eab66a52737a1646b2e7b57e04`; both selected
  tags are absent. Saved all remote refs before publication.
- Both exact candidate archives passed independent tidy, module graph checks,
  build/test/vet and tagged compile/vet checks with GOWORK=off and no replacements.
  Evidence: `candidate/results.json` and `candidate.log` under the task root.
- The standalone consumer using the two candidates and published Turso v0.9.0
  passed tidy/build/vet and actual SQLite/Redis filtering with GOWORK=off and no
  replacements or checksum exemptions. Candidate hashes were seeded in go.sum;
  public dependency checks retained normal checksum verification.
- Its 128-resource warm filter used 0 SQL statements (8 Redis commands including
  first-use script loading). Across 100 serial unrelated publications, filters
  stayed at 0 SQL, recorded 100 cache hits and caused no rebuilds. Published
  revocation, restored-old-mirror recovery, empty-mirror fallback/rebuild,
  duplicate/order preservation and the readable key all passed.
- These are isolated fixtures, not Segovia latency or production-scale claims.
  Consumer evidence: `consumer/candidate-results.json` and
  `consumer/candidate-behavior.log`. The Redis fixture stopped on completion.

- Final workspace `make check` passed, including build/test/vet, generated-artifact
  drift checks, tagged compilation and layering guards. Candidate source inventory
  still matches. Log: `release-make-check.log` under the task evidence root.
- Release source committed and pushed to main as
  `26c7ff55fc728cac88724309028f11d99c953db9`. Both annotated nested tags were
  published dependency-first at that commit; remote tag objects and peeled
  commits match. All prior tags and owner edits are preserved.
- Initial public download resolved the correct Git tag/commit but the checksum
  service returned an indexing-time unknown revision. Normal downloads were
  retried with checksum verification enabled; the indexing delay resolved.
  No bypass was used.
- GitHub release CI run `35025825443` failed in the unchanged SDK
  `TestDisk_Conformance/RangeEdges`: Linux rejects seek at MaxInt64 with EINVAL.
  The SDK has no diff between pre-release main and the release commit. This is
  the same failure disclosed by the preceding release, not a newly claimed fix.
  Log: `release-ci-failed.log`; run inventory: `release-ci.json` under the task root.

## Public verification

Both published module archives match the tested candidates and release commit
`26c7ff55fc728cac88724309028f11d99c953db9`. Normal public checksum verification,
independent module graphs, build/test/vet and tagged checks passed with GOWORK=off
and no replacements or checksum exemptions. Evidence: `public-verified/results.json`.

The standalone consumer repeated its successful SQLite/Redis filtering and recovery
checks against published versions. Warm filters and filters after revocation used
zero SQL; all 100 unrelated write/publication cycles kept cache hits without rebuilds.
Turso v0.9.0 remains compatible. Evidence: `consumer/public-results.json` and
`consumer/public-behavior.log`. The temporary Redis process stopped.

The release is complete. Segovia adoption/benchmarking and the unchanged Linux SDK
CI failure remain separate. No host files, services or caches were modified.
