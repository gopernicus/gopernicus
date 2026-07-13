# Phase 3 — challenge service and hardened recovery

Status: READY after phase 2.
Depends on: phases 0–2, including dual-dialect live evidence.
Design: §§3, 4.1 challenge rules, 5.8–5.9, 13 V8.

## Goal

Put every surviving verification/reset/sensitive secret on the hardened rail,
retire plaintext verification storage, and make password reset one atomic
composition across challenge, password, sessions, and grants.

## Task AV3-3.1 — challenge issue/consume/redeem service

Touch: `internal/logic/authsvc`, challenge domain helpers, public service seams,
tests.

Implement:

- package-private cryptographically secure generation for six-digit codes and
  256-bit URL tokens; never use `Config.IDs`;
- purpose metadata for format, TTL, attempts, and allowed caller path;
- issue through `ChallengeProtector` and atomic `Replace`;
- code consumption using all key-ID digest candidates and expected context
  digest;
- token redemption using SHA-256 digest, atomic delete-returning, then current
  binding validation before any business action;
- stable errors/events for expired, invalid, too many attempts, and success;
- no secret in events/logs/errors and no user-controlled purpose strings;
- tests for each purpose, old-key rotation, current identifier invalidation,
  context mismatch consumption, one-winner concurrency, and injected repository
  failures.

Verify:

```sh
cd features/authentication && go test -race ./internal/logic/authsvc/... ./domain/challenge ./storetest
make check
```

## Task AV3-3.2 — migrate registration verification

Depends on: AV3-3.1, phase-1 identifier contracts.
Touch: register/verify service and handlers, tests, templates only as needed for
the current synchronous seam (phase 4 moves transport).

Replace legacy verification-code issue/get/delete with purpose
`verify_registration`. Verification atomically consumes the code, verifies the
primary email identifier with a revision-CAS apply, and records the security
event. Wrong code increments attempts; lockout deletes. A post-consume apply
conflict is a safe restart/reissue path and must be tested.

Keep unauthenticated response/error uniformity. Do not yet introduce the phase-4
outbox into this task.

Verify:

```sh
cd features/authentication && go test ./internal/logic/authsvc/... ./internal/inbound/authentication/... -run 'Register|Verify'
make check
```

## Task AV3-3.3 — atomic password-reset composition

Depends on: AV3-3.1.
Touch: new `domain/passwordreset` repository contract, storetest reference and
authmem, pgx/turso adapters, auth service reset flow.

Implement a narrow composition repository operation that receives the reset
token digest, already-validated new password hash, current time, and any needed
protector metadata, then in one transaction:

1. deletes/returns a live `password_reset` challenge;
2. sets the typed `user_passwords` row;
3. deletes all user sessions;
4. deletes outstanding password/reset recent-auth grants/challenges; and
5. returns the user ID needed for notification/audit.

Unknown/expired/already-used tokens are generic failures. Inject a failure at
each statement boundary in reference/fake tests and prove full rollback. Run two
simultaneous resets with one token and prove exactly one commit. Do not mint a
session after reset.

Update forgot-password issue to use an active verified recovery identifier and
the challenge rail. Timing-safe asynchronous start comes in phase 4; until then
preserve the existing externally uniform response and mark the synchronous
timing debt in the task log.

Verify:

```sh
cd features/authentication && go test -race ./storetest ./internal/logic/authsvc/... -run 'PasswordReset|ForgotPassword'
cd features/authentication/stores/pgx && go test ./...
cd features/authentication/stores/turso && go test ./...
make check
```

## Task AV3-3.4 — password policy hardening

Depends on: AV3-3.3.
Touch: password validation/configuration and all register/set/change/reset
tests.

Implement:

- minimum 15 Unicode code points for a single-factor password;
- accepted maximum at least 64 and a finite pre-hash input cap;
- no arbitrary composition or periodic-rotation rules;
- optional host-injected compromised-password checker with explicit
  fail-open/fail-closed policy (production default must be documented and
  tested); and
- identical validation across register, set, change, and reset.

Do not add a network dependency to core. A remote breach-check integration is a
future adapter; local/custom checkers satisfy the port now.

Verify:

```sh
cd features/authentication && go test ./internal/logic/authsvc/... -run 'Password'
make check
```

## Task AV3-3.5 — retire the legacy verification rail

Depends on: AV3-3.2, AV3-3.3.
Touch: public repositories, `domain/verification`, both stores, storetest,
authmem, migrations, integration table inventories.

Remove `VerificationCodes`, `VerificationTokens`, their implementations,
storetest sub-runners/imports, and their canonical CREATE migrations. Renumber
both dialect trees identically. Update the phase-2 upgrade draft to drop old
tables only after application cutover and successful backfill/flow verification.

Verification grep:

```sh
rg 'VerificationCodes|VerificationTokens|domain/verification|verification_codes|verification_tokens' features/authentication examples/auth-cms
```

Only intentional host-upgrade/history text may remain.

## Task AV3-3.6 — live atomic recovery proof

Depends on: AV3-3.3, AV3-3.5.

On fresh pgx and turso databases, run registration verification, wrong-attempt
lockout, reset success, double reset contention, and injected/forced rollback
where the harness supports it. Confirm prior sessions fail immediately after
reset and the reset does not issue cookies/tokens. Record evidence.

## Phase acceptance

```sh
make check
make guard
```

Plus fresh live proof on both dialects and zero active legacy verification rail
references.

## Stop conditions

- Reset cannot be atomic across the four tables with a supported adapter:
  stop; do not fall back to consume-then-best-effort revoke.
- Password policy would silently truncate input in a hasher: stop and fix the
  hasher contract/integration explicitly.

## Execution log

Append dated entries per completed task.

### 2026-07-12 — AV3-3.1 (challenge issue/consume/redeem service)

Dependencies: phases 0, 1, and 2 all complete and gate-green — every phase-0/1/2
task is checked off in `TASKS.md` with execution-log entries (phase-2 close incl.
the AV3-2.4 Finding-1 turso-connector fix and dual-dialect live conformance). The
frozen, race-proven `challenge.Repository` port, the bundled
`NewHMACChallengeProtector`, and `DigestCandidate`/`ConstantTimeDigestEqual` are
all live. Worktree changes preserved (no resets); the pre-existing auth-v2/JWT
work and all AV3-0.x–2.x code are intact and built on. Scope held to the challenge
SERVICE + its enable-time wiring: no register/verify/forgot/reset re-key (AV3-3.2/
3.3 own those), no store work (phase 2 froze the ports), no new purpose constants
(`verify_registration`/`password_reset` land with their flows in 3.2/3.3).

