# examples/auth-cms — the two-feature proof host

This host is the auth-v1 milestone's acid test. It mounts **two real feature
modules** — `features/cms` and `features/auth` — onto one host router, with
in-memory stores and no datastore driver, and wires auth's identity middleware
into cms's admin surface.

## What it proves

- **Constitution rule 6 (features never import other features), with two real
  features.** `features/cms` never imports `features/auth`; `features/auth`
  never imports `features/cms`. Only this host's `cmd/server/main.go` imports
  both. The cross-feature connection is made entirely in the composition root:

  ```go
  authSvc, _ := auth.NewService(authRepos, authCfg)
  auth.Register(mount, authRepos, authCfg)            // auth's own /auth/* routes
  cms.Register(mount, cmsRepos, cms.Config{
      // ...
      AdminMiddleware: []web.Middleware{authSvc.RequireUser}, // auth gates cms admin
  })
  ```

  `auth.Service.RequireUser` satisfies `sdk/web.Middleware` structurally, so cms
  gates its admin routes on auth without either feature knowing the other
  exists.

- **The feature-module opt-out holds for a second feature.** Both features run
  on in-memory stores (`internal/memstore` for cms, `internal/authmem` for
  auth), so **no libsql is in this module's graph**:

  ```sh
  cd examples/auth-cms && GOWORK=off go list -m all | grep -i libsql   # empty
  ```

  (The repo-root `go.work` unions every workspace module, so a workspace-active
  `go list -m all` reports the store adapters' libsql; the module's own graph —
  `GOWORK=off`, i.e. what actually builds this host — has none, exactly like
  `examples/minimal`.) `bcrypt` is a CPU-bound library with no external service,
  so importing `integrations/cryptids/bcrypt` keeps the host zero-infra.

## Wiring

- **cms store**: `internal/memstore` — an in-memory implementation of cms's five
  ports (a verbatim copy of `examples/minimal/internal/memstore`; a host never
  reaches into another example's `internal/`).
- **auth store**: `internal/authmem` — an in-memory implementation of auth's five
  ports (`user.UserRepository`, `user.PasswordRepository`,
  `session.SessionRepository`, `verification.CodeRepository`,
  `verification.TokenRepository`). It honors the port contracts the shared
  `features/auth/storetest` suite proves: duplicate email → `errs.ErrAlreadyExists`,
  expired session/code/token → `errs.ErrExpired` on read.
- **hasher**: `bcrypt.New()` (real, no infra).
- **mailer**: `email.NewConsole(log)` (dev logger).
- **rate limiter**: auth's default `ratelimiter.NewMemory()` (nil in Config).

No user is seeded — registration is part of the proof flow below.

## The auth flow (cookie jar)

Boot the server (defaults to `localhost:8082`):

```sh
cd examples/auth-cms && go run ./cmd/server
```

Then drive the five-step flow with a cookie jar. Admin routes (the cms CRUD
surface, e.g. `GET /articles`) are gated; public routes (`GET /`, published
singles) are not.

```sh
# 1. Unauthenticated admin request → 401 (JSON error)
curl -i -c jar -b jar http://localhost:8082/articles

# 2. Register → 201
curl -i -c jar -b jar -X POST http://localhost:8082/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"email":"admin@example.com","password":"correct horse battery","display_name":"Admin"}'

# 3. Login → 200 + Set-Cookie (session)
curl -i -c jar -b jar -X POST http://localhost:8082/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"admin@example.com","password":"correct horse battery"}'

# 4. Admin request with the session cookie → 200
curl -i -c jar -b jar http://localhost:8082/articles

# 5. Logout → 200, then the admin request is 401 again
curl -i -c jar -b jar -X POST http://localhost:8082/auth/logout
curl -i -c jar -b jar http://localhost:8082/articles

# Public home is always 200, no session required
curl -i http://localhost:8082/
```

## Route surface

- **auth** (JSON): `POST /auth/{register,login,logout,verify,password/forgot,password/reset}`.
- **cms**: public site (`GET /`, published singles, contact) ungated; admin CRUD
  (`/articles`, `/pages`, `/terms`, `/menus`, `/media`, …) gated by
  `AdminMiddleware`.
