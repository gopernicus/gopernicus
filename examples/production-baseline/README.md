# examples/production-baseline

The **auditable reference host** (SOC 2 S1): one composition root that asks
for the production posture of [the host contract](../README.md) §3b on every
surface and acknowledges nothing. `gopernicus guard --posture` passes it with
no `ack` line, and its `production_test.go` proves that removing any one
production input stops the boot.

Users sign in at one external OIDC provider. The authentication pocket maps
each verified subject to a local user and keeps the security-event audit rail;
the authorization pocket records every role change and serves role
administration, and its denials reach the security-event rail through a host
pocket. Both persist in PostgreSQL through their pgx stores, and the pgxdb
limiter gives authentication one rate-limit budget across instances.

It is a small host (H0): `cmd/`, one host integration, one host pocket
(`pockets/audit`, the decision bridge) and `workshop/`. There is no
`internal/`.

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
   `Panics`; the authentication adapter's `ClientInfoMiddleware()` joins it in
   step 8, in a later `Use`, so it runs inside `TrustProxies`.
7. The identity provider, chosen by whether `IDP_ISSUER` is set: unset → the
   development issuer served by this process; set → `sdkauth.NewOIDC` over
   go-oidc with `IDP_ISSUER`, `IDP_AUDIENCE` and `IDP_JWKS_URL`.
8. `authentication.New` with the security-event rail wired,
   `PasswordFlowsDisabled`, delivery `off`, the pgxdb limiter, the identifier
   keyer from the keys, `Secure` session cookies, `AllowedOrigins` from
   `PUBLIC_BASE_URL`, the one external provider, and
   `WithAuditWriteObserver` logging lost security-event rows at ERROR,
   throttled to one line a minute; then
   `router.Use(HTTP.ClientInfoMiddleware())`, the client-attribution stamp
   for every host route, and `HTTP.Register(mount)` (the JSON `/auth/*`
   surface).
9. The decision bridge over `Components.SecurityEvents`, the authentication
   pocket's one recorder (see [Audit completeness](#audit-completeness)).
10. `authorization.New` with `WithRuntimeMode(mode)`, the model below,
   `WithDecisionObserver` with the bridge, and `Tuples`, `Mutations`,
   `Operations` and `Audit` from the store. The role routes run behind
   `authenticate ∘ RequireBrowserSafeMutation() ∘ authorize`: `authenticate`
   admits the provider's token at the `Live()` tier
   (`RequireAccessTokenLive()` would refuse it), and `authorize` requires the
   global `admin` role. The adapter serving them is rebuilt with
   `authorizationhttp.WithDecisionObserver` (the same bridge) and
   `WithLogger`.
11. `GET /healthz` — 200 while the database answers, 503 otherwise; its
   `detail` carries `audit_write_failures`, `audit_rows_suppressed` and
   `bridge_dropped`.

The model: a `project` is managed by its `owner`s and viewed by its `viewer`s
and managers, and the global `admin` role manages every project. The integrity
policy keeps one owner on every project.

## Layout

```
cmd/server/                              composition root (wiring only, H10)
cmd/bootstrap/                           one-shot: grants the first global admin through the authorization pocket
integrations/authentication/devissuer/   development OIDC issuer: JWKS and POST /dev/token (H11)
pockets/audit/outbound/securityevents/   host pocket: records authorization denials and refused role writes in the security-event rail (H6)
workshop/migrations/                     the pre-boot runner, its -posture-role preflight, and its host-owned ledger (primary/)
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
| `0025_iam_audit_integrity.sql`, `0026_iam_audit_immutable.sql` | `pockets/authorization/stores/pgx/migrations` `0005`, `0006`, renumbered |
| `0027_security_events_integrity.sql`, `0028_security_events_immutable.sql` | `pockets/authentication/stores/pgx/migrations` `0021`, `0022`, renumbered |

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
key, the plaintext-connection WARN, one `append_only=unenforced` WARN per store
(`make run` connects as the owner, see [Two roles](#two-roles)),
`authentication: development-only external identity provider wired; production
refuses it`, and the authorization registration line with
`audit_recording=true audit_reader=true decision_observer=true
runtime_mode=development`.

```sh
curl -i localhost:8080/healthz
#   HTTP/1.1 200 OK
#   Strict-Transport-Security: max-age=31536000; includeSubDomains
#   X-Content-Type-Options: nosniff
#   X-Frame-Options: DENY
#   {"detail":{"audit_rows_suppressed":0,"audit_write_failures":0,"bridge_dropped":0},"status":"ok"}

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
`external_subject_provisioned` row to `security_events`; the last request's 403
writes one `authorization_denied` row through the decision bridge. Both carry
the client's `ip_address` and `user_agent`.

