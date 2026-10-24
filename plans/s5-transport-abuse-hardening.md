# S5 — Transport and abuse hardening (SOC 2 readiness, slice 5)

**Status:** EXECUTED 2026-10-23 — RELEASED. Six code PRs squash-merged on `main`: #175 PR-5 go-oidc @ 815e8435 · #176 PR-4 pgxdb @ c9882f13 · #177 PR-1 sdk @ ba561be1 · #178 PR-3 cms @ e36add4a · #179 PR-6 workshop @ 764d5d6e · #180 PR-2 authentication @ 99b9e30d; PR-7 = the record (`docs/s5-release-record`). SIX tags (no store tags, R-1): integrations/cryptids/go-oidc v0.2.0 @ 815e8435; integrations/datastores/pgxdb v0.13.0 @ c9882f13 (MVS set of the six released pgx stores cold-built); sdk v0.17.0 @ ba561be1; pockets/cms v0.6.0 @ e36add4a; workshop/gopernicus v0.8.0 @ 764d5d6e; pockets/authentication v0.24.0 @ 99b9e30d. Live legs on merged main before each pocket/connector tag (six pgx store legs before pgxdb; cms pgx+turso before cms; auth pgx bare+schema+turso before authentication). Build-time deviations under "Build-time deviations" below. Former status: RATIFIED 2026-10-23 (owner: "ratified"; rev 2 as reviewed; R-1 no store tags/repin + mandatory legs; R-2 QuerierFrom DEFERRED to the per-tenant plan; R-3 refuse in every mode; R-4 budgets as recommended (native OAuth 40); R-5 explicit public at commit/validPage + cms AllowImplicitPublic; R-6 PDFs as attachment). Build started from `main` @ 1a749d01: PR-1 sdk, PR-4 pgxdb, PR-5 go-oidc in parallel; PR-2/PR-3 after the sdk tag; PR-6 after sdk + pgxdb tags; PR-7 last.

Anchors read on `main` @ 1a749d01 (S1–S3 RELEASED — `RELEASING.md` top sections). Released: sdk v0.16.0;
authentication v0.23.0 + stores pgx v0.11.0 / turso v0.10.0; authorization v0.30.0 + stores pgx v0.21.0 / turso
v0.20.0; cms v0.5.0 + stores pgx v0.5.0 / turso v0.4.0; jobs v0.7.0; events v0.4.0; secrets v0.2.1; pgxdb v0.12.0;
turso v0.8.0; go-oidc v0.1.0; workshop v0.7.0. Parent: `roadmap.md:200-209` (the S5 slice), `:239-249` (S5–S7
independent), `:316-322` (`pgxdb.QuerierFrom` carry-over — DEFERRED, R-2), `:307` (the 2026-10-23 process ruling:
one store-repin PR per slice if any, two reviewers, 500-line cap, record in RELEASING; legs, cold-verify and
run-and-look stay). Evidence: `review.md:293-307` (M9, M10); `passes/03-http-surface.md:255-345, 367-433`
(FHS-3/4/5/6/8/9/10/11/12); `passes/01-auth-core.md:428-435` (FAC-16); `passes/07-other-pockets.md:152-189`
(FOP-4/5); `passes/06-sdk-kernel.md:153-158` (FSK-9); `passes/02-auth-federation.md:129-134` (FAF-10);
`passes/04-auth-stores.md:141-146` (FAS-10). Control-map rows closed: `control-map.md:64,66,68-71` (CC6.6), `:81,83`
(CC6.7), `:143` (A1); NOT `:155-157` (C1 — R-2). Reviews folded: `review-s5/01-lead-backend-engineer.md` (15),
`02-product-manager.md` (12) — "Decided by gate". Rev 1 is `s5-transport-abuse-hardening.rev1.md` beside this file.

## Goal

Make the framework's HTTP edge safe by default — token-bearing responses uncacheable, every public
credential-establishment route throttled per IP and origin-gated on its JSON arm, limiter outages failing
closed, bodies and uploads bounded, uploads typed by sniffed content, request and forwarded-IP headers
validated before they reach a log or a limiter key, cookie `Domain` and plaintext OIDC discovery refused where
they contradict the trust model, the public page cache opt-in by directive, and Postgres statements and locks
bounded by a knob — in six independent trains (sdk, authentication, cms, pgxdb, go-oidc, workshop), every
behaviour flip carrying a named escape hatch, the record one `RELEASING.md` section with an "Upgrade notes" table.

## Scope and non-goals

Every `roadmap.md:200-209` item, mapped (PR ids are the Tasks below):

| Roadmap item | PR | Finding |
|---|---|---|
| `no-store` on the whole `/auth` registrar | PR-2 | FHS-3, M9 |
| per-route IP budgets register/verify/reset/verify-link/native-OAuth via `AbuseProtectionConfig` (token and forgot are already per-IP in logic — recorded, not re-budgeted) | PR-2 | FHS-4 |
| origin gate on their JSON arm | PR-2 | FHS-4 |
| `PublicRouteLimiterFailOpen` default false, also the refresh per-session arm | PR-2 (+ PR-1 `Retry-After`) | FHS-6, FAC-16 |
| bound `DecodeJSON`; fixed decode-error text | PR-1 | FHS-10, FHS-11, M10 |
| cms upload allow-list + sniff + `MaxBytesReader` + `nosniff` + `Content-Disposition` | PR-3 | FOP-4, FOP-5 |
| static `nosniff`; validate inbound `X-Request-ID`; `Panics` answers JSON on JSON routes | PR-1 | FHS-10, FHS-8, FHS-11 |
| parse forwarded IPs with `netip`, explicit `X-Real-IP` option | PR-1 (+ PR-2 WARN, PR-6 P2) | FHS-9 |
| cookie `Domain` production guard | PR-2 (+ PR-1 accessor, PR-6 P5) | FHS-12 |
| page cache requires explicit `public` | PR-1 (+ PR-3 cms opt-in) | FSK-9 |
| https-only `gooidc` issuer discovery | PR-5 | FAF-10 |
| statement/lock timeout knobs | PR-4 (+ PR-6 scaffold, PR-7 baseline) | FAS-10 |

