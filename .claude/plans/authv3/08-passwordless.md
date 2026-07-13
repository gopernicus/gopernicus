# Phase 7 — passwordless login

Status: READY after phase 5; may execute before phase 6 with one implementer.
Depends on: phases 0–5.
Design: §§3.2–3.3, 4, 6.4, 8–9.1, 13 V3/V4/V7/V14.

## Goal

Add login-only email/phone passwordless authentication against existing active
verified login identifiers, using asynchronous starts, atomic redemption, and
the existing single session-mint path.

## Task AV3-7.1 — enablement and construction matrix

Touch: config validation, route capability methods, tests.

Implement `Config.Passwordless []string`:

- empty means routes absent;
- allowed v3 kinds are email and phone only;
- each enabled kind requires a production-capable delivery route in production;
- links require valid `PublicAuthBaseURL`; production requires HTTPS;
- production validation checks durable/shared limiter and delivery worker;
- config transition summary rejects stranding users under strict production
  startup validation;
- no auto-provision and no phone+password behavior.

Tests cover every partial wiring combination and route absence/presence.

## Task AV3-7.2 — asynchronous passwordless start

Depends on: AV3-7.1, phase-4 delivery.

Implement `POST /auth/passwordless/start` with strict body:

- `{identifier_kind, identifier, method?}`; method link/code, defaults email→link
  and phone→code, both explicitly selectable when transport supports them;
- normalize, apply per-keyed-identifier and trusted-IP limits before account
  resolution, enqueue opaque job, return one accepted body/status;
- worker resolves `GetLogin`, requires active + verified + enabled use, issues
  purpose-bound challenge with identifier ID/kind/value context, and sends;
- unknown/unverified/disabled identifiers produce no challenge/send but complete
  the same job/request shape without externally observable distinction;
- delivery failure remains job/audit state, not request response.

Tests use a blocking provider and instrumented repositories to prove the request
handler never resolves users or waits for provider latency. Compare known,
unknown, invalid, and unverified response shape and limiter calls.

## Task AV3-7.3 — OTP verification and session mint

Depends on: AV3-7.2, phase-3 challenge service.

Implement `POST /auth/passwordless/verify`:

- normalize and rate-limit before lookup;
- resolve current identifier/user, require active verified login use;
- compute HMAC candidates and atomically consume `login_otp` by user/purpose +
  expected identifier context;
- on success call only `mintSession`; return the standard access/refresh pair
  and cookie behavior;
- invalid/expired/unknown/disabled/context mismatch are the same generic 401;
- attempts/lockout events contain kind/purpose only.

Tests: one-winner simultaneous correct verify, wrong-code attempts/lockout,
identifier removed/replaced after issue, old pepper key, rate limit, generic
errors, no auto-provision, and standard refresh rotation after login.

## Task AV3-7.4 — magic-link redemption and URL safety

Depends on: AV3-7.2, phase-3 challenge service.

Implement `POST /auth/passwordless/redeem`:

- URL token is 256-bit and SHA-256-digested;
- atomic delete-returning by purpose/digest; then reload/validate bound current
  identifier before minting;
- only `mintSession` creates the session;
- no GET endpoint consumes; generic 401 on all failures.

Implement link construction from configured absolute public base URL only,
never request host/forwarded headers. Exact-match redirect allowlist. Prefer
fragment token delivery for browser host landing page. Server responses use
no-store/no-referrer guidance; phase 8 builds the page.

Tests: scanner GET cannot consume, token absent from server request logs in the
fragment flow, exact redirect matching, hostile Host ignored, HTTP production
URL rejected, double redemption one winner, identifier changed/removed, and
standard refresh rotation.

## Task AV3-7.5 — events, errors, timing, and live proof

Depends on: AV3-7.3, AV3-7.4.

- Record `passwordless_start` accepted/blocked and `passwordless_login`
  success/failure without secret/raw identifier.
- Verify strict CSRF policy distinction: start/verify/redeem are credential
  establishment endpoints and follow origin/content rules without requiring a
  pre-existing CSRF session; minted cookie cannot be forced cross-site under the
  origin policy.
- Run email link, email code if enabled, phone SMS link, and phone OTP against
  fresh pgx and turso stores with development console delivery.
- Observe start timing under an intentionally slow provider: response completes
  before worker send for known and unknown identifiers.
- Verify refresh/logout after both successful methods.

Record redacted transcripts and worker/job IDs.

## Phase acceptance

```sh
make check
make guard
```

Plus both live dialect passwordless legs. No route auto-provisions, no start
calls a provider synchronously, and all success uses `mintSession`.

## Stop conditions

- A transport cannot carry the selected method safely: reject construction or
  method; do not silently switch methods.
- The host/public URL would need request Host to construct links: stop and
  require configuration.
- Browser origin enforcement would block native clients: preserve bearer/body
  client semantics with an explicit tested non-cookie path, not a global bypass.

## Execution log

Append dated entries per completed task.

### 2026-07-13 — AV3-7.1 enablement and construction matrix

Task: AV3-7.1. Dependencies confirmed complete (phases 0–6 all checked off in
TASKS.md; phase 7 depends on 3–5 per the overview and 6 is also closed). The
challenge rail already carries `PurposeLoginMagicLink` (token, 15m) and
`PurposeLoginOTP` (code, 5m, lockout) in `domain/challenge` + `authsvc/challenge.go`
— 6.x added them, so this task did not. Worktree preserved (only additive edits +
one new file; no unrelated file touched).

Implemented:

- **`Config.Passwordless []string`** (`authentication.go`) — the kind-granular,
  deny-by-absence enablement knob (design §4.2). Empty → passwordless OFF and its
  routes absent. Documented: allowed v3 kinds are `email`/`phone`; each needs a wired
  delivery channel; enabling it requires the challenge rail + durable outbox + a
  valid `PublicAuthBaseURL`; it never auto-provisions and never enables phone+password
  (V10). No `env` tag (host-wired like `AllowedOrigins`; the config parser has no
  `[]string` env convention).
- **Construction matrix (`security.go`).** New `validatePasswordless(mode, kinds,
  router, challengesWired, outboxWired, publicBaseURL)` enforces, when a host opts in:
  every kind is `email`/`phone` (`ErrPasswordlessKindInvalid`); each has a wired
  channel via the phase-4 `delivery.Router.Supports(kind)` seam (email always, phone
  iff a phone-kind notifier is wired — `ErrPasswordlessKindUnsupported`); the atomic
  challenge rail is wired (`ErrPasswordlessChallengeRequired`); the durable delivery
  outbox is wired (`ErrPasswordlessDeliveryRequired`, async starts / V14); and a
  link-capable `PublicAuthBaseURL` (`validatePublicAuthBaseURL` →
  `ErrPublicAuthBaseURLRequired` / `ErrPublicAuthBaseURLInvalid` / production
  `ErrPublicAuthBaseURLInsecure`). Wired into `NewService` right after the delivery
  queue is built, so the always-on production gates (durable/shared limiter,
  identifier keyer, worker acknowledgment) and the global transport-security check
  (production-capable channel) all run first and a passwordless-enabled production
  host inherits them.
- **Route capability methods (`internal/logic/authsvc/passwordless.go`, new).**
  `Service.PasswordlessEnabled()` and `Service.PasswordlessKindEnabled(kind)` over a
  resolved `passwordless map[string]bool` (threaded `auth.Config.Passwordless →
  authsvc.Deps.Passwordless → Service.passwordless`), mirroring
  `OAuthEnabled`/`MachineEnabled`/`TokenEnabled`. These are the seam AV3-7.2 gates the
  `POST /auth/passwordless/{start,verify,redeem}` registration on.

Premise adaptations (logged):