Files changed:

- `features/authentication/internal/logic/authsvc/challenge.go` (new) — the
  challenge service surface. Package-private `generateCode` (crypto/rand-uniform
  six-digit) and `generateToken` (crypto/rand 32-byte → base64url), NEVER
  `Config.IDs` (D9/D10 opaque-secret rule). `challengeSpecs` is the closed
  purpose registry (format + TTL + attempt budget + allowed caller path): codes
  15m/OTP-5m with `MaxAttempts` lockout, magic link 15m token with none — the
  §3.2 TTL table. `IssueChallenge` mints+protects (`DigestCode` under the active
  key for codes, `DigestToken` for tokens) and atomically `Replace`s, returning
  the plaintext secret (never persisted/logged). `ConsumeChallenge` (code path)
  builds a candidate per accepted key (rotation), digests the expected context,
  and maps the atomic `ConsumeCode` `ConsumeOutcome` → stable error; a lockout
  records a `challenge_lockout` event with a purpose-only detail. `RedeemToken`
  (token path) SHA-256-digests, atomically delete-returns, resolves the user from
  the row, then validates the stored binding against the caller's current binding
  (`WithExpectedContext`) — a changed binding is `ErrChallengeInvalid` with the
  secret already spent. Stable errors `ErrChallengeExpired` (410/sdk.ErrExpired),
  `ErrChallengeInvalid` (400/sdk.ErrInvalidInput), `ErrTooManyAttempts`
  (403/sdk.ErrForbidden), `ErrUnknownChallengePurpose`. Context is a
  redemption-time binding validator only (SHA-256 digest for codes, canonical
  JSON blob for tokens) — never a payload channel; secrets never enter
  errors/events/logs; unknown purposes are rejected so no user-controlled purpose
  string can drive the rail. Local `challengeProtector` interface (accept
  interfaces) avoids the auth→authsvc import cycle.
- `features/authentication/internal/logic/authsvc/service.go` — added
  `Challenges challenge.Repository` + `Protector challengeProtector` to `Deps` and
  the `Service` struct/constructor wiring; imported `domain/challenge`.
- `features/authentication/authentication.go` — enable-time validation (design
  §3.3): `repos.Challenges != nil && cfg.ChallengeProtector == nil` →
  `ErrChallengeProtectorRequired`; wired `deps.Challenges`/`deps.Protector` from
  the Repositories/Config, keeping a genuine nil interface when the subsystem is
  off (deny-by-absence).
- `features/authentication/domain/securityevent/securityevent.go` — added
  `TypeChallengeLockout = "challenge_lockout"`, the challenge rail's own
  security-control event (the "(+ event)" of the §3.2 lockout row; business flows
  record their own domain events separately).
- `features/authentication/internal/logic/authsvc/challenge_test.go` (new) —
  deterministic `fakeProtector` (drives key rotation) + mutex-atomic
  `fakeChallenges` (mirrors the storetest reference, with injectable infra
  errors). Covers: issue/consume each code purpose + token purpose (single-use);
  old-key-still-verifies after rotation; code context match/mismatch (mismatch
  consumes); token current-binding invalidation (stale binding consumes);
  five-wrong-attempt lockout + secret-free event; code+token expiry; one-winner
  concurrency (code and token, `-race`, 24-way); injected Replace/ConsumeCode/
  ConsumeToken failures surface; unknown purpose on all three entry points; wrong
  caller path (format gate); empty-presented; subsystem-off fail-closed.
- `features/authentication/security_test.go` — `stubChallenges` +
  `TestNewServiceChallengeProtectorRequired` / `TestNewServiceChallengeSubsystemWiring`
  proving the enable-time gate and deny-by-absence tolerance.

Commands / results:

- `cd features/authentication && go test -race ./internal/logic/authsvc/... ./domain/challenge ./storetest`
  — **PASS** (`authsvc` and `storetest` ok under `-race`; `domain/challenge` has no
  test files — it is the entity/vocabulary package, its executable spec is the
  storetest conformance suite). `gofmt -l` on all touched files clean.
- `make check` — **PASS** (`all checks passed`: templ drift clean; per-module
  build/vet/test across every module incl. the reference + authmem hermetic
  conformance; integration-tag compile-only vet incl. both auth stores; all 13
  guards green — `make guard` runs inside `make check`).

Premise adaptations:

- **Enable trigger is `repos.Challenges != nil`.** §3.3 states the protector is
  "REQUIRED" flatly, while the frozen-slot comments say "Nil is tolerated until
  [phase 3]" and the overview's enable-time rule validates "only when their
  subsystem becomes enabled." Reconciled by gating `ErrChallengeProtectorRequired`
  on a wired Challenges repository — the concrete enable signal — matching the
  OAuth/machine/invitation deny-by-absence precedents in the same constructor. A
  host that wires no Challenges repo (the state until AV3-3.2 migrates a flow)
  keeps a nil protector without error; wiring the repo demands the protector.
- **`ConsumeChallenge`/`RedeemToken` take `...ChallengeOption` for the expected
  context.** The design's illustrative service signatures show `opts` only on
  `IssueChallenge`, but code-flow context validation must be "supplied to atomic
  ConsumeCode as an expected digest" and the token flow must validate the returned
  binding — neither is possible without a consume-time context input. Added
  `WithExpectedContext` (a consume-time twin of `WithStoredContext`); the atomic
  guarantees are unchanged (the code comparison still happens inside
  `ConsumeCode`; the token comparison still happens after the atomic
  delete-returning, on an already-consumed row).
- **Current-binding validation is caller-supplied, not a store lookup.** §3.2 says
  "the caller validates the returned binding against the current identifier"; the
  identifier resolver is not wired into `authsvc` until the phase-5 re-key, so
  `RedeemToken` validates the stored blob against a caller-supplied current
  binding (`WithExpectedContext`) rather than dragging the identifier store into
  authsvc early. The "current identifier invalidation" behavior is proven at the
  challenge-service level (changed binding → `ErrChallengeInvalid`, secret spent);
  phase 7 will supply the current identifier from its resolver.
- **`domain/challenge` shows "[no test files]" under the verify command.** It is
  the entity + vocabulary package (no repository logic of its own); its executable
  contract is the `storetest` conformance suite, which the command runs and which
  passes. Not a gap — the package has no unit-testable behavior beyond `Expired`,
  exercised transitively.

### 2026-07-12 — AV3-3.2 (migrate registration verification)

