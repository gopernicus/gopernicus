---
sidebar_position: 3
title: Invitations
---

# Core — Invitations

The invitations package (`core/auth/invitations/`) implements generic resource invitation workflows. It handles the full lifecycle — create, accept, decline, cancel, resend — with support for auto-acceptance, direct member addition for known users, and automatic resolution when a new user verifies their email.

The central type is the `Inviter`. It orchestrates the invitation flow by coordinating the invitations repository, the authorization system (for ReBAC relationship creation), and the event bus (for email delivery).

## Setup

### Constructor

```go
inviter := invitations.NewInviter(
    invitationsRepo,  // *invitationsrepo.Repository
    authorizer,       // *authorization.Authorizer
    bus,              // events.Bus
    invitations.WithUserLookup(lookupFn),
    invitations.WithMemberCheck(memberFn),
)
```

### Options

| Option | Purpose |
|---|---|
| `WithUserLookup(fn)` | Resolve an identifier (email) to a subject — enables the direct-add path for known users |
| `WithMemberCheck(fn)` | Check if a subject is already a member of a resource — prevents duplicate additions |

### Dependency Functions

```go
// Resolve an email to a subject. Return ("", "", nil) if user doesn't exist.
type UserLookup func(ctx context.Context, email string) (subjectType, subjectID string, err error)

// Check if a subject already has a relationship with a resource.
type MemberCheck func(ctx context.Context, resourceType, resourceID, subjectType, subjectID string) (bool, error)
```

Both are optional. Without `UserLookup`, all invitations create pending records regardless of `AutoAccept`. Without `MemberCheck`, duplicate membership is not detected.

## Invitation Lifecycle

### Create

```go
result, err := inviter.Create(ctx, invitations.CreateInput{
    ResourceType:   "org",
    ResourceID:     orgID,
    Relation:       "member",
    Identifier:     "jane@example.com",
    IdentifierType: invitations.IdentifierTypeEmail,
    InvitedBy:      currentUserID,
    AutoAccept:     true,
})
// result.DirectlyAdded — true if user was added without a pending invitation
// result.Invitation    — non-nil if a pending invitation was created
```

The behavior depends on two factors: the `AutoAccept` flag and whether the invitee is a known user.

| AutoAccept | User exists | Behavior |
|---|---|---|
| true | yes | **Direct add** — ReBAC relationship created immediately, `MemberAddedEvent` emitted, no invitation record |
| true | no | **Pending** — invitation created with `AutoAccept=true`, auto-accepted when user verifies email |
| false | yes | **Pending** — invitation created with `ResolvedSubjectID` set, user must explicitly accept |
| false | no | **Pending** — invitation created, user must register then accept |

**Direct add path:** when `AutoAccept` is true and `UserLookup` resolves the email to an existing user, the inviter skips the invitation record entirely. It checks for duplicate membership (if `MemberCheck` is configured), creates the ReBAC relationship directly via the authorizer, and emits a `MemberAddedEvent`.

**Pending invitation path:** generates a 32-character token (URL-safe, no ambiguous characters), stores the SHA256 hash in the database (never the plaintext), sets a 7-day expiry, and emits an `InvitationSentEvent` with the plaintext token for email delivery.

Every pending invitation also creates two ReBAC relationships on the invitation resource itself:
- An **owner** tuple linking the inviter to the invitation
- A **parent** tuple linking the invitation to the target resource

These enable permission checks on invitation management (who can cancel, list, etc.).

### Accept

```go
result, err := inviter.Accept(ctx, invitations.AcceptInput{
    Token:       token,       // plaintext token from the invitation link
    SubjectType: "user",
    SubjectID:   userID,
    Identifier:  "jane@example.com",
})
// result.ResourceType, result.ResourceID, result.Relation
```

Hashes the provided token, looks up the invitation by token hash, validates that:
- The invitation is pending (not already accepted, cancelled, or expired)
- The invitation has not expired (updates status to "expired" if it has)
- The identifier matches the invitation's identifier

On success, creates the ReBAC relationship on the target resource and marks the invitation as accepted.

### Decline

```go
err := inviter.Decline(ctx, invitationID, "jane@example.com")
```

Invitee-initiated rejection. Verifies the identifier matches, then hard-deletes the invitation record and its ReBAC relationships.

