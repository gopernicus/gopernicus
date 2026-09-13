# Firestore reconciliation and first releases

Status: v0.1.0 VERIFIED, PUBLICATION PENDING; REAL GCP SUITE UNTESTED — 2026-09-13. Owner explicitly requested releasing Firestore
following the coordinated framework release and Segovia adoption.

## Unsuffixed release and GPS-360-Go handoff — 2026-09-13

After the beta publication and disclosure of the untested real GCP suite, the
owner instructed: "go ahead and release", then requested a handoff prompt for
upgrading all applicable Gopernicus packages in GPS-360-Go through AUDIT.md.
Proceed with unsuffixed `v0.1.0` for the three Firestore modules, explicitly
retaining the known GCP verification gap. This authorization supersedes the
earlier live-gate requirement for this first release; it does not turn any unrun
test into a pass or alter runtime/workflow safeguards. Preserve the beta tags.

1. Change the two stores' connector pins to v0.1.0; update module READMEs,
   schema/release notes and manifests to describe the actual verification scope.
2. Rebuild isolated v0.1.0 candidates, update store checksums in dependency order,
   and run replacement-free build/test/vet/live compile/vet plus the full
   workspace check. Reuse today's emulator race evidence only after confirming
   all runtime/test/index source is unchanged; do not rerun unchanged behavior
   without a new concern. Temporary root workspace bootstrap is pre-tag only.
3. Review and publish all three immutable v0.1.0 tags in dependency order; verify
   public checksums/source/builds with GOWORK=off and no exemptions/replacements.
   Remove the bootstrap, update evidence and push the release branch. Preserve
   remote main and all consumer repositories.
4. Inspect `/Users/jrazmi/code/gps/three-sixty/gps-360-go` read-only and write
   `plans/gps-360-go-audit-upgrade-handoff.md`. Use authoritative release manifests
   and all AUDIT-001–034 entries, including superseding decisions. Include the
   target's actual dependencies, current branch state, migration/behavior checks
   and exact final release versions. This turn prepares the prompt, not consumer
   changes or deployment.

### v0.1.0 verification complete

- Stable candidate archives passed source/ZIP/checksum matching, independent
  graph/build/test/vet and live compile/vet with GOWORK=off and no replacements.
  All 176 entries match final source; only unpublished Firestore paths have
  candidate checksum exemptions. Published SDK/core checksums remain verified.
- Full `make check` with final v0.1.0 pins passed in 43.3 seconds, including all
  42 modules, generation consistency, tagged vet and guards, with no source drift.
  All 25 workflow helper tests passed after final comment changes.
- Today's beta emulator race evidence is reused after proving all runtime,
  tests and index source bytes identical. Source changes are README/SCHEMA and
  version pins/sums only. No emulator was restarted and no GCP suite was run.
- Named platform-sre reviewed the release exception, docs, pins, candidate tool
  and publisher: no blocker. Publisher preserves all 34 audit tags and the exact
  beta tag objects/targets, checks the clean release commit and final evidence,
  publishes in dependency order and verifies public checksums without exemptions.
- Final stable evidence is under `/tmp/firestore-release-20260913/stable` and
  in the tracked manifest/verification record. GPS-360-Go remains untouched;
  the prompt is being prepared from its local and remote-tracking inventories.

## Completed beta publication — 2026-09-13

- All three annotated `v0.1.0-beta.1` tags are published at
  `973f94322a3a99b8699df8f266c33539c0c21ed8`, in connector, authorization,
  authentication order. Each tag annotation and module README explicitly state
  that the real GCP suite has not run. Unsuffixed v0.1.0 remains live-gated.
- Fresh public Firestore downloads match all candidate ZIP/go.mod checksums and
  all 176 source entries. Independent graph/build/test/vet and live compile/vet
  pass with `GOWORK=off`, `proxy.golang.org`, `sum.golang.org`, no replacements,
  and no checksum exemptions. Third-party dependencies use cached public downloads.
- Root `go.work` no longer has the connector bootstrap. The canonical workspace
  resolves through the public proxy: 348 modules, zero replacements. No module
  source changed after candidate verification. The owned emulator is stopped.
