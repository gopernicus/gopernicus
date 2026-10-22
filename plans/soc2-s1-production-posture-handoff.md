# Host handoff — production posture floor (SOC 2 slice S1, released 2026-10-22)

**Status:** OPEN — apply in gps-360-go, Segovia and coordination-hub on each host's own
schedule. Release record: `RELEASING.md` "Production posture floor". Plan of record:
`plans/s1-production-posture.md`. Manifest: `plans/s1-production-posture-release-manifest.json`.

## The versions, in repin order

Repin in this order so every `go.sum` resolves against published tags; `go mod tidy` with
`GOWORK=off` after each step.

1. `github.com/gopernicus/gopernicus/sdk` **v0.16.0**
2. `integrations/datastores/pgxdb` **v0.10.0**, `integrations/datastores/turso` **v0.7.0**,
   `integrations/tracing/otel` **v0.3.0** (whichever the host uses)
3. `pockets/authorization` **v0.28.0** TOGETHER WITH `pockets/authorization/stores/pgx`
   **v0.19.0** / `stores/turso` **v0.18.0** — a v0.28.0 core over an older store refuses in
   production with `ErrAuditRequired` ("does not report AuditRecording()")
4. `pockets/authentication` **v0.20.0** (its stores and views are untouched)
5. `workshop/gopernicus` **v0.5.0** for `gopernicus guard --posture` (an older pin fails the
   invocation: the binary exits 2, `go run` reports `exit status 2` and returns 1 — treat any
   non-zero as a broken invocation, never a pass)

`pockets/cms` **v0.4.0** applies only to a host that mounts cms (BREAKING: admin routes are
deny-by-absence — see AUDIT-053).

Every tag carries S8's `go 1.26.10` directive and pgx v5.9.2; `GOTOOLCHAIN=auto` switches on
its own, `GOTOOLCHAIN=local` on an older toolchain fails.

## What changes at boot (every host)

With a non-zero runtime mode passed, production refuses at construction — in one joined error
naming every acknowledgement — unless the composition root writes the acknowledgement as a
boolean literal (no env knob). Each honored acknowledgement logs one `posture_waiver=<Flag>`
WARN. Zero mode on the new optional inputs (`authorization.WithRuntimeMode`,
`cms.Config.RuntimeMode`, `pgxdb.Config.RuntimeMode`, `tursodb.Config.RuntimeMode`,
`otel.Config.RuntimeMode`) is today's behavior; the authentication pocket's mode was already
positional.

| Refusal (production) | Acknowledgement | Where |
|---|---|---|
| nil `Repositories.SecurityEvents` | `authentication.WithAuditDisabledAcknowledged()` | authentication |
| native single-factor login (password flows on, or any `Passwordless` kind) | `IdentityConfig.SingleFactorAcknowledged` | authentication |
| a delivery transport declaring opportunistic STARTTLS (both check sites) | `DeliveryConfig.OpportunisticTLSAcknowledged` | authentication |
| an OAuth/external provider declaring `DevelopmentOnly` | none | authentication |
| tuple authority / mutations not reporting `AuditRecording()` true, or nil `Repositories.Audit` | `authorization.WithAuditDisabledAcknowledged()` | authorization (`WithRuntimeMode`) |
| `AdminUngatedAcknowledged` itself; a Mailer failing `notify.CheckTransport` | — / — | cms (`RuntimeMode`) |
| a connection target that is *plaintext* (`disable`, the fallback of `prefer`/`allow`) or *unverified* (bare `require`) | `TLSDisabledAcknowledged` (all) / `TLSUnverifiedAcknowledged` (bare `require`) | pgxdb |
| `LogQueries`, or a `Tracer` that is or wraps `*LoggingQueryTracer` | none | pgxdb |
| a non-`file:` URL that is not `libsql`/`https`/`wss`, or `libsql` with `tls=0` | `TLSDisabledAcknowledged` | turso |
| empty `Exporter`; OTLP `Insecure` to a non-loopback endpoint | none (set `TRACING_EXPORTER`) | otel |

