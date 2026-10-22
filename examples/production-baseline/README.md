# examples/production-baseline

The **auditable reference host** (SOC 2 S1): one composition root that asks
for the production posture of [the host contract](../README.md) §3b on every
surface and acknowledges nothing. `gopernicus guard --posture` passes it with
no `ack` line, and its `production_test.go` proves that removing any one
production input stops the boot.

Users sign in at one external OIDC provider. The authentication pocket maps
each verified subject to a local user and keeps the security-event audit rail;
the authorization pocket records every role change and serves role
administration. Both persist in PostgreSQL through their pgx stores, and the
pgxdb limiter gives authentication one rate-limit budget across instances.

It is a thin host (H0): `cmd/`, one host integration, and `workshop/`. There is
no `internal/` and no host pocket.

## The composition, in order

`cmd/server/main.go`, top to bottom:

1. `environment.LoadEnvForMode("APP_RUNTIME_MODE")` — the mode comes from the
   process environment only; `.env` is read in development and never opened in
   production.
2. `environment.ParseEnvTags` into `authentication.KeysInput`, then
   `authentication.NewKeys(mode, in, log, KeyJWT, KeyIdentifier)`. Delivery is
   off and there are no passwords, so those two keys are the only ones needed.
3. `web.ServerConfig` and, in production, `ValidateProduction()`: an https
   `PUBLIC_BASE_URL`.
4. `pgxdb.Open` with `RuntimeMode: mode` (production refuses a connection whose
   TLS does not verify the server), then `pgxdb.NewLimiter(db).StatusCheck(ctx)`.
5. The authentication pgx store, with the native rails this host never serves
   unwired: challenges, contact changes, service accounts and API keys. The
   authorization pgx store `WithAudit()` and `WithIntegrityPolicy(DefaultIntegrityPolicy())`.
6. The router: `TrustProxies`, `SecurityHeaders`, `RequestID`, `Logger`,
   `Panics`.
7. The identity provider, chosen by whether `IDP_ISSUER` is set: unset → the
   development issuer served by this process; set → `sdkauth.NewOIDC` over
   go-oidc with `IDP_ISSUER`, `IDP_AUDIENCE` and `IDP_JWKS_URL`.
8. `authentication.New` with the security-event rail wired,
   `PasswordFlowsDisabled`, delivery `off`, the pgxdb limiter, the identifier
   keyer from the keys, `Secure` session cookies, `AllowedOrigins` from
   `PUBLIC_BASE_URL`, and the one external provider; then
   `HTTP.Register(mount)` (the JSON `/auth/*` surface).
9. `authorization.New` with `WithRuntimeMode(mode)`, the model below, and
   `Tuples`, `Mutations`, `Operations` and `Audit` from the store. The role
   routes run behind `authenticate ∘ RequireBrowserSafeMutation() ∘ authorize`:
   `authenticate` admits the provider's token at the `Live()` tier
   (`RequireAccessTokenLive()` would refuse it), and `authorize` requires the
   global `admin` role.
10. `GET /healthz` — 200 while the database answers, 503 otherwise.

The model: a `project` is managed by its `owner`s and viewed by its `viewer`s
and managers, and the global `admin` role manages every project. The integrity
policy keeps one owner on every project.

## Layout

```
cmd/server/                              composition root (wiring only, H10)
integrations/authentication/devissuer/   development OIDC issuer: JWKS and POST /dev/token (H11)
workshop/migrations/                     the pre-boot runner and its host-owned ledger (primary/)
```

`devissuer` is a copy of `examples/external-identity`'s, declaring
`sdkauth.Capabilities{DevelopmentOnly: true}`. A fourth host that needs it gets
a `devissuer` package inside `integrations/cryptids/go-oidc`, not a fourth copy.

## Migrations

`workshop/migrations/primary` is one ordered stream under one
`schema_migrations` ledger, applied pre-boot by `make migrate`. The server
never migrates. Its files are the host's:

| files | source |
|---|---|
| `0001_users.sql` … `0020_external_subjects.sql` | `pockets/authentication/stores/pgx/migrations`, unrenamed |
| `0021_iam_tuples.sql`, `0022_iam_operations.sql`, `0023_iam_operations_fenced.sql` | `pockets/authorization/stores/pgx/migrations` `0001`, `0003`, `0004` (the base schema, without the optional tuple-cache `0002`), renumbered into this ledger |
| `0024_ratelimit_windows.sql` | the pgxdb limiter's reference DDL |

When a store ships a new migration, copy it in as the next number. Never
renumber a file once it is applied: the filename is its ledger identity.

## First run

Postgres 17 in docker, on a free port:

```sh
docker run --rm -d --name baseline-pg -p 127.0.0.1:55491:5432 -e POSTGRES_PASSWORD=postgres postgres:17
cd examples/production-baseline
cp .env.example .env
# in .env: uncomment DB_SSLMODE=disable (local docker has no TLS), comment out
# DB_SSLMODE=verify-full, set DB_PORT=55491 and DB_PASSWORD=postgres.
# Never add APP_RUNTIME_MODE to .env: the loader refuses it.
make run          # migrate, then serve on :8080 with APP_RUNTIME_MODE=development
```

The boot log carries the development posture: an ephemeral-key WARN per unset
key, the plaintext-connection WARN, `authentication: development-only external
identity provider wired; production refuses it`, and the authorization
registration line with `audit_recording=true audit_reader=true
runtime_mode=development`.

```sh
curl -i localhost:8080/healthz
#   HTTP/1.1 200 OK
#   Strict-Transport-Security: max-age=31536000; includeSubDomains
#   X-Content-Type-Options: nosniff
#   X-Frame-Options: DENY

TOKEN=$(curl -s -XPOST localhost:8080/dev/token -d '{"sub":"u1","email":"a@x.test","email_verified":true}' | jq -r .token)
ASSIGN='{"subject_type":"user","subject_id":"x","role":"admin","scope":{"kind":"global"}}'

# a cross-site request carrying the session cookie: refused before authorization
curl -i -XPOST localhost:8080/authorization/roles -H "Authorization: Bearer $TOKEN" \
  -H 'Cookie: session=present' -H 'Origin: https://evil.example' -H 'Sec-Fetch-Site: cross-site' \
  -H 'Content-Type: application/json' -d "$ASSIGN"
#   403 {"message":"cross-site request rejected","code":"origin_rejected"}

# no credential
curl -i -XPOST localhost:8080/authorization/roles -H 'Content-Type: application/json' -d "$ASSIGN"   # 401
curl -i 'localhost:8080/authorization/roles/by-subject?subject_type=user&subject_id=x'               # 401

# the provider's token alone: authenticated, then refused by authorization (u1 is not an admin)
curl -i -XPOST localhost:8080/authorization/roles -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d "$ASSIGN"
#   403 {"message":"permission denied","code":"permission_denied"}
```

The first token provisions a local user and writes one
`external_subject_provisioned` row to `security_events`.

**The first admin.** This host ships no bootstrap: nobody holds the global
`admin` role until a trusted out-of-band write grants it to a local user id
(`external_subjects.user_id`). Once one admin exists, role writes through
`POST /authorization/roles` are recorded in `iam_audit` with the admin as the
actor.

## Production

```sh
APP_RUNTIME_MODE=production go run ./cmd/server
```

Production never opens `.env` (it logs `.env present, ignored in production`),
so every variable moves to the process environment. Each missing input stops
the boot before anything is served, in this order:

1. `AUTH_JWT_SECRET` and `AUTH_IDENTIFIER_KEY` unset → one joined
   `authentication.ErrKeyRequired` naming both (`openssl rand -hex 32`).
2. `PUBLIC_BASE_URL` unset or not https → `web.ErrPublicBaseURLRequired` /
   `ErrPublicBaseURLInsecure`.
3. A database target without verified TLS (`DB_SSLMODE=disable`, `prefer`, a
   bare `require`) → `pgxdb.ErrTLSRequired`, naming the class; no connection is
   attempted. Use `verify-full` with `sslrootcert=system` or the provider CA.
4. `IDP_ISSUER` unset → `devissuer.ErrNotDevelopment`: the development issuer
   refuses production itself. Set `IDP_ISSUER`, `IDP_AUDIENCE` and
   `IDP_JWKS_URL` (empty runs OIDC discovery) for the real provider.

The pockets refuse the rest at construction — a missing security-event rail
(`authentication.ErrAuditRequired`), an authorization store that does not
record or an unwired audit reader (`authorization.ErrAuditRequired`), a session
cookie without `Secure`, a development-only provider
(`authentication.ErrDevelopmentOnlyProvider`). The binary always wires them;
`cmd/server/production_test.go` proves each refusal.

TLS terminates at the edge; the app sends HSTS. Set `TRUSTED_PROXY_COUNT` to
the number of proxies in front of it, so the access log, the rate limits and
the audit rail see the client's address.

## Tests

```sh
go test ./...
```

`cmd/server/production_test.go` builds the same composition over in-memory
stores: the authorization memory store and identity doubles for the
authentication ports the external engine reads. It proves that the production
baseline constructs, and that each single omission is refused by its stable
error: the security-event rail, `WithAudit()`, `Repositories.Audit`, each
needed key, `PUBLIC_BASE_URL`, `Secure` cookies, and a development issuer built
in development and handed to the production pocket. An HTTP test serves the
router on a loopback listener and proves the role routes' gate order (403 for a
cross-site cookie request, 401 without a credential).

## Guard

```sh
make guard   # go run ../../workshop/gopernicus guard --posture .
```

H0–H14 and P0–P8 pass with no `ack` line. The repository's own `make guard`
runs the same check (G30, G32).