- Exact tags, tag-object hashes, public sums and current verification outcomes
  are persisted in `firestore-release-manifest.json` and
  `firestore-release-verification.json`. Temporary detailed evidence:
  `/tmp/firestore-release-20260913/{publication,public-v1}`.
- The release branch is published; remote `main` stays at `d97dfddd` and consumers
  are unchanged. Remaining separately scoped work: default-branch integration,
  real GCP verification before unsuffixed v0.1.0, and the core/SQL credential
  replacement follow-up before the next authentication patch. The prior recovery
  and v0.1.0 preparation records below are historical; do not repeat beta publication.

## Emulator release preparation — 2026-09-13

The owner asked to use emulator verification and publish either a prerelease or
a release explicitly documenting that the real GCP suite has not run. The
target is `v0.1.0-beta.1` for all three modules, the recommended default stated
while awaiting an optional version preference. This supersedes waiting for GCP
configuration as the only useful next step. RELEASING.md now permits this beta
after emulator verification; unsuffixed `v0.1.0` retains the real GCP gate.
Live tests must continue to report their actual status.

1. Recreate an isolated emulator using the workflow's pinned image. Run all three
   modules' build/test/vet, integration race suites and live compile/vet checks.
   Audit failures and skips; only authorization's documented ambient family may
   skip. Keep the real GCP suite explicitly unrun.
2. Prepare the selected module versions and connector pins, update release notes
   and module READMEs with emulator coverage and unverified GCP indexes, Admin API
   probing, contention/isolation and backend limits. Preserve runtime safeguards.
3. Rebuild independent candidate archives without module replacements, record
   fresh hashes/evidence in tracked release records, and review the final diff.
4. Publish the three selected tags in dependency order, verify public module
   resolution/checksums, remove the workspace bootstrap after connector
   availability, and stop the owned emulator. Preserve remote `main` and consumers.

### Beta verification complete

- Durable evidence: [firestore-release-verification.json](firestore-release-verification.json).
  All three module build/test/vet and live compile/vet checks passed. Emulator
  race suites passed: connector 331, authorization 361, authentication 422 test
  outcomes; no failures, only authorization's documented `TestRunTransactional`
  skip. Runtime/test sources and index manifests are unchanged since these runs;
  subsequent module edits affect only README/SCHEMA documentation and beta pins/sums.
- The full 42-module `make check` passed in the actual checkout in 132.5 seconds,
  including generation consistency, tagged vet and repository guards; no source
  files changed. The 25 workflow helper tests passed after the comment updates.
- All three independent beta candidates passed `GOWORK=off` download/source
  comparison, build/test/vet and live compile/vet without replacements. All 176
  archive entries match checkout plus inherited licenses. Firestore downloads
  are fresh; public third-party dependencies use cached downloads. Only the
  unpublished Firestore paths have candidate checksum exemptions. Final sums and
  source inventory hashes are in the release manifest; old v0.1.0 sums do not apply.
- Named platform-sre review found no policy or candidate-tool blocker. Beta
  READMEs and tag annotations disclose the unrun GCP suite; unsuffixed v0.1.0
  retains its required live gate. Workflow changes are comments only.
- Owned emulator `43b3397c4d9c29817f5ceaef9eee1b6c7585f12d51a4abd8e9d6a89ebbe5c523`
  (`gopernicus-firestore-release-20260913`, pinned image, loopback port 49913)
  was stopped after the tests. Existing user containers were not reset or stopped.
- All logs/scripts are under `/tmp/firestore-release-20260913`; durable command
  outcomes, source/hash summaries and raw-log hashes are in the tracked evidence.
  The separate core/SQL credential-replacement follow-up remains outstanding.

## Session recovery — 2026-09-13

- The checkout was clean on `firestore-release-20260911` at
  `11558785a8dcd8686bc5caa7a9d2144bee174809`, matching the remote branch. The merge
  is complete, with parents `c98f5618` and `1c0f06d9`; earlier merge-in-progress
  and commit/push instructions below are historical.
