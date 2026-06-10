package invitations

import (
	"errors"
	"net/http"

	"github.com/gopernicus/gopernicus/bridge/transit/httpmid"
	invitationscore "github.com/gopernicus/gopernicus/core/auth/invitations"
	"github.com/gopernicus/gopernicus/sdk/fop"
	"github.com/gopernicus/gopernicus/sdk/web"
)

// AddHttpRoutes registers invitation endpoints on the given route group.
func (b *Bridge) AddHttpRoutes(group *web.RouteGroup) {
	// Create + list — authorized against the target resource via
	// AuthorizeDynamicParam: resource_type is dynamic (tenant, project, etc.).
	group.POST("/{resource_type}/{resource_id}", b.httpCreate,
		httpmid.MaxBodySize(httpmid.DefaultBodySize),
		httpmid.Authenticate(b.authenticator, b.log, b.jsonErrors),
		httpmid.RateLimit(b.rateLimiter, b.log),
		httpmid.AuthorizeDynamicParam(b.authorizer, b.log, b.jsonErrors, "manage", "resource_type", "resource_id"),
	)
	group.GET("/{resource_type}/{resource_id}",
		b.httpListByResource,
		httpmid.MaxBodySize(httpmid.DefaultBodySize),
		httpmid.Authenticate(b.authenticator, b.log, b.jsonErrors),
		httpmid.RateLimit(b.rateLimiter, b.log),
		httpmid.AuthorizeDynamicParam(b.authorizer, b.log, b.jsonErrors, "manage", "resource_type", "resource_id"),
	)

	group.POST("/{invitation_id}/cancel", b.httpCancel,
		httpmid.MaxBodySize(httpmid.DefaultBodySize),
		httpmid.Authenticate(b.authenticator, b.log, b.jsonErrors),
		httpmid.RateLimit(b.rateLimiter, b.log),
		httpmid.AuthorizeParam(b.authorizer, b.log, b.jsonErrors, "invitation", "manage", "invitation_id"),
	)

	group.POST("/{invitation_id}/resend", b.httpResend,
		httpmid.MaxBodySize(httpmid.DefaultBodySize),
		httpmid.Authenticate(b.authenticator, b.log, b.jsonErrors),
		httpmid.RateLimit(b.rateLimiter, b.log),
		httpmid.AuthorizeParam(b.authorizer, b.log, b.jsonErrors, "invitation", "manage", "invitation_id"),
	)

	// Self-service — authenticated user lists their own invitations.
	group.GET("/mine", b.httpListMine,
		httpmid.MaxBodySize(httpmid.DefaultBodySize),
		httpmid.Authenticate(b.authenticator, b.log, b.jsonErrors, httpmid.WithUserSession()),
		httpmid.RateLimit(b.rateLimiter, b.log),
	)

	// Accept — authenticated with full user context (need email for verification).
	group.POST("/accept", b.httpAccept,
		httpmid.MaxBodySize(httpmid.DefaultBodySize),
		httpmid.Authenticate(b.authenticator, b.log, b.jsonErrors, httpmid.WithUserSession()),
		httpmid.RateLimit(b.rateLimiter, b.log))

	// Decline — public (email-verified in handler), so rate limiting is the
	// only brake on invitation_id×identifier brute force.
	group.POST("/{invitation_id}/decline", b.httpDecline,
		httpmid.MaxBodySize(httpmid.DefaultBodySize),
		httpmid.RateLimit(b.rateLimiter, b.log),
	)
}

// ---------------------------------------------------------------------------
// Resource-scoped handlers (authorized against target resource)
// ---------------------------------------------------------------------------

func (b *Bridge) httpCreate(w http.ResponseWriter, r *http.Request) {
	resourceType := web.Param(r, "resource_type")
	resourceID := web.Param(r, "resource_id")

	req, err := web.DecodeJSON[CreateInvitationRequest](r)
	if err != nil {
		web.RespondJSONError(w, web.ErrValidation(err))
		return
	}

	userID := httpmid.GetSubjectID(r.Context())

	result, err := b.invitations.Create(r.Context(), invitationscore.CreateInput{
		ResourceType:   resourceType,
		ResourceID:     resourceID,
		Relation:       req.Relation,
		Identifier:     req.Identifier,
		IdentifierType: req.IdentifierType,
		InvitedBy:      userID,
		AutoAccept:     req.AutoAccept,
	})
	if err != nil {
		switch {
		case errors.Is(err, invitationscore.ErrAlreadyMember):
			web.RespondJSONError(w, web.ErrConflict("already a member"))
		case errors.Is(err, invitationscore.ErrPendingInvitationExists):
			web.RespondJSONError(w, web.ErrConflict("pending invitation already exists"))
		default:
			web.RespondJSONDomainError(w, err)
		}
		return
	}

	resp := CreateInvitationResponse{DirectlyAdded: result.DirectlyAdded}
	if result.Invitation != nil {
		resp.Invitation = toInvitationResponse(*result.Invitation)
	}

	web.RespondJSON(w, http.StatusCreated, resp)
}

