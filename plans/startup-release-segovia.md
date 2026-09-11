# Startup cleanup, coordinated release and Segovia v2 adoption

Status: IN PROGRESS — 2026-09-11. The owner approved the sequence recorded in
framework-audit.md: bounded startup/configuration cleanup, release preparation,
commit/push/module publication, then Segovia v2 adoption. CMS behavioral work and
production deployment remain outside scope.

## Scope and decisions

- Carry the host context as the first parameter of pgxdb.Open, turso.Open and
  authentication/authorization Firestore probing constructors. Honor cancellation
  before I/O and during startup ping/retry/index probes. Retain configured timeout
  bounds and cleanup; construction does not own or retain the host context for
  the lifetime of the returned database/store. Migrate owned callers and templates.
- Return configuration errors from SendGrid.New and borrowed-client S3.New;
  reject known-invalid inputs before returning a usable component. Preserve
  transport policy, caller ownership and all provider/send behavior. Do not add
  connection probes to pure constructors.
- Make outbox.NewPoller reject missing repository/delivery at construction,
  including typed-nil repositories; use an error result for caller-handled wiring
  validation. Preserve delivery-before-mark, cancellation and replay semantics.
- Review raw store wrappers' nil database precondition. Preserve their simple
  return signatures and make programmer errors explicit where missing, rather
  than imposing error returns on every wrapper. Leave MultiQueryTracer/value
  constructor redesigns deferred.
- Record new migrations as AUDIT-032; keep prior audit records historical.

## Tasks

1. Implement startup cancellation and constructor validation; migrate owned
   callers/docs/templates, exercise canceled startup and valid runtime behavior,
   and complete affected build/test/vet, race and workspace checks.
2. Inventory publishable modules and remote tag/branch state, choose compatible
   version bumps and dependency order, update sibling and scaffold pins, reconcile
   migration notes and verify without workspace/local replacements. Inspect live
   release requirements; do not claim skipped cloud checks passed or silently
   release incomplete first-tag modules that require unsatisfied gates.
3. Commit the reviewed audit changes and release metadata on a new release branch,
   and publish the selected immutable module tags in dependency order. Avoid an
   incidental merge/push to main: the docs workflow deploys on main changes. Verify
   remote module resolution with GOWORK=off and no local replacements.
4. Upgrade /Users/jrazmi/code/segovia/segovia/v2 using released tags and AUDIT.md.
   Read its current branch/status and instructions before editing. Preserve v1 and
   user edits, tenant isolation and immutable migration history; export new
   upstream migrations rather than editing existing ledger entries. Use its shared
   .claude/plans location for the host implementation plan. Run v2 make check and
   relevant local runtime/migration checks; no deployment or production mutations.

## Preconditions and ownership

- Framework branch: firestore-authentication, initial HEAD 6807ed06. Extensive
  preexisting audit changes are preserved. Baseline snapshot and manifest:
  /tmp/gopernicus-startup-release-baseline and matching .json; initial status:
  /tmp/gopernicus-startup-release-status.txt.
- Every Go/make command uses GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache.
  Formatter: /Users/jrazmi/go/bin/goimports. No root go.mod; 42 workspace modules.
  Never hand-edit generated artifacts. Use exact-source snapshot make check plus
  actual-repository guards until a committed tree supports generated-drift checks.
- Named implementer roles own SQL startup and Firestore/raw-store follow-ups;
  root owns SendGrid/S3/outbox and coordinated release/host work. The named
  lead-backend-engineer supplies independent read-only release/API review.
  Legacy opus role models are unavailable; use the current runtime model and
  current architecture/owner decisions where old role examples are stale.
- Use isolated disposable services for behavioral checks; never consume inherited
  production/provider test environment implicitly. Loopback, git and consumer
  path writes may require sandbox escalation. Current approval covers this
  sequence; routine escalation is not a new request for user authorization.

## Verification, changed files and continuation

- Remote inspection: /tmp/gopernicus-release-remote-refs.txt. No remote tags are
  missing locally. origin/firestore-authentication is 1c0f06d9, six commits beyond
  initial local HEAD; 24 newly added remote Firestore files are absent locally.
  Preserve that remote branch untouched. Release the SQL/core/other compatible
  modules from a new audit release branch; defer Firestore reconciliation and its
  three first tags. Required real Firestore live gate remains unsatisfied. This
  does not block Segovia's PostgreSQL graph.
- Independent lead-backend-engineer review:
  /tmp/gopernicus-release-backend-review.md; inventory and proposed order:
  /tmp/gopernicus-release-module-inventory.json and
  /tmp/gopernicus-release-order.json. Borrowed S3.New validates nil client/bucket,
  while signing policy remains with the supplied client; Open retains its region
  check. No remote state has changed yet.
- Root local constructor tests initially hit the expected sandbox loopback
  restriction. Re-run script /tmp/gopernicus-startup-root-check.py clears live
  provider settings and uses approved local listeners; log is
  /tmp/gopernicus-startup-root-check.log. Completion pending.

Pending: exact changed-file inventories, remaining commands, release tags/commits,
consumer branch, failures and unverified provider behavior before handoff.


### Release verification progress

