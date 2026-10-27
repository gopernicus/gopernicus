---
title: Operations runbooks
description: Deactivate or erase a user, revoke sessions, handle an abusive IP, read the audit rails, and run retention.
---

# Operations runbooks

Procedures a host operator runs with what the framework ships on `main` at 2026-10-23.
Each names the bundled route or service call, what it records, and what is missing.

## Deactivate or reactivate a user

- **Bundled routes** (mounted only when the host wires `UserAdminCheck` plus the
  `UserAdmin` and `ActiveSessions` repositories): `POST /auth/admin/users/{id}/deactivate`,
  `POST /auth/admin/users/{id}/reactivate`, `GET /auth/admin/users`,
  `GET /auth/admin/users/{id}`. Each runs the authenticator, the browser-safe gate and the
  host check before any target lookup.
- **Service:** `Service.DeactivateUser(ctx, actor, id)` / `ReactivateUser` /
  `SetUserStatus(ctx, actor, id, status)`.
- **What happens:** one store transaction sets the status, bumps `auth_revision`, and deletes
  every session and grant; a concurrent login cannot mint a session for a deactivated user.
  Afterwards the account is indistinguishable from bad credentials on public endpoints.
- **Recorded:** `user_deactivated` / `user_reactivated` with the administrator as `Actor`.
  Admin listings and reads record nothing.
- **Erasure** is a separate, irreversible procedure — *Erase a person* below. Deactivation
  keeps every identifier and credential row.

## Revoke sessions for one user

- **Self-service HTML** (when `SessionManagement` is wired): `GET /auth/sessions`,
  `POST /auth/sessions/{id}/revoke`, `POST /auth/sessions/revoke-all`.
- **Service:** `ListUserSessions`, `RevokeUserSession(ctx, userID, sessionID)`,
  `RevokeAllUserSessions(ctx, userID)` for a host admin route.
- **Recorded:** `session_revoked`, `all_sessions_revoked`.
- **Lag:** outstanding access JWTs live up to `AccessTokenTTL` on the default tier
  (*Production posture*, "Revocation SLA"). For every user at once, see *Keys and rotation*.

## Revoke all sessions

Authentication v0.31.0 (SOC 2 S4d). For a suspected fleet-wide session compromise.

- **Route** (opt-in: `AdministrationConfig.GlobalSessionRevocationRoute`, beside
  `Repositories.GlobalSessions` and a `UserAdminCheck`): `POST /auth/admin/sessions/revoke-all`
  with the body `{}`. The caller presents a first-party access token or an external token
  (never an API key or a delegated token: 401); the host check sees the action
  `UserAdminRevokeAllSessions` (`revoke-all-sessions`); under `MFARequired: admins` the
  caller must be at **AAL2** (an AAL1 admin gets 401 with the RFC 9470 challenge: step up
  and retry).
- **Budget:** one call a minute per actor; a second is **429**. Re-running later is safe.
- **Answer:** 200 `{"users":n,"sessions":n,"grants":n,"batches":n,"revoked_at":"…"}`, and
  the session cookies are cleared: the caller's own native session ends with the rest, so
  sign in again. The run is detached from the request (10-minute limit); if the client
  times out first, read the outcome from the rail.
- **Service:** `Service.RevokeAllSessions(ctx, actor)` for a host CLI, when the admin path
  itself (an IdP, an admin sign-in) is what is compromised. The pocket README has the recipe.
- **Recorded:** `all_sessions_revoked_global` twice: `started` (`batch_size`), then
  `success` with `users`, `sessions`, `grants`, `batches`, `duration_ms`, or `failure` with
  the partial counts and `error_kind` (re-run it). Read them with
  `GET /auth/admin/security-events?event_type=all_sessions_revoked_global`.
- **What survives:** outstanding access JWTs on safe reads under the default tier, for up
  to `AccessTokenTTL` (rotate `AUTH_JWT_SECRET` too to end them at once, *Keys and
  rotation*); API keys (next runbook); external IdP sessions (an external admin's next
  provider token still signs in; deactivate the user locally to stop one).

## Disable a service account

Authentication v0.31.0 (SOC 2 S4d). A reversible pause of a machine identity.