func (b *Bridge) httpListByResource(w http.ResponseWriter, r *http.Request) {
	resourceType := web.Param(r, "resource_type")
	resourceID := web.Param(r, "resource_id")

	page, err := fop.ParsePageStringCursor(
		r.URL.Query().Get("limit"),
		r.URL.Query().Get("cursor"),
		invitationscore.MaxLimitListByResource,
	)
	if err != nil {
		web.RespondJSONError(w, web.ErrBadRequest("invalid pagination: "+err.Error()))
		return
	}

	orderBy, err := fop.ParseOrder(
		invitationscore.OrderByFields,
		r.URL.Query().Get("order"),
		fop.NewOrder(invitationscore.OrderByCreatedAt, fop.DESC),
	)
	if err != nil {
		web.RespondJSONError(w, web.ErrBadRequest("invalid order: "+err.Error()))
		return
	}
	filter := invitationscore.FilterListByResource{}
	if v := r.URL.Query().Get("status"); v != "" {
		filter.InvitationStatus = &v
	}
	if v := r.URL.Query().Get("auto_accept"); v == "true" {
		t := true
		filter.AutoAccept = &t
	}

	records, pagination, err := b.invitations.ListByResource(
		r.Context(), resourceType, resourceID, filter, orderBy, page,
	)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}

	web.RespondJSONOK(w, map[string]any{
		"data":       toInvitationResponses(records),
		"pagination": pagination,
	})
}

// ---------------------------------------------------------------------------
// Invitation-scoped handlers (authorized via through-relation)
// ---------------------------------------------------------------------------

func (b *Bridge) httpCancel(w http.ResponseWriter, r *http.Request) {
	invitationID := web.Param(r, "invitation_id")

	if err := b.invitations.Cancel(r.Context(), invitationID); err != nil {
		switch {
		case errors.Is(err, invitationscore.ErrInvitationNotFound):
			web.RespondJSONError(w, web.ErrNotFound("invitation not found"))
		case errors.Is(err, invitationscore.ErrInvitationInvalidStatus):
			web.RespondJSONError(w, web.ErrConflict("invitation is not pending"))
		default:
			web.RespondJSONDomainError(w, err)
		}
		return
	}

	web.RespondNoContent(w)
}

func (b *Bridge) httpResend(w http.ResponseWriter, r *http.Request) {
	invitationID := web.Param(r, "invitation_id")

	inv, err := b.invitations.Resend(r.Context(), invitationID)
	if err != nil {
		switch {
		case errors.Is(err, invitationscore.ErrInvitationNotFound):
			web.RespondJSONError(w, web.ErrNotFound("invitation not found"))
		case errors.Is(err, invitationscore.ErrInvitationInvalidStatus):
			web.RespondJSONError(w, web.ErrConflict("invitation cannot be resent"))
		default:
			web.RespondJSONDomainError(w, err)
		}
		return
	}

	web.RespondJSONOK(w, toInvitationResponse(*inv))
}

// ---------------------------------------------------------------------------
// Self-service handlers (authenticated only)
// ---------------------------------------------------------------------------

func (b *Bridge) httpListMine(w http.ResponseWriter, r *http.Request) {
	// List invitations linked to the authenticated user's subject ID.
	// WithUserSession() loads the full user into context.
	user := httpmid.GetUser(r.Context())
	if user == nil {
		web.RespondJSONError(w, web.ErrUnauthorized("user context not available"))
		return
	}

	page, err := fop.ParsePageStringCursor(
		r.URL.Query().Get("limit"),
		r.URL.Query().Get("cursor"),
		invitationscore.MaxLimitListBySubject,
	)
	if err != nil {
		web.RespondJSONError(w, web.ErrBadRequest("invalid pagination: "+err.Error()))
		return
	}

	orderBy, err := fop.ParseOrder(
		invitationscore.OrderByFields,
		r.URL.Query().Get("order"),
		fop.NewOrder(invitationscore.OrderByCreatedAt, fop.DESC),
	)
	if err != nil {
		web.RespondJSONError(w, web.ErrBadRequest("invalid order: "+err.Error()))
		return
	}

	filter := invitationscore.FilterListBySubject{}
	if v := r.URL.Query().Get("status"); v != "" {
		filter.InvitationStatus = &v
	}
	if v := r.URL.Query().Get("auto_accept"); v == "true" {
		t := true
		filter.AutoAccept = &t
	}

	records, pagination, err := b.invitations.ListBySubject(
		r.Context(), user.UserID, filter, orderBy, page,
	)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}

	web.RespondJSONOK(w, map[string]any{
		"data":       toInvitationResponses(records),
		"pagination": pagination,
	})
}