- Read-only `git ls-remote origin` verification confirms all 34 tags in
  `audit-release-manifest.json` point to
  `c3f8b4ad453021ba1e40c91572d8e4618c2d8382`. The broad framework refactor is
  published; do not repeat or move those tags. No Firestore tag exists remotely.
- Independent named platform-sre review compared tracked source ownership using
  the nearest `go.mod`, excluding nested modules. No file owned by any of the
  34 published modules differs between the release commit and `11558785`.
  All 76 subsequent changed paths are held Firestore sources or repository-level
  CI/docs/plans/workspace files; no additional non-Firestore module release is
  needed for the current checkout.
- Remote `main` remains at `d97dfddd116c2d06c6960880333bf92aaa04714e`.
  Module publication and default-branch integration are separate: the older
  Firestore PRs #47, #48 and #49 remain open, and neither release branch has a
  PR into `main`. The prior release plan preserved `main` because updates can
  deploy documentation. Preserve that boundary until integration is agreed.
- `gh run list --workflow live-stores.yml` shows no run for this release branch.
  `gh secret list --json name` and `gh variable list --json name` both return
  empty lists. The dedicated GCP test project/region and test credential location
  were requested again. Do not dispatch the required gate with missing config.
- Segovia remains on local `chore/gopernicus-audit-upgrade` at `9db497fd`, with
  its two owner plans untracked. Its recorded adoption is intact; no consumer
  files were changed during this recovery.
- The previous `/tmp` candidate directory, publication helper, Go cache, test
  logs, independent review and credential-ownership follow-up file no longer
  exist. Tracked manifests and plans retain hashes and historical results;
  this recovery did not rerun builds, tests, public downloads or live checks.
  Recreate candidate verification and required evidence before first-tag
  publication. Reconstruct the separate core/SQL credential-replacement finding
  from source before its next authentication patch; its original temporary
  report cannot be relied on as an available artifact.
- Recovery edits are limited to this plan, `framework-audit.md`, and the stale
  publication status in `audit-release-manifest.json`. No module code or pins
  changed. Verification: remote refs against every manifest tag, GitHub PR/run/
  configuration inventories, branch/consumer status, JSON parsing and
  `git diff --check`.
- Resume with the required live workflow once test configuration is available,
  archive the tested commit/run/artifact, then publish the three manifest tags
  in dependency order and verify public checksums. Remove the root `go.work`
  bootstrap only after the connector is publicly available. Main-branch
  integration remains outstanding, separately from the already published audit.

## Preconditions and scope

- Baseline audit-release-20260911 at c98f5618, clean. SDK/core tags already immutable.
  Remote firestore-authentication at 1c0f06d93b64288f20f5da16d68d81249222fade adds
  six commits after common ancestor 6807ed06 (65 files, primarily completed
  authentication adapter). Preserve that history and the audited contracts.
- Work on firestore-release-20260911. Reconcile via a reviewed merge; preserve remote
  main and original Firestore branch. No consumer or production changes.
- Publish first v0.1.0 tags for connector, authorization store and authentication
  store in dependency order, only after corresponding gates pass. SDK v0.9.0,
  authentication v0.11.0 and authorization v0.13.0 stay authoritative.
- Every Go/make command: GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache.
  Formatter /Users/jrazmi/go/bin/goimports. Never hand-edit generated files.
- Named implementer and lead-backend-engineer roles apply current ARCHITECTURE.md;
  legacy opus selection is unavailable, so roles inherit the current runtime model.
  Current root SDK/errors and public logic package contracts override stale role text.

## Tasks

1. Root merge/import inventory; authentication implementer reconciles completed
   remote adapter with audited proof, ownership, invitations and service contracts.
   No obsolete receipt/scopes APIs or incomplete ports hidden by compilation.
2. CI implementer reconciles live-stores workflow and complete index manifests,
   emulator/live matrices and skip auditing for all three current modules.
3. Root validates connector + authorization reconciliation, isolated emulator
   behavior, required build/test/vet/race and relevant workspace guards. Review
   authentication integration failures against current shared store conformance.
