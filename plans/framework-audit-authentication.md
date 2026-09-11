# Authentication pocket: audit and implementation plan

Status: AUDIT/PLAN COMPLETE — 2026-09-10. The owner subsequently authorized
implementation, now complete and verified in [authentication-audit-implementation.md](authentication-audit-implementation.md).
The findings below preserve the original read-only audit; current fixes and evidence
are tracked in that implementation plan and AUDIT-022.
Parent: [authentication-authorization-audit.md](authentication-authorization-audit.md).
Sibling: [authorization audit](framework-audit-authorization.md).

Date: 2026-09-10. Reviewer: named lead-backend-engineer, read-only. Active plan: `plans/authentication-authorization-audit.md`. Repository root: `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus`; branch `firestore-authentication`, baseline commit `6807ed06` with substantial pre-existing work. References below are repository-relative current source paths and line numbers, not claims that a finding was introduced in the current dirty diff.

## Verdict

**Fix proof lifetime and authority before structural simplification.** Keep the architecture and earlier SDK/OAuth decisions. Several important invariants need concrete fixes, especially credential revocation during concurrent login, verified ownership before invitation grants, and authenticated logout. These are bounded changes, but the credential fence and invitation lifecycle need port/store contracts before handler edits. The proposed sequence below defines that work. This review makes no source/schema changes and does not mark proposed breaks as implemented in AUDIT.md.

## Strengths to preserve

- The datastore-free pocket, typed public repositories, and separate PG/Turso/Firestore modules are appropriate. Cross-pocket authorization remains a host policy; authentication does not import an authorization adapter.
- Host-owned password validation, optional compromised-password checking, password-flow enablement, OAuth email trust, route authorization and delivery lifecycle are useful seams. Do not replace them with framework product policy.
- Refresh uses stored secret hashes, atomic current-hash rotation, a bounded single previous-token grace lane, reuse detection, and a fixed session expiry. These mechanisms passed existing live conformance; retain their contract while adding the missing credential-generation fence.
- Human session credentials and API keys carry separate credential metadata, including the real machine actor for act-as-user keys. Machine management routes are absent unless a host gate is configured. API key material is shown once and stored hashed. Public service calls remain trusted host orchestration; they are not automatically authorized just because HTTP convenience routes are gated.
- The SDK identity resolver projects the exact principal, stored display name and active verified addresses (`internal/logic/authsvc/resolver.go:34,79`). This is a sound boundary; invitation lookup must meet the same proof standard where an address determines authority.
- Preserve AUDIT-015 OAuth decisions: browser/native typed start/callback, independent flow secret, state+secret+mode+provider binding before atomic consume, explicit native redirect allowlist, host `TrustOAuthEmail`, raw verification/authority signals, and ID-token validation without downgrade. No new defect in those proof/transport mechanisms was confirmed in this audit. Do not turn native completion into cookie-dependent browser redirects.
- Delivery has sealed payloads, explicit durable jobs versus bounded ephemeral in-process mode, generation fencing within the queue, and host-owned RunDelivery cancellation. Keep these. The defect below is a challenge/send-site mismatch, not a reason to create another worker abstraction.
- Shared challenge error mapping and generic credential failure responses are useful. Login/token password verification precedes the explicit unverified-email response; the latter is an intentional host policy distinction, not anonymous address enumeration by itself.

## Confirmed findings, in implementation priority order

### AUTH-01 — High: successful password reset does not fence an in-flight old-password login

Evidence: `internal/logic/authsvc/service.go:826` reads and verifies the password, then separately calls session creation (`:1176,:1207`). `domain/session/active.go` fences active user status only. `service.go:959,977` changes a password with a blind Set followed by revocation; `:1033` delegates reset to the atomic reset store. PostgreSQL `stores/pgx/password_resets.go:37` atomically consumes the reset challenge, sets the password and deletes existing sessions/grants, but it does not reject a later session creation authenticated against the old password.

