# gopernicus — coordination-hub authentication upstream batch

Status: **PROPOSED — PLANNING ONLY.** Tasks are dispatched individually after
the owner settles this plan. Nothing in this file authorizes implementation,
tag creation, pushes, or coordination-hub changes.

Date: 2026-08-14

Driver: coordination-hub's authentication milestone (Google OAuth,
email/password, passwordless email, and cookie-authenticated SPA sessions).

This is the single gopernicus-side plan of record. It combines the original
upstream defect register with the later `upstream-2` capability and release
requests. The evidence for the known defects remains in the second half of this
file so no dispatched task has to re-derive it.

Consumed versions at the time of planning: `sdk` and
`integrations/datastores/pgxdb` at **v0.2.0**;
`features/authentication` and `integrations/oauth/google` at **v0.1.0**.
coordination-hub adopts each completed module in the usual way:

```sh
go get github.com/gopernicus/gopernicus/<module>@<tag>
go mod tidy
go mod vendor
```

## Goal

Tagged gopernicus releases exist such that coordination-hub can build its full
login experience through version bumps and explicit host wiring only: no
vendored patch, duplicated internal auth contract, local CORS workaround, or
missing Postgres-compatible production dependency.

## Proposed design rulings

1. The blocked browser case is a **same-site, cross-origin,
   cookie-authenticated SPA**. Bearer-only clients already work; arbitrary
   cross-site cookie authentication remains outside the `SameSite=Lax` posture.
2. CORS remains generic sdk mechanism. The sdk gains a configurable header
   policy; it does not import or otherwise know about authentication's
   `X-CSRF-Token` header. The host opts that header in.
3. Authentication exposes a full `RefreshCookiePath`, not a generic route
   prefix. Empty defaults to `/auth`; coordination-hub supplies
   `/api/v1/auth`. The same resolved path is used for issue and deletion.
4. The JSON CSRF bootstrap is `GET /auth/csrf`, protected by
   `RequireLiveSession` plus the origin-only browser gate. It sets the CSRF
   cookie and returns the matching token under `Cache-Control: no-store`.
   Passwordless and other first-credential endpoints do not need this token.
5. `WebHandler.Use` means genuinely global middleware: it surrounds the whole
   mux, including mux-generated redirects/404s/405s and `HandleRaw`
   registrations. `HandleRaw` remains an escape hatch for registering an
   arbitrary `http.Handler`/ServeMux pattern, **not** an escape hatch from panic
   recovery, request IDs, logging, CORS, or other global policy. Its current
   bypass documentation and behavior change accordingly. Ordinary routes use
   `Handle`; `HandleRaw` is reserved for the rare caller that genuinely needs
   the raw `http.Handler`/pattern surface.
6. `GET /auth/me` uses `RequireLiveSession`. Hydration costs one revocation
   lookup, intentionally matching the immediate-revocation posture of
   `GET /auth/methods`.
7. The recommended pgx limiter placement is
   `integrations/datastores/pgxdb`, not a second
   `integrations/ratelimiter/pgx` module. The integration unit is the external
   library/client: `kvstores/goredis` already establishes that one connector may
   implement several sdk ports. `pgxdb` already owns pgx/v5 and depends on sdk;
   duplicating that boundary would create two modules for one library.
8. Invitation-driven account provisioning is deferred to the campaign-onboarding
   milestone. It is a real pre-authentication security design, is not required
   for v1 login, and must not ride this release batch.

## Definition of done

- A credentialed request from an explicitly allowed SPA origin can obtain a
  CSRF token from a no-store JSON body, retain the matching API-origin cookie,
  pass an `X-CSRF-Token` preflight, and complete a browser-safe mutation without
  reading the API cookie.
- A host mounting authentication beneath `/api/v1` can refresh and log out via
  cookies; both issue and clear use `Path=/api/v1/auth`.
- `GET /auth/me` returns the unmasked login-shaped current-user body for a live
  session and rejects missing or revoked sessions.
- Global middleware observes ordinary routes, `HandleRaw` routes, and
  ServeMux-generated redirects/404s/405s while preserving route middleware
  order and automatic 405 `Allow` headers.