### The first admin

Nobody holds the global `admin` role until `cmd/bootstrap` grants it. Sign in
once at the provider (above, the first token), find the local user id the
authentication pocket provisioned — the `user_id` of the
`external_subject_provisioned` row in `security_events`, or of the person's
`external_subjects` row — and grant it:

```sh
docker exec baseline-pg psql -U postgres -Atc "select user_id from external_subjects where provider = 'idp' and subject = 'u1'"
make bootstrap SUBJECT_ID=<user_id>
#   applied: global admin for user <user_id>
#   audit event <event_id> at <time>
```

The command writes through the authorization pocket over the same recording
store, under the same runtime mode as the server, so the grant is in the
change history (`iam_audit`, `system_source = bootstrap`, reason `first platform
admin`). Rerunning it answers `no_change` and records nothing. **A raw SQL
insert into `iam_tuples` is not supported:** it leaves no audit row. Once an
admin exists, role writes through `POST /authorization/roles` are recorded with
that admin as the actor:

```sh
curl -i -XPOST localhost:8080/authorization/roles -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d "$ASSIGN"
#   200 {"outcome":"applied"}
```

## Two roles

The runner and the server connect as different roles. The runner (`make
migrate`, `cmd/bootstrap`) is the **owner**: it owns the schema, every table and
sequence, and the trigger functions. The server connects as **`baseline_app`**,
a runtime role with data access only. Create it with SQL, never a provider
console, and **before** the first migration, so the default privileges cover
every table the ledger creates:

```sql
-- as the owner, before make migrate
CREATE ROLE baseline_app LOGIN PASSWORD '…';
GRANT USAGE ON SCHEMA public TO baseline_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO baseline_app;
```

After `make migrate`, narrow the audit tables to read and append. `DELETE` stays
granted on `iam_operations`: its trigger admits deleting an operation that is
not fenced and refuses a fenced one.

```sql
REVOKE UPDATE, DELETE, TRUNCATE, TRIGGER ON security_events, iam_audit FROM baseline_app;
GRANT USAGE ON SEQUENCE security_events_seq, iam_audit_seq TO baseline_app;
REVOKE UPDATE, TRUNCATE, TRIGGER ON iam_operations FROM baseline_app;
```