**Reproduced under `-race` in memory and actual disposable PostgreSQL:** pause Login after the old password has verified, complete ChangePassword or ResetPassword, resume Login. It succeeds, creates a stored session, and that session can Refresh after the reset returned success. The PostgreSQL probe uses the bundled ActiveSessions implementation, so merely wiring that optional capability does not close the hole. This violates the intended meaning of credential revocation and can preserve access for someone with a compromised old password.

Fix contract first: bind password proof/session admission to a credential revision or equivalent atomic expected-credential check. Password mutation must update that fence atomically with credential state and session/grant revocation; session admission must reject stale proof. Reuse a suitable existing revision only after defining which identifier/credential changes increment it. A second non-atomic Get before Create is insufficient. Review IssueToken, SetPassword, removal and OAuth adoption against the same fence. `oauth.go:622` adoption revokes sessions and removes the password in separate operations; it has the related stale-proof risk by inspection, not a separate live reproduction.

Tests: pause proof before reset/change, race two changes/initial sets, issue token through the duplicate login path, reset versus refresh and passwordless/OAuth adoption, process restart and every supported store. Assert old proof cannot create any usable session, not just that old session rows disappeared.

### AUTH-02 — High when invitations are enabled: unverified email can acquire auto-accepted invitation authority

Evidence: `internal/logic/authsvc/service.go:670,675,688` creates an unverified account and immediately calls `resolvePendingInvitations`, before Verify (`:717`). Resolver at `:777` passes the address to invitationsvc without a proof requirement. `internal/logic/invitationsvc/service.go:830,854,859` grants every pending auto-accept invitation for that address. Also, public `authentication.go:2339` userLookup returns an active login/recovery identifier without checking Verified; `invitationsvc/service.go:528` uses this for direct auto-add of an existing account. Existing `authsvc/invitation_test.go` explicitly expects registration to invoke the resolver.

**Confirmed by source and existing tests, not a new end-to-end grant probe.** With default RequireVerifiedEmail=false, someone can register another person's email with their own password and receive that email's pending auto-accept permissions without proving mailbox ownership. Setting RequireVerifiedEmail=true blocks password login before verification, but the grant still happens early and is not the right invariant.

Resolve email-derived grants only after trusted proof of the matching address. Require Verified in auto-add lookup. Preserve explicit host-trusted manual subject grants and the host's decision to permit unverified ordinary login. Test register-before-verify, verify success, trusted OAuth address, squatter adoption, existing unverified address and no invitation leakage across address kinds.

### AUTH-03 — High for session integrity: logout accepts an unsigned session ID and suppresses revocation failures

Evidence: `internal/logic/authsvc/service.go:909,919` falls back to `sessionIDIgnoringExpiry`; `token.go:115,122` decodes the JWT payload without checking a signature. A caller can provide `x.<base64url({"session_id":"victim"})>.x`. `/auth/logout` is intentionally not live-session-gated (`internal/inbound/authentication/routes.go:222`), and native requests without Origin/Fetch headers are valid. The target session ID must be known; this is a targeted unauthorized revocation, not a session-ID enumeration or account takeover claim.

**Reproduced with a real service harness:** signer.Verify rejects the forged token, but Logout deletes an existing victim session. The source comment calling this a low-value revoke should be removed; the intended expired-access logout requirement does not authorize trusting an arbitrary payload.

Additionally `internal/inbound/authentication/sessions.go:508` discards Logout's error and reports success. Refresh lookup errors are also swallowed by the service. On a datastore failure a user can be told they logged out while a usable refresh credential remains. Clearing browser cookies can still be best effort, but server-side failure must be observable and retryable without claiming successful revocation. `logoutJSON` does not decode a refresh token body even though the service documentation mentions an API body; decide and document the native wire contract.