4. Independent review covers transaction read-before-write, claims/revocation,
   proof consumption, rollback/committed outcomes and live index coverage.
5. Required real Firestore gate: repository RELEASING.md requires a passing
   live-stores dispatch with firestore_live_required=true and archived run ID /
   firestore-live-evidence artifact; an emulator does not prove deployed indexes
   or real contention. Repo secret/variable inventories are empty. Asked owner
   which GCP project/region to use; continue all independent work meanwhile.
   Never use (default), inherited production targets or delete a preexisting DB.
6. Update module pins/remove pre-tag replacements, verify isolated candidate
   archives, commit/push release branch and publish dependency-ordered immutable
   tags when gates pass. Verify public checksums/builds with GOWORK=off and no
   replacements. Update AUDIT.md, RELEASING.md and master plan with actual status.

## Decisions and known family constraints

Firestore adapters reject ambient host transactions; SQL alone supplies the
cross-repository ambient-join contract. Preserve loud named allowed skips, write
ceilings, bounded query behavior, host-owned index deployment and explicit index
probe opt-out. Current host relationship model remains authoritative. Authentication
must satisfy every implemented current store port, including audited invitation
acceptance and sensitive proof binding/consumption; do not release a stubbed adapter.

## Evidence and continuation

Remote inventory is fetched; no Firestore tags exist. CI secret names and variables
both empty. Live project/credential configuration remains pending owner input.
Record merge conflicts, exact changed paths, commands, failures, review results,
live run/artifact and final tag hashes here as work proceeds.


### Reconciliation and owned emulator

- Merge in progress: `git merge --no-commit --no-ff origin/firestore-authentication`
  from `c98f5618`. Root retains audited authorization tuple/audit storage and
  resolves its docs/live fixture conflicts; authentication implementer owns that
  adapter; CI implementer owns workflow and skip-audit helper. Stage only after
  reviewing resolved text and tests. No tags or release branch published yet.
- Isolated emulator container `gopernicus-firestore-release-20260911`, ID
  `78862bc5596d6947c7f0c6862ba74d67d0a4d0d8f3a82b86bd98754dc2fb5b55`, image
  `sha256:45a15cc163d2df13137478856b4b9bb90426100fa8aec542269460c10110b727`.
  Endpoint `127.0.0.1:53174`, project `gopernicus-release-test`. Only this owned
  fixture may be reset/stopped; existing emulators on 8080 and 8081 are untouched.
- Connector and authorization build/test/vet plus emulator race tests running
  with the canonical workspace, published-v2 module cache and fixed Go cache.
  Logs: `/tmp/firestore-release-connector-race.log` and
  `/tmp/firestore-release-authorization-race.log`.
- Independent review is checking current proof/credential contracts against
  remote claim defenses. Preserve both; neither branch alone is release-ready.
- Root refreshed the unreleased Firestore notes in RELEASING.md: current pins,
  no obsolete scopes/receipts/tuple IDs or 500-document ceiling, and explicit
  live evidence requirement. Documentation does not claim pending tests passed.


### Connector and authorization results

- Both modules passed `go build ./...`, `go test ./...`, `go vet ./...` and
  `go test -race -p 1 -tags=integration -count=1 -timeout=10m -json ./...` on the
  owned emulator. Connector: 331 passed test outcomes, zero skips/failures;
  authorization: 361 passed, exactly `TestRunTransactional` skipped (the known
  unsupported ambient join family), zero failures. Counts include parent tests.
- Both `go test -tags='integration,live' -run '^$' ./...` and
  `go vet -tags='integration,live' ./...` passed: live paths compile; this is
  not a live database verdict.
- Isolated candidate preparation and verification passed for connector and
  authorization. `/tmp/firestore-release-candidates.py` snapshots module sources,
  runs GOWORK=off tidy/build/test/vet and tagged compile, creates standard Go
  module ZIPs, and compares every archive entry to the checkout plus inherited
  root license. SDK/core dependencies retain public checksum verification;
  only the three unpublished Firestore module paths use candidate exemptions.
