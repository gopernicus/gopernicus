# Middleware consolidation — capability ratelimit, authorization gate, host recipes

**Milestone:** middleware-consolidation (cross-cutting: sdk + two features + one example + docs)
**Status:** COMPLETE 2026-07-11 — all seven tasks landed (uncommitted working tree), `make check` green, task-4 and task-7 live-drives verified
**Verify:** per-task module `go build ./... && go test ./... && go vet ./...`, `make guard` on every boundary-touching task, `make check` to close; boot auth-cms and re-drive the gated curl legs (user-facing task 4).

## Context

The middleware review is done and three decisions are ratified (jrazmi, 2026-07-11);
this plan encodes them — it does not relitigate. (1) Generic HTTP middleware
(Panics/Logger/RequestID/CORS/DefaultHeaders) STAYS in `sdk/foundation/web` — the
layering law already assigns pure mechanism there; the "scattered" feeling is answered
by documentation, not relocation. (2) Authorization gets an exported middleware
builder in `features/authorization`, mirroring `authentication.RequireUser` — pure
`Check`, no bypass hook (the f9397ac posture: platform-admin/self-access stay host
recipes). (3) The ratelimiter×web composition inside authentication's `RateLimitByIP`
moves to `sdk/capabilities/ratelimiter/middleware.go` as a generic capability×foundation
middleware (the `cacher.Pages` / `tracing.Middleware` precedent, ARCHITECTURE.md
"Cross-capability composition"); auth keeps its exact exported surface and delegates.

Added post-panel at owner request (2026-07-11, not yet panel-reviewed): (4) port
the original gopernicus proxy middleware — `TrustProxies`, rightmost-minus-N
client-IP resolution from `gopernicus-original/bridge/transit/httpmid/trust_proxies.go`
— into `sdk/foundation/web` (task-7 / Track C / D-E). Motivation is a hardening,
not nostalgia: auth's current `clientIP()` takes the LEFTMOST X-Forwarded-For hop,
which a client can spoof to dodge `RateLimitByIP` keys and poison security-event
audit rows.

## Goal

Authorization ships an exported `RequirePermission` middleware builder, the generic
IP/key rate-limit middleware lives in `sdk/capabilities/ratelimiter`, auth-cms
demonstrates the builder composed under the host's platform-admin recipe, and
ARCHITECTURE.md states where middleware lives — with zero host-observable behavior
change in `authentication.Service.RateLimitByIP`. To be plain about the rate-limit
track: `ratelimiter.Middleware` is a ratified RELOCATION, not an example-validated
new capability — its proof is auth's unchanged existing tests, and no example
mounts it directly.

## Definition of Done

- `sdk/capabilities/ratelimiter/middleware.go` exists with tests covering allowed /
  rejected (default body + custom reject) / fail-open-on-limiter-error.
- `authentication.Service.RateLimitByIP(keyPrefix string, perMinute int) web.Middleware`
  keeps its exact signature and semantics, now delegating to the capability middleware;
  existing auth tests stay green untouched.
- `features/authorization` exports `RequirePermission` + `ResourceResolver` +
  `FixedResource`, implemented in `internal/logic/authorizersvc` with the root
  package a thin delegation (no HTTP written at root), with table tests: no
  principal → 401, denied → 403, allowed → next, engine error → 500, resolver
  error → 500 (fail closed); feature core still requires sdk only (FS1 green).
- `examples/auth-cms` `requireMembership` composes host platform-admin recipe first,
  then the builder for the Check/403 leg — verified by booting the app and driving
  the 401/403/200 curl legs, not just green tests.
- ARCHITECTURE.md carries the "where middleware lives" statement;
  CORS/DefaultHeaders are documented as available host middleware (kept surface —
  not prune candidates).
- `web.TrustProxies` + the `web.ClientIP` context read exist with
  rightmost-minus-N table tests including the spoofed-XFF case; auth's inbound
  prefers the host-resolved IP when present; auth-cms wires `TrustProxies` from
  env.
- `make check` and `make guard` green across all 36 modules.

## Out of scope