Prefer an authenticated refresh credential for expired-access logout, or add an explicit signer operation that checks signature/required claims while intentionally relaxing expiry. Do not bypass signature validation via payload parsing. Test valid expired signed token, bad signature, victim ID, unknown/empty credentials, transient lookup/Delete failure, browser cookie clearing and native body/bearer behavior.

### AUTH-04 — Medium/high browser boundary: JSON login and cookie refresh bypass the existing Origin policy

Evidence: `internal/inbound/authentication/routes.go:190,211` mounts login and refresh without the credential-establishment Origin middleware that logout/passwordless/resend use. `sessions.go:297` sets session cookies after JSON login; `:341` supports cookie-driven refresh. Form login calls `forms.go:86` formOriginOK, but the JSON branch does not. `dispatch.go:36` accepts missing Content-Type as JSON for compatibility.

**Reproduced through the real router/service:** Origin `https://evil.example.com`, Sec-Fetch-Site `same-site` successfully logs in with JSON and with no Content-Type and sets both cookies. A hostile-origin cookie refresh also succeeds. This is a confirmed HTTP policy bypass; actual browser cookie acceptance/CORS deployment exploitability was not exercised. Missing Content-Type allows a relevant simple-request route; CORS read restrictions alone do not prevent the mutation. Cookie refresh can rotate credentials and create reuse/revocation failures without the user's intent.

Apply the existing allowlisted-Origin gate consistently to cookie-establishing login and cookie-driven refresh. Keep no-Origin/no-Fetch native flows working. Do not weaken the separate `__Host-auth_csrf` double-submit rule for authenticated sensitive mutations. Test same-origin, explicitly allowed frontend, hostile sibling, cross-site simple request, bearer/body-only native client and both form/JSON branches; perform a real browser scenario before calling the browser fix verified.

### AUTH-05 — Medium: repeated sensitive-code starts leave only an invalid delivery queued

Evidence: `internal/logic/authsvc/stepup.go:213,229` replaces the challenge then uses `enqueueRendered`. `delivery.go:52` keys delivery by address digest plus generic `sensitive_code`; `:82` uses submit-once Enqueue. `password.go:90,105` password removal uses the same pattern/key. Other sensitive-code send sites share the category, creating cross-operation collisions as well.

**Reproduced with the real sealed delivery router/processor and a controlled dispatcher:** two successful BeginStepUp calls before the first queue item drains yield one delivered message containing the first code, while the stored challenge contains the second code. Completing with the delivered code returns ErrChallengeInvalid. Both start calls reported delivery accepted.

Represent one challenge generation and its intended delivery consistently. `enqueueRenderedReplace` already exists for identifier changes, but a blind swap to Replace is not a complete concurrency proof: two concurrent starts can still persist challenges and enqueue in opposite orders. Choose a small, explicit generation/order contract (or the existing opaque intent path where appropriate), include the actual operation/binding in the key, and assert the delivered generation is redeemable. Test sequential restart, concurrent starts, cross-purpose starts, queue saturation/provider retry, enqueue failure and restart. Do not build a new generic orchestration framework.

### AUTH-06 — Medium: recent-authentication grants do not enforce the requested policy consistently

Evidence: `internal/logic/authsvc/stepup.go:146` checks session liveness, age and assurance only in recentLoginGrant (`:178`); explicit grant Consume success is accepted after only UserID matching. Store Consume binds stored session ID/purpose/context/expiry but does not check a live session or requested assurance/age.

**Reproduced:** a minute-old AAL1 explicit grant satisfies a one-second/AAL2 policy. An explicit grant also succeeds after its session was deleted. Bundled sensitive HTTP routes ordinarily apply live authentication first, so the deleted-session case is a service-contract/race gap, not proof of a standalone unauthenticated HTTP bypass. Current bundled flows use AAL1; the higher-assurance defect matters to the advertised policy and future host usage rather than an existing MFA implementation.

