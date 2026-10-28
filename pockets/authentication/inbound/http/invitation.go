package authenticationhttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gopernicus/gopernicus/pockets"
	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	invitations "github.com/gopernicus/gopernicus/pockets/authentication/logic/invitations"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// queryIdentifierKind selects the identifier kind /auth/invitations/mine lists by.
const queryIdentifierKind = "identifier_kind"

// InvitationService supplies invitation preparation and policy-free use cases.
// The adapter checks its policy against a prepared command before executing it.
// A nil service leaves invitation routes unmounted.
type InvitationService interface {
	PrepareCreate(ctx context.Context, in invitations.CreateInput) (invitations.PreparedCreate, error)
	CreatePrepared(ctx context.Context, prepared invitations.PreparedCreate) (invitations.CreateResult, error)
	ListByResource(ctx context.Context, resourceType, resourceID string, req list.Request) (list.Page[invitations.Invitation], error)
	Mine(ctx context.Context, kind, identifier string, req list.Request) (list.Page[invitations.Invitation], error)
	Accept(ctx context.Context, in invitations.AcceptInput) (invitations.AcceptResult, error)
	Decline(ctx context.Context, id, token string) error
	PrepareManagement(ctx context.Context, id string) (invitations.PreparedManagement, error)
	Cancel(ctx context.Context, prepared invitations.PreparedManagement) error
	Resend(ctx context.Context, prepared invitations.PreparedManagement, redirectTo string) (invitations.Invitation, error)
}

