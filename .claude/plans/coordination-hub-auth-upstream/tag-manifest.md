# coordination-hub-auth-upstream — owner-cut tag manifest

Prepared by U7 (2026-08-14). **RELEASED 2026-08-15** on owner dispatch:
batch commit `261f859` (with the owner-approved `__Host-auth_csrf` rename
folded in), all three tags cut and pushed on it, cold-scratch resolution
verified for each, post-tag `make tidy` committed as `fe3dc65`, and
coordination-hub repinned on `authentication-ui` (`d4ccfad`).

Release evidence:

- `make check` green on `261f859` before tagging.
- live-stores dispatches on `261f859`: runs 31889882851 and 31890041347.
  Job `redis-pgxdb` (the limiter gate) **passed in both**. Job
  `postgres-turso` failed in both on
  `TestConformance_Postgres/Sessions/RotationKeepsExpiresAt` — a ns-vs-µs
  timestamp-precision assertion in `features/authentication/stores/pgx`
  (untouched since its v0.1.0 tag, not retagged by this batch; passes
  locally against postgres:17, last CI green 2026-07-07 on identical code).
  Recorded as a known flake / follow-up, not a batch defect.
- Cold scratch: `sdk@v0.3.0`, `features/authentication@v0.2.0` (selected
  sdk v0.3.0 with no workspace — the pin proof), `pgxdb@v0.3.0` all
  resolved and built.

Tags are owner-cut and immutable; a correction is a new patch tag,
never a retag.

Plan of record: the batch plan file (working tree `1.md` at the repo root —
file it beside this manifest when the batch is committed). Upgrade notes for
every tag below are in `RELEASING.md` under "Upgrade notes".

## 1. Expected tags

| Module | Tag | Bump | Why |
|---|---|---|---|
| `sdk` | `sdk/v0.3.0` | minor (additive API, one behavior change) | `CORSWithConfig`/`CORSConfig`; `Vary: Origin`; `Access-Control-Expose-Headers` defaulting to `X-Request-ID`; genuine-preflight-only 204. **`WebHandler.Use` now wraps the entire mux and `HandleRaw` no longer bypasses global middleware** — the batch's one silent behavior change for existing sdk consumers. |
| `features/authentication` | `features/authentication/v0.2.0` | minor (additive) | `Config.RefreshCookiePath` + `ErrRefreshCookiePathInvalid`; `GET /auth/csrf`; `GET /auth/me`; `origin_rejected` code split; login/register/me share one email projection. `go.mod` now requires `sdk v0.3.0`. |
| `integrations/datastores/pgxdb` | `integrations/datastores/pgxdb/v0.3.0` | minor (additive API; **ships host schema**) | `Limiter`, `NewLimiter`, `WithLimiterKeyPrefix`, `(*Limiter).StatusCheck`. Adopters must apply the reference DDL from the connector README before deploying. |

### Modules that do NOT get a tag

- **`integrations/oauth/google` stays at `v0.1.0`** (U6 verdict). Evidence:
  `git diff integrations/oauth/google/v0.1.0 -- integrations/oauth/google` is
  **empty** — source and `go.mod` are byte-identical to the tag. Its only sdk
  import is `sdk/capabilities/oauth`, which this batch did not touch (U1 changed
  `sdk/foundation/web` only). Its `sdk v0.1.0` floor is deliberately NOT raised:
  MVS supplies the host's higher compatible sdk. Build/test/vet are green under
  the workspace-selected sdk.
- **`features/authentication/stores/{pgx,turso}` do NOT retag.** No repository
  contract changed: `git diff features/authentication/stores/pgx/v0.1.0 -- …` and
  the turso equivalent are both **empty**, and the batch touched no `domain/`
  port, no migration, and no store adapter. Their `features/authentication v0.1.0`
  / `sdk v0.1.0` pins upgrade at the host through MVS.
- **`features/authentication/views/goth`** — untouched; stays `v0.1.0`.
- The four `examples/*` hosts are demonstrations and are never tagged.

### The one go.mod change in this batch

`features/authentication/go.mod`: `sdk v0.1.0` → **`sdk v0.3.0`**. This is the
CSRF/CORS tag-coupling control (plan risk 2): the feature's documented
cross-origin browser flow cannot work on an sdk whose CORS allow-header list is
not configurable, so the pair is pinned rather than left to a host's MVS luck.

**The pinned tag does not exist yet.** Workspace builds stay green because
`go.work` resolves siblings by directory, but `go mod tidy` / any cold (`GOWORK=off`)
resolution of `features/authentication` will fail until the owner pushes
`sdk/v0.3.0`. Do not run `make tidy` on this tree before the sdk tag is pushed.

