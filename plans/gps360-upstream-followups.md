# GPS-360-Go upstream fixes — September 2026

Status: RELEASED and public-module verified, 2026-09-13. Release authorization includes
patched module tags and consumer repinning instructions. No production or live
GCP work is needed; related Firestore validation receives fresh emulator coverage
and carries forward the user-accepted unverified live-GCP limitation.

## Preconditions and scope

Work in `/tmp/gopernicus-gps360-followups-20260913`, branch
`gps360-upstream-fixes-20260913`, based on `aa75aa0776d3398c75d35a58c54aa17479f65c91`.
Preserve the original checkout's modified handoff, all published tags and remote
main. Do not modify the consumer checkout. Its reproducer and verification report
are in `workshop/migrations/UPGRADE-gopernicus-audit-2026-09.md` in
`/Users/jrazmi/code/gps/three-sixty/gps-360-go`.

The consumer reproduced cross-user identifier-primary mutation through DELETE
`/auth/identifiers/{id}?replacement=...` with authentication v0.11.0 / pgx v0.6.0,
including with password and delivery disabled. Account takeover was not proven.
Core checks and store transaction checks must reject foreign or ineligible
replacement identifiers without any partial changes; valid same-user replacement
must continue working. Inspect and fix equivalent first-party SQL/memory paths
if affected; retain the existing Firestore ownership protection.

The PostgreSQL limiter needs optional validated schema selection covering Allow,
Reset and StatusCheck on one unpinned pool. Preserve default unqualified behavior,
admission algorithm and key namespace. Host applies qualified DDL before startup;
no automatic migration or search-path/pool workaround.

The consumer reports builds, full tests, vet, race checks, guards, parity, fresh
and seeded migrations, storage emulators, 50 runtime checks and manual browser
verification passed. Browser OAuth used a synthetic provider. Live Google/GCP
remains unverified. These are consumer-reported results, not upstream reruns.

## Tasks and ownership

1. Credential implementer: core validation, atomic adapter enforcement and
   regression coverage, including the disabled-feature route and valid replacement.
   Read ARCHITECTURE.md and the repository implementer role. Inspect SQL siblings
   and coordinate any necessary shared conformance or Firestore changes.
2. Limiter implementer: `WithLimiterSchema(pgxdb.Schema)` option, qualified SQL,
   host DDL documentation and shared-pool schema isolation regressions. Own pgxdb
   files only; preserve default behavior.
3. Release coordinator: isolated local PostgreSQL fixture, independent review,
   full verification, exact module dependency pins and release evidence. Use the
   named platform-SRE role for release/security review; legacy role model labels
   are unavailable here, so inherit the configured runtime model.
4. Publish changed modules in dependency order after candidate verification.
   Tentative patches: pgxdb v0.7.1, authentication v0.11.1, authentication/pgx
   v0.6.1, authentication/turso v0.5.1 and authentication/firestore v0.1.1.
   Turso is confirmed affected; Firestore already checks ownership but needs the
   same eligibility checks and therefore fresh emulator/race evidence. Optional
   limiter host configuration preserves defaults (RELEASING.md patch policy).
5. Return a durable handoff with exact published pins, qualified limiter DDL,
   startup wiring, migration/rollback notes and remaining verification limits.

## Verification and release gates

- Format changed Go with `/Users/jrazmi/go/bin/goimports`.
- Use `GOTOOLCHAIN=local GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache`.
- Focused core/SQL regressions; real PostgreSQL transaction and shared-pool schema
  tests; race checks for changed modules. Use a new owned local fixture, never
  existing application containers/databases. SQLite tests for an affected Turso
  adapter; Firestore emulator if the Firestore implementation changes.
- `make check`: generation drift, all-module build/test/vet and boundary guards.
- Verify isolated candidate module archives with GOWORK=off and no replacements;
  preserve normal public checksum verification for existing dependencies.
- Commit exact tested source, annotated nested-module tags; verify public module
  downloads with normal sumdb settings and source/checksum identity. No tag moves.
- Record exact passes, skips, commands and public versions here and in a release
  manifest; clean up only owned fixtures. No claim of live Google/GCP coverage.

## Execution record

- Original handoff SHA-256 before work:
  `6afdee8279ab6639309341270fa7c775e5444947136784215f81743007449f7a`.
- Original checkout and consumer files remain outside implementation scope.

### Implementation and verification

- Implemented the two requested fixes plus matching Turso/Firestore/example
  memory validation. Shared command validators define owned/active target and
  primary replacement eligibility. SQL checks and row locks run inside the
  owning-user transaction; Firestore validates during transaction reads.
