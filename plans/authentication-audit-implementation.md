# Authentication audit implementation

Status: COMPLETE — 2026-09-10. Owner authorized working through all recommendations after the
[authentication audit](framework-audit-authentication.md) walkthrough. This
supersedes the earlier no-implementation restriction for authentication only.
Authorization implementation and CMS remain deferred.

All ten authentication findings and the accepted completeness/simplification work
are implemented and verified. Consumer migration: [AUDIT-022](../AUDIT.md#audit-022-authentication-proof-lifecycle-and-host-api).
For the next pocket, start with [the authorization audit](framework-audit-authorization.md)
and [listing design](authorization-listing-design.md); implementation is not yet authorized.

## Preconditions and scope

- Branch firestore-authentication / 6807ed06; large pre-existing audit diff.
  `/tmp/gopernicus-authentication-implementation-baseline.json` records 2054 files;
  `/tmp/gopernicus-authentication-implementation-AUDIT-before.md` preserves prior
  migration entries. Preserve every earlier/user change. No release/commit or
  external consumer mutations.
- SDK stdlib-only, datastore-free pocket, host-owned password strength, OAuth
  trust, authorization, enabled capabilities and delivery lifecycle remain.
- Use named implementer roles for bounded concurrent slices and backend/verifier
  roles for review. Current architecture overrides stale paths in role files.
  Configured opus unavailable; inherited models preserve role behavior.
- Formatter: /Users/jrazmi/go/bin/goimports. Go 1.26.1, 42 workspace modules,
  no root go.mod. GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache.
- Live tests use newly owned disposable loopback fixtures; no application DBs,
  external providers, email delivery or deployed index changes. Firestore feature
  completion is a separate milestone; preserve/upgrade implemented capabilities
  and document unimplemented ones explicitly.

## Decisions and acceptance

1. Credential/session proof is checked atomically against the credential state
   it verified. Reuse auth_revision where suitable; no non-atomic recheck or
   optional insecure fallback. Password mutations/provisioning use focused
   atomic store operations. Preserve active-user fencing. Explain mixed-version
   rollout and existing-session behavior before declaring migrations complete.
2. Email-derived invitation grants require verified ownership independently of
   RequireVerifiedEmail. Acceptance claims the current pending token/state before
   invoking the host Granter; duplicate/retry execution uses the same operation
   identity. Cancellation after claim conflicts, never pretends to revoke a
   grant in progress. Resend invalidates an unclaimed old token. Use a focused
   durable state machine with retry/finalization, no general workflow engine.
3. Logout requires verified access or refresh proof; expired access-only logout
   asks for refresh proof. Missing/already-revoked credentials remain idempotent;
   datastore revocation failures propagate. Browser cookies clear independently.
   JSON login/cookie refresh enforce existing Origin policy, native no-Origin
   flows remain supported. No SDK signer API expansion unless needed.
4. Challenge issuance/delivery share one generation and purpose/binding, including
   concurrent starts. Reuse existing delivery intent/generation mechanisms where
   possible. Recent-auth grants enforce session liveness, age and assurance with
   one policy evaluator; preserve honest method metadata and one-use proof.
   PostgreSQL Replace becomes atomic; abuse budgets are host configurable.
5. Boot checks match enabled features, validate once, snapshot host collections,
   and reject missing required collaborators before requests can mutate/panic.
   Production cookie sessions require Secure, development can use HTTP.
   Expose a small typed public service surface for existing passwordless,
   credential and step-up use cases; preserve trusted-host semantics. Consolidate
   duplicate logic, not domains/modules. SDK auth/query promotion is out of scope.
6. Correct memory ownership and transaction documentation; explicitly inventory
   Firestore capabilities. Do not claim all-feature Firestore parity from its
   default unit tests. All affected implemented adapters must compile and satisfy
   the stronger contracts; custom-store breaks go in root AUDIT when implemented.

## Execution packets

- [x] A — credential/session admission and atomic provisioning (AUTH-01/10).
- [x] B — invitation proof and conditional lifecycle (AUTH-02/09).
- [x] C — logout and browser establishment (AUTH-03/04).
- [x] D — challenge generation, recent-auth policy, SQL replacement (AUTH-05/06/07).
- [x] E — enabled-feature validation, configuration ownership, public API,
  shared proof logic and abuse budgets (AUTH-08 and additional findings).
- [x] F — adapter/reference ownership, supported-capability and transaction docs,
  host adoption/migration guide; isolated live conformance and real HTTP/browser
  verification where practical.
- [x] G — independent review, full make check, relevant race tests, docs checks,
  cleanup of owned fixtures, exact changed-file inventory and final handoff.

Each packet must record source changes, meaningful regressions, commands/results
and any remaining limits here. Temporary audit probes are before-fix evidence;
new maintained tests must assert the corrected invariant.

## Coordination and progress

Root owns public Config/facade, Logout and HTTP protection, global verification,
documentation/migrations. Credential implementer owns credential/session/service
mutation and provisioning paths; invitation implementer owns invitation domain,
service and stores; challenge implementer owns issuance/delivery/step-up and
challenge/grant stores. Shared files use disjoint method edits and explicit
messages; never replace an entire shared file from a stale read. Notify root of
needed Config fields or public wiring rather than editing its section.

The progress entries below preserve the execution record. Final verification and
remaining limits are recorded at the end of this plan.

## B — invitation lifecycle implementation design

Invitation owner: lead_backend_pocket_review in named implementer role.

- Durable states: pending → accepting → accepted; pending/expired may be
  resent, declined or cancelled through a current-token conditional update.
  Accepting rows retain the active tuple reservation and cannot be cancelled or
  resent. Grant failures keep the claim because a host side effect may already
  have committed; the same token and bound subject resume with invitation ID as
  the stable Granter operation identity. Finalization is idempotent.
- Add ClaimAcceptance / CompleteAcceptance repository operations and persist
  resolved_subject_type beside the existing subject ID. SQL migration 0018 adds
  the column and extends active-tuple uniqueness to accepting. Mixed old/new
  workers must not serve invitation mutations concurrently: old UpdateStatus
  writers cannot honor claims. Host migrations apply before the new binary.
- Email and phone acceptance require the active verified identifier independently
  of login's RequireVerifiedEmail. Automatic resolution verifies ownership before
  claiming; a claimed proof stays bound to the same subject across retries and
  token expiry. Root owns registration callback and verified UserLookup wiring.
- No automatic retry worker is added: explicit acceptance or verified-email
  resolver retries resume durable claims, including after reconstruction. Hosts
  must deduplicate concurrent/repeated Granter calls by OperationID, because the
  grant and invitation stores have no shared transaction. Firestore invitations
  remain explicitly unimplemented, with updated contract-compatible stubs.
- Regressions cover verified proof, stale resend tokens, accept/cancel/resend
  interleaving, distinct subject claims, ambiguous grant/finalize failures, restart
  retry after expiry, and active-tuple reservation; shared live conformance runs
  against isolated PostgreSQL/libSQL fixtures.

### Packet A — credential fence design and progress

- Reuse existing `users.auth_revision`; no schema column or migration is needed.
  Every service session mint requires `ActiveSessions.CreateForActiveUser` with an
  expected revision captured before reading the credential. The optional plain
  session-insert fallback is removed. Existing session rows remain unchanged;
  credential mutation increments the revision and revokes current sessions.
- Add `Users.Provision` for atomic user + primary identifier + initial password or
  provider link. `Passwords.Change` checks expected revision and current hash in
  the same transaction as write/revision/revocation; trusted `Passwords.Set` also
  increments and revokes. `OAuthAccounts.Link` combines conditional linkage and,
  when adopting an unverified address, verification/password removal/revocation.
  Custom stores must implement these stronger methods; there is no fallback.
- PostgreSQL locks the user before credential writes and session admission. Reset
  discovers its challenge without taking a row lock, then locks the user before
  guarded consumption, avoiding password-writer user/challenge lock inversion.
  LibSQL uses its write transaction; Firestore reads before writes and coordinates
  through the same user document while preserving its email directory projection.
- Password Login/IssueToken now share proof logic; the identifier is re-read after
  the revision snapshot. Password changes/initial sets use conditional writes;
  the returned revision fences remint. OAuth provisioning failures cannot orphan
  a user, and a concurrent identical provider registration can converge to login.
- Final proof inventory also binds removal codes, explicit/pending OAuth link
  payloads, passwordless OTP and all existing-owner magic links to their issuing
  revision. Magic-link BindingVersion2 rejects all v1 payloads, including old
  misclassified ownerless forms; genuinely ownerless v2 provisioning preserves
  current-claim semantics. Removal
  binds the recovery identifier; pending adoption requires a current login/recovery
  identifier. Registration Verify captures revision before consume and preserves
  current identifier uses. Stale consumed proof never rebases onto new credentials.
- Maintained races, rollback and ownership tests pass in service/reference/example
  memory. Full PostgreSQL default/named and libSQL conformance passed; supported
  Firestore paths plus all 64 ambient-method checks passed. A deterministic live
  PostgreSQL test proves owner-before-identifier/challenge locking and fails against
  the prior implementation via a /tmp Go overlay. Final adoption predicate tests
  passed across all three persistent families. Final full core/host races passed
  after B's reset binding (host 76.160s). Final v2-format affected core/reference/
  example-memory races and complete PG/libSQL passwordless conformance also passed
  (8.067s/3.916s), with no skips. Root owns the final workspace gate.
- Packet evidence and touched paths: `/tmp/gopernicus-authentication-A-implementation-report.md`
  and `/tmp/gopernicus-authentication-A-changed-files.json`. No A migration; custom
  repository APIs and in-flight proof payloads change. Root owns final AUDIT/rollout.

### Packet B — implementation and verification

- Implemented verified invitation acceptance/resolution, durable bound claim and
  idempotent completion, token-conditional resend/cancel/decline, active tuple
  reservation, detached memory metadata, and 0018 for both SQL dialects. Firestore
  invitation ports remain explicit unsupported methods with compatible signatures.
- Maintained service tests cover verified proof, blocked concurrent mutations,
  stale resend reads, ambiguous grant/finalization errors and reconstructed-service
  recovery after expiry. Shared store tests cover competing claim/cancel, bound
  retries, token/tuple collisions and rejected writes preserving current state.
- PASS invitation memory/service race suites; PASS PostgreSQL/libSQL maintained
  Invitation conformance with race, plus adapter build/vet, on the recorded owned
  fixtures. PASS all inbound authentication tests with race after migrating the
  fixture aggregate and HTTP setup to stronger credential/session/delivery rails.
- Firestore's intermediate sessions document-ownership guard failure was corrected
  by packet A; final unit/guard and supported emulator checks pass. Detailed evidence and
  migration/behavior limits are in
  `/tmp/gopernicus-authentication-invitations-implementation-report.md`.

### Packet D completed evidence

Challenge generation/delivery, recent-auth proof policy and PostgreSQL replacement
are implemented. Root-approved follow-up also scopes identifier-change proof and
queue commands to pending IDs, adds conditional pending consumption, and fixes
ContactChange.Create's PostgreSQL replacement race. No new migrations/workflow
runtime. Custom grant/contact store APIs change; old proof scopes require restart
or expiry at rollout. Superseded provider messages may arrive and reject; queue
admission failure returns an error and requires restart rather than claiming a
DB/queue transaction.

Controlled concurrent service/delivery regressions, reference/example memory,
real PostgreSQL default/named schema and libSQL conformance pass with race checks.
Implemented Firestore grant conformance plus retry/contention checks pass against
the owned emulator; unfinished challenge/contact features were not claimed.
Core + all authentication store modules + example authmem build/vet pass. Root's
abuse helper tests prove independent subject/IP buckets, purpose-shared sensitive
limits, fail-closed failures and invalid partial-limit rejection. Exact files,
commands, results and rollout notes: `/tmp/gopernicus-authentication-D-report.md`.
Root's completed G/F verification and consumer documentation are recorded below.

### Independent credential review follow-up

The owner authorized correcting proof reuse across later credential revisions:
identifier removal/use changes now capture the revision before recent-auth proof
and apply only that revision, and step-up code context includes the issuing
revision and selected verified identifier. The operation/session slot remains
separate from those proof-generation facts. Retained sessions on an inactive
owner cannot satisfy the primary-login shortcut. New maintained regressions cover
credential change after proof and old delivered codes after identifier replacement.
Credential owner separately fixes removal-code and OAuth-link revision binding
and the PostgreSQL passwordless identifier/user lock inversion.

Registration/admin resend now key pre-rendered delivery by the issued challenge
ID. Each public opaque verification resend receives its own admitted command key,
so a late initializer cannot replace the challenge and then lose its checkpoint
to a newer resend. Current proof follows challenge persistence order, not HTTP
request order; old provider messages may still arrive and reject. Budgets remain
in place, and the public request still performs no account lookup or rendering.

Final review also found the add/change confirmation's remaining retry path. Start
now binds its issuing session and credential revision in challenge context alongside
the pending ID. Confirm checks a matching live session before consuming anything,
requires that issuing session/revision, and evaluates/applies only once at the
captured revision. No schema change; older pending flows must restart. Maintained
tests cover changed credentials after proof consumption, changed issuance revision
with a retained session, missing/expired/wrong-owner sessions, a different live
session, and legitimate retry after pre-consumption session rejection.

## Root public/transport verification and review follow-ups

- New public configuration and host use-case tests pass; bundled Goth render tests
  pass after regenerating `stepup_templ.go` from `stepup.templ`. Host regression
  explicitly checks OAuth unlink revokes the old session and requires fresh login.
- Full inbound suite passes with race detector (packet B fixture migration).
  Root's maintained cases cover hostile JSON Origin, native refresh-body logout,
  signed/expired/forged access proof and revocation failures with cookie clearing.
- Real Chromium against an owned loopback auth-cms process passed HTML registration,
  email verification, login and hydration, cookie refresh, hostile-origin form login
  and refresh rejection, and logout/cookie deletion/subsequent hydration denial.
  `/tmp/gopernicus-authentication-browser-evidence.json`, runner
  `/tmp/gopernicus-authentication-browser-check.mjs`, final log
  `/tmp/gopernicus-authentication-browser-check.log`. All accounts and delivery were
  disposable memory/console fixtures. The advertised in-app browser skill was absent
  from its catalog path and local search; reused the repo's existing Playwright
  1.60.0/Chromium installation. Early runner failures were a strict label selector,
  CORS response-observation mismatch and an auth-page CSP blocking the runner's fetch;
  final runner uses inspected inputs, document navigation and the host API page.
- Independent root review in `/tmp/gopernicus-authentication-root-surface-review.md`
  found typed-nil required dependencies and unfinishable optional flow starts.
  Required-port nil checks now share the existing OAuth nil-value logic; contact
  rails fail constructor validation, and credential/identifier starts reject a
  missing completion rail before state changes. Maintained regressions added.
- Public account use-case error aliases and CurrentSessionID complete the host seam;
  an example-host test authenticates through live middleware, reads methods,
  rejects insufficient assurance and obtains password proof using only public API.
- Deeper independent credential review found lock ordering and proof-rebase issues;
  packet A/D owners corrected them before the final workspace gate.

Additional root checks: shared sensitive-operation/forgot-password errors now map to
HTTP 429 rather than generic 500. Maintained HTTP tests exhaust real independent
budgets and cover all new start/proof handlers. Documentation `pnpm typecheck` and
`pnpm build` both passed; only the pre-existing untracked-file date warning remains.
Logs `/tmp/gopernicus-authentication-docs-{typecheck,build}.log`.

Simplification decisions: Login and IssueToken share one password proof path;
constructor validation and nil-dependency checks share one implementation; public
use cases reuse typed inputs rather than copying transport DTOs. Retain the bundled
HTTP adapter's private aggregate interface: merely splitting its declarations into
embedded interfaces would leave every handler on the same dependency and add names
without narrowing an actual consumer. A future handler extraction can introduce
the interface it consumes. The root public facade does not expose that aggregate.
Historical audit plans remain historical evidence, with current implementation
links; no broad comment/package rewrite or memory-store framework was introduced.

Final workspace gate will use a complete working-tree source snapshot in /tmp
without .git. This preserves the large pre-existing dirty tree and exercises the
Makefile's before/after generated-file checksum gate rather than comparing intentional
uncommitted generated changes with HEAD. The snapshot manifest will be compared with
the final working tree; the source snapshot is not a commit or release. Existing UI
asset files are unchanged from the pre-implementation baseline.

- Packet B final migration check: both adapters now probe
  `invitations.resolved_subject_type` at construction. Maintained upgrade tests
  apply through 0016, retain an existing pending invitation, require 0018 before
  boot, then prove token/row preservation and claim/finalization after upgrade.
  Both fail before the probe fix and pass afterward with race against the owned
  PostgreSQL named schema/libSQL fixture; both adapter vet checks pass. Logs:
  `/tmp/gopernicus-invitation-upgrade-{pgx,turso}-{before,after}.log`.

The first isolated `make check` passed every module's build/test/vet and all
integration/live-tag compilation, then caught one FS9 response-helper violation in
new form logout error handling. Replaced raw http.Error with the existing HTML
error renderer; the focused logout regression passes. An initial standalone guard run used the
unwritable default Go cache; rerunning with the established GOCACHE passed all guards
(`/tmp/gopernicus-authentication-guards.log`). The final
snapshot will include that correction and the last credential-proof binding fixes.

### Final reset proof binding follow-up

Independent review reproduced an old recovery address's reset link remaining
usable after address replacement. Password-reset Context now stores the issuing
AuthRevision and verified recovery IdentifierID; atomic Redeem checks current
active ownership/recovery use and exact revision before committing the reset.
User-first SQL lock order is preserved, rejected proof rolls back token and all
state, and successful reset advances the revision once. No schema/signature
change; legacy unbound reset links require a fresh request, and custom reset
stores must honor the stronger contract. Maintained service regression failed
before and passes afterward; reference/service/HTTP race and owned PostgreSQL
named/libSQL reset+credential-admission race conformance pass. Core and both SQL
adapter build/vet pass. Full combined gates remain A/root-owned. Details and
exact files: `/tmp/gopernicus-authentication-reset-binding-report.md` and its
companion inventory.

## Final verification and handoff

Completed on branch `firestore-authentication`, HEAD `6807ed06`; no commit, release,
publication, consumer edit or production mutation. All ten numbered findings are
addressed. Independent bounded reviews of credential proofs, invitation claims,
root configuration/transports and challenge/grant/identifier paths have no remaining
blocker. Firestore feature completion and authorization implementation remain out
of this phase.

Passed:

- Final `make check` across all **42 modules**: generated templ checksum stability,
  `go vet ./...`, `go build ./...`, `go test ./...`, integration/live-tag vet and
  architecture guards. Used `GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache` and the
  complete source snapshot `/tmp/gopernicus-authentication-check-32uc0u4e`.
  Log: `/tmp/gopernicus-authentication-make-check.log` (exit 0).
- The snapshot has no `.git`, so Make uses its before/after generated-file check.
  Its Git-dependent vocabulary guard cannot inspect that copy; final **root**
  `GOCACHE=... make guard` separately passed all guards against the real working
  tree (`/tmp/gopernicus-authentication-guards.log`). UI asset files are unchanged
  from the pre-implementation baseline. Snapshot hashes remained unchanged during
  the gate, and every final Go/template/module source matches the checked copy.
- Full authentication and auth-cms `go test -race -count=1 ./...` passed; host
  server suite 76.160s. Logs `/tmp/auth-a-full-{core,host}-race-final.log`.
  The subsequent magic-link format-only migration to version 2 passed all affected
  authsvc/reference/example-memory races and the entire PG/libSQL passwordless
  conformance families (`/tmp/auth-a-magic-v2-core.log`,
  `/tmp/auth-a-{pgx,turso}-magic-v2.log`). The final workspace gate includes v2.
- Full maintained PostgreSQL default/named and libSQL conformance passed at the
  implementation checkpoint. Later reset, invitation upgrade, adoption and
  passwordless changes passed their affected live families with race; no selected
  leaf silently skipped. Reports below preserve exact selectors and log paths.
- Supported Firestore emulator families, grant contention/retry and all 64 ambient
  method checks passed. Unsupported methods were excluded explicitly. No deployed
  Firestore indexes or live GCP project were tested.
- Real Chromium registration, verification, login/hydration, cookie refresh,
  hostile-origin login/refresh rejection and logout passed against an owned
  auth-cms memory/console fixture. Evidence:
  `/tmp/gopernicus-authentication-browser-evidence.json`.
- Documentation `pnpm typecheck` and final `pnpm build` passed. Logs:
  `/tmp/gopernicus-authentication-docs-{typecheck,build}.log`. Docusaurus's optional
  update check could not access its local config store; this did not fail the build.
- Final `goimports -l` reports no drift in changed non-generated Go;
  `git diff --check` passed. Prior AUDIT content is preserved byte-for-byte.

The first full run found the form-response guard violation, which was fixed.
A later sandbox run could not bind httptest listeners; the same final command
passed with disposable local listeners enabled. Those are resolved attempts,
not outstanding failures. Default `make check` intentionally skips env-gated live
store tests; the actual authentication live runs above supply that evidence.
Non-C PostgreSQL collation, real OAuth/email/SMS providers, production key/index
rollouts and external consumer deployments were not exercised.

Cleanup is complete: the browser process stopped and all **seven** recorded owned
PG/libSQL/Firestore containers were stopped and verified removed. Their four
`/tmp/gopernicus-authentication-{implementation-stores,invitations-stores,challenges-stores,emulator}.json`
records contain `cleaned_up: true`; the old endpoints are no longer usable.

The phase inventory is `/tmp/gopernicus-authentication-final-inventory.json`,
computed from the 2054-file pre-implementation SHA manifest, including additions
and deletions. It records **193 changed paths, 43 additions, no deletions**. Every
change is in authentication, its auth-cms example, migration/release notes or audit
documentation. SDK, integrations, authorization and CMS implementation hashes are
unchanged from that baseline. Post-gate edits only finalize Markdown handoff state;
no code changed after final verification.

Detailed packet evidence and inventories:

- `/tmp/gopernicus-authentication-A-implementation-report.md`
- `/tmp/gopernicus-authentication-invitations-implementation-report.md`
- `/tmp/gopernicus-authentication-reset-binding-report.md`
- `/tmp/gopernicus-authentication-D-report.md`
- `/tmp/gopernicus-authentication-root-surface-review.md`
- `/tmp/gopernicus-authentication-D-final-review.md`

Consumer manual upgrade: apply SQL invitation `0018`, upgrade core/adapters/custom
stores together with old writers/workers stopped, restart the listed in-flight
proofs (including all v1 magic links), and verify the host's idempotent invitation
Granter plus browser/native login/reset/logout. Password strength remains host
configuration. `AUDIT-022` contains the concrete contracts and rollout steps.
