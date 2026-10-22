---
title: Operations runbooks
description: Deactivate a user, revoke sessions, handle an abusive IP, read the audit rails, and what retention exists today.
---

# Operations runbooks

Procedures a host operator runs with what the framework ships on `main` at 2026-10-22.
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
- **Not available:** deletion, anonymization or erasure. `status` is `active` or
  `deactivated` only; there is no erase path and no foreign keys to cascade one (planned, S3).

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
(the application clock, UTC). There is no request id on a row today (planned, S2).

No bundled HTTP route exposes the rail. `examples/auth-cms` has `GET /debug/security-events`
behind `AUTH_DEBUG=1`; it is gated by any authenticated session and is a harness feature —
do not copy it into a host.

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
system, optional reason) and time. Refusals and denials are not recorded (planned, S2). The
listing recipes for a periodic access review are in `pockets/authorization/README.md`,
"Access review".

## Retention today

| Data | Purge API | Who runs it |
|---|---|---|
| challenges (codes, link tokens) | `Challenges.PurgeExpired(ctx, before, limit)` | nothing in the framework or examples calls it |
| OAuth2 authorization-server codes, clients, refresh history | `OAuth2.Prune(ctx, now)` | nothing calls it |
| published events (`event_outbox`) | `outbox.PurgePublished(ctx, before)` | host scheduler |
| fenced job rows (terminal) | `jobs.Service.PurgeTerminal(ctx, before, limit)` | host scheduler |
| sessions (expired rows keep their refresh-token hash), `oauth_states` (abandoned flows keep the PKCE verifier), expired grants, `contact_changes`, invitations (no DELETE exists; invitee address persists), retired identifiers, `security_events`, `iam_operations` ("never deleted in v1"), ordinary `job_queue` rows (plaintext payloads), cms `inquiries` (third-party PII) | **none** | — (a maintenance port and schedule are planned, S3) |

Define the retention period per table in the host's policy and, until S3, enforce it with
host SQL against the tables above. Keep `iam_operations` rows for at least every producer's
retry window.

## Tenants and offboarding

Tenant isolation is host-owned: one database or schema per tenant, never a row-level
predicate the framework enforces. Offboarding a tenant means dropping its database and
destroying the vault owner that held its credentials (`Vault.DeleteOwner`), then rotating the
secrets master ring if backups must be defeated (*Keys and rotation*). The one framework
hazard for a multi-database host is `pgxdb.QuerierFrom`: it returns the ambient transaction
to any `*DB`, so a repository bound to one database called inside another database's
transaction runs against the wrong one. Keep control-plane and tenant work in separate
transactions until the fix lands.
