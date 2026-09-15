# Authorization cache integration and release from main — 2026-09-14

Status: MERGED INTO MAIN AND PUBLISHED; PUBLIC VERIFICATION PENDING. Owner explicitly requested integrating all cache work into
updated main, adapting to newly merged work where needed, then publishing fresh
versions from main. Existing tags remain immutable. Prior accepted cloud and
performance verification gaps persist; no host deployment is authorized.

## Preconditions and integration decision

- Current origin/main: `437da308c0e106acde5aab9cd3214f144749530b`.
- Cache branch: `3b6e16a3f6b61e816de5c681626c06c973f1a6fd`.
- Main's entire tree is `a74436c4ab7f86974402128d3ab9032f3dbe3062`, exactly equal
  to `1c0f06d9`, an ancestor of the cache branch. Main's new commits squash the
  already included Firestore trains; no main-only source change exists.
- Named backend lead independently confirmed tree equality and ancestry. An
  explicit `git merge -s ours` from the cache-based integration branch safely
  joins this exact main history while retaining the fully audited newer tree.
  This is justified by whole-tree identity, not a preference to discard conflicts.
- Worktree: `/private/tmp/gopernicus-authorization-cache-main-20260914`, branch
  `authorization-cache-main-20260914`. The original checkout and its owner
  changes remain preserved. Historical conflicting local tags are not overwritten;
  fetch main with `--no-tags` and inspect remote tags directly.
- Original six-module release passed full public checksum/source/build/test/vet
  verification: `/private/tmp/authorization-cache-public-20260914-3/results.json`.
  That prior verification is distinct from this new release.

## Planned sequence

1. Record the independently reviewed ancestry merge; verify merge parents and
   unchanged source tree. Recheck main before publication.
2. Prepare new patch candidates and pin the three stores to the new core and
   changed connector versions. No production algorithm changes are needed unless
   verification discovers a concrete problem.
3. Verify all six exact candidate archives with GOWORK=off, no replacements,
   isolated tidy/build/test/vet and integration/live compilation. Run make check
   against the candidate proxy to avoid negative public lookups before tagging.
   Compare module production files to the already live-verified release; if
   unchanged, retain those local multi-process/race/HTTP proofs rather than
   rerunning an identical expensive performance matrix.
4. Push the tested integration commit to main with a normal fast-forward push.
   Re-read remote main and refuse to overwrite concurrent updates. Merge the
   complete resulting commit into the original local checkout.
5. Only after remote main contains that commit, publish new annotated module
   tags from it, in connector/core/store order. Verify all public checksums,
   archive/source identities, origins, isolated builds/tests/vet, and record the
   final manifest on main. Never move prior tags.

## New patch versions

| Module directory | Published | Main-based rerelease |
|---|---|---|
| integrations/datastores/turso | v0.5.0 | v0.5.1 |
| integrations/datastores/pgxdb | v0.8.0 | v0.8.1 |
| pockets/authorization | v0.14.0 | v0.14.1 |
| pockets/authorization/stores/turso | v0.8.0 | v0.8.1 |
| pockets/authorization/stores/pgx | v0.9.0 | v0.9.1 |
| pockets/authorization/stores/firestore | v0.2.0 | v0.2.1 |

These patch tags establish the requested release from main with coordinated
pins; no new feature or performance improvement is claimed. All existing release
exceptions remain disclosed. No authentication/SDK/Redis-adapter rerelease is
required solely for this cache train.


## Execution evidence

- Named backend lead confirmed whole-tree equality and ancestor relation.
- Merge commit `dda6699debb17544c05723c7a41ec4e17255a5ce` has the exact main
  commit as second parent; its tree matches its first parent. The only difference
  from the prior cache branch is this new plan. No production adaptation is needed.
  Evidence: `/private/tmp/authorization-cache-main-merge-evidence.json`.
- Original release public verification is now recorded as passed in its manifest.
  The source/checksum results do not waive the documented cloud/performance gaps.

- All six new candidate artifacts passed isolated tidy, versioned dependency
  graphs, build/test/vet and both integration-only and integration/live
  compilation. No new versions were queried on public services before tagging.
  Evidence: `/private/tmp/authorization-cache-main-candidate-20260914/results.json`.
- Full framework source comparison against the prior published release shows
  only six store go.mod/go.sum changes. All production algorithms, migrations,
  HTTP handlers, fixtures and test source are unchanged, so prior successful
  owned live/race/multi-process/mounted-HTTP checks remain applicable. The
  unchanged performance matrix is not rerun or relabeled as passed.
- [Main release manifest](authorization-cache-main-release-manifest.json) records
  module versions/checksums and merge evidence.

- `make check` passed all 42 modules, generated-file checks, tagged-test
  compilation and unchanged architecture guards using the candidate proxy.
  Log: `/private/tmp/authorization-cache-main-make-check.log`.
- Remote main was rechecked at `437da308`; integration will use a normal
  fast-forward push, never force. Source inventory is checked again before tags.


## Publication from main

- Verified integration commit `5aa0b29e23f5fd411bdb40b3a7abaea4fa531a1a`
  was fast-forwarded to remote main before any new patch tag was created.
- All six annotated patch tags point to that exact main commit. Complete cache
  ancestry is included; all pre-existing remote tags were verified unchanged.
  Evidence: `/private/tmp/authorization-cache-main-publication-20260914/remote-published.json`.
- The user's usual checkout now tracks main at the release commit. Original
  owner plan/handoff contents were verified preserved after switching branches.
- Standard public checksum/consumer verification is pending proxy propagation;
  no checksum bypass or tag replacement was used. Prior local/candidate checks
  remain passed; this public gate is recorded separately.
