# Phase 5 — re-key existing services onto identifiers

Status: READY after phases 1–4.
Depends on: phases 1, 2, 3, and 4 complete.
Design: §§2, 4.1, 5.7, 7, 13 V5/V9/V11.

## Goal

Move all existing registration, password login/recovery, OAuth, identity
resolution, invitations, and limiter behavior to `user_identifiers`, then
remove the temporary email-on-user compatibility surface and finalize the
canonical schema.

## Task AV3-5.1 — registration, login, token, recovery, and resolver re-key

Touch: authsvc registration/login/token/recovery, public service methods/DTOs,
resolver, tests.

Implement:

- registration uses atomic `CreateWithPrimaryIdentifier` with one unverified
  primary email identifier carrying login/recovery/notification uses;
- password login and token issuance normalize email and call `GetLogin`, reject
  inactive/unverified as configured, then load user/password by user ID;
- forgot-password uses `GetRecovery` only inside the phase-4 worker, never the
  request handler;
- public API signatures remain email-shaped where V9 preserves them, but domain
  storage has no duplicate email source;
- resolver returns all active verified identifiers with stable primary-first
  ordering and no replaced rows;
- magic link/token binding helpers always use identifier ID + kind + normalized
  value and revalidate the current row.

Write tests for multiple login-enabled emails on one user, notification-only
shared phone, replaced primary, unverified registration, and compatibility DTO
behavior.

Verify:

```sh
cd features/authentication && go test ./internal/logic/authsvc/... ./domain/... -run 'Register|Login|Token|Forgot|Resolver'
make check
```

## Task AV3-5.2 — OAuth matching and adoption hardening

Depends on: AV3-5.1.
Touch: OAuth service/state payload/provider tests.

Implement:

- match provider email through normalized identifier lookup;
- permit match/adoption only when the provider explicitly asserts verified
  email provenance;
- capture identifier ID and unverified-at-flow-start fact in persisted pending
  link state; never re-derive at completion;
- on adoption of an unverified claim, revoke pre-existing passwords and
  sessions before completing the link, in a transaction/composition that cannot
  leave the squatter credential alive;
- exempt self-registration verification and self-initiated identifier changes;
- preserve PKCE, state, nonce, exact callback/redirect allowlist, and typed
  OAuth-account storage.

Tests include provider email without verification, changed identifier between
start/finish, captured-flag TOCTOU, attacker password/session revocation, and
existing linked login.

Verify:

```sh
cd features/authentication && go test -race ./internal/logic/authsvc/... ./internal/inbound/authentication/... -run 'OAuth|Adopt|PendingLink'
make check
```

## Task AV3-5.3 — invitations and kind-aware identity seam

Depends on: AV3-5.1.
Touch: invitations domain/service/repository, stores/index if not already added,
storetest, tests.

Implement:

- replace `EmailForUser`/`VerifiedPhoneForUser` proliferation with one narrow
  kind-aware active-verified-identifier accessor;
- normalize invitation identifiers through the same `IdentifierNormalizer` at
  creation;
- fix `ListBySubject` to filter `(identifier_kind, identifier)` and ensure both
  dialects have the composite lookup index;
- phone invitation accept-time account match against the caller's active
  verified phone identifier;
- keep grant authorization and token/session requirements unchanged.

Tests prove cross-kind same-string isolation, E.164 normalization, replaced or
unverified phone mismatch, and email behavior regression.

Verify:

```sh
cd features/authentication && go test ./internal/logic/invitationsvc/... ./storetest -run 'Invitation|ListBySubject'
make check
```

## Task AV3-5.4 — PII-free rate limits and trusted client IP

Depends on: AV3-5.1, AV3-0.2, AV3-0.5.
Touch: rate-limit key helpers, client-info middleware, construction validation,
tests.

Replace every raw email/phone limiter key with the separate identifier-keyer
digest. Apply per-identifier and per-trusted-IP budgets before account
resolution. Remove fallback trust in raw forwarding headers. Production mode
requires limiter metadata declaring a durable/shared implementation for
multi-instance use; development memory limiter warns.

Tests prove equivalent normalized values share a bucket, raw PII is absent from
fake limiter calls/logs, spoofed XFF cannot rotate buckets, and unknown/known
identifiers consume the same limiter arms.

## Task AV3-5.5 — remove transitional email-on-user surface and finalize schema

Depends on: AV3-5.1 through AV3-5.4.
Touch: user entity/repository, both stores, reference/authmem, canonical
migrations, upgrade draft, all tests/fixtures.

Remove deprecated user email/verification fields and `GetByEmail`/legacy
creation methods. Edit the greenfield `users` CREATE to stable subject/profile +
`auth_revision`; `user_identifiers` becomes the only identifier source. Update
queries, scans, seeds, fakes, and migration table inventories. Renumber both
dialect migration trees identically if required.

The host-upgrade draft must now include the validated cutover/drop/table-rebuild
step, with backfill verification before column removal.

Required grep:

```sh
rg 'GetByEmail|EmailVerified|PhoneVerified|users\.email|email_verified' features/authentication examples/auth-cms
```

Only intentional compatibility DTO names, historical docs, provider claims,
and upgrade SQL may remain; classify each result in the execution log.

## Task AV3-5.6 — regression and live re-key proof

Depends on: AV3-5.5.

Run register→verify→password-login, token issue, forgot/reset, OAuth existing
link/new registration/pending adoption, resolver projection, and invitation
flows on fresh pgx and turso databases. Include two emails for one user and a
shared notification-only phone fixture. Record evidence.

## Phase acceptance

```sh
make check
make guard
```

Both live dialect legs pass; no active code reads identity from `users`.

## Stop conditions

- OAuth integration cannot prove provider email verification: that provider
  must not auto-match; stop if existing product behavior requires otherwise.
- Host-upgrade backfill cannot remove legacy columns without data loss: stop and
  report fixture/collision details.

## Execution log

Append dated entries per completed task.

### 2026-07-13 — AV3-5.1 (registration/login/token/recovery/resolver re-key)

Dependencies: phases 1–4 all closed and gate-green (checked in `TASKS.md`; execution
logs in `02`/`03`/`04`/`05` phase files). Worktree changes preserved; no resets. No
reviewer/consultation agents spawned (forbidden AV3-0.1..9.6).

