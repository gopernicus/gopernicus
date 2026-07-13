# Phase 0 — security foundations

Status: READY after `00-overview.md` preflight.
Depends on: preflight only.
Design: §§3.2–3.3, 5.0, 5.6, 6.3, 8, 9.1, 12.1.

## Goal

Freeze the security vocabulary and executable contracts before identifier,
store, or route work begins. This phase intentionally leaves SQL adapters for
phase 2 but lands specification tests/fakes so later implementations have one
target.

## Task AV3-0.1 — method, assurance, and runtime configuration vocabulary

Touch: public authentication socket/config, internal auth service dependency
types, session domain as required, construction tests.

Implement:

- `AuthenticationMethod` descriptors and honest `AssuranceLevel` vocabulary.
  V3 password, email link/code, SMS, and ordinary OAuth are AAL1 unless a
  provider-specific future policy says otherwise. Include phishing/replay/PSTN
  properties needed by policy; do not add MFA implementations.
- Required `RuntimeMode` enum with empty/unknown rejection.
- Config/constructor slots and stable errors for `ChallengeProtector`,
  `IdentifierNormalizer`, `IdentifierKeyer`, `CredentialPolicy`,
  `DeliveryEncrypter`, and `PublicAuthBaseURL`. Collaborators not usable until a
  later phase may be validated only when their subsystem becomes enabled, as
  pinned in the design; required core collaborators fail loudly now.
- Optional delivery transport-security metadata in the sdk email/notify
  capability families. Bundled console transports declare development-only.
  Production rejects missing metadata and development-only transports;
  development warns.
- Construction matrix tests for empty/development/production mode and every
  missing or unsafe collaborator. Preserve existing nil-semantics not changed
  by the design.

Do not add a third-party sdk dependency or real SMS transport.

Verify:

```sh
cd sdk && go test ./capabilities/email ./capabilities/notify
cd features/authentication && go test ./...
make guard
```

Acceptance: config cannot accidentally default to development; console email
and notify transports are rejected in production; existing production-capable
test doubles declare metadata explicitly.

## Task AV3-0.2 — HMAC challenge protector and privacy keyer

Depends on: AV3-0.1.
Touch: authentication public/default crypto helpers and tests; no SQL.

Implement:

- Stdlib-only `HMACChallengeProtector` with an active key ID and accepted-key
  map, minimum 32-byte keys, HMAC-SHA-256 code digests bound to domain label,
  user ID, purpose, and code, and SHA-256 token digests.
- Candidate digests for every accepted key ID so an unexpired old-key challenge
  remains verifiable during rotation.
- Constant-time digest comparison helper where comparison occurs in Go.
- Empty token/digest rejection.
- A distinct HMAC identifier keyer for limiter/idempotency keys. It must use a
  separate key and domain label; never reuse the challenge, JWT, or encryption
  key.
- Tests: deterministic same-input digest, domain/user/purpose separation,
  different key divergence, key rotation, invalid active ID, short/missing
  key, empty code/token handling, and no plaintext code in returned/stored
  structures.

Verify:

```sh
cd features/authentication && go test ./... -run 'ChallengeProtector|IdentifierKeyer'
make check
```

Acceptance: everything is local Go code using `crypto/hmac` and `sha256`; no
external service or new module is introduced.

## Task AV3-0.3 — atomic challenge and recent-auth repository specifications

Depends on: AV3-0.2.
Touch: new `domain/challenge`, new recent-authentication-grant domain, storetest
suite/reference memory, `authmem` fakes as needed for compile.

Write conformance tests first, then ports/entities and reference-memory
implementations for:

- atomic challenge `Replace`, `ConsumeCode`, `ConsumeToken`, and `PurgeExpired`;
- exactly one winner for simultaneous correct code and token redemption;
- atomic wrong-code attempt increment and 4→5 lockout/delete races;
- expiry deletion, context mismatch consumption, key-ID candidate selection,
  and empty-digest rejection;
- atomic recent-auth grant consume bound to session, user, purpose, context
  digest, and expiry; mismatch/expiry/success all enforce single-use semantics;
- session authentication metadata types needed for the recent-login shortcut.

The memory implementations use one mutex around each promised atomic
operation. Do not emulate the old Get/Increment/Delete API.

Verify:

```sh
cd features/authentication && go test ./storetest ./domain/... ./internal/logic/authsvc/...
make guard
```

Acceptance: `go test -race` on the reference challenge/grant tests is green and
concurrency cases prove one winner.

## Task AV3-0.4 — credential-policy and optimistic-mutation specification

Depends on: AV3-0.1, AV3-0.3.
Touch: policy/method-set domain, repository interfaces, storetest reference,
service tests.

Implement the contracts, not SQL adapters:

- `MethodSet` with `AuthRevision` and typed password/OAuth/identifier methods.
- `CredentialPolicy.EvaluateMutation(current, proposed)` and bundled safe
  default: at least one direct login method, at least one verified recovery
  method, restricted PSTN classification, and configurable stronger host rules.
- Closed v3 `CredentialMutation` variants: remove password, unlink OAuth,
  retire identifier, and change identifier uses/primary.
- `CredentialMutationRepository.Snapshot` and revision-CAS `Apply`.
- Reference-memory atomic apply that rejects stale revisions, increments exactly
  once, and never partially mutates.