- A production authentication host can use the existing pgxdb connection as a
  durable, cross-instance `ratelimiter.Limiter`, with atomic concurrency proven
  against live Postgres.
- Google OAuth is green against the sdk version coordination-hub will consume.
- Upgrade notes and an owner-cut tag manifest exist for every changed module;
  cold-cache downstream resolution is verified after the owner cuts tags.

## API and schema impact

Authentication adds:

- `Config.RefreshCookiePath string` (empty means `/auth`);
- `GET /auth/csrf` returning `{"csrf_token":"..."}` and setting the matching
  cookie; and
- `GET /auth/me` returning the existing `userResponse` shape:
  `{id,email,display_name,email_verified}`.

Authentication changes no existing request body and removes no route. An
origin-gate rejection gains a stable `origin_rejected` code. Refresh-cookie
response shape is unchanged; only its configurable `Path` changes.

The sdk adds an additive CORS configuration entry point while retaining
`CORSMiddleware(origins)` as the compatibility constructor. Global middleware
semantics change deliberately as described above.

The pgxdb limiter requires one host-owned rate-window table. gopernicus ships
reference DDL and a pruning statement in the connector README; it never runs a
host migration. coordination-hub copies that DDL into its own migration ledger.

## Tasks

### U1 — sdk CORS policy and genuinely global middleware

- **depends_on:** none
- **files:** `sdk/foundation/web/middleware.go`,
  `sdk/foundation/web/middleware_test.go`,
  `sdk/foundation/web/handler.go`, handler tests, and affected sdk docs/examples
- **deliverables:**
  - Keep `CORSMiddleware(origins []string)` source-compatible and add an
    additive config-based constructor. The config carries allowed origins,
    allowed request headers, and exposed response headers. A nil allowed-header
    list selects today's `Accept, Content-Type, Authorization` default; a
    non-nil list replaces it, so coordination-hub can include
    `X-CSRF-Token`. A nil expose list defaults to `X-Request-ID`; an explicit
    empty list suppresses the expose header.
  - Add `Vary: Origin` without overwriting or duplicating existing `Vary`
    dimensions, for both matched and unmatched origins.
  - Preserve wildcard behavior: `*` may echo an origin but never produces
    `Access-Control-Allow-Credentials`.
  - Return the preflight 204 only for `OPTIONS` carrying both `Origin` and
    `Access-Control-Request-Method`; other `OPTIONS` requests reach the mux.
  - Apply `Use` once around the entire mux rather than baking it into registered
    patterns. Preserve outermost-first global order, route-local order,
    ServeMux redirect/404/405 behavior, and the automatic 405 `Allow` header.
  - Make `HandleRaw` participate in global middleware. Update its contract to
    describe direct `http.Handler` registration only. Prove that an OpenAPI/raw
    handler panic is recovered when `Panics` is installed globally.
- **acceptance tests:** configured CSRF allow-header; default exposed request ID;
  `Vary` on allowed and rejected origins; genuine vs non-preflight `OPTIONS`;
  `router.Use(CORSMiddleware(...))` handles a preflight for a method-qualified
  route; global middleware sees 404, 405, and `HandleRaw`; route/global order is
  unchanged.
- **verify:** from `sdk`, `go build ./... && go test ./... && go vet ./...`;
  then repo-root `make guard`.

### U2 — authentication refresh-cookie path

- **depends_on:** none
- **files:** `features/authentication/authentication.go`,
  `features/authentication/internal/logic/authsvc/service.go`, auth construction
  and cookie tests, and `features/authentication/README.md`
- **deliverables:**
  - Add public `Config.RefreshCookiePath string` and an exported, typed
    construction error for invalid values.
  - Empty resolves to `/auth`. A non-empty value must be a valid absolute cookie
    path: leading slash, no query/fragment/control/header-delimiter characters,
    and no trailing slash except `/` itself.
  - Thread the resolved value into authsvc cookie policy. Use it for every
    refresh-cookie issue and deletion; do not change the access cookie or
    `SameSite=Lax`.
- **acceptance tests:** default remains `/auth`; `/api/v1/auth` is issued on
  login/rotation and deleted on logout; a real cookie-jar/path-match test proves
  the cookie is sent to `/api/v1/auth/refresh` and `/api/v1/auth/logout` but not
  unrelated paths; invalid values fail at construction.