Scope held to registration/login/token/recovery/resolver re-key. OAuth branch-2
(`oauth.go`) and invitation identity ports were left untouched — AV3-5.2 and AV3-5.3
own those. The transitional email-on-user dual-write remains (removal is AV3-5.5):
re-keyed flows no longer READ `users.email`/`EmailVerified`, but `CreateWithPrimaryIdentifier`
still writes the deprecated column and Verify still dual-writes the verified flag.

Files changed:

- `features/authentication/internal/logic/authsvc/service.go` — added the injected
  `identifier.Normalizer` (Deps + Service field, nil-defaulted to `DefaultNormalizer`)
  and a single `normalizeEmail` helper. Registration now creates the unverified primary
  email identifier ATOMICALLY with the user via `CreateWithPrimaryIdentifier` +
  `identifier.NewRegistrationEmail` (login+recovery+notification, primary, unverified).
  Verify resolves the account through `GetLogin`, consumes the challenge, then
  `ApplyVerifiedChange` retires the registration identifier (`ReplacesIdentifierID`) and
  claims its verified replacement under the revision-CAS. Login resolves identity via
  `GetLogin` + `users.Get(ident.UserID)` and gates verification on `ident.Verified()`.
  ForgotPassword normalizes through the injected normalizer so the phase-4 worker's
  `GetRecovery` resolves the same stored value.
- `features/authentication/internal/logic/authsvc/token.go` — `IssueToken` re-keyed to
  `normalizeEmail` + `GetLogin` + `ident.Verified()`; dropped the now-unused `user` import,
  added `identifier`.
- `features/authentication/internal/logic/authsvc/resolver.go` — `Resolve` projects every
  active VERIFIED identifier as an `identity.Address`, primary-first then oldest-first,
  excluding replaced/unverified rows (new `projectAddresses`/`firstEmailLocalPart`);
  nil Identifiers projects nothing, a repo error fails closed.
- `features/authentication/authentication.go` — wire `deps.Normalizer` from
  `cfg.IdentifierNormalizer` (nil → bundled default); refreshed the public `Resolve` doc.
- `features/authentication/internal/logic/authsvc/service_test.go` — linked
  fakeUsers↔fakeIdentifiers so `CreateWithPrimaryIdentifier` persists the primary and
  arbitrates the auth claim; `ApplyVerifiedChange` fake now retires the displaced primary;
  new tests: multiple login-enabled emails on one user, notification-only shared phone
  (no login claim, projected as contact), compatibility DTO stays email-shaped.
- `features/authentication/internal/logic/authsvc/resolver_test.go` — reseeded the two
  existing resolver tests onto identifier rows; added primary-first projection, replaced/
  unverified exclusion, and unverified-registration-projects-nothing cases.
- `features/authentication/internal/inbound/authentication/{helpers,machine,invitation,oauth,token}_test.go`
  — linked memUsers↔memIdentifiers (constructors `newMemUsers`/`newMemIdentifiers`,
  `CreateWithPrimaryIdentifier` persists the primary) and wired `Identifiers` in every
  handler harness so the re-keyed Login/IssueToken resolve through `GetLogin`; dropped
  orphaned `user` imports.

Premise adaptation logged: the task bullet "magic link/token binding helpers always use
identifier ID + kind + normalized value and revalidate the current row" has no target in
this task — the only token flow present is password reset, which is user+purpose-keyed
through the atomic `passwordreset.Repository` (phase 3) and carries no identifier binding.
The identifier-bound magic-link/OTP helpers arrive with passwordless login (phase 7,
AV3-7.x); nothing to re-key here. Recorded, not silently skipped.

Commands:

- `cd features/authentication && go test ./internal/logic/authsvc/... ./domain/... -run 'Register|Login|Token|Forgot|Resolver'` → ok (authsvc + domain packages pass; domain packages report no matching tests).
- `cd features/authentication && go test ./...` → ok (all feature packages incl. inbound transport, storetest, authmem).
- `make check` → all checks passed (per-module build/vet/test, templ drift, integration-tag vet, all layering/import guards).
- `gofmt -l` on changed non-test files → clean.

Live legs: none required by this task (hermetic re-key). Dual-dialect live conformance is
the phase gate (AV3-5.6) after the schema finalization (AV3-5.5).

### 2026-07-13 — AV3-5.2 (OAuth matching and adoption hardening)

Dependencies: AV3-5.1 complete and checked off (execution log above); phases 1–4 all
closed/gate-green. Worktree changes preserved; no resets. No reviewer/consultation agents
spawned (forbidden AV3-0.1..9.6).

Scope held to OAuth service + state payload + provider tests. Invitations/kind-aware seam
(AV3-5.3) and PII-free limiter keys (AV3-5.4) untouched. The transitional email-on-user
surface remains (AV3-5.5 owns removal): the OAuth register path still dual-writes
`user.Email`/`MarkVerified` via `user.NewUser`+`CreateWithPrimaryIdentifier`, matching the
AV3-5.1 registration precedent.

Files changed:

- `features/authentication/internal/logic/authsvc/oauth.go` — re-keyed branch 2 off
  `user.NormalizeEmail`/`users.GetByEmail` onto the injected normalizer + a new
  `matchIdentifier` helper (`GetLogin` then `GetRecovery`, verification NOT filtered so the
  unverified-at-start fact is observable). Added the §5.7 verified-provenance gate:
  match/adoption/register now require `p.TrustEmailVerification() && ident.EmailVerified`,
  else `ErrProviderEmailUnverified` (403); branch 1 (existing link, provider-user-id keyed)
  is unaffected. Pending-link payload changed from a bare `oauthaccount.OAuthAccount` to a
  new `pendingLink` struct that CAPTURES the matched identifier id and
  `UnverifiedAtStart` (`!matched.Verified()`) at flow start. `startPendingLink` now takes the
  matched identifier and mails to its normalized value. `registerAndLink` re-keyed to
  `CreateWithPrimaryIdentifier` + a VERIFIED primary email identifier (`identifier.New`,
  login+recovery+notification, primary). `VerifyLink` reads `UnverifiedAtStart` VERBATIM (no
  re-derivation) and, when set, runs a `revokeForAdoption` composition BEFORE creating the
  link: `sessions.DeleteByUser` then `removePassword` (revision-CAS `credential.RemovePassword`
  via the credential-mutation rail, bounded retry on `ErrConflict`, no-op when passwordless).
  Fails closed (`ErrAdoptionRevocationUnavailable`) when the rail is unwired — the link is
  never created leaving a squatter credential alive. Added `adoptionRevisionRetries` const and
  two new sentinel errors.