- Tests for concurrent removal of the final two methods: at most one mutation
  may commit after policy/reload; stale safe-looking snapshots cannot win.

Verify:

```sh
cd features/authentication && go test -race ./storetest ./internal/logic/authsvc/...
make check
```

Acceptance: no `authenticationMethodCount` helper exists; policy evaluates
typed method sets and revision conflicts are stable errors.

## Task AV3-0.5 — HTTP security primitives

Depends on: AV3-0.1.
Touch: sdk web middleware only if generally reusable; otherwise authentication
inbound middleware/helpers and tests.

Implement:

- cookie-authenticated mutation protection using allowlisted `Origin`,
  `Sec-Fetch-Site` where present, and a synchronizer/double-submit CSRF token
  contract appropriate to the existing session architecture;
- exact rules for bearer-only requests so API clients are not forced through a
  browser CSRF flow;
- strict `application/json` content type, bounded bodies, unknown/trailing JSON
  rejection, and `Cache-Control: no-store` helpers;
- trusted-proxy-only forwarded IP resolution; absent trusted-proxy context falls
  back to `RemoteAddr`, never raw `X-Forwarded-For`;
- table tests for same-origin, allowlisted cross-origin, missing/mismatched CSRF,
  cross-site fetch, bearer-only, oversized/trailing body, wildcard CORS with
  credentials, spoofed forwarding headers, IPv4, and IPv6.

Do not yet rewrite every route; phase 5/6/7 apply these primitives.

Verify:

```sh
cd features/authentication && go test ./internal/inbound/authentication/...
cd sdk && go test ./foundation/web/...   # only if sdk changed
make check
```

## Task AV3-0.6 — delivery-job repository specification

Depends on: AV3-0.1, AV3-0.2.
Touch: new public `domain/deliveryjob` entity/repository port, storetest
reference, tests. The port must be public because external host/store adapters
implement it; orchestration remains internal and lands in phase 4.

Define and prove:

- encrypted payload envelope only (destination, rendered secret/message, and
  account-resolution input never appear as separate plaintext columns);
- kind, template/purpose, keyed idempotency key, state, attempt count,
  `available_at`, lease/claim metadata, created/updated/terminal timestamps;
- atomic enqueue-idempotency, due-job claim with lease expiry, success,
  retry-with-backoff, terminal failure, cancellation/replacement, and bounded
  purge;
- at-least-once worker semantics with idempotent state transitions;
- contention tests proving one active claimant per job and replacement tests
  proving a user-requested resend cancels the earlier pending job.

Do not import the jobs or events feature.

Verify:

```sh
cd features/authentication && go test -race ./storetest ./internal/logic/delivery/...
make check
```

## Phase acceptance

```sh
cd features/authentication && go test -race ./...
make check
make guard
```

Fresh-eyes grep confirms no new code models every method as equal, uses the old
non-atomic challenge sequence, trusts raw `X-Forwarded-For`, or logs a pepper.

## Stop conditions

- A repository cannot express one-winner redemption on both supported
  dialects: stop before weakening the port.
- CSRF protection would require changing unrelated host routes globally: stop
  and present the narrow authentication-only alternative.
- Existing unfinished JWT/session work lacks a place to store authentication
  metadata: stop and resolve that baseline dependency.

## Execution log

Append dated entries per completed task.

### 2026-07-12 — Preflight gate (pre-AV3-0.1)

- `make check` green (all module tests + integration-tag vet + all guards pass).
- `git status --short`: 78 pre-existing changes (auth-v2 + JWT-refresh in-flight
  work) recorded and preserved; no resets.
- Migration filename parity: pgx vs turso auth trees byte-identical filename
  sets (`diff` of `ls` output clean).
- Conformance suites: `features/authentication` storetest ok; turso store module
  hermetic pass; pgx `TestConformance_Postgres` **SKIP** (no live DSN — recorded
  per preflight rule 4).