Dependencies: AV3-3.1 complete and gate-green (challenge service live; `verify_registration`
spec added here per its handoff), phase-1 identifier contracts frozen
(`identifier.IdentifierRepository.ApplyVerifiedChange`, `NewRegistrationEmail`,
`ApplyVerifiedChangeInput`). Worktree preserved — no resets; all AV3-0.x–3.1 work
intact and built on. Scope held to the register/verify SERVICE + handlers + tests +
the host protector wiring the migrated flow now requires; the legacy
`verification.Code`/`Token` rail is left wired-but-unused (retirement is AV3-3.5), and
the forgot/reset rail is untouched (AV3-3.3).

Files changed:

- `features/authentication/domain/challenge/challenge.go` — added
  `PurposeVerifyRegistration = "verify_registration"` (design §3.2 purpose list).
- `features/authentication/internal/logic/authsvc/challenge.go` — added the
  `verify_registration` `challengeSpec` (`formatCode`, 15m TTL, `MaxAttempts`
  lockout) per the AV3-3.1 handoff (purposes land with their flows).
- `features/authentication/internal/logic/authsvc/service.go` — `Deps.Identifiers`
  + `Service.identifiers` wiring; `Register` now issues a `verify_registration`
  challenge (`IssueChallenge`) instead of `verification.NewCode`, still writing the
  legacy email column via `users.Create` (the `CreateWithPrimaryIdentifier` re-key is
  AV3-5.x per §7). `Verify(ctx, email, code)` resolves the account by email, atomically
  consumes the code (wrong→attempt, 5th→`ErrTooManyAttempts` lockout), then claims and
  verifies the primary email identifier via the revision-CAS `ApplyVerifiedChange`
  (pure add, `ReplacesIdentifierID` empty, expected `AuthRevision` from the read),
  dual-writes the legacy `EmailVerified`, records `TypeEmailVerified`, and resolves
  invitations. New stable `ErrRegistrationVerificationConflict` (wraps `sdk.ErrConflict`
  → 409) covers a post-consume apply conflict as a safe restart/reissue.
- `features/authentication/authentication.go` — pass `repos.Identifiers` into
  `authsvc.Deps`; `Verify` wrapper signature `(ctx, email, code)`.
- `features/authentication/internal/inbound/authentication/sessions.go` —
  `authService.Verify(ctx, email, code)`, `verifyRequest` gains `email`, handler
  passes both. Route path `POST /auth/verify` unchanged.
- `features/authentication/internal/logic/authsvc/*_test.go` — `fakeIdentifiers`
  (revision-CAS over `fakeUsers`, injectable `applyErr`), `fakeUsers.applyRevision` +
  `Update` auth_revision preservation (pgx parity), `recordingMailer.codeFor`; wired
  the challenge/protector/identifier rails into every harness that drives Register/
  Verify (`newHarnessDeps`, `serviceWithResolver`, `newTokenHarness`, `newMachineHarness`,
  the two securityevent NewService sites, the inline TTL harness). Rewrote/added
  `TestRegister` (challenge issued, no legacy code), `TestVerify` (identifier claimed
  + verified + revision bumped + dual-written flag), `TestVerifyUnknownAccount`
  (generic `ErrChallengeInvalid`), `TestVerifyWrongCodeLocksOut`,
  `TestVerifyApplyConflictIsRestartable`.
- `features/authentication/internal/inbound/authentication/helpers_test.go` +
  `sessions_test.go` — `memChallenges`/`memProtector`/`memIdentifiers` + `memUsers`
  revision handling wired into `newTestHandler`; `TestVerifyRouteUnknown` now posts
  `{email, code}` and asserts 400 (challenge_invalid, enumeration-uniform) rather
  than the old 404.
- `examples/auth-cms/cmd/server/{demo.go,main.go}` — `buildChallengeProtector`
  (bundled HMAC protector from `AUTH_CHALLENGE_HMAC_KEY` hex, or an ephemeral
  dev/single-instance key) wired into `auth.Config.ChallengeProtector`. The host has
  wired `Challenges` since AV3-1.4 but no protector, so the AV3-3.1 enable-gate would
  have failed `auth.NewService` at runtime; the register/verify migration makes the
  rail load-bearing, so the host now supplies it (also unblocks AV3-3.6 live proof).

Commands / results:

- `cd features/authentication && go test ./internal/logic/authsvc/... ./internal/inbound/authentication/... -run 'Register|Verify'`
  — **PASS** (both packages; `TestRegister`, `TestVerify`, `TestVerifyUnknownAccount`,
  `TestVerifyWrongCodeLocksOut`, `TestVerifyApplyConflictIsRestartable`,
  `TestVerifyRouteUnknown`, plus the invitation/securityevent/oauth Register/Verify
  cases). `gofmt -l` on all touched files clean.
- `make check` — **PASS** (`all checks passed`: templ drift clean; per-module
  build/vet/test across every module incl. the auth feature, storetest reference +
  authmem hermetic conformance, and the auth-cms host; integration-tag compile-only
  vet incl. both auth stores; all 13 guards green — `make guard` runs inside it).

Premise adaptations:

