# Authorization cache release — 2026-09-14

Status: PUBLICATION AUTHORIZED WITH DISCLOSED QUALIFICATION EXCEPTION.

The owner authorized pushing and releasing the implemented authorization cache.
The implementation is locally merged into `firestore-release-20260911` at
`f8f45c8d`; candidate preparation uses `authorization-cache-20260914` in
`/private/tmp/gopernicus-authorization-cache-20260914`. Preserve the owner's other
plan/handoff edits. No host activation, database migration, deployment or live
cloud mutation is part of this request.

## Release candidates

Remote tags were re-read before selecting these minor versions. These additions
expand the coordinated host contract; optional SQL invalidation adds a separate
migration source. No SDK, Redis adapter, authentication or Firestore connector
release is required for this feature.

| Module directory | Latest final | Candidate |
|---|---|---|
| integrations/datastores/turso | v0.4.0 | v0.5.0 |
| integrations/datastores/pgxdb | v0.7.1 | v0.8.0 |
| pockets/authorization | v0.13.0 | v0.14.0 |
| pockets/authorization/stores/turso | v0.7.0 | v0.8.0 |
| pockets/authorization/stores/pgx | v0.8.0 | v0.9.0 |
| pockets/authorization/stores/firestore | v0.1.0 | v0.2.0 |

## Sequence

1. Commit the final owned verification runner and implementation evidence.
2. Pin changed stores to the candidate core/connectors. Verify exact versioned
   module artifacts with `GOWORK=off`, no local replacements, isolated
   build/test/vet, tagged-test compilation, migration exports and public wiring.
3. Run `make check` on the resulting source; record commit and evidence.
4. Push the implementation branch without force, preserving remote main and
   unrelated branches. Merge verified follow-ups into the user's local checkout.
5. Resolve the qualification gaps below before cutting final module tags.
6. Publish connector tags, then core, then stores; verify remote tag identity,
   public module checksums and isolated consumer build/test/vet after publication.

## Qualification

Local correctness passed: full repository checks, core/store race suites,
PostgreSQL/default and explicit schema, libSQL HTTP, Firestore emulator,
independent-process LRU/Redis cache behavior, and mounted SQL authentication.
The final owned all-mode runner passed with zero required adapter skips and
successful cleanup. The implementation plan records exact reports and commands.

Outstanding final-release requirements from `RELEASING.md` and the implementation
plan:

- Real GCP Firestore cache verification with ready indexes and independent clients.
  No disposable project/database and credentials were supplied or authorized.
- Turso Cloud authoritative primary routing verification; local file/HTTP evidence
  does not certify a cloud replica route.
- Representative performance acceptance: provisional PostgreSQL/Turso thresholds
  failed; the Firestore scheduled-load matrix saturated and timed out before cache
  legs. Microbenchmarks show warm gains and measurable cold/write overhead, but
  do not satisfy this gate or establish a host SLO.

Earlier Firestore release exceptions do not cover this feature. Pushing the
source does not certify final-release qualification. No tag has been cut by this
release task. If the owner accepts a release exception, record its exact scope
and retain all failed/unmeasured evidence; do not rewrite results as passed.


## Prepared evidence and publication state

- Six versioned candidates passed isolated tidy (no drift), no-replacement module
  graphs, build/test/vet and integration/integration+live compilation. Candidate
  archives match source inventories; public dependencies retain checksum checks.
  Exact proposed versions and hashes are in
  [authorization-cache-release-manifest.json](authorization-cache-release-manifest.json).
- Final verification runner and plans committed as `9dfa5e95` and merged into the
  original checkout. Other owner plan changes are untouched. The original
  requested implementation plan was backed up before merging its status updates.
- Parent verification PostgreSQL and both containers were stopped only after
  exact PID/data-directory and container-label checks. Evidence was retained.
- `git push -u origin authorization-cache-20260914` was rejected by automatic
  approval review: exact external destination approval/trust was not established,
  and release gates remain unmet. No remote write or tag occurred. Do not retry
  through another transport; obtain explicit approval for the named destination.
- Candidate dependency pins remain on the release worktree until publication;
  merging unpublished requirements into the everyday checkout would make normal
  public dependency resolution fail. The implementation there remains available.

- Candidate code/pins commit: `8688160a`. Final `make check` passed all 42 modules,
  generation/tag-compilation checks and architecture guards using the verified
  candidate proxy. The initial plain-public-proxy run could not resolve the
  deliberately unpublished versions; no dependency resolution failure was hidden.
  Final log: `/private/tmp/authorization-cache-release-make-check-candidate.log`.
- Candidate source inventory was rechecked successfully after preparation.
  No module tags were created. Final remaining action requires exact destination
  approval and either fulfillment or explicit owner acceptance of the recorded
  qualification gaps. No failed benchmark result will be relabeled as passed.


## Owner release exception — 2026-09-14

After the exact GitHub destination, six-module scope and verification gaps were
presented, the owner replied: “yeah go ahead and release as the next semvar of
 auth”. This authorizes the requested push to
`https://github.com/gopernicus/gopernicus.git` and the six prepared minor releases,
including authorization core `v0.14.0`, with the missing real-GCP/Turso Cloud
verification and failed/incomplete performance acceptance explicitly accepted
for publication. These results remain unverified/failed, not passed. This does
not authorize host activation, database migrations or deployment.
