# Phase 3 — `cms.Config.AdminMiddleware` hook (decision A3)

Status: DRAFT — pending ratification
Executor model: opus
Depends on: nothing in code (independent of phases 1–2); sequenced third.

## Goal

`features/cms` gains one additive Config field — `AdminMiddleware
[]web.Middleware` — applied to every ADMIN route the registry-driven router
mounts, and to nothing public. This is the hook the design doc §3 names as the
cross-feature wiring point (host passes `authSvc.RequireUser` into it) and the
fix for the flagged fact that cms admin routes are currently unauthenticated.

## Context

`features/cms/internal/http/router.go`'s `Mount` iterates
`Registry.Types()` and registers the admin CRUD set (list/new/create/edit/
update/publish/unpublish/delete) plus public routes; `feature.RouteRegistrar`'s
`Handle(method, path, handler, ...middleware)` already accepts variadic
middleware — read both files first; the change should thread the configured
middleware into the admin `Handle` calls only.

## Work items

1. `features/cms/cms.go`: add `AdminMiddleware []web.Middleware` to `Config`
   with a doc comment stating scope (admin routes only; nil = no gating,
   preserving current behavior) and the intended use (a host passes another
   feature's middleware, e.g. auth's RequireUser — cite `features/README.md`
   §5). Thread it through `Register` → `internal/http.Mount`'s deps.
2. `internal/http/router.go`: apply to every admin route registration; do NOT
   touch public routes, the media upload/serving routes if they're
   admin-surface (decide by reading — media management is admin; public asset
   serving is not; match the existing admin/public split exactly and record
   the classification in the execution log).
3. Tests (`internal/http`): a counting middleware asserts (a) invoked on each
   admin route, (b) NOT invoked on public routes, (c) nil AdminMiddleware
   changes nothing (existing tests must pass unmodified — they are the
   regression proof).
4. Both examples still compile/run unmodified (they don't set the field —
   zero-value compatible).

## Acceptance

```sh
cd features/cms && go build ./... && go vet ./... && go test ./...
make check     # green; guards unchanged
```

## Real-interaction check

Standing check (a) — plus: boot examples/minimal and confirm an admin route
(`GET /articles`) still serves 200 with no middleware configured (explicit
no-regression on the zero value).

## Execution log

### 2026-07-02 — phase 3 executed (loop leg 8; implementer on opus)

Shipped the A3 hook: `cms.Config.AdminMiddleware []web.Middleware` (doc
cites charter §5 C2; nil = current behavior), threaded Register →
internal/http.Mount (new `adminMW` param + `WithAdminMiddleware`
RouterOption).

Route classification (recorded per work item 2): GATED — per-type admin
CRUD (list/new/create/edit/update/publish/unpublish/delete under each
AdminBase), terms admin (6 routes), menus admin (8 routes), media
MANAGEMENT (`GET /media`, `POST /media`, `POST /media/{id}/delete`),
`GET /inquiries`. NOT gated (public) — home `GET /{$}`, per-type singles,
`GET /category/{slug}`, `GET /tag/{slug}`, `GET /menu/{slug}`, public
asset serving `GET /media/{id}/file`, `GET/POST /contact`. Public routes
keep their pre-existing page-cache middleware only.

Tests added (existing tests untouched — the nil regression proof):
TestAdminMiddleware_WrapsEveryAdminRoute (34 admin requests, counted),
TestAdminMiddleware_SkipsEveryPublicRoute (9 public requests, zero
invocations), TestAdminMiddleware_NilPreservesBehavior.

Acceptance (re-run FIRSTHAND): cms build/vet/test PASS (9 packages ok);
`make check` → "all checks passed". A stale gopls diagnostic claimed a
Mount arg-count mismatch — disproven by the firsthand build.
Real-interaction (a) + the phase's explicit zero-value check:
`GET http://localhost:8081/` → 200, `GET /products/widget-3000` → 200,
`GET /articles` (admin, no middleware configured) → 200; killed; port
free. Both examples compile unmodified (zero-value compatible).

Unverified: the GATING behavior live (401 without session) — that is
exactly phase 4's flow (b); here only the nil path has a live host.
