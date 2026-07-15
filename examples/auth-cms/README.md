# examples/auth-cms — the multi-feature proof host (auth-v2 A9 + auth-v3)

This host mounts **real feature modules** — `features/cms`,
`features/authentication`, `features/authorization`, and `features/events` — onto
one host router, with in-memory stores and no datastore driver, and wires auth's
identity middleware into cms's admin surface. It is both the auth-v2 milestone's
**A9 proof host** (OAuth, machine identity, JWT bearer, security-event audit, and
ReBAC-decoupled invitations) and the **auth-v3 identity proof host**: it wires the
full v3 surface with zero infra — the `user_identifiers` model, the atomic
challenge rail, the two-mode delivery runtime (generic **jobs** wiring /
bounded **in_process** — both backed by in-memory stores here, so both are
non-durable on this proof host), passwordless email/phone login, the
step-up-gated credential/identifier management suite, the bundled default HTML/
templ pages (`authtempl`) **with a real host page override** (`internal/authpages`),
and a `RuntimeMode=development` posture whose production-negative twin is proven
hermetically.

> The v3 surface has BOTH a JSON API and (because `Config.Views` is wired) normal
> HTML pages/forms. The legs below drive the JSON API with curl; the HTML/browser
> journeys are described alongside. **curl `-d` sends
> `Content-Type: application/x-www-form-urlencoded`, which the content-type
> dispatcher routes to the FORM arm** — a JSON leg must pass
> `-H 'Content-Type: application/json'` (an absent header still decodes as JSON, the
> lenient path). Where a leg shows a JSON body, assume the JSON header is set.

## What it proves

- **Constitution rule 6 (features never import other features), with THREE real
  features.** `features/cms`, `features/authentication`, and
  `features/authorization` never import one another. Only this host's
  `cmd/server/main.go` imports all three. The cross-feature connections are made
  entirely in the composition root (`auth.Service.RequireUser` →
  `cms.Config.AdminMiddleware`; the engine `relationshipGranter` →
  `auth.Config.Granter`; `authorizer.Check` → `events.Config.Authorize`) — over
  sdk-shaped seams, with zero import edges between the features.

- **The feature-module opt-out holds for a second feature — no libsql in the
  module graph:**

  ```sh
  cd examples/auth-cms && GOWORK=off go list -m all | grep -i libsql   # empty
  ```

  (The repo-root `go.work` unions every workspace module, so a workspace-active
  `go list -m all` reports the store adapters' libsql; the module's own graph —
  `GOWORK=off`, i.e. what actually builds this host — has none, exactly like
  `examples/minimal`.) `bcrypt` is a CPU-bound library with no external service,
  and the JWT signer is the sdk stdlib HS256 default (no integration), so the host
  stays zero-infra.

- **The whole auth-v2 surface, live:** the verified-email login gate, a
  host-local fake OAuth provider, API-key machine calls, JWT access tokens +
  rotating store-backed refresh tokens (host-signed by the sdk stdlib HS256
  default, `sdk/foundation/cryptids`), security-event audit rows,
  and invitations that grant through the **`features/authorization` engine's
  `relationshipGranter`** — invitation-accept writes a real ReBAC tuple via the
  trusted `SystemMutator.GrantRelationship` (the flagship posture; the memstore-backed
  engine keeps the host **driver-free** — no libsql in the graph). The A9 milestone
  shipped this seam with a toy in-memory `Granter` instead (ratified AV4:
  invitations work with no ReBAC in the graph); `authorization-v1` Z4 commit 2
  swapped the engine in through the identical `auth.Config.Granter` seam.

## Authorization postures — the flagship, demonstrated (both kinds)

Authorization is "supported, never required": a host runs with no checks, with a
**host-authored Check closure** (the middle posture), or with the mounted
`features/authorization` IAM domain (the flagship). This host now demonstrates the
**flagship** — and the middle posture stays a permanent, recorded artifact in git
history:

- **Middle posture (commit 1, `2e1e5eb`):** `events.Config.Authorize` was
  satisfied by a plain ownership closure over a toy membership map, with **no
  `features/authorization` in the module graph** (`GOWORK=off go list -m all |
  grep -c authorization` → `0`) — a Check seam met entirely by host code, no IAM
  module required. Retained as a git artifact, not the current wiring.
- **Flagship posture (current):** the host mounts `features/authorization`, **both
  kinds** wired and **memstore-backed** (so the graph stays driver-free —
  `GOWORK=off go list -m all | grep -i libsql` is still empty). The SAME
  `events.Config.Authorize` seam now delegates to `authorizer.Check`, and the
  invitation `Granter` is the engine's `relationshipGranter`.

**The relationship kind (GUARDED, AZ3-4.1).** `main` declares a schema
(`authorization.NewSchema`) with a `project` resource type (`owner`/`member`
relations, `view` = `AnyOf(owner, member)`, and `manage_access` = `Direct(owner)` —
the permission the host `MutationGuard` enforces) and a flat `platform` admin-list
type (`admin` relation + `admin` permission). The composition is guarded: actor-facing
writes pass a host `MutationGuard` (`guard.go`) that reads `manage_access` (its backing
`owner` relation) plus the platform-admin short-circuit **only through the mutation
repository's dependency-tracking `DecisionView`**, and the ownable `project` type
carries the ratified guardian minimum (`owner`, min-1). At boot the host seeds
`project:demo#owner@user:demo-owner` (establishing the guardian minimum FIRST) and the
**platform-admin data tuple** `platform:main#admin@user:demo-owner` through the
**trusted `SystemMutator`** (`seedAuthorization`) — platform-admin is DATA (a tuple over
a `platform` resource type), never a Config field, and establishing the first owner is
inherently trusted. **`Check` is pure schema evaluation**: the engine grants no bypass,
so the host runs the platform-admin recipe itself — an `admin` permission `Check` on
`platform/main`, first, in its own closure (`isPlatformAdmin` in `membership.go`). A
member gets `view` on `project/demo` the moment the invitation is accepted (the Granter
writes the tuple through the `SystemMutator`). Demo routes are READ-ONLY (AZ3-4.1
removed the session-only mutation routes — see below); the guarded actor path and
`SystemMutator` composition are proven by `authorization_test.go`, not a browser flow.