// ---------------------------------------------------------------------------
// Accept (authenticated) + Decline (public)
// ---------------------------------------------------------------------------

func (b *Bridge) httpAccept(w http.ResponseWriter, r *http.Request) {
	req, err := web.DecodeJSON[AcceptInvitationRequest](r)
	if err != nil {
		web.RespondJSONError(w, web.ErrValidation(err))
		return
	}

	// WithUserSession() loads the full user — get subject and email from context.
	user := httpmid.GetUser(r.Context())
	if user == nil {
		web.RespondJSONError(w, web.ErrUnauthorized("user context not available"))
		return
	}

	result, err := b.invitations.Accept(r.Context(), invitationscore.AcceptInput{
		Token:       req.Token,
		SubjectType: "user",
		SubjectID:   user.UserID,
		Identifier:  user.Email,
	})
	if err != nil {
		switch {
		case errors.Is(err, invitationscore.ErrInvitationNotFound):
			web.RespondJSONError(w, web.ErrNotFound("invitation not found"))
		case errors.Is(err, invitationscore.ErrInvitationAlreadyUsed):
			web.RespondJSONError(w, web.ErrConflict("invitation already used"))
		case errors.Is(err, invitationscore.ErrInvitationExpired):
			web.RespondJSONError(w, web.ErrGone("invitation expired"))
		case errors.Is(err, invitationscore.ErrInvitationCancelled):
			web.RespondJSONError(w, web.ErrGone("invitation cancelled"))
		case errors.Is(err, invitationscore.ErrIdentifierMismatch):
			web.RespondJSONError(w, web.ErrForbidden("identifier does not match invitation"))
		default:
			web.RespondJSONDomainError(w, err)
		}
		return
	}

	web.RespondJSONOK(w, AcceptInvitationResponse{
		ResourceType: result.ResourceType,
		ResourceID:   result.ResourceID,
		Relation:     result.Relation,
	})
}

func (b *Bridge) httpDecline(w http.ResponseWriter, r *http.Request) {
	invitationID := web.Param(r, "invitation_id")

	req, err := web.DecodeJSON[DeclineInvitationRequest](r)
	if err != nil {
		web.RespondJSONError(w, web.ErrValidation(err))
		return
	}

	if err := b.invitations.Decline(r.Context(), invitationID, req.Identifier); err != nil {
		switch {
		case errors.Is(err, invitationscore.ErrInvitationNotFound):
			web.RespondJSONError(w, web.ErrNotFound("invitation not found"))
		case errors.Is(err, invitationscore.ErrInvitationInvalidStatus):
			web.RespondJSONError(w, web.ErrConflict("invitation is not pending"))
		case errors.Is(err, invitationscore.ErrIdentifierMismatch):
			web.RespondJSONError(w, web.ErrForbidden("identifier does not match invitation"))
		default:
			web.RespondJSONDomainError(w, err)
		}
		return
	}

	web.RespondNoContent(w)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------


func toInvitationResponse(inv invitationscore.Invitation) *InvitationResponse {
	return &InvitationResponse{
		InvitationID:   inv.InvitationID,
		ResourceType:   inv.ResourceType,
		ResourceID:     inv.ResourceID,
		Relation:       inv.Relation,
		Identifier:     inv.Identifier,
		IdentifierType: inv.IdentifierType,
		InvitedBy:      inv.InvitedBy,
		Status:         inv.InvitationStatus,
		ExpiresAt:      inv.ExpiresAt,
		AcceptedAt:     inv.AcceptedAt,
		CreatedAt:      inv.CreatedAt,
	}
}

func toInvitationResponses(invs []invitationscore.Invitation) []InvitationResponse {
	out := make([]InvitationResponse, len(invs))
	for i, inv := range invs {
		out[i] = *toInvitationResponse(inv)
	}
	return out
}
