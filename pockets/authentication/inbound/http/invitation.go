package authenticationhttp

import (
	"context"
	"net/http"
	"time"

	"github.com/gopernicus/gopernicus/pockets"
	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	invitations "github.com/gopernicus/gopernicus/pockets/authentication/logic/invitations"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// declineAttemptsPerMinute caps invitation-decline attempts per client IP: the
// decline route is PUBLIC (token-authorized, not session-gated), so it is
// rate-limited to blunt token-guessing and abuse (design §6).
const declineAttemptsPerMinute = 10

// InvitationService supplies invitation preparation and policy-free use cases.
// The adapter checks its policy against a prepared command before executing it.
// A nil service leaves invitation routes unmounted.
type InvitationService interface {
	PrepareCreate(ctx context.Context, in invitations.CreateInput) (invitations.PreparedCreate, error)
	CreatePrepared(ctx context.Context, prepared invitations.PreparedCreate) (invitations.CreateResult, error)
	ListByResource(ctx context.Context, resourceType, resourceID string, req list.Request) (list.Page[invitations.Invitation], error)
	Mine(ctx context.Context, identifier string, req list.Request) (list.Page[invitations.Invitation], error)
	Accept(ctx context.Context, in invitations.AcceptInput) (invitations.AcceptResult, error)
	Decline(ctx context.Context, id, token string) error
	PrepareManagement(ctx context.Context, id string) (invitations.PreparedManagement, error)
	Cancel(ctx context.Context, prepared invitations.PreparedManagement) error
	Resend(ctx context.Context, prepared invitations.PreparedManagement, redirectTo string) (invitations.Invitation, error)
}

// ---------------------------------------------------------------------------
// DTOs
// ---------------------------------------------------------------------------

type createInvitationRequest struct {
	Identifier string `json:"identifier"`
	// IdentifierKind is the address kind of Identifier (sdk.AddressKindEmail,
	// sdk.AddressKindPhone, …). Optional: an omitted/empty value defaults to email,
	// so existing requests are unchanged.
	IdentifierKind string `json:"identifier_kind"`
	Relation       string `json:"relation"`
	AutoAccept     bool   `json:"auto_accept"`
	Redirect       string `json:"redirect"`
	// Metadata is opaque, host-owned routing data that rides the invitation to the
	// Granter seam. Optional: an omitted object is the no-metadata case. The
	// service/domain bound its shape and size (invitations.ValidateMetadata); the
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
	// HTTP adapter enforces cancel/resend access on. It is an identifier, never a
	// token or secret, and it is what lets a resource list distinguish the rows the
	// current admin owns (and may cancel/resend) from another admin's rows. The
	// HTTP adapter enforces issuer access regardless of what a client renders.
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

func newInvitationResponse(inv invitations.Invitation) invitationResponse {
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

func newMyInvitationResponse(inv invitations.Invitation) myInvitationResponse {
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
// from Mount only when a Granter is wired. Every authenticated route rides the
// Invitations authenticator (design §6/D3), so a revoked session's outstanding
// access JWT is denied within one round-trip; decline is public and
// IP-rate-limited.
func mountInvitations(r pockets.RouteRegistrar, h *handlers, invitations, declineLimit web.Middleware) {
	r.Handle("POST", "/auth/invitations/{resource_type}/{resource_id}", h.createInvitation, invitations)
	r.Handle("GET", "/auth/invitations/{resource_type}/{resource_id}", h.listResourceInvitations, invitations)
	r.Handle("GET", "/auth/invitations/mine", h.listMyInvitations, invitations)
	r.Handle("POST", "/auth/invitations/accept", h.acceptInvitation, invitations)
	r.Handle("POST", "/auth/invitations/{id}/cancel", h.cancelInvitation, invitations)
	r.Handle("POST", "/auth/invitations/{id}/resend", h.resendInvitation, invitations)
	r.Handle("POST", "/auth/invitations/{id}/decline", h.declineInvitation, declineLimit)
}

// createInvitation prepares once, authorizes the inspected command, then executes
// that same command. Denial leaves no invitation row or grant on either path.
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
	prepared, err := h.inv.PrepareCreate(r.Context(), invitations.CreateInput{
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
	in := prepared.Input()
	if err := h.checkInvite(r.Context(), InviteCheckRequest{Principal: sdk.Principal{Type: sdk.PrincipalTypeUser, ID: invitedBy}, Action: InviteCreate, ResourceType: in.ResourceType, ResourceID: in.ResourceID, Relation: in.Relation, Metadata: in.Metadata, Identifier: in.Identifier, IdentifierKind: in.IdentifierKind, ResolvedSubjectID: prepared.ResolvedSubjectID()}); err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	res, err := h.inv.CreatePrepared(r.Context(), prepared)
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

// listResourceInvitations authorizes the user-only resource listing before the
// service reads its owner-facing projection, which includes host metadata.
func (h *handlers) listResourceInvitations(w http.ResponseWriter, r *http.Request) {
	req, ok := h.parseListRequest(w, r, invitations.OrderFields, invitations.DefaultOrder)
	if !ok {
		return
	}
	userID, ok := h.svc.CurrentUser(r.Context())
	if !ok {
		web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
		return
	}
	resourceType, resourceID := web.Param(r, "resource_type"), web.Param(r, "resource_id")
	if err := h.checkInvite(r.Context(), InviteCheckRequest{Principal: sdk.Principal{Type: sdk.PrincipalTypeUser, ID: userID}, Action: InviteList, ResourceType: resourceType, ResourceID: resourceID}); err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	page, err := h.inv.ListByResource(r.Context(), resourceType, resourceID, req)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONOK(w, newPageResponse(page, newInvitationResponse))
}

// listMyInvitations pages the caller's own invitations, keyed on their email
// (session-gated). The email is resolved from the caller's active verified email
// identifier so invitation service stays decoupled from the identifier store. It
// answers the RECIPIENT projection, which carries no host metadata.
func (h *handlers) listMyInvitations(w http.ResponseWriter, r *http.Request) {
	req, ok := h.parseListRequest(w, r, invitations.OrderFields, invitations.DefaultOrder)
	if !ok {
		return
	}
	userID, ok := h.svc.CurrentUser(r.Context())
	if !ok {
		web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
		return
	}
	email, err := h.svc.ActiveVerifiedIdentifier(r.Context(), userID, sdk.AddressKindEmail)
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
	if !strictJSONBody(w, r, &req, maxJSONBodyBytes) {
		return
	}
	userID, ok := h.svc.CurrentUser(r.Context())
	if !ok {
		web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
		return
	}
	email, err := h.svc.ActiveVerifiedIdentifier(r.Context(), userID, sdk.AddressKindEmail)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	res, err := h.inv.Accept(r.Context(), invitations.AcceptInput{
		Token:       req.Token,
		SubjectType: authlogic.PrincipalUser,
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
	prepared, ok := h.prepareInvitationManagement(w, r, userID)
	if !ok {
		return
	}
	if err := h.inv.Cancel(r.Context(), prepared); err != nil {
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
	prepared, ok := h.prepareInvitationManagement(w, r, userID)
	if !ok {
		return
	}
	inv, err := h.inv.Resend(r.Context(), prepared, r.URL.Query().Get("redirect"))
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONOK(w, newInvitationResponse(inv))
}

// prepareInvitationManagement admits only the authenticated issuer of the exact
// loaded target. The prepared value pins the row used by the subsequent mutation.
func (h *handlers) prepareInvitationManagement(w http.ResponseWriter, r *http.Request, userID string) (invitations.PreparedManagement, bool) {
	id := web.Param(r, "id")
	prepared, err := h.inv.PrepareManagement(r.Context(), id)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return invitations.PreparedManagement{}, false
	}
	if id == "" || prepared.ID() != id || userID == "" || prepared.InvitedBy() != userID {
		web.RespondJSONDomainError(w, sdk.ErrForbidden)
		return invitations.PreparedManagement{}, false
	}
	if err := r.Context().Err(); err != nil {
		web.RespondJSONDomainError(w, err)
		return invitations.PreparedManagement{}, false
	}
	return prepared, true
}

// declineInvitation declines a pending invitation (PUBLIC, IP-rate-limited). The
// caller proves they are the invitee with the token; a wrong token → 404.
func (h *handlers) declineInvitation(w http.ResponseWriter, r *http.Request) {
	var req declineInvitationRequest
	if !strictJSONBody(w, r, &req, maxJSONBodyBytes) {
		return
	}
	if err := h.inv.Decline(r.Context(), web.Param(r, "id"), req.Token); err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONOK(w, map[string]string{"status": "declined"})
}
