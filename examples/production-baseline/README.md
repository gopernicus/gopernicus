# examples/production-baseline

The **auditable reference host** (SOC 2 S1): one composition root that asks
for the production posture of [the host contract](../README.md) §3b on every
surface and waives nothing. It makes one acknowledgement —
`MaintenanceRunnerAcknowledged: true`, because it runs retention itself (see
[Retention](#retention)) — so `gopernicus guard --posture` passes it with one
`ack` line, and its `production_test.go` proves that removing any one
production input stops the boot.

Users sign in at one external OIDC provider. The authentication pocket maps
each verified subject to a local user and keeps the security-event audit rail;
the authorization pocket records every role change and serves role
administration, and its denials reach the security-event rail through a host
pocket. Both persist in PostgreSQL through their pgx stores, and the pgxdb
limiter gives authentication one rate-limit budget across instances.

It is a small host (H0): `cmd/`, one host integration, two host pockets
(`pockets/audit`, the decision bridge; `pockets/retention`, the retention
runner) and `workshop/`. There is no `internal/`.

## The composition, in order

`cmd/server/main.go`, top to bottom:

1. `environment.LoadEnvForMode("APP_RUNTIME_MODE")` — the mode comes from the
   process environment only; `.env` is read in development and never opened in
   production.
2. `environment.ParseEnvTags` into `authentication.KeysInput`, then
   `authentication.NewKeys(mode, in, log, KeyJWT, KeyIdentifier)`. Delivery is
   off and there are no passwords, so those two keys are the only ones needed.
3. `web.ServerConfig` and, in production, `ValidateProduction()`: an https
   `PUBLIC_BASE_URL`. Then the security-event archive: a
   `filestorage.NewDisk` sink at `AUDIT_ARCHIVE_DIR`, labeled
   `disk:<dir>` — required in production (see [Retention](#retention)).
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
   throttled to one line a minute; `WithAdministration` with a
   `UserAdminCheck` admitting the global `admin` role and
   `UserErasureRoute: true` (see [Erase a person](#erase-a-person));
   `WithMFA(MFAConfig{Required: MFARequiredAdmins})`, so an admitted
   administrator below AAL2 gets the RFC 9470 401 on `/auth/admin/users*` (see
   [MFA](#mfa)); and
   `WithMaintenance` with the archive as `ArchiveSink` and `ArchiveLocation`
   and `MaintenanceRunnerAcknowledged: true`; then
   `router.Use(HTTP.ClientInfoMiddleware())`, the client-attribution stamp
   for every host route, and `HTTP.Register(mount)` (the JSON `/auth/*`
   surface).
9. The decision bridge over `Components.SecurityEvents`, the authentication
   pocket's one recorder (see [Audit completeness](#audit-completeness)).
10. `authorization.New` with `WithRuntimeMode(mode)`, the model below,
   `WithDecisionObserver` with the bridge, `WithOperationRetention(180d)`, and
   `Tuples`, `Mutations`, `Operations`, `OperationExpiry` and `Audit` from the
   store. The user-administration check is bound to its `Decisions` here,
   before any route is served. The role routes run behind
   `authenticate ∘ RequireBrowserSafeMutation() ∘ authorize`: `authenticate`
   admits the provider's token at the `Live()` tier
   (`RequireAccessTokenLive()` would refuse it) and only when the provider
   asserted MFA (`RequireMFA()`), and `authorize` requires the global `admin`
   role. The adapter serving them is rebuilt with
   `authorizationhttp.WithDecisionObserver` (the same bridge) and
   `WithLogger`.
11. The retention runner (`pockets/retention/inbound/runner`) over
   `Components.Maintenance` and `Components.Mutations`.
12. `GET /healthz` — 200 while the database answers, 503 otherwise; its
   `detail` carries `audit_write_failures`, `audit_rows_suppressed`,
   `bridge_dropped` and the last maintenance pass (see
   [Retention](#retention)).
13. `web.Run`, with the runner beside it on its own context: it stops after
   the server drains and before the database closes.

The model: a `project` is managed by its `owner`s and viewed by its `viewer`s
and managers, and the global `admin` role manages every project. The integrity
policy keeps one owner on every project.

## Layout

```
cmd/server/                              composition root (wiring only, H10)
cmd/bootstrap/                           one-shot: grants the first global admin through the authorization pocket
integrations/authentication/devissuer/   development OIDC issuer: JWKS and POST /dev/token (H11)
pockets/audit/outbound/securityevents/   host pocket: records authorization denials and refused role writes in the security-event rail (H6)
pockets/retention/inbound/runner/        host pocket: drives the maintenance loop and the operation expiry, and reads the last pass for /healthz (H6)
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
| `0029_iam_operations_expiry.sql` | `pockets/authorization/stores/pgx/migrations` `0007`, renumbered |
| `0030_retention.sql`, `0031_security_events_retention.sql` | `pockets/authentication/stores/pgx/migrations` `0023`, `0024`, renumbered |
| `0032_passkey_credentials.sql` | `pockets/authentication/stores/pgx/migrations` `0025`, renumbered; required by the store's boot probe even with passkeys unwired |

When a store ships a new migration, copy it in as the next number. Never
renumber a file once it is applied: the filename is its ledger identity.

`0029`–`0031` (retention) are additive: apply them in their own `make migrate`
invocation, before the binary that needs them deploys. The runner applies
every pending file in one transaction, so on a large table pre-build their
indexes `CONCURRENTLY` first (the [database posture
guide](../../workshop/documentation/docs/guides/database-posture.md) has the
recipe and the `indisvalid` check). `0031` refuses to apply unless the
migrator is a member of the owner of `security_events`.

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
refuses it`, the maintenance registration line (`maintenance_runner=true
archive_sink=none` until `AUDIT_ARCHIVE_DIR` is set), the MFA posture line
(`auth: mfa posture mfa_required=admins source=config gates=/auth/admin/users*`) and the authorization
registration line with `audit_recording=true audit_reader=true
decision_observer=true runtime_mode=development`.

```sh
curl -i localhost:8080/healthz
#   HTTP/1.1 200 OK
#   Strict-Transport-Security: max-age=31536000; includeSubDomains
#   X-Content-Type-Options: nosniff
#   X-Frame-Options: DENY
#   {"detail":{"audit_rows_suppressed":0,"audit_write_failures":0,"bridge_dropped":0,"maintenance_last_error_kind":"none","maintenance_last_pass":null,"maintenance_steps_failed":0},"status":"ok"}

TOKEN=$(curl -s -XPOST localhost:8080/dev/token -d '{"sub":"u1","email":"a@x.test","email_verified":true,"amr":["pwd","mfa"]}' | jq -r .token)
SINGLE=$(curl -s -XPOST localhost:8080/dev/token -d '{"sub":"u1","email":"a@x.test","email_verified":true,"amr":["pwd"]}' | jq -r .token)
ASSIGN='{"subject_type":"user","subject_id":"x","role":"admin","scope":{"kind":"global"}}'

# a cross-site request carrying the session cookie: refused before authorization
curl -i -XPOST localhost:8080/authorization/roles -H "Authorization: Bearer $TOKEN" \
  -H 'Cookie: session=present' -H 'Origin: https://evil.example' -H 'Sec-Fetch-Site: cross-site' \
  -H 'Content-Type: application/json' -d "$ASSIGN"
#   403 {"message":"cross-site request rejected","code":"origin_rejected"}

# no credential
curl -i -XPOST localhost:8080/authorization/roles -H 'Content-Type: application/json' -d "$ASSIGN"   # 401
curl -i 'localhost:8080/authorization/roles/by-subject?subject_type=user&subject_id=x'               # 401

# a single-factor token: refused by authentication with the RFC 9470 challenge
curl -i -XPOST localhost:8080/authorization/roles -H "Authorization: Bearer $SINGLE" \
  -H 'Content-Type: application/json' -d "$ASSIGN"
#   401 WWW-Authenticate: Bearer error="insufficient_user_authentication", error_description="multi-factor authentication is required"
#       {"message":"authentication required","code":"unauthenticated"}

# the provider's MFA token alone: authenticated, then refused by authorization (u1 is not an admin)
curl -i -XPOST localhost:8080/authorization/roles -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d "$ASSIGN"
#   403 {"message":"permission denied","code":"permission_denied"}
```

The first token provisions a local user and writes one
`external_subject_provisioned` row to `security_events`, and each token's first
use one `external_login` row whose `details` carry `"assurance": "aal2"` for
`$TOKEN` and `"aal1"` for `$SINGLE`. The single-factor request writes one
`mfa_required_denied` row (`blocked`; `{"gate": "route", "provider": "idp",
"required": "aal2", "assurance": "aal1", "credential": "external_token"}`); the
last request's 403 writes one `authorization_denied` row through the decision
bridge. All carry the client's `ip_address` and `user_agent`:

```sh
docker exec baseline-pg psql -U postgres -Atc "select event_type,event_status,details from security_events order by created_at desc, id desc limit 5"
#   authorization_denied|failure|{"operation": "EvaluateResolved", "permission": "admin", "resource_id": "", "resource_type": ""}
#   mfa_required_denied|blocked|{"gate": "route", "provider": "idp", "required": "aal2", "assurance": "aal1", "credential": "external_token"}
#   external_login|success|{"provider": "idp", "assurance": "aal1"}
#   external_login|success|{"provider": "idp", "assurance": "aal2"}
#   external_subject_provisioned|success|{"provider": "idp", "verified": true, "identifier_kind": "email"}
```

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
that admin as the actor (the MFA token; `$SINGLE` still answers 401):

```sh
curl -i -XPOST localhost:8080/authorization/roles -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d "$ASSIGN"
#   200 {"outcome":"applied"}
```

### MFA

`MFARequiredAdmins` requires AAL2 on `/auth/admin/users*` of every caller the
`UserAdminCheck` admits, after the check runs, and the role routes carry
`RequireMFA()`. AAL2 is the provider's assertion: `amr` with `mfa`, or values
from two factor categories (the pocket README's "Assurance model"). With u1 an
admin:

```sh
OTP=$(curl -s -XPOST localhost:8080/dev/token -d '{"sub":"u1","email":"a@x.test","email_verified":true,"amr":["otp"]}' | jq -r .token)
U2=$(curl -s -XPOST localhost:8080/dev/token -d '{"sub":"u2","email":"b@x.test","email_verified":true,"amr":["pwd"]}' | jq -r .token)
curl -i localhost:8080/auth/admin/users -H "Authorization: Bearer $TOKEN"    # 200 {"items":[…]}
curl -i localhost:8080/auth/admin/users -H "Authorization: Bearer $SINGLE"   # 401 + the RFC 9470 challenge
curl -i localhost:8080/auth/admin/users -H "Authorization: Bearer $OTP"      # 401: one factor, the strict rule
curl -i localhost:8080/auth/admin/users -H "Authorization: Bearer $U2"       # 403 {"code":"permission_denied"}: not an admin
```

Each 401 writes one `mfa_required_denied` row with `"gate": "admin"`; the
non-admin's 403 writes none, so the challenge never reveals who is an
administrator. The admin gate does not cover host routes or the role routes —
those carry their own `RequireMFA()`. In production, an IdP-only host that
declares no MFA posture is refused at boot (`ErrSingleFactorUnacknowledged`,
cause `external_no_mfa`); `WithMFA` is this host's declaration, so it needs no
acknowledgement.

## Two roles

The runner and the server connect as different roles. The runner (`make
migrate`, `cmd/bootstrap`) is the **owner**: it owns the schema, every table and
sequence, the trigger functions and the rail's definer function. Make it
`NOSUPERUSER` — the docker image's `postgres` is a superuser, and the boot
probe reports a superuser-owned definer function inoperative (a production
WARN, `definer_function=inoperative`). The server connects as
**`baseline_app`**, a runtime role with data access only. Create both with SQL,
never a provider console, and **before** the first migration, so the default
privileges cover every table the ledger creates:

```sql
-- as the cluster's superuser (postgres in docker), once
CREATE ROLE baseline_owner LOGIN NOSUPERUSER PASSWORD '…';
CREATE ROLE baseline_app LOGIN PASSWORD '…';
ALTER SCHEMA public OWNER TO baseline_owner;

-- as baseline_owner, before make migrate (DB_USER=baseline_owner)
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT USAGE ON SCHEMA public TO baseline_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO baseline_app;
```

`REVOKE CREATE … FROM PUBLIC` keeps the runtime role from planting an object
in the schema the definer function resolves names in.

After `make migrate`, narrow the audit tables to read and append, and grant the
rail's one purge path. `DELETE` stays granted on `iam_operations`: its trigger
admits deleting an operation that is not fenced and refuses a fenced one.

```sql
REVOKE UPDATE, DELETE, TRUNCATE, TRIGGER ON security_events, iam_audit FROM baseline_app;
GRANT USAGE ON SEQUENCE security_events_seq, iam_audit_seq TO baseline_app;
REVOKE UPDATE, TRUNCATE, TRIGGER ON iam_operations FROM baseline_app;
GRANT EXECUTE ON FUNCTION security_events_purge_before(pg_catalog.timestamptz, pg_catalog.int4) TO baseline_app;
```

`0031_security_events_retention.sql` revokes the function from `PUBLIC` and
cannot know the runtime role, so the `GRANT EXECUTE` is yours. Without it the
server boots with one `maintenance_execute=missing` WARN and every pass reports
`step=security_events error_kind=forbidden` while the other steps run.

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
#   functions=[security_events_purge_before:execute=true unsafe=none inoperative=none] floor=365 days
#   table=iam_audit protected_by=privilege owner=false insert=true sequence_usage=true trigger=verified
#   table=iam_operations protected_by=privilege owner=false insert=true sequence_usage=true trigger=verified
```

It decides exactly as the stores' boot probe does, over the stores'
`AuditTables()`, and exits 1 when a table is `protected_by=none`, the role
cannot append, or the definer function is missing or unsafe (PUBLIC can
execute it, or its `search_path` is not pinned). `execute=false` (no `GRANT
EXECUTE`) and an `inoperative=` reason (not `SECURITY DEFINER`, a superuser or
foreign owner, `CREATE` on the schema) are reported without failing: the boot
WARNs on both. `floor=365 days` is the rail's database floor. Before the
`REVOKE` above it prints `protected_by=trigger`.

## Retention

The host pocket `pockets/retention/inbound/runner` runs retention in this
process — the reason for the one acknowledgement. It drives two pocket calls:

- `Components.Maintenance.Run(ctx)`: a first pass within a tenth of an hour of
  boot, then one an hour. A pass purges expired sessions (24 h past expiry),
  OAuth states (1 h), grants and contact changes (24 h), terminal and expired
  invitations (90 days) and the OAuth2 rows; challenges are unwired here and
  report `skipped`. Once a day it archives then purges the security-event rows
  older than 400 days (`security_event_retention=9600h0m0s`).
- `Components.Mutations.ExpireOperations(ctx, 500)` at boot and every hour,
  until a short batch: `iam_operations` records committed more than 180 days
  ago (`WithOperationRetention`); a fenced record never expires, and an expired
  operation id reads as never recorded.

Each step logs one line — `msg=maintenance_step step=<name> count=<n>
skipped=<bool> error_kind=<kind>`, then `msg=maintenance_pass`, and
`msg="retention step" step=operations count=<n>`. The registration line says
what runs: `maintenance_runner=true archive_sink=disk:<dir>
security_event_retention=9600h0m0s`.

**The floor.** `security_events_purge_before` refuses a cutoff younger than
`security_events_retention_floor()` — 365 days, a framework invariant no
procedure lowers. The function is the runtime role's only way to delete from
the rail; it appends one `security_events_purged` row per call (`count`,
`floor_days`, `actor_id='maintenance'`), a zero count included. Maintenance
must run against the database that holds the rail: the store probes the
function on the connection it purges through.

**The archive.** Each chunk of rail rows is written to `AUDIT_ARCHIVE_DIR` as
`security-events/<cutoff>/<id>.ndjson`, read back (size, lines, SHA-256),
evidenced by one `security_events_archived` row (`object`, `sha256`, `rows`,
`archive_location`), and only then purged. A failed archive purges nothing
that pass (`error_kind=archive_failed`). The archive holds IP addresses and
User-Agents: restrict access, back it up, set its own retention — it is outside
"Erase a person". A disk sink does not fsync and is per instance: this host is
single-instance and runs **one runner**; a multi-replica host runs the runner
on one replica or archives to a shared object store
(`integrations/filestorage/gcs`, `integrations/filestorage/s3` — any
`filestorage.Storer` is a `maintenance.ArchiveSink`).

**Health.** `/healthz` `detail` reports the last pass and never error text —
the endpoint is unauthenticated — and a maintenance failure never changes the
status code:

```text
"maintenance_last_pass":"2026-10-23T18:04:12Z"   null before the first pass
"maintenance_steps_failed":0
"maintenance_last_error_kind":"none"             forbidden | below_floor | archive_failed | budget_exhausted | other
```

**The alert.** No `security_events_purged` row in 48 h means retention
stopped. Run it from the monitor as the owner or a read-only role:

```sh
psql -Atc "SELECT coalesce(max(created_at) < now() - interval '48 hours', true) AS retention_stalled
           FROM security_events WHERE event_type = 'security_events_purged'"
#   f   — a rail step has run in the last 48 h
#   t   — page: the runner is stopped or the rail step fails every pass
```

The [operations runbooks](../../workshop/documentation/docs/guides/operations-runbooks.md)
("Retention") verify the archive against the archived rows and restore the
grants after a `pg_restore -x`.

## Erase a person

`UserErasureRoute: true` mounts `POST /auth/admin/users/{id}/erase` behind the
admin routes' gates and the host check, which admits the global `admin` role:

```sh
curl -i -XPOST localhost:8080/auth/admin/users/<user_id>/erase \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' -d '{}'
#   200 {"user":{"status":"erased",…},"changed":true,"counts":{…}}
```

One transaction marks the user erased, blanks the name, the email and every
identifier, deletes the sessions, grants and the provider mirror
(`external_subjects`) and appends one `user_erased` row with the counts and the
admin's client info. A pending invitation addressed to them is deleted; a
settled one loses `identifier` and `metadata` and keeps `invited_by` and
`resolved_subject_id`. A repeat
answers 200 `changed:false`; `…/reactivate` answers 409; erasing yourself 400;
a held accepting invitation claim 409 until it is settled. The database keeps
`status='deactivated'` with `erased_at` set; the API says `erased`.

It is not a ban. The provider still vouches for the person, so their next
request — the same token included — provisions a fresh, empty user from the
provider's claims; to keep them out, remove them at the provider. It leaves
what is out of reach by design: their `security_events` rows keep the IP and
User-Agent until the rail's 400-day retention removes them, archives already
written stay, and `iam_operations` fingerprints expire after 180 days.
Then finish the composition: remove the person's role assignments through
`/authorization/roles` (an executed step), and sweep — the runbook's "Erase a
person" has both.

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
3. `AUDIT_ARCHIVE_DIR` unset → `AUDIT_ARCHIVE_DIR is required in production`.
4. A database target without verified TLS (`DB_SSLMODE=disable`, `prefer`, a
   bare `require`) → `pgxdb.ErrTLSRequired`, naming the class; no connection is
   attempted. Use `verify-full` with `sslrootcert=system` or the provider CA.
5. A runtime role that owns or can modify an audit table, or cannot append to
   it → `pgxdb.ErrAppendOnlyTableWritable` / `ErrAppendOnlyTableUnwritable`,
   naming the table and the fix ([Two roles](#two-roles)); a purge function
   PUBLIC can execute or whose `search_path` is not pinned →
   `pgxdb.ErrDefinerFunctionUnsafe`, naming the function and the `REVOKE`.
6. `IDP_ISSUER` unset → `devissuer.ErrNotDevelopment`: the development issuer
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
(`authentication.ErrDevelopmentOnlyProvider`), the runner acknowledged without
a maintenance store (`authentication.ErrMaintenanceAcknowledgedWithoutRepository`),
the erase route without the erasure store
(`authentication.ErrUserErasureRouteRequires`). The binary always wires them;
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
needed key, `PUBLIC_BASE_URL`, `AUDIT_ARCHIVE_DIR`, `Secure` cookies, the
maintenance and erasure stores, and a development issuer built in development
and handed to the production pocket. The positive control also reads the
registration lines for `decision_observer=true`, `maintenance_runner=true`,
`archive_sink=disk:…` and `operation_expiry`, and asserts production logs no
WARN. An HTTP test serves the router on a loopback listener and proves the role
routes' gate order (403 for a cross-site cookie request, 401 without a
credential, 401 with the RFC 9470 challenge for a single-factor token) and that
the erase route refuses a non-admin with 403 whatever the token's assurance.
The positive control also reads the `mfa_required=admins source=config
gates=/auth/admin/users*` line.

`pockets/retention/inbound/runner/runner_test.go` proves the runner: it starts
the maintenance loop and returns only after it stops, drains the operation
expiry in 500-record calls until a short batch, logs a failing or panicking
expiry by kind and retries next tick, and reports the last pass — time,
failed-step count, kind — with no error text.

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

H0–H14 and P0–P9 pass with one `ack` line,
`cmd/server/main.go:<line>: MaintenanceRunnerAcknowledged`; P9 finds a
byte-identical copy of every store migration, both trigger files and the
retention files included. The repository's own `make
guard` runs the same check (G30, G32, G34).