Require a live matching session and apply one policy evaluator to both grant sources. Define whether an unsuitable explicit grant is consumed; ideally avoid destroying a valid lower-assurance grant merely by asking for an unsuitable policy. Keep atomic one-use semantics. Also correct proof metadata: `stepup.go:294` records EmailCode for phone code, `password.go:184` remints Password after removal proved through recovery, and `passwordless.go:414` stamps EmailLink for generic link kind. Preserve honest authentication methods rather than inventing assurance.

### AUTH-07 — Medium: concurrent PostgreSQL challenge Replace violates the replacement contract

Evidence: `stores/pgx/challenges.go:73` deletes the prior subject/purpose row and then inserts a replacement in READ COMMITTED. Two transactions can both target the old row; after one commits a new row, the second DELETE need not discover that new row and its INSERT hits the subject/purpose unique constraint. The contract only identifies duplicate purpose/secret digest as the expected AlreadyExists collision.

**Reproduced on disposable PostgreSQL** with distinct secrets and a controlled lock wait: the second normal repository Replace returns sdk.ErrAlreadyExists. Memory serialization and Turso's transaction behavior do not produce this interleaving, so a passing generic suite missed parity.

Use an atomic conflict-targeted replacement or serialize on a stable subject key. Preserve secret-digest uniqueness and explicit ID semantics. Add a live concurrent Replace conformance case for both pre-existing and initially absent rows; include challenge redemption versus replacement. Review contact-change Delete+Insert paths for the same pattern before declaring full parity (not reproduced here).

### AUTH-08 — Medium: incomplete boot validation and mutable security configuration

Evidence: public `authentication.go:1733` validates Hasher, Mailer and TokenSigner but not the core Users/Identifiers/Passwords/Sessions repositories required by mounted password/session routes. `:2874` Register dereferences an absent Router. Enabled password flows can reach persistence before missing challenge/delivery wiring is discovered. Conversely a host disabling passwords and delivery still must provide Hasher and Mailer. Duplicate InviteCheck and UserAdmin blocks at `:1783,:1799,:1807,:1823` are literal drift. `:2322` retains AllowedOrigins by reference. Production validates durable limiter/transports but does not require Secure session cookies; cookie values are copied into authsvc at `:2192`.

**Reproduced:** a constructor with no core repositories succeeds, then ordinary RegisterUser panics; Register with a plain nil router panics; a production configuration with a durable limiter accepts Secure=false for access/refresh cookies. AllowedOrigins aliasing and duplicate validation are source-confirmed, not separate probes.

Make an explicit enabled-feature/dependency matrix, validate it once, and fail before mounting or mutating state. Preserve library-only usage; require collaborators only for enabled features. Return ordinary configuration errors for plain nil required ports/router, copy host-owned collections at construction, and decide whether production mode must reject insecure session cookies (recommended, with a documented development path). No need for reflection to accommodate arbitrary typed-nil interface misuse. Keep Logger injection; no independent logger ownership bug was confirmed.

### AUTH-09 — Medium: invitation transitions are read-then-write and external grant runs before a guarded claim

Evidence: `internal/logic/invitationsvc/service.go:626` reads a pending invitation, invokes host Granter at `:663`, then UpdateStatus at `:671`. Auto-resolve has the same ordering (`:859,:868`). PostgreSQL `stores/pgx/invitations.go:198` updates WHERE id only; domain `invitation/repository.go:13` has no expected state/token/version. Turso and memory mirror that contract.

**Source-confirmed interleaving; not a live probe:** accept can read pending, cancellation completes, then accept grants and overwrites cancelled with accepted. Resend can rotate the token while an old-token accept is paused, then the stale acceptance can still grant and write the old token state. Idempotent Granter operation IDs prevent duplicate execution of the same logical grant, but do not order cancellation, token generation and first execution.

Specify a state machine with conditional transition/claim and an honest cross-store grant-delivery contract. A CAS only after Granter is insufficient: the unauthorized side effect has already happened. A durable accepting state plus idempotent grant/retry/finalization is one option; a same-transaction host operation is another only where actually supported. Cancellation semantics while acceptance is already claimed must be explicit. This needs design approval, not an ad hoc distributed transaction layer. Test cancel/accept, resend/old-token accept, duplicate accept, failed grant, successful grant plus failed finalize and restart.