Deferred, with why: **`pgxdb.QuerierFrom` foreign-`*DB` refusal** (`roadmap.md:316-322`) — to the per-tenant
authorities plan; rev 1's fix was partial (authorization stores use `TxFromContext` directly in six sites; turso's
`QuerierFrom` untouched); correct shape in Notes; the carry-over stays open and control-map C1 is NOT claimed (R-2).
A turso statement-timeout knob (SQLite/libSQL has none; `BusyTimeout` exists — PR-4 adds one README sentence). FHS-6's
fail-open counter (the flag now fails closed). Escalating lockout (S4/M8). `web.Logger` using `web.ClientIP` (S6,
FHS-7). SSE stream cap and explicit `ReadHeaderTimeout`/`MaxHeaderBytes` (FHS-10 remainder; sdk follow-up issue, owner
files). IPv6 /64 bucketing (Notes). Host adoption (once, at the roadmap's end — no handoff file, no manifest). No new
migration, no store code, no `.templ` change, no new guard (every seam is a unit-testable default), no new P-rule.

## Design

Per item: symbol, default, behaviour, finding. "Hatch" = the host's escape from a behaviour flip.

**sdk (`sdk/pkg/web`, `sdk/capabilities/cacher`, `sdk/capabilities/ratelimiter`) — PR-1**

- `DecodeJSONLimit[T any](r *http.Request, limit int64) (T, error)` (new); `DecodeJSON[T](r)` becomes
  `DecodeJSONLimit[T](r, DefaultBodyLimit)` (1 MiB, `readbody.go:21`). The body is read through
  `http.MaxBytesReader(nil, r.Body, limit)` (a nil writer only loses the connection-close hint; overrun still
  surfaces `*http.MaxBytesError`, which `ErrValidation` maps to 413, `errors.go:68-74`). `limit <= 0` → error
  wrapping `sdk.ErrInvalidInput`: no unbounded mode. Flip: bodies > 1 MiB now 413; a host's outer `MaxBytesReader`
  at a larger limit no longer governs. Hatch: `DecodeJSONLimit` with the host's number. FHS-10, M10.
- Fixed decode text: NO new sentinel and NO new wire code. `DecodeJSON*` wraps every read/empty/`null`/unmarshal
  failure with `sdk.ErrInvalidInput` using `ReadBody`'s existing sentences (`readbody.go:84-104`: "request body is
  empty", "request body must be a JSON object", …); `ErrValidation`/`ErrFromDomain` answer 400 with that sentence
  and the existing kind. A `Validate()` error keeps its own text. Go type and field names never reach a client. FHS-11.
- `Panics(log)` (`middleware.go:37-65`): after recovery, when `Accept` lists `application/json` ahead of
  `text/html` or `Content-Type` is `application/json`, answer `RespondJSONError(sw, ErrInternal(...))`; else the
  HTML page as today. No new symbol. FHS-11.
- `RespondJSONError` sets `Retry-After: 5` on a 503 whose header lacks one. One site covers both fail-closed arms:
  `ratelimiter.Middleware`'s `!cfg.FailOpen` branch (`ratelimiter/middleware.go:79-81`, today no `Retry-After`) and
  `ErrFromDomain`'s `sdk.ErrUnavailable` → 503 mapping (`errors.go:322-323`). The pocket's revocation 503 already
  writes 5 (auth README `:1870`) — unchanged. FHS-6 (the 503 contract the pocket claims becomes true).
- `setStaticHeaders` and the SPA-index branch (`static.go:231-239, 254`) add `X-Content-Type-Options: nosniff`. FHS-10.
- `RequestID()` (`middleware.go:125-137`): an inbound `X-Request-ID` is reused only when it matches
  `^[A-Za-z0-9._-]{1,128}$`; otherwise minted. Doc line: trusted proxies may set it. No hatch needed. FHS-8.
- `TrustProxiesConfig{TrustedProxyCount int; TrustRealIPHeader bool}` + `TrustProxiesWithConfig(cfg) Middleware`
  (new, struct input, named like `CORSWithConfig`); `TrustProxies(n)` = `TrustProxiesWithConfig(TrustProxiesConfig{
  TrustedProxyCount: n})`. Resolution (`trustproxies.go:59-75`) parses the chosen hop with `netip.ParseAddr` (after
  `netip.ParseAddrPort` for a bracketed/ported form; zone dropped), returns `Addr.Unmap().String()`, and falls back to
  the peer on garbage. `X-Real-IP` is consulted ONLY when `TrustRealIPHeader` is true AND `TrustedProxyCount >= 1` AND
  `X-Forwarded-For` is absent (today's order, `:64-70`), netip-parsed the same way; today's implicit fallback goes
  away. Flip: a proxy setting only `X-Real-IP` attributes the peer. Hatch: `TrustRealIPHeader: true`. FHS-9. (P2
  keeps `web.TrustProxies(` as the norm; PR-6 teaches it the second name.)
- `OriginPolicy.HasNamespace() bool` (new, beside `HasHTTPNamespace`, `origin_policy.go:143`): any scheme. Consumer:
  PR-2's cookie-Domain guard. Additive.
- `cacher.PageConfig.AllowImplicitPublic bool` (new, default false). The explicit-public rule — `Cache-Control` must
  carry `public` or `s-maxage`; `max-age` alone or no header is not enough — is applied at commit
  (`captureWriter.commit`, `middleware.go:195-213`) and in `validPage` (`:152-158`) ONLY, never in `pageTTL`
  (`middleware_policy.go:14`): the pre-flight at `middleware.go:70` runs on the OUTER headers before the handler, where
  no `Cache-Control` is normal, and must keep meaning "outer headers don't forbid" or `Pages` bypasses every route.
  Flip: header-less or `max-age`-only HTML is no longer stored. Hatch: `AllowImplicitPublic: true` (cms sets it, PR-3). FSK-9.

**authentication (`pockets/authentication`) — PR-2**

- `no-store` on the registrar: `mount()` (`inbound/http/routes.go:217-222`) wraps `r` in a second unexported
  decorator beside `clientInfoRegistrar` that prepends `web.NoStore()` (`sdk/pkg/web/middleware.go:268`) to every
  `Handle`. HTML pages, OAuth2 AS (keeps `Pragma`) and every JSON arm are covered; the pocket mounts no assets.
  README `:354-361` and the `/auth/password/change` route-table line are corrected. FHS-3.
- `PublicRouteLimits` (new exported struct, package `authentication`): `Register, Verify, PasswordReset, VerifyLink,
  OAuthNative, Refresh, InvitationDecline ratelimiter.Limit` — the vocabulary `AuthenticationLimits` already uses
  (`logic/authentication/abuse.go:12-24`); zero → default; `Validate()` runs `Limit.Normalize` on each (no
  separate negative rule; no off switch — a host behind its own edge sets a large number). Field
  `AbuseProtectionConfig.PublicRouteLimits`, carried by `WithAbuseProtection`. Defaults and keys (`prefix:ip`
  through `clientIPFor`, TrustProxies-resolved); this table goes into the README verbatim (route → key → default →
  knob), with the fixed passwordless and resend budgets appended:

  | Route | Key | Default /min/IP | Knob |
  |---|---|---|---|
  | `POST /auth/register` | `register` | 10 | `PublicRouteLimits.Register` |
  | `POST /auth/verify` | `verify` | 20 | `.Verify` |
  | `POST /auth/password/forgot` | logic `password_reset` PerIP | 20 (3/subject) | `AuthenticationLimits.PasswordReset` — already per-IP; NO edge bucket |
  | `POST /auth/password/reset` | `password_reset` | 20 | `.PasswordReset` (the token-consume step, unbudgeted today) |
  | `POST /auth/token` | logic `login` PerIP | 30 (5/subject) | `AuthenticationLimits.Login` — already per-IP; NO edge bucket |
  | `POST /auth/refresh` | `refresh` | 30 | `.Refresh` — the IP arm ONLY (`routes.go:24`); the per-session 30 (`service.go:76`) stays a constant |
  | `POST /auth/oauth/verify-link`, `/auth/oauth/native/verify-link` | `verify_link` | 20 | `.VerifyLink` |
  | `POST /auth/oauth/{p}/native/{start,complete,link/start}` | `oauth_native` | 40 (shared key ≈ 13 sign-ins) | `.OAuthNative` |
  | invitation decline | `invitation_decline` | 10 | `.InvitationDecline` |

  `RateLimitByIP(key, perMinute int)` stays as the host-route surface (README `:1396`); the mounts call an unexported
  `Limit`-taking sibling. FHS-4 (throttle). New 429s are the intent.
- Origin gate on the JSON arm: the JSON handlers of register/verify/forgot/reset/token/verify-link run
  `browserOriginAllowed` (`security.go:216-226`) and answer the existing `forbidOrigin` 403 when a browser-borne
  request fails the allowlist/`Sec-Fetch-Site` policy; a non-browser client that sends neither header passes, as
  the form arm's `formOriginOK` does (`forms.go:85-92`). No new symbol. Flip: a same-site or cross-origin SPA calling
  these routes without an allowlisted origin now gets 403 (today only login is gated, `routes.go:236`). Hatch:
  `BrowserConfig.AllowedOrigins` / `OriginPolicy`. FHS-4 (origin).
- `AbuseProtectionConfig.PublicRouteLimiterFailOpen bool` (new; zero value fails CLOSED **in every mode** — a
  deliberate departure from FHS-6's "true in development": a dev host sees the same 503 production will; the memory
  limiter's `ErrCapacity` at 10,000 keys is the only dev-time trigger). Both arms fail closed: the HTTP arm
  (`RateLimitByIP`, `public_rate_limit.go:17-28`, today `FailOpen: true`) through `ratelimiter.Middleware`; the
  logic refresh per-session arm (`logic/authentication/refresh.go:64-72`, today WARN-and-admit) returns the limiter
  error wrapped `%w sdk.ErrUnavailable`, which `sessions.go:368` maps to 503 — both carry `Retry-After: 5` (PR-1).
  Plumbing: a named bool `LimiterFailOpen` on the logic-side option struct the constructor fills from
  `AbuseProtectionConfig` (the `constructorConfig` path `WithAbuseProtection` writes, `config.go:238`), NOT on
  `AuthenticationLimits` (a budget type); the HTTP adapter gets the same bool at construction. In production a true
  literal is a posture waiver: one `posture_waiver=PublicRouteLimiterFailOpen` WARN at `New`; P5 lists it (PR-6).
  **The production flip, plainly:** a Redis or Postgres limiter outage now 503s register, verify, reset, verify-link,
  native OAuth, decline, refresh (silent refresh fails → SPA users are signed out) AND every host route behind
  `RateLimitByIP`. Hatch: the flag, or a highly-available limiter. README outage policy rewritten (`:4254-4261`). FHS-6, FAC-16.
- `proxyPosture` (`inbound/http/routes.go:495-509`) gains a case: `X-Real-IP != "" && X-Forwarded-For == "" && ip ==
  peer` → one WARN naming `web.TrustProxiesConfig.TrustRealIPHeader` (the X-Real-IP flip plus the new budgets would
  otherwise key a whole site to the proxy's IP with no signal). Additive. FHS-9.
- Cookie `Domain` guard (`configuration.go:51-53`, beside the Secure check): production refuses
  `SessionCookie.Domain != ""` with `ErrCookieDomainRefused` unless `BrowserConfig.CookieDomainAcknowledged` (new
  bool, no env tag, P5-listed, logged `posture_waiver=CookieDomainAcknowledged`); in EVERY mode a non-empty `Domain`
  beside `OriginPolicy.HasNamespace()` is `ErrCookieDomainWithNamespaceRule`, no waiver — the sdk's own trust text
  says "keep session cookies host-only (no Domain attribute)" under a namespace (`sdk/pkg/web/origin_policy.go:55-62`),
  the `cms.ErrAdminAcknowledgedWithMiddleware` precedent (`cms.go:45-49`). Flip: a production host with a `Domain`
  fails `New` until it acknowledges. FHS-12 (R-3).

**cms (`pockets/cms`) — PR-3**

- `Config.MaxUploadBytes int64` (0 → 32 MiB, today's constant `inbound/cms/media.go:13`; negative →
  `sdk.ErrInvalidInput` at `New`): `Upload` (`media.go:49`) sets `r.Body = http.MaxBytesReader(w, r.Body, n)` before
  `ParseMultipartForm`, whose in-memory limit stays the FIXED 32 MiB constant (a 1 GiB cap must not buffer 1 GiB).
  Oversize → the library re-renders "upload too large" with status **413** (`renderLibraryError` gains a status
  parameter; today it always answers 400, `media.go:107`). FOP-5.
- `Config.AllowedMediaTypes []string` (nil → `DefaultAllowedMediaTypes()`, a func returning a COPY of `image/png,
  image/jpeg, image/gif, image/webp, application/pdf`; SVG absent by design). The inbound sniffs the first 512
  bytes (`http.DetectContentType`), parses the result with `mime.ParseMediaType`, and passes the PARSED sniffed
  type to `Upload`; the client's `Content-Type` is never consulted — the sniff decides. `mediasvc.MediaService`
  gains the allow-list through a struct-input setter or option resolved in `cms.go:176` (NO fifth positional
  argument on `NewService`, which is already positional with a nil clock); `Upload` rejects a type outside the
  list with `sdk.ErrInvalidInput` whose message names the allowed types ("unsupported file type; allowed: image/png,
  …") → 400 in the library. Flip: non-listed uploads refused. Hatch: `AllowedMediaTypes`. FOP-4.
- `Serve` (`media.go:73-86`): adds `X-Content-Type-Options: nosniff` and `Content-Disposition` via `mime.FormatMediaType`:
  `inline; filename="<sanitized>"` for png/jpeg/gif/webp, `attachment; filename=…` for everything else (pdf per R-6's
  default, host-added types, pre-existing assets of another type — served, never refused). `Cache-Control: public, max-age=3600` stays. FOP-4.
- Page cache: `inbound/cms/routes.go:82` passes `pageCache` with `AllowImplicitPublic: true` to `cacher.Pages` —
  the public routes are public by construction — and the handlers emit NO `Cache-Control` directive, so nothing
  changes on the wire and no CDN starts storing pages the host's content-event invalidation cannot purge. FSK-9 (R-5).

**pgxdb (`integrations/datastores/pgxdb`) — PR-4**

- `Config.StatementTimeout`, `Config.LockTimeout time.Duration` (`env:"DB_STATEMENT_TIMEOUT"`, `env:"DB_LOCK_TIMEOUT"`;
  zero → unset, server default; negative, or positive under 1ms (would send `"0"` = disabled) → `sdk.ErrInvalidInput`).
  `poolConfig` applies them as `RuntimeParams["statement_timeout"|"lock_timeout"]` in milliseconds. If the DSN or its
  `options` ALSO names the parameter (the `hasSessionTimeZone` fold, `postgres.go:266-282`, generalised) → `ErrInvalidInput`
  at `Open`: an explicit knob is never silently overridden. README: server process only (a migrator leaves them zero —
  index builds); PgBouncer transaction mode rejects unknown startup parameters → pooler hosts use `ALTER ROLE … SET`. FAS-10. Additive.

**go-oidc (`integrations/cryptids/go-oidc`) — PR-5**

- `validateIssuer` (`gooidc.go:267-276`) mirrors `validateJWKSURL` (`:280-294`: https, or http for loopback) ONLY
  when `jwksURL == ""`, i.e. when discovery will fetch (`:157-163`); a pinned `WithJWKSURL` makes the issuer a string
  compared to `iss` — no plaintext fetch, so https-only must not refuse it. New option `WithInsecureIssuer()`
  (`options.go`, this constructor's shape) admits a non-loopback http issuer with one WARN; the pocket's production
  engine still refuses http issuers, so the hatch is dev-only in practice. Flip: a docker-hostname http dev issuer that discovers fails `New` until it opts in. FAF-10.

**workshop (`workshop/gopernicus`) + docs — PR-6**

- P2 (`posture_rules.go:28, 903`) accepts `web.TrustProxiesWithConfig(` beside `web.TrustProxies(`; the sentence
  constant and `examples/README.md:282` change together (byte-match, `TestGuardListMatchesContract`,
  `guard_test.go:42`) — a host-contract amendment.
- P5 `postureAcknowledgements` (`:64-75`) gains `CookieDomainAcknowledged`, `CompromisedPasswordFailOpen` and
  `PublicRouteLimiterFailOpen` (a true literal on either FailOpen prints as a posture waiver); `examples/README.md:334-341`
  lists the same three.
- `templates/init/migrations.main.go.tmpl:119-124` zeroes `cfg.StatementTimeout`/`cfg.LockTimeout` after
  `ParseEnvTags` (`Makefile.tmpl:19` makes `run` depend on `migrate`, so the migrator parses the same env), with a
  scaffold test; `env.example.tmpl` ships `# DB_STATEMENT_TIMEOUT=30s` / `# DB_LOCK_TIMEOUT=5s` COMMENTED OUT with
  "server process only — the migrator zeroes them"; `go.mod.tmpl:5` pins sdk v0.17.0 / pgxdb v0.13.0
  (`scaffold_test.go:49-51`); `readme.md.tmpl` one paragraph. P7 NOT extended (a new required key breaks downstream CI).
- Docs: `workshop/documentation/docs/guides/production-posture.md` "Refused at construction in production" (`:15`)
  gains the two authentication rows and the two waivers; a "Transport and abuse (S5)" subsection carries the Upgrade
  notes table.

## Upgrade notes

Eighteen rows: 10 behaviour flips with a hatch, 5 observable changes without a knob (rows 2, 3, 12, 15, 16), 3 purely
additive (rows 4, 7, 18). Three fail boot (11, 12 in any mode, 16 only with a double setting, 17 in dev). PR-7 copies
this table verbatim into `RELEASING.md`.

| # | Flip | What the host sees | Hatch | Module@version |
|---|---|---|---|---|
| 1 | `DecodeJSON` bounded at 1 MiB | bodies > 1 MiB 413; an outer larger `MaxBytesReader` no longer governs | `DecodeJSONLimit[T](r, n)` | sdk@v0.17.0 |
| 2 | fixed decode-error text | 400 carries `ReadBody`'s sentences, never Go type names | none (match on `code`, not text) | sdk@v0.17.0 |
| 3 | `Panics` answers JSON to JSON clients | 500 body is JSON when `Accept`/`Content-Type` is JSON | none | sdk@v0.17.0 |
| 4 | static `nosniff`; `X-Request-ID` validated; unparsable forwarded hop → peer; 503 `Retry-After: 5` | additive | none | sdk@v0.17.0 |
| 5 | `X-Real-IP` ignored by default | a proxy setting only `X-Real-IP` attributes the peer; auth WARN names the knob | `TrustProxiesWithConfig(TrustProxiesConfig{…, TrustRealIPHeader: true})` | sdk@v0.17.0 |
| 6 | page cache stores only `public`/`s-maxage` | header-less or `max-age`-only HTML no longer cached | `PageConfig.AllowImplicitPublic: true` (cms sets it itself) | sdk@v0.17.0 |
| 7 | `no-store` on every `/auth` response; `X-Real-IP` posture WARN | additive | none | authentication@v0.24.0 |
| 8 | per-route IP budgets | new 429s on register/verify/reset/verify-link/native OAuth | `AbuseProtectionConfig.PublicRouteLimits.<Route>` | authentication@v0.24.0 |
| 9 | origin gate on the JSON arm | a same-site/cross-origin SPA calling register/verify/reset/token/verify-link without an allowlisted origin gets 403 | `BrowserConfig.AllowedOrigins` / `OriginPolicy` | authentication@v0.24.0 |
| 10 | limiters fail closed in every mode | a limiter outage 503s the budgeted routes incl. silent refresh and host `RateLimitByIP` routes | `PublicRouteLimiterFailOpen: true` (production: posture waiver WARN) | authentication@v0.24.0 |
| 11 | cookie `Domain` refused in production | `New` fails `ErrCookieDomainRefused` | `BrowserConfig.CookieDomainAcknowledged` | authentication@v0.24.0 |
| 12 | cookie `Domain` + namespace rule refused in every mode | `New` fails `ErrCookieDomainWithNamespaceRule` | none — remove one (R-3) | authentication@v0.24.0 |
| 13 | upload allow-list by sniffed type | non-listed uploads 400 naming the allowed types | `Config.AllowedMediaTypes` | cms@v0.6.0 |
| 14 | upload cap | > 32 MiB 413 | `Config.MaxUploadBytes` | cms@v0.6.0 |
| 15 | media `nosniff` + `Content-Disposition`; PDFs as `attachment` | visitors download PDFs instead of viewing inline | none (R-6 may pick inline + `CSP: sandbox`) | cms@v0.6.0 |
| 16 | timeout knobs; DSN-and-Config double setting refused | `Open` fails `ErrInvalidInput` only when both name the parameter | set one | pgxdb@v0.13.0 |
| 17 | https-only issuer when discovering | non-loopback http dev issuer fails `New` | `WithInsecureIssuer()` (dev only) | go-oidc@v0.2.0 |
| 18 | P2 second name; P5 three names; scaffold migrator zeroes timeouts | additive / informational | none | workshop@v0.8.0 |

## Risks

- The new 429s trip any auth-pocket or example fixture that posts register/verify past the budget per minute from one IP — fixtures raise `PublicRouteLimits`, never disable the mechanism.
- The explicit-public rule lands at commit/`validPage` only; a slip into `pageTTL`'s pre-flight silently bypasses every cached route — the stored/not-stored test pair in sdk AND cms's `page_cache_test.go` are the proof.
- Fail-closed in production turns a limiter outage into sign-outs (refresh 503) and host-route 503s — the Upgrade notes row and the README outage section are the only warning a host gets.
- Residual migrator hazard: a host whose own migrator parses `pgxdb.Config` from the server env and does NOT zero the timeouts inherits a 30s statement timeout on index builds — the scaffold and baseline zero it; the README sentence is the only guard for hand-rolled migrators.

## Tasks as PRs

Branch names in parentheses. Every PR: `make check` green in CI, squash-merge, annotated tag at the squash commit,
cold-verify from a scratch consumer (`GOWORK=off`, empty module cache, `go mod verify`, `go build <module>/...`). Never
`go work sync`; never `go mod tidy` a dependant before the proxy serves the tag (poll the `.info` URL first).

### PR-1 — sdk: bounded decode, fixed decode text, JSON panics, 503 `Retry-After`, static nosniff, request-id and forwarded-IP validation, explicit-public page cache (`sdk/transport-hardening`)
- **files:** `sdk/pkg/web/request.go`, `errors.go`, `respond.go` (or wherever `RespondJSONError` lives), `middleware.go`,
  `static.go`, `trustproxies.go`, `origin_policy.go` (+ `_test.go` siblings; `trustproxies_test.go` gains
  IPv6/ported/garbage/`X-Real-IP`-with-and-without-XFF/count-0 cases), `sdk/capabilities/cacher/middleware.go`,
  `middleware_policy.go` (+ tests: stored with `public`, with `s-maxage`, NOT with `max-age` alone or no header,
  stored with `AllowImplicitPublic`; pre-flight unchanged), `sdk/capabilities/ratelimiter/middleware_test.go`
  (503 carries `Retry-After`), `sdk/README.md`.
- **BREAKING for hosts:** Upgrade notes rows 1–6.
- **verify:** `cd sdk && go build ./... && go test ./... && go vet ./...`; `make guard` (G12 sdk layering — `web`
  stays stdlib, `net/netip` + `regexp` only; `cacher` imports no sibling capability). Then `make check`.
- **tag:** `sdk/v0.17.0` (MINOR: new symbols).

### PR-2 — authentication: registrar-wide `no-store`, per-route IP budgets, JSON-arm origin gate, fail-closed limiters, X-Real-IP posture WARN, cookie-Domain guard (`authentication/edge-hardening`)
- **depends_on:** PR-1 tagged (`OriginPolicy.HasNamespace`, 503 `Retry-After`).
- **files:** `pockets/authentication/config.go` (`PublicRouteLimits`, `PublicRouteLimiterFailOpen`,
  `CookieDomainAcknowledged`, option plumbing), `configuration.go`, `configuration_errors.go` (two sentinels),
  `constructor.go` (waiver WARNs), `inbound/http/routes.go` (decorator, budgets, `proxyPosture` case), `oauth.go`,
  `invitation.go`, `public_rate_limit.go` (`Limit`-taking sibling, fail-open bool), `sessions.go` + the JSON arms in
  `oauth.go` (origin gate), `logic/authentication/refresh.go` (`%w sdk.ErrUnavailable`), the logic option struct
  (`LimiterFailOpen`), `README.md` (`:206-213` budgets, `:354-361` no-store, the route table, `:1396` host routes,
  `:4254-4261` outage policy rewritten to fail-closed + the per-IP budget table), `go.mod` (sdk v0.17.0), tests
  beside each (fixtures that hammer register/verify raise `PublicRouteLimits`).
- **BREAKING for hosts:** Upgrade notes rows 8–12; row 9 names the SPA 403 with its hatch.
- **verify:** `cd pockets/authentication && go build ./... && go test ./... && go vet ./...`; `make guard`; `make check`.
  Then, on merged `main` BEFORE the tag (R-1 — the standing rule since the v0.19.0 regression: store legs run
  whenever a pocket's HTTP or authentication layer changes; `stores/pgx/identifier_http_test.go:116` mounts the real
  pocket and drives `/auth/*`): `cd pockets/authentication/stores/pgx && POSTGRES_TEST_DSN=… go test -count=1 ./...`
  bare, again with `POSTGRES_TEST_SCHEMA=…`, and `cd ../turso && TURSO_DATABASE_URL=… TURSO_AUTH_TOKEN=… go test
  -count=1 ./...` (the playground URL only), each counted with `go test -json`.
- **tag:** `pockets/authentication/v0.24.0` (MINOR).

### PR-3 — cms: upload cap, allow-list + sniff, serve headers, page-cache opt-in (`cms/upload-hardening`)
- **depends_on:** PR-1 tagged (the opt-in field exists only under sdk v0.17.0; cms pins it).
- **files:** `pockets/cms/cms.go` (`MaxUploadBytes`, `AllowedMediaTypes`, `DefaultAllowedMediaTypes()`, validation,
  allow-list resolved at `:176`), `internal/logic/mediasvc/service.go` (+ test: listed type stored, unlisted type →
  `ErrInvalidInput` naming the list), `internal/inbound/cms/media.go` (+ `media_test.go`: oversize → 413 with the
  library rendered, sniffed type not allowed → 400, pdf served `attachment`, png `inline`, `nosniff` present,
  `Content-Disposition` via `mime.FormatMediaType`), `internal/inbound/cms/routes.go` (`AllowImplicitPublic: true`,
  + `page_cache_test.go`: still stored, no `Cache-Control` on the wire), `pockets/cms/README.md`, `go.mod`
  (sdk v0.17.0). No `.templ` change (`MediaLibrary(assets, formError)` already carries any message).
- **BREAKING for hosts:** Upgrade notes rows 13–15.
- **verify:** `cd pockets/cms && go build ./... && go test ./... && go vet ./...`; `make check`; run-and-look §3. Then, on
  merged `main` BEFORE the tag (R-1): `cd pockets/cms/stores/pgx && POSTGRES_TEST_DSN=… go test -count=1 ./...` and
  `cd ../turso && TURSO_DATABASE_URL=… TURSO_AUTH_TOKEN=… go test -count=1 ./...`, counted.
- **tag:** `pockets/cms/v0.6.0` (MINOR).

### PR-4 — pgxdb: statement/lock timeout knobs (`integrations/pgxdb-timeouts`)
- **files:** `integrations/datastores/pgxdb/postgres.go` (+ `postgres_test.go`: params applied in ms, zero unset,
  negative refused, sub-ms refused, DSN-and-Config double setting refused at `Open`, DSN-only passes untouched),
  `README.md` (knobs, server-only, PgBouncer `ALTER ROLE … SET` note); `integrations/datastores/turso/README.md` (one
  sentence: no statement timeout exists; `BusyTimeout`, `:122-126`). NO `tx.go`/`transact.go` change (R-2).
- **BREAKING for hosts:** Upgrade notes row 16 only.
- **verify:** `cd integrations/datastores/pgxdb && go build ./... && go test ./... && go vet ./...`; the pgxdb live leg
  (`POSTGRES_TEST_DSN=… go test -count=1 ./...`, which also proves `SHOW statement_timeout` = the knob and `pg_sleep`
  past it → SQLSTATE 57014 — the timeout proof lives HERE, not in an example); `make check`. Then, on merged `main`
  BEFORE the tag: the SIX pgx store legs — `pockets/{authentication,authorization,cms,events,jobs,secrets}/stores/pgx`
  (events' `outbox.go:85` uses `InTx`) — against the workspace pgxdb on a fresh scratch Postgres 17, counted.
- **tag:** `integrations/datastores/pgxdb/v0.13.0` (MINOR).

### PR-5 — go-oidc: https-only issuer when discovering, loopback exception, `WithInsecureIssuer` (`integrations/gooidc-https-issuer`)
- **files:** `integrations/cryptids/go-oidc/gooidc.go`, `options.go`, `gooidc_test.go` (http non-loopback + discovery
  refused; loopback admitted; pinned `WithJWKSURL` + http issuer admitted; option admits with WARN), `README.md`.
- **BREAKING for hosts:** Upgrade notes row 17.
- **verify:** `cd integrations/cryptids/go-oidc && go build ./... && go test ./... && go vet ./...`;
  `cd examples/external-identity && go test ./...` (dev issuer stays loopback); `make check`.
- **tag:** `integrations/cryptids/go-oidc/v0.2.0` (MINOR).

### PR-6 — workshop + host contract + docs: P2 second name, P5 three names, scaffold migrator zeroes timeouts, env + pins, the guide (`workshop/s5-posture`)
- **depends_on:** PR-1 and PR-4 tagged (scaffold pins).
- **files:** `workshop/gopernicus/internal/commands/posture_rules.go` (+ `guard_test.go`, `scaffold_test.go`: the
  rendered migrator zeroes both fields; pins), `templates/init/migrations.main.go.tmpl`, `env.example.tmpl`,
  `go.mod.tmpl`, `readme.md.tmpl`, `version.go`, `workshop/gopernicus/README.md`, **`examples/README.md`** (`:282` P2
  sentence byte-match; `:334-341` ack list — host-contract amendment),
  `workshop/documentation/docs/guides/production-posture.md`.
- **BREAKING for hosts:** no (Upgrade notes row 18).
- **verify:** `cd workshop/gopernicus && go build ./... && go test ./... && go vet ./...`; `make guard-host-contract
  guard-host-posture` (G30/G32 over the seven examples); a cold `gopernicus init --db pgx` from the tag builds, its
  `guard --posture` is clean, and its rendered `workshop/migrations/main.go` zeroes the two fields; `make check`.
- **tag:** `workshop/gopernicus/v0.8.0` (MINOR: rule text changes).

### PR-7 — record: `RELEASING.md` section, `SECURITY.md` rows, examples repinned, baseline migrator, control-map, executed copy (`docs/s5-release-record`)
- **depends_on:** every tag above cold-verified.
- **files:** `RELEASING.md` (ONE new top section "Transport and abuse hardening (SOC 2 slice S5)": the tag table, cold
  verification, live-leg counts, the Upgrade notes table verbatim, the X-Real-IP flip and the posture WARN presented
  together, the new env keys, the "Not tagged" list, a C1 line saying `QuerierFrom` is NOT in S5), `SECURITY.md`
  (supported-versions rows), `examples/*/go.mod` + `go.sum` (seven examples to the S5 tags; wiring unchanged —
  defaults are the safe ones), `examples/production-baseline/workshop/migrations/main.go:104-111` (zero both timeout
  fields after `ParseEnvTags`) + `.env.example:36-45` (`DB_STATEMENT_TIMEOUT=30s`, `DB_LOCK_TIMEOUT=5s` with the
  server-only comment), `.claude/plans/soc2-readiness/control-map.md` (rows `:64,66,68-71,81,83,143` → shipped S5;
  `:155-157` UNCHANGED), `roadmap.md` (an "S5 RELEASED" paragraph; the `:316-322` carry-over stays open),
  `plans/s5-transport-abuse-hardening.md` (executed copy with "Build-time deviations"). No manifest, no handoff file.
- **verify:** `make check`; `make guard-host-posture`; Verification §1–§5 on the repinned examples.
- **tag:** none.

## Sequencing

```
PR-1 sdk ──tag──▶ PR-2 authentication ──auth legs──tag──┐
          └─────▶ PR-3 cms ─────────────cms legs──tag──┤
PR-4 pgxdb ──six pgx legs──tag──┐                       ├──▶ PR-7 record
PR-5 go-oidc ──tag──────────────┤                       │
                                └──▶ PR-6 workshop ──tag┘
```

PR-1, PR-4 and PR-5 open in parallel on day one. PR-2 and PR-3 branch from `main` after PR-1 merges and pin `sdk v0.17.0`
once the proxy serves it. PR-6 waits for the sdk and pgxdb tags. PR-7 last. Each PR is one `git reset`-reversible unit.

## Release

| Step | Tag | After | Cold verification |
|---|---|---|---|
| 1 | `sdk/v0.17.0` | PR-1 merge | scratch consumer builds `sdk/...` |
| 2 | `integrations/datastores/pgxdb/v0.13.0` | PR-4 merge + the SIX pgx store legs on merged `main` | builds pgxdb AND the MVS set: pgxdb v0.13.0 beside authentication `stores/pgx` v0.11.0, authorization v0.21.0, cms v0.5.0, events v0.4.0, jobs v0.7.0, secrets v0.2.1 — `go.mod` selects v0.13.0, `go mod verify`, `go build` |
| 3 | `integrations/cryptids/go-oidc/v0.2.0` | PR-5 merge | builds |
| 4 | `pockets/authentication/v0.24.0` | PR-2 merge + auth pgx bare + pgx schema + turso legs on merged `main` | builds; resolves beside `stores/pgx` v0.11.0 / `stores/turso` v0.10.0 (core pin v0.23.0 lifted by MVS) |
| 5 | `pockets/cms/v0.6.0` | PR-3 merge + cms pgx + turso legs on merged `main` | builds; beside `stores/pgx` v0.5.0 / `stores/turso` v0.4.0 |
| 6 | `workshop/gopernicus/v0.8.0` | PR-6 merge | cold `init` from the tag builds; `guard --posture` clean |

Steps 1–3 independent, cut as ready; 4 and 5 after 1; 6 after 1 and 2. **Not tagged:** every store module (no store
code — R-1; MVS lifts their core and pgxdb pins inside a host), `turso` (README only), authorization, jobs, events,
secrets, `cms/views/goth`, `authentication/views/goth`, `pockets`. Nothing retracts. Tags annotated, not signed; one
push / tag / merge per command (classifier rule). **Live legs (mandatory, R-1):** pgxdb's own leg in PR-4's CI; the
six pgx store legs before step 2; auth pgx bare + `POSTGRES_TEST_SCHEMA` + turso before step 4; cms pgx + turso
before step 5 — all on merged `main`, fresh scratch Postgres 17 / the truncate-safe turso playground, counted with
`go test -json` (S3's counted gate from `RELEASING.md:60`), pass/fail/skip recorded in PR-7.

## Verification

Run-and-look on real binaries after PR-7's repins (examples build from tags: once with `GOWORK=off`, then normally). `$B` =
the example's origin. production-baseline mounts NO password flows (`PasswordFlowsDisabled: true`, `main.go:349`) → auth checks run on auth-cms.

1. **auth-cms (`examples/auth-cms`, development mode; its `AbuseProtectionConfig` is zero → `ratelimiter.Memory`):**
   - `curl -si -X POST $B/auth/login -H 'Content-Type: application/json' -d '{}'` → `Cache-Control: no-store` on the
     400/401; `curl -si $B/auth/login` (GET HTML) → `no-store` too.
   - `for i in $(seq 1 12); do curl -s -o /dev/null -w '%{http_code}\n' -X POST $B/auth/register -H 'Content-Type:
     application/json' -d '{"email":"a@b.c","password":"x"}'; done` → the 11th and 12th are `429` with `Retry-After`.
   - `curl -si -X POST $B/auth/register -H 'Origin: https://evil.example' -H 'Sec-Fetch-Site: cross-site' -H
     'Content-Type: application/json' -d '{}'` → `403` (JSON forbid-origin); the same POST without `Origin`/
     `Sec-Fetch-Site` → 400, not 403.
   - Saturation (fail closed): with `TRUSTED_PROXY_COUNT=1`, loop 10,001 POSTs to `/auth/refresh`, each with a distinct
     `X-Forwarded-For: 10.<a>.<b>.<c>` → the memory limiter hits `ErrCapacity` and `/auth/refresh` answers `503` +
     `Retry-After: 5`; `PublicRouteLimiterFailOpen: true` locally → admits with one WARN per failure (production: a boot `posture_waiver` line).
2. **production-baseline (`examples/production-baseline`, `make run`, production mode):**
   - `curl -si $B/healthz -H 'X-Request-ID: '$(python3 -c 'print("A"*300)')` → the echoed id is a fresh ≤128-char id.
   - With `TRUSTED_PROXY_COUNT=1`: `-H 'X-Forwarded-For: not-an-ip'` → access-log `client_ip` is the peer (127.0.0.1);
     `-H 'X-Real-IP: 9.9.9.9'` alone → still the peer AND one WARN naming `TrustRealIPHeader` in the log.
   - Boot with `AUTH_COOKIE_DOMAIN=example.com` → `New` fails naming `ErrCookieDomainRefused` and
     `CookieDomainAcknowledged`; `gopernicus guard --posture` on a host setting the acknowledgement prints one `ack`.
   - `make migrate` with `DB_STATEMENT_TIMEOUT=30s` → the migrator applies no timeout (zeroed). The timeout itself is proven by the pgxdb live leg (PR-4) — no baseline route drives `pg_sleep`.
3. **cms (`examples/cms`, development mode — its admin is ungated, `cmd/server/main.go:180`):** upload `evil.html`
   renamed `.png` with `Content-Type: image/png` → library shows "unsupported file type; allowed: …" (sniff decided);
   upload a real PNG → `GET /media/{id}/file` carries `Content-Type: image/png`, `X-Content-Type-Options: nosniff`,
   `Content-Disposition: inline; filename="…"`; upload a PDF → `attachment`; `curl -s -o /dev/null -w '%{http_code}'
   -F file=@40MiB.bin $B/media` → `413` and the library renders "upload too large". `curl -si $B/` twice → NO
   `Cache-Control` on the wire, first `X-Cache: MISS`, second `X-Cache: HIT`; set `AllowImplicitPublic` false in a
   local build → `MISS` both times.
4. **minimal (`examples/minimal`) + a scratch host:** `curl -si $B/assets/app.js` → `X-Content-Type-Options: nosniff`.
   No example route calls `web.DecodeJSON` (grep of `examples/*`), so a ten-line scratchpad `main` mounts one
   `web.DecodeJSON[struct{X int}]` route on `web.NewWebHandler` with `web.Panics`: `-d '{"x":'` → `400` "request body
   must be a JSON object" (no Go type names); a 2,000,000-byte body → `413`; a panicking route with `-H 'Accept:
   application/json'` → JSON 500, without → HTML 500.
5. **external-identity (`examples/external-identity`):** `go test ./...` green (loopback dev issuer); a local edit to
   `http://keycloak.local` with discovery → `New` fails until `WithInsecureIssuer()`; with `WithJWKSURL` pinned → passes.

### Build-time deviations (lead rulings during execution; PRs #175–#180)

1. PR-5 go-oidc (#175): with `WithInsecureIssuer()` the DISCOVERED `jwks_uri` may also be plain http (a container-hostname issuer returns one on the same host; the literal reading would make the hatch a no-op for its dev case); a pinned `WithJWKSURL` is never waived; `ftp://`/relative issuers still refused; the WARN fires only when the hatch is actually used (http, non-loopback, discovering). Upgrade-notes ROW 17 must be reworded: in v0.1.0 a container-hostname http issuer whose `jwks_uri` was on the same host already failed `New` at the key-set check; the new refusal is earlier and clearer and newly refuses only the rare http issuer whose discovered key set is https or loopback. No new error variable (wraps `sdk.ErrInvalidInput`).

2. PR-4 pgxdb (#176): a separate exact-name parser for the DSN/options/PGOPTIONS double-setting check instead of generalising `hasSessionTimeZone` (plan :204 — a substring match would refuse `deadlock_timeout`); PGOPTIONS and service-file entries count as 'the DSN' (pgconn folds them into one map); no upper-bound check (the server refuses out-of-range at the ping); proven live that a startup parameter silently wins over a DSN option, which is why the refusal exists. FOLLOW-UP (not fixed): `hasSessionTimeZone`'s substring check also matches `-c log_timezone=…`.

3. PR-1 sdk (#177): THREE files outside sdk/ ride the PR because the workspace CI gate runs every module against the local sdk — `pockets/cms/internal/inbound/cms/routes.go` sets `AllowImplicitPublic = true` on cms's own Pages mount (PR-3's line, moved earlier; cms keeps pinning sdk v0.16.0 until PR-3 repins — the S3 #170→#171 window), and two pocket tests (`authentication/inbound/http/audit_correlation_test.go`, `authorization/decision_observer_test.go`) now expect the edge to replace a 200-byte inbound request id; PR-3 drops the moved line from its scope. `TrustProxies(n)` is the count-only form so `X-Real-IP` is ignored by default (plan Design + row 5; PR-2's WARN covers X-Real-IP-only proxies). `ErrFromDomain` still answers the generic 400 (the fixed sentence reaches clients via `ErrValidation`); sentence set and `Panics` negotiation details per the PR body; existing cache tests that stored without a directive now set `AllowImplicitPublic`. DOCS FOLLOW-UP for PR-6: `docs/sdk/web.md:86-95` ('no implicit global limit') now wrong; `docs/sdk/capabilities.md:90-104` lacks the explicit-public rule.

4. PR-3 cms (#178): docs live in `workshop/documentation/docs/pockets/cms.md` (the plan's `pockets/cms/README.md` :293 does not exist); empty non-nil `AllowedMediaTypes` gets the default too (plan :177); allow-list entries parsed at install, unparsable → ErrInvalidInput; a service with no policy refuses every upload (Register always installs one); 'upload too large' (413, incl. multipart.ErrMessageTooLarge) vs 'upload malformed' (400); sniff read error → 500; `mime.FormatMediaType` leaves plain-token filenames unquoted (plan :186 showed quotes); the cap default resolved in the inbound beside the existing constant and passed via `Deps` (no new positional arg); no env tags on the two fields. Pre-existing follow-up: `svc.Upload` errors still rendered raw at 400. The AllowImplicitPublic line landed in PR-1 (#177).
5. PR-6 workshop (#179): the P5 sentence amended — a `true` FailOpen prints `posture_waiver` (runtime log token; `posture_waiver (conditional)` under if/switch) not `ack`, byte-matched in `examples/README.md` with an amendment note (plan :207); the guide carries prose bullets + an 'Added by S5' refusal table and points to RELEASING for the Upgrade notes table (plan :211-212); guide rows for authentication v0.24.0 / cms v0.6.0 written ahead of those merges; `version.go` (plan :327) unchanged (prints the 0.0.0-dev placeholder); two edits beyond the list (`examples/README.md` review-evidence sentence; `host-contract.md` P2 summary). LEFT FOR PR-7: `readme.md.tmpl:143` 'one ack line per waiver' + the scaffold README `@v0.6.0 guard --posture` pin (→ v0.8.0).

6. PR-2 authentication (#180): the logic option struct is `authlogic.AbuseProtectionConfig{LimiterFailOpen}` (none existed; plan :141); `PublicRouteLimits` defined in inbound and aliased at root (root and logic are both package `authentication`; plan :117); native verify-link shares the `verify_link` budget but is NOT origin-gated (native routes stay ungated); waiver WARNs production-only; X-Real-IP WARN has its own once-guard; README budget table merges `/auth/login` with `/auth/token` (same logic budget), drops stale file:line refs, adds the fixed passwordless/resend rows; refresh arm wraps both `sdk.ErrUnavailable` and the limiter error (a pathological limiter wrapping ErrInvalidInput would map to 400 — left). PLAN GAP for the record: Verification §2 'boot production-baseline with AUTH_COOKIE_DOMAIN' cannot run as written (hardcoded `Secure: true`, no such env var, production boot needs TLS Postgres + https issuer) — proven in-process over the real production composition; the auth-cms `PublicRouteLimiterFailOpen: true` local check has no env knob — covered by unit tests. No env tags on the new fields (pinned by a test).

## Open rulings — YOUR CALL

- **R-1 Store repins.** No store module's code changes. Recommendation (lead, both reviewers): tag NO store module,
  run no repin PR — BUT the live legs are MANDATORY: auth pgx bare + schema + turso before authentication v0.24.0, cms
  pgx + turso before cms v0.6.0, the six pgx store legs before pgxdb v0.13.0, all on merged `main` (written into
  Release and the PR verify lines). Alternative: one repin PR, eight more tags for no behaviour.
- **R-2 `QuerierFrom` carry-over.** Recommendation (lead, backend): DEFER to the per-tenant authorities plan — rev 1's
  fix was partial (authorization stores call `TxFromContext` directly in six sites, Notes; turso's `QuerierFrom`,
  `transact.go:46-51`, untouched), so a correct fix needs both connectors, those sites and a store repin. PR-4 keeps
  only the timeouts; the carry-over stays open; control-map C1 is not claimed; the correct shape is in Notes.
  Alternative (pm's position, overruled): ship the partial pgxdb sentinel in PR-4 now.
- **R-3 Cookie `Domain` beside a namespace rule.** Recommendation (all): refuse in every mode, no waiver
  (`ErrCookieDomainWithNamespaceRule`), citing `sdk/pkg/web/origin_policy.go:55-62`; the pocket documents the two as
  mutually exclusive. Alternative: let `CookieDomainAcknowledged` cover it too.
- **R-4 Default per-route budgets** (per minute per IP), as `ratelimiter.Limit` fields: register 10, verify 20,
  reset 20 (`/auth/password/reset` only — forgot and `/auth/token` are already per-IP in logic and get NO edge
  bucket), verify-link 20, refresh 30 (IP arm only), decline 10, native OAuth 40 (shared key over three routes).
  Recommendation: accept; the README table lists every per-IP budget by route. Alternative: count only native start.
- **R-5 Page cache directive = `public` or `s-maxage`,** applied at commit and `validPage` only; cms sets
  `AllowImplicitPublic: true` on its own mount and emits NO directive (no CDN behaviour change, invalidation intact).
  Recommendation: accept. Alternative: cms emits `public, s-maxage=<TTL>` on the wire plus a CDN note in RELEASING.
- **R-6 PDFs.** Recommendation (lead): serve `application/pdf` as `Content-Disposition: attachment` — simplest and
  safe (no script execution context on the pocket's origin). Alternative: `inline` with `Content-Security-Policy:
  sandbox` on the media route; visitors keep in-browser viewing. Decide before PR-3.

## Decided by gate

- backend#1 (HIGH, legs skipped) → folded: R-1 keeps "no tags" but the auth pgx bare + schema + turso and cms legs are mandatory before each pocket tag (PR-2/PR-3 verify, Release).
- backend#2 (HIGH, R-2 partial; C1 claimed) → folded: `QuerierFrom` deferred, PR-4 timeouts only, C1 row not claimed, correct shape in Notes (plain `errors.New` sentinel → 500, `BeginRead` records its `*DB`).
- backend#3 / pm#2 (HIGH, scaffold migrator inherits timeouts) → folded: PR-6 zeroes both after `ParseEnvTags` + scaffold test, env keys commented out; PR-7 zeroes the baseline migrator and adds the keys to its `.env.example`.
- backend#4 (`pageTTL` pre-flight) → folded: rule at commit and `validPage` only, named in Design.
- backend#5 / pm#6 (503 `Retry-After` false; plumbing) → folded: logic arm wraps `%w sdk.ErrUnavailable`; `RespondJSONError` adds `Retry-After: 5` on 503 in PR-1 (both arms); named bool on a logic option struct, not `AuthenticationLimits`; production flip stated plainly; FHS-6 counter → deferred line.
- backend#6 / pm#5 (budget vocabulary; forgot/token double bucket) → folded: `ratelimiter.Limit` fields, `Normalize` validates; no edge budget on forgot or `/auth/token`; reset bucket on `/auth/password/reset` only; `Refresh` = IP arm only; README per-IP budget table.
- backend#7 (`RuntimeParams` precedence) → folded: DSN-and-Config → `ErrInvalidInput` at `Open`; sub-1ms refused; PgBouncer `ALTER ROLE … SET` note.
- backend#8 (gooidc pinned JWKS) → folded: https-only enforced only when `jwksURL == ""`.
- backend#9 (`ErrMalformedBody`) → folded: no new sentinel, no new wire code; `ReadBody`'s sentences and kind.
- backend#10 (`RealIPHeader` name/precedence) → folded: `TrustRealIPHeader`; consulted only when count ≥ 1 and XFF absent; netip-parsed.
- backend#11 (BREAKING list incomplete) → folded: `RateLimitByIP` host routes named; README outage section in PR-2's files; SPA 403 named with its hatch (row 9).
- backend#12 (P2 byte-match; `examples/README.md` missing) → folded: in PR-6's files as a host-contract amendment.
- backend#13 / pm#9 (cms PR-3 details) → folded: fixed `ParseMultipartForm` memory limit; negative `MaxUploadBytes` → `ErrInvalidInput`; `DefaultAllowedMediaTypes()` returns a copy; parsed media types compared; default resolved in `cms.go`, no fifth positional arg; `mime.FormatMediaType`; message names the allowed types; sniff decides ("mismatched claim" test dropped, restated as "sniffed type not allowed → 400"); oversize → 413 with status carried through the render path.
- backend#14 / pm#7 (`s-maxage` on the wire lets CDNs cache) → folded: cms `AllowImplicitPublic: true` on its own mount, no directive emitted.
- backend#15 / pm#11 (citations, counts, ceremony) → folded: `sdk/pkg/web/origin_policy.go:55-62`; six pgx legs (events); Risks section; house-rules recital cut; naming question closed.
- backend naming + versions notes → folded: keep `PublicRouteLimiterFailOpen` (matches `CompromisedPasswordFailOpen`, `config.go:128-135`, and `ratelimiter.MiddlewareConfig.FailOpen`); P5 lists both FailOpen names; zero value fails closed in every mode as a deliberate departure from FHS-6; all tags MINOR, only the cookie-Domain boot failure needs an acknowledgement.
- backend question (legs waived?) → NO: see R-1.
- pm#1 / pm#10 (verification targets; `X-Cache`; no admin gate) → folded: no-store/429/origin on auth-cms (memory limiter, said so); cookie Domain/proxy/request-id on production-baseline; timeouts via the pgxdb live leg; saturation = 10,001 distinct XFF IPs under `TRUSTED_PROXY_COUNT=1`; `X-Cache: HIT|MISS`; examples/cms "admin signed in" removed.
- pm#3 (X-Real-IP flip silent) → folded: `proxyPosture` case `X-Real-IP present && XFF absent && ip == peer` WARN naming `TrustProxiesConfig.TrustRealIPHeader`; RELEASING presents the two flips together.
- pm#4 (no single flip list; counts disagree) → folded: "Upgrade notes" table, 18 rows, counted honestly; PR-7 copies it verbatim.
- pm#8 (native OAuth shared key) → folded: default 40.
- pm#12 (six trains) → accepted, no change.
- pm questions (fail-closed in every mode intended? IPv6 /64 bucketing?) → yes, stated in Design; one follow-up line in Notes.
- pm question (PDF attachment vs inline sandbox) → new owner ruling R-6.
- pm R-2 position (ride PR-4, RELEASING isolation bullet) → declined: lead R-2 defers on backend#2's analysis; shipping a partial public sentinel ahead of the per-tenant plan's finding 4 would mark C1 closed when it is not.

## Recommended reviews

Post-hoc, after the owner rules R-1…R-6: **product-manager** (the Upgrade notes table as the host's only handoff; R-6
from a visitor's seat), **platform-sre** (the fail-closed production flip; migrator-zeroing in scaffold and baseline;
the six-leg release gate), **architecture-steward** (the logic-side `LimiterFailOpen` bool — no HTTP type crosses into
logic; `cacher` stays sibling-free). No further lead pass unless a ruling changes the design.

## Notes

- **Correct `QuerierFrom` shape, for the per-tenant authorities plan (R-2):** `Tx` records the `*DB` that began it
  (`pgxdb/tx.go:17-20`, set in `Begin`, `BeginRead` `:33-39`, `InTx`, `TransactSnapshot`); an ownership-checked
  `(*DB).TxFrom(ctx) (*Tx, bool)` replaces direct `TxFromContext` use in the six authorization store sites
  (`stores/pgx/writes.go:97`, `relationships.go:618`, `tuples.go:384`; `stores/turso/writes.go:94`,
  `relationships.go:614`, `tuples.go:376`); `QuerierFrom` on both connectors (`pgxdb/transact.go:62-71`,
  `turso/transact.go:46-51`) consults it; a foreign `*DB` fails on the first statement with a plain `errors.New`
  sentinel like `ErrNestedTransact` (`transact.go:14`) — NOT wrapping `sdk.ErrInvalidInput`, which `ErrFromDomain`
  would answer 400 and `MapError` already uses for check violations — so the misuse is a 500. Needs both connector
  tags, an authorization store repin and the plan's own live legs. No store type-asserts `*Tx`/`*DB` (grep clean);
  `pgxdb.List`/`ExecAffecting` call only Exec/Query/QueryRow.
- **Cut to fit the cap (detail, not scope):** rev 1's per-handler file:line list for the JSON-arm gate (the arms are
  in `sessions.go`/`oauth.go`); rev 1's exact test-case tables per PR (the file lists name the cases); the per-PR
  live-leg counting recipe (S3's counted gate, `RELEASING.md:60`, verbatim); the "house rules in force" recital
  (ARCHITECTURE.md and `pockets/README.md` are the authority); rev 1's R-2 design paragraph (replaced by the shape
  above); rev 1's reassurance notes (sdk layering — stated in PR-1 verify; turso zero-mode `http://` — S1's, not S5's).
- Follow-up (owner files): per-IP keys on a full IPv6 /128 rotate trivially within a /64 — now that PR-1 parses with
  `netip`, a `/64` bucketing option on `TrustProxiesConfig` is a one-liner for S6 or later.
