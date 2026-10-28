---
title: First admin
description: Bootstrap the first administrator without raw SQL. A role grant for hosts behind an external identity provider, a converging invitation for invitation-only native hosts.
---

# First admin

A new environment has no administrator, so nobody can use the admin routes to appoint one. The
first one is bootstrapped by a one-shot command the operator runs once per environment. Two
shapes cover the hosts the framework knows:

| Your host | Shape | Reference |
|---|---|---|
| signs people in through an external identity provider; the authorization pocket holds roles | **grant**: assign the admin role to a local user id | `examples/production-baseline/cmd/bootstrap` |
| native sign-in under `invitation_only` registration | **invitation**: invite the first admin's address to the admin relation | below |

Both write through the pockets, never raw SQL. A raw insert into `iam_tuples` or `invitations`
leaves no audit row and skips the pocket's invariants.

Both rerun safely. Running the command twice leaves the same state as running it once, so an
operator who is unsure whether it ran can run it again.

## The grant shape

The person signs in once at the provider, and the authentication pocket provisions a local
user. The operator finds that user's id (the `user_id` of the `external_subject_provisioned`
security event, or of the person's `external_subjects` row) and runs the command with it. The
command builds the authorization pocket over the same recording store and under the same runtime
mode as the server, then makes one call:

```go
result, err := components.Mutations.AssignRole(
	audit.WithSource(ctx, audit.Source{System: "bootstrap", Reason: "first platform admin"}),
	mutations.AssignRoleCommand{Subject: subject, Role: "admin", Scope: tuples.Global()},
)
```

The grant lands in the change history (`iam_audit`, `system_source = bootstrap`). A rerun answers
`no_change` and records nothing, so `AssignRole` is already the converge step and `cmd` stays a
flag parse plus that call. See the production-baseline README, "The first admin", for the
drive.

## The invitation shape

Under `invitation_only`, an address can only sign up if an invitation is waiting for it. The
bootstrap invites the first admin's address to the admin relation, for example
`platform:<id>#admin`. The person signs in by magic link (an invited address is provisioned),
accepts, and the host's `Granter` writes the role.

To converge, a rerun must not stack up invitations to the same address. If a pending
invitation exists, the rerun resends it: a fresh link, a fresh expiry, the old link dead. If
the invitation is already being accepted, the rerun does nothing. Otherwise it creates one.

### Where the converge step lives

That decision is behaviour, so it belongs in host logic, not in `cmd/`. H10's machine floor
would let it pass, but its rule is that cmd wires. It names the authentication pocket's
invitation types, so its home is a **host pocket**, for example `pockets/auth/logic/bootstrap`.
H6 lets host pocket logic import framework pocket rims, and H1 keeps them out of
`internal/logic`. Battleboard's `cmd/bootstrap` currently carries this logic in `cmd/`. Do not
copy that placement.

```go
// Package bootstrap issues the invitation that makes the first admin.
package bootstrap

import (
	"context"
	"fmt"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/invitations"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

const (
	// inviter is the invitation's InvitedBy: there is no inviting user.
	inviter = "system:bootstrap"
	// pageSize bounds one page of the scan for an active invitation.
	pageSize = 100
)

// actor attributes every bootstrap create and resend in the audit rail.
var actor = sdk.Principal{Type: "system", ID: "bootstrap"}

// ErrBeingAccepted means the address already holds an invitation that is
// being accepted: there is nothing to do.
var ErrBeingAccepted = fmt.Errorf("first admin: the invitation is being accepted: %w", sdk.ErrConflict)

// Invitations is the slice of *invitations.Service the bootstrap uses.
type Invitations interface {
	ListActiveByResource(context.Context, invitations.ActiveListInput, list.Request) (list.Page[invitations.Invitation], error)
	PrepareManagement(context.Context, string) (invitations.PreparedManagement, error)
	Resend(context.Context, invitations.PreparedManagement, string) (invitations.Invitation, error)
	PrepareCreate(context.Context, invitations.CreateInput) (invitations.PreparedCreate, error)
	CreatePrepared(context.Context, invitations.PreparedCreate) (invitations.CreateResult, error)
}

// Outcome reports what EnsureFirstAdmin did.
type Outcome string

const (
	Created Outcome = "created"
	Resent  Outcome = "resent"
)

// Service converges the first-admin invitation for one resource relation.
type Service struct {
	invitations Invitations
	target      invitations.ActiveListInput
}

// NewService returns a Service inviting to target's resource and relation.
func NewService(inv Invitations, target invitations.ActiveListInput) *Service {
	return &Service{invitations: inv, target: target}
}

// EnsureFirstAdmin resends the pending invitation to email, or creates one.
// The link travels through the pocket's delivery path, never the return value.
func (s *Service) EnsureFirstAdmin(ctx context.Context, email string) (Outcome, invitations.Invitation, error) {
	address, err := identifier.DefaultNormalizer{}.Normalize(sdk.AddressKindEmail, email)
	if err != nil {
		return "", invitations.Invitation{}, err
	}
	active, found, err := s.findActive(ctx, address)
	if err != nil {
		return "", invitations.Invitation{}, err
	}
	if found {
		if active.Status != invitations.StatusPending {
			return "", invitations.Invitation{}, ErrBeingAccepted
		}
		prepared, err := s.invitations.PrepareManagement(ctx, active.ID)
		if err != nil {
			return "", invitations.Invitation{}, err
		}
		inv, err := s.invitations.Resend(ctx, prepared.WithActor(actor), "")
		return Resent, inv, err
	}
	prepared, err := s.invitations.PrepareCreate(ctx, invitations.CreateInput{
		ResourceType:   s.target.ResourceType,
		ResourceID:     s.target.ResourceID,
		Relation:       s.target.Relation,
		Identifier:     address,
		IdentifierKind: sdk.AddressKindEmail,
		InvitedBy:      inviter,
	})
	if err != nil {
		return "", invitations.Invitation{}, err
	}
	res, err := s.invitations.CreatePrepared(ctx, prepared.WithActor(actor))
	return Created, res.Invitation, err
}

// findActive pages the resource's active invitations for one addressed to
// address.
func (s *Service) findActive(ctx context.Context, address string) (invitations.Invitation, bool, error) {
	req := list.Request{Limit: pageSize}
	for {
		page, err := s.invitations.ListActiveByResource(ctx, s.target, req)
		if err != nil {
			return invitations.Invitation{}, false, err
		}
		for _, inv := range page.Items {
			if inv.IdentifierKind == sdk.AddressKindEmail && inv.Identifier == address {
				return inv, true, nil
			}
		}
		if !page.HasMore {
			return invitations.Invitation{}, false, nil
		}
		req.Cursor = page.NextCursor
	}
}
```