// invitationTokenDecliner is the optional token-only decline an InvitationService
// may also offer (*invitations.Service does). POST /auth/invitations/decline is
// mounted only when the wired service satisfies it, so host fakes of
// InvitationService keep compiling and simply go without the route.
type invitationTokenDecliner interface {
	DeclineByToken(ctx context.Context, token string) error
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
	// InvitedBy is the user id that created the invitation — an identifier, never
	// a token or secret. Without the invitation resource rule it is the value the
	// HTTP adapter admits cancel/resend on, so a resource list can distinguish the
	// rows the current admin may manage; with the rule, any holder of the resource
	// permission manages every row and InvitedBy is attribution only. The adapter
	// enforces access regardless of what a client renders.
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
// IP-rate-limited. The authenticated mutations also carry the browser-safe gate
// (allowlisted Origin + double-submit CSRF, design §9.1); bearer-only callers
// skip it. Decline has no session and so no __Host-auth_csrf cookie to
// double-submit — the token in its body is the credential — so, like the other
// session-less routes, it carries the ORIGIN-ONLY gate. The token-only decline
// shares that gate and the same declineLimit instance, so both decline routes
// draw on one invitation_decline budget.
func mountInvitations(r pockets.RouteRegistrar, h *handlers, invitations, declineLimit web.Middleware) {
	browserSafe := requireBrowserSafeMutation(h.mutation.csrf())
	r.Handle("POST", "/auth/invitations/{resource_type}/{resource_id}", h.createInvitation, invitations, browserSafe)
	r.Handle("GET", "/auth/invitations/{resource_type}/{resource_id}", h.listResourceInvitations, invitations)
	r.Handle("GET", "/auth/invitations/mine", h.listMyInvitations, invitations)
	r.Handle("POST", "/auth/invitations/accept", h.acceptInvitation, invitations, browserSafe)
	if h.syncer != nil {
		r.Handle("POST", "/auth/invitations/sync", h.syncInvitations, invitations, browserSafe)
	}
	r.Handle("POST", "/auth/invitations/{id}/cancel", h.cancelInvitation, invitations, browserSafe)
	r.Handle("POST", "/auth/invitations/{id}/resend", h.resendInvitation, invitations, browserSafe)
	r.Handle("POST", "/auth/invitations/{id}/decline", h.declineInvitation, requireBrowserSafeOrigin(h.mutation.csrf()), declineLimit)
	if decliner, ok := h.inv.(invitationTokenDecliner); ok {
		r.Handle("POST", "/auth/invitations/decline", h.declineInvitationByToken(decliner), requireBrowserSafeOrigin(h.mutation.csrf()), declineLimit)
	}
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
		writeInvitationError(w, err)
		return
	}
	in := prepared.Input()
	principal := sdk.Principal{Type: sdk.PrincipalTypeUser, ID: invitedBy}
	if h.resourceRuleOn() {
		if err := h.checkResource(r.Context(), principal, in.ResourceType, in.ResourceID); err != nil {
			web.RespondJSONDomainError(w, err)
			return
		}
	}
	// With the rule on, InviteCheck is the optional create-only refinement;
	// without it, InviteCheck is the whole policy.
	if !h.resourceRuleOn() || h.inviteCheck != nil {
		if err := h.checkInvite(r.Context(), InviteCheckRequest{Principal: principal, Action: InviteCreate, ResourceType: in.ResourceType, ResourceID: in.ResourceID, Relation: in.Relation, Metadata: in.Metadata, Identifier: in.Identifier, IdentifierKind: in.IdentifierKind, ResolvedSubjectID: prepared.ResolvedSubjectID()}); err != nil {
			web.RespondJSONDomainError(w, err)
			return
		}
	}
	res, err := h.inv.CreatePrepared(r.Context(), prepared.WithActor(principal))
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
	if h.resourceRuleOn() {
		if err := h.checkResource(r.Context(), sdk.Principal{Type: sdk.PrincipalTypeUser, ID: userID}, resourceType, resourceID); err != nil {
			web.RespondJSONDomainError(w, err)
			return
		}
	} else if err := h.checkInvite(r.Context(), InviteCheckRequest{Principal: sdk.Principal{Type: sdk.PrincipalTypeUser, ID: userID}, Action: InviteList, ResourceType: resourceType, ResourceID: resourceID}); err != nil {
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

// listMyInvitations pages the caller's own invitations, keyed on one of their
// verified identifiers (session-gated): ?identifier_kind=email (the default) or
// phone; any other kind is a 400. The value is the caller's first claim-holding
// (login- or recovery-enabled) verified identifier of that kind, primary first,
// resolved so invitation service stays decoupled from the identifier store. It answers the RECIPIENT projection,
// which carries no host metadata.
func (h *handlers) listMyInvitations(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get(queryIdentifierKind)
	switch kind {
	case "":
		kind = sdk.AddressKindEmail
	case sdk.AddressKindEmail, sdk.AddressKindPhone:
	default:
		web.RespondJSONError(w, web.ErrBadRequest("identifier_kind must be email or phone"))
		return
	}
	req, ok := h.parseListRequest(w, r, invitations.OrderFields, invitations.DefaultOrder)
	if !ok {
		return
	}
	userID, ok := h.svc.CurrentUser(r.Context())
	if !ok {
		web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
		return
	}
	value, err := h.mineIdentifier(r.Context(), userID, kind)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	page, err := h.inv.Mine(r.Context(), kind, value, req)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONOK(w, newPageResponse(page, newMyInvitationResponse))
}

// mineIdentifier is the caller's first claim-holding verified identifier of
// kind (primary first); without a claim lister it is ActiveVerifiedIdentifier.
func (h *handlers) mineIdentifier(ctx context.Context, userID, kind string) (string, error) {
	if h.claims == nil {
		return h.svc.ActiveVerifiedIdentifier(ctx, userID, kind)
	}
	addresses, err := h.claims.VerifiedIdentifiers(ctx, userID)
	if err != nil {
		return "", err
	}
	for _, a := range addresses {
		if a.Kind == kind {
			return a.Value, nil
		}
	}
	return "", fmt.Errorf("no sign-in %s identifier for user: %w", kind, sdk.ErrNotFound)
}

// acceptInvitation redeems a token for the calling user (session-gated). An
// email or phone invitation is matched inside the service against every active
// verified identifier of the caller (design §7/V11); a mismatch is a 403
// invitation_identity_mismatch.
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
	res, err := h.inv.Accept(r.Context(), invitations.AcceptInput{
		Token:       req.Token,
		SubjectType: authlogic.PrincipalUser,
		SubjectID:   userID,
	})
	if err != nil {
		writeInvitationError(w, err)
		return
	}
	web.RespondJSONOK(w, map[string]string{
		"resource_type": res.ResourceType,
		"resource_id":   res.ResourceID,
		"relation":      res.Relation,
	})
}

// cancelInvitation cancels a pending invitation the caller may administer
// (session-gated; see prepareInvitationManagement).
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

// resendInvitation regenerates and re-mails a pending invitation the caller may
// administer (session-gated; see prepareInvitationManagement).
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
		writeInvitationError(w, err)
		return
	}
	web.RespondJSONOK(w, newInvitationResponse(inv))
}

