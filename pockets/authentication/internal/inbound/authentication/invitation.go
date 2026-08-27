package authentication

import (
	"context"
	"net/http"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/domain/invitation"
	"github.com/gopernicus/gopernicus/pockets/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/pockets/authentication/internal/logic/invitationsvc"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
	"github.com/gopernicus/gopernicus/sdk/pocket"
)

// declineAttemptsPerMinute caps invitation-decline attempts per client IP: the
// decline route is PUBLIC (token-authorized, not session-gated), so it is
// rate-limited to blunt token-guessing and abuse (design §6).
const declineAttemptsPerMinute = 10

// InvitationService is the narrow surface the invitation handlers consume.
// *invitationsvc.Service satisfies it. It is separate from authService because
// the Granter seam is injected into invitationsvc ONLY (design §6): a host with
// no Granter passes a nil InvitationService and the routes are never registered.
// Create and list are the AUTHORIZED operations (design §6/D3): the host
// InviteCheck lives with the service, which poses it over the fully prepared
// request, so no handler here owns invitation authorization. Accept interfaces,
// return structs.
type InvitationService interface {
	CreateAuthorized(ctx context.Context, principal identity.Principal, in invitationsvc.CreateInput) (invitationsvc.CreateResult, error)
	ListByResourceAuthorized(ctx context.Context, principal identity.Principal, resourceType, resourceID string, req crud.ListRequest) (crud.Page[invitation.Invitation], error)
	Mine(ctx context.Context, identifier string, req crud.ListRequest) (crud.Page[invitation.Invitation], error)
	Accept(ctx context.Context, in invitationsvc.AcceptInput) (invitationsvc.AcceptResult, error)
	Decline(ctx context.Context, id, token string) error
	Cancel(ctx context.Context, id, currentUserID string) error
	Resend(ctx context.Context, id, currentUserID, redirectTo string) (invitation.Invitation, error)
}

// ---------------------------------------------------------------------------
// DTOs
// ---------------------------------------------------------------------------

type createInvitationRequest struct {
	Identifier string `json:"identifier"`
	// IdentifierKind is the address kind of Identifier (identity.KindEmail,
	// identity.KindPhone, …). Optional: an omitted/empty value defaults to email,
	// so existing requests are unchanged.
	IdentifierKind string `json:"identifier_kind"`
	Relation       string `json:"relation"`
	AutoAccept     bool   `json:"auto_accept"`
	Redirect       string `json:"redirect"`
	// Metadata is opaque, host-owned routing data that rides the invitation to the
	// Granter seam. Optional: an omitted object is the no-metadata case. The
	// service/domain bound its shape and size (invitation.ValidateMetadata); the
	// route-level MaxBytesReader guards the body before this unbounded map decodes.
	Metadata map[string]string `json:"metadata"`
}

type acceptInvitationRequest struct {
	Token string `json:"token"`
}

type declineInvitationRequest struct {
	Token string `json:"token"`
}

// invitationResponse is the RESOURCE-OWNER projection of an invitation, WITHOUT
// its token — the secret is only ever in the mail (design §5.1 WI3). It serves the
// endpoints an inviting owner drives (pending create, resource list, resend) and
// is the ONLY projection carrying the host Metadata. The recipient-facing
// /auth/invitations/mine surface deliberately uses myInvitationResponse instead:
// metadata is opaque issuer→host routing that may be sensitive, so it must never
// reach the invitee by a shared DTO growing a field.
type invitationResponse struct {
	ID           string `json:"id"`
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	Relation     string `json:"relation"`
	Identifier   string `json:"identifier"`
	// InvitedBy is the user id that created the invitation — the same value the
	// service enforces cancel/resend ownership on. It is an identifier, never a
	// token or secret, and it is what lets a resource list distinguish the rows the
	// current admin owns (and may cancel/resend) from another admin's rows. The
	// server still enforces ownership regardless of what a client renders.
	InvitedBy         string `json:"invited_by"`
	Status            string `json:"status"`
	AutoAccept        bool   `json:"auto_accept"`
	ResolvedSubjectID string `json:"resolved_subject_id,omitempty"`
	ExpiresAt         string `json:"expires_at"`
	AcceptedAt        string `json:"accepted_at,omitempty"`
	CreatedAt         string `json:"created_at"`
	// Metadata is the opaque host routing data the inviter supplied at create time,
	// echoed so a resource owner can verify and audit the pending invitation's
	// routing choice. It omits when empty, so an invitation that never carried
	// metadata keeps a byte-identical response.
	Metadata map[string]string `json:"metadata,omitempty"`
}