- **verify:** from `features/authentication`,
  `go build ./... && go test ./... && go vet ./...`; then repo-root
  `make guard`.

### U3 — JSON CSRF bootstrap and origin-rejection code

- **depends_on:** U1
- **files:** `features/authentication/internal/inbound/authentication/security.go`,
  `routes.go`, security/session tests, and `features/authentication/README.md`
- **deliverables:**
  - Register `GET /auth/csrf` with `RequireLiveSession` and the existing
    origin-only browser gate. If the request already carries a valid non-empty
    CSRF cookie, return that value; otherwise mint through `issueCSRFToken`.
    Set `Cache-Control: no-store` and return
    `{"csrf_token":"<cookie value>"}`. This avoids invalidating another tab's
    in-flight token on every bootstrap. A randomness failure returns a safe 500
    and no successful body.
  - Split origin denial from generic forbidden/CSRF denial. Origin failures use
    HTTP 403 with `code: "origin_rejected"`; token mismatch remains a separate
    gate failure. Keep human messages non-sensitive.
  - Do not expose authentication header constants from sdk or weaken the
    double-submit comparison.
- **acceptance tests:** an allowlisted, same-site cross-origin test performs
  login, obtains the body token without reading the cookie, preflights through
  the configured sdk CORS middleware, then completes
  `POST /auth/password/change` with the session cookie, returned CSRF cookie,
  and `X-CSRF-Token`. Missing/revoked sessions cannot bootstrap. A disallowed
  origin gets `origin_rejected` when CORS permits the response to be read.
- **verify:** authentication module build/test/vet plus repo-root `make guard`.

### U4 — `GET /auth/me` session hydration

- **depends_on:** none logically; dispatch after U3 to avoid simultaneous edits
  to `routes.go`
- **files:** authentication `routes.go`, a new inbound `me.go`/`me_test.go`,
  an authsvc current-user view/service method and tests, and the authentication
  README
- **deliverables:**
  - Add a narrow internal current-user view that loads `users.Get(ctx, userID)`
    plus the active primary email identifier and its verification state. The
    current handler interface has `CurrentUser` and `userResponseFor` but no way
    to load the `user.User` aggregate, and `ActiveVerifiedIdentifier` alone
    cannot reproduce login's email field for an allowed-but-unverified account.
  - Register `GET /auth/me` behind `RequireLiveSession`.
  - Resolve the user ID from context and map the service view through the same
    `userResponse` DTO as login/register: unmasked active primary email,
    display name, and the correct verified flag. Refactor the existing response
    helper if useful so the three paths cannot drift. Set
    `Cache-Control: no-store`.
  - A machine/API-key principal is not a current user and receives 401 rather
    than a fabricated profile.
- **acceptance tests:** 401 without credentials; 401 after session revocation;
  401 for a machine principal; correct body for a live cookie and bearer-backed
  user session; no-store header; prefixed registration yields
  `/api/v1/auth/me` without handler changes.
- **verify:** authentication module build/test/vet plus repo-root `make guard`.

### U5 — durable pgx-backed limiter in `pgxdb`

- **depends_on:** none
- **files:** new limiter implementation/tests under
  `integrations/datastores/pgxdb`, its README, and the live-integration workflow
- **deliverables:**
  - Add a pgxdb `Limiter` satisfying
    `sdk/capabilities/ratelimiter.Limiter`. It uses the caller-owned `*DB`;
    `Close` is idempotent and does not close the shared connection pool.
  - Match goredis's sliding-window approximation, including
    `Requests + Burst`, independent keys, reset, remaining count, reset time,
    and retry-after behavior.
  - Use Postgres server time for window selection, reset time, and retry-after;
    caller clock skew must not affect decisions or returned durations.
  - Make each `Allow` an atomic statement/transactionally indivisible row
    transition. Under N concurrent instances and a limit of K, exactly K calls
    may return allowed. Denied calls do not consume additional quota.
  - Namespace keys with a configurable prefix. Never concatenate keys or table
    names into SQL unsafely; the table name is fixed framework reference DDL.
  - README reference DDL defines the one window table plus an expiry/pruning
    index and a host-run pruning statement. State explicitly that hosts copy the
    DDL into their own migration ledger and schedule pruning; the connector
    creates and migrates nothing.
