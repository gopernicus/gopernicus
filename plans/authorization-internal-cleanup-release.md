# Authorization internal cleanup release

Status: IN PROGRESS. Owner authorized release on 2026-09-16.

## Scope and versions

| Module | Previous | Release |
| --- | --- | --- |
| pockets/authorization | v0.20.0 | v0.21.0 |
| pockets/authorization/stores/pgx | v0.14.0 | v0.15.0 |
| pockets/authorization/stores/turso | v0.13.0 | v0.14.0 |

Coordinated pre-v1 minor releases cover obsolete API removals and stricter raw
relationship reads. Update SQL adapter and auth-cms version pins. Keep unchanged
Redis, authentication, SDK and connectors at their published versions. No schema,
cursor or cache protocol changes, host adoption or production mutation.

## Preconditions and preservation

- Local and remote main start at 6bad23cc8d9432a42f7125bf98ed695a817d2d6d.
- Proposed versions are absent remotely; confirm absent locally before tagging.
- Include cleanup implementation, tests, audit artifacts, notes and release record.
- Exclude owner plans/cacher-design.md, plans/gps-360-go-audit-upgrade-handoff.md,
  plans/segovia-v2-audit-upgrade-handoff.md and Python cache files; preserve hashes.
- Retain prior owned PostgreSQL/default/named-schema, SQLite and SQL-to-Redis
  race evidence only after matching all recorded implementation inputs.
- Ordinary main/tag pushes only; preserve all existing tags and branch protection.

## Tasks and gates

- [x] R1 Update pins/adoption notes and freeze exact module archive candidates.
- [x] R2 Independently tidy/build/test/vet with GOWORK=off, no replacements;
  verify auth-cms, a standalone consumer and unchanged Redis compatibility.
  Run the final 42-module make check after version pins change.
- [ ] R3 Confirm committed source equals verified candidates; push main and
  annotated module tags in dependency order.
- [ ] R4 Download public archives with normal sum.golang.org verification;
  compare source inventories and Git origins, run public module and consumer
  checks, inspect GitHub CI, and publish the documentation receipt.

Use a private file proxy only for unpublished candidates. Public verification has
no checksum exemptions. Sanitize test environment; use owned fixtures only.
Reuse external download archives with hard links to limit temporary disk use;
candidate and public module caches remain separate for the new versions.

## Known limits

Previous GitHub Linux CI fails unchanged SDK TestDisk_Conformance/RangeEdges when
seeking to MaxInt64. Inspect and report this release's actual runs; do not imply
the remote workspace gate passed. No unrelated SDK fix is in scope.
Remote Turso, production-scale performance and arbitrary external hosts remain
outside verification. Existing core and example HTTP suites exercise real handlers.

Evidence directory: /tmp/gopernicus-authorization-internal-cleanup-release.
The release manifest and completed plan retain durable publication evidence.

## Candidate verification

All three exact versioned candidates passed independent tidy/build/test/vet and
tagged compile/vet with GOWORK=off and no replacements. The standalone consumer
passed HTTP admission/404, integrity, raw counts/paging, invalid selector/search,
cancellation and graph-filtering checks under the race detector. Auth-cms passed
build/race/vet with the new pins. Published Redis v0.4.0 passed unchanged-source
compatibility and real SQLite/Redis delivery, roles and revocation races.

The final 42-module make check passed with all recorded inputs unchanged. The
earlier owned PostgreSQL/default/named-schema, local SQLite and both SQL-to-Redis
race results remain valid: all 1743 implementation/build inputs matched before
release pin changes. Module archives contain 312 entries in total.
