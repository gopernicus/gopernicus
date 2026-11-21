package authenticationhttp

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/gopernicus/gopernicus/pockets"
	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

func mountSessionManagement(r pockets.RouteRegistrar, h *handlers) {
	// The JSON connections contract is mounted on both consent lanes; the HTML
	// sessions page only when the pocket renders OAuth2 pages (OAuthViews).
	api := h.svc.RequirePrincipal(Accept(CredentialAccessToken), FirstParty(), Transports(authlogic.TransportCookie), Live(), AllowMFAEnrollment())
	apiMutation := requireBrowserSafeMutation(h.mutation.csrf())
	r.Handle("GET", "/auth/connections", h.listConnections, oauth2Headers, api)
	r.Handle("POST", "/auth/connections/{id}/revoke", h.revokeConnection, oauth2Headers, api, apiMutation)
	r.Handle("POST", "/auth/connections/revoke-all", h.revokeAllConnections, oauth2Headers, api, apiMutation)
	if nilDependency(h.oauth2.Views) {
		return
	}
	live := htmlSecured(h.htmlPolicy, h.svc.RequirePrincipal(Accept(CredentialAccessToken), FirstParty(), Transports(authlogic.TransportCookie), Live(), Browser(), AllowMFAEnrollment()))
	mutation := requireBrowserSafeFormMutation(h.mutation.csrf())
	r.Handle("GET", "/auth/sessions", h.sessionsPage, live)
	r.Handle("POST", "/auth/sessions/{id}/revoke", h.revokeSessionForm, live, mutation)
	r.Handle("POST", "/auth/sessions/revoke-all", h.revokeAllSessionsForm, live, mutation)
}

func (h *handlers) sessionsPage(w http.ResponseWriter, r *http.Request) {
	userID, currentID, ok := h.formPrincipal(w, r)
	if !ok {
		return
	}
	req, ok := h.parseListRequest(w, r, session.OrderFields, session.DefaultOrder)
	if !ok {
		return
	}
	page, err := h.oauth2.Sessions.ListUserSessions(r.Context(), userID, req)
	if err != nil {
		h.renderError(w, r, http.StatusServiceUnavailable, "Sessions are unavailable. Please try again.")
		return
	}
	pc := h.newPageContext(w)
	m := SessionsPage{PageContext: pc, Sessions: make([]SessionView, 0, len(page.Items))}
	for _, sess := range page.Items {
		m.Sessions = append(m.Sessions, h.sessionView(r.Context(), sess, currentID))
	}
	if page.HasMore {
		q := r.URL.Query()
		if req.ResolvedStrategy() == list.StrategyOffset {
			q.Set("offset", strconv.Itoa(req.Offset+len(page.Items)))
		} else {
			q.Set("cursor", page.NextCursor)
		}
		m.NextURL = "/auth/sessions?" + q.Encode()
	}
	h.renderPage(w, r, pc.CSPNonce, h.oauth2.Views.Sessions(m))
}

func (h *handlers) revokeSessionForm(w http.ResponseWriter, r *http.Request) {
	h.accountForm(w, r, func(_ url.Values) {
		userID, currentID, ok := h.formPrincipal(w, r)
		if !ok {
			return
		}
		id := r.PathValue("id")
		if err := h.oauth2.Sessions.RevokeUserSession(r.Context(), userID, id); err != nil {
			h.renderError(w, r, http.StatusBadRequest, "That session could not be revoked.")
			return
		}
		if id == currentID {
			h.svc.ClearSessionCookies(w)
			h.prgTo(w, r, "/auth/login?auth=signed_out")
			return
		}
		h.prgTo(w, r, "/auth/sessions")
	})
}

func (h *handlers) revokeAllSessionsForm(w http.ResponseWriter, r *http.Request) {
	h.accountForm(w, r, func(_ url.Values) {
		userID, _, ok := h.formPrincipal(w, r)
		if !ok {
			return
		}
		if err := h.oauth2.Sessions.RevokeAllUserSessions(r.Context(), userID); err != nil {
			h.renderError(w, r, http.StatusServiceUnavailable, "Sessions could not be revoked. Please try again.")
			return
		}
		h.svc.ClearSessionCookies(w)
		h.prgTo(w, r, "/auth/login?auth=signed_out")
	})
}

// sessionView is one listed session, shared by the HTML page and the JSON
// connections contract.
func (h *handlers) sessionView(ctx context.Context, sess session.Session, currentID string) SessionView {
	profile := string(sess.Profile)
	if sess.FirstParty() {
		profile = string(session.ProfileFirstParty)
	}
	clientName := ""
	if sess.Profile == session.ProfileDelegated {
		clientName, _ = h.oauth2.Service.ClientDisplayName(ctx, sess.Delegation.ClientID)
	}
	return SessionView{ID: sess.ID, Profile: profile, ClientName: clientName, ClientID: sess.Delegation.ClientID,
		Resource: sess.Delegation.Resource, CreatedAt: sess.CreatedAt, ExpiresAt: sess.ExpiresAt, Current: sess.ID == currentID}
}

type connectionResponse struct {
	ID         string    `json:"id"`
	Profile    string    `json:"profile"`
	ClientID   string    `json:"client_id,omitempty"`
	ClientName string    `json:"client_name,omitempty"`
	Resource   string    `json:"resource,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Current    bool      `json:"current"`
}

// listConnections is GET /auth/connections: the caller's live sessions,
// first-party and delegated, paged like the HTML sessions page.
func (h *handlers) listConnections(w http.ResponseWriter, r *http.Request) {
	userID, currentID, ok := h.oauth2JSONPrincipal(w, r)
	if !ok {
		return
	}
	req, ok := h.parseListRequest(w, r, session.OrderFields, session.DefaultOrder)
	if !ok {
		return
	}
	page, err := h.oauth2.Sessions.ListUserSessions(r.Context(), userID, req)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONOK(w, newPageResponse(page, func(sess session.Session) connectionResponse {
		v := h.sessionView(r.Context(), sess, currentID)
		return connectionResponse{ID: v.ID, Profile: v.Profile, ClientID: v.ClientID, ClientName: v.ClientName, Resource: v.Resource, CreatedAt: v.CreatedAt, ExpiresAt: v.ExpiresAt, Current: v.Current}
	}))
}

// revokeConnection is POST /auth/connections/{id}/revoke: 204, clearing the
// session cookies when the caller revoked its own session.
func (h *handlers) revokeConnection(w http.ResponseWriter, r *http.Request) {
	userID, currentID, ok := h.oauth2JSONPrincipal(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if err := h.oauth2.Sessions.RevokeUserSession(r.Context(), userID, id); err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	if id == currentID {
		h.svc.ClearSessionCookies(w)
	}
	w.WriteHeader(http.StatusNoContent)
}

// revokeAllConnections is POST /auth/connections/revoke-all: every session of
// the caller, as the HTML revoke-all, including the fence on approved but
// unredeemed codes; 204 with the session cookies cleared.
func (h *handlers) revokeAllConnections(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := h.oauth2JSONPrincipal(w, r)
	if !ok {
		return
	}
	if err := h.oauth2.Sessions.RevokeAllUserSessions(r.Context(), userID); err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	h.svc.ClearSessionCookies(w)
	w.WriteHeader(http.StatusNoContent)
}