### AUTH-10 — Medium: account/credential provisioning can partially persist and initial password Set is not conditional

Evidence: `internal/logic/authsvc/service.go:670,675` persists user+primary identifier atomically, then separately Sets password. A password-store error strands an account claiming the email; retry collides instead of finishing. OAuth first registration (`oauth.go:568` and subsequent provider-link creation) similarly persists user/identifier separately from provider account linkage. `password.go:47` checks absence then blind upserts via PasswordRepository.Set, allowing concurrent initial-set attempts to overwrite one another without proving the newly installed credential.

**Source-confirmed; no failure-injection reproduction in this audit.** Consolidate credential/account admission into focused atomic repository operations where this invariant belongs, building on AUTH-01. Do not attempt to solve it by deleting a partially created user without a concurrency/ownership guarantee. Define retry/convergence for two first OAuth callbacks and failures after user creation. Keep acknowledged asynchronous mail delivery failure separate from atomic identity/credential creation.

## Additional bounded completeness and simplification work

1. **Abuse budgets are uneven.** ForgotPassword (`service.go:1004`) and sensitive-code/step-up-password routes lack the per-identifier/per-IP budgets present in passwordless starts and verification. Challenge failure limits bound one challenge, but freely restarting a challenge resets that bound; generic sensitive-code submit-once is not a real rate limit. Inspect and declare host-configurable limits for password guessing, reset starts and sensitive starts, preserving native callers and generic public error envelopes. Source review only; no flood/provider test performed.
2. **Public facade is incomplete relative to bundled HTTP behavior.** Public Service exposes legacy password, OAuth, machine and invitation operations (`authentication.go:2466` onward) but not passwordless start/redeem, credential inventory/identifier management, Set/RemovePassword or Begin/CompleteStepUp. These exist inside an `internal` package and therefore cannot be reused directly by a host's custom terminal/mobile/HTML adapter. This is unfinished architecture, not grounds to delete working HTTP features. Choose a small public use-case surface and stable request/result types, with explicit trusted-host versus authorized-entrypoint semantics. Avoid simply exporting the entire large inbound authService test interface.
3. **Memory ownership differs.** Example authmem session Create/Get/GetByRefreshHash (`examples/auth-cms/internal/authmem/authmem.go:347,360,377`) stores/returns shallow Session copies whose Authentication.Methods slice aliases persisted state; auth grant Create/Consume (`ports_v3.go:286,296`) likewise shares Methods. Most methods ignore context. These are source-confirmed demo/reference parity gaps, not production SQL corruption. Add minimal ingress/egress cloning and cancellation contracts if this store is a supported reference; do not bury it under generic clone machinery. Invitation metadata already has a deliberate copy path.
4. **Ambient transaction claim is inaccurate.** Firestore `stores/firestore/store.go:33` says SQL adapters join the host ambient transaction. Authentication SQL password repositories instead invoke `s.db.Exec/QueryRow` (`stores/pgx/passwords.go:32,40`, Turso `:29,37`); connector DB methods call pool/sql.DB directly (`integrations/datastores/pgxdb/db.go:52`, `turso/db.go:49`), and transactional auth repositories call InTx directly. They do not use connector QuerierFrom(ctx). Therefore wrapping arbitrary auth operations in host Transact is not an atomicity guarantee. Source-confirmed, no ambient rollback probe. Clarify the contract first; focused atomic domain ports are sufficient for many fixes. Do not promise cross-pocket transactions the stores do not implement. Firestore's explicit ambient rejection is preferable to silently splitting a transaction.
5. **Firestore is explicitly unfinished.** Its constructor exposes all 18 repository slots, but 29 production methods across challenges, reset, passwordless, contact changes, credential mutations, API keys, service accounts, security events, invitations and user status still return errNotImplemented. `store.go:42` and SCHEMA/index comments mark the N1/N2–N5 work as incomplete. Implemented portions include users/identifiers/claims, passwords, OAuth accounts/states, sessions/active fencing, grants, and portions of user administration. Default unit tests can pass while full emulator conformance fails. Classify this as completion work, not a newly introduced bug. Do not advertise a production-ready adapter until supported capability matrix, emulator semantics and real index coverage are proven. No Firestore emulator or production index verification was performed here.
6. **Simplify after the contracts.** Remove duplicate constructor validations, move historical plan narratives out of hot source comments, share the private password proof/session-admission core between Login and IssueToken, and split test-facing interfaces by the real handler groups that need them. Preserve explicit typed atomic ports, stable errors, delivery/runtime boundaries and host policies. File length alone does not justify new packages. Do not merge authentication and authorization or add a generic workflow engine.