- `GET /demo/members-only` — gated through the host closure: platform admin (via
  `isPlatformAdmin`) OR `authorizer.Check` (`view` on `project/demo`) → 200,
  otherwise 403.
- `GET /demo/my-projects` — the relationship kind's **enumeration** via
  `authorizer.LookupResources(..., "view", "project")` (pure, no bypass), returned
  as `{"admin", "ids"}` where `admin` is the host-composed platform-admin flag: a
  member → `{"admin":false,"ids":["demo"]}`, a stranger → `{"admin":false,"ids":[]}`,
  the platform admin → `{"admin":true,"ids":[]}` (a real app skips ID filtering when
  `admin`). This is a **demo-only host surface** exercising a **flagship-specific
  API** — enumeration is NEVER a consumer seam (§2.4); consumer seams are Check-only.

**The roles kind** is **independently wireable** — a roles-only host would wire
`authorization.Repositories{Roles: …}` alone and never construct a model. Here it
rides alongside the relationship kind (two kinds, two checks, no entanglement).
Roles are **opaque strings** the host interprets. Demo routes:

- `GET /demo/audit` — gated through `authorizer.HasRole(..., "auditor",
  "project", "demo")`: 403 without the role, 200 with it. On success it drives a
  `ListRoleAssignmentsByResource` read-back. The engine's `HasRole` honors the
  **global fallback** (a GLOBAL `auditor` grant satisfies the scoped check), but
  the listing is **direct-scope only** — so a subject allowed via a global grant is
  allowed yet does NOT appear in the read-back (the documented v1
  enumeration-vs-decision divergence, visible in the JSON).
Role assignment has **no shipped HTTP surface** on this host: AZ3-4.1 removed the
session-only `POST /demo/roles/{assign,unassign}` and `POST /demo/admin/bootstrap`
routes (a shipped route must never mutate authorization with session presence alone,
and authentication does not yet export a public sensitive-operation protector). Boot
seeding runs through the trusted `SystemMutator`; the browser-driven role-assignment
surface is deferred with the AZADM packet.

## Wiring

- **cms store**: `internal/memstore` (in-memory cms ports).
- **auth store**: `internal/authmem` — an in-memory implementation of **every**
  auth port: v1 user/password/session, the v2 oauth-account/oauth-state/
  service-account/api-key/security-event/invitation ports, and the v3 identity +
  atomic-security rails (identifier, challenge, password-reset, contact-change,
  authentication-grant, credential-mutation — see `ports_v3.go`; delivery owns no
  auth port — durable delivery runs on the generic jobs feature). It honors the
  contracts the shared `features/authentication/storetest`
  suite proves (uniqueness, sentinels, expired-at-read, the pinned GetByHash and
  partial-pending-uniqueness contracts, atomic single-use consume + revision-CAS,
  and the created_at DESC, id DESC paging), and projects the masked credential
  inventory from the real identifier rows exactly as pgx/turso do.
- **hasher**: `bcrypt.New()`. **mailer**: `email.NewConsole(log)` (logs mail —
  this is how you read verification codes and invitation tokens below).
- **OAuth provider**: `fakeOAuthProvider` (`cmd/server/oauthfake.go`) — a
  self-contained `sdk/capabilities/oauth.Provider`, no vendor, no network; identity derived
  from the authorization `code`.
