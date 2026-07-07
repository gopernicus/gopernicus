# Phase 4 — `examples/auth-cms`: the two-feature proof host (decision A1)

Status: DRAFT — pending ratification
Executor model: opus
Depends on: phases 1 + 2 + 3. This is the milestone's acid test — design doc §4
is the specification.

## Goal

A new module `gopernicus/examples/auth-cms` mounting BOTH features with
in-memory stores: auth gates cms's admin surface via
`Config.AdminMiddleware`, constitution rule 6 is demonstrated with two real
feature modules, and the full auth flow works over real HTTP.

## Work items

1. Module scaffold (`go.mod` requiring `gopernicus/{sdk, features/auth,
   features/cms, integrations/cryptids/bcrypt}` + replaces; go.work; Makefile
   MODULES). **No libsql anywhere in its graph** — verify like
   examples/minimal does.
2. `internal/authmem`: in-memory implementations of auth's five ports —
   sibling pattern to `examples/minimal/internal/memstore` (read it; memstore
   now enforces uniqueness — mirror that honesty per design §4:
   `UserRepository.Create` on duplicate email → `errs.ErrAlreadyExists`;
   expired sessions/tokens → `errs.ErrExpired` on read). Its tests call
   `storetest.Run` (phase 1 W7) — the suite subsumes the bespoke
   uniqueness/honesty assertions (ratified R1 edit 4); add bespoke cases only
   for behavior the suite doesn't cover.
3. `cmd/server/main.go`, modeled on examples/minimal's (read it): cms wired
   exactly as minimal does (reuse its memstore package via a local copy or its
   own — decide by reading; minimal's is `internal/`, so this host needs its
   own cms memstore too — copy the package and note it; do NOT reach into
   another example's internal), plus:
   `hasher := bcrypt.New()` · `mailer := email.NewConsole(logger)` ·
   `authSvc, err := auth.NewService(authRepos, authCfg)` ·
   `auth.Register(mount, authRepos, authCfg)` ·
   `cms.Register(..., cms.Config{ ..., AdminMiddleware:
   []web.Middleware{authSvc.RequireUser} })` — the §3 wiring sketch, live.
   Port: localhost:8082 default (avoid colliding with minimal's 8081).
   Seed cms content as minimal does; do NOT seed a user (registration is part
   of the proof).
4. README.md for the example: what it proves (rule 6 with two real features),
   the curl flow from `00-overview.md` check (b), and the module-graph claim.

## Acceptance

```sh
cd examples/auth-cms && go build ./... && go vet ./... && go test ./...
go list -m all | grep -i libsql            # in examples/auth-cms — empty
grep -rn "features/auth" features/cms/     # empty
grep -rn "features/cms" features/auth/     # empty
make check                                  # green, module included
```

## Real-interaction check — THE milestone gate

Standing check (a), then check (b) from `00-overview.md` in full against
localhost:8082 (cookie jar; the five steps: unauthenticated admin 401 →
register → login+cookie → admin 200 → logout → admin 401 again). Also:
`GET /` (public cms home) serves 200 WITHOUT any session — public surface must
stay ungated. Record every URL + status code. Kill the server; port free.

## Execution log

### 2026-07-02 — phase 4 executed (loop leg 9; implementer on opus) — THE ACID TEST PASSES

Shipped `examples/auth-cms` (11th module): `internal/authmem` (five honest
ports; tests = `storetest.Run` per amended work item 2), `internal/memstore`
(verbatim copy of minimal's, noted in the package comment, keeps its
conformance call), `cmd/server/main.go` (both features mounted; bcrypt
hasher, Console mailer, `auth.NewService` + `auth.Register`, cms with
`AdminMiddleware: []web.Middleware{authSvc.RequireUser}`; :8082; cms seeded,
NO user seeded), README. go.work + MODULES updated. No changes to
features/* or sdk — the flow surfaced no feature bug.

**Flow (b), driven TWICE live** (implementer, then the loop leg firsthand
with a fresh cookie jar):
1. `GET /articles` no session → **401**
2. `POST /auth/register` → **201**
3. `POST /auth/login` → **200** + one session cookie in the jar
4. `GET /articles` with cookie → **200**
5. `POST /auth/logout` → **200**; `GET /articles` again → **401**
Public `GET /` with no session → **200**. Server killed; port 8082 free.
(Firsthand note: a first attempt sent `name` instead of `display_name` and
got a clean 400 — strict request decoding verified incidentally.)

Isolation proofs: `grep -rn "features/auth" features/cms/` EMPTY;
`grep -rn "features/cms" features/auth/` EMPTY;
`GOWORK=off go list -m all | grep -i libsql` in auth-cms → 0 hits (the
workspace-active variant unions all modules and is not the meaningful
check — README documents the distinction; minimal behaves identically).

Acceptance (firsthand): module build/vet/test PASS; `make check` → "all
checks passed" (11 modules, 4 guards). Standing check (a): minimal :8081
`GET /` → 200, `GET /products/widget-3000` → 200; port free.

Unverified: nothing — constitution rule 6 is now demonstrated with two
real, independently-modured features composed only in a host's main.
