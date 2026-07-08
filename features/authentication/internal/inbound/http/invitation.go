package http

import (
	"context"
	"net/http"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/invitationsvc"
	"github.com/gopernicus/gopernicus/features/authentication/domain/invitation"
	"github.com/gopernicus/gopernicus/sdk/crud"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/web"
)

// declineAttemptsPerMinute caps invitation-decline attempts per client IP: the
// decline route is PUBLIC (token-authorized, not session-gated), so it is
// rate-limited to blunt token-guessing and abuse (design §6).
const declineAttemptsPerMinute = 10

// InvitationService is the narrow surface the invitation handlers consume.
// *invitationsvc.Service satisfies it. It is separate from authService because
// the Granter seam is injected into invitationsvc ONLY (design §6): a host with
// no Granter passes a nil InvitationService and the routes are never registered.
// Accept interfaces, return structs.
type InvitationService interface {
	Create(ctx context.Context, in invitationsvc.CreateInput) (invitationsvc.CreateResult, error)
	ListByResource(ctx context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[invitation.Invitation], error)
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
	Relation   string `json:"relation"`
	AutoAccept bool   `json:"auto_accept"`
	Redirect   string `json:"redirect"`
}

type acceptInvitationRequest struct {
	Token string `json:"token"`
}

type declineInvitationRequest struct {
	Token string `json:"token"`
}

// invitationResponse is an invitation WITHOUT its token — the secret is only
// ever in the mail (design §5.1 WI3).
type invitationResponse struct {
	ID                string `json:"id"`
	ResourceType      string `json:"resource_type"`
	ResourceID        string `json:"resource_id"`
	Relation          string `json:"relation"`
	Identifier        string `json:"identifier"`
	Status            string `json:"status"`
	AutoAccept        bool   `json:"auto_accept"`
	ResolvedSubjectID string `json:"resolved_subject_id,omitempty"`
	ExpiresAt         string `json:"expires_at"`
	AcceptedAt        string `json:"accepted_at,omitempty"`
	CreatedAt         string `json:"created_at"`
}

func newInvitationResponse(inv invitation.Invitation) invitationResponse {
	return invitationResponse{
		ID:                inv.ID,
		ResourceType:      inv.ResourceType,
		ResourceID:        inv.ResourceID,
		Relation:          inv.Relation,
		Identifier:        inv.Identifier,
		Status:            inv.Status,
		AutoAccept:        inv.AutoAccept,
		ResolvedSubjectID: inv.ResolvedSubjectID,
		ExpiresAt:         inv.ExpiresAt.Format(time.RFC3339),
		AcceptedAt:        formatOptionalTime(inv.AcceptedAt),
		CreatedAt:         inv.CreatedAt.Format(time.RFC3339),
	}
}

// mountInvitations registers the invitation route surface (design §6). Called
// from Mount only when a Granter is wired. All routes are session-gated except
// decline, which is public and IP-rate-limited.
func mountInvitations(r feature.RouteRegistrar, h *handlers, requireUser, declineLimit web.Middleware) {
	r.Handle("POST", "/auth/invitations/{resource_type}/{resource_id}", h.createInvitation, requireUser)
	r.Handle("GET", "/auth/invitations/{resource_type}/{resource_id}", h.listResourceInvitations, requireUser)
	r.Handle("GET", "/auth/invitations/mine", h.listMyInvitations, requireUser)
	r.Handle("POST", "/auth/invitations/accept", h.acceptInvitation, requireUser)
	r.Handle("POST", "/auth/invitations/{id}/cancel", h.cancelInvitation, requireUser)
	r.Handle("POST", "/auth/invitations/{id}/resend", h.resendInvitation, requireUser)
	r.Handle("POST", "/auth/invitations/{id}/decline", h.declineInvitation, declineLimit)
}

// createInvitation invites an identifier to the path resource (session-gated).
// A direct add (known invitee + auto_accept) returns 200; a pending invite 201.
func (h *handlers) createInvitation(w http.ResponseWriter, r *http.Request) {
	var req createInvitationRequest
	if !decode(w, r, &req) {
		return
	}
	invitedBy, ok := h.svc.CurrentUser(r.Context())
	if !ok {
		web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
		return
	}
	res, err := h.inv.Create(r.Context(), invitationsvc.CreateInput{
		ResourceType: web.Param(r, "resource_type"),
		ResourceID:   web.Param(r, "resource_id"),
		Relation:     req.Relation,
		Identifier:   req.Identifier,
		InvitedBy:    invitedBy,
		AutoAccept:   req.AutoAccept,
		Redirect:     req.Redirect,
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

// listResourceInvitations pages a resource's invitations (session-gated).
func (h *handlers) listResourceInvitations(w http.ResponseWriter, r *http.Request) {
	req, ok := parseListRequest(w, r)
	if !ok {
		return
	}
	page, err := h.inv.ListByResource(r.Context(), web.Param(r, "resource_type"), web.Param(r, "resource_id"), req)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONOK(w, newPageResponse(page, newInvitationResponse))
}

// listMyInvitations pages the caller's own invitations, keyed on their email
// (session-gated). The email is resolved from the caller's user record so
// invitationsvc stays decoupled from the user store.
func (h *handlers) listMyInvitations(w http.ResponseWriter, r *http.Request) {
	req, ok := parseListRequest(w, r)
	if !ok {
		return
	}
	userID, ok := h.svc.CurrentUser(r.Context())
	if !ok {
		web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
		return
	}
	email, err := h.svc.EmailForUser(r.Context(), userID)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	page, err := h.inv.Mine(r.Context(), email, req)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONOK(w, newPageResponse(page, newInvitationResponse))
}

// acceptInvitation redeems a token for the calling user (session-gated). The
// caller's email is checked against the invitation identifier in the service.
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
	email, err := h.svc.EmailForUser(r.Context(), userID)
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
