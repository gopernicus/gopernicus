# Authorization consistent lookup release — 2026-09-15

Status: PUBLISHED AND PUBLICLY VERIFIED. The owner explicitly requested publication of the completed
lookup fix so Segovia can adopt it. No host edits or deployment are included.

## Scope and versions

| Module | Previous | Release |
|---|---|---|
| `pockets/authorization` | v0.15.1 | v0.16.0 |
| `pockets/authorization/stores/turso` | v0.9.0 | v0.10.0 |
| `pockets/authorization/stores/pgx` | v0.10.0 | v0.11.0 |

The optional exported lookup-snapshot capability, new 503-class contention error
and coordinated SQL adapter adoption warrant minor releases under RELEASING.md.
Both SQL stores pin core v0.16.0. Redis remains compatible at v0.2.0; Firestore
keeps the guarded fallback with bounded retries when paired with the new core.
No schema migration or connector release is required.

## Preconditions and workflow

- Starting main `3a50cab7525bb35ee45f9f75442f2749ef50300a`; remote main matches.
  Selected version tags are absent. All prior authorization refs are saved under
  `/tmp/gopernicus-lookup-snapshots/release-remote-before.txt`.
- Preserve the three owner plan files listed in the implementation plan; hashes
  recorded in `release-owner-sha.json` under the same evidence directory.
- Implementation checks passed: full workspace make check; core, SQLite and
  isolated PostgreSQL full race tests; controlled revocation, unrelated writes,
  transaction/lifetime and 16-reader/2-writer lookup tests.
- Stage only release-owned files, pin SQL stores, prepare exact module archives
  with the existing release verifier. Candidate-only proxy supplies unpublished
  versions; external dependencies retain public checksum verification.
- Verify independent versioned module tidy/build/test/vet and tagged compilation,
  GOWORK=off, no replacements. Exercise a standalone versioned SQL consumer.
- Recheck workspace and candidate source inventory; selectively commit source,
  push main normally, and publish annotated immutable tags dependency-first.
- Verify remote tag objects/commits, public archive source and normal Go checksums;
  repeat independent module and consumer verification against public versions.
- Record results in this plan, release manifest, RELEASING.md and AUDIT-038.

## Tasks

- [x] Prepare release notes, pins and verified candidates.
- [x] Verify standalone consumer and final workspace.
- [x] Commit/push source and publish tags.
- [x] Verify public artifacts and consumer, record release completion.

## Limits and adoption

Upgrade core plus the configured SQL adapter. Core alone supplies bounded retries;
new SQL adapters supply the read snapshot. Snapshots cover one lookup call, not
multiple pages. Existing ambient transactions retain caller isolation and ownership.
Persistent discovery/verification mismatch becomes model.ErrEnumerationContended
wrapping sdk.ErrUnavailable; hosts may attach Retry-After using errors.Is.

Segovia's /home HTTP benchmark, remote Turso/sqld and live Firestore remain
unverified. Prior release CI disclosed an unchanged Linux SDK RangeEdges failure;
inspect current CI and report its actual result without claiming that bug fixed.
No cache rebuild, schema migration or host configuration change is introduced.

## Changed files and evidence

Implementation files: [implementation plan](authorization-lookup-snapshots.md).
Release additions: this plan and release manifest; SQL store go.mod/go.sum;
RELEASING.md and AUDIT.md. Evidence root: `/tmp/gopernicus-lookup-snapshots`.

## Execution record

- All three exact candidates passed independent tidy/build/test/vet, module graph,
  source checks and tagged compilation with GOWORK=off and no replacements.
  Candidate report: `candidate/results.json`; candidate hash entries are in the
  [manifest](authorization-lookup-snapshots-release-manifest.json).
- Standalone versioned consumer passed both SQL snapshot, transaction/lifecycle
  and concurrent suites (1,024 lookups, 16 readers, two writers per backend).
  It also retained Redis v0.2.0 and passed warm 128-resource filters with zero SQL,
  revocation, 100 unrelated publications and mirror recovery after restore/loss.
  Report: `consumer/candidate-results.json`; `consumer/candidate-lookup.log` and
  `consumer/candidate-behavior.log`. Temporary Redis fixture stopped.
- Final workspace `make check` passed with release pins, including all module
  build/test/vet, generation drift checks, tagged compilation and guards.
  Log: `release-make-check.log`. Source matches verified candidate inventories.
- Release source committed and pushed to main as `bd534a495010abe8720febffb8d2caf1b0497577`.
  All three annotated tags were published dependency-first at that commit. Remote
  tag objects/commits match; all prior tags and owner file hashes are preserved.
  Receipt: `publication/publication.json`. Public verification passed after checksum indexing completed.

- Initial normal public downloads resolved the correct tag and source commit,
  but the checksum service returned indexing-time unknown revision for core.
  Normal public verification subsequently passed; no bypass was used.
- GitHub CI run [35029677164](https://github.com/gopernicus/gopernicus/actions/runs/35029677164)
  failed the unchanged SDK `TestDisk_Conformance/RangeEdges` case: Linux rejects
  seek at MaxInt64 with EINVAL. SDK has no source diff from pre-release main.
  This is the same failure disclosed by the previous release. Log:
  `release-ci-failed.log`; local full workspace checks passed.

- The proxy's core .info metadata omits Origin.Ref after commit-based resolution.
  The verifier checks the live annotated remote tag object and peeled commit
  against the publication receipt in that case. Normal public checksum, exact
  archive/source, repository, subdirectory and commit checks remain mandatory.
  This verification-tool correction changes no released source or checksum.

## Public verification and completion

All three public module archives match the verified candidates and release commit
`bd534a495010abe8720febffb8d2caf1b0497577`. Normal public checksum and Git origin
verification, independent versioned build/test/vet, tagged compilation and module
graph checks passed with GOWORK=off and no replacements or checksum exemptions.
Report: `public-confirmed/results.json`.

The standalone consumer repeated its successful SQLite/PostgreSQL lookup tests
against public versions: controlled interleavings, snapshot lifetimes, ambient
transactions and 1,024 lookups with 16 readers/two writers per SQL backend. Redis
v0.2.0 stayed compatible: warm filters used zero SQL; revocation and mirror
restore/loss recovery passed. Reports: `consumer/public-results.json`,
`consumer/public-lookup.log` and `consumer/public-behavior.log`.
The disposable Redis and PostgreSQL fixtures were stopped after verification.

Release complete. Segovia adoption and its /home HTTP benchmark remain host work.
The unchanged Linux SDK CI failure remains disclosed. No host files, caches or
services were modified.