func newInvitationResponse(inv invitation.Invitation) invitationResponse {
	return invitationResponse{
		ID:                inv.ID,
		ResourceType:      inv.ResourceType,
		ResourceID:        inv.ResourceID,
		Relation:          inv.Relation,
		Identifier:        inv.Identifier,
		InvitedBy:         inv.InvitedBy,
		Status:            inv.Status,
		AutoAccept:        inv.AutoAccept,
		ResolvedSubjectID: inv.ResolvedSubjectID,
		ExpiresAt:         inv.ExpiresAt.Format(time.RFC3339),
		AcceptedAt:        formatOptionalTime(inv.AcceptedAt),
		CreatedAt:         inv.CreatedAt.Format(time.RFC3339),
		Metadata:          inv.Metadata,
	}
}

// myInvitationResponse is the RECIPIENT-facing projection served by
// /auth/invitations/mine. It is deliberately a separate type from
// invitationResponse and has NO Metadata field: host metadata is opaque
// issuer→host routing that may be sensitive in another host, so the invitee's own
// view stays conservative by default. Keeping the two projections distinct is the
// structural guarantee — a field added to the owner projection can never leak here.
type myInvitationResponse struct {
	ID           string `json:"id"`
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	Relation     string `json:"relation"`
	Identifier   string `json:"identifier"`
	// InvitedBy is the user id that created the invitation — an identifier, never a
	// token or secret. It is retained here so the /mine payload stays byte-compatible
	// with the shape recipients already consume.
	InvitedBy         string `json:"invited_by"`
	Status            string `json:"status"`
	AutoAccept        bool   `json:"auto_accept"`
	ResolvedSubjectID string `json:"resolved_subject_id,omitempty"`
	ExpiresAt         string `json:"expires_at"`
	AcceptedAt        string `json:"accepted_at,omitempty"`
	CreatedAt         string `json:"created_at"`
}

func newMyInvitationResponse(inv invitation.Invitation) myInvitationResponse {
	return myInvitationResponse{
		ID:                inv.ID,
		ResourceType:      inv.ResourceType,
		ResourceID:        inv.ResourceID,
		Relation:          inv.Relation,
		Identifier:        inv.Identifier,
		InvitedBy:         inv.InvitedBy,
		Status:            inv.Status,
		AutoAccept:        inv.AutoAccept,
		ResolvedSubjectID: inv.ResolvedSubjectID,
		ExpiresAt:         inv.ExpiresAt.Format(time.RFC3339),
		AcceptedAt:        formatOptionalTime(inv.AcceptedAt),
		CreatedAt:         inv.CreatedAt.Format(time.RFC3339),
	}
}

// mountInvitations registers the invitation route surface (design §6). Called
// from Mount only when a Granter is wired. Every authenticated route rides
// requireLiveSession (design §6/D3), so a revoked session's outstanding access
// JWT is denied within one round-trip; decline is public and IP-rate-limited.
func mountInvitations(r pocket.RouteRegistrar, h *handlers, requireLiveSession, declineLimit web.Middleware) {
	r.Handle("POST", "/auth/invitations/{resource_type}/{resource_id}", h.createInvitation, requireLiveSession)
	r.Handle("GET", "/auth/invitations/{resource_type}/{resource_id}", h.listResourceInvitations, requireLiveSession)
	r.Handle("GET", "/auth/invitations/mine", h.listMyInvitations, requireLiveSession)
	r.Handle("POST", "/auth/invitations/accept", h.acceptInvitation, requireLiveSession)
	r.Handle("POST", "/auth/invitations/{id}/cancel", h.cancelInvitation, requireLiveSession)
	r.Handle("POST", "/auth/invitations/{id}/resend", h.resendInvitation, requireLiveSession)
	r.Handle("POST", "/auth/invitations/{id}/decline", h.declineInvitation, declineLimit)
}

// createInvitation invites an identifier to the path resource (live-session
// gated). A direct add (known invitee + auto_accept) returns 200; a pending
// invite 201. It calls the AUTHORIZED create operation with the resolved
// principal: the service prepares the request (metadata validation, identifier
// normalization, invitee lookup) and poses the host InviteCheck over that complete
// context before any row exists or a grant is attempted (design §6/D3), so a
// denial or infrastructure error fails closed through the normal web/sdk mapping
// and a forbidden create never mutates.
func (h *handlers) createInvitation(w http.ResponseWriter, r *http.Request) {
	var req createInvitationRequest
	// The bounded strict decoder caps the body before decoding the unbounded
	// metadata object; the service/domain limits remain authoritative for shape.
	if !strictJSONBody(w, r, &req, maxJSONBodyBytes) {
		return
	}
	invitedBy, ok := h.svc.CurrentUser(r.Context())
	if !ok {
		web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
		return
	}
	res, err := h.inv.CreateAuthorized(r.Context(), identity.Principal{Type: identity.User, ID: invitedBy}, invitationsvc.CreateInput{
		ResourceType:   web.Param(r, "resource_type"),
		ResourceID:     web.Param(r, "resource_id"),
		Relation:       req.Relation,
		Identifier:     req.Identifier,
		IdentifierKind: req.IdentifierKind,
		InvitedBy:      invitedBy,
		AutoAccept:     req.AutoAccept,
		Redirect:       req.Redirect,
		Metadata:       req.Metadata,
	})
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	if res.DirectlyAdded {
		web.RespondJSONOK(w, map[string]string{"status": "member_added"})
		return
	}
	web.RespondJSONCreated(w, newInvitationResponse(res.Invitation))
}