- Candidate evidence: `/tmp/firestore-release-candidates/{connector,authorization}.log`,
  `results.json`, `environment.json`, local proxy and source snapshots. Hashes
  and 46/58 matching source-entry counts are in `firestore-release-manifest.json`.
  Authorization's temporary connector replacement is now removed and its go.sum
  includes the exact candidate connector hashes. No public tags were created.
- Authentication's first full emulator run exposed ten outdated remote fixtures
  (new proof bindings/active-owner rules). Implementer is correcting those
  setups while retaining the original claim, rollback and corruption assertions.
  Current shared suite and final independent implementation review remain to be
  reported. No failures are waived by the candidate checks above.


### Workflow reconciliation

- CI report: `/tmp/gopernicus-firestore-ci-report.md`. The workflow now deploys
  all three store manifests (including authorization audit) plus connector live
  fixtures: 80 distinct composites and 88 field overrides, with no conflicting
  field requests. Readiness matches each requested identity/configuration, not
  a count of unrelated READY indexes.
- Five standard-library Python helpers/tests under `.github/scripts` cover
  index readiness, source-derived/package-aware live root auditing, and the
  workflow's actual shell configuration/provision/cleanup paths. All 25 tests
  pass; YAML and all 22 shell steps parse. Synthetic command-line audits cover
  current 11/10/4 connector/authorization/authentication live-root inventories.
- Required runs reject missing credentials and any pinned database before GCP
  actions. Disposable database IDs include run, attempt and a random suffix;
  ownership is recorded only after successful create. Cleanup requires matching
  recorded ownership. A failed/ambiguous create is reported for manual inspection,
  never treated as permission to delete a possibly preexisting database.
- Root's `make guard` passed (`/tmp/firestore-release-guards.log`). Root reran the
  25 Python tests successfully. No cloud workflow was dispatched; repository
  credentials/project configuration and the release gate remain pending.


### Separate follow-up discovered during review

Read-only ownership review found a current core/SQL identifier-replacement
validation gap beyond this new Firestore adapter. The direct Firestore mutation
path is being fixed and tested in this release. Existing published core/SQL
versions are not changed here. Detailed call-path evidence and proposed regression
coverage: `/tmp/firestore-credential-ownership-followup.md`; review it before the
next authentication patch. Do not lose this follow-up when handing off context.


### Authentication verification and pre-tag workspace bootstrap

- Authentication's completed shared conformance passed all 217 leaf cases.
  Full adapter emulator race run passed: 422 pass outcomes / 382 leaves,
  zero failures/skips, including proof bindings, admission/revocation, ownership,
  claims and rollback. Log: `/tmp/firestore-authn-integration-race-final.jsonl`.
- Hermetic build/test/vet, integration vet and live compile/vet passed. Initial
  isolated authentication candidate also passed against the published core/SDK;
  that archive is superseded by final invitation claim/timestamp test refinements
  and must be rebuilt before recording final hashes. No tag was published.
- Removing both module replacements exposed a real first-tag bootstrap failure:
  even the canonical workspace with a pure public proxy requested the unpublished
  connector's v0.1.0 go.mod and received404. Added a version-specific replacement
  ONLY in root go.work so CI can test the candidate dependency graph before its
  first tag. Module go.mod files remain replacement-free; go.work is excluded
  from Go module archives. Remove this workspace bootstrap after public connector
  verification. This is distinct from the isolated candidate proxy used to prove
  independent consumer resolution.
- Updated root release/audit notes to distinguish SQL authorization baseline
  ambient joins from guarded authorization/authentication transaction boundaries.
  Merely choosing SQL does not make every pocket composition join a host transaction.


### Final authentication review

- Final implementer report `/tmp/firestore-authn-reconciliation-report.md`; exact
  55 changed paths `/tmp/firestore-authn-reconciliation-files.txt`; final module
  hash inventory `/tmp/firestore-authn-source-manifest.json` (71 paths).