- Full design read vs tree: CLEAR TO EXECUTE. No stale premises, no
  product/architecture conflicts. Notables for phase 0: `web.TrustProxies` +
  `web.ClientIP` already exist (consume, don't rebuild); `ratelimiter.Limiter`
  port exists; `sdk/foundation/cryptids/hs256.go` is the key-floor/construction
  pattern precedent for the HMAC protector (separate key, no reuse);
  `authsvc/refresh.go` D1–D8 contract is pinned untouched; no CSRF middleware
  exists yet (AV3-0.5 builds from zero); console transports carry no capability
  metadata yet (AV3-0.1 adds to both).

### 2026-07-12 — AV3-0.1 (method, assurance, runtime configuration vocabulary)

Files changed:

- `sdk/capabilities/email/capabilities.go` (new) — `TransportSecurity` enum,
  `Capabilities{TransportSecurity, DevelopmentOnly}`, optional
  `CapabilityReporter` interface.
- `sdk/capabilities/email/console.go` — `Console.Capabilities()` declares
  development-only + transport none.
- `sdk/capabilities/email/smtp.go` — `SMTP.Capabilities()` declares
  production-capable (not development-only, STARTTLS).
- `sdk/capabilities/email/capabilities_test.go` (new) — Console dev-only, SMTP
  production-capable.
- `sdk/capabilities/notify/capabilities.go` (new) — parallel metadata family for
  notifiers.
- `sdk/capabilities/notify/console.go` — `Console.Capabilities()` declares
  development-only.
- `sdk/capabilities/notify/capabilities_test.go` (new) — Console dev-only.
- `features/authentication/domain/session/method.go` (new) — `AssuranceLevel`
  (AAL1 only; AAL2/AAL3 reserved), `MethodKind`, `AuthenticationMethod` honest
  descriptors (all AAL1, none phishing/replay-resistant, SMS=PSTN),
  `DescribeMethod`.
- `features/authentication/domain/session/method_test.go` (new) — honest
  vocabulary proofs.
- `features/authentication/security.go` (new) — `RuntimeMode` required enum +
  validation, ports `ChallengeProtector`/`DigestCandidate`,
  `IdentifierNormalizer`, `IdentifierKeyer`; stable errors
  (`ErrRuntimeModeRequired`/`ErrRuntimeModeInvalid`/`ErrInsecureDeliveryTransport`
  enforced now; `ErrChallengeProtectorRequired`/`ErrIdentifierKeyerRequired`/
  `ErrDeliveryEncrypterRequired`/`ErrPublicAuthBaseURLRequired` frozen for their
  enabling phases); transport-security production/development enforcement.
- `features/authentication/authentication.go` — Config slots (`RuntimeMode`,
  `ChallengeProtector`, `IdentifierNormalizer`, `IdentifierKeyer`,
  `DeliveryEncrypter` = `cryptids.Encrypter`, `PublicAuthBaseURL`); NewService
  validates RuntimeMode (after existing required-collaborator checks) and
  delivery-transport security.
- `features/authentication/security_test.go` (new) — construction matrix for
  empty/dev/production mode, console/metadata-less rejection, declared-transport
  acceptance, dev WARN.
- `features/authentication/auth_test.go` — existing construction tests supply
  `RuntimeMode: RuntimeModeDevelopment`.
- `examples/auth-cms/cmd/server/main.go` — host sets
  `RuntimeMode: auth.RuntimeModeDevelopment` (console transports; WARN expected).

Commands / results:

- `cd sdk && go test ./capabilities/email ./capabilities/notify` — **PASS**.
- `cd features/authentication && go build ./... && go vet ./... && go test ./...`
  — **PASS** (all packages).
- `make guard` — **PASS** (all guards, sdk stays stdlib-only).
- `cd examples/auth-cms && go build ./...` — **PASS** (host hygiene, not a task
  verify command).

Premise adaptations:

- **CredentialPolicy Config slot deferred to AV3-0.4.** The task lists a slot for
  `CredentialPolicy`, but its interface signature references `MethodSet`, which
  AV3-0.4 explicitly owns ("Implement MethodSet with AuthRevision and typed
  password/OAuth/identifier methods") and whose typed method set depends on the
  phase-1 identifier domain. Defining it now would front-run/duplicate AV3-0.4.
  The other five slots are frozen now; AV3-0.4 adds the `CredentialPolicy` field
  + `ErrCredentialPolicyRequired` alongside MethodSet.
- **`authsvc.Deps` intentionally not modified.** AV3-0.1 has no internal consumer
  of the new vocabulary yet: RuntimeMode + transport validation live in the
  public composition root (NewService), and RuntimeMode cannot move into the
  `authsvc` package without an import cycle (public → authsvc). Threading the
  production posture into `authsvc` lands with the later phases that consume it.
- **Session persistence fields not added yet.** Only the method/assurance
  vocabulary lands in `domain/session`; the `authenticated_at`/methods/assurance
  Session columns are AV3-0.3's "session authentication metadata types" plus
  phase-2 schema.

### 2026-07-12 — AV3-0.2 (HMAC challenge protector and privacy keyer)

Dependency AV3-0.1 confirmed complete (checked off in `TASKS.md`; execution-log
entry above). Worktree changes preserved; no resets.

Files changed:

- `features/authentication/security_hmac.go` (new) — stdlib-only
  (`crypto/hmac`, `crypto/sha256`, `crypto/subtle`) implementations of the
  AV3-0.1 ports: `HMACKeyRing` + `NewHMACChallengeProtector`
  (`HMACChallengeProtector`) and `NewHMACIdentifierKeyer`
  (`HMACIdentifierKeyer`). 32-byte key floor mirrors `cryptids.NewHS256`; keys
  copied defensively at construction. `DigestCode` is domain-separated,
  length-prefixed HMAC-SHA-256 bound to `userID + purpose + code`;
  `CandidateCodeDigests` returns one candidate per accepted key ID (sorted) for
  rotation; `DigestToken` is unkeyed SHA-256 hex (empty token → empty digest,
  the empty-hash guard); `ConstantTimeDigestEqual` is the exported constant-time
  comparison helper for in-Go (memory/authmem) stores. `IdentifierKey` uses a
  separate key and a distinct domain label — never the challenge pepper, JWT, or
  encryption key. Construction/use errors: `ErrChallengeKeyRingEmpty`,
  `ErrChallengeActiveKeyMissing`, `ErrChallengeKeyTooShort`,
  `ErrChallengeUnknownKeyID`, `ErrEmptyChallengeCode`,
  `ErrIdentifierKeyTooShort`.
- `features/authentication/security_hmac_test.go` (new) — deterministic
  same-input digest, domain/user/purpose separation (incl. length-prefix
  field-boundary proof), different-key divergence, key rotation
  (candidate-per-key-ID equals direct old-key digest), invalid/empty active key
  ID, empty ring + short-key rejection, unknown key ID, empty code/token
  handling, token digest determinism + pepper-independence, no-plaintext-code in
  returned structures, constant-time helper equality/empty guard, and identifier
  keyer determinism/separation/divergence/short-key + domain separation from the
  challenge digest under identical key bytes.

Commands / results:

- `cd features/authentication && go test ./... -run 'ChallengeProtector|IdentifierKeyer'`
  — **PASS** (13 new tests; other packages report no matching tests).
- `make check` — **PASS** (templ drift clean; per-module vet/build/test green;
  integration-tag vet green; all 14 guards green).

Premise adaptations:

- **Domain-separation label literals must avoid the `"gopernicus/` prefix.** The
  `guard-no-legacy-path` guard greps every `*.go` for a quoted string beginning
  `"gopernicus/` (the retired legacy import path). The design's illustrative
  label `gopernicus/auth/challenge/code/v1` is a domain-separation constant, not
  an import, but it tripped the guard verbatim. Adapted the two labels to
  dot-separated form (`gopernicus.auth.challenge.code.v1`,
  `gopernicus.auth.identifier-key.v1`); domain separation and versioning are
  preserved and the guard is not weakened.

### 2026-07-12 — AV3-0.3 (atomic challenge and recent-auth repository specs)

Dependencies AV3-0.1 and AV3-0.2 confirmed complete (both checked off in
`TASKS.md` with execution-log entries above). Worktree changes preserved; no
resets. Tests written first, then ports/entities and reference-memory impls.

Files changed:

- `features/authentication/domain/challenge/challenge.go` (new) — the atomic
  secret domain: `Challenge` entity (`id, user_id, purpose, secret_digest,
  protector_key_id, context, attempt_count, expires_at, created_at, version`),
  `Consumed`, `ConsumeOutcome` (NotFound/Expired/Rejected/LockedOut/
  ContextMismatch/Redeemed, zero=NotFound = fail-closed), `DigestCandidate`
  (canonical home), `MaxAttempts = 5`, and the six purpose constants.
- `features/authentication/domain/challenge/repository.go` (new) — `Repository`
  port: `Replace`, `ConsumeCode(...candidates, expectedContextDigest, maxAttempts,
  now) (Consumed, ConsumeOutcome, error)`, `ConsumeToken`, `PurgeExpired`. Doc
  comments are the spec.
- `features/authentication/domain/authgrant/authgrant.go` (new) — recent-auth /
  step-up grant domain: `Grant` (session/user/purpose/context digest + methods/
  assurance + authenticated/expiry/created/consumed times), `Expired`,
  `Consumed`.
- `features/authentication/domain/authgrant/repository.go` (new) — `Repository`
  port: `Create`, atomic single-use `Consume(sessionID, purpose, contextDigest,
  now)`, `DeleteBySession` (revocation cascade).
- `features/authentication/domain/session/authentication.go` (new) —
  `AuthenticationMetadata{AuthenticatedAt, Methods, Assurance}` + `Recorded()`;
  the recent-primary-login shortcut vocabulary.
- `features/authentication/domain/session/session.go` — `Session` gains an
  `Authentication AuthenticationMetadata` field (memory round-trips it; persisted
  columns are phase-2 schema).
- `features/authentication/security.go` — `DigestCandidate` converted from a
  concrete struct to a type alias of `challenge.DigestCandidate` (see premise
  adaptation).
- `features/authentication/authentication.go` — `Repositories` gains
  `Challenges challenge.Repository` and `AuthenticationGrants authgrant.Repository`
  (nil-tolerated; validation deferred to phases 3/6).
- `features/authentication/storetest/storetest.go` — `Challenges` (14 cases:
  replace single-active + unique claim, code redeem, key-candidate selection,
  context-mismatch-consumes, attempt-increment + lockout, expiry-delete,
  empty-digest reject, token redeem/expiry/empty, bounded purge, and three
  concurrent single-winner races) and `AuthenticationGrants` (7 cases: consume,
  context-mismatch not-found, expiry, single-use, metadata round-trip,
  delete-by-session cascade, concurrent single-winner) sub-runners, skip-loudly
  when the port is nil.
- `features/authentication/storetest/reference_test.go` — `refChallenges` and
  `refAuthGrants` reference-memory impls, one mutex around each promised atomic
  operation; digest comparison routes through `auth.ConstantTimeDigestEqual`.

Commands / results:

- `cd features/authentication && go test ./storetest ./domain/... ./internal/logic/authsvc/...`
  — **PASS** (all packages; challenge/authgrant have no in-domain test files).
- `go test -race -run 'TestReference/(Challenges|AuthenticationGrants)' ./storetest`
  — **PASS** (21 sub-cases; the three concurrent-code/token/lockout and the
  concurrent-grant cases each prove exactly one winner, race-clean).
- `cd features/authentication && go test ./...` — **PASS** (whole feature; the
  DigestCandidate alias and Repositories additions break nothing).
- `make guard` — **PASS** (all guards; sdk stays stdlib-only).
- Downstream build check (not a task verify command): `stores/pgx`,
  `stores/turso`, and `examples/auth-cms` all `go build ./...` clean — the two new
  nil-tolerated Repositories fields and the alias do not break the SQL adapters or
  the proof host.

Premise adaptations:

- **`DigestCandidate` is now a type alias, not a concrete struct.** The challenge
  `Repository.ConsumeCode` takes `[]DigestCandidate`, and `Repositories.Challenges`
  forces the root package to import `domain/challenge`; the challenge domain
  therefore cannot import the root back. The canonical `DigestCandidate` moved to
  `domain/challenge` and `authentication.DigestCandidate` became
  `= challenge.DigestCandidate`. The public symbol AV3-0.2 froze is preserved
  identically (an alias is transparent to every caller); `security.go` was touched
  though it is not in the task's explicit touch list — a compile-necessity to keep
  the protector and the repository speaking one type.
- **Grant domain named `authgrant` with entity `Grant`.** The design entity name
  `AuthenticationGrant` would stutter as `authgrant.AuthenticationGrant`; the
  package maps to the `Repositories.AuthenticationGrants` field, entity is `Grant`,
  port is `Repository`. (Info for AV3-0.4/6.1.)
- **`ConsumeCode` disposition rides `ConsumeOutcome`, not the error return.** Per
  the design's stated purpose for adding the outcome, error is reserved for
  infrastructure failures; not-found/expired/rejected/locked-out/mismatch/redeemed
  are all outcomes with a nil error. `ConsumeToken` (no outcome return) keeps the
  sdk-sentinel channel (ErrNotFound/ErrExpired), matching `oauthstate.Consume`.
- **Context binding compared by plain equality.** `expectedContextDigest` is
  compared against the stored `context` blob with equality inside the atomic op
  (portable to both SQL dialects as `context = ?`); the service binds a digest for
  code flows. No separate `context_digest` column — the design's `context TEXT`
  holds it, and the token flow returns it verbatim in `Consumed` for
  caller-side binding validation.
- **Repositories `Challenges`/`AuthenticationGrants` are nil-tolerated now.**
  Consistent with the AV3-0.1/0.2 "validated only when their subsystem becomes
  enabled" rule: NewService is not changed to require them (phase 3 requires
  Challenges, phase 6 requires AuthenticationGrants). Adding the fields does not
  break existing hosts/stores (Go zero-values them). authmem was left untouched
  (it compiles; AV3-1.4 owns wiring the proof-host memory impls).

### 2026-07-12 — AV3-0.5 (HTTP security primitives)

Dependency AV3-0.1 confirmed complete (checked off in `TASKS.md` with an
execution-log entry above). Worktree changes preserved; no resets. Primitives
only — routes are NOT rewritten (phases 5/6/7 apply these), per the task's
explicit scope and the phase stop condition.

Files changed:

- `features/authentication/internal/inbound/authentication/security.go` (new) —
  the §9.1 HTTP security primitive set, all in the authentication inbound layer
  (not sdk): `requireBrowserSafeMutation(csrfConfig)` middleware (bearer-only
  requests skip; cookie-authenticated mutations validate `Sec-Fetch-Site` where
  present, an exact-match `Origin` allowlist, and a constant-time double-submit
  CSRF token via `crypto/subtle`); `issueCSRFToken` (256-bit token + non-HttpOnly
  Secure SameSite=Lax double-submit cookie); `isBearerOnly` /`originAllowed`
  (wildcard `*` never authorizes a credentialed cross-origin mutation);
  `requireJSON` (strict `application/json`, else 415); `strictJSONBody`
  (`http.MaxBytesReader` bound → 413, `DisallowUnknownFields` + trailing-data
  rejection → 400); `writeNoStore` (`Cache-Control: no-store`). Constants
  `csrfCookieName`/`csrfHeaderName`/`maxJSONBodyBytes`.
- `features/authentication/internal/inbound/authentication/routes.go` —
  `clientIP()` fixed to trusted-proxy-only resolution: absent the
  `web.TrustProxies` context it now falls back to `RemoteAddr` and NO LONGER
  trusts a raw `X-Forwarded-For` header (the prior spoofable back-compat
  fallback removed). Orphaned `strings` import dropped; `net`/`net/http`/
  `web.ClientIP` retained.
- `features/authentication/internal/inbound/authentication/security_test.go`
  (new) — table tests for the full task matrix: same-origin, allowlisted
  cross-origin, missing/mismatched CSRF, cross-site fetch, non-allowlisted origin
  without `Sec-Fetch-Site`, bearer-only skip, bearer+cookie stays gated;
  `issueCSRFToken` round-trip through the gate; wildcard-ignore in `originAllowed`;
  `web.CORSMiddleware(["*"])` never sets `Access-Control-Allow-Credentials`;
  `requireJSON` (json/charset/form/missing); `strictJSONBody`
  (valid/unknown-field/trailing-object/trailing-token/malformed/oversized);
  `writeNoStore`; `clientIP` IPv4/IPv6/spoofed-header fallback and
  trusted-proxy-resolution paths.

Commands / results:

- `cd features/authentication && go test ./internal/inbound/authentication/...`
  — **PASS**.
- `cd sdk && go test ./foundation/web/...` — **SKIPPED / N/A** (sdk was not
  changed; the CSRF/JSON primitives live in authentication inbound and `clientIP`
  consumes the existing `web.TrustProxies`/`web.ClientIP`).
- `make check` — **PASS** (templ drift clean; per-module vet/build/test green
  incl. pgx/turso stores + auth-cms host; integration-tag vet green; all 14
  guards green, FS9 responder guard included).

Premise adaptations:

- **All primitives live in authentication inbound, not sdk web.** The task allows
  sdk web middleware "only if generally reusable; otherwise authentication
  inbound." CSRF here is bound to the session-cookie architecture (bearer-only
  exemption keys off the session cookie name) and the JSON strictness needs
  `DisallowUnknownFields` + trailing-data rejection that sdk's existing
  `web.DecodeJSON` deliberately does not do (other features depend on its lenient
  semantics). Keeping the set in the feature avoids changing shared sdk behavior
  and satisfies the design §9.1 contract narrowly — no sdk change, so the
  conditional sdk verify leg does not apply. No route rewrite; phases 5/6/7 wire
  these onto their routes.
