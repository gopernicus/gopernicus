# Firestore reconciliation and first releases

Status: LOCAL PREPARATION COMPLETE; REQUIRED LIVE GATE PENDING — 2026-09-11. Owner explicitly requested releasing Firestore
following the coordinated framework release and Segovia adoption.

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