- **acceptance tests:** shared `ratelimitertest.Run`; live Postgres concurrency
  proof under `-race`; server-time behavior; reset; burst; independent keys;
  context cancellation; caller-owned DB remains usable after limiter close.
  Tests skip loudly when `POSTGRES_TEST_DSN` is absent and run in the existing
  live Postgres CI job when present.
- **verify:** from `integrations/datastores/pgxdb`,
  `go build ./... && go test ./... && go vet ./...`; then live
  `POSTGRES_TEST_DSN=... go test -race ./...` and repo-root `make guard`.

### U6 — Google OAuth compatibility verification

- **depends_on:** U1
- **files:** none expected; `integrations/oauth/google/go.mod` only if the
  verification proves a real compatibility change is required
- **deliverables:**
  - Confirm the existing provider still satisfies the current eight-method
    `oauth.Provider` contract and its OIDC/JWKS, PKCE, nonce, email-trust,
    refresh, and userinfo tests remain green under the sdk version prepared by
    this batch.
  - Do not bump its `sdk v0.1.0` floor merely because the workspace selects a
    newer sdk; MVS supplies the host's higher compatible version.
  - Record `integrations/oauth/google/v0.1.0` as the tag coordination-hub should
    pull unless source or go.mod truly changes. Only then prepare a patch tag.
- **verify:** from `integrations/oauth/google`,
  `go build ./... && go test ./... && go vet ./...`, once in the workspace and
  once in the release-resolution check in U7.

### U7 — release preparation and downstream acceptance manifest

- **depends_on:** U1–U6
- **files:** changed module go.mod files as genuinely required,
  `RELEASING.md`, and architecture/docs affected by the final contracts
- **deliverables:**
  - Pin `features/authentication` to the new sdk tag so the feature's documented
    browser flow cannot be consumed with an sdk lacking the matching CORS seam.
  - Update the existing keyed "next tag" entries—do not create contradictory
    parallel notes—for CORS configuration/global middleware,
    `RefreshCookiePath`, `/auth/csrf`, `/auth/me`, `origin_rejected`, and the
    pgxdb limiter/reference DDL/pruning obligation.
  - Prepare—do not create or push—the owner-cut tag manifest. Expected semver
    floors are `sdk/v0.3.0`, `features/authentication/v0.2.0`, and
    `integrations/datastores/pgxdb/v0.3.0`. Authentication store modules do not
    retag unless a repository contract changes (none is expected). Google stays
    at v0.1.0 unless U6 changed it.
  - Run the complete same-site cross-origin cookie flow using only exported
    host seams and record the exact coordination-hub wiring: configured CORS
    headers, `RefreshCookiePath: "/api/v1/auth"`, CSRF bootstrap route, `/me`,
    and pgxdb limiter constructor.
  - After the owner cuts tags, prove each with a cold scratch module using
    `GOWORK=off go get`; then coordination-hub may bump, tidy, and vendor.
- **verify:** repo-root `make check`; live pgxdb limiter gate; Google gate;
  post-tag cold scratch resolution.

### U8 — deferred invitation-provisioning design

- **depends_on:** none; gates nothing in this batch
- **status:** named follow-up, not dispatchable implementation yet
- **reason:** invitation accept currently requires a live account/session, so
  invitations grant relations to existing accounts but cannot provision new
  vendors. Pre-auth acceptance or registration-time claim needs its own threat
  model (token binding, enumeration resistance, replay, expiry, and identity
  ownership). Password registration covers v1. Create a separate plan at the
  campaign-onboarding milestone; never fold it into U7's auth tag.

## Sequencing

The dispatch-safe default is:

1. U1 (sdk CORS/router semantics).
2. U2 then U3 then U4 (authentication; sequential route-table ownership).
3. U5 (pgxdb limiter; independent and safe to dispatch anywhere before U7).
4. U6 (Google verification once U1 is stable).
5. U7 (one release-preparation and cross-module acceptance pass).

Critical path to coordination-hub login is U1 → U3, plus U2, U4, U5, and U7.
U6 is verification, not construction. U8 gates nothing.

## Risks and controls