- **Prerequisite:** migration 0027 (`service_account_status`, baseline ledger 0034)
  applied pre-boot, then `AdministrationConfig.ServiceAccountStatus`
  (`AUTH_SERVICE_ACCOUNT_STATUS`). Set without the table, the host refuses to boot naming
  0027; unset, the table is never read.
- **Routes** (behind the machine routes' gate): `POST /auth/service-accounts/{id}/disable`
  and `…/enable`, body `{}`. Each answers
  `{"service_account":{…,"status":…},"changed":bool,"keys_revoked":n}`; a repeat is
  `changed: false`. **Service:** `DisableServiceAccount` / `EnableServiceAccount`.
- **What happens:** disable marks the account disabled AND revokes every unrevoked key in
  one transaction; its keys, act-as-user ones included, are refused from then on, and a
  mint is 409 `service_account_disabled`. Enable restores minting only: **the old keys stay
  revoked**, so mint and distribute new ones. A compromised account: revoke its keys or
  delete it; disable is the reversible pause. Un-wiring the flag or rolling the binary back
  resumes mints on a disabled account (the revoked keys stay revoked).
- **Recorded:** `service_account_disabled` (`keys_revoked`), `service_account_enabled`;
  a refused key writes `apikey_auth blocked reason=service_account_disabled`.
- **Find keys that never expire or expire soon** (the listing is per account,
  `GET /auth/service-accounts/{id}/keys?status=active&never_expires=true`); across accounts,
  in SQL:
  `select service_account_id, key_prefix, expires_at from api_keys where revoked_at is null and (expires_at is null or expires_at < now() + interval '30 days')`.

## Enable an idle timeout on a live fleet

Authentication v0.32.0, stores pgx v0.16.0 / Turso v0.15.0 (SOC 2 S4d part 2). Ends a
first-party session nobody has refreshed within `IdleTimeout`.

1. **Repin** core v0.32.0 and the store (pgx v0.16.0 or Turso v0.15.0); a host with its own
   `SessionRepository` implements `session.ActivityRepository` or leaves idle off.
2. **Apply migration 0029** (`sessions.last_rotated_at`, nullable; the baseline ledger copy is
   0035) with `make migrate`, pre-boot. It is required only once idle is set: unset, the
   column is never read and an unmigrated database boots.
3. **Set `AUTH_IDLE_TIMEOUT`** (`SessionsConfig.IdleTimeout`) above `AccessTokenTTL` (at or
   below refuses boot; under twice WARNs `idle_granularity`) and below `RefreshTTL` (at or
   above WARNs `idle_never_fires`). Set without 0029, the host refuses to boot naming
   `0029_session_activity.sql`.
4. **Roll the fleet.** The boot line reads `idle_timeout=<d> idle_source=config`.
- **Expected logout wave: zero.** Under the grandfather rule a session rotated while idle was
  off (rotations, no stamp) is not judged until its first refresh after the switch stamps
  it; only a session never refreshed is judged on `created_at`, and only once it is really
  that old. `RefreshTTL` still bounds every session.
- **Recorded:** `session_idle_expired` once per deleted row; the client sees the same generic
  401 as any refresh denial. Read them with
  `GET /auth/admin/security-events?event_type=session_idle_expired`.
- **What it does not cover:** delegated OAuth2 connections (`OAuth2Config.SessionTTL`); an
  outstanding access JWT, which lives up to `AccessTokenTTL` on `Stateless()` routes and
  default-tier safe reads; a client that refreshes on a timer, which is never idle.
- **Turning it off:** unset `AUTH_IDLE_TIMEOUT`; rotation stops stamping and nothing is judged.
  Before turning it on again, read the next runbook.

## Re-enable an idle timeout after a gap

While idle is off, rotation leaves `last_rotated_at` as it was, so the stamps age. Re-enabling
judges those old stamps: every session refreshed during the gap is logged out once at its next
refresh. To avoid that wave, reset the stamps BEFORE setting `AUTH_IDLE_TIMEOUT` again, so the
grandfather rule applies afresh:

```sql
UPDATE sessions SET last_rotated_at = NULL WHERE session_profile = 'first_party';
```

**The NULL reset is the only supported manual write to `last_rotated_at`.** Never set it to a
time: the column is the idle clock, and the store writes it only through the compare-and-swap
rotation.

## Migration 0029 lock timeout: re-run

0029 takes an `ACCESS EXCLUSIVE` lock on `sessions`, the busiest authentication table, and on
pgx opens with `SET LOCAL lock_timeout = '5s'`. Behind a long reader it fails after about 5 s
(`55P03`, lock not available) and records nothing in the ledger. **Re-run `make migrate`**
(at a quieter moment if it repeats); the file is idempotent (`ADD COLUMN IF NOT EXISTS`). On
Turso the `ALTER` briefly holds the database write lock. The only supported manual write to
the new column is the NULL reset above.

## An abusive IP

There is no bundled block list. What exists:

- Per-IP budgets on login and token issuance (30/min), reset starts (20/min), sensitive codes
  (30/min), refresh (30/min) and invitation decline (10/min), keyed on the
  `web.TrustProxies`-resolved client IP. Behind a proxy without `TrustProxies` every client
  shares the proxy's budget.
- The budgets fail closed on a limiter outage, except the HTTP refresh and decline budgets,
  which fail open.
- Blocking is the edge's job (WAF, load balancer). Register, verify, reset, token and the
  native OAuth routes have no HTTP-level per-IP throttle today (planned, S5).

## Read the authentication audit rail

`security_events` is written by the authentication service when `Repositories.SecurityEvents`
is wired. Read it through the repository:

```go
page, err := repos.SecurityEvents.List(ctx, securityevent.ListFilter{
    UserID: id,            // optional
    EventType: "login",    // optional
    EventStatus: "failure",// optional
    Since: from, Until: to,// optional, Until exclusive
}, list.Request{})
// ordered created_at DESC, id DESC; cursor-paginated
```

Rows carry `user_id`, `actor_type`/`actor_id`, `event_type`, `event_status`, `details`
(identifiers and key prefixes, never secrets), `ip_address`, `user_agent`, `created_at`
(the application clock, UTC). Since S2, rows also carry `request_id` and `trace_id`, which
join a row to the request's logs and trace (blank when the request carried none, or when the
recorder refused an id that was not printable ASCII of at most 128 bytes).