## Coverage and verification

Read paths: public config/facade and security options; service registration/login/password/reset/verification; challenges/passwordless/identifier and credential mutation; session and refresh; step-up/grants; OAuth start/callback/link/adoption; API keys/machine resolution/middleware; SDK identity projection; inbound routes/forms/JSON/security/error mapping; invitation resolution/acceptance; user administration; representative and security-relevant PG/Turso/Firestore/memory implementations and migrations. This is a deep targeted audit, not a proof that every statement in every historical plan or adapter method is correct.

Passed:

- `GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache go test -race ./pockets/authentication/...` — core packages pass; several package results cached. Final log `/tmp/gopernicus-authentication-core-race.log`. The initial sandbox attempt failed solely on local httptest listener permission, then the authorized escalated rerun passed.
- Full isolated PostgreSQL `go test -race -count=1 -v ./pockets/authentication/stores/pgx` with disposable POSTGRES_TEST_DSN — pass, 58.739s. **One explicit skip:** `TestCollationControlsOrdering_NonC`, because POSTGRES_NON_C_TEST_DSN was not set. Log `/tmp/gopernicus-authentication-pgx-live.log`.
- Full isolated libSQL `go test -race -count=1 -tags=integration -v ./pockets/authentication/stores/turso` — pass, 26.794s, no skips found. Log `/tmp/gopernicus-authentication-turso-live.log`.
- `go build` and `go vet` over core plus PG/Turso/Firestore authentication modules — pass. Logs `/tmp/gopernicus-authentication-build.log`, `...-vet.log`.
- Default Firestore `go test -race ./pockets/authentication/stores/firestore/...` — pass, 3.056s; integration-tagged emulator tests were not selected. Log `/tmp/gopernicus-authentication-firestore-unit.log`.
- Temporary overlays reproduce AUTH-01/03/04/05/06/07 and selected AUTH-08 cases. All pass (a probe PASS means the described defect was reproduced). Core/config/route overlay `/tmp/gopernicus-authentication-review-overlay.json`; test sources `/tmp/gopernicus-authentication-{review,route,config}-probes_test.go`; log `/tmp/gopernicus-authentication-review-probes.log`. Actual PostgreSQL overlay `/tmp/gopernicus-authentication-pgx-overlay.json`, source `/tmp/gopernicus-authentication-pgx-probes_test.go`, log `/tmp/gopernicus-authentication-pgx-probes.log`.

Probe replay, without modifying tracked source:

```sh
GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache go test -race -overlay=/tmp/gopernicus-authentication-review-overlay.json -run '^TestAudit' -v ./pockets/authentication ./pockets/authentication/internal/logic/authsvc ./pockets/authentication/internal/inbound/authentication
# Use a newly created disposable POSTGRES_TEST_DSN; the recorded fixture has been removed.
GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache go test -race -count=1 -overlay=/tmp/gopernicus-authentication-pgx-overlay.json -run '^TestAudit' -v ./pockets/authentication/stores/pgx
```