- **Moving Panics/Logger/RequestID/CORSMiddleware/DefaultHeadersMiddleware out of
  `sdk/foundation/web`** — ratified non-goal. Foundation owns pure HTTP mechanism.
- **Deleting CORSMiddleware / DefaultHeadersMiddleware.** They currently have zero
  production call sites (definitions at `sdk/foundation/web/middleware.go:102,149`
  are the only non-test hits), but they are KEPT surface — owner call, 2026-07-11:
  CORS and default security headers are expected host wiring for any API-serving or
  browser-facing host. The docs task lists them as available host middleware; they
  are NOT prune candidates.
- **Any bypass/short-circuit hook on `RequirePermission`** — the engine evaluates
  the schema, nothing else (segovia-lessons/05). Hosts compose recipes as closures.
- `sdk/foundation/workers/middleware.go` — worker-plane, not HTTP.
- **`PathValueResource` (path-param resolver)** — deferred to the first plan that
  needs a path-derived resource; hosts can write their own `ResourceResolver`
  meanwhile. No example mounts it today (auth-cms uses `FixedResource` only), and
  a host-facing resolver helper no example exercises is unproven surface.
- Rate-limit resolver-path (`*RateLimiter` / `LimitResolver`) middleware variants;
  the generic middleware takes an explicit `Limit` only.
- Cutting module tags (RELEASING.md notes below are for whenever tags happen).

## Schema / datastore impact

None. No SQL, no migrations, no store-adapter changes. `RequirePermission`
delegates to the existing `Service.Check`; storetest suites are untouched.

## Module / API impact

- **No new modules; no go.mod changes anywhere.** `sdk/capabilities/ratelimiter`
  gains an in-module file (capability → foundation/web import is legal, G12c);
  `features/authorization` already requires sdk, and `sdk/foundation/web` +
  `sdk/foundation/identity` are in-sdk imports (FS1 green, authentication precedent
  at `features/authentication/authentication.go:501-537`).
- **Exported API additions** (minor-version bumps whenever tags are first cut, per
  RELEASING.md — no tags exist yet): `ratelimiter.Middleware` (+ `Allower`, reject
  func type) and `web.TrustProxies` + `web.ClientIP` in sdk;
  `authorization.RequirePermission`, `authorization.ResourceResolver`,
  `authorization.FixedResource`. Note `ratelimiter.Middleware` is a relocation of
  auth-internal behavior — its proof is auth's unchanged tests, not a new
  example-mounted capability.
- **One deliberate behavior change** (task-7, opt-in): a host that wires
  `web.TrustProxies` changes what IP auth's rate-limit keys and audit rows see —
  the intended hardening. Hosts that don't wire it are unchanged.
- **No exported-API change** in `features/authentication` (internal delegation only)
  and no signature change in `examples/auth-cms` (hosts are untagged demonstrations).
- **Guard delta:** G6 (`guard-feature-transport-sdk-web`) today greps only
  `features/*/internal/`, leaving feature-root files (and `domain/`/`memstore/`/
  `storetest/`) outside FS9 — the blind spot `features/authentication/authentication.go`
  already sits in. With D-C's restructure the new root file writes no HTTP (the
  implementation lives in `internal/logic/authorizersvc`), so task-5's widening is
  defense-in-depth, not the thing legitimizing a root transport: it flips G6 to an
  exclusion-style grep over all of `features/` so FS9 is enforced everywhere
  non-adapter feature code lives.

## Generated-artifact impact

None. No `.templ` sources touched; `make generate` remains a no-op for this work.

## Design decisions (pinned for the implementer)

**D-A — `ratelimiter.Middleware` shape** (`sdk/capabilities/ratelimiter/middleware.go`):

```go
// Allower is the one method Middleware consumes; Limiter satisfies it.
type Allower interface {
    Allow(ctx context.Context, key string, limit Limit) (Result, error)
}

// Middleware returns web.Middleware that throttles requests on key(r) against
// limit. reject is called on a denied request; nil reject → the default JSON
// 429 via web.RespondJSONError (code "rate_limited"). A limiter ERROR fails
// OPEN — the request proceeds; reject fires ONLY on err == nil && !res.Allowed.
func Middleware(l Allower, limit Limit, key func(*http.Request) string,
    reject func(http.ResponseWriter, *http.Request, Result)) web.Middleware
```