- `features/authentication/internal/logic/authsvc/service.go` — added the
  `credential.MutationRepository` collaborator (`Deps.CredentialMutations` +
  `Service.credentialMutations`, nil-tolerated; the adoption path fails closed while nil).
- `features/authentication/authentication.go` — thread `repos.CredentialMutations` into
  `authsvc.Deps`.
- `features/authentication/internal/logic/authsvc/oauth_test.go` — wired `Identifiers` and a
  new `fakeCredentialMutations` into `newOAuthHarness`; `mustOAuthUser`/`mustOAuthUserVerified`
  now seed a primary email identifier (verified or unverified-registration) so branch-2
  matching resolves; `seedSquatterCredential` seeds a password + session; `completePendingLink`
  helper. Repurposed the untrusted-provider test to assert refusal and added: trusted-but-
  unverified refusal, existing-link login despite unverified email, squatter password/session
  revocation on adoption, captured-flag TOCTOU (identifier verified after start still revokes),
  captured-identity-after-change (matched identifier retired after start still completes to the
  captured user), verified-match-no-revocation, and adoption-fails-closed-without-rail.
- `features/authentication/internal/logic/authsvc/service_test.go` — added a `fakePasswords`
  `delete` seam (the credential rail's RemovePassword removal) and `fakeIdentifiers`
  `markAllVerified`/`retireAll` TOCTOU test helpers.

Premise adaptation logged: the handoff and design call the adoption revocation "a
transaction/composition that cannot leave the squatter credential alive." The task Touch
scope excludes stores/storetest, and no single-transaction "remove-password + revoke-sessions
+ create-link" store operation exists (`credential.MutationRepository.Apply` mutates one typed
source and does not touch sessions; `PasswordRepository` exposes no delete). Implemented as a
SERVICE-level ordered composition (`revokeForAdoption`): sessions revoked first, password
removed via the revision-CAS rail second, and the adopting link created LAST — only on a nil
revocation return — so the invariant "a completed adoption cannot leave the squatter
credential alive" holds without a cross-aggregate store transaction. A dedicated atomic
adoption store operation was NOT added (out of scope; would touch stores/storetest). Recorded,
not silently skipped.

Behavior change logged: under §5.7 an untrusted provider (or one that does not assert the
email verified) can no longer OAuth-register or match — previously an untrusted provider
registered an unverified user. The design mandates this ("never ... registers"); the old
`TestOAuthCallbackRegisterUntrustedEmailUnverified` was repurposed to
`TestOAuthCallbackUntrustedProviderRefused`.

Commands:

- `cd features/authentication && go test -race ./internal/logic/authsvc/... ./internal/inbound/authentication/... -run 'OAuth|Adopt|PendingLink'` → ok (both packages pass under -race).
- `cd features/authentication && go test ./...` → ok (all feature packages incl. inbound, storetest, authmem, invitationsvc).
- `make check` → all checks passed (per-module build/vet/test, templ drift, integration-tag vet, all layering/import guards).
- `gofmt -l` on changed files → clean.

Live legs: none required by this task (hermetic). Dual-dialect live conformance is the phase
gate (AV3-5.6).

### 2026-07-13 — AV3-5.3 (invitations and kind-aware identity seam)

Dependencies: AV3-5.1 complete and checked off (execution logs above); phases 1–4 all
closed/gate-green. Worktree changes preserved; no resets. No reviewer/consultation agents
spawned (forbidden AV3-0.1..9.6).

Scope held to invitations domain/service/repository, both stores + index, storetest, and the
one kind-aware accessor that replaces the `EmailForUser` proliferation. The PII-free limiter
keyer (AV3-5.4) and the transitional email-on-user removal (AV3-5.5) were left untouched: the
direct-add `userLookup` still resolves through `user.NormalizeEmail` + `users.GetByEmail`
(AV3-5.5 owns that), and grant authorization / token / session requirements are unchanged.

Files changed:

- `features/authentication/internal/logic/authsvc/service.go` — replaced `EmailForUser`
  (which read the transitional `u.Email`) with the single kind-aware
  `ActiveVerifiedIdentifier(ctx, userID, kind)`, resolved through the existing
  `projectAddresses` projection (active + VERIFIED, primary-first then oldest-first). No
  active verified identifier of that kind → `sdk.ErrNotFound`. This is the one accessor the
  invitation HTTP handlers and the V11 phone accept-time match key on.
- `features/authentication/internal/inbound/authentication/sessions.go` — the `authService`
  port method `EmailForUser` → `ActiveVerifiedIdentifier(ctx, userID, kind)`.
- `features/authentication/internal/inbound/authentication/invitation.go` — `listMyInvitations`
  and `acceptInvitation` resolve the caller's email through
  `ActiveVerifiedIdentifier(..., identity.KindEmail)`; added the `identity` import.
- `features/authentication/internal/logic/invitationsvc/service.go` — added the injected
  `identifier.Normalizer` (Deps.Normalizer + field, nil-defaulted to `DefaultNormalizer`) and
  the `IdentifierLookup` accessor (Deps.CallerIdentifiers + field). `normalizeIdentifier`
  became a method routing email/phone through the injected normalizer (strict addr-spec /
  strict E.164) while open host-declared notifier kinds keep the prior trim-only behavior.
  `Create`/`Mine`/`ResolveInvitations` re-keyed onto the method and the kind-aware
  `ListBySubject(kind, identifier, …)`. `Accept` now runs the §7 account match as a
  kind switch: EMAIL matches the handler-passed email (unchanged, V11 "nothing else changes"),
  PHONE matches the caller's active verified phone via `callerIdentifierMatches` (fail-closed
  on unwired accessor, resolution error, or empty want), other kinds keep address-possession.
- `features/authentication/domain/invitation/repository.go` — `ListBySubject` gains the `kind`
  parameter; doc records the load-bearing `(identifier_kind, identifier)` tuple filter.
- `features/authentication/stores/pgx/invitations.go` + `stores/turso/invitations.go` —
  `ListBySubject` filters `identifier_kind = ? AND identifier = ?` (was `identifier` only —
  the design-cited latent cross-kind collision bug).
- `features/authentication/stores/pgx/migrations/0009_invitations.sql` +
  `stores/turso/migrations/0009_invitations.sql` — added
  `idx_invitations_kind_identifier (identifier_kind, identifier)` (greenfield canonical edit
  in place; filename sets stay byte-for-byte identical). Both dialect `migrations_test.go`
  `expectedIndexes` slices now assert it.
- `examples/auth-cms/internal/authmem/ports_v2.go` + `storetest/reference_test.go` — the two
  reference `ListBySubject` implementations gain the kind filter.
