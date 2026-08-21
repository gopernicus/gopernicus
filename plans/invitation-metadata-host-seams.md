# Invitation metadata follow-ups — three platform seams surfaced by a first consumer

**Feature:** `features/authentication` (proposals 1 and 3), `sdk/foundation/web`
(proposal 2)
**Status:** EXECUTED and RELEASED — owner ruled 2026-08-21; proposals 1 and 3
tagged `features/authentication/v0.4.1`, proposal 2 tagged `sdk/v0.4.1`.
**Evidence source:** coordination-hub firm-aware vendor onboarding. Its
implementation exposed three framework seams, but its firm model and
`vendor_org_id` are evidence and examples only, not requirements for the
framework surface.

## Platform-validation gate

Gopernicus is a framework, so a consumer request is not sufficient by itself to
justify a new public seam. Before implementation, the owner should validate that
each proposal solves a reusable host problem and remains useful without knowing
coordination-hub's domain model.

Acceptance criteria for these proposals:

- The API uses only generic invitation concepts: principal, normalized invitee
  identifier, resolved subject, relation, metadata, and resource. No firm,
  campaign, vendor, tenant, or application-specific field is introduced.
- Proposal 1 is justified by several host-policy classes, such as per-subject
  eligibility, quota/deduplication, account compatibility, and standing checks;
  it is not justified solely by a firm-membership conflict.
- Proposal 2 is an explicit opt-in for a host-facing, user-safe error body. It
  must not turn arbitrary feature errors or arbitrary `*web.Error` values into
  wire-visible messages.
- Proposal 3 is an invitation-projection decision: resource owners may inspect
  host routing metadata, while recipient-facing `/mine` remains conservative by
  default because metadata is opaque and may be sensitive in another host.
- Tests and README examples use neutral metadata such as `routing_key` and
  generic policy fixtures. Coordination-hub remains a motivating integration
  case, not the compatibility or acceptance contract.

## Problem 1 — the host cannot authorize the complete invitation with invitee context

`InviteCheckRequest.Metadata`'s own doc says the metadata is surfaced so the host
can authorize the complete invitation. That is useful only if the host also has
the invitee context needed to evaluate metadata-dependent policy. For a
per-subject rule, completeness requires both the normalized invitee identifier
and, when available, the subject it resolves to.

The evidence case was a host policy that needed to compare invitation metadata
with the invitee's existing state. The check cannot do that today: the feature
normalizes the identifier and performs its email `UserLookup` inside
`Service.Create`, after the inbound handler has already posed `InviteCheck`.
The resolved subject exists one call later than the host needs it.

It is stricter than "no resolved subject": the request carries no **invitee
identifier** either (`Principal` is the inviter), so a host cannot run its own
lookup at check time. The fix must expose the feature's normalized preparation
result to the authorization seam; it should not duplicate identifier policy in
the host.

> **Evidence update (2026-08-21):** coordination-hub has since ruled firm
> membership multi-valued, so its original firm-conflict policy is no longer the
> requirement. The reusable platform question remains whether a host can
> authorize metadata against the invitee's normalized identifier and optional
> resolved subject before persistence or direct grant.

The consequence is not hypothetical. Deferred to grant time, the conflict
refusal lands in the worst possible places:

- **accept / resolve-on-registration:** the error reaches the *invitee* during
  sign-up — about a mistake the inviter made, with advice ("resolve in the
  console") the invitee cannot act on. On the resolve path it is worse:
  `ResolveInvitations` is deliberately best-effort (`continue` on grant error,
  security event only, never retried), so the failure is **silent** — the person
  registers, holds nothing, and only the still-pending invitation row hints why.
- **direct-add:** the Granter error does surface to the inviter — proving the
  create-time refusal is the natural shape; it is only the pending path that
  defers the bad news.

### Proposal

Do not put authorization into the existing trusted `Create` and
`ListByResource` methods. Their public contract deliberately allows a host to
drive composition methods directly while owning authorization itself. Instead,
factor the existing preparation logic and add explicit authorized operations:

- `CreateAuthorized(ctx, principal, in)` prepares metadata, normalizes the
  invitee identifier, performs `UserLookup`, poses `InviteCheck`, then calls the
  existing side-effect path only after the check succeeds.
- `ListByResourceAuthorized(ctx, principal, resourceType, resourceID, req)`
  poses the `InviteList` check in the service and then delegates to the existing
  list path. The current trusted `ListByResource` remains unchanged.

The HTTP adapter uses these authorized operations. `InviteCheck` becomes a
dependency of the invitation service for those operations, while the existing
trusted methods continue to skip it. The request gains the complete generic
invitee context:

```go
type InviteCheckRequest struct {
    // …existing fields…

    // Identifier is the feature-normalized invitee identifier. It is empty for
    // InviteList.
    Identifier string
    // IdentifierKind is the normalized identifier kind. It is empty for
    // InviteList and makes an empty ResolvedSubjectID unambiguous for kinds the
    // feature cannot resolve today.
    IdentifierKind string
    // ResolvedSubjectID is the existing subject the identifier resolves to, or
    // "" when it is unknown or the kind is not resolvable.
    ResolvedSubjectID string
}
```

Everything the check's doc promises stays true — it still runs after
live-session validation, principal resolution, parsing, metadata validation,
identifier normalization, and lookup, and still before any row exists or grant
is attempted. For `InviteList`, `Relation`, `Metadata`, `Identifier`,
`IdentifierKind`, and `ResolvedSubjectID` are empty.

Design points the owner may want to shape differently:

- Alternative considered and rejected: put the check into the trusted
  `Create`/`ListByResource` methods. That silently changes direct-composition
  behavior and gives list authorization no principal unless the trusted API is
  redesigned. Explicit authorized operations preserve both contracts.
- `UserLookup` is email-kind-only today; `ResolvedSubjectID == ""` must never be
  interpreted as proof that the invitee is new.
- Adding fields to the exported aliased request is source-compatible for keyed
  literals and function implementations, but breaks unkeyed composite literals;
  this must be called out in the release notes.

## Problem 2 — `ErrFromDomain` discards every host sentence

Host seams (`InviteCheck`, `Granter`) refuse through the vendored feature's
handlers, which respond via `web.RespondJSONDomainError` → `ErrFromDomain`
(`sdk/foundation/web/errors.go:183–206`). That mapping is kind→generic-message
by design — safe as a catch-all — but it means **no host refusal can ever carry
a legible sentence to the wire** through a vendored feature's handler:

- "routing key is not valid for this resource" (a `sdk.ErrInvalidReference`) arrives
  as `400 {"message":"invalid reference","code":"bad_request"}` —
  indistinguishable from every other reference fault except by a generic string.
- A host's metadata conflict (`sdk.ErrConflict`) arrives as
  `409 {"message":"conflict","code":"conflict"}` — **byte-identical** to the
  feature's own `ErrAlreadyMember` body, so the SPA provably cannot tell "this
  person is already a member" from "the metadata conflicts with existing host
  state." A host may currently need to ship one deliberately vague combined
  sentence for both, which is a poor generic product.

### Proposal

Do not pass through arbitrary `*web.Error` values from the top of
`ErrFromDomain`. Add an explicit host-facing safe-error wrapper/marker (for
example, `web.SafeDomainError`) that carries a deliberately public `*web.Error`
and an underlying domain cause. `ErrFromDomain` recognizes only that explicit
wrapper before the kind switch:

```go
func ErrFromDomain(err error) *Error {
    var safeErr *SafeDomainError
    if errors.As(err, &safeErr) {
        return safeErr.HTTPError()
    }
    // …existing kind switch unchanged…
}
```

A host seam that wants a specific wire body constructs the wrapper explicitly,
for example with `web.ErrStateConflict("already attached to another account")`
and `sdk.ErrConflict` as the underlying cause. The wrapper must preserve
`errors.Is`/`errors.As`; the implementation may use an `Unwrap` cause or
`errors.Join`, but the construction must be documented and tested. Bare
sentinels and arbitrary feature errors retain today's generic mapping.

Feature-internal errors must not use this wrapper. This is an explicit host-seam
affordance, not a general permission for domain code to place user text on the
wire.

## Problem 3 — a pending invitation's metadata is invisible

The current invitation projection deliberately omits `metadata`. A resource
owner who supplied opaque routing data has no way to verify or audit the pending
invitation's routing choice. This is a generic operational concern for any host
metadata that affects later grant behavior, not a requirement to expose a
particular application's business object.

### Proposal

Add the persisted metadata to a distinct **resource-owner projection** used by
pending create responses, resource invitation lists, and resend responses:

```go
Metadata map[string]string `json:"metadata,omitempty"`
```

Use a separate recipient-facing `/mine` projection that has no Metadata field.
Do not add the field to the shared `invitationResponse` used by every endpoint;
otherwise `/mine` will expose it accidentally. Empty maps omit from the owner
projection, so hosts and invitations that never touch metadata retain
byte-identical responses.

## Touch points

Proposal 1 (`features/authentication`): `internal/logic/invitationsvc/service.go`
(`InviteCheckRequest` fields; preparation factoring; authorized create/list
operations; configured check dependency), `authentication.go` (wire the check
into the invitation service while preserving trusted methods),
`internal/inbound/authentication/invitation.go` and `routes.go` (handlers use
authorized operations and no longer own the check), facade/docs and README §6/D3
updated. No store or migration change.

Proposal 2 (`sdk/foundation/web`): an explicit safe host-error wrapper,
`errors.go` mapping, wrapper constructors/docs, and tests for safe passthrough,
sentinel preservation, and rejection of arbitrary `*web.Error` wrapping.

Proposal 3 (`features/authentication`): `internal/inbound/authentication/
invitation.go` (separate owner and `/mine` response projections), README
response-shape docs, and endpoint tests. No store change (metadata is already
persisted).

## Backward compatibility

The existing trusted `Create` and `ListByResource` methods remain behaviorally
unchanged. The authorized operations are additive, but the exported aliased
`InviteCheckRequest` gains fields and therefore breaks unkeyed composite
literals. Proposal 2 changes behavior only for errors that explicitly use the
new safe wrapper. Proposal 3 adds metadata only to owner/resource projections;
`/mine` remains byte-compatible.

## Testing

1. Authorized create prepares metadata, normalized identifier/kind, and lookup
   before checking; the check sees known, unknown, and non-email cases, and a
   refusal leaves no row or grant on pending and direct-add branches. Trusted
   `Create` remains check-free. Authorized list checks receive the principal;
   trusted list remains check-free.
2. `ErrFromDomain`: the explicit safe wrapper passes through status, code, and
   message while `errors.Is` still matches its domain cause; a bare sentinel,
   arbitrary wrapped `*web.Error`, and feature-internal error keep today's
   generic body.
3. Owner/resource responses round-trip metadata; `/mine` never contains the
   key; invitations without metadata omit it everywhere.
4. Platform fixtures use neutral metadata and cover at least three generic
   policy classes, with no coordination-hub-specific field or invariant in the
   feature tests.

## Evidence consumer (coordination-hub, not part of this repo)

coordination-hub can use the generic invitee context, safe host error wrapper,
and owner projection for its onboarding flow. Its firm terminology, conflict
rules, and UI are integration evidence only; none is a framework acceptance
criterion.

## For the owner: tagging

The authentication proposals are additive and small — a natural
`features/authentication v0.4.1` (with the stores tags untouched: no schema
motion). The SDK safe-error wrapper is a small `sdk/foundation/web` patch.
Tagging should wait until the platform-validation gate is satisfied. Proposal 1
is load-bearing for the reported consumer, while proposals 2 and 3 improve
generic host UX; they can land and tag independently after that validation.