- Accept the narrow `Allower`, not `Limiter` (auth's seam holds a
  `ratelimiter.Limiter` and calls only `Allow` — lead-backend (a): don't demand
  `Reset`/`Close` the HTTP seam never uses) and not `*RateLimiter` (auth
  deliberately has no resolver).
- **Fail-open parity is exact**: reject solely on `err == nil && !res.Allowed`,
  proceed on any limiter error — replicating
  `features/authentication/internal/logic/authsvc/service.go:752-763` bit for bit.
  Anything else silently flips auth from fail-open to fail-closed (lead-backend (d)).
- **A limiter error fails open silently BY DESIGN** (platform-sre, resolved
  documented-by-design): the middleware takes no logger and emits nothing on the
  `err != nil` branch — parity with auth today. A host needing visibility wraps its
  `Allower` with a logging/metrics decorator (the `Allower` seam is the
  observability point) and/or relies on limiter-side alerting. The doc comment must
  state this so silent-fail-open is never mistaken for a monitored state.
- Default reject body: `web.RespondJSONError(w, web.NewError(http.StatusTooManyRequests,
  "too many requests").WithCode("rate_limited"))` — byte-identical to auth's
  `writeTooManyRequests` today. It MAY additionally set `Retry-After` from
  `Result.RetryAfter`; that touches only the sdk default, because auth passes its
  own reject (next bullet), keeping "no host-observable change" true by construction.

**D-B — auth delegation** (`features/authentication/internal/logic/authsvc/service.go`):
`Service.RateLimitByIP` body becomes a call to `ratelimiter.Middleware(s.limiter,
ratelimiter.PerMinute(perMinute), keyFunc, rejectFunc)` where `keyFunc` is a closure
reading `clientInfoFromContext(r.Context()).ip` (the private carrier stays private —
this is why the wrapper lives in authsvc) and building `keyPrefix + ":" + ip`, and
`rejectFunc` wraps the existing `writeTooManyRequests` (FS9 shape pinned by the
feature, not incidentally by the sdk default). The exported
`authentication.go:723` re-export is untouched.

**D-C — `RequirePermission` shape** (implementation in
`features/authorization/internal/logic/authorizersvc`, root package a thin
delegation — authentication's precedent read *correctly*: its root `Require*` are
one-line delegations, the handler bodies live in `internal/logic/authsvc`. The
root package writes NO HTTP; "services and HTTP are `internal/`" anatomy holds.
`Resource`/`CheckRequest` already live in `authorizersvc/model.go` and the root
already aliases them, so the internal implementation has every type it needs):

```go
// ResourceResolver extracts the resource to authorize from the request.
type ResourceResolver func(r *http.Request) (Resource, error)

// FixedResource always resolves the same resource (the auth-cms demo case).
func FixedResource(resourceType, resourceID string) ResourceResolver

// RequirePermission returns web.Middleware gating a route on the context
// Principal holding permission on the resolved resource. Pure Check — no
// bypass hook; hosts compose recipes (platform-admin) as their own closures.
func (s *Service) RequirePermission(permission string, resource ResourceResolver) web.Middleware
```

The exported root surface: `ResourceResolver` aliased in the existing alias block,
`FixedResource` and `(s *Service) RequirePermission` in a thin root
`features/authorization/middleware.go` that checks the relationships wiring, panics
if absent, and otherwise delegates to the `authorizersvc` implementation.

Semantics, all via `web.RespondJSONError` (FS9 parity with authentication's
`writeUnauthorized`):
- `identity.FromContext(r.Context())` not ok → 401 `web.ErrUnauthorized("authentication required")`.
- resolver error → 500 — fail closed (simplest mapping; a richer `*web.Error`
  passthrough is deferred with `PathValueResource`, the only planned resolver that
  needed it).