- `features/authentication/storetest/storetest.go` — three `ListBySubject` call sites pass
  `identity.KindEmail`; new `ListBySubjectKindIsolation` case proves the same string invited
  under email and phone resolves to disjoint sets.
- `features/authentication/internal/logic/invitationsvc/service_test.go` — `fakeInvRepo.
  ListBySubject` gains the kind filter; `TestAcceptNonEmailKindFork` (which asserted phone
  accept SKIPS the match) reworked into `TestAcceptPhoneKindMatchesVerifiedPhone` (V11 match +
  member-added notifier fork), plus new `TestAcceptPhoneKindMismatch`,
  `TestAcceptPhoneKindNoVerifiedPhone` (fail-closed, covers replaced/unverified), and
  `TestCreatePhoneNormalizesE164`.
- `features/authentication/authentication.go` — wired `invDeps.Normalizer` from
  `cfg.IdentifierNormalizer` (nil → invitationsvc's bundled default) and `invDeps.
  CallerIdentifiers` to a late-bound closure over `authService.ActiveVerifiedIdentifier`;
  `authService` is now declared before the invitation block and assigned (not `:=`) at
  construction, breaking the authsvc↔invitationsvc construction cycle (the closure only fires
  at request time).

Premise adaptations logged:

1. The design's re-key map (§7 V11 row) names the create-time normalization edge
   `invitationsvc → domain/user` "for strict E.164." Normalization moved to `domain/identifier`
   in phase 1 (the `identifier.Normalizer` seam, R9), so the edge implemented is the legal
   ordinary service→domain `invitationsvc → domain/identifier`. Same intent (one injected
   normalizer, strict E.164 phone), corrected home. The handoff flagged this to verify.
2. The design pins the phone accept-time match "against the caller's VERIFIED phone." Because
   V11 says the email path is unchanged and the email accept still uses the handler-passed
   email claim, the phone match was added as a service-injected `IdentifierLookup` (wired from
   `authsvc.ActiveVerifiedIdentifier`) rather than removing `AcceptInput.Identifier` — the
   more surgical shape that keeps the email path byte-identical.
3. The design's index note references "canonical 0011"; the invitations table is `0009` in the
   current greenfield tree, so the composite index landed in `0009_invitations.sql` per the
   standing greenfield-migration rule (no new migration file, filename parity preserved).

Behavior change logged: a phone-kind invitation can no longer be accepted by possession of the
delivered token alone — the accepting subject must own the invited number as an active verified
phone identifier (V11). Email invitation accept, `Mine`, auto-accept, and resolve-on-
registration remain email-only and unchanged. Create-time email normalization is now strict
addr-spec (via the injected normalizer) rather than the prior blind trim+lowercase; malformed
email invitations now fail Create with `sdk.ErrInvalidInput`.

Commands:

- `cd features/authentication && go test ./internal/logic/invitationsvc/... ./storetest -run 'Invitation|ListBySubject'`
  → ok (invitationsvc passes; storetest reports "no tests to run" because the suite entry point
  is `TestReference` — the `-run` filter matches top-level names, and the invitation cases are
  subtests under it). Verified the invitation storetest cases directly:
  `go test ./storetest -run 'TestReference/Invitations'` → PASS, including the new
  `ListBySubjectKindIsolation`.
- `make check` → all checks passed (per-module build/vet/test incl. inbound/authmem/storetest,
  templ drift, integration-tag vet, all layering/import guards).
- `gofmt -w` on changed non-generated files → clean.

Live legs: none required by this task (hermetic). The kind-filter fix rides the dual-dialect
live conformance at the phase gate (AV3-5.6).

What AV3-5.4 needs to know: `Login` still keys the limiter on the raw normalized email
(`loginKey(normalized, clientIP)` in `authsvc/service.go`) and the invitation audit `record`
writes `identifier` values verbatim into security-event details — both are raw-PII sites 5.4
must convert to the digest keyer. The single canonicalization seam is now shared three ways
(authsvc, invitationsvc, and the public `Config.IdentifierNormalizer`), so the 5.4 identifier
digest should key off the SAME normalized value these produce. Client IP still flows from the
`WithClientInfo` carrier (`clientInfoFromContext`), the seam 5.4 hardens against spoofed
forwarding headers.

### 2026-07-13 — AV3-5.4 (PII-free rate limits and trusted client IP)

Dependencies: AV3-5.1, AV3-0.2 (`NewHMACIdentifierKeyer`), AV3-0.5 (`web.TrustProxies`/
`web.ClientIP`, `clientIp` RemoteAddr fallback) all complete and checked off; phases 1–4
closed/gate-green. Worktree changes preserved; no resets. No reviewer/consultation agents
spawned (forbidden AV3-0.1..9.6).

Scope held to limiter key helpers, the production construction validation, and tests. The
transitional email-on-user removal (AV3-5.5) and the passwordless two-budget start endpoints
(phase 7) were left untouched.

Files changed:

- `features/authentication/internal/logic/authsvc/delivery.go` — extracted the shared
  `identifierDigest(kind, normalizedValue)` seam (keyed HMAC digest when `IdentifierKeyer` is
  wired, per-instance SHA-256 fallback otherwise — both PII-free). `idempotencyKey` now derives
  from it (the keyer path is byte-identical to before; the nil-keyer fallback digest input
  changed from `kind:value:purpose` to `digest(kind:value):purpose`, still stable/unique — no
  test asserts the exact fallback string).
- `features/authentication/internal/logic/authsvc/service.go` — `loginKey` converted from a
  free function embedding the raw normalized email to a `Service` method
  `s.loginKey(kind, normalizedValue, clientIP)` returning `login:<identifierDigest>|<ip>`.
  `Login`'s `limiter.Allow` re-keyed onto it (`identifier.KindEmail`).
- `features/authentication/internal/logic/authsvc/token.go` — `IssueToken`'s `limiter.Allow`
  re-keyed onto `s.loginKey` (already imported `identifier`).
- `features/authentication/security.go` — added `LimiterDurability`,
  `RateLimiterDurabilityReporter` (feature-side, because `ratelimiter.Limiter` is an sdk port
  and sdk cannot import the feature), `ErrNonDurableRateLimiter`, `validateRateLimiter`, and
  `limiterInProcessOnly`. Enforces the shared-limiter posture: production rejects an
  in-process-only limiter (the bundled `ratelimiter.Memory` default/concrete type, or one
  declaring `InProcessOnly`), tolerates a limiter that does not identify as in-process-only,
  and development warns. Imports sdk `ratelimiter`.
- `features/authentication/authentication.go` — wired the two always-on production gates after
  the limiter default: `validateRateLimiter(cfg.RuntimeMode, cfg.RateLimiter, transportLog)`
  (the un-defaulted `cfg.RateLimiter` is passed so nil reads as the in-process default) and the
  now-enforced `ErrIdentifierKeyerRequired` (`RuntimeMode==production && IdentifierKeyer==nil`).
  Both fire after the transport-security check so the existing transport-reject tests still get
  their error first.
- `features/authentication/security_test.go` — added `durableLimiter` (declares durable),
  `inProcessLimiter` (declares in-process), `prodKeyer` doubles; added `IdentifierKeyer`/durable
  `RateLimiter` to `prodDeliveryConfig()` and `TestNewServiceProductionAcceptsDeclaredTransports`
  so the delivery-focused production cases isolate their own dimension; new cases:
  default/explicit-memory and declared-in-process rejection, durable acceptance, keyer
  requirement, dev memory WARN, dev tolerates missing keyer.
- `features/authentication/internal/logic/authsvc/securityevent_test.go` — `loginKeyFor` helper
  over the existing `keyCapturingLimiter`; `TestLoginKeyIsPIIFree`,
  `TestLoginKeyEquivalentValuesShareBucket` (domain/local case variants → one bucket via the
  default normalizer), `TestLoginKeyUnknownAndKnownConsumeSameArm`.
- `features/authentication/internal/inbound/authentication/security_test.go` —
  `TestSpoofedForwardedHeaderCannotRotateLimiterBucket` proves `clientInfoMiddleware` stamps
  RemoteAddr (never a raw `X-Forwarded-For`) onto the carrier the login key reads.

Premise adaptations logged:

1. The AV3-5.3 handoff flagged the invitation audit `record` writing `identifier` verbatim as a
   raw-PII site for 5.4 to convert. Held OUT of scope: the AV3-5.4 Touch list is limiter key
   helpers / client-info middleware / construction validation (not audit), and design §5.1 WI3
   plus authsvc's own `recordLogin` deliberately carry the plaintext identifier in audit
   details ("an identifier, never a secret") so a failed-login/grant audit is useful. The
   invariant the design forbids from limiter keys/logs is raw PII in the *bucket*, not the audit
   identifier. Audit-detail identifiers are unchanged; the AV3-9.x retention/redaction policy
   owns PII-in-audit lifecycle.
2. Limiter durability read as the delivery-durability precedent (`DurabilityReporter`: reject a
   positively in-process limiter — the memory default and any reporter-declared in-process one —
   and tolerate a limiter that declares nothing) rather than the transport precedent (reject
   metadata-less). Reasons: "development memory limiter warns" names the memory limiter
   specifically as the non-durable case; a metadata-less durable host limiter (goredis) has no
   bundled satisfier and should not be forced to implement a feature interface; a non-shared
   limiter is an N×-budget hazard, not a secret leak. A host CAN positively declare durable or
   in-process via `RateLimiterDurabilityReporter`.
3. The design's "magic link / SMS OTP passwordless start endpoints get BOTH a per-identifier and
   a per-IP budget" (§4.4) is phase 7 (routes not yet built). Password `Login`/`IssueToken`
   behavior is "otherwise untouched" (§4.4): the single combined `(digest, IP)` key was retained
   and only the identifier part digested. No two-budget split was added here.

Commands:

- `cd features/authentication && go test ./...` → ok (all feature packages incl. inbound,
  authsvc, storetest, invitationsvc, root `authentication`).
- `cd features/authentication && go vet ./...` → clean.
- `go test ./... -run 'LoginKey|SpoofedForwarded|MemoryLimiter|Durable|IdentifierKeyer|RateLimiter'`
  → all new cases PASS (login key PII-free / equivalence / same-arm; spoofed-XFF; prod
  memory/in-process rejection; durable/keyer acceptance; dev warn/tolerate).
- `make check` → all checks passed (per-module build/vet/test, templ drift, integration-tag vet,
  all layering/import guards). `make check` runs the guard suite; all green.
- `gofmt -l` on changed files → clean.

Live legs: none required by this task (hermetic). Dual-dialect live conformance is the phase gate
(AV3-5.6).

What AV3-5.5 needs to know: no `users.email`/`GetByEmail`/verification-column reads were added or
removed here — the transitional dual-write in `Register`/`Verify` (`user.NewUser` with the email
arg, `MarkVerified`) is untouched and remains AV3-5.5's removal target. The login/token limiter no
longer keys on the raw email; nothing downstream reads `loginKey`'s old raw form. The production
profile now has two always-on gates (`ErrNonDurableRateLimiter`, enforced
`ErrIdentifierKeyerRequired`); AV3-5.5's schema/fixture churn stays in development RuntimeMode
(auth-cms is dev + wires the keyer) so it will not trip them. `examples/auth-cms` now emits an
in-process-rate-limiter WARN at dev startup (nil RateLimiter → memory) — expected, not a
regression.

### 2026-07-13 — AV3-5.5 (remove transitional email-on-user surface and finalize schema)

Dependencies: AV3-5.1 through AV3-5.4 complete and checked off (execution logs above); phases 1–4
closed/gate-green. Worktree changes preserved; no resets. No reviewer/consultation agents spawned
(forbidden AV3-0.1..9.6).

Removed the entire transitional email-on-user surface. `user.User` now carries only
`ID/DisplayName/AuthRevision/CreatedAt/UpdatedAt`; `user_identifiers` is the sole identity source.

Files changed / removed surface:

- `features/authentication/domain/user/user.go` — dropped `Email`/`EmailVerified` fields, the
  `NormalizeEmail` helper (moved to `domain/identifier` in phase 1), and `MarkVerified`. `NewUser`
  reshaped to `NewUser(ids, displayName, now) User` (no email arg, no error return — validation/
  normalization is the identifier's job now). Dropped the orphaned `fmt`/`net/mail`/`sdk` imports.
- `features/authentication/domain/user/repository.go` — removed the deprecated `Create` and
  `GetByEmail` methods from `UserRepository`; updated the sentinel-contract doc.
- `features/authentication/stores/pgx/users.go` + `stores/turso/users.go` — removed `Create` and
  `GetByEmail`; `userColumns`/`userRow`/`toDomain` reshaped to `id, display_name, auth_revision,
  created_at, updated_at`; `CreateWithPrimaryIdentifier` and `Update` INSERT/SET column lists drop
  email/email_verified. **Bug fixed while reshaping:** `Get` now reads `auth_revision` into the
  domain (the old `userColumns`/`userRow` omitted it, so live `Users.Get(...).AuthRevision` always
  returned 0 — a latent hazard for AV3-5.6's register→verify live leg, where `Verify` passes
  `u.AuthRevision` to `ApplyVerifiedChange`). `Update` still leaves `auth_revision` to the revision-CAS
  rails.
- `features/authentication/stores/{pgx,turso}/migrations/0001_users.sql` — greenfield canonical edit
  in place (filename sets stay byte-identical): the `users` CREATE drops `email`/`email_verified`,
  leaving stable subject/profile + `auth_revision`. Both `migrations_test.go` inventories already
  assert `auth_revision` (users) and no email column, so they pass unchanged; no renumbering needed.
- `features/authentication/internal/logic/authsvc/service.go` — `Register` builds the identifier
  first (it validates the address before any user row mints), calls `NewUser(displayName, now)`, and
  captures the returned primary's `NormalizedValue` (`primaryEmail`) for the invitation resolve and
  the delivery enqueue (was `created.Email`). `Verify` deleted the `MarkVerified`+`Update` dual-write
  and resolves the invitee's invitations against the `normalized` value.
- `features/authentication/internal/logic/authsvc/oauth.go` — `registerAndLink` builds the verified
  primary identifier, calls `NewUser("", now)`, and drops `MarkVerified`.
- `features/authentication/authentication.go` — `userLookup` re-keyed off `user.NormalizeEmail`+
  `users.GetByEmail` onto the identifier rail (injected normalizer + `GetLogin` then `GetRecovery`),
  wired from a resolved `idNormalizer` (nil → bundled default) shared with the invitation deps.
- `features/authentication/internal/inbound/authentication/sessions.go` (+ inbound `oauth.go`) — the
  compatibility `userResponse` DTO keeps its email-shaped JSON (design V9), now rendered by a new
  `handlers.userResponseFor` that sources email/`email_verified` from `ActiveVerifiedIdentifier`
  (falling back to the submitted request email with `email_verified=false` while the primary is still
  unverified, e.g. a just-registered account). Register/login/oauth-verify-link handlers route through
  it; the free `newUserResponse` is gone.
- Reference/fakes: `storetest/reference_test.go` (refUsers), `examples/auth-cms/internal/authmem/
  authmem.go` (userRepo), `internal/inbound/authentication/helpers_test.go` (memUsers +
  `activeAuthClaim`), `internal/logic/authsvc/service_test.go` (fakeUsers) — dropped `Create`/
  `GetByEmail` and the `u.Email` uniqueness loops; `CreateWithPrimaryIdentifier` now arbitrates
  solely on the identifier auth-claim (the users table has no email UNIQUE anymore). Dropped the
  now-unused `strings` imports in reference_test.go and authmem.go.
- Tests re-keyed off the removed fields: storetest `testUsersCRUD`/`testUsersAbsent`/
  `testUsersDBGeneratedID`/`seedUserWithIdentifier`/`seedCredentialUser`/rollback + the two
  `NewUser` `reference_test` literal, authsvc `TestRegister`/`TestVerify`/`TestVerifyWrongCodeLocksOut`/
  `TestVerifyApplyConflictIsRestartable`/`TestRegisterEmailShapedSignatureResolvesIdentifier` (renamed
  from `...CompatibilityDTORemainsEmailShaped`), the two rate-limit-short-circuit tests (now count
  `fakeIdentifiers.loginCalls`, not the removed `fakeUsers.calls`), oauth `mustOAuthUserVerified` +
  `TestOAuthCallbackRegisterAndLink` (verified-identifier assertion, not `res.User.EmailVerified`),
  `token_test` `mustVerify` (identifier normalizer, not `user.NormalizeEmail`), and the three
  `password_policy_test` account-existence probes (GetLogin, not GetByEmail).
- Dropped the `EmailUniqueness` storetest case: it exercised the `users.email` UNIQUE constraint,
  which no longer exists; the equivalent lost-authentication-claim collision is proven by
  `testIdentifiersCreateRollback`.
- `.claude/plans/authv3/host-upgrade-runbook-draft.md` — the two forward-references that called AV3-5.5
  "pending" are now landed; updated the no-blind-copy warning and the Step-6 closing note to state the
  canonical `0001_users.sql` is the final `id, display_name, auth_revision, created_at, updated_at`
  shape. Step 6 already carried the validated drop-column (pgx) / 12-step table-rebuild (SQLite) with
  the Step-4 backfill parity gate before removal, so no DDL change was required — the runbook's target
  users shape already matched what AV3-5.5 froze.

Required grep classification (`rg 'GetByEmail|EmailVerified|PhoneVerified|users\.email|email_verified'
features/authentication examples/auth-cms`) — zero `GetByEmail`, zero `users.email`, zero
`user.User.Email/EmailVerified`. Every remaining hit is intentional:
- `oauthaccount.ProviderEmailVerified` + `provider_email_verified` (domain/oauthaccount, both stores'
  oauth_accounts, oauth.go `providerIdentity.EmailVerified`, oauth_test, storetest, 0004 migrations):
  the OAuth **provider's** verified-provenance claim (design §5.7 gate), a typed OAuth-account column —
  not the removed user surface.
- `securityevent.TypeEmailVerified = "email_verified"` (domain, service.go, securityevent_test,
  README): the append-only **audit event kind** for a completed verification.
- `sessions.go` `userResponse.EmailVerified json:"email_verified"` + `userResponseFor`: the intentional
  V9 compatibility DTO, now identifier-sourced (the phase file explicitly permits the compat DTO name).
- README/oauth_test comment text: historical docs / provider-claim comment.

Premise adaptations logged:

1. The compatibility user DTO (`userResponse`, design V9) previously read `user.User.Email/
   EmailVerified`, which this task removes. Rather than break the public JSON contract (the inbound
   register test asserts `resp["email"]`), the DTO keeps its email/verified JSON shape and now sources
   both from `ActiveVerifiedIdentifier` — the authoritative post-v3 identity — falling back to the
   submitted request email (verified=false) while the primary is still unverified. The service Register/
   Login/IssueToken signatures were left byte-identical (V9 pins the request shape only); the sourcing
   moved to the transport helper, not a service-signature change. The AV3-5.1 service-level compat test
   `TestRegisterCompatibilityDTORemainsEmailShaped` (which asserted `user.User.Email`) was renamed to
   `TestRegisterEmailShapedSignatureResolvesIdentifier` and now proves the login-enabled identifier is
   the normalized identity and login resolves to the same subject.
2. `store.Get` did not read `auth_revision`. Fixing it is squarely inside "update queries, scans" and
   is needed for AV3-5.6's live register→verify leg; logged above under the store change.
3. `seedUserWithIdentifier`'s first parameter (the old per-user email) is retained as a call-site
   `label` (18 call sites) rather than churning every caller; it is `_`-discarded in the body since
   identity lives entirely on the identifier now.

Commands:

- `go build ./...` (workspace) → ok.
- `go vet ./...` (workspace) → clean.
- `cd features/authentication && go test ./...` → ok (all feature packages incl. authsvc, inbound,
  storetest, invitationsvc, root).
- `cd features/authentication/stores/pgx && go test ./...` / `.../stores/turso && go test ./...` → ok
  (migration inventory + parity green with the reshaped `0001_users.sql`).
- `cd examples/auth-cms && go build ./... && go vet ./... && go test ./...` → ok.
- `rg 'GetByEmail|EmailVerified|PhoneVerified|users\.email|email_verified' features/authentication
  examples/auth-cms` → only the intentional hits classified above.
- `make check` → all checks passed (per-module build/vet/test, templ drift, integration-tag vet, all
  layering/import guards).
- `make guard` → all guards green.
- `gofmt -l` on changed files → clean (service_test.go re-aligned after the `loginCalls` field add).

Live legs: none required by this task (hermetic removal). Dual-dialect live conformance on fresh/reset
DBs is the phase gate, AV3-5.6.

What AV3-5.6 needs to know: the canonical `users` table is now `id, display_name, auth_revision,
created_at, updated_at` in BOTH dialects — the Docker live DBs (`authv3-pg`, `authv3-libsql`) carry the
pre-AV3-5.5 schema (still with `email`/`email_verified` UNIQUE) and MUST be reset/recreated before the
live re-key proof, or `CreateWithPrimaryIdentifier`'s INSERT (no email column) will fail against the old
table. Editing `0001_users.sql` also invalidates the `schema_migrations` checksum any long-lived test DB
holds (the §2.5 caveat), reinforcing the fresh/reset requirement. `Users.Get` now returns the true
`auth_revision`, so the register→verify revision-CAS is live-correct. Identity is resolved only through
`user_identifiers`; no active code path reads identity from `users` (phase acceptance's "no active code
reads identity from `users`" holds).

### 2026-07-13 — AV3-5.6 (regression and live re-key proof) — PHASE 5 CLOSE

Dependencies: AV3-5.1 through AV3-5.5 all complete and checked off in `TASKS.md` with execution-log
entries above; phases 1–4 closed/gate-green. This is a proof/verification task — NO code changed
(the AV3-3.6 precedent). Worktree preserved (no resets): the substantial uncommitted AV3-0.x–5.5 work
and the unrelated auth-v2/JWT work are intact (166 tracked-modified/untracked entries before and after
the run; the finalized post-AV3-5.5 store/service/migration code was run as-is against fresh live DBs).
No reviewer/consultation agents spawned (forbidden AV3-0.1..9.6).

Proof model (AV3-3.6 precedent, logged there): the re-keyed FLOW proof is delivered by the storetest
conformance sub-runners running against fresh live pgx/turso — the re-keyed store rails (`UserIdentifiers`,
`Invitations` kind-filter, `CredentialMutations`, `Users` reshaped, `Challenges`, `PasswordResets`,
`OAuthAccounts`, `Sessions`) — complemented by the hermetic service/handler re-key flow tests
(`authsvc`/`invitationsvc`/`inbound`) added in AV3-5.1–5.4. The task-named fixtures map to existing live
leaves and hermetic cases, so a bespoke live driver would duplicate the frozen contract without adding
coverage; none was written.

Live environment (freshly reset moments before the run; both confirmed EMPTY pre-run — pg
`information_schema.tables` public count = 0, libsql `sqlite_master` table count = 0):
- Postgres 17 container `authv3-pg`; ran against the C-collation DB `authv3_cconf` (AV3-2.4 Finding-2
  byte-order pagination parity), DSN `postgres://postgres:postgres@localhost:5432/authv3_cconf?sslmode=disable`.
- libsql-server container `authv3-libsql` (recreated fresh) at `http://127.0.0.1:8080`,
  `TURSO_AUTH_TOKEN=local-dev` — the approved local substitute for a remote Turso DB (precedent AV3-2.4).
  Each `newRepos` applies the finalized canonical migrations (reshaped `0001_users.sql`) and truncates.

Commands / results:

- **hermetic — full feature suite** — `cd features/authentication && go test ./...` → **PASS** (all
  packages: authsvc, inbound, invitationsvc, delivery, storetest reference, root, domains).
- **hermetic — re-key flows under `-race`** —
  `go test -race -run 'Register|Login|Token|Forgot|Reset|Verify|Resolver|Resolve|OAuth|Adopt|PendingLink|Invitation|Accept|Multiple|Notification|SharedPhone|Primary' ./internal/logic/authsvc/... ./internal/logic/invitationsvc/... ./internal/inbound/authentication/...`
  → **PASS** (all three packages, no data race). Confirms the milestone properties at the service/handler
  layer, incl. the two task-named fixtures: `TestLoginResolvesAnyLoginEnabledIdentifier` (two
  login-enabled emails on one user), `TestNotificationOnlyPhoneSharedNotLoginClaim` (shared
  notification-only phone, projected as contact not a login claim); plus register→verify→login
  (`TestRegister`/`TestVerify`/`TestLogin`, `TestVerifyWrongCodeLocksOut`,
  `TestVerifyApplyConflictIsRestartable`), token issue (`TestIssueTokenRoundTrip` + gate/limit variants),
  forgot/reset (`TestForgotAndResetPassword`, `TestForgotPasswordEnqueuesOpaqueSamePath`,
  reset unknown/expired), OAuth existing-link/new-register/pending-adoption
  (`TestOAuthCallbackExistingLinkLogin`, `TestOAuthCallbackRegisterAndLink`,
  `TestOAuthCallbackPendingLinkThenVerify`, `TestOAuthAdoptionRevokesSquatterCredentials`,
  `TestOAuthAdoptionCapturedFlagTOCTOU`, `TestOAuthPendingLinkUsesCapturedIdentityAfterChange`,
  `TestOAuthCallbackUntrustedProviderRefused`, `TestOAuthCallbackUnverifiedEmailRefused`), resolver
  projection (`TestResolveProjectsAllVerifiedIdentifiersPrimaryFirst`,
  `TestResolveExcludesReplacedAndUnverified`, `TestResolveUnverifiedRegistrationProjectsNoAddress`), and
  invitation accept (`TestAcceptGrantsWithTupleArgs`, `TestAcceptIdentifierMismatch`, phone kind matches).
- **pgx live full suite (C-collation)** —
  `cd features/authentication/stores/pgx && POSTGRES_TEST_DSN='…/authv3_cconf…' go test -run TestConformance_Postgres ./...`
  → **PASS** (`ok … stores/pgx 8.218s`). Re-keyed rail leaves all green:
  `UserIdentifiers/{LoginRecoveryLookup,MultipleIdentifiersPerUser,SharedNotificationOnlyAddress,AuthenticationClaimCollision,ApplyVerifiedChange*,ConcurrentClaimArbitration}`,
  `Invitations/{CrossKindCoexistence,ListBySubjectKindIsolation}`, `CredentialMutations/*` (incl.
  `ConcurrentApplySingleWinner`), reshaped `Users/{CRUDRoundTrip,AbsentNotFound,DBGeneratedIDOnEmpty}`.
- **turso live full suite** —
  `cd features/authentication/stores/turso && TURSO_DATABASE_URL='http://127.0.0.1:8080' TURSO_AUTH_TOKEN='local-dev' go test -tags=integration -run TestConformance_Turso ./...`
  → **PASS** (`ok … stores/turso 10.286s`). Same re-keyed rail leaves green on the second dialect.
- **re-key CAS concurrency, `-race -count=5`, both dialects** —
  pgx `… go test -race -count=5 -run 'TestConformance_Postgres/(UserIdentifiers|CredentialMutations)/.*Concurrent' ./...` → **PASS** (3.109s);
  turso `… go test -tags=integration -race -count=5 -run 'TestConformance_Turso/(UserIdentifiers|CredentialMutations)/.*Concurrent' ./...` → **PASS** (3.300s).
  Single-winner integrity of the identifier auth-claim arbitration and revision-CAS on the finalized
  schema, 5× under the race detector on both dialects (turso rides the AV3-2.4 Finding-1 `BEGIN IMMEDIATE`
  connector fix).

Phase-5 close gate:

- `make check` → **PASS** (`all checks passed`: templ drift clean; per-module build/vet/test across every
  module incl. the auth feature, storetest reference + authmem hermetic conformance, both auth stores, and
  the auth-cms host; integration-tag compile-only vet incl. both auth stores; all guards green).
- `make guard` → **PASS** (exit 0; all guards green run standalone).
- Phase acceptance extras: both dialect live legs pass (recorded above); "no active code reads identity
  from `users`" holds — `rg 'GetByEmail|users\.email|\.EmailVerified|user\.User\{…Email'` over the feature
  and auth-cms shows zero `GetByEmail`/`users.email`, and the four `EmailVerified` hits are the OAuth
  PROVIDER's verified-provenance claim (`providerIdentity.EmailVerified` / `oauthaccount.ProviderEmailVerified`,
  §5.7 gate), not identity read from the `users` row (the AV3-5.5 classification).

