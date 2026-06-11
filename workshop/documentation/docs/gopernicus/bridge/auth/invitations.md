---
sidebar_position: 2
title: Invitations
---

# Bridge — Invitations

The invitations bridge (`bridge/auth/invitations/`) exposes HTTP endpoints for creating, accepting, declining, cancelling, and resending resource invitations. It is a hand-written case bridge that translates HTTP requests into calls to the [Core Invitations](../../core/auth/invitations.md) package.

## Construction

```go
ib := invitations.New(log, inviter, authorizer, authenticator, rateLimiter)
```

Options:
- `WithJSONErrorRenderer(r)` — override the default JSON error renderer used by middleware
- `WithHTMLErrorRenderer(r)` — set an HTML error renderer for server-rendered routes
- `WithAllowedFrontends(origins)` — origin allow-list for `redirect_url` validation on create; use the same list the authentication bridge gets (typically `ALLOWED_FRONTENDS`)

## Route Registration

```go
group := handler.Group("/invitations")
ib.AddHttpRoutes(group)
```

Unlike the authentication bridge, invitations wires its own middleware inline — authentication, authorization, rate limiting, and body size limits are applied per-route inside `AddHttpRoutes`.

## Endpoints

### Resource-Scoped (Manage Permission Required)

These operate on a specific resource (tenant, project, etc.). The resource type and ID come from the URL path; `AuthorizeDynamicParam` middleware reads both params at request time and checks `manage` permission on the target resource.

| Method | Path | Handler | Purpose |
|---|---|---|---|
| POST | `/{resource_type}/{resource_id}` | `httpCreate` | Create invitation for resource |
| GET | `/{resource_type}/{resource_id}` | `httpListByResource` | List invitations for resource |

### Invitation-Scoped (Manage Permission via Middleware)

Authorization is handled by `AuthorizeParam` middleware against the `invitation` resource type.

| Method | Path | Handler | Purpose |
|---|---|---|---|
| POST | `/{invitation_id}/cancel` | `httpCancel` | Cancel a pending invitation |
| POST | `/{invitation_id}/resend` | `httpResend` | Resend invitation notification |

### Self-Service (Authenticated)

| Method | Path | Handler | Purpose |
|---|---|---|---|
| GET | `/mine` | `httpListMine` | List invitations for the current user |
| POST | `/accept` | `httpAccept` | Accept invitation by token |

Both use `WithUserSession()` to load the full user from the database — accept needs the user's email to verify the invitation identifier.

### Public

| Method | Path | Handler | Purpose |
|---|---|---|---|
| POST | `/{invitation_id}/decline` | `httpDecline` | Decline invitation (identifier verified in handler) |

Decline is public because the recipient may not have an account yet. The request body includes the `identifier` (e.g., email), which the handler verifies against the invitation.

## Authorization Model

Invitations use two authorization strategies depending on the endpoint:

**Resource-scoped** (create, list by resource): `AuthorizeDynamicParam` middleware resolves `resource_type` and `resource_id` from the URL at request time — the resource type is dynamic (`tenant`, `project`, etc.), so it can't be fixed at middleware construction. The caller needs `manage` permission on the target resource.

**Invitation-scoped** (cancel, resend): Uses `AuthorizeParam` middleware with `"invitation"` as the resource type and `"manage"` as the permission. The invitation's authorization relationship is set up when the invitation is created (through-relation to the parent resource).

## Create Behavior

The create endpoint supports two modes via the `auto_accept` flag:

- **`auto_accept: false`** (default) — creates a pending invitation that the recipient must accept
- **`auto_accept: true`** — if the identifier matches a known user, adds them directly (no invitation created). If unknown, creates an invitation that auto-accepts when the user registers and verifies their email

### Redirect URL Validation

The create request accepts a `redirect_url` field specifying where the invitation accept flow should land, validated against the origin allow-list configured via `WithAllowedFrontends` — the same list the authentication bridge uses.

- **Strict mode** (allow-list configured): `redirect_url` is required and its origin must match the list; otherwise the bridge returns `400`.
- **Legacy mode** (no allow-list): `redirect_url` passes through unvalidated, including empty.

The validated URL is forwarded to `CreateInput.RedirectURL`, persisted on the invitation row, and carried on `InvitationSentEvent` / `MemberAddedEvent` — on resend, the original URL is restored from the row.

The response indicates which path was taken:

```json
{
  "directly_added": true,
  "invitation": null
}
```

or:

```json
{
  "directly_added": false,
  "invitation": { "invitation_id": "...", "status": "pending", ... }
}
```

## Event Subscribers

`Subscribers` auto-resolves pending invitations and (optionally) delivers invitation emails:

| Event | Handler | Purpose |
|---|---|---|
| `user.email_verified` | `handleEmailVerified` | Looks up the user, then calls `Inviter.ResolveOnRegistration()` to accept matching pending invitations |
| `invitation.sent` | `handleInvitationSent` | Delivers the invitation email — `invitations:invite` (token accept link) or `invitations:shared` (auto-accept, direct link) |
| `member.added` | `handleMemberAdded` | Notifies an existing user added directly to a resource (`invitations:member_added`) |

The email handlers are only registered when an emailer is wired via `WithEmailer` — without it, only auto-resolve runs. Each email's frontend origin travels on the event itself (`RedirectURL`, validated at create); events missing it are logged and skipped.

The user lookup is defined on the authentication engine's `User` type, so the generated users satisfier plugs straight in — no project-local adapter:

```go
userSat := satisfiers.NewUserSatisfier(authRepos.Users)
subs := invbridge.NewSubscribers(inviter, userSat, log,
    invbridge.WithEmailer(mailer),
    invbridge.WithResourceNameResolver(func(ctx context.Context, rtype, rid string) string {
        // best effort: "" falls back to the resource type label
        return lookupDisplayName(ctx, rtype, rid)
    }),
    invbridge.WithDestinationPathResolver(func(ctx context.Context, rtype, rid string) string {
        // relative path only; joined to the event's validated origin
        return "/" + rtype + "/" + rid
    }),
)
subs.Register(bus)
```

`init` scaffolds the three email templates under `app/server/emails/templates/invitations/` when the authorization feature is selected. Resolver paths must be relative (`/...`) — anything carrying a scheme or host is rejected to prevent open redirects, falling back to the origin root.

## Files

| File | Purpose |
|---|---|
| `bridge.go` | `Bridge` struct, `New()` constructor, options |
| `http.go` | `AddHttpRoutes()`, all handlers, in-handler authorization helper |
| `model.go` | Request/response types |
| `subscribers.go` | Event subscribers: auto-resolve on email verification, invitation + member-added email delivery |