- `s.Check` error → 500 fail closed; `!res.Allowed` → 403 `web.ErrForbidden(...)`.
- **Fail-fast on misconfiguration** (lead-backend unasked landmine):
  `RequirePermission` checks the unexported relationships wiring directly (nil
  field) and panics at REGISTRATION/BOOT time — when the host mounts the builder at
  route registration, before serving traffic; NOT caught by `go build`/CI — with a
  message naming the missing relationships kind. It does NOT probe `Check`; the
  `ErrRelationshipsNotConfigured` sentinel remains the runtime guard for direct
  `Check` callers. Document it; a roles-only host must not mount this builder, and
  hosts must mount at registration (not lazily) so the panic fires at boot.
- Update the package doc: it currently bills the feature as mounting no routes /
  view-free; amend so the exported HTTP middleware surface doesn't read as forbidden.

**D-E — `web.TrustProxies` port** (added post-panel at owner request, 2026-07-11;
`sdk/foundation/web/trustproxies.go`):

```go
// TrustProxies returns middleware resolving the real client IP via the
// rightmost-minus-N X-Forwarded-For algorithm (trustedProxyCount reverse
// proxies in front of the server; 0 = trust RemoteAddr only) and stashing it
// on the request context.
func TrustProxies(trustedProxyCount int) Middleware

// ClientIP returns the TrustProxies-resolved client IP, if any.
func ClientIP(ctx context.Context) (string, bool)
```

- Port of `gopernicus-original/bridge/transit/httpmid/trust_proxies.go`:
  rightmost-minus-N over X-Forwarded-For, X-Real-IP single-proxy fallback,
  RemoteAddr base case (port stripped). Pure HTTP mechanism with no capability
  port behind it → foundation/web, stdlib-only, FLAT — legal.
- **The carrier lives in web, NOT the sdk kernel.** Its consumers (auth's inbound,
  web itself) already import web; kernel promotion is "a visible, deliberate act"
  this doesn't need. Unexported context key, `ClientIP` the only read.
