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

No bundled HTTP route exposes the rail. `examples/auth-cms` has `GET /debug/security-events`
behind `AUTH_DEBUG=1` (refused outside development); it requires a live session holding the
platform-admin permission (`admin` on `platform:main`) and is a harness feature — do not copy
it into a host.

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
wires the audit bridge records them on the security-event rail (above). The
listing recipes for a periodic access review are in `pockets/authorization/README.md`,
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
secrets master ring if backups must be defeated (*Keys and rotation*). The one framework
hazard for a multi-database host is `pgxdb.QuerierFrom`: it returns the ambient transaction
to any `*DB`, so a repository bound to one database called inside another database's
transaction runs against the wrong one. Keep control-plane and tenant work in separate
transactions until the fix lands.