// prepareInvitationManagement admits the caller against the exact loaded target:
// with the resource rule, a holder of the resource's permission (a denial is an
// indistinguishable 404); without it, only the issuer. The prepared value pins
// the row used by the subsequent mutation and carries the caller as audit actor.
func (h *handlers) prepareInvitationManagement(w http.ResponseWriter, r *http.Request, userID string) (invitations.PreparedManagement, bool) {
	id := web.Param(r, "id")
	prepared, err := h.inv.PrepareManagement(r.Context(), id)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return invitations.PreparedManagement{}, false
	}
	if id == "" || prepared.ID() != id || userID == "" {
		web.RespondJSONDomainError(w, sdk.ErrForbidden)
		return invitations.PreparedManagement{}, false
	}
	principal := sdk.Principal{Type: sdk.PrincipalTypeUser, ID: userID}
	if h.resourceRuleOn() {
		if err := h.checkResource(r.Context(), principal, prepared.ResourceType(), prepared.ResourceID()); err != nil {
			if errors.Is(err, sdk.ErrForbidden) {
				err = sdk.ErrNotFound
			}
			web.RespondJSONDomainError(w, err)
			return invitations.PreparedManagement{}, false
		}
	} else if prepared.InvitedBy() != userID {
		web.RespondJSONDomainError(w, sdk.ErrForbidden)
		return invitations.PreparedManagement{}, false
	}
	prepared = prepared.WithActor(principal)
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

// declineInvitationByToken declines the pending invitation a token addresses
// (PUBLIC, IP-rate-limited) for a landing page holding only the mailed secret.
// An unknown or wrong token is the same 404 as the id route; a token past
// expiry is 410; an invitation already accepted or declined is 409, so a second
// click reads as done.
func (h *handlers) declineInvitationByToken(decliner invitationTokenDecliner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req declineInvitationRequest
		if !strictJSONBody(w, r, &req, maxJSONBodyBytes) {
			return
		}
		if req.Token == "" {
			web.RespondJSONError(w, web.ErrBadRequest("invalid request body"))
			return
		}
		if err := decliner.DeclineByToken(r.Context(), req.Token); err != nil {
			web.RespondJSONDomainError(w, err)
			return
		}
		web.RespondJSONOK(w, map[string]string{"status": "declined"})
	}
}

// syncInvitationsResponse is the body of POST /auth/invitations/sync: how many
// auto-accept invitations this call granted.
type syncInvitationsResponse struct {
	Resolved int `json:"resolved"`
}

// syncInvitations resolves, on demand, the pending auto-accept invitations
// addressed to any of the caller's verified identifiers (session-gated). It is
// idempotent and budgeted per user; exhaustion is a 429.
func (h *handlers) syncInvitations(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	var body struct{}
	if !strictJSONBody(w, r, &body, maxJSONBodyBytes) {
		return
	}
	userID, ok := h.svc.CurrentUser(r.Context())
	if !ok {
		web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
		return
	}
	n, err := h.syncer.SyncInvitations(r.Context(), userID)
	if err != nil {
		writeInvitationError(w, err)
		return
	}
	web.RespondJSONOK(w, syncInvitationsResponse{Resolved: n})
}

// writeInvitationError maps the invitation refusals that carry a machine code
// before the shared domain mapping: a redirect with a fragment is 400
// invitation_redirect_fragment, an accept by a caller holding no matching
// verified identifier is 403 invitation_identity_mismatch.
func writeInvitationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, invitations.ErrRedirectFragment):
		web.RespondJSONError(w, web.NewError(http.StatusBadRequest, "invitation redirect must not carry a fragment").WithCode("invitation_redirect_fragment"))
	case errors.Is(err, invitations.ErrIdentifierMismatch):
		web.RespondJSONError(w, web.NewError(http.StatusForbidden, "invitation is addressed to another identifier").WithCode("invitation_identity_mismatch"))
	default:
		respondDomainError(w, err)
	}
}