- **TokenSigner** (REQUIRED — the core no longer tolerates a nil signer):
  `cryptids.NewHS256` (the sdk stdlib HS256 default) over `AUTH_JWT_SECRET`;
  absent → an **ephemeral** per-boot key. The ephemeral key is a **DEV /
  single-instance convenience only**: access JWTs don't survive a restart, and a
  **multi-instance** deployment MUST share `AUTH_JWT_SECRET` across every instance
  (per-instance keys can't cross-verify). API clients recover a dead access JWT via
  `POST /auth/refresh` (refresh tokens are store-backed).
- **Granter**: `relationshipGranter` (`cmd/server/membership.go`) — a host-local
  adapter whose `Grant` calls the trusted `SystemMutator.GrantRelationship` with a
  stable derived MutationID, so invitation-accept writes a real ReBAC tuple, trusted
  and idempotent (the flagship posture; the A9 toy map is retired).
- **authorization**: `features/authorization` (`cmd/server/main.go`) — BOTH kinds
  (relationships + roles), memstore-backed (no driver in the graph). Backs
  `auth.Config.Granter`, `events.Config.Authorize` (`authorizer.Check`), and the
  host demo routes.
- **`RequireVerifiedEmail: true`** — login and `/auth/token` refuse an unverified
  user with 403.
- **event bus + cache invalidation**: the host builds one shared
  `sdkevents.NewMemory` bus and passes it as `Mount.Events`, so cms emits its
  `content.*` events post-write (best-effort). The public-page cache
  (`cacher.NewMemory`, held in a variable and handed to `cms.Config.Cache`) is
  `web.CachePages` keyed as `page:<uri>`. The host subscribes on `"*"`, filters
  `content.*` in the handler, and calls `cache.DeletePattern(ctx, "page:*")` to
  drop every cached page so the next request re-renders fresh content. Delivery
  is async (ratified: an emitter's latency must not depend on its subscribers),
  so invalidation runs shortly *after* the admin write returns, not
  synchronously — a re-fetch trigger, not a transactional write. Before this
  wiring an edited page stayed stale until its 60s TTL expired. On shutdown the
  bus is closed on a fresh bounded context (HTTP drains first inside `web.Run`,
  which returns only after the parent ctx is already canceled).

### auth-v3 wiring (identity, challenge rail, delivery, HTML)

- **RuntimeMode**: `development` (explicit — production has no default). A memory
  host cannot boot production by design; the production-negative matrix is
  hermetic (`cmd/server/production_test.go`).
- **Five distinct dev secrets** (`.env.example`): `AUTH_JWT_SECRET` (JWT/session),
  `AUTH_CHALLENGE_PEPPER` (OTP HMAC key ring), `AUTH_IDENTIFIER_KEY` (PII-free
  rate-limit/idempotency digests), `AUTH_DELIVERY_ENCRYPTER_KEY` (outbox
  envelope), `AUTH_TOKEN_ENCRYPTER_KEY` (provider tokens). Each unset key falls
  back to an **ephemeral per-boot** key (dev/single-instance only, WARN-logged; key
  material is never printed).
- **Challenge rail**: `ChallengeProtector` (`buildChallengeProtector`, HMAC key
  ring with active key ID `dev`) + the `Challenges`/`PasswordResets`/`Challenges`
  ports from `authmem` back verify/reset/step-up/passwordless.
- **Delivery** (two proof-host variants, `DELIVERY_MODE`): the queue is the ONLY
  send path in either — the console mailer/notifier log the delivered secret, so
  drive codes/tokens/links from the server log.
  - **`DELIVERY_MODE=jobs` (default — jobs-mode wiring over an IN-MEMORY fenced
    queue):** `DeliveryMode: jobs` + `DeliveryEncrypter` (AES-GCM) +
    `DeliveryJobsAcknowledged: true`; the host wires `Config.DeliveryDispatcher` over
    the generic **jobs** feature and runs the jobs `FencedRuntime` (bound to
    `authSvc.DeliveryJobRuntime()`) in a **supervised** goroutine (see the shutdown
    section: an unexpected runtime exit brings the host down rather than serving with a
    dead delivery runtime). This demonstrates the exemplary jobs-mode composition
    (`internal/authjobs` is the ONE adapter importing both features), but **this proof
    host backs the fenced queue with `jobsmem.NewFencedQueue` — an in-memory store**. So
    on THIS host jobs mode is **NOT durable**: accepted work is **lost on restart** and
    there is **no cross-instance coordination** (the same development posture as
    `in_process`, just via the jobs wiring). Durability is a store swap a real host makes
    — a durable `FencedQueue` adapter (`features/jobs/stores/{pgx,turso}` + its
    `0003_fenced_job_queue` migration) — which this in-memory demo deliberately does not
    wire. In jobs mode the host also runs a **scheduled terminal-purge loop** (a
    host-owned goroutine on its own context, stopped in shutdown order) that removes at
    most a bounded batch of terminal delivery rows older than a retention window each
    tick, so durable rows and their encrypted metadata do not grow without bound:
    `DELIVERY_PURGE_INTERVAL` (default `1h`), `DELIVERY_PURGE_RETENTION` (default `24h`),
    `DELIVERY_PURGE_BATCH` (default `500`). It is a no-op posture on this in-memory demo
    (nothing survives restart) but exercises the exact host-owned purge wiring a durable
    host uses; the purged count surfaces on `/healthz/delivery`.
  - **`DELIVERY_MODE=in_process` (small/development — EPHEMERAL):** the same delivery
    processor runs behind a bounded queue + fixed worker pool the host drives via
    `authSvc.RunDelivery`. No dispatcher, no jobs runtime;
    `DeliveryEphemeralAcknowledged: true`. Its posture is **never hidden**: startup
    logs a LOUD WARN that accepted in-flight work is **LOST on crash or restart**,
    there is **no cross-instance coordination**, and running multiple instances
    de-duplicates on **neither** (a user may receive duplicate messages). Any value
    other than `in_process` falls back to `jobs` — the host never *silently* selects
    an ephemeral mode.
- **Delivery operational health** (`GET /healthz/delivery`, AV3D-5.3): a host-COMPOSED,
  secret-free, bounded status surface (`internal/deliveryhealth`) — counters/gauges/
  enums only, **never** a recipient, payload, secret, or logical key. It distinguishes
  runtime **not-started vs running** (host-owned lifecycle), **request accounting /
  backlog** (jobs mode: `admitted` counts **accepted** admission requests over the
  wrapped dispatcher — a rejected admission is not counted — and `outstanding` =
  admitted − delivered − skipped − dead_lettered − superseded, a **derived**
  request-accounting figure clamped at zero, **not** an authoritative queue depth; the
  fenced queue exposes no cheap authoritative nonterminal count. in_process mode: live
  `queued`/`capacity`/`saturated` from the auth Service's `InProcessQueueDepth` read is
  the **authoritative** backlog), **provider retry + dead-letter activity** (counted from
  the wrapped secret-free `authentication.delivery.*` lifecycle events), and
  **observer failure** (`observer_failures` increments when the events emitter errors).
  It is host-composed over existing/narrow-additive seams — NOT a new feature route.
- **Passwordless**: `Passwordless: [email, phone]` (`AUTH_PASSWORDLESS`) — email
  via the console Mailer, phone via the console Notifier;
  `POST /auth/passwordless/{start,verify,redeem}` and the bundled magic-link
  landing GET mount.
- **PublicAuthBaseURL**: defaults to `http://localhost:8082/auth/magic` so a
  zero-config magic link lands on the bundled fragment-reading page
  (`AUTH_PUBLIC_BASE_URL` overrides). Request Host/forwarded headers never
  participate.
- **AllowedOrigins**: defaults to this host's own origin (`AUTH_ALLOWED_ORIGINS`)
  so same-origin browser forms pass the browser-safe gate and cross-site
  credentialed POSTs are refused.
- **Views (HTML) + page override**: `Config.Views = authpages.New()` —
  `internal/authpages` **embeds the bundled `authtempl.Views`** and overrides ONLY
  `Login` with a Gopernicus-CMS-branded page rendered through stdlib
  `html/template` (no templ import in the host). Every other page is the promoted
  bundled default; the override changes presentation ONLY (same endpoints, CSRF/
  origin gate, PRG, status mapping, JSON contract).
- **EmailContentTemplates (distinct override system)**: `authpages.EmailOverride()`
  — a branded verification email BODY at `email.LayerApp`, the SECOND, DISTINCT
  override facility from `Views` (different field, different subsystem).
- **ContactChanges**: wired in `authmem.Repositories()`, so identifier add/change/
  remove flows work; `authmem`'s credential-mutation `Snapshot` projects the
  masked inventory from the real identifier rows (matching pgx/turso).

### Config / port nil-semantics (host view)

| collaborator | this host wires | nil/absent means |
|---|---|---|
| `Config.Providers` | the fake provider | OAuth routes not registered (deny-by-absence) |
| `Config.TokenSigner` | sdk HS256 over `AUTH_JWT_SECRET` (or ephemeral dev key) | REQUIRED — nil is `ErrTokenSignerRequired` at construction (no nil variant) |
| `Config.TokenEncrypter` | AES-GCM iff `AUTH_TOKEN_ENCRYPTER_KEY` | provider tokens not persisted (login/link still work) |
| `Config.Granter` | engine `relationshipGranter` (`SystemMutator.GrantRelationship`) | invitation routes not registered (deny-by-absence) |
| `Config.RuntimeMode` | `development` (explicit) | REQUIRED, no default — nil is `ErrRuntimeModeRequired` |
| `Config.DeliveryMode` | `jobs` (explicit) | REQUIRED, no default — empty is `ErrDeliveryModeRequired`, unknown is `ErrDeliveryModeInvalid` |
| `Config.ChallengeProtector` | HMAC key ring (`AUTH_CHALLENGE_PEPPER` or ephemeral) | REQUIRED once `Challenges` wired — `ErrChallengeProtectorRequired` |
| `Config.DeliveryEncrypter` | AES-GCM (`AUTH_DELIVERY_ENCRYPTER_KEY` or ephemeral) | REQUIRED once delivery can send (`jobs` dispatcher or `in_process`) — `ErrDeliveryEncrypterRequired` |
| `Config.IdentifierKeyer` | HMAC (`AUTH_IDENTIFIER_KEY` or ephemeral) | production-required; dev falls back to per-instance SHA-256 |
| `Config.Passwordless` | `[email, phone]` (`AUTH_PASSWORDLESS`) | empty → passwordless routes not registered |
| `Config.PublicAuthBaseURL` | `…/auth/magic` (`AUTH_PUBLIC_BASE_URL`) | REQUIRED once a link flow is enabled; production requires HTTPS |
| `Config.Views` | `authpages.New()` (branded-Login override of `authtempl.Views`) | nil → API-only (no HTML pages, no templ in the graph) |
| `Config.EmailContentTemplates` | `authpages.EmailOverride()` | empty → bundled LayerCore email bodies |
| `Repositories.SecurityEvents` | authmem | no audit trail (recording site is a no-op) |
| `AUTH_DEBUG` | off by default | `/debug/security-events` not registered (404) |

## Environment

See [`.env.example`](.env.example) for every knob (all secret-free placeholders):
the JWT/session knobs (`AUTH_JWT_SECRET`, `AUTH_ACCESS_TOKEN_TTL` default 15m,
`AUTH_REFRESH_TTL` default 7d, `AUTH_TOKEN_ENCRYPTER_KEY`), the four other distinct
v3 secrets (`AUTH_CHALLENGE_PEPPER`, `AUTH_IDENTIFIER_KEY`,
`AUTH_DELIVERY_ENCRYPTER_KEY`), the v3 HTML/passwordless/magic-link knobs
(`AUTH_PUBLIC_BASE_URL`, `AUTH_ALLOWED_ORIGINS`, `AUTH_PASSWORDLESS`),
`DELIVERY_MODE` (`jobs` default / `in_process` for the ephemeral bounded variant —
see the Delivery wiring bullet), and
`AUTH_DEBUG` + `OAUTH_CLIENT_ID/SECRET`. The host boots with **none** of them set:
every secret gets a required-but-ephemeral single-instance key at boot
(WARN-logged, never printed), passwordless defaults to `email,phone`, the magic
link lands on the bundled `/auth/magic` page, and the debug route is off.

## The A9 proof protocol (copy-pasteable curls)

Boot the server (defaults to `localhost:8082`). The examples below use a fixed
JWT secret and enable the debug route:

```sh
cd examples/auth-cms
export AUTH_JWT_SECRET='choose-a-secret-of-at-least-32-bytes-xxxxx'   # >=32 bytes; do NOT commit
AUTH_DEBUG=1 go run ./cmd/server
```

The console mailer logs every message to the server's STDERR. Read the
verification code from the `text="Your verification code is: …"` line and the
invitation token from the `text="… (token: …)"` line.

### Leg 0 — verified-email gate (five-step, gate ON)

```sh
# 1. no session -> 401
curl -i -c jar -b jar http://localhost:8082/articles
# 2. register -> 201
curl -sX POST http://localhost:8082/auth/register -H 'Content-Type: application/json' \
  -d '{"email":"admin@example.com","password":"correct horse battery staple","display_name":"Admin"}'
# 3. login BEFORE verify -> 403 (the gate)
curl -sX POST http://localhost:8082/auth/login -H 'Content-Type: application/json' \
  -d '{"email":"admin@example.com","password":"correct horse battery staple"}'
# 4. verify with the code from the mailer log -> 200
#    v3 break: the body now carries {email, code} (the challenge rail keys the
#    code by identifier); a {code}-only body is a 400.
curl -sX POST http://localhost:8082/auth/verify -H 'Content-Type: application/json' \
  -d '{"email":"admin@example.com","code":"<CODE_FROM_LOG>"}'
# 5. login -> 200 + TWO Set-Cookies; gated GET -> 200; logout -> 200; gated GET -> 401
curl -i -c jar -b jar -X POST http://localhost:8082/auth/login -H 'Content-Type: application/json' \
  -d '{"email":"admin@example.com","password":"correct horse battery staple"}'
#   Set-Cookie: session=<access JWT>; Path=/; HttpOnly; SameSite=Lax
#   Set-Cookie: session_refresh=<opaque>; Path=/auth; Max-Age=604800; HttpOnly; SameSite=Lax
curl -i -c jar -b jar http://localhost:8082/articles
curl -i -c jar -b jar -X POST http://localhost:8082/auth/logout   # clears BOTH cookies
curl -i -c jar -b jar http://localhost:8082/articles
```

### Leg 1 — OAuth (fake provider)

```sh
# start -> 302; note state + PKCE code_challenge in the Location header
curl -i "http://localhost:8082/auth/oauth/fake/start"
# drive the callback (code becomes the identity) -> 302 + session cookie
curl -i -c ojar -b ojar \
  "http://localhost:8082/auth/oauth/fake/callback?code=oauth-user%40fake.local&state=<STATE_FROM_LOCATION>"
# the minted session works, and the link is listed
curl -i -c ojar -b ojar http://localhost:8082/demo/whoami        # 200 (user principal)
curl -i -c ojar -b ojar http://localhost:8082/auth/oauth/linked  # 200 -> [{"provider":"fake",...}]
# re-run start+callback with the SAME code -> login path, still ONE link (no duplicate account)
```

### Leg 2 — API-key machine call

```sh
# with the admin session: create a service account, mint a key (plaintext ONCE)
curl -sX POST -c jar -b jar http://localhost:8082/auth/service-accounts \
  -H 'Content-Type: application/json' -d '{"name":"ci-bot"}'                 # -> {"id":"<SAID>",...}
curl -sX POST -c jar -b jar http://localhost:8082/auth/service-accounts/<SAID>/keys \
  -H 'Content-Type: application/json' -d '{"name":"k1"}'                     # -> {"id":"<KEYID>",...,"key":"<KEY>"}
# WITHOUT any cookie: the RequirePrincipal-gated demo route accepts the bearer key
curl -i -H "Authorization: Bearer <KEY>" http://localhost:8082/demo/whoami  # 200 (service_account principal)
# revoke, then the same call -> 401
curl -sX POST -c jar -b jar http://localhost:8082/auth/api-keys/<KEYID>/revoke
curl -i -H "Authorization: Bearer <KEY>" http://localhost:8082/demo/whoami  # 401
```

### Leg 3 — JWT bearer (the session-backed token pair)

`POST /auth/token` is the API-flow twin of login: it mints the **same
session-backed pair** login sets as cookies, returned in the JSON body
(**breaking change from AV6's stateless-only token** — the body is now
`{access_token, expires_at, refresh_token}`, no longer `{token, expires_at}`).

```sh
# issue a pair (access JWT + opaque refresh token)
curl -sX POST http://localhost:8082/auth/token -H 'Content-Type: application/json' \
  -d '{"email":"admin@example.com","password":"correct horse battery staple"}'
#  -> {"access_token":"<JWT>","expires_at":"2026-…","refresh_token":"<opaque>"}
curl -i -H "Authorization: Bearer <JWT>" http://localhost:8082/demo/whoami       # 200 (user principal)
```

Expired-access-JWT path — reboot with a 1-second access TTL, mint, wait, retry
→ **401** (then recover with the refresh token, below):

```sh
AUTH_JWT_SECRET=$AUTH_JWT_SECRET AUTH_ACCESS_TOKEN_TTL=1s go run ./cmd/server
# ... mint a pair, then after >1s:
curl -i -H "Authorization: Bearer <JWT>" http://localhost:8082/demo/whoami       # 401 (expired)
```

### Leg 3b — refresh rotation, grace, and reuse detection (auth-jwt plan §7)

The refresh token is presented via the `session_refresh` cookie (browser) or the
JSON body (API). Rotation is compare-and-swap; the single previous token gets one
**grace** use; a second use of a rotated-away token is **reuse** and burns the
session. `POST /auth/refresh` returns the same `{access_token, expires_at,
refresh_token}` shape (the `refresh_token` field is **omitted on the grace lane**).

```sh
# happy path: present the current refresh token -> a NEW pair; the old token is now
# the grace/previous slot. (Cookie clients send no body; the cookie carries it.)
curl -sX POST http://localhost:8082/auth/refresh -H 'Content-Type: application/json' \
  -d '{"refresh_token":"<R0>"}'
#  -> 200 {"access_token":"<JWT>","expires_at":"…","refresh_token":"<R1>"}

# grace lane: replay the OLD token <R0> ONCE -> a new access JWT only, NO
# refresh_token field (cookie clients keep the winning token, self-healing a race)
curl -sX POST http://localhost:8082/auth/refresh -H 'Content-Type: application/json' \
  -d '{"refresh_token":"<R0>"}'
#  -> 200 {"access_token":"<JWT>","expires_at":"…"}     # note: no refresh_token

# reuse detection: replay <R0> a SECOND time -> 401, and the session is REVOKED,
# so even the current token <R1> now 401s. A "refresh token reuse detected" WARN
# lands in the server log (session_id, user_id, rotation_count, ip, ua).
curl -i -X POST http://localhost:8082/auth/refresh -H 'Content-Type: application/json' \
  -d '{"refresh_token":"<R0>"}'                          # 401
curl -i -X POST http://localhost:8082/auth/refresh -H 'Content-Type: application/json' \
  -d '{"refresh_token":"<R1>"}'                          # 401 (session revoked)
```

### Leg 3c — logout window vs live-session gate, and ephemeral-restart recovery

Stateless `RequireUser`/`RequirePrincipal` routes honor an outstanding access JWT
for up to `AUTH_ACCESS_TOKEN_TTL` (default 15m) after logout; a
**`RequireLiveSession`** route denies immediately. AZ3-4.1 removed the host demo route
(`POST /demo/admin/bootstrap`) that illustrated this tier — a session-only
authorization-mutation route is exactly what the packet forbids — so the live-session
gate is now exercised only by authentication's own sensitive-operation routes. The
stateless-vs-live contrast still shows on the surviving stateless demo route:

```sh
# log in (cookie jar), then copy the access JWT out of the `session` cookie.
curl -i -H "Authorization: Bearer <ACCESS_JWT>" http://localhost:8082/demo/whoami   # 200 (stateless)
curl -sX POST -c jar -b jar http://localhost:8082/auth/logout                        # clears cookies, deletes session
# post-logout, within the 15m access window the stateless route still accepts the JWT:
curl -i -H "Authorization: Bearer <ACCESS_JWT>" http://localhost:8082/demo/whoami   # 200 (stateless honors the TTL)
```

Ephemeral-restart recovery (API lane) — with `AUTH_JWT_SECRET` **unset**, a restart
mints a fresh signing key, so old access JWTs stop verifying:

```sh
# capture a pre-restart access JWT + refresh token, then restart the server.
curl -i -H "Authorization: Bearer <OLD_ACCESS_JWT>" http://localhost:8082/demo/whoami   # 401 (new key)
curl -i -X POST http://localhost:8082/auth/refresh -H 'Content-Type: application/json' \
  -d '{"refresh_token":"<OLD_REFRESH>"}'
```

> **authmem caveat (honest):** refresh tokens are store-backed and *would* survive
> a restart against a durable store — but this host's `internal/authmem` is
> **in-memory**, so a restart wipes every session. The `/auth/refresh` call above
> therefore returns **401** here: the surviving-refresh recovery half of this leg
> is only observable against a persistent store (e.g. `stores/turso` / `stores/pgx`
> in a real host). The access-JWT-dies-on-restart half is real and shown above.

### Leg 4 — invitations (the authorization engine `Granter`)

The membership-gated route is `GET /demo/members-only` — it checks `view` on the
`project/demo` resource through `authorizer.Check` (the flagship posture). An
accepted invitation grants the `member` tuple via the engine `relationshipGranter`
(the trusted `SystemMutator.GrantRelationship`), and `view = AnyOf(owner, member)`, so
the member passes the gate. The observable codes are identical to the A9 toy-Granter
run — the swap is invisible at the seam.

```sh
# register + verify user B (b@example.com) and user C (c@example.com), each with its own cookie jar.
# B is not a member yet:
curl -i -c bjar -b bjar http://localhost:8082/demo/members-only                  # 403
# A (admin session) invites B on project/demo -> 201 pending; token is in the mailer log
curl -sX POST -c jar -b jar http://localhost:8082/auth/invitations/project/demo \
  -H 'Content-Type: application/json' -d '{"identifier":"b@example.com","relation":"member"}'
# B accepts with the token -> 200, and the engine Granter writes B's member tuple
curl -sX POST -c bjar -b bjar http://localhost:8082/auth/invitations/accept \
  -H 'Content-Type: application/json' -d '{"token":"<INVITE_TOKEN_FROM_LOG>"}'
curl -i -c bjar -b bjar http://localhost:8082/demo/members-only                  # 200 (granted)
curl -i -c cjar -b cjar http://localhost:8082/demo/members-only                  # 403 (C is not a member)
# decline path on a second invitation -> declined, no grant
curl -sX POST -c jar -b jar http://localhost:8082/auth/invitations/project/demo \
  -H 'Content-Type: application/json' -d '{"identifier":"d@example.com","relation":"member"}'  # -> {"id":"<DID>",...}
curl -sX POST http://localhost:8082/auth/invitations/<DID>/decline \
  -H 'Content-Type: application/json' -d '{"token":"<D_INVITE_TOKEN_FROM_LOG>"}'  # 200 -> status "declined"
```

### Leg 5 — audit rows visible (DEFAULT-OFF, session-gated)

```sh
# with AUTH_DEBUG=1 and the admin session -> 200, the rows the legs produced
curl -s -c jar -b jar http://localhost:8082/debug/security-events                # 200 (register/login/verify/
                                                                                 #  oauth_*/apikey_auth/token_issued/invitation_*)
curl -i http://localhost:8082/debug/security-events                              # 401 (no session)
# with AUTH_DEBUG unset, the route is not registered:
curl -i -c jar -b jar http://localhost:8082/debug/security-events                # 404
```

### Leg 6 — the events SSE stream (`features/events`, best-effort)

The host mounts the events feature's SSE gateway on the same root router (no
prefix), so the subject stream lands at **`GET /events`**. It is wrapped by
`authSvc.RequireUser` (`Config.StreamMiddleware`): the handler reads the stashed
`identity.Principal` and **fails closed with 401 when no session/bearer is
present**. `Config.Authorize` is wired through the **authorization engine**
(`authorizer.Check`, the flagship posture — see "Authorization postures" above),
so the resource-scoped `GET /events/{resource_type}/{resource_id}` route **is
registered**: a member of the resource is allowed, a resolved non-member gets 403,
an unauthenticated caller 401. `Repositories.Outbox` is nil — direct-emit mode: the
gateway fans
best-effort `content.*` frames out over SSE the moment cms emits them (async, O3),
with no durable rail and no poller. Bodies are metadata-only (`{type, occurred_at,
aggregate_type, aggregate_id, tenant_id}`); the SSE `id:` is the event's
correlation id. Heartbeat comment frames (`: ping`) arrive on a ~25s cadence.

```sh
# unauthenticated -> 401 (RequireUser fails closed)
curl -N http://localhost:8082/events                                             # 401

# as the logged-in admin (cookie jar from Leg 0): open the stream and leave it running
curl -N -b jar http://localhost:8082/events
# in another shell, edit a seeded article via the admin form (RequireUser-gated):
#   ID=$(curl -s -b jar http://localhost:8082/articles | grep -o '/articles/[a-z0-9]*/edit' | head -1 | sed 's#/articles/##;s#/edit##')
#   curl -sX POST -b jar "http://localhost:8082/articles/$ID" \
#     --data-urlencode 'title=Bring your own stores (edited)' --data-urlencode 'status=published'
# -> a content.updated frame arrives on the open stream:
#      event: content.updated
#      id: <correlation-id>
#      data: {"type":"content.updated","occurred_at":"…","aggregate_type":"entry","aggregate_id":"<id>"}
# reload the public page -> fresh content (phase-3 cache invalidation).
```

### Leg 6b — the durable outbox variant (`EVENTS_OUTBOX=memory`)

Reboot with `EVENTS_OUTBOX=memory` to swap the emit path in front of the bus from
direct-emit to the **durable at-least-once rail**. The host wires an example-local
in-memory outbox (`internal/outboxmem`, an honest `outbox.EntryRepository`) into
`Repositories.Outbox` and drives an `events.Poller` on an `sdk/foundation/workers` pool. A
host-owned `POST /outbox-demo` route appends a record, then signals a cap-1 wake
channel (the canonical append-then-signal pattern) so the poller drains it
sub-second rather than waiting out the idle interval: **outbox → poll → emit →
SSE**. The frame's SSE `id:` is the durable **outbox EventID** the poller surfaces
(not the CorrelationID the direct-emit rail uses) — the de-dupe key consumers key
on. The gateway is unchanged; only the path feeding the bus differs. cms itself
never touches the outbox (O2).

```sh
EVENTS_OUTBOX=memory go run ./cmd/server
# as the logged-in admin: open the stream and leave it running
curl -N -b jar http://localhost:8082/events
# in another shell, trigger a durable append:
curl -sX POST -b jar http://localhost:8082/outbox-demo        # -> 202 {"event_id":"<outbox EventID>"}
# -> a demo.outbox frame arrives promptly via outbox -> poller -> bus:
#      event: demo.outbox
#      id: <outbox EventID>            # the durable EventID, NOT a correlation id
#      data: {"type":"demo.outbox","occurred_at":"…","aggregate_type":"demo","aggregate_id":"outbox-demo"}
```

Shutdown order (SIGTERM): **HTTP server → delivery runtime → terminal-purge scheduler
→ poller pool → `bus.Close`** — the delivery runtime, purge scheduler, and poller pool
each run on their own Background-derived context so they stop only after HTTP has
drained, and the bus closes last on a fresh bounded context. HTTP and delivery are
**supervised as one lifecycle** (IX-02): the server blocks on a cancelable child of the
signal context, and if the delivery runtime exits unexpectedly (error OR a clean return)
while the host is running, the supervisor cancels that context — so the server drains
and the process exits nonzero rather than continuing to admit work against a dead
delivery runtime. A normal signal-driven shutdown (the delivery context is canceled by
the ordered stop above) takes the quiet path and is never treated as a failure.

### Leg 7 — the authorization flagship (guarded, AZ3-4.1)

The `project:demo` owner and the `platform:main#admin` data tuple are **boot-seeded
for `user:demo-owner`** through the trusted `SystemMutator` (`seedAuthorization`) — the
host no longer self-bootstraps the caller through a session-only route. The READ demos
below still drive live off invitation-granted membership; `<BID>`/`<CID>` are B/C's
principal ids from `GET /demo/whoami`.

```sh
# relationship kind — B is a member (Leg 4 accept); enumeration (demonstration b):
curl -s -b bjar http://localhost:8082/demo/my-projects                            # {"admin":false,"ids":["demo"]}
curl -s -b cjar http://localhost:8082/demo/my-projects                            # {"admin":false,"ids":[]}
# resource-scoped stream gated through authorizer.Check:
curl -N --max-time 2 -b bjar http://localhost:8082/events/project/demo            # 200 (member)
curl -N --max-time 2 -b cjar http://localhost:8082/events/project/demo            # 403 (non-member)

# roles kind — /demo/audit gates on HasRole (global fallback + direct-scope read-back):
curl -i -b bjar http://localhost:8082/demo/audit                                  # 403 (no role)
```

**Role assignment has no HTTP surface** (AZ3-4.1): the session-only `POST
/demo/roles/{assign,unassign}` and self-bootstrap routes were removed because a shipped
route must not mutate authorization on session presence alone, and authentication does
not yet export a public sensitive-operation protector. The guarded actor-mutation path
(manage_access + platform-admin over the `DecisionView`), the trusted `SystemMutator`
seeding/invitation flow, the last-owner guardian minimum, the roles kind's global
fallback, and the enumeration-vs-decision divergence are all proven in
`cmd/server/authorization_test.go`, not a browser flow. The browser-driven
role-assignment surface returns with the deferred AZADM packet.

### Leg 8 — auth-v3: normal HTML pages (twice-through)

Because `Config.Views` is wired, every core auth journey has a normal HTML page in
addition to the JSON API. The dispatcher keeps ONE route per endpoint; a form POST
303-redirects (PRG), a JSON POST keeps its JSON status/body. Drive them in a
browser at `http://localhost:8082/auth/...`:

```sh
# the public pages render (200) under the full HTML security header set
curl -i http://localhost:8082/auth/login        # 200: branded (authpages override) Login
curl -i http://localhost:8082/auth/register      # 200: bundled default (promoted) Register
# every HTML response carries: Cache-Control: no-store, Referrer-Policy: no-referrer,
# X-Frame-Options: DENY, X-Content-Type-Options: nosniff, and a nonced CSP.
# a form login (curl -d = urlencoded) 303-redirects on success; JSON login stays JSON:
curl -i -c jar -b jar -d 'email=admin@example.com&password=correct horse battery staple&csrf_token=<TOK>&return_to=/' \
  http://localhost:8082/auth/login              # 303 -> / on success (form arm, PRG)
```

The register→verify→login and reset chains PRG through `/auth/verify?email=…` →
`/auth/login?email=…` → `/`. `GET /auth/login` renders the host's **branded**
`authpages.Views.Login` (`data-brand="gopernicus-cms"`) — the page override — while
`GET /auth/register` renders the promoted bundled default: proof the override
changes presentation only. A failed form login re-renders the same branded page at
401 with NO session cookie (never a secret repopulated); a cross-site `Origin` form
login is 403; a cookie form mutation missing `csrf_token` is 403.

### Leg 9 — auth-v3: passwordless + magic link

Passwordless is enabled for `email` and `phone`. Start is enumeration-safe (known
and unknown return the same generic 202; the start never resolves the account or
calls a provider — the worker does, off-path):

```sh
# email OTP: start -> 202 accepted; the code is delivered to the server log
curl -sX POST http://localhost:8082/auth/passwordless/start -H 'Content-Type: application/json' \
  -d '{"identifier_kind":"email","identifier":"admin@example.com","method":"code"}'   # 202
curl -i -c jar -b jar -X POST http://localhost:8082/auth/passwordless/verify -H 'Content-Type: application/json' \
  -d '{"identifier_kind":"email","identifier":"admin@example.com","code":"<CODE_FROM_LOG>"}'  # 200 + cookies
# email magic link: start method=link -> the link is logged as
#   http://localhost:8082/auth/magic#token=<token>  (config base, token on the fragment)
curl -sX POST http://localhost:8082/auth/passwordless/start -H 'Content-Type: application/json' \
  -d '{"identifier_kind":"email","identifier":"admin@example.com","method":"link"}'   # 202
# opening the link in a browser: the /auth/magic page reads the fragment client-side,
# scrubs history, and POSTs /auth/passwordless/redeem -> 200 + session. The token never
# enters a request URL/log/referrer. Replaying the same token -> 401 (single-use atomic).
```

An unknown identifier returns the identical 202 at the same wall time and produces
no delivery job (a blocked/slow provider cannot change the start response or leak
existence).

### Leg 10 — auth-v3: account security, identifiers, step-up (bearer)

The masked inventory and the credential/identifier suite. All are
`RequireLiveSession` + browser-safe + `Cache-Control: no-store`. With the admin
session:

```sh
# masked method inventory (identifier values masked; never a full value)
curl -s -b jar http://localhost:8082/auth/methods
#  -> {"has_password":true,"oauth":[...],"identifiers":[{"kind":"email","value":"a***@example.com",
#      "uses":["login","recovery","notification"],"primary":true,"removable":false}]}
# add an identifier: start delivers a proof code to the NEW address (server log)
curl -sX POST -b jar http://localhost:8082/auth/identifiers/email -H 'Content-Type: application/json' \
  -d '{"email":"admin2@example.com","uses":{"notification":true},"make_primary":false}'  # 200 {"status":"sent","receipt":...}
curl -sX POST -b jar http://localhost:8082/auth/identifiers/email/confirm -H 'Content-Type: application/json' \
  -d '{"code":"<PROOF_CODE_FROM_LOG>"}'                                                   # 200 {"status":"confirmed"}
# remove the last login identifier -> 409 cannot_remove_last_method (policy-refused)
# step-up before a sensitive mutation (a freshly-logged-in session already satisfies it):
curl -sX POST -b jar http://localhost:8082/auth/step-up/password -H 'Content-Type: application/json' \
  -d '{"purpose":"remove_password","context":"","password":"correct horse battery staple"}'  # 200 verified
# set/remove password + provider-bound OAuth unlink (a fake-provider code cannot unlink another provider)
```

The same journeys are reachable in the browser from the account hub
(`GET /auth/account`): masked inventory, add→confirm→edit/remove identifiers,
password set/change/remove, and OAuth unlink — the HTML forms call the SAME service
methods (the HTML layer recalculates no policy). A stale/revoked session on an
account page redirects to login (after validating a safe relative return-to).

### Leg 11 — auth-v3: override systems + delivery runtime

The host demonstrates the **two distinct override facilities**: `Config.Views`
(the branded Login page, Leg 8) and `Config.EmailContentTemplates` (a branded
verification email BODY at `email.LayerApp`). Register any user and the delivered
verification body reads "Your Gopernicus CMS verification code is: …" (the
LayerApp override), not the bundled default — proof the two systems are distinct.
The startup log shows the development console-transport WARNs (email sender + phone
notifier) and the per-job delivery lifecycle (`delivery job … outcome=delivered
attempt=1`). The delivery dispatcher is the ONLY send path:
retry/replacement/terminal/purge are proven hermetically
(`internal/logic/delivery` + the jobs-mode host proofs), and a memory host cannot
boot production, so the production-negative gates
(`cmd/server/production_test.go`) are hermetic: console→`ErrInsecureDeliveryTransport`,
http-base→`ErrPublicAuthBaseURLInsecure`, memory-limiter→`ErrNonDurableRateLimiter`,
nil-keyer→`ErrIdentifierKeyerRequired`, and (default `jobs` mode) an unacknowledged
jobs runtime→`ErrDeliveryJobsUnacknowledged`.

## Invitation kinds demo (identity-resolution, 2026-07-10)

The host wires a phone-kind `notify.Console` notifier, so phone
invitations are a supported kind here: `POST /auth/invitations/project/demo
{"identifier":"+1 555 0134","identifier_kind":"phone","relation":"member"}`
→ 201, the token DELIVERED to the server log (the dev stand-in for SMS),
accepted by token (no email-match — address-possession binding). An
unwired kind (e.g. `slack`) fails 400; email is always-on via the Mailer.

## Route surface

- **events** (SSE, `features/events`): `GET /events` — the authenticated
  subject's stream (best-effort `content.*` fan-out), gated by `RequireUser`
  (401 when absent). `GET /events/{resource_type}/{resource_id}` — the
  resource-scoped stream, registered because `Config.Authorize` is wired through
  the authorization engine (`authorizer.Check`, the flagship posture): member →
  stream, resolved non-member → 403. Under `EVENTS_OUTBOX=memory` the host also
  mounts `POST /outbox-demo` (host-owned durable-rail trigger, not feature surface).
- **auth** (JSON + HTML, `features/authentication`): core
  `POST /auth/{register,login,logout,verify,refresh,password/forgot,password/reset,
  password/change,token}`; the v3 credential/identifier suite
  `GET /auth/methods`, `POST /auth/step-up/{begin,password,code}`,
  `POST /auth/password/{set,remove/start,remove}`,
  `POST /auth/identifiers/{email,phone}{,/confirm}`,
  `PATCH|DELETE /auth/identifiers/{id}`; passwordless
  `POST /auth/passwordless/{start,verify,redeem}`;
  `GET /auth/delivery/status`; OAuth
  `/auth/oauth/{provider}/{start,callback,link/start,unlink/start,unlink}` +
  `/auth/oauth/verify-link`; machine `/auth/service-accounts…`,
  `/auth/api-keys/{id}/revoke`; invitations `/auth/invitations/…`. Because
  `Config.Views` is wired, the HTML GET pages (`/auth/{login,register,verify,
  password/forgot,password/reset,account,step-up,magic,…}`) mount alongside the
  JSON API — a form POST 303-redirects, a JSON POST keeps its JSON body.
- **cms**: public site (`GET /`, published singles, contact) ungated; admin CRUD
  (`/articles`, `/pages`, `/terms`, `/menus`, `/media`, …) gated by
  `AdminMiddleware` (auth's `RequireUser`).
- **host-local demo/debug** (host code, not feature surface):
  `GET /demo/whoami` (RequirePrincipal-gated: any credential class → 200),
  `GET /demo/members-only` (RequirePrincipal + engine-Check gated: member/owner →
  200, resolved non-member → 403), `GET /demo/my-projects` (the relationship
  kind's `LookupResources` enumeration → `{admin, ids}`), `GET /demo/audit`
  (the roles kind's `HasRole` gate + a direct-scope `ListRoleAssignmentsByResource`
  read-back), and `GET /debug/security-events`
  (`AUTH_DEBUG=1` + `RequireUser`). The demo routes are READ-ONLY: AZ3-4.1 removed the
  session-only `POST /demo/roles/{assign,unassign}` and `POST /demo/admin/bootstrap`
  mutation routes. See "Authorization postures" for the flagship demo flow.
- **host-local health** (host code, not feature surface): `GET /healthz` —
  unauthenticated liveness probe. Both stores are memory-backed, so there is no
  DB to probe: reaching the handler returns `200`. `GET /healthz/delivery` —
  unauthenticated, secret-free delivery operational health (bounded counters/gauges/
  enums): `{mode, runtime, admitted, outstanding, queued, capacity, saturated,
  delivered, skipped, retried, dead_lettered, superseded, purged, observer_failures}`.
  See the "Delivery operational health" wiring bullet above.
