# features/auth — session-based authentication

A pluggable, datastore-free authentication feature: registration, email
verification, login/logout, password change/reset, and a `RequireUser`
middleware other routes gate on. Server-side session identity (opaque
token, cookie-delivered), JSON API only — no server-rendered pages (a host
wanting a login *page* renders its own form and calls this API, exactly as
a SPA would). Design of record:
`.claude/plans/restructure/auth-feature-design.md`.

## Layout (the trio — see `features/README.md` §2 for the full contract)

```
auth.go                  the socket: Repositories, Config, PasswordHasher,
                         Service, NewService, Register — the entire
                         host-facing exported surface
logic/                   the hexagon's public rim — entities + repository
  user/                  ports. Public BY NECESSITY: hosts and store
  session/               modules implement/import these across module
  verification/          boundaries
internal/
  logic/authsvc/         services — the sealed interior (business rules)
  inbound/http/          driving adapter: JSON handlers + route table
storetest/               executable spec for logic/'s ports + the
                         reference in-memory implementation
stores/turso/            the outbound tier: per-dialect SQL + canonical
stores/pgx/              migrations, each its own module (source "auth")
```

## Route surface

Claimed namespace **`/auth/*`** (prefixable via `feature.PrefixRegistrar`;
JSON bodies carry no in-page links, so C1's absolute-link limitation does
not apply):

- `POST /auth/register` — `{email, password, display_name}` → 201
- `POST /auth/verify` — `{code}` → 200
- `POST /auth/login` — `{email, password}` → 200 + session cookie
- `POST /auth/logout` — session-gated → 200 + cookie cleared
- `POST /auth/password/forgot` — `{email}` → 200 (never reveals whether
  the email exists)
- `POST /auth/password/reset` — `{token, password}` → 200

Login is rate-limited (email+IP key) BEFORE any credential work → 429 on
denial. Requests are strictly decoded — unknown fields are rejected (400).
Login is not gated on email verification in v1 (`EmailVerified` is tracked,
not enforced).

## Repositories (the five ports a host or store adapter satisfies)

```go
type Repositories struct {
    Users              user.UserRepository
    Passwords          user.PasswordRepository // separate from Users: credential
                                               // material rotates independently
    Sessions           session.SessionRepository
    VerificationCodes  verification.CodeRepository
    VerificationTokens verification.TokenRepository
}
```

Sentinel contract (the port doc comments are the spec; `storetest` is its
executable form): duplicate email → `errs.ErrAlreadyExists`; absent →
`errs.ErrNotFound`; expired session/code/token → `errs.ErrExpired` on read.

## Config — required vs defaulted (deliberate asymmetry)

| field | nil means | why |
|---|---|---|
| `Hasher` (PasswordHasher) | **hard error** at `NewService`/`Register` | a password feature with no hasher is a security foot-gun, not a convenience |
| `Mailer` (email.Sender) | **hard error** | silently dropping verification/reset emails is unsafe degradation |
| `MailFrom` | — | From address for verification/reset mail |
| `RateLimiter` | `ratelimiter.NewMemory()` | permissive default is safe-by-default |
| `SessionCookie` (CookieConfig) | sane defaults | name/secure/domain/max-age |

`integrations/cryptids/bcrypt` satisfies `PasswordHasher` structurally
(`bcrypt.New()`); the integration never imports this module.

## The dual entry points (the cross-feature wiring pattern)

`Register(mount, repos, cfg)` mounts the routes. `NewService(repos, cfg)`
builds the identity capability WITHOUT routes — the surface other features
consume via host wiring (charter §5): the host builds `authSvc`, passes
`authSvc.RequireUser` into e.g. `cms.Config.AdminMiddleware`, and calls
`auth.Register` for auth's own routes. `Service` is deliberately built
twice in that flow (once inside Register) — it holds no state beyond the
shared deps, so this is two allocations, not a bug. See
`examples/auth-cms/cmd/server/main.go` for the live wiring, and that
host's README for the end-to-end curl flow.

## Datastores — {turso, postgres} out of the box, or none at all

Both dialect stores ship and pass the same `storetest` suite (charter
checklist items 10–11; live runs recorded in NOTES.md). A host may also
satisfy `Repositories` itself — `examples/auth-cms/internal/authmem` is the
zero-infra proof. Store conformance is env-gated: turso via
`-tags=integration` + `TURSO_*`; postgres via `POSTGRES_TEST_DSN`
(`docker run --rm -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:17`).
Schema notes: session tokens are stored plain (the port's opaque-token
contract; hashing is a v2 hardening candidate); child tables carry no
enforced FKs (the suite exercises them independently).