1. **Middleware semantic expansion.** Code that relied on `HandleRaw` bypassing
   panic recovery/logging changes behavior. That bypass is now explicitly
   rejected, but U1 must test response-writer capabilities (streaming, SSE,
   flush, hijack) through the global stack and document the change.
2. **CSRF/CORS tag coupling.** Authentication can ship a correct bootstrap
   while an older sdk still rejects the echo header. U7 pins and verifies the
   pair.
3. **CSRF token rotation.** Repeated bootstrap calls would invalidate tokens held
   by another tab if every call blindly rotated the cookie. U3 reuses an
   existing non-empty CSRF cookie and mints only when absent; it must never
   return a body value that differs from the effective cookie value.
4. **Limiter atomicity.** A read-then-write implementation over-admits under
   concurrency. U5 requires one atomic database transition and an exact-K live
   test, not only the shared sequential conformance suite.
5. **Limiter retention/write load.** Every login attempt writes Postgres. The
   reference DDL includes expiry metadata and pruning guidance; coordination-hub
   owns scheduling and capacity monitoring.
6. **Release drift.** Tags are immutable and owner-cut. U7 happens only after
   all dispatched tasks settle and `make check` plus live gates pass.

## Suggested review at dispatch

Suggested only; this plan invokes no reviewers or subagents by itself.

- **architecture-steward:** U1's whole-mux middleware semantics and U5's
  recommended pgxdb placement/multi-port connector ruling.
- **lead-backend-engineer:** U3's CSRF issue-or-reuse contract, U4's live-session
  hydration view, and U5's atomic sliding-window SQL.
- **platform-sre:** U5's per-attempt write load, reference DDL, expiry index,
  and pruning ownership.

## Out of scope

- Cross-site cookie authentication or configurable `SameSite`.
- Teaching `PrefixRegistrar` to rewrite rendered URLs or expose its prefix.
- A Redis deployment for coordination-hub; goredis remains valid precedent and
  an existing alternative backend.
- SendGrid capability metadata. coordination-hub uses sdk SMTP; revisit only if
  the owner selects SendGrid.
- Tag creation/push and coordination-hub vendoring until U7 is explicitly
  dispatched and the owner cuts the prepared tags.

---

## Known-defect evidence register

The sections below preserve the source evidence behind U1–U3. Line references
were verified against the current gopernicus source corresponding to the
vendored versions, not transcribed from notes.

---

## 1. A same-site, cross-origin SPA cannot complete the cookie-authenticated mutation flow

**Severity: blocking.** This is the headline ask — items (a) and (b) together
are a hard prerequisite for building authentication into a SPA served from a
different origin than the API while retaining cookie authentication, which is
coordination-hub's production topology. Both origins are HTTPS siblings under
the same registrable domain, so they are cross-origin but same-site.

Bearer-only JSON clients are not affected: the token responses expose access and
refresh tokens, and `requireBrowserSafeMutation` deliberately bypasses its CSRF
gate when a bearer token is present without the session cookie. This ask is about
the cookie-authenticated browser lane, not every cross-origin API client. It also
does not claim to enable an arbitrary cross-site SPA: the session and CSRF cookies
remain `SameSite=Lax` by design.

### (a) The CSRF token is unreachable by a cross-origin client

`features/authentication/internal/inbound/authentication/security.go:157-172`

```go
func issueCSRFToken(w http.ResponseWriter) (string, error) {
	// ...
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    token,
		Path:     "/",
		Secure:   true,
		HttpOnly: false,
		SameSite: http.SameSiteLaxMode,
	})
	return token, nil
}
```

The double-submit token is delivered **only** as a host-only cookie on the API's
own origin, and the sole caller is `html.go:106` — inside the HTML views, which
mount only when `Config.Views != nil` (`html.go:17`). An API-only host leaves
`Views` nil, so nothing ever calls it.

Even if it were called, a page on `spa.example.com` cannot read a cookie set by
`api.example.com`. A host can duplicate the internal cookie name and token
contract in its own bootstrap endpoint, but that is a brittle workaround over
unexported feature behavior rather than a supported configuration seam.

Consequence: every `browserSafe` route is unusable from a same-site,
cross-origin JSON client that authenticates with the session cookie — password
change, password set, step-up, and all identifier management.

