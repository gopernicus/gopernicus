# Inbound authorization and integrity release

Status: IN PROGRESS. Owner authorized push to main and release.

## Scope and versions

| Module | Previous | Release |
| --- | --- | --- |
| pockets/authentication | v0.11.1 | v0.12.0 |
| pockets/authorization | v0.19.0 | v0.20.0 |
| pockets/authorization/stores/pgx | v0.13.0 | v0.14.0 |
| pockets/authorization/stores/turso | v0.12.0 | v0.13.0 |

One main commit, four annotated tags, core before adapters. Breaking pre-v1 APIs
move all principal permission policy to inbound, make tuple commands principal-free,
replace Guardian names with IntegrityPolicy, enforce integrity for every ordinary
writer, require role WritePolicy and prepare invitation commands before admission.
Include implementation, tests, benchmarks, documentation and completed records.
Update SQL adapter and auth-cms pins. Unchanged Redis, authentication adapters,
views, SDK and connectors receive no tag; verify their compatibility with new cores.
No SQL schema or cache protocol change. No host adoption or data migration.

## Preconditions and preservation

- Local main and remote main start at 22c712e2100ff58e0b8a3a53528a9b43ae69c942.
- Confirm four proposed tags absent locally and remotely; preserve every old tag.
- Selectively stage only task files. Exclude owner plans/cacher-design.md,
  plans/gps-360-go-audit-upgrade-handoff.md,
  plans/segovia-v2-audit-upgrade-handoff.md and Python cache files.
- Retain prior implementation evidence after source inventory comparison: full
  workspace check, core/authentication/example races, owned PostgreSQL and SQLite
  integrity races, non-C PostgreSQL and benchmark runs. Docs build/typecheck and
  executable snippets passed in the documentation pass.
- No force push, branch protection bypass, tag replacement or production fixture.

## Tasks and gates

- [x] R1 Freeze scope, update pins/notes, construct exact module candidates.
- [x] R2 Independently tidy/build/test/vet candidates with GOWORK=off and no
  replacements; test versioned auth-cms and a standalone inbound/integrity consumer.
  Run final full make check with release pins. Verify unchanged adapters remain
  compatible; SQL/Redis end-to-end if available using disposable local data.
- [ ] R3 Named platform/SRE review; verify staged source equals verified archives.
  Commit and push main normally, confirm remote main, then push four annotated tags.
- [ ] R4 Verify public archive inventories, normal Go checksums, Git origin,
  versioned build/test/vet and consumer behavior. Inspect actual GitHub CI, record
  durable publication evidence, and push root documentation receipt separately.

Use a candidate-only file proxy for unpublished versions, with exemptions limited
to those candidates. Public verification uses sum.golang.org without exemptions.
Avoid duplicate large dependency cache copies given limited temporary disk space.
Candidate/public tools sanitize environment so tests inherit no host DB credentials.

## Known limits

Remote Turso and live authentication datastores are not part of this release check.
Prior GitHub Linux CI fails unchanged SDK TestDisk_Conformance/RangeEdges (seek at
MaxInt64 returns EINVAL). Inspect and report the actual run, without implying
the remote workspace gate passed. No unrelated SDK fix in this release.

Evidence: /tmp/gopernicus-authorization-integrity-release. Completed plan and
checksum/verification manifest belong under plans/.

## Candidate verification

All four versioned candidates passed independent tidy/build/test/vet and tagged
compile/vet. The final 42-module make check passed in 155.84 seconds with all
1917 inputs unchanged. Auth-CMS and the standalone inbound/integrity consumer
passed with race detection and no replacements. Published unchanged authentication
pgx/turso/firestore stores and Goth views passed compatibility build/test/vet with
the new core. Redis v0.4.0 passed against the new core and both SQL stores,
including real SQLite/Redis delivery, role reads and final-revocation race tests.
Published sibling archives were downloaded with normal checksum verification
before candidate use. Compatibility copies changed only go.mod/go.sum.

The release note includes the SRE review's old-writer rollout requirement.
Temporary harness/fixture setup errors (consumer package setup, readonly module
copies and missing sibling migration directories) were corrected; final checks
passed without production source changes. Prior live SQL/race and benchmark
evidence remains valid: all 1917 implementation inputs matched before pin changes.