// listResourceInvitations pages a resource's invitations (live-session gated).
// It resolves CurrentUser both to keep the surface user-only (a service-account
// principal that RequireLiveSession admits is rejected here, exactly as
// RequireUser's JWT-only gate rejected it) and to hand the AUTHORIZED list
// operation its principal: the service poses the InviteList question (empty
// Relation, no invitee context — design §6/D3) before reading. A denial or
// infrastructure error fails closed through the normal web/sdk mapping. The page
// carries the RESOURCE-OWNER projection, so an owner sees the host metadata they
// supplied.
func (h *handlers) listResourceInvitations(w http.ResponseWriter, r *http.Request) {
	req, ok := h.parseListRequest(w, r, invitation.OrderFields, invitation.DefaultOrder)
	if !ok {
		return
	}
	userID, ok := h.svc.CurrentUser(r.Context())
	if !ok {
		web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
		return
	}
	page, err := h.inv.ListByResourceAuthorized(r.Context(), identity.Principal{Type: identity.User, ID: userID},
		web.Param(r, "resource_type"), web.Param(r, "resource_id"), req)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONOK(w, newPageResponse(page, newInvitationResponse))
}

// listMyInvitations pages the caller's own invitations, keyed on their email
// (session-gated). The email is resolved from the caller's active verified email
// identifier so invitationsvc stays decoupled from the identifier store. It
// answers the RECIPIENT projection, which carries no host metadata.
func (h *handlers) listMyInvitations(w http.ResponseWriter, r *http.Request) {
	req, ok := h.parseListRequest(w, r, invitation.OrderFields, invitation.DefaultOrder)
	if !ok {
		return
	}
	userID, ok := h.svc.CurrentUser(r.Context())
	if !ok {
		web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
		return
	}
	email, err := h.svc.ActiveVerifiedIdentifier(r.Context(), userID, identity.KindEmail)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	page, err := h.inv.Mine(r.Context(), email, req)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONOK(w, newPageResponse(page, newMyInvitationResponse))
}

// acceptInvitation redeems a token for the calling user (session-gated). The
// caller's email is checked against an email-kind invitation identifier in the
// service; a phone-kind invitation is matched against the caller's active
// verified phone identifier inside the service (design §7/V11).
func (h *handlers) acceptInvitation(w http.ResponseWriter, r *http.Request) {
	var req acceptInvitationRequest
	if !decode(w, r, &req) {
		return
	}
	userID, ok := h.svc.CurrentUser(r.Context())
	if !ok {
		web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
		return
	}
	email, err := h.svc.ActiveVerifiedIdentifier(r.Context(), userID, identity.KindEmail)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	res, err := h.inv.Accept(r.Context(), invitationsvc.AcceptInput{
		Token:       req.Token,
		SubjectType: authsvc.PrincipalUser,
		SubjectID:   userID,
		Identifier:  email,
	})
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONOK(w, map[string]string{
		"resource_type": res.ResourceType,
		"resource_id":   res.ResourceID,
		"relation":      res.Relation,
	})
}

// cancelInvitation cancels a pending invitation the caller owns (session-gated;
// ownership = InvitedBy == caller).
func (h *handlers) cancelInvitation(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.svc.CurrentUser(r.Context())
	if !ok {
		web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
		return
	}
	if err := h.inv.Cancel(r.Context(), web.Param(r, "id"), userID); err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONOK(w, map[string]string{"status": "cancelled"})
}

// resendInvitation regenerates and re-mails a pending invitation the caller owns
// (session-gated; ownership = InvitedBy == caller).
func (h *handlers) resendInvitation(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.svc.CurrentUser(r.Context())
	if !ok {
		web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
		return
	}
	inv, err := h.inv.Resend(r.Context(), web.Param(r, "id"), userID, r.URL.Query().Get("redirect"))
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONOK(w, newInvitationResponse(inv))
}

// declineInvitation declines a pending invitation (PUBLIC, IP-rate-limited). The
// caller proves they are the invitee with the token; a wrong token → 404.
func (h *handlers) declineInvitation(w http.ResponseWriter, r *http.Request) {
	var req declineInvitationRequest
	if !decode(w, r, &req) {
		return
	}
	if err := h.inv.Decline(r.Context(), web.Param(r, "id"), req.Token); err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONOK(w, map[string]string{"status": "declined"})
}