**Ask:** add a supported JSON bootstrap response that mints the cookie and
returns the matching token in its body. `issueCSRFToken` already returns the
value. The response must be `Cache-Control: no-store`, and the contract should
be covered by an end-to-end test of a credentialed, allowlisted cross-origin
request followed by a `browserSafe` mutation.

### (b) `X-CSRF-Token` cannot be sent even if the client had one

`sdk/foundation/web/middleware.go:132`

```go
w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Authorization")
```

Hardcoded, with no `X-CSRF-Token`, and there is no supported configuration seam
to widen it. A simple header wrapper does not work: the middleware `Set`s the
value and commits the preflight response itself. A host can instead replace or
intercept the whole preflight path, but that duplicates the framework CORS
policy and is the workaround coordination-hub already needs for item 5.

**Ask:** make the allowed-header list configurable. Prefer that over teaching
the generic sdk foundation package about an authentication-feature header; the
host can then include `X-CSRF-Token` explicitly. Preserve the current list as
the compatibility default.

---

## 2. `refreshCookiePath` is not prefix-aware

**Severity: high, and not specific to this repo.** Any host that mounts the
authentication feature under a path prefix is affected.

`features/authentication/internal/logic/authsvc/service.go:66-68`, used at `:1049`

```go
// refreshCookiePath scopes the refresh cookie to /auth (D4): it covers
// /auth/refresh AND /auth/logout, never riding on unrelated requests.
refreshCookiePath = "/auth"
```

coordination-hub mounts features under `/api/v1` via `feature.PrefixRegistrar`,
so the refresh cookie is scoped to `/auth` while the endpoint that needs it
lives at `/api/v1/auth/refresh`. The browser never sends it. **Cookie-driven
refresh is dead**, and it fails silently — logout survives only by accident,
falling back to the access cookie whose `Path` is `/`.

This will surface the moment authentication lands in any prefixed host's ui.

**Ask:** add a validated `RefreshCookiePath` authentication configuration value,
defaulting to `/auth` for compatibility, and use it identically when setting and
deleting the refresh cookie. Set it to `/api/v1/auth` in a prefixed host. Deriving
the path from `feature.PrefixRegistrar` is not currently viable because the
registrar deliberately exposes registration, not mount-prefix introspection.

---

## 3. `CORSMiddleware` gaps

All in `sdk/foundation/web/middleware.go:102-140`. Individually minor; together
they make a cross-origin deployment harder to operate than it should be.

### (c) No `Access-Control-Expose-Headers`

`web.RequestID()` deliberately echoes `X-Request-ID`
(`middleware.go:16,77`), but without an expose-headers directive that header is
invisible to cross-origin JavaScript. Any "quote the request id when you contact
support" flow is foreclosed on a split-origin deployment.

### (d) No `Vary: Origin`

The middleware writes no `Vary`, even though the presence and value of its CORS
response headers depend on the request's `Origin`. A shared cache may therefore
reuse a response selected for one origin for another, commonly turning an
otherwise allowed request into a browser-visible CORS failure. This includes
corporate forward proxies as well as application-facing caches.

coordination-hub works around this with a ~6-line `varyOrigin` wrapper in
`cmd/server/cors.go` that `Add`s the header before delegating. The wrapper stays
correct after an upstream fix, so adopting one is not a breaking change here.

**Ask:** set `Vary: Origin` unconditionally, and add a configurable
expose-headers list defaulting to the framework's own `X-Request-ID`.

### (e) Every `OPTIONS` request is treated as a CORS preflight

`CORSMiddleware` returns 204 for any request whose method is `OPTIONS`, even
when it has neither `Origin` nor `Access-Control-Request-Method`. If the
middleware is made truly global as requested in item 5, it will make legitimate
non-CORS `OPTIONS` handlers unreachable.

**Ask:** short-circuit only an actual preflight — at minimum, an `OPTIONS`
request carrying both `Origin` and `Access-Control-Request-Method`. Let other
`OPTIONS` requests continue to the handler or mux.

---

## 4. An origin rejection has no distinct machine-readable error code

**Severity: diagnosability.**

`features/authentication/internal/inbound/authentication/security.go:212-214`
→ `sdk/foundation/web/errors.go:135-137`

