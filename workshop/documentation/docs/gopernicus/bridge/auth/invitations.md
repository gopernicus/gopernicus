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

## Route Registration

```go
group := handler.Group("/invitations")
ib.AddHttpRoutes(group)
```

Unlike the authentication bridge, invitations wires its own middleware inline — authentication, authorization, rate limiting, and body size limits are applied per-route inside `AddHttpRoutes`.

## Endpoints

### Resource-Scoped (Manage Permission Required)

These operate on a specific resource (tenant, project, etc.). The resource type and ID come from the URL path, so authorization is checked in-handler rather than via middleware — the resource type isn't known at middleware construction time.

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

**Resource-scoped** (create, list by resource): The handler calls `authorizer.Check()` directly, because `resource_type` is dynamic — it could be `tenant`, `project`, or any resource type. The caller needs `manage` permission on the target resource.

**Invitation-scoped** (cancel, resend): Uses `AuthorizeParam` middleware with `"invitation"` as the resource type and `"manage"` as the permission. The invitation's authorization relationship is set up when the invitation is created (through-relation to the parent resource).

## Create Behavior

The create endpoint supports two modes via the `auto_accept` flag:

- **`auto_accept: false`** (default) — creates a pending invitation that the recipient must accept
- **`auto_accept: true`** — if the identifier matches a known user, adds them directly (no invitation created). If unknown, creates an invitation that auto-accepts when the user registers and verifies their email

### Redirect URL Validation

The create request accepts an optional `redirect_url` field specifying where the invitation accept flow should land. This URL must be validated against `ALLOWED_FRONTENDS` before being passed to core — the same origin allow-list used for password reset URLs.

When `ALLOWED_FRONTENDS` is configured, the bridge should:
- Validate `redirect_url` origin against `allowlist.Matcher`
- Return `400 invalid redirect_url origin` if validation fails
- Pass the validated URL to `CreateInput.RedirectURL`

When `ALLOWED_FRONTENDS` is empty (legacy/dev mode), the bridge may skip validation or require explicit opt-in. The `redirect_url` is persisted on the invitation row and included in `InvitationSentEvent` — on resend, the original URL is restored from the row.

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

`Subscribers` listens for email verification events and auto-resolves pending invitations:

| Event | Handler | Purpose |
|---|---|---|
| `user.email_verified` | `handleEmailVerified` | Looks up the user, then calls `Inviter.ResolveOnRegistration()` to accept matching pending invitations |

When a user verifies their email, any pending invitations sent to that email address are automatically resolved — the user is added to the resource with the invited relation. The subscriber listens for the `user.email_verified` data event emitted by the users repository.

```go
subs := invbridge.NewSubscribers(inviter, userRepo, log)
subs.Register(bus)
```

## Files

| File | Purpose |
|---|---|
| `bridge.go` | `Bridge` struct, `New()` constructor, options |
| `http.go` | `AddHttpRoutes()`, all handlers, in-handler authorization helper |
| `model.go` | Request/response types |
| `subscribers.go` | Event subscriber for auto-resolving invitations on email verification |