The integrity migrations grant `USAGE` on both sequences to `PUBLIC`, so the
role appends even before the explicit grant. Between the migration and the
`REVOKE`, the triggers already refuse the role's UPDATE and DELETE (`ERROR:
security_events is append-only`); after it, PostgreSQL refuses them by privilege.

In production the server meets the store's contract, in one sentence: **in
production a store refuses to boot when its runtime role is superuser, owns an
audit table, can UPDATE/DELETE/TRUNCATE/TRIGGER it without the verified
triggers, or cannot insert into it.** A server that connects as the owner stops
before serving anything:

```text
postgres: runtime role can modify an append-only table: security_events: owner=table superuser; activate the trigger files; …
```

and a role without `USAGE` on a rail's sequence stops with
`postgres: runtime role cannot append to an append-only table: security_events: sequence_usage=false sequence=public.security_events_seq; …`.
Each refusal ends with the statements that fix it. The [database posture
guide](../../workshop/documentation/docs/guides/database-posture.md) carries the
general recipe.

## Audit completeness

Three append-only tables hold the evidence: `security_events` (the
authentication pocket's rail), `iam_audit` (every role change) and
`iam_operations` (the authorization operation ledger).

**Denials.** `pockets/audit/outbound/securityevents` bridges the authorization
pocket's decision observer to the security-event rail. `cmd/server` builds it
over `Components.SecurityEvents`, the authentication pocket's one recorder, so a
row the bridge loses counts in `audit_write_failures`, and installs the
authentication adapter's `ClientInfoMiddleware()` after the `TrustProxies`
`Use`, so a denial on a host route carries the client's IP and User-Agent like
the pocket's own rows. A denied decision becomes
an `authorization_denied` row (`failure`) and a role write the `WritePolicy`
refuses a `role_write_refused` row (`blocked`); an allowed decision records
nothing. The route gates decide through `EvaluateResolved`; since authorization
v0.29.1 its event names the evaluated leaf's permission and resource (for a
composite expression, the first denied leaf), so the rows name what was denied
and the dedupe key below is per target. The bridge has two bounds, so a flood of 403s neither
drains the pool nor writes a row per request:

- at most 8 writes run at once; an event arriving while all 8 are in flight is
  dropped and counted in `/healthz` `detail.bridge_dropped`;
- one row per (event type, principal, permission, resource type, resource id,
  client IP) per 60 s window, kept in memory, so per instance; a second address
  within the window records its own row.

A security-event write that fails never fails the request: the recorder logs
`audit_write_failed` (WARN, then one summary line a minute), the host logs
`security event not recorded` at ERROR for the first and then one line a minute
with the count, and `/healthz` `detail.audit_write_failures` counts them. A
failure row the per-IP failure-row budget suppresses counts in
`detail.audit_rows_suppressed`.

**Immutability.** `0026_iam_audit_immutable.sql` and
`0028_security_events_immutable.sql` are ordinary ledger files. Their triggers,
`ENABLE ALWAYS`, refuse UPDATE, DELETE and TRUNCATE from every role but the
owner (`iam_operations` admits deleting rows that are not fenced). The owner is
exempt by design so it can backfill, which is why the server must not run as it.

**Order evidence.** `security_events` and `iam_audit` carry `seq`, a sequence
value drawn at insert, and all three tables `recorded_at`, the database clock at
insert. The stores never read either; they are evidence of insertion order. A
rolled-back write leaves a gap in `seq`, so a gap alone is not evidence of a
deletion.

**Before each deploy**, preflight the runtime role from the owner's connection
and archive the output with the deploy:

```sh
go run ./workshop/migrations -posture-role baseline_app
#   table=security_events protected_by=privilege owner=false insert=true sequence_usage=true trigger=verified
#   table=iam_audit protected_by=privilege owner=false insert=true sequence_usage=true trigger=verified
#   table=iam_operations protected_by=privilege owner=false insert=true sequence_usage=true trigger=verified
```

It decides exactly as the stores' boot probe does, over the stores'
`AuditTables()`, and exits 1 when a table is `protected_by=none` or the role
cannot append. Before the `REVOKE` above it prints `protected_by=trigger`.

## Production

```sh
DB_USER=baseline_app APP_RUNTIME_MODE=production go run ./cmd/server
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
4. A runtime role that owns or can modify an audit table, or cannot append to
   it → `pgxdb.ErrAppendOnlyTableWritable` / `ErrAppendOnlyTableUnwritable`,
   naming the table and the fix ([Two roles](#two-roles)).
5. `IDP_ISSUER` unset → `devissuer.ErrNotDevelopment`: the development issuer
   refuses production itself. Set `IDP_ISSUER`, `IDP_AUDIENCE` and
   `IDP_JWKS_URL` (empty runs OIDC discovery) for the real provider.

After the first person signs in at the provider, grant them the global `admin`
role with the same environment, as the owner:

```sh
APP_RUNTIME_MODE=production go run ./cmd/bootstrap -subject-type user -subject-id <user_id>
```

It exits non-zero on any error, including the same TLS refusal as the server.

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
in development and handed to the production pocket. The positive control also
reads the authorization registration line for `decision_observer=true`. An HTTP
test serves the router on a loopback listener and proves the role routes' gate
order (403 for a cross-site cookie request, 401 without a credential).

`pockets/audit/outbound/securityevents/bridge_test.go` proves the bridge: a
denial and a refusal map to their rows, an allowed decision records nothing, a
full in-flight bound drops and counts, and thirty identical denials write one
row while a different resource writes another.

`cmd/bootstrap/main_test.go` runs the bootstrap twice over the recording memory
store in production: the first run grants global `admin` and writes one change
record with `System: "bootstrap"`; the rerun answers `no_change` and writes
none.

## Guard

```sh
make guard   # go run ../../workshop/gopernicus guard --posture .
```

H0–H14 and P0–P9 pass with no `ack` line; P9 finds a byte-identical copy of
every store migration, both trigger files included. The repository's own `make
guard` runs the same check (G30, G32, G34).