- Core checks explicit replacement before consuming a step-up grant. Preserve
  same-user contact-only replacements and existing login/recovery verification.
- Original core and PostgreSQL source failed the new regressions. The exact HTTP
  reproducer returned 200 and changed persisted state with both password and
  delivery disabled; fixed source returns 404 with no credential state changes.
  A valid same-user request returns 200. Only token cryptography is stubbed in
  the upstream HTTP harness; middleware, sessions and repositories are real.
- Full authentication core, PostgreSQL, local libSQL and Firestore emulator race
  suites passed. The full pgxdb PostgreSQL race suite recorded 279 passing
  test/subtest outcomes and zero skips. Example memory conformance race passed.
- Final archive-based focused races passed 98 test/subtest outcomes, zero skips:
  core (12), PostgreSQL default and qualified schema (22 each), Turso (18),
  Firestore (18), limiter shared-pool schema tests (6). These runs cover the final
  test-only fixture compatibility refinements made after the full races.
- All five independent candidate module tidy checks, versioned graphs,
  build/test/vet and integration/live compile/vet checks passed. Archives contain
  548 owned source entries including inherited licenses; nested module contents
  stay out of parent archives. Tests use exact verified copies in repository
  layout so the existing cross-store migration-parity checks run unchanged.
  Imports still resolve tagged module-cache versions with GOWORK=off and no
  replacements. Candidate-v1 exposed the missing sibling test fixture; the
  corrected verifier passed candidate-v2 without changing repository tests.
- `make check` passed all 42 modules and guards in 133.88 seconds, with zero
  source/generation drift. A first harness attempt pinned a `/tmp` workspace
  alias incorrectly; normal per-directory workspace discovery fixed the harness.
  All 16 changed Go files are goimports-clean; generated templ/assets remain
  unchanged against the base commit.
- Platform-SRE reviewed the implementation, migration/handoff, candidate verifier
  and immutable-tag publisher. Corrected the stale Firestore SCHEMA no-op text
  and verification-tool completion flags before release.
- No real Google/GCP run occurred. PostgreSQL's optional non-C collation fixture
  was not configured. Full authentication race logs are nonverbose; exact zero
  skip counts are claimed only for the recorded final targeted races and pgxdb
  suite. Hermetic repository/module checks do not substitute for backend tests.
- Durable evidence and module checksums:
  [gps360-upstream-release-manifest.json](gps360-upstream-release-manifest.json).
  Consumer instructions: [gps360-upstream-repin-handoff.md](gps360-upstream-repin-handoff.md).

### Changed files

- pgxdb limiter, constructor option tests, shared-pool schema tests and README.
- Authentication credential definitions/contract/validator, RemoveIdentifier,
  core replacement tests and README.
- PostgreSQL credential mutations, real HTTP regression, README and module pins.
- Turso credential mutations and module pins.
- Firestore credentials, README/SCHEMA and core dependency pin.
- Shared credential conformance/reference, example authmem implementation and
  example core pin. Associated go.sum files updated by candidate sums/tidy.
- RELEASING.md and the three follow-up plan/manifest/handoff files.

### Publication

All five annotated module tags were published at
`5fb8d51cb2355027c6a32a9b1bb617acb42b13db` in dependency order:

| Module | Published patch |
|---|---|
| integrations/datastores/pgxdb | v0.7.1 |
| pockets/authentication | v0.11.1 |
| pockets/authentication/stores/pgx | v0.6.1 |
| pockets/authentication/stores/turso | v0.5.1 |
| pockets/authentication/stores/firestore | v0.1.1 |

Every public module passed independent download/source/checksum verification,
versioned graph, build/test/vet and integration/live compile/vet with GOWORK=off,
no replacements and no checksum exemptions. All 548 source entries matched the
verified candidates. SQL parity tests used exact verified sibling archives, with
imports still resolved through versioned module-cache dependencies.

Public proxy/checksum propagation initially returned cached 404 responses for the
pgxdb and core tags. Verification resumed after ordinary checksum verification
succeeded; Go's standard proxy/direct fallback was reviewed and used. No tag was
moved, recreated or forced. The original tag objects and remote main remain
unchanged. The release branch contains the implementation and durable handoff;
main was not merged or pushed. The original checkout's user-edited handoff was
verified unchanged by SHA-256; the consumer repository was not edited.

The changed auth-cms example also passed normal public-module download/graph,
build, authmem race and vet checks with GOWORK=off and no replacements. Only the
three task-owned PostgreSQL/libSQL/Firestore emulator containers were removed,
after ID/label checks; existing application containers were preserved.
