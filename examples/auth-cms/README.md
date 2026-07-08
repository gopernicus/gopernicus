# examples/auth-cms — the two-feature proof host (auth-v2 A9)

This host mounts **two real feature modules** — `features/cms` and
`features/authentication` — onto one host router, with in-memory stores and no datastore
driver, and wires auth's identity middleware into cms's admin surface. It is the
auth-v2 milestone's **A9 proof host**: on top of the v1 cross-feature seam it
exercises the whole auth-v2 surface end to end (OAuth, machine identity, JWT
bearer, security-event audit, and ReBAC-decoupled invitations) with zero infra.

## What it proves

- **Constitution rule 6 (features never import other features), with two real
  features.** `features/cms` never imports `features/authentication`; `features/authentication`
  never imports `features/cms`. Only this host's `cmd/server/main.go` imports
  both. The cross-feature connection is made entirely in the composition root
  (`auth.Service.RequireUser` → `cms.Config.AdminMiddleware`), and the toy
  membership `Granter` (below) → `auth.Config.Granter`.

- **The feature-module opt-out holds for a second feature — no libsql in the
  module graph:**

  ```sh
  cd examples/auth-cms && GOWORK=off go list -m all | grep -i libsql   # empty
  ```

  (The repo-root `go.work` unions every workspace module, so a workspace-active
  `go list -m all` reports the store adapters' libsql; the module's own graph —
  `GOWORK=off`, i.e. what actually builds this host — has none, exactly like
  `examples/minimal`.) `bcrypt` and `golang-jwt` are CPU-bound libraries with no
  external service, so the host stays zero-infra.

- **The whole auth-v2 surface, live:** the verified-email login gate, a
  host-local fake OAuth provider, API-key machine calls, stateless bearer JWTs
  (host-signed by `integrations/cryptids/golang-jwt`), security-event audit rows,
  and invitations that grant through a **toy in-memory membership `Granter`** —
  the demonstration of ratified AV4: invitations work with **no ReBAC anywhere in
  the module graph**. `authorization-v1` later swaps `CreateRelationships` in via
  the same seam.

## Wiring

- **cms store**: `internal/memstore` (in-memory cms ports).
- **auth store**: `internal/authmem` — an in-memory implementation of **all
  twelve** auth ports (v1 user/password/session/verification, plus the v2
  oauth-account, oauth-state, service-account, api-key, security-event, and
  invitation ports). It honors the contracts the shared `features/authentication/storetest`
  suite proves (uniqueness, sentinels, expired-at-read, the pinned GetByHash and
  partial-pending-uniqueness contracts, and the created_at DESC, id DESC paging).
- **hasher**: `bcrypt.New()`. **mailer**: `email.NewConsole(log)` (logs mail —
  this is how you read verification codes and invitation tokens below).
- **OAuth provider**: `fakeOAuthProvider` (`cmd/server/oauthfake.go`) — a
  self-contained `sdk/oauth.Provider`, no vendor, no network; identity derived
  from the authorization `code`.