```go
func forbidCSRF(w http.ResponseWriter, msg string) {
	web.RespondJSONError(w, web.ErrForbidden(msg))
}

func ErrForbidden(msg string) *Error {
	return &Error{Status: http.StatusForbidden, Message: msg, Code: "permission_denied"}
}
```

An origin-gate rejection and an authorization denial produce the same status
and machine-readable `code`. Their human-readable messages differ, but clients
should not have to branch on message copy. When CORS itself rejects the caller,
JavaScript cannot read any response (`fetch` rejects with no status, headers, or
body); when CORS allows the origin but the authentication allowlist does not, a
distinct response code would make that configuration mismatch diagnosable.

coordination-hub compensates by failing at **boot** when
`CORS_ALLOWED_ORIGINS` is not a subset of `AUTH_ALLOWED_ORIGINS`, because the
runtime symptom is so hard to read.

**Ask:** a distinct error code for the origin-gate rejection.

---

## 5. Per-pattern "global" middleware does not see mux-generated responses

**Severity: framework wart. Worked around, but it silently breaks the obvious
wiring.**

`sdk/foundation/web/handler.go:48-68`

```go
func (h *WebHandler) Use(middleware ...Middleware) {
	h.globalMiddleware = append(h.globalMiddleware, middleware...)
}

func (h *WebHandler) Handle(method, path string, handler http.HandlerFunc, middleware ...Middleware) {
	allMiddleware := append(append([]Middleware{}, h.globalMiddleware...), middleware...)
	// ... wraps handler, then:
	pattern := path
	if method != "" {
		pattern = fmt.Sprintf("%s %s", strings.ToUpper(method), path)
	}
	h.mux.Handle(pattern, final)
}
```

`Use` bakes global middleware into **each registered pattern**, and the feature
patterns are method-qualified. An `OPTIONS` preflight with no explicit matching
pattern is therefore answered by `http.ServeMux` itself with a 405 — **no global
middleware runs**. The same boundary excludes mux-generated 404 responses and
other method-mismatch 405 responses. A methodless route or an explicit
`OPTIONS` registration can match, so the defect is not that middleware can
never observe that method; it is that unmatched dispatch never enters the
supposedly global stack.

So the natural wiring, `router.Use(web.CORSMiddleware(origins))`, compiles, looks
right, and silently fails preflights to the method-qualified feature routes.
Verified empirically: `POST /api/v1/auth/login` reaches the handler; `OPTIONS`
on the same path returns 405 with only `Allow`, `Content-Type`, and
`X-Content-Type-Options`.

Because the SPA sends `Content-Type: application/json` on every mutation, the
symptom is that simple GETs work and every POST/PATCH/DELETE fails — a
misleading signature that points away from the cause.

coordination-hub instead wraps the whole handler passed to `web.Run`
(`cmd/server/main.go`), with a comment explaining why, since the next reader
will otherwise "clean it up" back to `Use`.

**Ask:** make `Use` wrap the entire mux dispatch so the stack observes normal
routes, `HandleRaw` routes, redirects, and mux-generated 404/405 responses,
while preserving middleware order and the automatic 405 status/`Allow` header.
Change `HandleRaw`'s contract: it registers an arbitrary `http.Handler` and raw
ServeMux pattern, but it does not bypass global panic recovery, request IDs,
logging, CORS, or other host policy. Do not implement this as an ordinary
unqualified catch-all: that can turn a method-mismatch 405 into a 404 and lose
the `Allow` header.

---

## Not upstream

Recorded here to keep the boundary clear — these are ours, not the framework's:

- `SameSite=Lax` is hardcoded on session cookies
  (`authsvc/service.go:1019,1037,1053`). This is a deliberate CSRF posture, and
  coordination-hub's topology (both hosts under one registrable domain) keeps it
  working. Not a defect; do not ask for it to be configurable without a concrete
  case.
- `feature.PrefixRegistrar` changing where handlers register but not URLs a
  feature _renders_ is documented behavior. It is the cause of item 2 and of the
  `AUTH_OAUTH_CALLBACK_BASE` quirk (the OAuth callback URL is built by
  concatenation and must carry the prefix), but the seam itself is working as
  designed.