### Cancel

```go
err := inviter.Cancel(ctx, invitationID)
```

Inviter-initiated cancellation. Does not require identifier verification (the bridge layer handles authorization). Hard-deletes the invitation record and its ReBAC relationships.

### Resend

```go
invitation, err := inviter.Resend(ctx, invitationID)
```

Generates a new token, resets the expiry to 7 days, and emits a new `InvitationSentEvent`. Works on both pending and expired invitations — an expired invitation is reset to pending status. The invitation ID stays the same; only the token and expiry change.

## Auto-Accept on Registration

```go
count, err := inviter.ResolveOnRegistration(ctx, "jane@example.com", invitations.IdentifierTypeEmail, "user", userID)
```

Called when a user verifies their email (typically via a subscriber on `EmailVerifiedEvent`). Finds all pending invitations for that identifier where `AutoAccept=true`, creates the ReBAC relationships, and marks them as accepted.

Invitations where `AutoAccept=false` are left pending — the user must explicitly accept them. Expired invitations are skipped. Processing is best-effort: if one invitation fails, the others are still attempted.

Returns the count of successfully resolved invitations.

## Listing

```go
// Invitations for a specific resource (e.g., "who's been invited to this org?")
invitations, pagination, err := inviter.ListByResource(ctx, "org", orgID, filter, orderBy, page)

// Invitations for the current user (e.g., "my pending invitations")
invitations, pagination, err := inviter.ListBySubject(ctx, userID, filter, orderBy, page)

// Invitations for an identifier (e.g., "invitations sent to this email")
invitations, pagination, err := inviter.ListByIdentifier(ctx, "jane@example.com", invitations.IdentifierTypeEmail, orderBy, page)
```

## Events

| Event | Emitted when | Payload |
|---|---|---|
| `InvitationSentEvent` (`invitation.sent`) | Pending invitation created or resent | InvitationID, ResourceType, ResourceID, Relation, Identifier, Token (plaintext), InvitedBy |
| `MemberAddedEvent` (`member.added`) | Known user added directly (AutoAccept + user exists) | ResourceType, ResourceID, Relation, SubjectType, SubjectID, AddedBy |

The `InvitationSentEvent` is the only time the plaintext token is available. Subscribers should use it to build the invitation link and send the email.

## Token Security

- **Generation:** 32-character tokens from a URL-safe alphabet (no visually ambiguous characters like `I`, `O`, `l`, `0`, `1`)
- **Storage:** only the SHA256 hash is stored in the database — the plaintext is emitted once via the event bus and never persisted
- **Verification:** the `Accept` method hashes the provided token and queries by hash
- **Expiry:** 7 days from creation, checked on both accept and list operations

## Errors

| Error | Base | When |
|---|---|---|
| `ErrInvitationNotFound` | `errs.ErrNotFound` | Token or ID not found |
| `ErrInvitationExpired` | `errs.ErrConflict` | Token has expired |
| `ErrInvitationAlreadyUsed` | `errs.ErrConflict` | Already accepted |
| `ErrInvitationCancelled` | `errs.ErrConflict` | Already cancelled |
| `ErrInvitationInvalidStatus` | `errs.ErrConflict` | Operation not valid for current status |
| `ErrIdentifierMismatch` | `errs.ErrForbidden` | Accepting user's identifier doesn't match invitation |
| `ErrAlreadyMember` | `errs.ErrAlreadyExists` | Subject is already a member of the resource |
| `ErrPendingInvitationExists` | `errs.ErrAlreadyExists` | A pending invitation already exists for this identifier and resource |

## Authorization is the Bridge's Job

The `Inviter` does not perform any permission checks — it trusts that the caller has already authorized the operation. This follows the Gopernicus convention that [core is ignorant of authorization](../../../design-philosophy.md#where-authorization-lives); the bridge layer gates access via middleware before the request reaches core.

In practice, your bridge should enforce who can invite to which resource types. Don't expose the `Inviter` directly without gating it behind appropriate permission checks — an unguarded invitation endpoint would let any authenticated user invite anyone to anything.

The same applies to cancel, resend, and list operations — each should be gated behind the appropriate permissions for your domain at the bridge layer.

See also: [Bridge Invitations](../../bridge/auth/invitations.md) for the HTTP endpoints and authorization middleware wiring.