Premise adaptations logged:

- **Proof delivered through the storetest conformance sub-runners + hermetic flow tests, not a bespoke
  live service driver** (AV3-3.6 precedent, verbatim rationale). The task enumerates register→verify→
  password-login, token issue, forgot/reset, OAuth existing-link/new-registration/pending-adoption,
  resolver projection, and invitation flows, "incl. two emails for one user and a shared notification-only
  phone fixture." Every store-contract half maps to a live conformance leaf
  (`UserIdentifiers/MultipleIdentifiersPerUser` and `.../SharedNotificationOnlyAddress` are the two named
  fixtures live on both dialects; `Invitations/ListBySubjectKindIsolation` is the kind-aware seam;
  `CredentialMutations/*` is the OAuth-adoption revision rail) and every service-flow half maps to an
  existing hermetic authsvc/invitationsvc/inbound test run above under `-race`. Standing up a full live
  `authentication.NewService` host to re-drive these would duplicate the frozen contract without adding
  coverage — no new test code was written.
- **C-collation `authv3_cconf` for the pg leg** (AV3-2.4 Finding-2 byte-order pagination parity
  precedent); the default `postgres` DB was left untouched. Both DBs confirmed empty pre-run.
- **Local libsql-server substitute for remote Turso** (AV3-2.4 precedent, orchestrator-approved). No
  containers stopped/removed; only the two DBs were used fresh (they were reset by the orchestrator moments
  before the run).