- Independent review approved the adapter reconciliation with no remaining code
  blocker: `/tmp/firestore-authentication-final-review.md`, reviewed production
  hashes `/tmp/firestore-authentication-final-reviewed-source.json`. The full race
  run has 382 passing leaves, including 217 shared leaves; zero failure/skip.
- Final invitation timestamp refinement returns the persisted microsecond value.
  Its focused invitation/physical-claim/lifecycle audit race run passed 32 leaves,
  zero failure/skip (`/tmp/firestore-authn-acceptance-race-final.jsonl`). Final
  build/test/vet, integration vet and live compile/vet all pass:
  `/tmp/firestore-authn-hermetic-final.{json,log}`. All agent test processes ended.
- Rebuilt the authentication candidate after these refinements. Moving the old
  Go cache entry failed on Go's read-only cache directories, so stale initial
  download hashes are not final evidence. Final verification uses a fresh
  `/tmp/firestore-release-candidates/download-cache-final`, compares downloaded
  ZIP bytes to the rebuilt proxy and every source entry, and runs independent
  build/test/vet on the downloaded modules. Only final-results.json and the final
  release manifest hashes should be used for publication, not initial results.json.
- Full final make check is running on a frozen source snapshot:
  `/tmp/firestore-release-final-check`, source hashes
  `/tmp/firestore-release-final-check-source.json`, log
  `/tmp/firestore-release-final-check.log`. Root go.work bootstrap compiles against
  the pure public proxy without candidate credentials/exemptions; live resources
  remain entirely unconfigured and no Firestore tags have been cut.


### Final local gate and continuation

- All three final candidate downloads passed independent `GOWORK=off`
  build/test/vet without module replacements. Downloaded ZIP bytes match the
  final proxy artifacts and all 176 source entries match the checkout (including
  inherited licenses). Final hashes: `firestore-release-manifest.json` and
  `/tmp/firestore-release-candidates/final-results.json`; log
  `/tmp/firestore-release-candidates/final-download-verification.log`.
- Final frozen-source 42-module `make check` PASSED, including generation
  consistency and integration/live-tag vet. Gitless snapshot guards emitted their
  known Git-unavailable message; the full `make guard` was also run in the actual
  Git checkout and PASSED (`/tmp/firestore-release-final-guards.log`). All 25
  workflow helper tests passed again (`/tmp/firestore-release-workflow-tests.log`).
  No executable source changed after the check snapshot; only release-plan
  evidence was appended. Browser/provider suites outside Firestore were not
  rerun because their sources did not change in this reconciliation.
- All merge conflicts are resolved and the 74-file changed inventory is saved in
  `/tmp/firestore-release-changed-files.txt`. The staged diff passes whitespace
  checks. Keep both merge parents; preserve existing remote main and Firestore
  source branch. Commit/push the prepared release branch after final review.
- Cleanup complete: the owned emulator ID was checked before stopping
  `gopernicus-firestore-release-20260911`; its `--rm` container was removed.
  Existing user containers/databases were untouched. No test services remain
  running for this task.
- Outstanding release requirement: the repository has no Firestore CI project or
  credentials configured. The owner must identify the test GCP project/region
  and make test credentials available. The asynchronous question remains unanswered.
  Configure `FIRESTORE_LIVE_PROJECT_ID`, `FIRESTORE_LIVE_CREDENTIALS_B64`, and
  optionally `FIRESTORE_LIVE_LOCATION`; do not set a database PIN for a required run.
- Then dispatch `gh workflow run live-stores.yml --ref firestore-release-20260911
  -f firestore_live_required=true`. Inspect the actual run, all three live audits,
  ready indexes and cleanup; record run ID, tested commit and
  `firestore-live-evidence-<run id>-<attempt>`. Fix failures without weakening
  the gate. Only after success publish the three v0.1.0 tags in manifest order.
- Verify each public ZIP/go.mod sum against the final manifest and build with
  `GOWORK=off`, a fresh cache, the public proxy and ordinary checksum verification.
  Remove the temporary connector override from root go.work after publication;
  update release status and evidence. No tag, GCP mutation or live run has happened
  during local preparation. Separately review the existing core/SQL credential
  replacement follow-up before the next authentication patch.