The live probes create/drop only their dedicated audit schemas. The root stopped and verified removal of the isolated PG/libSQL containers.
The recorded audit endpoints are no longer running. No existing service, real provider, production account, email transport or consumer source was mutated.

Unverified: actual browser exploit/fix behavior, real OAuth login and provider outages, production secrets/key rollover, Firestore live/emulator semantics and index coverage, non-C database collation, external consumer deployment behavior. The independent reviewer did not locate the external consumers. The root
reviewer subsequently inspected their actual paths; see the consumer appendix
below. Consumer execution/deployment remains unverified.

## Proposed implementation sequence and breaking-change ledger

These are dependent reviewable tasks for later owner authorization; no implementations are claimed here.

1. **A: freeze invariants and capability matrix.** Document authentication proof lifetime, password mutation/session-admission revision, invitation proof/transition rules, expiry-authenticated logout, enabled-feature dependencies and trusted host API boundary. Select supported Firestore scope. This must precede schema/API work; no runtime behavior changes in the planning stage.
2. **B: implement credential mutation/admission fence** (AUTH-01, AUTH-10). Domain user/credential/session/passwordreset ports, authsvc password/login/token/adoption, all supported stores, paired migrations and storetest. Proposed break: custom stores must implement the stronger admission/mutation contract; existing data needs explicit revision initialization. Define whether existing sessions are kept or deliberately revoked during rollout. Mixed old/new application instances must not bypass the new fence.
3. **C: secure logout and browser establishment** (AUTH-03/04). authsvc/token, inbound session/form routes, signer port only if expiry-relaxed authenticated validation is needed, docs and browser/native tests. Proposed behavior break: forged/unverifiable access payloads no longer revoke anything; hostile-origin cookie flows fail; revocation failures return an error. Native refresh-only logout may gain an explicit body contract.
4. **D: enforce verified invitation ownership and atomic lifecycle** (AUTH-02/09). authsvc resolver callsites, public userLookup, invitationsvc and invitation repository/state schema, all supported stores. Keep idempotent Granter operation IDs; select claim/retry/cancellation rules before implementation. Proposed break: unverified address registrations no longer auto-grant; custom invitation stores need conditional transitions; cancellation after an accepted claim has a defined outcome. Add failed-side-effect/finalization/restart tests.
5. **E: align challenge generations, grants and abuse budgets** (AUTH-05/06/07). authsvc challenge/send/step-up/password/credential code, delivery integration, PG Replace and storetest. Proposed behavior changes: obsolete deliveries/grants reject consistently; assurance and age are enforced; abuse exhaustion has stable native/form/JSON outcomes. Keep current OAuth proof binding unchanged.
6. **F: validate and simplify wiring/public host surface** (AUTH-08 and additional items). Config validation once, collection snapshots, feature-sensitive collaborators, plain nil guards, optional production Secure rule, small reusable public use cases, duplicate login proof removal and accurate transaction docs. Proposed break: formerly accepted but unusable configurations fail at boot; disabled features can shed dependencies; public APIs should grow only around real host use cases.
7. **G: complete supported adapter parity and consumer migration notes.** Firestore completion is a separately approved capability milestone, not a hidden dependency that forces all features on every host. Add memory ownership tests and DB failure/concurrency conformance. Repeat relevant build/test/vet and real browser/native flow verification. Only when each change is implemented add its concrete migration impact to AUDIT.md for consumers.

No architectural decision requires asking the owner to reauthorize already approved SDK/OAuth behavior. The decisions that need review before implementation are the credential fence rollout, invitation acceptance/cancellation state machine, supported Firestore scope, production cookie constraint and desired public host use-case surface.

## Root synthesis and consumer appendix