Not a refusal but a change: `web.SecurityHeaders()` sends HSTS (`max-age=31536000;
includeSubDomains`), `nosniff`, `Referrer-Policy: strict-origin-when-cross-origin`,
`X-Frame-Options: DENY` and a minimal `Permissions-Policy` when installed; HSTS is sticky, so
install it only on an origin that is fully HTTPS. `RespondJSON*` now always sets `nosniff`.
`ServerConfig.ValidateProduction()` requires `PUBLIC_BASE_URL` to be a bare https origin.

## The mode and `.env` rule

`environment.LoadEnvForMode("<KEY>")` reads the mode from the process environment ONLY. A
`.env` that assigns the key — even empty — is refused with `ErrModeFromFile`; in production
`.env` is never opened (one WARN if present); in development it loads. A host that ships
`.env` beside a production binary moves EVERY variable to the process environment (systemd
`Environment=`/`EnvironmentFile=`, `docker --env-file`, platform secrets), not only the mode.
`.env.example` mentions the mode key in a comment only (guard P7).

## Key continuity (do this BEFORE the repin reaches production)

`authentication.NewKeys` decodes all five keys as hex. If the host copied the reference host's
loaders, `AUTH_JWT_SECRET`'s hex TEXT was the key material and `AUTH_DELIVERY_ENCRYPTER_KEY` /
`AUTH_TOKEN_ENCRYPTER_KEY` were raw 32-character strings. Set each new variable to the hex of
the OLD bytes so nothing is invalidated and a mixed fleet keeps accepting each other's output:

```sh
printf %s "$OLD_VALUE" | xxd -p | tr -d '\n'
```

Never mint a new value at the upgrade: that logs every session out (JWT), strands every
in-flight sealed outbox job (delivery key) and makes every stored provider token undecryptable
(token key). `AUTH_TOKEN_ENCRYPTER_KEY` stays optional (unset ⇒ provider tokens not persisted).

## Read-only dry run before the repin

From the host root, with workshop/gopernicus v0.5.0:

```sh
go run github.com/gopernicus/gopernicus/workshop/gopernicus@v0.5.0 guard --posture .
```

P0 mode from the process environment only · P1 every recognized `Config` names `RuntimeMode`,
`authentication.New` passes a mode, `authorization.New` passes `WithRuntimeMode(` · P2
`web.TrustProxies(` first in the first `.Use(` · P3 `web.SecurityHeaders(` in a `.Use(` · P4
`ValidateProduction()` called · P5 acknowledgements are boolean literals (one `ack` line each) ·
P6 keys through `authentication.NewKeys` (`rand` lines are informational) · P7 `.env.example`
hygiene · P8 every `cms.Config` names `AdminMiddleware` or `AdminUngatedAcknowledged`.

## gps-360-go

- **Refusals expected:** authentication — the audit rail is wired (nothing to acknowledge);
  single-factor only if native password or passwordless login is enabled (set
  `IdentityConfig.SingleFactorAcknowledged` or move to `PasswordFlowsDisabled`); STARTTLS only if
  the mailer declares it. Authorization — pass `WithRuntimeMode(mode)`, construct the pgx store
  `WithAudit()` and wire `Repositories.Audit` (the store returns it), repin core and store together.
- **Postgres (O-4, owner fills in):** gps-360-go connects to Cloud SQL via ______ (unix socket /
  direct IP / Auth Proxy on loopback TCP).
  - unix socket (connector or Auth Proxy `--unix-socket`): class *local*, no acknowledgement;
  - direct IP: `sslmode=verify-ca` + `sslrootcert=server-ca.pem` (verified, no acknowledgement);
  - Auth Proxy on `127.0.0.1:5432` with `sslmode=disable`: class *plaintext* — prefer the socket,
    or set `TLSDisabledAcknowledged: true` and accept the `posture_waiver` WARN in the log.
