# Host authorization controls release — 2026-09-16

Status: EXECUTED. Published and publicly verified on 2026-09-16.

## Release scope

Publish pockets/authorization v0.19.0 from main, then an annotated
pockets/authorization/v0.19.0 tag. Core is currently v0.18.0. Require becomes
variadic (exact method/function types must adapt), so use a pre-v1 minor release.
Normal calls stay compatible; denial response customization is optional, default
403 remains, and existing WithLogger enables DEBUG decision records. Current
store/connector pins, schema and cache protocol remain compatible and unchanged;
no store/connector tags or example pin updates are needed.

Per-policy denial presentation is included. Same-route visibility/action denial
classification is outside this release, as already documented.

## Preconditions and preservation

- Main and origin/main both start at f1dbd30d9049f3835bf3aaecbafcaa9e4e32cacc.
- Selected release tag absent remotely; check local absence before publication.
- Preserve owner plans/cacher-design.md, plans/gps-360-go-audit-upgrade-handoff.md,
  plans/segovia-v2-audit-upgrade-handoff.md, and pre-existing Python cache files.
- Source exactly matches the implementation's verified Go inventory. Retain the
  passing full42-module make check, full authorization race suite, formatter,
  architecture/backend reviews and27benchmark samples; source is unchanged.
- No existing tags changed, no force push, no host adoption or datastore mutation.

## Tasks

- [x] Prepare release notes and exact core module candidate.
- [x] Independently tidy/build/test/vet/race the candidate with GOWORK=off, no
  replacements, candidate-only file proxy and normal checksums for public deps.
  Exercise an external versioned HTTP consumer's403/404/allow and DEBUG behavior.
  Reuse the full workspace gate only after checking source and inventory equality.
- [x] Named platform/SRE release review, selective staging, source inventory freeze.
- [x] Commit and push main normally; verify remote main; annotate/push release tag.
- [x] Verify public archive/source/checksum/Git origin, independent checks and
  external consumer. Inspect actual GitHub CI, record a durable receipt and push
  documentation completion separately.

## Operational limits

Existing GitHub Linux SDK RangeEdges seek-at-MaxInt64 failure is outside this
change; inspect and disclose actual release CI. Live SQL/Redis and remote Turso
suites are not repeated for this core presentation/logging change. Focused
benchmarks discard output and do not measure production logging I/O.

Temporary release evidence: /tmp/gopernicus-authorization-host-policy-release.
Use empty dependency seed caches for this small stdlib-based module to avoid
repeating the preceding release's large disposable cache copies. Resolve the
published commit through the public proxy before normal version verification;
never disable public checksum verification to work around indexing delay.

## Execution receipt

- Release commit: `68866b39e3d68830dd97d4e1d4232ef390d0d23c`, pushed normally to
  main before the annotated `pockets/authorization/v0.19.0` tag.
- Tag object: `30d6e3bb5592935e41f4f92f92289559d0f3f0e7`. Existing remote tags
  retained; no force pushes. All three owner plan files and Python cache preserved.
- Candidate and public archive match all 227 entries, module/go.mod checksums and
  Git origin. Public verification uses sum.golang.org with no checksum exceptions.
- Candidate and public independent build/test/vet/race, tagged compile/vet and
  no-replacement module graph checks passed. Candidate tidy made no source change.
- Standalone versioned consumer exercised real HTTP 403/404/204/401 responses and
  exactly four host-tagged DEBUG decision events, with race detection, in both
  candidate and public modes.
- The prior full 42-module make check is retained: all 306 authorization Go files
  and their inventory were rechecked, with no source or dependency changes since
  that gate. Architecture/backend reviews passed; named platform/SRE release
  review found no concrete blockers. Nine benchmark cases have 27 samples.
- GitHub [main CI](https://github.com/gopernicus/gopernicus/actions/runs/35132569235)
  and [tag CI](https://github.com/gopernicus/gopernicus/actions/runs/35132572027)
  failed at the existing SDK `TestDisk_Conformance/RangeEdges`: Linux rejects the
  seek at MaxInt64 with `invalid argument`. SDK is unchanged. CI therefore did not
  complete the workspace gate; a separate SDK fix remains follow-up work.
- Live SQL/Redis and remote Turso were not rerun for this presentation/logging
  release. No host adoption, datastore mutation or deployment was performed.

Durable evidence: [release manifest](authorization-host-policy-release-manifest.json),
[implementation verification](authorization-host-policy-verification.json), and
[implementation record](authorization-host-policy.md). A subsequent root-docs-only
commit records publication; the release tag continues to identify the verified
source commit above.