- **CSRF scheme is double-submit, not synchronizer.** §9.1 offers
  "synchronizer/double-submit"; the session is JWT-backed (no server-side CSRF
  store on the session), so the stateless double-submit cookie+header pair is the
  architecture-appropriate choice (constant-time compared).
- **`clientIP()` hardening applied now (not deferred).** Although the task builds
  primitives phases apply later, `clientIP` is already live on every route via
  `clientInfoMiddleware`; leaving the raw `X-Forwarded-For` fallback would keep a
  spoofable audit/limiter source and trip the phase-acceptance fresh-eyes grep.
  The fix is surgical (drop the raw-XFF branch) and consumes the existing
  `web.TrustProxies`/`web.ClientIP` seam rather than rebuilding it.

### 2026-07-12 — AV3-0.4 (credential-policy and optimistic-mutation spec)

Dependencies AV3-0.1 and AV3-0.3 confirmed complete (both checked off in
`TASKS.md` with execution-log entries above). Worktree changes preserved; no
resets. Contracts only — no SQL adapters (phase 2 owns those).

Files changed:

- `features/authentication/domain/credential/credential.go` (new) — the typed
  method-set domain: `MethodSet{AuthRevision, HasPassword, OAuth, Identifiers}`,
  typed `OAuthMethod` / `IdentifierMethod` (+ `IdentifierUses` role flags),
  `LoginMethods()` / `VerifiedRecoveryMethods()` returning honest
  `session.AuthenticationMethod` descriptors, the pure `MethodSet.With(Mutation)`
  proposed-projection, and the closed sealed-sum `Mutation` (`RemovePassword`,
  `UnlinkOAuth`, `RetireIdentifier` with atomic primary replacement,
  `ChangeIdentifierUses`).