- **TokenSigner**: `golang-jwt` from `AUTH_JWT_SECRET`; absent → an **ephemeral**
  per-boot key (tokens don't survive a restart); `AUTH_JWT_DISABLED=1` → nil.
- **Granter**: `membership` (`cmd/server/membership.go`) — a toy in-memory
  `resource→relation→subject` map, read back by the membership-gated demo route.
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

### Config / port nil-semantics (host view)

| collaborator | this host wires | nil/absent means |
|---|---|---|
| `Config.Providers` | the fake provider | OAuth routes not registered (deny-by-absence) |
| `Config.TokenSigner` | golang-jwt (or ephemeral / nil) | `POST /auth/token` 404; bearer JWTs never parsed |
| `Config.TokenEncrypter` | AES-GCM iff `AUTH_TOKEN_ENCRYPTER_KEY` | provider tokens not persisted (login/link still work) |
| `Config.Granter` | toy membership map | invitation routes not registered (deny-by-absence) |
| `Repositories.SecurityEvents` | authmem | no audit trail (recording site is a no-op) |
| `AUTH_DEBUG` | off by default | `/debug/security-events` not registered (404) |

## Environment

See [`.env.example`](.env.example) for every knob (all secret-free placeholders):
`AUTH_JWT_SECRET`, `AUTH_JWT_DISABLED`, `AUTH_TOKEN_TTL`,
`AUTH_TOKEN_ENCRYPTER_KEY`, `AUTH_DEBUG`, `OAUTH_CLIENT_ID/SECRET`. The host boots
with **none** of them set (JWT mode uses an ephemeral key; the debug route is
off).

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
curl -sX POST http://localhost:8082/auth/verify -H 'Content-Type: application/json' \
  -d '{"code":"<CODE_FROM_LOG>"}'
# 5. login -> 200 + Set-Cookie; gated GET -> 200; logout -> 200; gated GET -> 401
curl -i -c jar -b jar -X POST http://localhost:8082/auth/login -H 'Content-Type: application/json' \
  -d '{"email":"admin@example.com","password":"correct horse battery staple"}'
curl -i -c jar -b jar http://localhost:8082/articles
curl -i -c jar -b jar -X POST http://localhost:8082/auth/logout
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

### Leg 3 — JWT bearer

```sh
# issue a short-TTL user token
curl -sX POST http://localhost:8082/auth/token -H 'Content-Type: application/json' \
  -d '{"email":"admin@example.com","password":"correct horse battery staple"}'  # -> {"token":"<JWT>","expires_at":...}
curl -i -H "Authorization: Bearer <JWT>" http://localhost:8082/demo/whoami       # 200 (user principal)
```

Expired-token path — reboot with a 1-second TTL, mint, wait, retry → **401**:

```sh
AUTH_JWT_SECRET=$AUTH_JWT_SECRET AUTH_TOKEN_TTL=1s go run ./cmd/server
# ... mint a token, then after >1s:
curl -i -H "Authorization: Bearer <JWT>" http://localhost:8082/demo/whoami       # 401 (expired)
```

Absent-signer path — reboot with the signer disabled; the same valid-looking JWT
is never parsed and the token route is gone:

```sh
AUTH_JWT_DISABLED=1 go run ./cmd/server
curl -i -H "Authorization: Bearer <JWT>" http://localhost:8082/demo/whoami       # 401 (never parsed)
curl -i -X POST http://localhost:8082/auth/token                                 # 404 (route absent)
```

### Leg 4 — invitations (toy membership `Granter`)

The membership-gated route is `GET /demo/members-only` — it checks the toy map
for the `member` relation on the `project/demo` resource.

```sh
# register + verify user B (b@example.com) and user C (c@example.com), each with its own cookie jar.
# B is not a member yet:
curl -i -c bjar -b bjar http://localhost:8082/demo/members-only                  # 403
# A (admin session) invites B on project/demo -> 201 pending; token is in the mailer log
curl -sX POST -c jar -b jar http://localhost:8082/auth/invitations/project/demo \
  -H 'Content-Type: application/json' -d '{"identifier":"b@example.com","relation":"member"}'
# B accepts with the token -> 200, and the toy Granter grants B
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
present**. `Config.Authorize` is left nil, so the resource-scoped
`/events/{resource_type}/{resource_id}` route is **not registered** (deny by
absence). `Repositories.Outbox` is nil — direct-emit mode: the gateway fans
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
`Repositories.Outbox` and drives an `events.Poller` on an `sdk/workers` pool. A
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

Shutdown order (SIGTERM): **HTTP server → poller pool → `bus.Close`** — the poller
pool runs on its own Background-derived context so it stops only after HTTP has
drained, and the bus closes last on a fresh bounded context.

## Route surface

- **events** (SSE, `features/events`): `GET /events` — the authenticated
  subject's stream (best-effort `content.*` fan-out), gated by `RequireUser`
  (401 when absent). The resource-scoped `/events/{resource_type}/{resource_id}`
  route is not registered (nil `Authorize`, deny by absence). Under
  `EVENTS_OUTBOX=memory` the host also mounts `POST /outbox-demo` (host-owned
  durable-rail trigger, not feature surface).
- **auth** (JSON, `features/authentication`): `POST /auth/{register,login,logout,verify,
  password/forgot,password/reset,password/change,token}`; OAuth
  `/auth/oauth/{provider}/{start,callback,link/start,link}`,
  `/auth/oauth/{verify-link,linked}`; machine `/auth/service-accounts…`,
  `/auth/api-keys/{id}/revoke`; invitations `/auth/invitations/…`.
- **cms**: public site (`GET /`, published singles, contact) ungated; admin CRUD
  (`/articles`, `/pages`, `/terms`, `/menus`, `/media`, …) gated by
  `AdminMiddleware` (auth's `RequireUser`).
- **host-local demo/debug** (host code, not feature surface):
  `GET /demo/whoami` (RequirePrincipal-gated: any credential class → 200),
  `GET /demo/members-only` (RequirePrincipal + toy-membership gated: member →
  200, resolved non-member → 403), and `GET /debug/security-events`
  (`AUTH_DEBUG=1` + `RequireUser`).