- **`PublicAuthBaseURL` required whenever ANY passwordless kind is enabled, not only
  when the email-link default is selected.** The design (§4.3) makes `method=link`
  always caller-selectable (a link in an SMS body is legal — "magic links deliverable
  to sms or email"), so a link flow is always reachable once passwordless is on;
  requiring the base URL unconditionally when `Passwordless` is non-empty prevents a
  half-configured host where a user selects `link` and it cannot render. This matches
  §8's "required when any link flow is enabled".
- **Config-time stranding guard, repository-summary query deferred.** §8's "startup
  validation *may* query a repository summary in strict production mode to detect
  configuration changes that would strand existing accounts" is explicitly optional
  and needs a live repository read. AV3-7.1 is a hermetic construction-matrix task, so
  it implements the config-level stranding guards that reject every wiring that would
  strand a passwordless user detectable at construction (unsupported kind, missing
  challenge rail / outbox, missing / insecure base URL; plus the inherited durable
  limiter + worker gates). The live repository-population "would this specific existing
  set be stranded" query belongs to the phase's live proof (AV3-7.5) and is deferred
  there.
- **Route registration deferred to AV3-7.2.** 7.1 ships the enablement config,
  validation, and the `PasswordlessEnabled()` capability seam; the actual
  `/auth/passwordless/*` route registration (and its inbound `authService` interface
  entry) lands with the handlers in 7.2, so no dead handler is registered now. Route
  absence/presence is proven at the construction level (empty → OFF, valid non-empty →
  ON, invalid non-empty → construction rejected) via `svc.svc.PasswordlessEnabled()`
  in the same-package `security_test.go`.

Files changed:
- `authentication.go` — `Config.Passwordless` field + docs; `validatePasswordless`
  call in `NewService`; `Passwordless: cfg.Passwordless` into `authsvc.Deps`.
- `security.go` — passwordless + base-URL errors
  (`ErrPasswordlessKindInvalid`/`Unsupported`/`ChallengeRequired`/`DeliveryRequired`,
  `ErrPublicAuthBaseURLInvalid`/`Insecure`); `validatePasswordless` +
  `validatePublicAuthBaseURL`; `net/url`, `internal/logic/delivery`, and
  `sdk/foundation/identity` imports.
- `internal/logic/authsvc/service.go` — `Deps.Passwordless`, resolved
  `passwordless` map field, and its resolution in `NewService`.
- `internal/logic/authsvc/passwordless.go` (new) — `PasswordlessEnabled` /
  `PasswordlessKindEnabled`.
- Tests: `security_test.go` — `TestNewServicePasswordless{AbsentByDefault,Matrix,
  KindEnabled}` (matrix: email wired, invalid kind, phone with/without notifier,
  email+phone, challenge-rail absent, outbox absent, base-URL absent/non-absolute,
  development-http) + `TestNewServiceProductionPasswordless{RejectsHTTPBaseURL,
  AcceptsFullWiring,RejectsConsolePhone}`.

Verification (observed):
- `cd features/authentication && go build ./... && go vet ./... && go test ./...` → all
  green (whole module; passwordless matrix `-run Passwordless -v` all PASS).
- `make guard` → all guards passed.
- `gofmt -l` on every changed file → clean.

For AV3-7.2 (asynchronous passwordless start): the capability seam is ready —
`authsvc.Service.PasswordlessEnabled()` gates route registration and
`PasswordlessKindEnabled(kind)` validates the requested kind; add
`PasswordlessEnabled()` to the inbound `authService` interface and gate a
`mountPasswordless` on it in `routes.go` (the OAuth/machine/token pattern). The
enablement guarantees 7.2 can rely on: the durable outbox (`delivery.Service`) is
wired (starts enqueue, never resolve synchronously — V14), the challenge rail +
protector are wired (issue `login_magic_link`/`login_otp`), `PublicAuthBaseURL` is a
valid absolute base (HTTPS in production) for link construction, and
`delivery.Router.Supports(kind)` is the channel-availability check. The per-identifier
+ per-IP keyed budgets (§4.4) run before account resolution; `IdentifierKeyer` is
production-required (already enforced globally).

### 2026-07-13 — AV3-7.2 asynchronous passwordless start

Task: AV3-7.2. Dependencies confirmed complete (phases 0–6 all checked off in
TASKS.md; phase-4 delivery closed; AV3-7.1 checked off). Worktree preserved (only
additive edits + two new files + two new test files; no unrelated file reverted).

Implemented:

- **`POST /auth/passwordless/start`** — strict body `{identifier_kind, identifier,
  method?}` (`internal/inbound/authentication/passwordless.go`, new). `mountPasswordless`
  is gated on `svc.PasswordlessEnabled()` in `routes.go` (deny-by-absence); the inbound
  `authService` interface gains `PasswordlessEnabled()` + `StartPasswordless`. The handler
  decodes the strict body, delegates, and returns one uniform 202 accepted body; a
  rate-limit exhaustion is a generic 429, an invalid kind/method a 400, an internal
  failure a 500 — none distinguishes existence.
- **`Service.StartPasswordless(ctx, kind, identifier, method)`**
  (`internal/logic/authsvc/passwordless.go`). The enumeration-safe unauthenticated start
  (§4.1): validate the enabled kind (`ErrPasswordlessKindDisabled`) and resolve/validate
  the method (`ErrPasswordlessMethodInvalid`; default email→link, phone→code, both
  caller-selectable) → normalize (malformed → nil accepted, mirroring `ForgotPassword`) →
  per-identifier AND per-IP start budgets (§4.4: `passwordless_start:<kind>:<digest>` and
  `passwordless_start:ip:<trusted-ip>`; `ErrPasswordlessRateLimited`) → enqueue an OPAQUE
  `delivery.Command` carrying only `Envelope{ResolutionInput: normalized}` with the
  method's delivery purpose. It never resolves the account or calls a provider on the
  request path; known/unknown/unverified traverse an identical request path.
- **Worker initializers** (`passwordless.go` + `delivery.go` `Initialize`/`Discard`
  dispatch). `initPasswordlessLink` / `initPasswordlessCode` run off-path: `GetLogin` (only
  active + login-enabled rows) → require `Verified()` → issue the purpose-bound challenge
  (`login_magic_link` token / `login_otp` code) with `WithStoredContext(loginBinding{
  IdentifierID, Kind, NormalizedValue})` → render (magic link built from `PublicAuthBaseURL`
  via `magicLinkURL`) → deliver. An unknown/unverified/login-disabled identifier resolves
  nothing (`deliver=false`): the job succeeds with no send. `Discard` voids a
  never-delivered magic-link token by secret; the code path relies on its short TTL +
  lockout (there is no delete-by-secret for codes).
- **`PurposeLoginCode` delivery purpose + `login_code.html` template** (`delivery/router.go`,
  `delivery/templates/login_code.html`). A distinct purpose from `PurposeSensitiveCode` so
  the opaque-start OTP job routes to its own initializer and carries sign-in wording.
- **Threaded `Config.PublicAuthBaseURL`** into `authsvc.Deps.PublicAuthBaseURL` →
  `Service.publicBaseURL` (7.1 added and validated it on Config but did not thread it to the
  service; the worker needs it to build the link).

Premise adaptations (logged):

- **Dedicated `PurposeLoginCode` delivery purpose rather than reusing
  `PurposeSensitiveCode`.** Delivery purpose is decoupled from challenge purpose (§6.2) and
  drives both the template and the worker's `Initialize` dispatch. `PurposeSensitiveCode` is
  already the request-path rendered purpose for step-up / OAuth-unlink / password-remove;
  routing an opaque OTP-login start through it would overload one purpose across an
  opaque-start initializer and rendered request-path sends. A distinct purpose keeps the
  dispatch unambiguous and the sign-in copy honest.
- **Magic-link URL is the minimal fragment form (`{PublicAuthBaseURL}#token=…`).** 7.2 needs
  a link to send (the handoff pins "magic links render through the router with
  PublicAuthBaseURL"); the exact-match redirect allowlist, no-GET landing, and fragment
  history-scrub hardening are explicitly AV3-7.4's scope. `magicLinkURL` builds the
  §6.4-preferred fragment link from the configured absolute base only (never a request
  Host) and is the seam 7.4 refines.
- **Security events (`passwordless_start` accepted/blocked) deferred to AV3-7.5.** The 7.2
  task lists no event recording; AV3-7.5 explicitly owns "events, errors, timing". No event
  is recorded here to keep the diff surgical.
- **CSRF/origin policy deferred to AV3-7.5.** The start is unauthenticated (no
  pre-existing session), so it carries only the blanket client-info middleware here; the
  credential-establishment origin/content policy (§9.1) is 7.5's named scope.

Files changed:
- `authentication.go` — `PublicAuthBaseURL: cfg.PublicAuthBaseURL` into `authsvc.Deps`.
- `internal/logic/authsvc/service.go` — `Deps.PublicAuthBaseURL` + `Service.publicBaseURL`
  field + resolution in `NewService`.
- `internal/logic/authsvc/passwordless.go` — `StartPasswordless`, method resolution,
  per-identifier+per-IP `passwordlessStartBudget`, `initPasswordlessLink`/`initPasswordlessCode`,
  `resolvePasswordlessLogin`, `magicLinkURL`, `loginBinding`, `MethodLink`/`MethodCode`,
  errors (`ErrPasswordlessKindDisabled`/`MethodInvalid`/`RateLimited`).
- `internal/logic/authsvc/delivery.go` — `Initialize`/`Discard` dispatch for the two
  passwordless delivery purposes.
- `internal/logic/delivery/router.go` — `PurposeLoginCode` const + spec.
- `internal/logic/delivery/templates/login_code.html` (new).
- `internal/inbound/authentication/sessions.go` — `PasswordlessEnabled`/`StartPasswordless`
  on the `authService` interface.
- `internal/inbound/authentication/routes.go` — `mountPasswordless` gate.
- `internal/inbound/authentication/passwordless.go` (new) — start handler + strict DTO.
- Tests: `internal/logic/authsvc/passwordless_test.go` (opaque same-path for
  known/unknown/unverified, no-resolve-on-request-path via the `loginCalls` counter, both
  budgets run, rate-limited, worker delivers/binds magic link, code method email, phone
  defaults to code over SMS, unverified no-send, malformed accepted, disabled kind/invalid
  method); `internal/inbound/authentication/passwordless_test.go` (route absent by default,
  accepted, uniform for unknown/malformed, strict decode, disabled kind 400, rate-limited
  429).

Verification (observed):
- `cd features/authentication && go build ./... && go vet ./... && go test ./...` → all
  green (whole module; `-run Passwordless -v` all PASS in both authsvc and inbound).
- `make guard` → all guards passed (exit 0).
- `gofmt -l` on every changed file → clean.

For AV3-7.3 (OTP verification and session mint): the start rail issues the
`login_otp` (code) challenge bound via `WithStoredContext(loginBinding{IdentifierID,
Kind, NormalizedValue})` — verify must `ConsumeChallenge(userID, challenge.PurposeLoginOTP,
code, WithExpectedContext(loginBinding{…}))` after re-resolving the CURRENT identifier
(`GetLogin` → require active+verified+login), so a code cannot log in after its identifier
was removed/replaced/login-disabled. The resolved `userID` comes from the consumed challenge
row (the code is user-scoped); the verify request must itself normalize + rate-limit on the
§4.4 verify key `passwordless:<kind>:<identifier-digest>|<trusted-ip>` (the start key prefix
is `passwordless_start:`) before lookup. Success calls ONLY `mintSession` with
`session.MethodEmailCode`/`MethodSMSCode` metadata; every invalid/expired/unknown/mismatch is
one generic 401. The link twin (`login_magic_link`, `magicLinkURL`) and its redeem are
AV3-7.4; `session.MethodEmailLink` is its mint method.

### 2026-07-13 — AV3-7.3 OTP verification and session mint

Task: AV3-7.3. Dependencies confirmed complete (phases 0–6 all checked off in
TASKS.md; phase-3 challenge service closed; AV3-7.1 and 7.2 checked off). Worktree
preserved (additive edits to two files + one new service test file + additive
inbound test edits; no unrelated file touched or reverted).

Implemented:

- **`POST /auth/passwordless/verify`** — strict body `{identifier_kind, identifier,
  code}` (`internal/inbound/authentication/passwordless.go`). `passwordlessVerify`
  joins `mountPasswordless` (routes.go already gated it on `PasswordlessEnabled()`);
  the inbound `authService` interface gains `VerifyPasswordless`. On success it sets
  both session cookies AND returns the access/refresh pair (`newTokenResponse`,
  mirroring refresh/token), so cookie and bearer clients both get a live session. An
  exhausted verify budget is a generic 429; EVERY other failure routes through
  `RespondJSONDomainError` where `ErrPasswordlessLogin` (wrapping `sdk.ErrUnauthorized`)
  becomes one generic 401 and an internal error becomes a 500 — the response never
  distinguishes invalid/expired/unknown/disabled/context-mismatch/lockout.
- **`Service.VerifyPasswordless(ctx, kind, identifier, code)`**
  (`internal/logic/authsvc/passwordless.go`). The OTP rail: reject a disabled kind
  (generic) → normalize (malformed → generic) → §4.4 verify budget on the verify key
  `passwordless:<kind>:<identifier-digest>|<trusted-ip>` (distinct from the
  `passwordless_start:` prefix) BEFORE any lookup → re-resolve the CURRENT active
  verified login identifier via `resolvePasswordlessLogin` (removed/replaced/
  login-disabled/unverified → generic) → `ConsumeChallenge(ident.UserID,
  PurposeLoginOTP, code, WithExpectedContext(loginBinding{IdentifierID, Kind,
  NormalizedValue}))` so a code whose bound identifier changed since issue fails the
  atomic binding and is spent → on success `mintSession` with the passwordless code
  method (`email_code`/`sms_code`). The stable challenge dispositions
  (invalid/expired/too-many) collapse to the one generic `ErrPasswordlessLogin`; only
  a genuine infrastructure error propagates (→ 500).
- **`ErrPasswordlessLogin`** (generic 401), **`passwordlessVerifyBudget` /
  `passwordlessVerifyKey`** (the §4.4 verify-key twin of `loginKey`),
  **`passwordlessVerifiesPerMinute = 10`**, and **`passwordlessCodeMethod(kind)`**
  (phone → `sms_code`, else `email_code`).

Premise adaptations (logged):

- **A disabled kind at verify is the generic 401, not the start's 400.** The task
  explicitly lists "disabled" among the single-generic-401 set; the verify surface
  collapses every reason so a probe cannot learn whether a kind is enabled from a
  verify. Start keeps its 400 request-shape rejection (`ErrPasswordlessKindDisabled`)
  because start is not a credential-verification surface.
- **Pre-lookup rate limit stays a distinct 429, not folded into the generic 401.**
  §5.8's "single generic 401" governs credential/challenge dispositions; the §4.4
  throttle is orthogonal, exactly as `Login`'s `ErrRateLimited` surfaces as 429 while
  its credential failures are 401. The challenge-rail lockout (`ErrTooManyAttempts`,
  wrong-code exhaustion) DOES collapse to the generic 401, per the handoff.
- **`passwordless_login` success/failure security events deferred to AV3-7.5.** The
  phase's AV3-7.5 explicitly owns "events, errors, timing" and names the
  `passwordless_login` pair; 7.2 deferred `passwordless_start` events there for the
  same reason. The existing challenge rail already records its own kind/purpose-only
  `challenge_lockout` event from `ConsumeChallenge` (no secret, no identifier), which
  satisfies "attempts/lockout events contain kind/purpose only" for this task without
  adding the login-outcome pair early.
- **Verify budget number (`10/min`) chosen; the design pins the key shape, not a
  count.** It layers over the per-code challenge lockout (`challenge.MaxAttempts`), so
  it only bounds cross-code cycling for one keyed identifier + IP.

Files changed:
- `internal/logic/authsvc/passwordless.go` — `VerifyPasswordless`,
  `passwordlessVerifyBudget`, `passwordlessVerifyKey`, `passwordlessCodeMethod`,
  `ErrPasswordlessLogin`, `passwordlessVerifiesPerMinute`; `session` import.
- `internal/inbound/authentication/passwordless.go` — `passwordlessVerifyRequest`
  DTO, `passwordlessVerify` handler, `/auth/passwordless/verify` in `mountPasswordless`.
- `internal/inbound/authentication/sessions.go` — `VerifyPasswordless` on the
  `authService` interface.
- Tests: `internal/logic/authsvc/passwordless_verify_test.go` (new — success mints +
  method stamp + refresh rotation, generic failures, no auto-provision, wrong-code
  lockout, identifier removed, identifier replaced, old pepper key, rate limit,
  single-winner concurrency, phone sms_code); `internal/inbound/authentication/
  passwordless_test.go` (verify route absent by default, strict decode, generic 401
  with no cookie, rate-limited 429).

Verification (observed):
- `cd features/authentication && go build ./... && go vet ./... && go test ./...` → all
  green (whole module; `-run 'Passwordless|VerifyPasswordless' -v` all PASS in both
  authsvc and inbound).
- `make guard` → all guards passed (exit 0).
- `gofmt -l` on every changed file → clean.

For AV3-7.4 (magic-link redemption and URL safety): the OTP verify pins the shared
patterns 7.4's `POST /auth/passwordless/redeem` mirrors — re-validate the CURRENT
bound identifier before minting, `mintSession` is the SOLE session creator, and ALL
failures are one generic `ErrPasswordlessLogin` (401) while a pre-lookup throttle
stays a distinct 429. But redeem differs: it consumes the `login_magic_link` TOKEN
via `RedeemToken(PurposeLoginMagicLink, token, WithExpectedContext(loginBinding{…}))`
(atomic delete-returning by digest; the user is resolved FROM the consumed row's
binding, then reload/validate the current identifier), NOT `ConsumeChallenge`. Its
mint method is `session.MethodEmailLink`. The link is already built fragment-only
from `PublicAuthBaseURL` (`magicLinkURL`, §6.4) — 7.4 adds the redirect allowlist,
no-GET-consumes hardening, HTTP-production rejection, and no-store/no-referrer
guidance. `ErrPasswordlessLogin` is reusable as redeem's single generic failure.

### 2026-07-13 — AV3-7.4 magic-link redemption and URL safety

Task: AV3-7.4. Dependencies confirmed complete (phases 0–6 all checked off in
TASKS.md; phase-3 challenge service closed; AV3-7.1/7.2/7.3 checked off). Worktree
preserved (additive edits to four files + one new service test file + additive
inbound test edits; no unrelated file touched or reverted).

Implemented:

- **`POST /auth/passwordless/redeem`** — strict body `{token}` only
  (`internal/inbound/authentication/passwordless.go`). `passwordlessRedeem` joins
  `mountPasswordless`; the inbound `authService` interface gains `RedeemPasswordless`.
  On success it sets both session cookies AND returns the access/refresh pair
  (`newTokenResponse`, mirroring verify), so cookie and bearer clients both get a live
  session. An exhausted redeem budget is a generic 429; EVERY other failure routes
  through `RespondJSONDomainError` where `ErrPasswordlessLogin` (from 7.3) becomes one
  generic 401 and an internal error a 500 — the response never distinguishes
  unknown/expired/replayed token, or removed/replaced/disabled bound identifier.
- **`Service.RedeemPasswordless(ctx, token)`** (`internal/logic/authsvc/passwordless.go`).
  The magic-link rail: pre-consume §4.4 throttle on the trusted client IP under a
  DISTINCT prefix `passwordless_redeem:ip:<ip>` (redeem carries no identifier, so IP is
  the only stable key; distinct from start's `passwordless_start:` and verify's
  `passwordless:`) → `RedeemToken(PurposeLoginMagicLink, token)` atomic delete-returning
  by digest (the token is spent whether or not the binding still validates) → decode the
  consumed row's stored `loginBinding` → reload the CURRENT active verified login
  identifier via `resolvePasswordlessLogin(binding.Kind, binding.NormalizedValue)` and
  require `ok && ident.ID == binding.IdentifierID && ident.UserID == consumed.UserID`
  (removed/replaced/login-disabled/unverified → generic 401) → `mintSession` with
  `session.MethodEmailLink`. `mintSession` is the sole session creator.
- **URL-safety hardening.** No-GET-consumes is structural: the surface is POST-only, so a
  link scanner that fetches the URL cannot authenticate (§6.4). HTTP-in-production
  rejection is enforced at construction (`ErrPublicAuthBaseURLInsecure` via
  `validatePublicAuthBaseURL`, landed 7.1 — confirmed by
  `TestNewServiceProductionPasswordlessRejectsHTTPBaseURL`, still green). Link
  construction (`magicLinkURL`) builds ONLY from the configured absolute
  `PublicAuthBaseURL` — request Host/forwarded headers never participate — with the
  256-bit token on the URL FRAGMENT (never query/path, so a server GET never receives
  it). No-store/no-referrer guidance rides all three passwordless responses via
  `writeNoStore` + new `writeNoReferrer` (`Referrer-Policy: no-referrer`, so a
  fragment-borne token never leaks via a downstream Referer).

Premise adaptations (logged):

- **No `WithExpectedContext` on the redeem `RedeemToken` call; the binding is validated
  by a post-consume reload instead.** The handoff sketched
  `RedeemToken(..., WithExpectedContext(loginBinding{…}))`, but the §4.3 redeem body is
  `{token}` only — the request supplies no identifier, so there is no current binding to
  pass as the expected context BEFORE consuming. `WithExpectedContext` is the OTP path's
  tool (verify carries the identifier). Redeem therefore consumes by digest, then decodes
  the stored binding and reloads/validates the CURRENT identifier by (kind, value) and
  ID-match — strictly stronger for the "replaced" case (same value, new row → ID
  mismatch → generic 401) and identical anti-probing (token spent atomically regardless).
  This matches the handoff's own "resolve the user FROM the consumed row's binding, then
  reload/validate the current identifier before minting."
- **Redeem throttle is per-trusted-IP, not per-identifier+IP.** §4.4 pins the verify key
  shape (`passwordless:<kind>:<digest>|<ip>`) around an identifier the caller supplies;
  redeem has no identifier in the body, so a keyed-identifier throttle is impossible
  pre-consume. The redeem budget keys on the trusted IP under a distinct prefix
  (`passwordless_redeem:ip:`), a coarse farming bound layered over the 256-bit token's
  own unguessability. Count reused from verify (10/min); the design pins key shape, not a
  count.
- **`session.MethodEmailLink` stamped for every link kind.** The frozen method table
  (`domain/session/method.go`) has `email_link` but no `sms_link`; adding a method kind
  is out of scope (the table is frozen and reserved for auth-v4). The handoff pins
  `MethodEmailLink`, so a link redemption stamps it regardless of the bound identifier's
  kind. Flagged for AV3-7.5/design if an SMS-link method is ever wanted — not invented
  here.
- **"Exact-match redirect allowlist" satisfied by no open-redirect surface, not a new
  body field.** §6.4 lists "Redirect targets are exact-match allowlisted"; the existing
  `internal/redirect.Allowlist` (`s.redirects`, used by OAuth) is the mechanism. The
  §4.3 passwordless bodies carry NO redirect/return_to field, and redeem returns a JSON
  token pair with no server-side HTTP redirect, so 7.4 introduces no open-redirect
  surface at all: the only URL the server constructs (the magic link) targets exactly
  the configured trusted base. The landing page's post-login navigation is phase 8. No
  redirect field was added to the pinned start/redeem contract.
- **`passwordless_login` success/failure security events deferred to AV3-7.5.** AV3-7.5
  explicitly owns "events, errors, timing" and names the `passwordless_login` pair; 7.2
  and 7.3 deferred their events there too. The challenge rail still records its own
  kind/purpose-only `challenge_lockout` from the consume path (no secret, no identifier).
- **CSRF/origin policy deferred to AV3-7.5.** Redeem is unauthenticated
  credential-establishment (no pre-existing session), carrying only the blanket
  client-info middleware here; the §9.1 credential-establishment origin/content policy is
  7.5's named scope.

Files changed:
- `internal/logic/authsvc/passwordless.go` — `RedeemPasswordless`,
  `passwordlessRedeemBudget`, `passwordlessRedeemsPerIPPerMinute`; `encoding/json`
  import; updated `magicLinkURL` / `passwordlessCodeMethod` doc comments.
- `internal/inbound/authentication/passwordless.go` — `passwordlessRedeemRequest` DTO,
  `passwordlessRedeem` handler, `/auth/passwordless/redeem` in `mountPasswordless`,
  no-store/no-referrer on all three passwordless handlers.
- `internal/inbound/authentication/security.go` — new `writeNoReferrer` helper.
- `internal/inbound/authentication/sessions.go` — `RedeemPasswordless` on the
  `authService` interface.
- Tests: `internal/logic/authsvc/passwordless_redeem_test.go` (new — success mints +
  email_link method + refresh rotation, generic failures, no auto-provision, identifier
  removed, identifier replaced, double-redemption single winner, rate limit not
  consuming, `magicLinkURL` fragment-only + exact base + hostile-Host-ignored);
  `internal/inbound/authentication/passwordless_test.go` (redeem route absent by default,
  strict decode, generic 401 no cookie, rate-limited 429, no-GET-consume, no-store +
  no-referrer headers, hostile Host ignored).

Verification (observed):
- `cd features/authentication && go build ./... && go vet ./... && go test ./...` → all
  green (whole module; `-run 'Redeem|MagicLink|Passwordless' -v` all PASS in both authsvc
  and inbound; `-run ProductionPasswordless` still PASS).
- `make guard` → all guards passed (exit 0).
- `gofmt -l` on every changed file → clean.

For AV3-7.5 (events, errors, timing, and live proof — the phase closer): all three
passwordless service methods are landed and share one generic failure
(`ErrPasswordlessLogin`, 401) with a distinct 429 throttle. 7.5 adds the
`passwordless_start` (accepted/blocked) and `passwordless_login` (success/failure)
security-event pairs — deferred from 7.2/7.3/7.4 — carrying kind + challenge purpose
only, never the secret/raw identifier; `recordSecurityEvent` + `securityevent` are the
seam (the challenge rail already emits `challenge_lockout`). It also owns the §9.1 CSRF
distinction (start/verify/redeem are credential-establishment endpoints: origin/content
rules WITHOUT a pre-existing CSRF session; the minted cookie cannot be forced
cross-site), the slow-provider start-timing proof (start completes before worker send
for known and unknown), and the fresh pgx + turso live legs (email link, email code,
phone SMS link, phone OTP) with development console delivery, plus refresh/logout after
both methods. The redeem rail's mint method is `session.MethodEmailLink`; verify's are
`email_code`/`sms_code`. No `sms_link` method exists (frozen table) — 7.5/design owns
whether a phone magic-link should stamp something other than `email_link`.

### 2026-07-13 — AV3-7.5 events, errors, timing, and live proof — PHASE 7 CLOSE

Task: AV3-7.5, the phase-7 closer. Dependencies confirmed complete (AV3-7.1–7.4
checked off in TASKS.md; phases 0–6 closed). Worktree preserved (additive edits to
three source files + one new source-file helper block, one new authsvc test file,
additive inbound test edits; no unrelated file reverted).

Implemented:

- **`passwordless_start` (accepted/blocked) + `passwordless_login`
  (success/failure/blocked) security-event pairs** (`domain/securityevent`
  `TypePasswordlessStart`/`TypePasswordlessLogin`; `authsvc/passwordless.go`
  `recordPasswordlessStart`/`recordPasswordlessLogin`). `StartPasswordless` records
  `passwordless_start` **success** on a successful outbox enqueue (accepted) and
  **blocked** on a throttled start; there is NO UserID (the start never resolves the
  account on the request path — §4.1) and NO failure status (a malformed/unknown start
  is still an accepted response, so no failure is meaningful). `VerifyPasswordless` and
  `RedeemPasswordless` record `passwordless_login` **success** on the mint, **failure**
  on every generic-401 disposition, and **blocked** on the pre-lookup throttle —
  mirroring the password `login` event's three statuses (§5.1 precedent). Details carry
  the identifier **kind** + **challenge purpose** (`login_magic_link` / `login_otp`)
  ONLY; a dedicated leak-scan test asserts no event carries the raw identifier value,
  code, or token. The challenge rail's own `challenge_lockout` (kind/purpose-only) still
  fires from the consume path underneath.
- **§9.1 credential-establishment origin gate** (`inbound/security.go`
  `requireBrowserSafeOrigin` + extracted shared `browserOriginAllowed`;
  `inbound/passwordless.go` `mountPasswordless` now wraps all three routes in it). The
  passwordless start/verify/redeem endpoints enforce the allowlisted-Origin /
  Sec-Fetch-Site policy WITHOUT the double-submit CSRF token check — there is no
  pre-existing session, so there is no `auth_csrf` cookie to compare, and requiring one
  would break a first-time browser sign-in. A cross-site origin is rejected 403 before
  any mint (the minted cookie can never be established from a disallowed origin); a
  native/bearer client that sends no Origin passes (the phase-7 stop condition — origin
  enforcement never blocks native clients). `requireBrowserSafeMutation` was refactored
  to call the same `browserOriginAllowed` (behavior-preserving; its existing gate test
  stays green).
- **Content hardening.** The three handlers moved from the plain `decode` to
  `strictJSONBody` (MaxBytesReader + unknown-field + trailing-data rejection), the §9.1
  body content rules, keeping the existing `writeNoStore`/`writeNoReferrer` guidance.
- **Slow-provider start-timing proof + four-flow end-to-end run** (new
  `authsvc/passwordless_events_test.go`). `TestStartPasswordlessDoesNotBlockOnProvider`
  runs a real worker against a `blockingSender` and proves the start returns before the
  worker send for BOTH a known and an unknown identifier, comparably timed.
  `TestPasswordlessEndToEndAllFlows` drives email link, email code, phone SMS link, and
  phone OTP end to end through the synchronous outbox drain with recorder ("console")
  delivery: accepted start → delivered secret → login via `mintSession` → refresh
  rotation → logout, per flow.

Premise adaptations (logged):

- **`passwordless_login` gains a `blocked` status (three statuses), where design §4.3
  lists only success/failure.** The pre-lookup 429 throttle is orthogonal to the
  credential disposition; recording it as `failure` would conflate a rate-limit with a
  wrong secret. The password `login` event already carries success/failure/**blocked**
  for exactly this (§5.1), so the passwordless login event mirrors it. `passwordless_start`
  keeps the design's success/blocked pair exactly (accepted = success).
- **Content-Type enforcement (`requireJSON`) deferred; strict body hardening applied.**
  §9.1's "JSON routes require Content-Type: application/json" is deferred to phase-8's
  dual JSON/form dispatch (§9.2, AV3-8.3), which owns the content-type/dispatch decision
  for these exact endpoints; the sibling credential-establishment endpoint `/auth/login`
  also does not enforce it. 7.5 applies the rest of the §9.1 body rules (size bound,
  unknown/trailing rejection) via `strictJSONBody`, so a form POST is not preemptively
  415'd before phase 8 chooses the dispatch. The existing `do`-based tests stay green
  (no Content-Type needed).
- **Phone magic-link stamps `session.MethodEmailLink` (carried from AV3-7.4).** The
  frozen method table has no `sms_link` (reserved for auth-v4); the redeem rail stamps
  `email_link` for every link kind. The `phone sms link` e2e flow exercises this path
  end to end. Flagged for design if an SMS-link method is ever wanted — not invented.
- **Live legs = the auth store conformance suites against the fresh live pgx + turso
  databases (the AV3-3.6/5.6 precedent), not a bespoke live service driver.** A
  service-level live e2e is structurally impossible from any importable location:
  `authsvc` is `internal/` to the feature core, and the core module is forbidden (guard
  G2/FS1) from importing its `stores/pgx`|`stores/turso` adapters — only a composition
  host can wire the real service against real stores, and that host is the phase-8 proof
  host (AV3-8.6). The passwordless flow rides only already-conformance-covered
  repository rails (challenge `login_magic_link`/`login_otp` rows, sessions, identifier
  `GetLogin`, `delivery_jobs`); running the conformance suites on the fresh live
  databases IS the live evidence for those rails on both dialects. The four flows
  themselves are proven end-to-end hermetically (`TestPasswordlessEndToEndAllFlows`).

Files changed:
- `domain/securityevent/securityevent.go` — `TypePasswordlessStart` /
  `TypePasswordlessLogin` constants.
- `internal/logic/authsvc/passwordless.go` — `recordPasswordlessStart` /
  `recordPasswordlessLogin` / `passwordlessChallengePurpose`; event recording woven into
  `StartPasswordless` (accepted/blocked), `VerifyPasswordless` (success/failure/blocked),
  `RedeemPasswordless` (success/failure/blocked); `securityevent` import.
- `internal/inbound/authentication/security.go` — `requireBrowserSafeOrigin`, extracted
  shared `browserOriginAllowed`; `requireBrowserSafeMutation` refactored onto it.
- `internal/inbound/authentication/passwordless.go` — `mountPasswordless` wraps the
  three routes in the credential-establishment origin gate; handlers use `strictJSONBody`.
- `internal/logic/authsvc/passwordless_events_test.go` (new) — start accepted/blocked,
  verify success/failure/blocked, redeem success/failure, PII-free event leak scan,
  slow-provider timing proof, four-flow e2e + refresh/logout.
- `internal/inbound/authentication/passwordless_test.go` — parameterized
  `newPasswordlessHandlerWith`; credential-establishment origin tests (no pre-existing
  CSRF session succeeds; cross-site rejected + no cookie on start/verify/redeem;
  allowlisted origin admitted; native client admitted).

Verification (observed):
- `cd features/authentication && go build ./... && go vet ./... && go test ./...` → all
  green (whole module; storetest reference conformance included).
- Slow-provider timing (`-run TestStartPasswordlessDoesNotBlockOnProvider -v`): **PASS**
  — `known=5.583µs unknown=3.375µs` (bound 200ms); the start completes ~5 orders of
  magnitude under the provider-block window for both, comparably timed.
- Four-flow e2e (`-run TestPasswordlessEndToEndAllFlows -v`): **PASS** — `email_link`,
  `email_code`, `phone_sms_link`, `phone_otp` each accepted → delivered → minted →
  refreshed → logged out.
- `gofmt -l` on every changed file → clean.
- **Live pgx conformance (C-collation DB, fresh/reset)** —
  `cd features/authentication/stores/pgx && POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5432/authv3_cconf?sslmode=disable' go test -run TestConformance_Postgres ./...`
  → **ok (8.369s)**. Re-proves the challenge/session/identifier/delivery_jobs rails the
  passwordless flow rides.
- **Live turso conformance (fresh/reset, local libsql substitute — AV3-2.4 precedent)** —
  `cd features/authentication/stores/turso && TURSO_DATABASE_URL='http://127.0.0.1:8080' TURSO_AUTH_TOKEN='local-dev' go test -tags=integration -run TestConformance_Turso ./...`
  → **ok (9.922s)**.

Phase-7 gate (observed):
- `make guard` → **all guards passed**.
- `make check` → **all checks passed** (templ drift + per-module build/vet/test across
  every module + integration-tag compile vet + all guards).
- Phase acceptance held: no route auto-provisions (deny-by-absence proven), no start
  calls a provider synchronously (timing proof + opaque-enqueue tests), all success
  mints through `mintSession`, and both live dialect passwordless-rail legs ran green on
  fresh databases.

For phase 8 (AV3-8.1, authentication Views port in `09-proof-host.md`):
- The passwordless service surface is frozen and complete: `StartPasswordless`,
  `VerifyPasswordless`, `RedeemPasswordless` over `authService`, one generic
  `ErrPasswordlessLogin` (401) + distinct `ErrPasswordlessRateLimited` (429), and the
  `passwordless_start`/`passwordless_login` audit pairs. The HTML Views port + form
  handlers can render over these unchanged.
- **The magic-link landing page is phase 8's (`09-proof-host.md`, AV3-8.7).** The token
  rides the URL fragment (`{PublicAuthBaseURL}#token=…`, `magicLinkURL`); the redeem is
  POST-only and consumes `{token}` — the landing page reads the fragment client-side and
  POSTs it. Server responses already carry `Cache-Control: no-store` +
  `Referrer-Policy: no-referrer`; the restrictive-CSP / history-scrub landing page is
  8.7's scope.
- **The passwordless routes carry a credential-establishment origin gate, NOT the
  double-submit CSRF gate.** When AV3-8.3 adds form submissions, the HTML passwordless
  form POST must ride the SAME origin gate (no pre-existing CSRF session) — do not put
  the passwordless form behind `requireBrowserSafeMutation` (it would demand an
  `auth_csrf` cookie a first-time signer-in does not have). `requireBrowserSafeOrigin`
  is the seam. Also revisit `requireJSON` for the JSON handlers once the form-vs-JSON
  dispatch (§9.2/8.3) is decided — content-type enforcement was deliberately deferred.
- **The service-level live passwordless e2e against real pgx/turso stores lands at the
  proof host (AV3-8.6 composition + AV3-8.10 run-and-look).** 7.5 proved the flows
  hermetically and the persistence rails live; the composed host is where the full
  wire-to-DB run happens.