The first implementation review should resolve A, then prioritize the credential
fence and verified invitation ownership. Logout/Origin corrections and bounded
configuration fixes can be separate small changes; they need not wait for a
complete invitation state machine or Firestore feature expansion. Keep host
password policy intact: the framework should enforce the chosen policy at each
password mutation, while length/strength rules and breach-check behavior remain
host configuration. Do not make an authentication redesign a prerequisite to
fixing demonstrated failures.

Recommended choices for that review:

- Prefer an atomic expected-credential revision check at session admission,
  coupled with revision advancement on relevant credential changes. Keep status
  fencing too. Specify mixed-version rollout before choosing columns or port
  signatures; an optional fallback that silently admits old proof would preserve
  the defect.
- Separate ordinary unverified login policy from email-derived authority.
  Auto-grants require a verified matching address even when the host permits
  unverified password login. Preserve explicit trusted host grants.
- Prefer authenticated refresh proof for expired-access logout if it meets both
  browser and native callers. Add an expiry-relaxed signer operation only if a
  real caller needs access-token-only revocation. Neither option may parse an
  unsigned payload as authority.
- Give invitation acceptance a documented linearization point before the host
  grant. A durable accepting claim with idempotent execution/finalization is the
  leading option for different stores; exact cancellation and recovery behavior
  must be reviewed before implementation. This is a small domain state machine,
  not a generic workflow framework.
- Require valid collaborators only for enabled capabilities, copy caller-owned
  security slices/maps, and fail on plain nil required repositories/router at
  construction. Decide typed-nil treatment consistently with authorization's
  C8; avoid creating a public generic dependency-validation abstraction.
- Keep Firestore's supported capability milestone separate. Completion of every
  optional auth feature is not implied by fixing PostgreSQL or core behavior.

The root inspected current consumer source at these actual paths, without
pulling, building, running, changing configuration or editing a consumer:

| Consumer | Verified source pattern and migration relevance |
| --- | --- |
| Segovia v2 `/Users/jrazmi/code/segovia/segovia/v2`, main `76b3d78`, auth 0.10.0 | `cmd/server/authentication.go:40` owns keys, delivery and policy wiring; jobs delivery is explicitly acknowledged, RequireVerifiedEmail defaults true, AllowedOrigins is the public origin, and the session cookie has a host name. Environment parsing can override defaults. Preserve the jobs delivery lifecycle and native/browser seams while applying the new credential and challenge contracts. No deployed cookie flags or environment values were inspected. |
| coordination-hub `/Users/jrazmi/code/gps/coordination-hub`, main `84ff08a`, auth 0.9.0 | `pockets/auth/outbound/authconfig.go:365` requires verified email and enables passwordless email with acknowledged ephemeral in-process delivery; the host owns startup/drain. `:772` builds a Secure-by-default host cookie with an explicit local-development override. The invitation bridge and custom SPA landing routes make grant timing, delivery-generation correctness and Origin parity material adoption cases. |
| gps-360-go `/Users/jrazmi/code/gps/three-sixty/gps-360-go`, main `e1ab3f0`, auth 0.9.0 | `cmd/server/authentication.go:22` declares Google-only human login, password flows disabled, delivery off and no invitations. Lines 47–48 still inject bcrypt and a console mailer solely because the framework requires them. This is a concrete case for feature-sensitive dependencies, not a speculative abstraction. `cmd/server/main.go:261` explicitly composes machine-management authorization with authentication. |

All three default RequireVerifiedEmail to true in the inspected source. That
limits the direct unverified-password-login invitation scenario in those defaults;
it does not repair the framework's premature grant or prove deployed overrides.
GPS's password-disabled profile should not acquire challenge/email/password
dependencies simply because other hosts need them. These repositories pin older
module versions, so the findings above establish migration use cases, not proof
that every current-workspace defect exists in their deployed binaries.

Durable context for future work is this report, the central plan, current
ARCHITECTURE.md and the affected source/tests. `/tmp` overlays are diagnostic
artifacts: their described fixtures and expected invariants must become maintained
regressions during an approved implementation; do not depend on temporary files
surviving another machine/session.