- **Register keeps the legacy email column; Verify claims the identifier as a pure
  add.** The §7 re-key map assigns `CreateWithPrimaryIdentifier`/email-column removal
  to B5 (phase 5), while this phase-3 task requires `ApplyVerifiedChange`. Reconciled
  by having Register write `users.Create` (legacy) and Verify model the first email
  claim as `ApplyVerifiedChange` with an empty `ReplacesIdentifierID` and
  `expectedAuthRevision = 0` (a fresh user's revision) — the reference/authmem apply
  arbitrate the auth-claim and bump the revision exactly once. AV3-5.x flips Register
  to `CreateWithPrimaryIdentifier`/`NewRegistrationEmail` and Verify to a
  replace-the-pending-registration-identifier apply.
- **`Verify` gained an `email` parameter.** Challenge codes are keyed by
  `(user, purpose)`, so the v1 global plaintext-code lookup is structurally
  impossible; Verify resolves the account by email, then consumes. An unknown/malformed
  account collapses to the generic `ErrChallengeInvalid` (design §5.8 enumeration
  uniformity). The route path is unchanged; the request body adds `email`.
- **Transitional dual-write of the legacy `EmailVerified` flag.** Login still gates on
  the email column until the AV3-5.x login re-key, so Verify writes both the verified
  identifier and `users.EmailVerified`. pgx `UserStore.Update` does not write
  `auth_revision`, so the dual-write cannot clobber the CAS anchor (the fakes were
  aligned to preserve it on Update).
- **No new hard construction requirement.** Challenges/Identifiers were NOT promoted to
  unconditional `auth.NewService` requirements (that would break the empty-`Repositories`
  construction-negative tests and every authsvc harness that builds a service for an
  unrelated subsystem). The existing enable-gate (`Challenges != nil → ChallengeProtector
  required`) is unchanged; Register/Verify assume the rails are wired exactly as they
  already assume Users/Passwords. Design §8's "required Identifiers/Challenges" lands at
  the phase-5 finalize.
- **Synchronous delivery seam retained.** Register still mails the code through the
  existing `sendVerificationEmail`/Mailer path; the phase-4 durable outbox is
  explicitly out of scope for this task (per the task body).

### 2026-07-12 — AV3-3.3 (atomic password-reset composition)

Dependencies: AV3-3.1 complete and gate-green (challenge service + `RedeemToken`/
`DigestToken` live), AV3-3.2 complete and checked off (register/verify on the rail;
`Deps.Identifiers` + host `buildChallengeProtector` wired). Phases 0–2 closed. Worktree
preserved — no resets; all AV3-0.x–3.2 work intact and built on. Scope held to the reset
composition + forgot-password re-key + tests; the legacy `verification.Code`/`Token` rail
stays wired-but-unused (retirement is AV3-3.5).

Files changed:

- `features/authentication/domain/passwordreset/{passwordreset.go,repository.go}` (new) —
  the narrow composition domain (design §5.9). `RedeemInput` carries the reset token
  DIGEST, the already-validated `NewPasswordHash`, the `PurgeChallengePurposes` set, and
  `Now`; `RedeemResult` returns only the user ID (the reset never mints a credential).
  `Repository.Redeem` is the whole surface: ONE atomic op that consumes the live
  `password_reset` challenge, sets the typed password row, and revokes all sessions +
  outstanding recent-auth grants + password/reset challenges, or none of them. A non-live
  challenge (unknown/consumed/expired) → `sdk.ErrNotFound` with no changes. This is the
  freeze-transfer rule (§3.1): reset's cross-aggregate consume semantics get their OWN
  domain rather than widening the generic challenge port with a callback.
- `features/authentication/domain/challenge/challenge.go` — added
  `PurposePasswordReset = "password_reset"` (design §3.2 purpose list).
- `features/authentication/internal/logic/authsvc/challenge.go` — added the
  `password_reset` `challengeSpec` (`formatToken`, `passwordResetTTL` = 1h, no lockout —
  the 256-bit token space is the defense, §3.2 TTL table).
- `features/authentication/internal/logic/authsvc/service.go` — `Deps.PasswordResets` +
  `Service.passwordResets` wiring. `ForgotPassword` re-keyed onto `GetRecovery`: it resolves
  an ACTIVE VERIFIED email recovery identifier (§2.3/§7 email-only), then issues a
  `password_reset` token challenge and mails it; a malformed/absent/unverified recovery
  identifier stays a silent no-op (enumeration-uniform). `ResetPassword` now validates +
  hashes the password, digests the token through the protector, and calls
  `passwordResets.Redeem` with `passwordResetPurgePurposes` = `{password_reset,
  remove_password}`; a non-live token collapses to the new stable
  `ErrPasswordResetInvalid` (wraps `sdk.ErrNotFound` → 404, preserving the external route
  contract and enumeration uniformity), and it NEVER mints a session. `sendResetEmail` now
  takes a recipient address (was a `user.User`).
- `features/authentication/authentication.go` — `Repositories.PasswordResets` slot +
  `authsvc.Deps.PasswordResets` wiring.
- `features/authentication/stores/pgx/password_resets.go` + `stores/turso/password_resets.go`
  (new) + `postgres.go`/`turso.go` wiring — the dialect adapters. Each does the whole
  composition in ONE transaction: a guarded `DELETE … WHERE purpose=? AND secret_digest=?
  AND expires_at>? RETURNING user_id` consumes the live challenge (unknown/expired/used all
  return no row → `sdk.ErrNotFound`), then the `user_passwords` upsert and the
  `sessions`/`authentication_grants` deletes by `user_id` and the challenge purge (pgx
  `= ANY(@purposes)`, turso a built `IN (?,…)` list). The `(purpose, secret_digest)` unique
  index + transactional/serialized writes give exactly-one-winner under concurrent
  redemption and full rollback on any statement failure.
- `features/authentication/storetest/{storetest.go,reference_test.go}` — a `PasswordResets`
  sub-runner (skips LOUDLY when unwired) with success (full composition + single-use),
  unknown/empty-digest, expired, and concurrent-single-winner cases run against
  reference + authmem + both dialects. `refPasswordResets` applies the five statements with
  a snapshot/restore and an injectable `resetFailAt`; `TestPasswordResetRollback` proves a
  failure at EACH of the five boundaries leaves NO partial state (password, sessions,
  grant, and both challenges all survive) and that the un-injected path commits every
  effect once.
- `examples/auth-cms/internal/authmem/{ports_v3.go,authmem.go}` — `passwordResetRepo` (the
  whole composition under authmem's one shared mutex) + `Repositories()` wiring, so the
  exported storetest suite proves the atomic-reset conformance against the proof-host
  memstore too.
- Test harnesses: `fakePasswordResets` (authsvc) / `memPasswordResets` (inbound) compose
  the challenge/password/session fakes into the functional reset and are wired into the
  reset-driving harnesses; `takeLiveToken`/`purgeUserPurposes` helpers added to the
  challenge fakes. Rewrote `TestForgotAndResetPassword` (verify → live session → reset →
  sessions revoked, none minted, pre-reset refresh dead, new password logs in), added
  `TestForgotPasswordUnverifiedNoReveal` and `TestResetPasswordUnknownToken`, rewrote
  `TestResetPasswordExpiredToken` onto the challenge rail, and updated
  `TestSecurityEventPasswordReset`; the inbound `TestResetPasswordRouteUnknownToken` still
  asserts 404.

Commands / results:

- `cd features/authentication && go test -race ./storetest ./internal/logic/authsvc/... -run 'PasswordReset|ForgotPassword'`
  — **PASS** (storetest reference incl. `TestPasswordResetRollback` + the `PasswordResets`
  conformance sub-runner; authsvc reset/forgot cases, all under `-race`).
- `cd features/authentication/stores/pgx && go test ./...` — **PASS** (hermetic; the live
  DSN conformance leg skips without a database — recorded, that is AV3-3.6's job).
- `cd features/authentication/stores/turso && go test ./...` — **PASS** (hermetic; live
  leg skips).
- `make check` — **PASS** (`all checks passed`: templ drift clean; per-module build/vet/test
  across every module incl. the auth feature, storetest reference + authmem hermetic
  conformance, and the auth-cms host; integration-tag compile-only vet incl. both auth
  stores; all 13 guards green — `make guard` runs inside it).

Premise adaptations:

- **Non-live tokens are one generic `sdk.ErrNotFound` via a guarded delete, not a
  delete-returning-then-branch.** The task says "unknown/expired/already-used tokens are
  generic failures"; rather than delete an expired row and return `ErrExpired` (which under
  a real transaction would roll the delete back anyway), the stores use a single guarded
  `… AND expires_at > now RETURNING user_id`, so all three non-live cases return no row →
  `sdk.ErrNotFound` uniformly and expired rows are left for `PurgeExpired`. The service maps
  `ErrNotFound`/`ErrExpired` to the stable `ErrPasswordResetInvalid`.
- **Grant revocation is by `user_id` (all the user's grants), not purpose-scoped.** The task
  step 4 says "password/reset recent-auth grants", but no grant-purpose constants exist yet
  (phase 6 owns grant issuance) and the same transaction revokes EVERY session, so any
  surviving grant is already dangling. Deleting all of the user's grants is a strictly-safer
  superset and keeps the op self-contained. Challenge purge IS purpose-scoped
  (`{password_reset, remove_password}`) since those constants exist.
- **Rollback-at-each-boundary is proven in the reference, not the live stores.** Real
  pgx/turso stores expose no mid-transaction injection seam, so the "inject a failure at
  each statement boundary and prove full rollback" requirement is met by the reference's
  snapshot/restore `resetFailAt` harness (`TestPasswordResetRollback`, all 5 boundaries).
  The shared conformance proves success, generic-failure, and concurrent-single-winner
  against the real dialects; the live all-or-nothing/forced-rollback proof is AV3-3.6.
- **Forgot-password resolution moved to `GetRecovery`, email-only.** Per the task ("active
  verified recovery identifier and the challenge rail") and §7 (recovery stays email-only in
  v3), `ForgotPassword` looks up `GetRecovery(KindEmail, normalized)` and requires
  `Verified()`; `Config.PasswordRecovery` kind policy remains deferred.
- **Synchronous-timing debt recorded.** The start path still resolves the recovery
  identifier and issues/sends synchronously, so known vs unknown addresses differ in
  latency though the response body is uniform. The durable, timing-safe outbox that removes
  the signal is phase 4 (§6.1.1); the debt is noted in the `ForgotPassword` doc comment.
- **`PasswordResets` is NOT promoted to an unconditional construction requirement.** Like
  Challenges/Identifiers in AV3-3.2, it is assumed wired wherever the forgot/reset flow is
  active (`ResetPassword` fails closed with `sdk.ErrForbidden` if nil); the design §8
  "required PasswordResets" lands at the phase-5 finalize so the empty-`Repositories`
  construction-negative tests stay green.

### 2026-07-12 — AV3-3.4 (password policy hardening)

Dependencies: AV3-3.3 complete and gate-green (atomic reset composition + forgot/reset on
the challenge rail; `ResetPassword` already routes the new password through
`validatePassword` before hashing, so the hardening applies to register/change/reset with
no reset-specific change). Phases 0–2 closed, AV3-3.1–3.3 checked off. Worktree preserved —
no resets; all AV3-0.x–3.3 work intact and built on. Scope held to the password
validation/configuration and the register/change/reset tests; no new construction
requirement, no host wiring change (the checker is optional, nil = length policy only).

Files changed:

- `features/authentication/internal/logic/authsvc/service.go` — replaced the flat
  `minPasswordLength = 8` byte floor with the §5.9 policy: `minPasswordCodePoints = 15`,
  `maxPasswordCodePoints = 64`, and `maxPasswordInputBytes = 256` (the finite pre-hash
  cap). `validatePassword` became a `*Service` method taking `ctx`: it checks the byte cap
  first (a pathological megabyte input never reaches the rune counter or the hasher), then
  the code-point min/max via `utf8.RuneCountInString` (code points, not bytes — the v3 floor
  is Unicode-aware), then — LAST, only after the cheap gates — a wired
  `compromisedChecker`. No composition/rotation rules. Added the internal
  `compromisedChecker` port (structural twin of `auth.CompromisedPasswordChecker`),
  `Deps.Compromised`/`Deps.CompromisedFailOpen` + the `Service` fields/wiring, and the
  stable `ErrPasswordCompromised` (wraps `sdk.ErrInvalidInput` → 400). Fail-closed is the
  default: a checker that cannot COMPLETE rejects the password (wrapping the cause) so an
  unavailable breach service is never a silent bypass; `CompromisedFailOpen` trades that for
  availability with a WARN. All three entry points (`Register`, `ChangePassword`,
  `ResetPassword`) now call `s.validatePassword(ctx, …)`, so the policy cannot drift.
- `features/authentication/authentication.go` — added the public
  `CompromisedPasswordChecker` port (OPTIONAL, host-injected; core ships none → no network
  dependency), `Config.CompromisedPasswordChecker` + `Config.CompromisedPasswordFailOpen`
  (documented production default: fail closed), wired both into `authsvc.Deps` (the checker
  guarded like the challenge protector so the field stays a genuine nil interface when
  unset).
- `features/authentication/internal/logic/authsvc/password_policy_test.go` (new) —
  `stubChecker` + `serviceWithChecker`; `TestPasswordPolicyLengthIdenticalAcrossFlows`
  (table over register/change/reset: 14/15/64/65 ASCII, a 100k-byte over-cap case, and
  14/15/64/65 multibyte-rune cases proving the min counts code points, not bytes — all three
  flows agree per candidate); `TestPasswordCompromisedRejected` (compromised verdict →
  `ErrPasswordCompromised`/400, no account, no mail; same across change and reset);
  `TestPasswordCompromisedCleanPasses`; `TestPasswordCompromisedCheckerFailClosed` (the
  documented production default — checker down → rejected, cause wrapped, no account);
  `TestPasswordCompromisedCheckerFailOpen` (opt-in availability trade → accepted); and
  `TestPasswordCheckerNotConsultedWhenLengthInvalid` (breach check runs last).
- register/change/reset test password literals across the authsvc + inbound test trees
  (`service_test.go`, `token_test.go`, `machine_test.go`, `invitation_test.go`,
  `securityevent_test.go`, `refresh_test.go`, inbound `sessions_test.go`/`token_test.go`/
  `machine_test.go`) — bumped the policy-valid fixtures to ≥15 code points
  (`password123`→`password123456789`, `newpassword456`→`newpassword456789`,
  `brandnewpass`→`brandnewpass1234`, `finalpass789`→`finalpass789012`,
  `password456`→`password456789012`, `newpassword`→`newpassword12345`), quote-anchored so
  the JSON `"password":` key and wrong-password probes (`short`, `wrongpassword`) were left
  untouched.

Commands / results:

- `cd features/authentication && go test ./internal/logic/authsvc/... -run 'Password'`
  — **PASS** (the new policy suite plus the existing forgot/reset/change cases). Full
  feature module `go test ./...` also PASS (authsvc, inbound, storetest reference + authmem
  conformance, invitationsvc, delivery). `gofmt -l` on all touched non-generated files clean.
- `make check` — **PASS** (`all checks passed`: templ drift clean; per-module build/vet/test
  across every module incl. the auth feature, storetest reference + authmem hermetic
  conformance, and the auth-cms host; integration-tag compile-only vet incl. both auth
  stores; all 13 guards green — `make guard` runs inside it).

Premise adaptations:

- **"set" password shares the validator ahead of its flow.** The task lists "register, set,
  change, reset"; the set-initial-password flow is §5.2 / AV3-6.3 (phase 6) and does not
  exist yet. `validatePassword` is now the single `*Service` method every password entry
  point routes through, so AV3-6.3's set flow inherits the identical policy for free; the
  three live flows (register/change/reset) are proven identical here.
- **Accepted maximum = 64 code points; finite pre-hash cap = 256 bytes.** §5.9 says "at
  least 64" (code points, matching the code-point minimum) and "length-bounded before
  expensive hashing". A 64-code-point password is ≤256 UTF-8 bytes, so the byte cap only
  fires on pathological over-long input; it is a genuine finite DoS guard, not a second
  length rule. The no-silent-truncation stop condition holds end to end: over-cap input is
  rejected here, and the bundled bcrypt integration already returns `ErrPasswordTooLong`
  (never truncates) past its 72-byte limit — so a 64-multibyte-code-point password that
  exceeds 72 bytes fails LOUDLY at the hasher, never silently.
- **Fail-closed is the production default; not construction-enforced.** The task requires an
  "explicit fail-open/fail-closed policy (production default documented and tested)".
  `CompromisedPasswordFailOpen` defaults to false = fail closed (an unavailable breach
  service rejects, consistent with the §8/V15 fail-closed profile); both behaviors are
  tested. Production does NOT reject an explicit fail-open at construction — that would be
  scope creep beyond "documented and tested" and risks the construction-negative matrix;
  flagged as an optional future strict-production tightening.
- **No new network dependency; checker stays optional.** The compromised-password port is
  host-injected and the core ships no implementation, so the sdk-only/feature-core-import
  invariants are untouched. The auth-cms host was NOT wired with a checker (nil → length
  policy only) — a remote breach-check adapter is a future integration, as the task directs.

### 2026-07-12 — AV3-3.5 (retire the legacy verification rail)

Dependencies: AV3-3.2 and AV3-3.3 complete and checked off — registration verification and
password reset both run on the atomic challenge/passwordreset rails, so the legacy
`domain/verification` rail (`CodeRepository`/`TokenRepository`, `Deps.Codes`/`Tokens`) was
wired-but-unused (confirmed: `s.codes`/`s.tokens` had zero readers, `verificationCodeTTL` was
lint-dead). Phases 0–2 closed. Worktree preserved — no resets; all AV3-0.x–3.4 work and the
unrelated auth-v2/JWT work intact. Scope held to removing the rail: no schema behavior change
to any surviving table, no touch to the deprecated `users.email` surface (that is AV3-5.5).

Files removed:

- `features/authentication/domain/verification/{verification.go,repository.go}` — the whole
  legacy domain package (Code/Token entities + Code/TokenRepository ports).
- `features/authentication/stores/pgx/verification.go` and
  `features/authentication/stores/turso/verification.go` — the CodeStore/TokenStore dialect
  adapters.
- `features/authentication/stores/{pgx,turso}/migrations/0004_verification_codes.sql` and
  `0005_verification_tokens.sql` — the canonical CREATE migrations (both dialect trees).

Migrations renumbered identically in BOTH dialect trees (byte-for-byte identical filename
sets preserved): `0006…0016` → `0004…0014` (oauth_accounts 0004, oauth_states 0005,
service_accounts 0006, api_keys 0007, security_events 0008, invitations 0009,
user_identifiers 0010, challenges 0011, contact_changes 0012, authentication_grants 0013,
delivery_jobs 0014). Renames used `git mv` for the tracked 0006–0011 files and plain `mv` for
the still-untracked AV3 0012–0016 files (worktree work not yet committed).

Files changed:

- `features/authentication/authentication.go` — dropped the `VerificationCodes`/
  `VerificationTokens` `Repositories` slots, their `authsvc.Deps` wiring, and the
  `domain/verification` import.
- `features/authentication/internal/logic/authsvc/service.go` — dropped `Deps.Codes`/`Tokens`,
  the `Service.codes`/`tokens` fields + constructor wiring, the dead `verificationCodeTTL`
  const, and the `domain/verification` import.
- `features/authentication/stores/pgx/postgres.go` + `stores/turso/turso.go` — dropped the
  `VerificationCodes`/`VerificationTokens` store wiring.
- `features/authentication/stores/{pgx,turso}/migrations_test.go` — `canonicalMigrations`
  renumbered to the new 14-file set; `verification_codes`/`verification_tokens` removed from
  `expectedTables`. The pgx/turso parity + inventory tests re-pass on the renumbered trees.
- `features/authentication/stores/pgx/conformance_test.go` +
  `stores/turso/conformance_integration_test.go` — the two verification tables removed from the
  live-truncation `authTables` inventory.
- `features/authentication/storetest/{storetest.go,reference_test.go}` — removed the
  `VerificationCodes`/`VerificationTokens` sub-runners and their six `testCodes*`/`testTokens*`
  bodies, the reference `codes`/`tokens` maps + `refCodes`/`refTokens` adapters + `repositories()`
  wiring, and the `domain/verification` imports; refreshed the package/port doc lines that named
  codes/tokens.
- `examples/auth-cms/internal/authmem/authmem.go` — removed the `codes`/`tokens` maps,
  `codeRepo`/`tokenRepo` adapters, `Repositories()` wiring, the import, and the doc-comment
  mentions.
- authsvc + inbound test harnesses (`service_test.go`, `token_test.go`, `password_policy_test.go`,
  `securityevent_test.go`, `challenge_test.go`, `invitation_test.go`, `oauth_test.go`,
  `machine_test.go`; inbound `helpers_test.go`, `token_test.go`, `invitation_test.go`,
  `oauth_test.go`, `machine_test.go`) — removed the `fakeCodes`/`fakeTokens`/`memCodes`/`memTokens`
  doubles, every `Codes:`/`Tokens:` Deps literal, the harness `codes`/`tokens` fields, the two
  compile-time seam asserts, and the now-unused `domain/verification` imports; dropped
  `TestRegister`'s legacy-code-count assertion (the challenge-count assertion stays).
- `features/authentication/README.md` — removed `verification/` from the domain rim listing and
  the `VerificationCodes`/`VerificationTokens` slots from the `Repositories` example.
- `.claude/plans/authv3/host-upgrade-runbook-draft.md` — strengthened Step 6's precondition so
  the `DROP TABLE verification_codes`/`verification_tokens` cutover runs only after the v3
  binary is stable AND registration-verification + forgot/reset are verified end to end on the
  challenge rail with Step-4 parity still holding (the "drop old tables only after cutover and
  successful backfill/flow verification" requirement). The drops were already in the post-cutover
  Step 6, not the additive Steps 1–5.

Commands / results:

- `rg 'VerificationCodes|VerificationTokens|domain/verification|verification_codes|verification_tokens' features/authentication examples/auth-cms`
  — **zero matches** (only general "email verification" prose remains elsewhere; no active rail
  reference).
- `cd features/authentication && go build ./... && go vet ./... && go test ./...` — **PASS**
  (feature core, inbound, authsvc, storetest reference + delivery/invitation).
- `cd features/authentication/stores/pgx && go build/vet/test ./...` — **PASS** (hermetic;
  migration inventory/parity green on the renumbered tree; live DSN leg skips).
- `cd features/authentication/stores/turso && go build/vet/test ./...` — **PASS** (hermetic; live
  leg skips).
- `cd examples/auth-cms && go build/vet/test ./...` — **PASS** (authmem hermetic conformance).
- `make check` — **PASS** (`all checks passed`: templ drift clean; per-module build/vet/test
  across every module incl. the auth feature, storetest + authmem hermetic conformance, both auth
  stores' integration-tag vet; all 13 guards green — `make guard` runs inside it).

Premise adaptations:

- **Migration renumbering used mixed `git mv`/`mv`.** The 0006–0011 canonical files were tracked
  (committed in earlier phases) so `git mv` moved them; the 0012–0016 AV3 files (user_identifiers
  … delivery_jobs) are still uncommitted worktree work, so `git mv` refused them and plain `mv`
  was used. The resulting on-disk trees are byte-for-byte identical filename sets across dialects
  (asserted green by `TestMigrationParity`); the staging-status difference is cosmetic to git.
- **The runbook already dropped the tables post-cutover.** The AV3-2.5 draft placed
  `DROP TABLE verification_codes/verification_tokens` in the destructive Step 6 (only after v3 is
  stable), not the additive Steps 1–5, so the task's "drop only after application cutover and
  successful backfill/flow verification" was structurally already met; the edit made the
  flow-verification precondition explicit rather than restructuring the runbook.

Handoff to AV3-3.6 (live atomic recovery proof):

- The challenge rail is now the ONLY secret rail; no verification tables exist to fall back on.
  Fresh databases must be migrated from the renumbered canonical trees (14 files, `0001…0014`),
  which no longer create `verification_codes`/`verification_tokens`. A database migrated before
  this task still carries those two tables — AV3-3.6 must use FRESH/reset databases (the running
  `authv3-pg` incl. the C-collation `authv3_cconf`, and `authv3-libsql` at
  `http://127.0.0.1:8080`) so the schema matches the new canonical set.
- `stores/{pgx,turso}/conformance_*` truncation inventories no longer list the two verification
  tables, so the live conformance harness truncates exactly the 14 canonical tables.

### 2026-07-12 — AV3-3.6 (live atomic recovery proof) — PHASE 3 CLOSE

Dependencies: AV3-3.3 and AV3-3.5 complete and checked off (atomic reset composition on the
challenge/passwordreset rails; legacy verification rail retired; canonical trees renumbered to
14 files `0001…0014`). Phases 0–2 closed with dual-dialect live evidence. This is a
proof/verification task — NO code changed. Worktree preserved (no resets): the substantial
uncommitted AV3-0.x–3.5 work and the unrelated auth-v2/JWT work are intact; the phase-3
migration/store/service code was run as-is against fresh live databases.

Live environment (freshly reset before the run, no stale pre-renumbering schema):
- Postgres 17 container `authv3-pg`; ran against the C-collation DB `authv3_cconf` (empty public
  schema confirmed: `information_schema.tables` count = 0 before the run) for byte-order
  pagination parity (AV3-2.4 Finding 2). DSN
  `postgres://postgres:postgres@localhost:5432/authv3_cconf?sslmode=disable`.
- libsql-server container `authv3-libsql` (recreated fresh) at `http://127.0.0.1:8080`,
  `TURSO_AUTH_TOKEN=local-dev` — the approved local substitute for a remote Turso DB (precedent
  AV3-2.4). Each `newRepos` call applies the 14 canonical migrations and truncates the 14
  canonical tables, so the schema matches the retired-verification-rail canonical set.

Each live `newRepos` migrates from the renumbered canonical tree and truncates; the recovery
proof is delivered by the storetest `Challenges`, `PasswordResets`, and `Sessions` sub-runners
(the atomic secret rail + the §5.9 reset composition + session revocation) running against the
real dialects, complemented by the hermetic service/handler flow tests and the reference
rollback-injection proof.

Commands / results:

- **pgx live conformance (full suite), C-collation DB** —
  `cd features/authentication/stores/pgx && POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5432/authv3_cconf?sslmode=disable' go test -run TestConformance_Postgres -v ./...`
  — **PASS** (`ok … stores/pgx 8.097s`). Recovery-relevant leaves all green, incl.
  `Challenges/ConsumeCodeRedeem` (registration-verify consume path),
  `Challenges/ConsumeCodeAttemptIncrementAndLockout` (wrong-attempt lockout),
  `Challenges/ConcurrentLockoutSingleWinner`, `Challenges/ConcurrentCodeSingleWinner`,
  `Challenges/ConcurrentTokenSingleWinner`,
  `PasswordResets/RedeemAppliesFullComposition` (reset success + full composition: password set,
  all sessions deleted, grants/challenges purged), `PasswordResets/ConcurrentRedeemSingleWinner`
  (double-reset contention → exactly one commit), `PasswordResets/UnknownTokenGenericFailure`,
  `PasswordResets/ExpiredTokenGenericFailure`, `Sessions/DeleteAndDeleteByUser` (prior-session
  revocation).
- **pgx recovery subtests, `-race -count=5`, C-collation DB** —
  `… go test -race -count=5 -run 'TestConformance_Postgres/(PasswordResets|Challenges|Sessions)' ./...`
  — **PASS** (`ok … stores/pgx 16.276s`), 5 iterations, no data race reported. Concurrency/
  atomicity evidence for the recovery composition and the challenge lockout single-winner.
- **turso live conformance (full suite)** —
  `cd features/authentication/stores/turso && TURSO_DATABASE_URL='http://127.0.0.1:8080' TURSO_AUTH_TOKEN='local-dev' go test -tags=integration -run TestConformance_Turso -v ./...`
  — **PASS** (`ok … stores/turso 10.611s`). Same recovery leaves green:
  `Challenges/ConsumeCodeRedeem`, `Challenges/ConsumeCodeAttemptIncrementAndLockout`,
  `Challenges/ConcurrentLockoutSingleWinner`, `PasswordResets/RedeemAppliesFullComposition`,
  `PasswordResets/ConcurrentRedeemSingleWinner`, `PasswordResets/UnknownTokenGenericFailure`,
  `PasswordResets/ExpiredTokenGenericFailure`, `Sessions/DeleteAndDeleteByUser`.
- **turso recovery subtests, `-race -count=5`** —
  `… go test -tags=integration -race -count=5 -run 'TestConformance_Turso/(PasswordResets|Challenges|Sessions)' ./...`
  — **PASS** (`ok … stores/turso 15.604s`), 5 iterations, no data race.
- **service + handler recovery flows (`-race`)** —
  `cd features/authentication && go test -race -run 'ForgotAndReset|ResetPassword|Verify|Register' ./internal/logic/authsvc/... ./internal/inbound/authentication/...`
  — **PASS** (both packages). Confirms the milestone properties that live at the service/handler
  layer: `TestForgotAndResetPassword` (verify → live session → reset → all sessions revoked,
  NONE minted, pre-reset refresh token dead immediately, new password logs in),
  `TestVerifyWrongCodeLocksOut` (wrong-attempt lockout), `TestVerifyApplyConflictIsRestartable`
  (post-consume apply conflict is a safe restart), `TestResetPasswordUnknownToken` /
  `TestResetPasswordExpiredToken` (generic failures), and the inbound
  `TestResetPasswordRouteUnknownToken` (404, no cookie/token issued on the reset route).
- **injected/forced rollback (reference harness)** —
  `cd features/authentication && go test -race -run TestPasswordResetRollback ./storetest/...`
  — **PASS**. The reference `resetFailAt` harness injects a failure at each of the five
  composition statement boundaries and proves full rollback (the "injected/forced rollback where
  the harness supports it" leg; the live pgx/turso stores expose no mid-transaction injection
  seam, so their all-or-nothing guarantee is proven via the single-winner contention leaf above).

Phase-3 close gate:

- `make check` — **PASS** (`all checks passed`: templ drift clean; per-module build/vet/test across
  every module incl. the auth feature, storetest reference + authmem hermetic conformance, and the
  auth-cms host; integration-tag compile-only vet incl. both auth stores; all 13 guards green).
- `make guard` — **PASS** (all 13 guards green).
- Phase acceptance extras: `rg 'VerificationCodes|VerificationTokens|domain/verification|verification_codes|verification_tokens' features/authentication examples/auth-cms`
  → **zero matches** (exit 1); both dialect migration trees carry exactly 14 files. Fresh live
  proof recorded on both dialects above.

Premise adaptations:

- **Recovery proof delivered through the storetest conformance sub-runners, not a bespoke live
  script.** The task enumerates registration verification, wrong-attempt lockout, reset success,
  double-reset contention, and injected/forced rollback. Each maps to an existing conformance leaf
  (`Challenges/*`, `PasswordResets/*`, `Sessions/*`) plus the reference rollback proof and the
  hermetic service/handler flows; running those against the fresh live pgx/turso databases IS the
  live evidence. No new test code was needed — a bespoke live driver would duplicate the frozen
  contract without adding coverage.
- **C-collation `authv3_cconf` used for the pg leg** (per the AV3-2.4 Finding-2 byte-order
  pagination parity precedent); the default `postgres` DB was left untouched. Both DBs were
  confirmed empty pre-run.
- **`-race -count=5` chosen for the concurrency/atomicity repetitions.** The task body names no
  specific count; the single-winner contention leaves are the atomicity evidence, so they were
  repeated 5× under the race detector on both dialects to harden the milestone-live proof.

Handoff to phase 4 (AV3-4.1 shared delivery renderer/router, `05-delivery.md`):

- The recovery start paths (`Register` verification email, `ForgotPassword` reset email) still send
  SYNCHRONOUSLY through the existing `Mailer`/`sendVerificationEmail`/`sendResetEmail` seam — the
  AV3-3.3 synchronous-timing debt is unpaid. Phase 4's durable enumeration-safe outbox
  (`delivery_jobs`, table 0014, whose `DeliveryJobs` conformance is already live-green on both
  dialects) is what removes the known-vs-unknown latency signal; AV3-4.3 migrates these two outbound
  sites onto it.
- The `delivery_jobs` store rail is proven live on both dialects (the `DeliveryJobs` sub-runner
  passed in both suites above: enqueue-idempotent-by-key, replace-cancels-prior, lease claim/reclaim,
  succeed/retry/fail/cancel terminality, purge, and concurrent-claim single-winner), so phase 4's
  worker builds on a verified durable outbox — no store work is owed to it from phase 3.
- The recovery flows already route their new password through the shared `s.validatePassword`
  (AV3-3.4) and emit their secrets only via the Mailer seam, so the shared delivery renderer/router
  in AV3-4.1 needs to render the verification-code and reset-token payloads currently passed to
  `sendVerificationEmail`/`sendResetEmail`; secrets must stay out of `delivery_jobs` plaintext
  columns (standing invariant), i.e. the renderer composes the message body from a
  reference/opaque-secret handoff, never a persisted plaintext secret.