- **Why now (hardening):** auth's `clientIP()`
  (`features/authentication/internal/inbound/authentication/routes.go:100`) takes
  the LEFTMOST X-Forwarded-For hop — client-spoofable: an attacker sets the header
  to rotate `RateLimitByIP` keys and poison security-event audit attribution.
  Rightmost-minus-N with an explicit trusted-proxy count is the correct algorithm
  (the original's semantics, kept exactly).
- **Auth consumption:** `clientIP(r)` in auth's inbound prefers
  `web.ClientIP(r.Context())` when present; when TrustProxies is unwired, the
  current leftmost-XFF fallback stays (back-compat for existing hosts), documented
  as spoofable-by-design-of-legacy with "wire TrustProxies" as the fix. **OPEN —
  owner call at ratification:** alternatively harden the fallback to
  RemoteAddr-only (safer default; but proxied hosts that haven't wired
  TrustProxies would see rate-limit keys collapse onto the proxy IP).

**D-D — deliberately opposite fail postures.** Ratelimiter middleware fails OPEN
(availability of public routes beats a limiter outage); `RequirePermission` fails
CLOSED (engine error → 500, matching `membership.go`'s posture). This is intentional
and must be stated in both doc comments and ARCHITECTURE.md so nobody "harmonizes"
them later. The fail-open path is additionally SILENT by design — no logger, no
emitted signal; the ARCHITECTURE.md note must say so and name the compensations (an
`Allower` logging/metrics decorator at the host, and/or limiter-side alerting) so
nobody treats silent-fail-open as a monitored state.

## Risks

1. **Fail-open flip in auth's rate limiting.** If the generic middleware's error
   handling deviates from `err == nil && !res.Allowed`, hosts' public routes start
   500ing/429ing on limiter outage. Mitigated by an explicit fail-open table test
   (erroring fake Allower → request proceeds) plus auth's existing tests.
2. **auth-cms 401/403 body shape changes.** `requireMembership` currently writes
   host JSON (`{"error": "not authorized on project/demo", "permission": "view"}`);
   the builder writes the FS9 `web.Error` shape. The README's A9 legs assert status
   codes, not those bodies (verified), but task-4 must re-drive the protocol live
   and touch any narrative that quotes the old body.
3. **G6 widening false-positives.** Flipping the FS9 guard to the exclusion-style
   grep over all of `features/` could flag legitimate existing code; the pattern was
   dry-run clean during review (twice, independently), and task-5 re-runs it across
   the tree and reports before committing.
4. **TrustProxies misconfiguration shifts rate-limit keys.** A host that wires
   `TrustProxies` with the wrong count keys rate limits and audit rows on a proxy
   IP (over-throttling shared clients) or on a still-spoofable hop. Mitigated by
   the doc comment spelling out the count semantics (0 = RemoteAddr only; N =
   number of trusted proxies) and the task-7 live-drive spoof check; auth's
   preference is inert for hosts that don't wire it (plan default, D-E open call).

## Tasks

### task-1: generic rate-limit middleware in sdk/capabilities/ratelimiter

- **depends_on:** []
- **model:** opus
- **files:**
  - `sdk/capabilities/ratelimiter/middleware.go` (new)
  - `sdk/capabilities/ratelimiter/middleware_test.go` (new)
- **verify:** `cd sdk && go build ./... && go test ./... && go vet ./...` then `make guard`
- **description:** Implement `Allower` + `Middleware` per D-A, with the doc comment
  carrying the fail-open rationale (D-D) and the cacher.Pages-style
  capability×foundation placement note. Table tests: allowed → next runs; denied +
  nil reject → 429 JSON with code `rate_limited`; denied + custom reject → custom
  writer fires, default doesn't; limiter error → request PROCEEDS (fail-open);
  key func output reaches the Allower as the key.

### task-2: authentication.RateLimitByIP delegates to the capability middleware

- **depends_on:** [task-1]
- **model:** opus
- **files:**
  - `features/authentication/internal/logic/authsvc/service.go` (RateLimitByIP body, ~752-763)
- **verify:** `cd features/authentication && go build ./... && go test ./... && go vet ./...` then `make guard`
- **description:** Rewrite `RateLimitByIP`'s body per D-B: same exported signature,
  same key derivation (`keyPrefix + ":" + clientIP` from the private clientInfo
  carrier), same `writeTooManyRequests` rejection, semantics delegated to
  `ratelimiter.Middleware`. No exported-surface change in `authentication.go`; do
  not touch existing tests — they are the parity oracle and must pass unmodified.

### task-3: authorization RequirePermission builder + resolvers + tests

- **depends_on:** []
- **model:** opus
- **files:**
  - `features/authorization/internal/logic/authorizersvc/middleware.go` (new — the implementation)
  - `features/authorization/internal/logic/authorizersvc/middleware_test.go` (new — the table tests)
  - `features/authorization/middleware.go` (new, root package — thin delegation only, no HTTP written)
  - `features/authorization/middleware_test.go` (new — exported-surface + panic tests)
  - `features/authorization/authorization.go` (package doc amendment only)
- **verify:** `cd features/authorization && go build ./... && go test ./... && go vet ./...` then `make guard`
- **description:** Implement `ResourceResolver`, `FixedResource`, and
  `RequirePermission` per D-C: the handler bodies and `web.RespondJSONError`
  responses live in `authorizersvc` (a method on the internal engine Service —
  the middleware is relationships-kind-only, so the engine is its natural owner
  and `Resource`/`CheckRequest` are already in `model.go`); the root
  `features/authorization/middleware.go` carries the alias/`FixedResource`
  re-export and the thin `(s *Service) RequirePermission` that panics at
  registration/boot time when the relationships kind is unwired (nil-field check,
  message naming the missing kind — never probing `Check`) and otherwise
  delegates. Doc comments state the pure-Check/no-bypass posture (point at the
  host-recipe pattern) and the fail-closed D-D note. Table tests over a
  memstore-backed Service with a small schema: no principal → 401; principal + no
  grant → 403; granted → next runs (and sees the original request); engine error
  (erroring relationship.Storer fake, the `authorization_test.go` relFake
  precedent) → 500; resolver error → 500; roles-only Service → builder panics at
  mount. Amend the package doc so the exported middleware surface is named
  alongside "Register mounts no routes".

### task-4: auth-cms requireMembership composes the builder under the host recipe

- **depends_on:** [task-3]
- **model:** opus
- **files:**
  - `examples/auth-cms/cmd/server/membership.go`
  - `examples/auth-cms/README.md` (only if narrative quotes the old 401/403 bodies)
- **verify:** `cd examples/auth-cms && go build ./... && go vet ./... && go test ./...`; then run-and-look: `cd examples/auth-cms && AUTH_DEBUG=1 go run ./cmd/server` and drive the README A9 legs — unauthenticated `GET /demo/members-only` → 401, resolved non-member → 403 (now the FS9 `web.Error` JSON shape), invited member → 200, platform admin → 200 via the host recipe. `requireMembership` stays mounted under `RequirePrincipal` — the builder's 401 depends on that ordering; do not reorder the mount.
- **description:** Keep `isPlatformAdmin` and its teaching comments intact — it is
  the flagship host-recipe demonstration. Rework `requireMembership` to: build
  `gate := authorizer.RequirePermission(demoPermission, authorization.FixedResource(demoResourceType, demoResourceID))`
  once; per-request, read the principal once at the top of the closure and run the
  host platform-admin recipe ONLY when a principal is present (admin → next),
  otherwise fall directly to the builder-gated handler (its 401 leg) — no
  empty-subject engine `Check` on unauthenticated hits. Non-admin principals fall
  through to the builder too (its 403/500 legs). Delete the now-orphaned host
  401/403 writes in this closure; update the
  function comment to say the Check/403 leg is the feature's exported builder while
  platform-admin remains host composition. Touch the README only where it quotes
  removed body shapes; the status-code legs stand.

### task-5: widen G6 (guard-feature-transport-sdk-web) to all non-adapter feature code

- **depends_on:** [task-3]
- **model:** opus
- **files:**
  - `Makefile` (guard-feature-transport-sdk-web, ~line 137)
- **verify:** `make guard` (all guards green); plant a positive `http.Error(` control in BOTH a feature-root file and an `internal/` file, confirm both trigger the widened guard, then remove the plants
- **description:** Flip the FS9 guard from the `features/*/internal/` prefix to the
  exclusion style (the G2 precedent):
  `grep -rn --include='*.go' --exclude='*_test.go' --exclude-dir=stores --exclude-dir=views -E 'json\.NewEncoder\(|http\.Error\(' features/`
  — one expression covering root, `domain/`, `memstore/`, `storetest/`, and
  `internal/`, closing the root-file blind spot authentication's root package sits
  in (dry-run verified clean twice during review). First re-run the pattern across
  the tree and fix or report any existing hits before committing it — expected
  clean, since root packages delegate.

### task-7: port web.TrustProxies; auth prefers the host-resolved client IP

- **depends_on:** []
- **model:** opus
- **files:**
  - `sdk/foundation/web/trustproxies.go` (new)
  - `sdk/foundation/web/trustproxies_test.go` (new)
  - `features/authentication/internal/inbound/authentication/routes.go` (clientIP preference)
  - `examples/auth-cms/cmd/server/main.go` (wire `web.TrustProxies` from env, count 0 default)
- **verify:** `cd sdk && go build ./... && go test ./... && go vet ./...`; `cd features/authentication && go build ./... && go test ./... && go vet ./...`; `make guard`; then run-and-look: boot auth-cms and confirm a login with a forged `X-Forwarded-For: 6.6.6.6` header no longer attributes/rate-limits as 6.6.6.6 when TrustProxies is wired (count 0 → RemoteAddr wins)
- **description:** Port D-E: `TrustProxies` + the unexported context carrier +
  `ClientIP` into `sdk/foundation/web`, semantics identical to the original
  (rightmost-minus-N, X-Real-IP single-proxy fallback, RemoteAddr base case).
  Table tests: count 0 → RemoteAddr; count 1/2 over multi-hop XFF; spoofed XFF
  with count 0 ignored; X-Real-IP fallback; malformed RemoteAddr. Auth's
  `clientIP()` prefers `web.ClientIP(r.Context())` when present, existing
  fallback otherwise (or RemoteAddr-only if the owner hardens the fallback at
  ratification — D-E open call); auth-cms mounts `web.TrustProxies(n)` in its
  stack (n from env, default 0) outer of the feature mounts. The workshop init
  template gains it in a follow-up, not here.

### task-6: docs — where middleware lives

- **depends_on:** [task-1, task-2, task-3, task-4, task-7]
- **model:** opus
- **files:**
  - `ARCHITECTURE.md`
  - `features/authorization/README.md`
  - `features/README.md` (one-line charter note)
  - `RELEASING.md` (next-tag breadcrumbs)
  - `NOTES.md` (dated ledger entry, house discipline)
- **verify:** `make check`
- **description:** Add a short "Where middleware lives" statement to ARCHITECTURE.md
  (near the layering-law section): foundation = pure HTTP mechanism
  (Panics/Logger/RequestID/CORS/DefaultHeaders in `sdk/foundation/web`);
  capability×foundation composition = in the capability (`cacher.Pages`,
  `tracing.Middleware`, `ratelimiter.Middleware`); identity/authorization gates =
  exported by the owning feature **as root-package re-exports of internal
  implementations** (`authentication.RequireUser`,
  `authorization.RequirePermission`) — reinforcing, not amending, the "services
  and HTTP are `internal/`" anatomy — injected by hosts through `[]web.Middleware`
  config seams (`cms.Config.AdminMiddleware`, `events.Config.StreamMiddleware`) or
  route registration; host recipes = host closures (auth-cms `isPlatformAdmin`).
  The foundation row includes `TrustProxies` (task-7) and lists
  `CORSMiddleware`/`DefaultHeadersMiddleware` as available host middleware — kept
  deliberately (owner call 2026-07-11) even though no example wires them yet; NOT
  prune candidates. Note the deliberate opposite fail postures (D-D) INCLUDING
  that the ratelimiter's fail-open is silent by design, with the `Allower`
  logging/metrics decorator and limiter-side alerting named as the compensations.
  Document
  `RequirePermission` in the authorization README next to the existing host-recipe
  section — LEADING with "requires the relationships kind wired; a roles-only host
  must not mount it (registration/boot-time panic)" and a one-line "mount at
  registration, not lazily" instruction, then the shape, the pure-Check/no-bypass
  posture, and the FS9 `web.Error` 401/403 body shape as a host contract (an
  adopter replacing a hand-rolled closure changes its response body contract).
  Add the `features/README.md` charter note (a feature may export HTTP middleware
  gates as root-package re-exports of internal implementations — authentication
  and authorization both do), the RELEASING.md breadcrumbs (sdk and
  features/authorization gain additive minor-floor symbols;
  features/authentication stays patch-only — internal delegation), and the
  NOTES.md ledger entry for the milestone.

## Sequencing

Three independent tracks that merge at the docs task:

- **Track A (rate limiting):** task-1 → task-2.
- **Track B (authorization gate):** task-3 → task-4, task-3 → task-5.
- **Track C (trusted proxies):** task-7.
- **Close:** task-6 after all tracks, then a final `make check` + the task-4 and
  task-7 live-drive re-confirmations close the milestone.

## Consultation notes

`lead-backend-engineer` reviewed the API sketch (2026-07-11), verdict
ship-with-edits; all four findings are folded in: (a) accept a narrow one-method
`Allower` instead of `Limiter`/`*RateLimiter` (D-A); (b) the G6 guard gap for
root-package feature transports — now task-5 — plus the authorization package-doc
amendment (task-3); (c) module/tagging clean, no go.mod churn anywhere; (d) exact
fail-open parity (`err == nil && !res.Allowed`) pinned in D-A/D-B, auth keeps its
own reject closure, and the deliberately opposite fail postures are documented
(D-D). The unasked landmine — `RequirePermission` on a roles-only Service turning
misconfiguration into per-request 500s — became the registration/boot-time panic
in D-C.

**Review panel (2026-07-11):** product-manager, architecture-steward,
lead-backend-engineer (post-hoc), platform-sre — all four ship-with-edits, all
accepted findings folded: the D-C restructure (implementation in
`authorizersvc`, root package a thin no-HTTP delegation — architecture-steward's
major finding, the authentication precedent read correctly);
`PathValueResource` cut as unproven surface no example mounts (product-manager);
fail-open observability resolved as documented-by-design with the
`Allower`-decorator pattern named as the host's observability seam, no logger
threaded (platform-sre high); "BUILD time" reworded to registration/boot time
with the nil-field panic mechanism named, never probing `Check` (lead-backend +
platform-sre); task-4 reads the principal first so no empty-subject engine
`Check`, and the `RequirePrincipal` mount-order dependency is pinned
(lead-backend); G6 flipped to the exclusion-style grep covering all non-adapter
feature code, positive controls planted in both arms (architecture-steward +
platform-sre); task-6 gains the `features/README.md` charter note, RELEASING.md
breadcrumbs, and the FS9 body-shape host contract (product-manager +
platform-sre); the ratelimiter track reframed as a ratified relocation, not an
example-validated capability (product-manager).

## Open questions

_None — resolved at ratification (jrazmi, 2026-07-11):_

1. **D-E fallback posture:** RESOLVED — keep today's leftmost-XFF fallback when
   `TrustProxies` is unwired (back-compat), documented as spoofable with "wire
   TrustProxies" as the fix.
2. **Task-7 review coverage:** RESOLVED — accepted on owner review at
   ratification; no extra panel pass.

The three original owner decisions are ratified; the remaining shape calls
(Allower, reject-func default, resolver-error mapping, registration/boot-time
panic, G6 widening) are made above and were reviewed by the four-agent panel.

## Reviews — completed 2026-07-11 (all ship-with-edits, folded above)

- **product-manager** — scope discipline; auth-cms teaching value confirmed
  protected; `PathValueResource` cut; relocation framing added.
- **architecture-steward** — D-A placement confirmed; D-C restructured to
  internal implementation + thin root delegation; G6 flipped to exclusion style.
- **lead-backend-engineer** — post-hoc pass: all four pre-plan findings landed
  correctly; panic-mechanism wording and task-4 principal-first ordering folded.
- **platform-sre** — fail-open documented as silent-by-design with the `Allower`
  decorator seam named; boot-time terminology fixed; RELEASING.md breadcrumbs
  added to task-6.

## Notes

- Verified during planning: `CORSMiddleware`/`DefaultHeadersMiddleware` have zero
  non-test call sites repo-wide; auth's limiter seam is the `ratelimiter.Limiter`
  port called with explicit `PerMinute` limits (never the resolver path); the
  auth-cms README asserts status codes, not the host 403 body; `identity.FromContext`
  is the platform-wide principal read (`sdk/foundation/identity/identity.go:108`)
  and `RequirePrincipal` stashes via `identity.WithPrincipal`, so the builder
  composes under it with no new plumbing.
- `sdk/foundation/workers/middleware.go` untouched — worker-plane middleware is a
  different axis (job execution, not HTTP).
- Owner review round 2 (2026-07-11): CORS/DefaultHeaders un-flagged as prune
  candidates (kept host surface); Track C / task-7 / D-E added — the original
  gopernicus `httpmid.TrustProxies` ported to foundation/web, motivated by the
  spoofable leftmost-XFF `clientIP()` in auth's inbound. On the cache question:
  capability cache middleware already exists — `cacher.Pages`
  (`sdk/capabilities/cacher/middleware.go`, GET-HTML page caching consumed via
  `cms.Config.Cache`); generalizing it (JSON/API responses, Cache-Control) is
  deferred until a consumer exists.
- The original's `httpmid` suite was also inventoried for other port candidates:
  logger/panics/rate_limit/telemetry/authenticate/authorize/client_info all have
  new-repo equivalents (web, ratelimiter+this plan, tracing, features);
  `body_limit` (request-size cap) has NO equivalent and is a reasonable future
  foundation/web addition; `tenant` is out of scope for the current architecture.