What phase 6 (AV3-6.1, recent-authentication grant service and routes in `07-credential-suite.md`) needs
to know — the overview allows phases 6 and 7 in either order after phase 5, which is now CLOSED:

- The whole re-keyed identity surface is live-conformant on both dialects on the FINALIZED schema:
  identity flows only through `user_identifiers`; `users` is the stable subject/profile + `auth_revision`
  anchor with no email/verification columns. Phase 6's step-up credential/identifier lifecycle builds on
  frozen, live-green ports — no store work is owed to it from phase 5.
- `CredentialMutations` (Snapshot + revision-CAS `Apply`) and `AuthenticationGrants` are live-conformant
  on both dialects (incl. `ConcurrentApplySingleWinner` under `-race`), and `ContactChanges` is live-green
  from phase 2 — the three rails the recent-authentication grant + credential/identifier mutation flows key
  on are ready. The turso rails rely on the AV3-2.4 Finding-1 `integrations/datastores/turso/tx.go`
  `BEGIN IMMEDIATE` connector mode; any new phase-6 read-then-write CAS on turso relies on it too.
- The OAuth adoption composition already exercises `revokeForAdoption` (sessions delete + credential-rail
  `RemovePassword` before link create); phase 6's set/change/remove-password and identifier-mutation
  routes reuse the same `credential.MutationRepository` revision rail and `authgrant` step-up seam.
- `ActiveVerifiedIdentifier(ctx, userID, kind)` (authsvc) is the single kind-aware active-verified accessor
  the invitation handlers and the resolver already key on; phase 6's identifier lifecycle should route
  through it rather than re-deriving. Client IP still flows from the `WithClientInfo`/`clientInfoFromContext`
  carrier hardened in AV3-5.4.
