---
title: Auth Overview
---

# Auth Overview

Gopernicus ships with a complete authentication, authorization, and invitation system. These are framework-provided packages — not generated — that applications import, configure, and wire into their bridge layer.

This page is a quick orientation. Each component has detailed documentation linked below.

## The three pieces

### Authentication — who are you?

The `Authenticator` handles identity: registration, login, sessions, password management, OAuth, API keys, and security events.

It supports multiple credential types simultaneously:
- **Email + password** — registration with email verification, NIST-compliant password policy
- **OAuth** — Google, GitHub, or any OIDC provider, with PKCE and account linking
- **API keys** — SHA256-hashed keys for service accounts
- **JWT + sessions** — short-lived access tokens with refresh token rotation and reuse detection

The bridge layer exposes these as HTTP endpoints with dual-client support — cookies for web, JSON tokens for mobile/API.

**Key design choice:** Authentication emits events (`VerificationCodeRequested`, `PasswordResetRequested`) instead of sending emails directly. The bridge layer subscribes to these events and handles delivery. This keeps the core free of transport concerns.

→ [Core — Authentication](../core/auth/authentication.md) · [Bridge — Authentication](../bridge/auth/authentication.md)

### Authorization — what can you do?

The `Authorizer` implements relationship-based access control (ReBAC), inspired by Google's [Zanzibar](https://research.google/pubs/zanzibar-googles-consistent-global-authorization-system/) paper — the same model behind SpiceDB, OpenFGA, and Authzed.

Instead of assigning roles or permissions directly, you model **relationships** between subjects and resources, then define a **schema** that maps those relationships to permissions:

```go
schema := authorization.Schema{
    ResourceTypes: map[string]authorization.ResourceTypeDef{
        "project": {
            Relations: map[string]authorization.RelationDef{
                "owner":  {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}}},
                "member": {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}}},
            },
            Permissions: map[string]authorization.PermissionRule{
                "read":   authorization.AnyOf(authorization.Direct("owner"), authorization.Direct("member")),
                "edit":   authorization.AnyOf(authorization.Direct("owner")),
                "delete": authorization.AnyOf(authorization.Direct("owner")),
            },
        },
    },
}
```

A permission check asks: *"Does this subject have this permission on this resource?"* The authorizer traverses the relationship graph — following through-relations across resource boundaries — to answer.

**Key operations:**

| Operation | What it does | Used for |
|---|---|---|
| `Check` | Does subject have permission on resource? | Single-resource endpoints (Get, Update, Delete) |
| `CheckBatch` | Batch of Check calls | Bulk validation |
| `FilterAuthorized` | Which of these IDs can subject access? | Post-filtering list results |
| `LookupResources` | All resource IDs subject can access | Pre-filtering list queries |

**Through-relations** are what make ReBAC powerful. If a user is a `member` of an `org`, and the org has an `owner` relation on a `project`, the user can inherit permissions on the project through that chain:

```go
"read": authorization.AnyOf(
    authorization.Direct("owner"),
    authorization.Direct("member"),
    authorization.Through("org", "read"),  // traverse org relation, check read on target
),
```

The bridge layer wires authorization as middleware. Generated `bridge.yml` configurations specify the pattern (check, prefilter, or postfilter) per route. The core layer never calls the authorizer — that's the bridge's job.

→ [Core — Authorization](../core/auth/authorization.md) · [Bridge — Middleware](../bridge/middleware.md)

### Invitations — join a resource

The `Inviter` handles adding members to resources: create, accept, decline, cancel, resend, and auto-resolution on registration.

The invitation flow:

1. **Create** — if the identifier matches a known user and auto-accept is on, creates the relationship immediately. Otherwise, generates a 32-character token (SHA256-hashed for storage), sets a 7-day expiry, and emits an `InvitationSentEvent` with the plaintext token.
2. **Accept** — validates the token, creates the ReBAC relationship, marks the invitation as accepted.
3. **Auto-resolve on registration** — when a new user verifies their email, pending invitations with auto-accept are resolved automatically.

Invitations are generic over resource type — the same system handles org invitations, project invitations, team invitations, or anything else with a ReBAC relationship.

→ [Core — Invitations](../core/auth/invitations.md) · [Bridge — Invitations](../bridge/auth/invitations.md)

## How they wire together

In `app/server.go`, the composition root connects these pieces:

```
Authenticator ← repos, hasher, signer, bus, config
Authorizer    ← store (wrapping rebac_relationships repo), schema, config
Inviter       ← invitations repo, authorizer, bus

Bridge middleware:
  Authenticate → validates credentials, sets subject in context
  Authorize    → checks permission via authorizer
  Handlers     → call authenticator/inviter for auth-specific flows
```

**Authorization lives in the bridge, not the core.** Cases and repositories never call the authorizer. This keeps business logic independent of access control — you can test cases without authorization, change authorization rules without touching domain code, and compose authorization differently across bridges.

## Feature flags

All three systems are opt-in via `gopernicus.yml`:

```yaml
features:
  authentication: gopernicus   # or false to disable
  authorization: gopernicus
```

When enabled, `gopernicus init` scaffolds the database migrations, repository packages, and app wiring. When disabled, the auth middleware and bridge packages are excluded from generation.

## Related docs

| Layer | Doc | Covers |
|---|---|---|
| Core | [Authentication](../core/auth/authentication.md) | Authenticator API, flows, satisfiers, events |
| Core | [Authorization](../core/auth/authorization.md) | Schema DSL, Check, LookupResources, CacheStore |
| Core | [Invitations](../core/auth/invitations.md) | Inviter API, token security, auto-resolution |
| Bridge | [Authentication](../bridge/auth/authentication.md) | HTTP routes, dual-client design, anti-enumeration |
| Bridge | [Invitations](../bridge/auth/invitations.md) | HTTP routes, dynamic resource authorization |
| Bridge | [Middleware](../bridge/middleware.md) | Authenticate, Authorize, context helpers |
| Generation | [Bridge Configuration](./code-generation/bridge-configuration.md) | auth_relations, auth_permissions, auth_create in bridge.yml |