**Over HTTP (since authentication v0.30.0).** With `AdministrationConfig.SecurityEventsRoute`
on, `GET /auth/admin/security-events` serves the same listing to an admin the host's
`UserAdminCheck` admits for the `read-audit` action (switch on `Action` before opting in), at
AAL2 under `MFARequired: admins`:

```sh
curl -s "$HOST/auth/admin/security-events?event_type=login&event_status=failure&since=2026-10-01T00:00:00Z&limit=100" -H "$AUTH"
```

Filters `user_id`, `event_type`, `event_status`, `since` (inclusive), `until` (exclusive);
cursor paging only. **Export is paging:** keep the filters fixed, follow `next_cursor` at
`limit=100` until `has_more` is false. Every page read leaves one `audit_read` row naming the
reader and the filter keys used, so the export is itself on the rail.

`examples/auth-cms` also has `GET /debug/security-events` behind `AUTH_DEBUG=1` (refused
outside development); it requires a live session holding the platform-admin permission
(`admin` on `platform:main`) and is a harness feature — do not copy it into a host.

## Read authorization change history

With `WithAudit()` on the store and `Repositories.Audit` wired:

```go
page, err := repos.Audit.List(ctx, audit.Filter{
    ResourceType: "project", ResourceID: pid, // both or neither
    SubjectType: "user", SubjectID: uid,      // both or neither
    ActorType: "user", ActorID: adminID,      // both or neither
}, list.Request{})
// ordered occurred_at DESC then id
```

Each record is one committed fact delta (`added` or `removed`) with its source (actor pair or
system, optional reason) and time. Refusals and denials are not in this history: since S2 the
decision observer reports them (`authorization_denied`, `role_write_refused`), and a host that
wires the audit bridge records them on the security-event rail (above).

**Over HTTP (since authorization v0.31.0).** With `RoleRoutes.AuditGate` wired (a
global-history gate: platform admin or auditor), `GET /authorization/audit` takes the same
three filter pairs and pages by cursor; export it the same way (`limit=100`, follow
`next_cursor` until `has_more` is false). With `WithAuditReadObserver` bridged into the
security-event rail, each read leaves an `audit_read` row with `rail: authorization`.