Notes on the code:

- **Attribution.** `PrepareCreate` → `WithActor({system, bootstrap})` → `CreatePrepared` records
  the `invitation_created` row with `Actor = system:bootstrap`, an empty `user_id` and
  `details.invited_by`. `PreparedCreate.WithActor` ships in authentication v0.34.0. On an older
  pin, `Create` records `InvitedBy` as the row's user. A resend is attributed the same way
  through `PreparedManagement.WithActor`. The actor is audit attribution only and never
  authorizes anything: the bootstrap is a trusted operator command, and the pocket's
  headless methods apply no policy.
- **Normalization.** The comparison uses the address as the pocket stores it. If the host
  configures a custom normalizer for the pocket, use the same one here.
- **Expired invitations.** `ListActiveByResource` lists a pending invitation past its expiry, and
  `Resend` renews it, so a rerun after the link expired converges too.
- **Tests.** `Invitations` is an interface, so the converge decision is unit-testable with a
  fake. Cover the create, resend and being-accepted cases, and a match on a later page.

### `cmd/bootstrap` wires

The command parses `-email`, builds the authentication pocket exactly as `cmd/server` does
(through the same host constructor, so the mode, origin, delivery and stores match), and makes
one call:

```go
first := bootstrap.NewService(auth.Invitations, invitations.ActiveListInput{
	ResourceType: "platform", ResourceID: platformID, Relation: "admin",
})
outcome, inv, err := first.EnsureFirstAdmin(ctx, *email)
if err != nil {
	return err
}
log.InfoContext(ctx, "first admin invitation", "outcome", outcome, "invitation_id", inv.ID, "expires_at", inv.ExpiresAt)
```

It logs the outcome, the invitation id and the expiry. Never log the token, its hash or the link.

### The link goes through delivery, never stdout

The invitation link is a bearer secret: anyone who holds it can accept the admin relation for
the address it was sent to. The pocket sends it through its delivery path, the sender the host
wired with `WithDelivery`, to the invited address. **The bootstrap never prints it**, because
stdout from a one-shot command ends up in CI logs, terminal scrollback and deploy-tool output,
and those outlive the invitation.

- Run the bootstrap in the `jobs` delivery mode, as production does. The command is queued
  durably and the host's worker sends it. With `in_process` delivery the queue lives in the
  bootstrap's own process and dies when the process exits.
- In development, `email.Console` (sdk `capabilities/notify/email`) logs each message instead of
  sending it. That is the fallback for a machine with no mail, and it is development-only: it
  declares itself so, and production refuses it at construction
  (`ErrInsecureDeliveryTransport`). Do not add a "print the link once" flag to the bootstrap
  itself.

### The drive

```sh
make bootstrap EMAIL=you@example.com
#   first admin invitation outcome=created invitation_id=<id> expires_at=<time>
make bootstrap EMAIL=you@example.com
#   first admin invitation outcome=resent invitation_id=<same id> expires_at=<later time>
```

The second run resends the same invitation, and the first link stops working. The person signs
in by magic link from the second email and accepts. A run while the accept is in flight answers
`ErrBeingAccepted`.

Convergence covers the window before the invitation is accepted. Once it is accepted it is no
longer active, so a later run would invite the address again. The bootstrap is for the first
admin only. From then on, admins invite other admins through the admin routes. A host that
wants a rerun after acceptance to be a no-op also checks the grant first, through its own
authorization port.

## See also

- [Authentication](../pockets/authentication.md), "Invitations and the audit route", for
  `PrepareCreate`, the actor attribution table and the landing page.
- [Row-level security with pgxdb](./row-level-security.md), for the tenant scope a bootstrap
  usually runs beside.