No other in-repo module needs a corresponding bump: the auth store and views
modules keep their `features/authentication v0.1.0` requirement (MVS raises it at
the host), `integrations/datastores/pgxdb` keeps `sdk v0.1.0` (every sdk symbol
its limiter uses shipped in v0.1.0), and the `examples/*` hosts resolve every
sibling through relative `replace` directives at `v0.0.0`.

## 2. Owner tag commands — DO NOT RUN as part of U7

Run from the repo root, on the commit that carries this batch, only after
`make check` and the live gates are green on that exact commit:

```sh
git tag sdk/v0.3.0 -m "sdk v0.3.0"
git tag features/authentication/v0.2.0 -m "features/authentication v0.2.0"
git tag integrations/datastores/pgxdb/v0.3.0 -m "integrations/datastores/pgxdb v0.3.0"

git push origin sdk/v0.3.0
git push origin features/authentication/v0.2.0
git push origin integrations/datastores/pgxdb/v0.3.0
```

Order matters for the first consumer resolution: push `sdk/v0.3.0` **before**
`features/authentication/v0.2.0`, because the feature's `go.mod` requires it.

## 3. Pre-tag evidence

- `make check` (full: templ/asset drift, warm scaffold cache, vet+build+test
  across all 39 workspace modules, integration-tag vet, all eighteen guards).
- Live pgxdb limiter gate: `POSTGRES_TEST_DSN=… go test -race ./...` in
  `integrations/datastores/pgxdb` against a real postgres:17 — including the
  exact-K concurrency proof (40 concurrent callers, ceiling 8) that the shared
  sequential conformance suite cannot make.
- Google gate: `go build ./... && go test ./... && go vet ./...` in
  `integrations/oauth/google` under the workspace-selected sdk.
- Cross-module acceptance:
  `examples/auth-cms/cmd/server/browser_cookie_flow_test.go` — the full browser
  flow through exported host seams only (section 5).
- **Live-stores dispatch (per the platform-SRE review).** The
  `live-stores` workflow is `workflow_dispatch`-only and NOT a required check —
  the button is pressed before cutting a tag. Press it on the batch commit: job
  `redis-pgxdb` now runs the pgxdb suite with `-race` against the postgres:17
  service container (the limiter legs included). Its DSN is **DESTRUCTIVE** —
  the limiter legs `CREATE TABLE ratelimit_windows`, create/drop a scratch
  schema, and run an unqualified `DELETE FROM ratelimit_windows` — so it may only
  ever point at the per-run service container, never a shared database. Failure
  signal is GitHub's workflow-run-failure email to whoever pressed dispatch.

## 4. Post-tag verification — BLOCKED until the owner cuts the tags

This is the remaining step. It cannot run before the tags are pushed and the
module proxy serves them.

1. Cold scratch resolution, one per tag, outside the workspace:

   ```sh
   cd "$(mktemp -d)" && go mod init scratch
   GOWORK=off GOFLAGS=-mod=mod go get github.com/gopernicus/gopernicus/sdk@v0.3.0
   GOWORK=off GOFLAGS=-mod=mod go get github.com/gopernicus/gopernicus/features/authentication@v0.2.0
   GOWORK=off GOFLAGS=-mod=mod go get github.com/gopernicus/gopernicus/integrations/datastores/pgxdb@v0.3.0
   GOWORK=off go build ./...
   ```

   The authentication leg is the one that proves the new pin: a cold resolution
   must select `sdk v0.3.0` with no workspace and no local replace.

2. Optionally re-run `make tidy` in this repo once the tags exist (it is expected
   to fail before then — see section 1).

3. Only then, in coordination-hub:

   ```sh
   go get github.com/gopernicus/gopernicus/sdk@v0.3.0
   go get github.com/gopernicus/gopernicus/features/authentication@v0.2.0
   go get github.com/gopernicus/gopernicus/integrations/datastores/pgxdb@v0.3.0
   go mod tidy
   go mod vendor
   ```

   `integrations/oauth/google` needs no `go get` — it stays at `v0.1.0`.

## 5. coordination-hub wiring record

The exact composition U7 proved end to end
(`examples/auth-cms/cmd/server/browser_cookie_flow_test.go`, which uses exported
seams only — it is in a different module from the feature and cannot reach an
`internal/` package).

### 5.1 Global CORS — one `Use`, whole mux

