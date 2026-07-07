# Phase 1 — `features/auth` core module

Status: DRAFT — pending ratification
Executor model: opus
Depends on: nothing (first phase). Read `00-overview.md` + the design doc
(`.claude/plans/restructure/auth-feature-design.md`) fully first — §2 (module
shape, Repositories, Config, ports), §3 (Service/RequireUser/CurrentUser,
identity-in-context, rate limiting) are the specification; this file only adds
execution mechanics.

## Goal

A new module `gopernicus/features/auth` that compiles standalone, requires ONLY
`gopernicus/sdk`, passes its unit tests, and exposes exactly the host-facing
surface the design doc defines. No store adapters (phase 5), no host (phase 4).

## Work items

### W1 — module scaffold

`features/auth/go.mod` (module `gopernicus/features/auth`, same Go version as
`features/cms`, `require gopernicus/sdk` + workspace-relative `replace`,
mirroring `features/cms/go.mod`'s shape). Add to root `go.work` and the
Makefile `MODULES` list.

### W2 — domain packages (design doc §2 tree, verbatim)

- `user/`: `User` entity (ID via `sdk/id`, email — normalized/validated,
  display name, timestamps; email verification state), `UserRepository` port,
  `PasswordRepository` port (separate, per §2's security-hygiene rationale).
- `session/`: `Session` entity (opaque token via `sdk/id`, user ID, expiry),
  `SessionRepository` port.
- `verification/`: `Code` + `Token` entities, `CodeRepository` +
  `TokenRepository` ports.
- Port doc comments state the sentinel contract explicitly (duplicate email →
  `errs.ErrAlreadyExists`; unknown → `errs.ErrNotFound`; expired session/token →
  `errs.ErrExpired`) — these become the memstore/turso test assertions later.
  Style-match `features/cms/{media,menus}/repository.go`.

### W3 — `auth.go` (the ENTIRE host-facing surface — one file, per design §3)

`Repositories` (5 ports, §2's struct verbatim) · `PasswordHasher` port
(`HashPassword`/`VerifyPassword`, ported from the original's
`core/auth/authentication/authenticator.go` — read it) · `Config` (§2: Hasher +
Mailer REQUIRED — `NewService`/`Register` error on nil; MailFrom;
RateLimiter nil→`ratelimiter.NewMemory()`; `CookieConfig`) ·
`NewService(repos, cfg) (*Service, error)` · `(*Service) RequireUser(next
http.Handler) http.Handler` · `(*Service) CurrentUser(ctx) (string, bool)` ·
`Register(m feature.Mount, repos Repositories, cfg Config) error` (builds a
Service internally, mounts routes; the "built twice" wrinkle is accepted and
documented in §3 — do not "fix" it).

### W4 — `internal/authsvc`

Registration (hash password, create user + verification code, send code via
Mailer), email verification, login (rate-limit FIRST — `email+IP` key, §3 —
then verify hash, mint session), logout (delete session), password change
(current-password check), password forgot/reset (token issue + redeem),
session validation (used by RequireUser). The unexported `contextKey` +
`withUserID`/`userIDFromContext` live here (§3 identity decision — NOT sdk).
Constant-time comparisons where secrets are compared; never log secrets,
never reveal whether an email exists in forgot-password responses.

### W5 — `internal/http`

JSON handlers + route table for the §5-item-7 surface: `POST
/auth/{register,login,verify,password/forgot,password/reset}`, `POST
/auth/logout` (RequireUser-gated), cookie set/clear per `CookieConfig`. Errors
via `sdk/web`'s error→status mapping. Mounted through `feature.RouteRegistrar`
only.

### W6 — tests (table-driven, stdlib-only, in-module fakes)

authsvc: register/verify/login/logout/reset flows against hand-rolled fake
repositories + a fake hasher + a recording mailer; rate-limit denial path;
sentinel contract per port. http: `httptest` through `web.WebHandler` for each
route incl. 401s. auth.go: nil-Hasher/nil-Mailer constructor errors;
compile-time `var _` assertions for the seams. Target: every exported symbol
exercised.

### W7 — `storetest` conformance suite (ratified R1/R4; portability plan §4)

`features/auth/storetest`: an exported `Run(t *testing.T, newRepos func(t
*testing.T) auth.Repositories)` fanning out per-port subtests over the five
ports — the port doc comments made executable: the W2 sentinel contract,
email/ID uniqueness, expired-at-read session/token semantics, cursor
pagination where ports page, and the mandatory timestamp-precision-collision
pagination case (portability plan §4.1 — assert ordering + identity, never
that nanosecond fidelity survives a round trip). The package also contains
the reference in-memory implementation of the five ports, and the feature's
own `go test ./...` runs the suite against it. Ports and suite are
co-designed — write them together, not suite-after. `testing` is stdlib, so
the acceptance line below ("exactly 1 require") still holds.

## Acceptance

```sh
cd features/auth && go build ./... && go vet ./... && go test ./...
grep -c require features/auth/go.mod            # exactly 1 (gopernicus/sdk)
grep -rn "features/cms" features/auth/          # empty (rule 6)
make check                                      # green with the new module included
```

## Real-interaction check

Standing check (a) from `00-overview.md` — examples/minimal unaffected, boots,
200s. (The auth flow check (b) arrives with phase 4.)

## Execution log

### 2026-07-02 — phase 1 executed (loop leg 6; implementer on opus)

Shipped `features/auth` (9th module): `auth.go` carries the ENTIRE
host-facing surface (PasswordHasher port, 5-port Repositories, CookieConfig,
Config with Hasher+Mailer required / RateLimiter defaulted, Service,
NewService, RequireUser, CurrentUser, Register — "built twice" wrinkle
preserved and documented); domain packages `user/` (User + separate
UserRepository/PasswordRepository), `session/` (opaque sdk/id token),
`verification/` (Code+Token, two ports), all with sentinel-contract doc
comments; `internal/authsvc` (rate-limit-FIRST login on email+IP, unexported
contextKey, constant-time comparisons, no email-existence leaks);
`internal/http` (the six JSON routes + RequireUser-gated logout, 429 on
rate-limit); W6 tests (fakes, denial path, httptest per route); W7
`storetest` (Run + in-package reference impl, suite green in the module's
own go test). go.work + MODULES updated; guard G2 GENERALIZED to all
features/* (A4) and prove-can-fail exercised (injected forbidden import →
guard failed exit 2 → removed → green).

Divergences (logged, all sound): errs.ErrExpired exists — no substitution;
no ports page in v1 → no pagination/precision cases in storetest (per R1's
"where ports page" wording, documented in the package comment); login NOT
gated on email verification (the standing flow (b) has no verify step —
flagged for phase 4 if wanted); ChangePassword implemented+tested but
unrouted (W5's route set is exhaustive); rate-limit denial is feature-local
ErrRateLimited → 429 (no errs sentinel maps there); PasswordHasher methods
are HashPassword/VerifyPassword — phase 2's bcrypt adapter must match
structurally.

Acceptance (re-run FIRSTHAND): build/vet/test PASS (4 packages ok);
`grep -c require go.mod` = 1; `grep -rn features/cms` = 0 hits;
`make check` → "all checks passed" (9 modules, 4 guards incl. generalized
G2). Real-interaction (a): `GET http://localhost:8081/` → 200,
`GET /products/widget-3000` → 200; killed; port free.

Unverified: nothing for this phase; flow (b) arrives with phase 4.