```sh
curl -s "$HOST/authorization/audit?resource_type=project&resource_id=$ID&limit=100" -H "$AUTH"
```

The listing recipes for a periodic access review are in `pockets/authorization/README.md`,
"Access review".

## Retention

The pockets ship bounded disposal calls and start none of them; the host runs each from its own
schedule, one runner instance per database. The lifetimes, defaults and the floor are in
*Production posture*, "Retention".

| Data | Call | Schedule |
|---|---|---|
| sessions, OAuth states, grants, contact changes, invitations, challenges, OAuth2 rows, `security_events` | `Components.Maintenance.Run(ctx)` (blocks; a jittered first pass, then one an hour), or `Components.Maintenance.Pass(ctx)` from the host's own ticker or a jobs schedule — the three-line handler in `pockets/authentication/README.md` | hourly; the rail step runs once a day (`SecurityEventStepInterval`) and reports `not_due` between |
| `iam_operations` | `Components.Mutations.ExpireOperations(ctx, limit)` until it returns less than `limit` | hourly |
| ordinary `job_queue` | `Components.Queue.PurgeTerminalJobs(ctx, before, limit)` | hourly; `before` behind the scheduler's longest retry window |
| fenced job rows (terminal, incl. sealed delivery envelopes) | `Components.Queue.PurgeTerminal(ctx, before, limit)` | host scheduler |
| published events (`event_outbox`) | `outbox.PurgePublished(ctx, before)` — unbounded: its own step, after a one-off manual drain | host scheduler |
| cms `inquiries` | `PurgeBefore(ctx, before, limit)` on the `messaging.InquiryEraser` the host wired as `Repositories.InquiryMaintenance` | host scheduler |
| `ratelimit_windows` (pgx limiter) | `DELETE FROM ratelimit_windows WHERE expires_at < now();` | host cron |
| `iam_audit`, `secrets_audit` | the host's definer function (*Production database posture*, "Definer functions") | host cron |

Set `MaintenanceRunnerAcknowledged: true` once the authentication runner runs; production WARNs
while it is false. `Pass` never cancels a running statement: it checks `PassBudget` (5m) between
batches, and a budget that runs out pass after pass WARNs `maintenance_budget_exhausted` — the
first enablement on an old host drains its backlog over many passes, so watch WAL and replica lag.
Two concurrent passes are safe, not cheap; a multi-replica host runs the runner on one replica.

**Where the evidence is.**

- One `msg=maintenance_step step=<name> count=<n> skipped=<bool> error_kind=<kind>` line per step
  per pass, and `LastReport()` for a health surface.
- `security_events_purged` rows (`actor_type='system'`, `actor_id='maintenance'`; Details
  `cutoff`, `count`, `floor_days`, `max_rows`, and on pgx `session_user`): one per purge call, a
  zero count included — the evidence that the control ran on a day with nothing to purge.
- `security_events_archived` rows (Details `object`, `sha256`, `rows`, `first_created_at`,
  `first_id`, `last_created_at`, `last_id`, `archive_location`): one per archive object, written
  before its chunk is purged.
- The archive objects themselves, NDJSON at `<ArchivePrefix><cutoff>/<id>.ndjson`.
- On a jobs schedule, the schedule's `last_run_at`.

Dashboards filter `actor_type='system'`. The canonical alert: **no `security_events_purged` row
in 48 h**.

**Verify the archive.** Fetch each object an archived row names, check its SHA-256 and line count
against `sha256` and `rows`, and compare, per cutoff, the sum of `rows` over the archived rows
with the sum of `count` over the purged rows. The archive's own lifecycle — retention, backup,
access control, object lock on a real sink — is host policy, and archived rows are outside
erasure's reach. A failed archive purges nothing that pass (`maintenance.ErrArchiveFailed`); an
object already written is left for reconciliation.

**After a restore.** `pg_restore -x` drops the grants: revoke `EXECUTE` on
`security_events_purge_before` from `PUBLIC` and grant it to the runtime role again (the boot
refuses a PUBLIC-executable definer). After a restore into a renamed schema, re-run the pin `DO`
block of `0024_security_events_retention.sql`.

