package authenticationhttp

import (
	"context"
	"fmt"

	"github.com/gopernicus/gopernicus/sdk"
)

// InviteAction is the invitation operation a host authorization policy
// (InviteCheck) is asked about (design §6/D3): creating an invitation or listing
// a resource's invitations.
type InviteAction string

const (
	// InviteCreate is the create-an-invitation action; the check carries the exact
	// requested Relation so the host can prevent privilege escalation (e.g. an
	// editor inviting an owner).
	InviteCreate InviteAction = "create"
	// InviteList is the list-a-resource's-invitations action; the check carries an
	// empty Relation.
	InviteList InviteAction = "list"
)

// InviteCheckRequest is the parsed, principal-resolved authorization question the
// HTTP invitation handlers pose to the host policy (design §6/D3).
// Relation is set for InviteCreate and empty for InviteList. The pocket owns
// parsing, normalization, and the invitee lookup, so the host sees the caller,
// resource, action, and — for create — the exact validated relation and the
// complete invitee context, which a RouteRegistrar decorator cannot.
type InviteCheckRequest struct {
	Principal    sdk.Principal
	Action       InviteAction
	ResourceType string
	ResourceID   string
	Relation     string
	// Metadata is the parsed, opaque host routing data of a create request (empty
	// for InviteList). It is UNTRUSTED inviter-supplied input the pocket does not
	// interpret, surfaced here so the host can authorize the COMPLETE invitation —
	// including a routing key it will later act on in its Granter. It is a defensive
	// copy; empty when the request carried none.
	Metadata map[string]string
	// Identifier is the pocket-normalized invitee identifier. It is empty for
	// InviteList.
	Identifier string
	// IdentifierKind is the normalized identifier kind. It is empty for InviteList
	// and makes an empty ResolvedSubjectID unambiguous for kinds the pocket cannot
	// resolve today.
	IdentifierKind string
	// ResolvedSubjectID is the existing subject the identifier resolves to, or ""
	// when it is unknown or the kind is not resolvable. The pocket's lookup is
	// EMAIL-KIND ONLY today, so an empty value must never be read as proof that the
	// invitee is new.
	ResolvedSubjectID string
}

// InviteCheck is the host authorization seam invitation HTTP handlers call
// after live-session validation,
// principal resolution, request parsing, metadata validation, identifier
// normalization, and the invitee lookup — and always before any row exists or a
// grant is attempted (design §6/D3). It is REQUIRED whenever a Granter enables
// invitations — package auth rejects a nil InviteCheck at construction
// (ErrInviteCheckRequired), never an allow-by-default. A nil return authorizes; a
// denial (wrapping sdk.ErrForbidden) or an infrastructure error fails closed
// through the normal web/sdk error path. Authority is issuance-time: a create-time
// authorization is a durable capability and acceptance never re-runs inviter
// authority.
type InviteCheck func(ctx context.Context, req InviteCheckRequest) error

// UserAdminAction is the user-administration operation a host UserAdminCheck
// policy is asked about (coordination-hub-auth-upstream CHAU-1.1). The set is
// closed: a new administrative capability adds a value here, so a host policy's
// default arm always sees an action it can recognize or refuse.
type UserAdminAction string

const (
	// UserAdminList is the paginated directory read. It carries no target user.
	UserAdminList UserAdminAction = "list"
	// UserAdminRead is a single-user directory read; the target is the user read.
	UserAdminRead UserAdminAction = "read"
	// UserAdminDeactivate denies the target subject every new session.
	UserAdminDeactivate UserAdminAction = "deactivate"
	// UserAdminReactivate returns the target subject to the active posture.
	UserAdminReactivate UserAdminAction = "reactivate"
	// UserAdminResendVerification re-issues the target's registration
	// verification challenge (the authorized counterpart to the enumeration-safe
	// public resend).
	UserAdminResendVerification UserAdminAction = "resend-verification"
)

// UserAdminCheckRequest is the parsed, principal-resolved authorization question
// the pocket poses to a host UserAdminCheck. TargetUserID is empty for
// UserAdminList and set for every other action.
//
// The Principal reaches the policy VERBATIM — including a machine principal from
// an API key. The pocket does not pre-decide whether a service account may
// administer users; that is exactly the decision the host owns.
type UserAdminCheckRequest struct {
	Principal    sdk.Principal
	Action       UserAdminAction
	TargetUserID string
}

// UserAdminCheck is the host authorization seam for user administration. It is
// the InviteCheck precedent applied to the user directory: the pocket owns
// session validation, principal resolution, and request parsing, then asks the
// host one question it can answer with its own roles, tenancy, or policy engine.
//
// Authentication NEVER invents a role named "admin" and never interprets a role
// string. It does not import pockets/authorization. A host that has an
// authorization pocket wires a closure over it; a host with a hard-coded
// operator list wires that instead.
//
// A nil return authorizes. A denial (wrap sdk.ErrForbidden) or an infrastructure
// error BOTH fail closed — the pocket never distinguishes "policy said no" from
// "policy could not answer" by proceeding.
//
// Wiring a nil UserAdminCheck leaves the bundled admin routes UNMOUNTED even when
// the repositories are present, so a store adapter may return a complete bundle
// without an authorization surface appearing anywhere.
type UserAdminCheck func(ctx context.Context, req UserAdminCheckRequest) error

func (h *handlers) checkInvite(ctx context.Context, req InviteCheckRequest) error {
	if h.inviteCheck == nil {
		return fmt.Errorf("invitation policy is not wired: %w", sdk.ErrForbidden)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := h.inviteCheck(ctx, req); err != nil {
		return err
	}
	return ctx.Err()
}
func (h *handlers) checkUserAdmin(ctx context.Context, req UserAdminCheckRequest) error {
	if h.userAdminCheck == nil {
		return fmt.Errorf("user administration policy is not wired: %w", sdk.ErrForbidden)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := h.userAdminCheck(ctx, req); err != nil {
		return err
	}
	return ctx.Err()
}