- `features/authentication/domain/credential/policy.go` (new) — `Policy`
  interface (`EvaluateMutation(ctx, current, proposed)`), bundled `DefaultPolicy`
  + `NewDefaultPolicy(PolicyConfig)` (safe default: ≥1 direct login method, ≥1
  verified recovery method, PSTN restricted-not-forbidden; configurable stronger
  host rules `MinRecoveryMethods` / `RequireNonPSTNRecovery` / `MinLoginAssurance`),
  and stable `ErrNoLoginMethod` / `ErrNoRecoveryMethod` / `ErrInsufficientRecovery`
  / `ErrRecoveryRequiresNonPSTN` / `ErrInsufficientAssurance`, all wrapping
  `sdk.ErrConflict` (the pinned `cannot_remove_last_method` 409 posture, §5.8).
- `features/authentication/domain/credential/repository.go` (new) —
  `MutationRepository{ Snapshot, Apply(userID, expectedAuthRevision, Mutation) }`;
  doc pins revision-CAS: `sdk.ErrConflict` on stale revision, `sdk.ErrNotFound`
  for unknown user, exactly-once increment, never partial.
- `features/authentication/domain/credential/policy_test.go` (new) — login floor
  (last-method rejection), recovery floor (sole-recovery removal + "recovery set
  is only the removed identifier" structural rejection), `MinRecoveryMethods=2`,
  PSTN default-permit vs `RequireNonPSTNRecovery`-reject, unverified/unknown-kind
  non-counting, and `With` variant correctness incl. receiver-immutability and
  primary reassignment.
- `features/authentication/security.go` — `type CredentialPolicy =
  credential.Policy` alias (Principal/Granter precedent) + frozen
  `ErrCredentialPolicyRequired` (reserved for phase-6 strict-production validation
  that disables the default without a replacement).
- `features/authentication/authentication.go` — `Repositories.CredentialMutations
  credential.MutationRepository` (nil-tolerated, REQUIRED at phase 6) and
  `Config.CredentialPolicy CredentialPolicy` (nil → bundled default at phase 6);
  NewService not changed (AV3-0.3 nil-tolerated posture).
- `features/authentication/storetest/storetest.go` — `CredentialMutations`
  sub-runner (skip-loud when nil): Snapshot projection + unknown→NotFound,
  Apply increments-once, stale-revision→Conflict-unchanged, and concurrent
  single-winner (two Applies at rev 0 → exactly one commits, revision advances by
  one, no double-mutation) seeded through the credential sibling repos.
- `features/authentication/storetest/reference_test.go` — `refCredentialMutations`
  projecting from the password/oauth/identifier sources under one mutex per
  atomic op (rejects stale, increments once, never partial); reference gains an
  `authRevisions` + `identifiers` (phase-1 identifier-table stand-in) map; and
  `TestCredentialPolicyConcurrentSelfRemoval` — the §5.6 crown-jewel proof: two
  concurrent identifier retirements through a Snapshot→policy→CAS-Apply retry loop
  yield exactly one commit; the loser reloads, re-runs `DefaultPolicy`, and is
  rejected on the login/recovery floor (the stale safe-looking snapshot cannot
  win).

Commands / results:

- `cd features/authentication && go test -race ./storetest ./internal/logic/authsvc/...`
  — **PASS** (race-clean; `TestReference/CredentialMutations` 5 sub-cases and
  `TestCredentialPolicyConcurrentSelfRemoval` each verified to RUN, not skip).
- `make check` — **PASS** (templ drift clean; per-module vet/build/test green
  incl. pgx/turso stores + authmem/proof host recompiling against the new
  nil-tolerated Repositories field; integration-tag vet green; all 14 guards
  green — no `authenticationMethodCount` helper exists).

Premise adaptations:

- **`CredentialMutation` modeled as a sealed sum interface, not a struct+kind.**
  The design writes "closed v3 sum type"; the idiomatic Go closure is an interface
  with an unexported `isMutation()` marker so only `credential` defines variants
  and `Apply` switches exhaustively with no open default. Public symbol is
  `credential.Mutation`.
- **`MethodSet` identifier projection carries role-flag `IdentifierUses`, not a
  phase-1 identifier entity.** The full identifier aggregate + normalization is
  AV3-1.1; front-running it here would duplicate/pre-empt phase 1. `MethodSet` is
  the policy projection (§5.6/§5.1), so it holds only the typed views policy
  reasons over — `Kind` as an `identity.Kind` string (reusing
  `sdk/foundation/identity.KindEmail`/`KindPhone`, no new kind vocabulary), `Uses`
  as Login/Recovery/Notification flags, `Verified`, `Primary`. Phase 1/6 map the
  real identifier rows into this projection.
- **`CredentialPolicy` Config slot is nil-tolerated; NewService not changed.**
  Per the AV3-0.3 posture and §8 ("nil → bundled safe default"), the slot is
  frozen now and the nil→`credential.NewDefaultPolicy` resolution + the required-
  ness wiring land with the phase-6 credential suite that consumes them.
  `ErrCredentialPolicyRequired` is defined now (handoff directive; the AV3-0.1
  frozen-vocabulary rule) but fires in phase 6, not at phase-0 construction.
- **`Apply` runs no policy; the reference is a projection over the existing
  credential sources.** §5.6 pins that the store's `Apply` is revision-CAS + the
  typed mutation only — policy runs in the service before `Apply`. The reference
  therefore projects `MethodSet` from the password/oauth/identifier sources and
  mutates the targeted source in `Apply`; a per-user `authRevisions` map is the
  `users.auth_revision` stand-in (that column is phase-2 schema). The `identifiers`
  map stands in for the phase-1 identifier table so the policy+reload race can
  seed a recovery-bearing state; the generic `Run` mechanics use only the
  already-wired credential sibling repos so future SQL stores conform unchanged.
- **`CredentialMutations` conformance is in `Run` now, seeded through sibling
  repos.** Phase 2's pgx/turso `Snapshot` must project from the same credential
  tables so the generic seed (Users.Create + Passwords.Set + OAuthAccounts.Create)
  drives them; identifier-bearing conformance folds in once AV3-1.2/2.2/2.3 land
  the identifier tables. authmem left untouched (nil-tolerated; AV3-1.4 owns the
  proof-host memory impl).

### 2026-07-12 — AV3-0.6 (delivery-job repository specification)

Dependencies AV3-0.1 and AV3-0.2 confirmed complete (checked off in `TASKS.md`
with execution-log entries above); AV3-0.3/0.4/0.5 also complete. Worktree
changes preserved; no resets. Tests written alongside the ports; no SQL adapters
(phase 2 owns those), no jobs/events feature import.

Files changed:

- `features/authentication/domain/deliveryjob/deliveryjob.go` (new) — the durable
  enumeration-safe outbox entity (design §6.1.1): `Job` carries only the OPAQUE
  `Payload []byte` (no plaintext destination/message/identifier column) plus
  `Kind`, `Purpose`, PII-free `IdempotencyKey`, `State`, `AttemptCount`,
  `AvailableAt`, lease metadata (`LeaseID`/`LeasedUntil`), redacted `LastError`,
  and created/updated/`TerminalAt` timestamps. String `State` constants
  (`pending`/`succeeded`/`failed`/`canceled`, invitation convention) with
  `pending` the only non-terminal state; `Terminal()`, `Leased(now)`, `Due(now)`
  predicates. In-flight is encoded by the lease fields, not a separate state, so
  an expired lease makes a still-pending job reclaimable (at-least-once).
- `features/authentication/domain/deliveryjob/repository.go` (new) — public
  `Repository` port: `Enqueue` (idempotent by IdempotencyKey among non-terminal
  jobs), `Replace` (cancel prior non-terminal jobs sharing the key + insert fresh
  — the user-requested resend), `Claim` (oldest due job → leased, attempt++;
  none → `sdk.ErrNotFound`), lease-checked `Succeed`/`Retry`/`Fail`
  (reclaimed lease → `sdk.ErrConflict`; already-in-terminal-state → idempotent
  nil), `Cancel`, and bounded `PurgeTerminal`. Doc comments are the spec.
- `features/authentication/internal/logic/delivery/envelope.go` (new) — the
  encrypted payload envelope (design §6.1.1): `Envelope{Destination,
  ResolutionInput, Subject, Body, Secret}` + `Seal(enc, env) []byte` /
  `Open(enc, payload) Envelope` through the required `cryptids.Encrypter`
  (JSON→AES-GCM). sdk-ports-only import (cryptids); nil enc → `sdk.ErrInvalidInput`.
  Orchestration (router/renderer/worker) is explicitly deferred to phase 4.
- `features/authentication/internal/logic/delivery/envelope_test.go` (new) —
  round-trip, the **encrypted-payload-only proof** (sealed blob leaks none of
  destination/resolution-input/body/secret as plaintext, and `deliveryjob.Job`
  has no plaintext column to leak), and nil-encrypter rejection.
- `features/authentication/authentication.go` — `Repositories.DeliveryJobs
  deliveryjob.Repository` (nil-tolerated; REQUIRED at phase 4) + import.
- `features/authentication/storetest/storetest.go` — `DeliveryJobs` sub-runner
  (skip-loud when nil): enqueue-idempotency, replace-cancels-prior-pending, claim
  leases due job, claim skips future/live-leased, expired-lease reclaim, idempotent
  succeed, retry-with-backoff, terminal fail, reclaimed-lease completion conflict,
  cancel terminates, bounded terminal purge, and concurrent-claim single-winner.
- `features/authentication/storetest/reference_test.go` — `refDeliveryJobs`
  reference-memory impl (one mutex per atomic op; deterministic oldest-due
  selection; lease-equality CAS on completion) + `reference.deliveryJobs` map,
  init, and `repositories()` wiring.

Commands / results:

- `cd features/authentication && go test -race ./storetest ./internal/logic/delivery/...`
  — **PASS** (race-clean; the 12 `TestReference/DeliveryJobs` sub-cases each
  verified to RUN, not skip; the concurrent-claim case proves exactly one winner).
- `make check` — **PASS** (templ drift clean; per-module vet/build/test green incl.
  pgx/turso stores + authmem/proof host recompiling against the new nil-tolerated
  Repositories field; integration-tag vet green; all guards green).

Phase-0 close gate (this is the last phase-0 task):

- `make check` — **PASS**.
- `make guard` — **PASS** (all 13 guards).
- Fresh-eyes acceptance grep — **CLEAN**: no `authenticationMethodCount`/
  `methodCount` (no every-method-equal model); no code trusts a raw
  `X-Forwarded-For` (only test-spoof assertions + the "NEVER trusts" comment in
  routes.go); no `Increment`/`GetAndDelete` non-atomic challenge/delivery API
  (delivery uses atomic Claim/Succeed/Retry/Fail); no log statement emits a pepper
  (every `pepper` hit is a doc comment, a const/error/field name, or the
  `testPepper` key helper — never a `slog`/`log` argument).

Premise adaptations:

- **`internal/logic/delivery` created now (envelope only), not deferred whole to
  phase 4.** The task's touch list names `domain/deliveryjob` + storetest + tests,
  but its verify command runs `go test -race ./storetest ./internal/logic/delivery/...`,
  and `go test` fails hard ("no such directory") when that path is absent — so the
  package must exist for the task to verify. Its design-faithful phase-0 content is
  exactly the **encrypted payload envelope** the task enumerates ("encrypted
  payload envelope only … never appear as separate plaintext columns"): a value
  type + `Seal`/`Open` through the required `DeliveryEncrypter`, importing sdk
  ports only (§6.1 dependency direction). The kind-aware router/renderer and the
  at-least-once worker loop (the actual orchestration) remain phase 4 (AV3-4.1/4.2)
  as the task and §6.1 pin.
- **`DeliveryJobs` is nil-tolerated now; NewService not changed.** Consistent with
  the AV3-0.3/0.4 posture and §8 — the slot is frozen and becomes REQUIRED when the
  phase-4 worker is wired. Adding the field zero-values in every existing host/store;
  authmem left untouched (AV3-1.4 owns the proof-host memory impls).
- **`Replace` and `Enqueue` both key off `IdempotencyKey` with different intent.**
  Enqueue dedups a double-submitted start (returns the existing non-terminal job);
  Replace is the explicit resend (cancels the prior non-terminal job, inserts a
  fresh one). One keyed digest serves both, so no second "group key" column is
  introduced. In-flight uses lease fields rather than a distinct `claimed` state,
  giving crash-safe at-least-once reclaim without extra schema.