- **Dry run:** confirm `TrustProxies` is first in the global `Use`, add `web.SecurityHeaders()`
  (or `SecurityHeadersWithConfig` if any route is embedded), call `ValidateProduction()` under
  production, route the five keys through `KeysInput` + `NewKeys` with the continuity recipe,
  and move the mode out of `.env`.
- **Repin:** sdk v0.16.0 → pgxdb v0.10.0 → authorization v0.28.0 + stores/pgx v0.19.0 →
  authentication v0.20.0; `GOWORK=off go mod tidy` after each.

## Segovia (v2)

Known findings from the S1 review's read-only dry run of `segovia/v2/cmd/server/` (line
numbers as the review recorded them on 2026-10-22; the functions are the durable anchors):
- **P0:** `hostRuntimeMode` (`main.go:718`) pre-seeds `environment.ModeDevelopment` and parses
  `AUTH_RUNTIME_MODE` with env tags — resolve the mode with `LoadEnvForMode` from the process
  environment and never default it.
- **P1:** `openDatabase` (`main.go:731-738`) parses `pgxdb.Config` from env tags with no
  `RuntimeMode`; set the resolved mode on it. Classify the DSN: `verify-full`/`verify-ca` need
  no acknowledgement; a bare `require` needs `TLSUnverifiedAcknowledged`; a unix socket is
  *local*.
- **P2:** already satisfied — `web.TrustProxies` is first in the global `router.Use`; the
  second `router.Use(callbackHop)` is fine (P2 reads the first `Use` on the served handler).
- **P3:** no `SecurityHeaders` in that global `Use`. Segovia's dashboards set CSP
  `frame-ancestors` for embedding (`internal/inbound/domains/dashboards/content.go:71`), so
  install `web.SecurityHeadersWithConfig(web.SecurityHeadersConfig{DisableFrameOptions: true})`
  and keep CSP as the framing control.
- **P6:** Segovia's own `secretKeys` loader already decodes hex through `environment.Secret`,
  fails closed in production and mints per-boot keys in development (the `rand` lines P6
  prints are informational). Moving to `KeysInput` + `NewKeys` deletes that loader; because the
  bytes are already the hex-decoded ones, no continuity step is needed.
- **Authentication:** the audit rail is wired. If native password or passwordless login is on,
  set `IdentityConfig.SingleFactorAcknowledged` until the IdP-delegated posture lands.
  `RequireCookieMutationOrigin` stays on Segovia's host routes; the bundled role routes gain
  `RequireBrowserSafeMutation` when Segovia composes the authentication adapter's gate.
- **Authorization:** `WithRuntimeMode(mode)`, pgx store `WithAudit()`, `Repositories.Audit`;
  core v0.28.0 with stores/pgx v0.19.0 together.
- **Repin:** sdk v0.16.0 → pgxdb v0.10.0 (goredis unchanged) → authorization v0.28.0 +
  stores/pgx v0.19.0 → authentication v0.20.0 → workshop v0.5.0 for the guard.

## coordination-hub

- Same order and the same refusal table. The hub runs authentication with the pgx store and
  its own authorization policy; expect `ErrAuditRequired` from the authorization pocket only if
  the hub passes `WithRuntimeMode(production)` before constructing the store `WithAudit()`.
- Dry run `guard --posture` first; the hub's `.env.example` and its global `Use` are the likely
  P7/P2/P3 findings. Apply the mode and key rules above.
- Repin: sdk v0.16.0 → pgxdb v0.10.0 → authorization v0.28.0 + stores v0.19.0/v0.18.0 →
  authentication v0.20.0.

## After the repin

Run `guard --posture` in CI (require a zero exit), archive the boot log's `posture_waiver`
lines with the deploy, and record the chosen acknowledgements in the host's own posture doc:
they are the waivers an auditor samples.