```go
router := web.NewWebHandler(web.WithLogging(log))
router.Use(web.CORSWithConfig(web.CORSConfig{
    AllowedOrigins: corsAllowedOrigins,                    // e.g. {"https://spa.example.com"}
    AllowedHeaders: []string{"Accept", "Content-Type", "Authorization", "X-CSRF-Token"},
    // ExposedHeaders nil ⇒ X-Request-ID is exposed by default.
}))
```

- `AllowedHeaders` is non-nil, so it **replaces** the default list — the
  `Accept, Content-Type, Authorization` entries must be repeated. The sdk never
  names a feature's header; the host opts `X-CSRF-Token` in.
- `Use` now wraps the entire mux, so the preflight to the method-qualified
  `POST /api/v1/auth/password/change` is answered. The local `varyOrigin`
  wrapper and the "wrap the handler passed to `web.Run`" workaround can both be
  dropped (keeping them stays correct).
- `Use` is **boot-time-only**: it rebuilds the dispatch chain without
  synchronization. Register all middleware before serving.
- Anything registered through `HandleRaw` (OpenAPI/metrics/streaming handlers)
  now runs INSIDE this stack. Re-check any raw handler that relied on the old
  bypass.

### 5.2 Authentication config + prefixed mount

```go
authCfg.RefreshCookiePath = "/api/v1/auth"   // MUST match the mount prefix
authCfg.AllowedOrigins    = []string{"https://spa.example.com"}

authSvc, err := auth.NewService(authRepos, authCfg)
// …
err = authSvc.Register(feature.Mount{
    Router: feature.PrefixRegistrar{Prefix: "/api/v1", Next: router},
    Logger: log,
    Events: bus,
})
```

- An invalid `RefreshCookiePath` fails at construction with
  `auth.ErrRefreshCookiePathInvalid` — it is never silently sanitized.
- Keep the host's existing boot check that `CORS_ALLOWED_ORIGINS ⊆
  AUTH_ALLOWED_ORIGINS`; the new `origin_rejected` code makes the runtime symptom
  readable but does not replace the boot check.

### 5.3 Routes the SPA now uses

| Route (prefixed) | Gate | Notes |
|---|---|---|
| `GET /api/v1/auth/csrf` | `RequireLiveSession` + origin-only | `{"csrf_token":"…"}`, `Cache-Control: no-store`; body value always equals the `__Host-auth_csrf` cookie on the same response. Reuses a well-formed token (does not rotate another tab's). Echo it in `X-CSRF-Token` on every browser-safe mutation. |
| `GET /api/v1/auth/me` | `RequireLiveSession` | `{id,email,display_name,email_verified}`, `Cache-Control: no-store`; a machine principal gets 401. |
| `POST /api/v1/auth/refresh` | none (rotation IS the credential) | Cookie-driven with an empty body once `RefreshCookiePath` matches the prefix. |
| `POST /api/v1/auth/logout` | origin-only | Clears the refresh cookie at the same `Path=/api/v1/auth` it was issued under. |

### 5.4 pgxdb limiter — constructor, boot probe, host schema

```go
limiter := pgxdb.NewLimiter(db)                       // or pgxdb.NewLimiter(db, pgxdb.WithLimiterKeyPrefix("ch:rl:"))
if err := limiter.StatusCheck(ctx); err != nil {      // BEFORE serving
    return fmt.Errorf("rate limiter is not usable: %w", err)
}
authCfg.RateLimiter = limiter                          // satisfies ratelimiter.Limiter
```

- **`StatusCheck` is on the concrete `*pgxdb.Limiter`, not on the
  `ratelimiter.Limiter` port.** Hold the concrete type at boot (construct it in
  `main`, probe it, then hand it to config as the port) — a variable typed as the
  port cannot call it without a type assertion.
- The `ratelimit_windows` table + its `expires_at` index are **host-owned**.
  Copy the reference DDL from `integrations/datastores/pgxdb/README.md` into the
  coordination-hub migration ledger and apply it **before** deploying the binary.
  Missing table ⇒ every `Allow` errors `42P01`, and the fail-open callers serve
  unthrottled traffic silently — which is exactly what `StatusCheck` at boot
  prevents.
- Schedule `DELETE FROM ratelimit_windows WHERE expires_at < now();` (cron,
  `features/jobs`, or pg_cron). Pruning is the **retention control** for the
  `key` column, which persists client IPs and user identifiers verbatim.
- Swapping `ratelimiter.NewMemory()` → this limiter is security-relevant: the
  sdk `ratelimiter.Middleware` fails **open**, authentication's login/passwordless
  paths fail **closed** (its refresh path fails open). Decide the posture per path
  and monitor limiter error rate and latency.