- Selected versions and dependency layers: plans/audit-release-manifest.json
  (34 modules; three Firestore modules held). Branch created:
  audit-release-20260911. Remote Firestore and main are unchanged.
- All 34 candidate module graphs passed isolated GOWORK=off tidy/build/test/vet;
  Go module ZIP tooling excludes nested modules and inherits the repository
  license. Candidate proxy/cache/source and logs:
  /tmp/gopernicus-release-candidates. Sibling manifests and example overrides are
  updated; the remaining eight development modules were tidied separately.
- 1,736 candidate archive entries matched repository source exactly before the
  later events PostgreSQL decoy-fixture correction; rebuild that one archive
  and recheck before tagging. No provider implementation changed for that fix.
- Final 42-module snapshot make check and actual-repository guards PASSED:
  /tmp/gopernicus-release-final-check.log and
  /tmp/gopernicus-release-final-guard.log. The first runner attempt used an
  explicit /tmp workspace path which differed from its canonical /private/tmp
  working directory; automatic discovery fixed the runner, with unchanged source.
  Initial log: /tmp/gopernicus-release-final-check-workspace-path-failure.log.
- UI release gate PASSED: all 441 Playwright/axe cases across Chromium, Firefox
  and WebKit, no retries/skips reported; /tmp/gopernicus-release-ui-browser.log.
  Documentation typecheck/build PASSED; /tmp/gopernicus-release-docs-{typecheck,build}.log.
- Generated released-pin hosts and pocket/store modules build/test/vet without
  framework replacements; freshly scaffolded no-database host health/shutdown
  smoke PASSED. Evidence: /tmp/gopernicus-release-scaffold-report.md and logs.
- Independent startup review is ship-ready with no blocking correctness issues:
  /tmp/gopernicus-startup-final-review.md. Source review hashes accompany it.
- Live SQL/Redis verification is in progress. It found one stale PostgreSQL
  events decoy test fixture still using JSON instead of the current BYTEA schema;
  align the fixture with migration0002 and rerun both schema variants before
  publication. Preserve the original failure log and report the correction.
- Segovia main76b3d783 equals remote main. Its only local additions are the
  untracked .claude/plans/v2-go-live.md and v2-vendors.md; preserve them and v1.

- A live SQLite jobs concurrency failure blocked publication: QueryOne discarded
  a final rows.Close SQLITE_BUSY after scanning UPDATE...RETURNING, allowing an
  uncommitted claim to escape as success. Named SQL implementer is fixing the
  completion boundary and adding a deterministic reader-lock regression. This
  pre-release correctness fix is AUDIT-033; no schema/API signature changes.
  Rebuild the Turso connector and dependent archives/checksums after verification.
- Read-only Segovia migration comparison found five append-only upstream SQL
  additions and no changes to existing exported migration bytes.


### Corrected live gate and final candidates

- Live release verification COMPLETE/PASS:
  /tmp/gopernicus-release-live-final.json and /tmp/gopernicus-release-live-checks.md.
  PostgreSQL default/named schemas, authentication/authorization non-C locale
  proofs, Redis and all six SQLite integration/race suites pass. No fixtures
  remain running. Final SQLite counts: connector173, authentication290,
  authorization239, jobs102, events13, CMS45; zero skipped tests/subtests.
- SQLite regression fails with the original helper and passes after the fix.
  The diagnostic original produced duplicate claims in12/20 repetitions; final
  uninstrumented code passes20/20 with unchanged contention settings/assertions.
  Events PostgreSQL correction passes18 cases in each fresh schema variant.
- Seven affected unpublished archives were rebuilt, including their dependent
  go.sum records. Only candidate-version hashes/cache entries were invalidated;
  external checksums were retained. The first rebuild lacked a sibling SQL
  migration fixture; retaining the full source tree while keeping GOWORK=off
  fixed that verification setup, without weakening its parity test.
- Final candidate source/hash proof:
  /tmp/gopernicus-release-candidate-source-match-final.json and
  /tmp/gopernicus-release-expected-hashes.json. All34 candidate downloads match
  source; expected hashes are persisted in plans/audit-release-manifest.json.
- Final corrected 42-module snapshot/guard check is running under
  /tmp/gopernicus-release-final-check-v2.py. Do not publish until it passes.


### Publication readiness

The corrected final 42-module snapshot make check and actual-repository guards
PASSED: /tmp/gopernicus-release-final-check-v2.log and
/tmp/gopernicus-release-final-guard-v2.log. Independent QueryOne review is
ship-ready: /tmp/gopernicus-turso-queryone-final-review.md. All release blockers
found in this pass are resolved; provider limitations and held Firestore releases
remain explicit. Final candidate proof covers1,737 archive entries across34
modules, all matching repository source. UI/browser and documentation evidence
remains applicable; the subsequent production change was only the SQL helper.

Prepared publication command: python3 /tmp/gopernicus-release-publish.py <commit>.
It verifies the branch/clean source and immutable tag targets, preserves remote
main/Firestore, pushes the release branch and dependency-ordered annotated tags,
and records verified remote refs in /tmp/gopernicus-release-publication.json.
After publication, verify real downloaded ZIP sums against the persisted expected
hashes without the candidate proxy/cache or checksum exemptions, then adopt Segovia.