## Erase a person

Erasure is irreversible and composes across pockets; run every step and record each.

1. **Settle any accepting invitation claim** for the person first: the erase refuses with 409
   (`user.ErrEraseBlockedByClaim`) while one is held, and writes nothing.
2. **Erase the account**: `POST /auth/admin/users/{id}/erase` (mounted only when
   `Repositories.UserErasure` is wired and `AdministrationConfig.UserErasureRoute` is true; the
   host check receives `UserAdminErase`; 400 on self) or `Service.EraseUser(ctx, actor, id)`. One
   transaction marks the user erased, blanks and retires every identifier, deletes credentials,
   sessions, grants, challenges and the act-as-user service accounts with their keys, removes or
   blanks the invitations addressed to the person, and appends `user_erased` with the counts. A
   retry is a 200 with `changed:false`. The `users` row and every pseudonymous id survive.
3. **Remove the authorization facts** for `user:<id>`: list them with
   `Roles.ListRoleAssignmentsBySubject` and remove each through the bundled role routes or
   `Mutations.UnassignRole`/`RevokeRelationship` — an executed step, not a note.
4. **Delete the person's inquiries**: `DeleteByEmail(ctx, email)` on the wired cms eraser (ASCII
   case-fold equality), for each address the person used.
5. **Purge sealed delivery rows**: `Queue.PurgeTerminal` for the terminal fenced jobs that carried
   the person's messages.
6. **Out of reach, by design**: archive objects already written; `iam_operations` fingerprints,
   which expire under `ExpireOperations`; `security_events` rows, which keep their IP and
   User-Agent and age out at the rail's retention.

Then **sweep**: as the migrator, count the rows of every table whose text or JSON columns contain
the person's email or last IP:

```sql
DO $sweep$
DECLARE
  needles pg_catalog.text[] := ARRAY['<email>', '<last ip>'];
  needle pg_catalog.text;
  col record;
  n pg_catalog.int8;
BEGIN
  FOR col IN
    SELECT c.table_schema, c.table_name, c.column_name
      FROM information_schema.columns c
      JOIN information_schema.tables t ON t.table_schema = c.table_schema AND t.table_name = c.table_name
     WHERE t.table_type = 'BASE TABLE'
       AND c.table_schema NOT IN ('pg_catalog', 'information_schema')
       AND c.data_type IN ('text', 'character varying', 'json', 'jsonb')
  LOOP
    FOREACH needle IN ARRAY needles LOOP
      EXECUTE pg_catalog.format('SELECT count(*) FROM %I.%I WHERE %I::text ILIKE %L',
        col.table_schema, col.table_name, col.column_name, '%' || needle || '%') INTO n;
      IF n > 0 THEN
        RAISE NOTICE '%.%.%: % rows contain %', col.table_schema, col.table_name, col.column_name, n, needle;
      END IF;
    END LOOP;
  END LOOP;
END
$sweep$;
```

Expect no notice except on `security_events`, whose rows within retention keep the IP (and, on
rows older than AUDIT-054, the raw address). The other documented carve-outs hold no plaintext
match: `oauth_refresh_history` keeps hashes, `iam_operations` keeps fingerprints, and archive
objects live outside the database. Erase is not a ban: the next external login provisions a fresh
user; deactivate to keep someone out.

## Tenants and offboarding

Tenant isolation is host-owned: one database or schema per tenant, never a row-level
predicate the framework enforces. Offboarding a tenant means dropping its database and
destroying the vault owner that held its credentials (`Vault.DeleteOwner`), then rotating the
secrets master ring if backups must be defeated (*Keys and rotation*). Ambient transactions
are per `*DB` (pgxdb v0.15.0, datastores/turso v0.10.0): a repository bound to one database,
called inside another database's transaction, runs on its own database's pool. The two never
share a transaction, so there is no atomicity between control-plane and tenant work; see
*Persistence*, "Two databases never share a transaction". Symptom of nesting two `*DB` over
ONE database: requests stall in pool acquire, or sit `idle in transaction` on the outer
instance while the inner pool saturates. Build those stores over one `*DB`, or set
`DB_LOCK_TIMEOUT` and do not nest the pair.
