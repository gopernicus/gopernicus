# Auth Architecture Overview

How authentication, authorization, and invitations fit together in a Gopernicus application.

## Three Pillars

The auth system consists of three packages under `core/auth/`:

| Package | Responsibility | Key Struct |
|---|---|---|
| `authentication` | Identity verification: login, registration, sessions, tokens, OAuth, API keys | `Authenticator` |
| `authorization` | Permission checks: relationship-based access control (ReBAC) | `Authorizer` |
| `invitations` | Resource invitations: invite by email, token verification, auto-accept on registration | `Case` |

All three are framework-provided code in `core/auth/`. Your application does not implement these from scratch. Instead, you configure them with dependencies, connect them to your generated repositories via satisfiers, and wire them into your HTTP layer with middleware.

## The Authenticator

`authentication.Authenticator` owns all identity verification flows. It is constructed with dependency injection:

```go
authenticator := authentication.NewAuthenticator(
    authentication.NewRepositories(users, passwords, sessions, tokens, codes),
    hasher,   // PasswordHasher (cryptids/bcrypt)
    signer,   // JWTSigner (cryptids/golangjwt)
    bus,      // events.Bus for email delivery
    cfg,      // Config populated from environment
    authentication.WithOAuth(providers, oauthRepo),
    authentication.WithAPIKeys(apiKeyRepo),
    authentication.WithSecurityEvents(securityEventsRepo),
)
```

The Authenticator defines its own repository interfaces (`UserRepository`, `SessionRepository`, `PasswordRepository`, etc.) and crypto interfaces (`PasswordHasher`, `JWTSigner`). It never imports your generated repository types directly.

## The Authorizer

`authorization.Authorizer` evaluates permission checks against a schema that maps relations to permissions. It is constructed with a store, a schema, and configuration:

```go
schema := authorization.NewSchema(
    authRepos.AuthSchema(),
    rebacRepos.AuthSchema(),
)
authorizer := authorization.NewAuthorizer(store, schema, cfg)
```

The Authorizer defines a `Storer` interface for relationship persistence and querying. The schema is built by composing `[]ResourceSchema` slices from each domain.

## The Inviter

`invitations.Inviter` provides resource invitation logic. It depends on an invitation repository, the Authorizer (to create ReBAC relationships when invitations are accepted), and an event bus (to emit `InvitationSentEvent` for email delivery):

```go
inviter := invitations.NewInviter(
    invitationRepo,
    authorizer,
    bus,
    invitations.WithUserLookup(lookupFn),
    invitations.WithMemberCheck(memberCheckFn),
)
```

The invitation flow:
1. `Create` -- generates a token, stores the invitation, emits `InvitationSentEvent`
2. Invitee clicks the link -- `Accept` verifies the token, creates a ReBAC relationship on the target resource
3. On email verification -- `ResolveOnRegistration` auto-accepts pending invitations with `AutoAccept=true`

## Satisfiers: The Adapter Pattern

The framework packages define their own minimal interfaces (e.g., `authentication.UserRepository`, `authorization.Storer`). Your generated repositories have different method signatures. Satisfiers bridge the gap.

Satisfiers live in:
- `core/auth/authentication/satisfiers/` -- adapts generated repos to authentication interfaces
- `core/auth/authorization/satisfiers/` -- adapts the generated `rebac_relationships` repo to the `Storer` interface

Example: `UserSatisfier` wraps the generated `users.Repository`, mapping between the generated `users.User` type and the framework's `authentication.User` type. The compile-time check `var _ authentication.UserRepository = (*UserSatisfier)(nil)` guarantees the adapter stays in sync.

This pattern follows the hexagonal architecture principle: the framework core accepts interfaces, and adapters in the scaffolded layer satisfy them.

## Wiring in the App Layer

The generated `server.go` (app layer) wires everything together:

1. Create satisfiers from generated repositories
2. Construct `Repositories` from satisfiers
3. Build the `Authenticator` with repositories, crypto, event bus, and options
4. Compose the authorization `Schema` from all domain `AuthSchema()` functions
5. Build the authorization `Storer` from the satisfier wrapping the generated relationships repo
6. Construct the `Authorizer` with the store, schema, and config
7. Pass both to the HTTP router, where middleware applies authentication and authorization

## Request Lifecycle

A typical authenticated and authorized request passes through:

1. `httpmid.Authenticate` -- extracts the Bearer token, validates it (JWT signature or API key hash), sets subject in context
2. `httpmid.AuthorizeParam` -- reads the subject from context, reads the resource ID from the URL, calls `Authorizer.Check`, returns 403 if denied
3. Handler -- reads `httpmid.GetSubjectID(ctx)` or `httpmid.GetUser(ctx)` as needed

For list operations, handlers call `Authorizer.LookupResources` to get authorized resource IDs, then pass them to the repository query as a prefilter.

## Events

Authentication emits events via the event bus for email delivery:

| Event | Purpose |
|---|---|
| `auth.verification_code_requested` | Send 6-digit code after registration |
| `auth.password_reset_requested` | Send reset link with token |
| `auth.oauth_link_verification_requested` | Send code for OAuth link confirmation |
| `auth.email_verified` | Trigger post-verification flows (e.g., resolve invitations) |
| `auth.user_deletion_requested` | Cascade data cleanup |

Invitations emit `invitation.sent` and `member.added` events.

Event subscribers in the bridge layer handle email rendering and sending. The Authenticator and invitation Case are write-only event producers.

## Related

- [Authentication](authentication.md)
- [Authorization](authorization.md)
- [Auth Schema Definition](schema-definition.md)
- [Auth Middleware](middleware.md)
- [Core Layer](../layers/core.md)
