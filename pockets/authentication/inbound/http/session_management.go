package authenticationhttp

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/gopernicus/gopernicus/pockets"
	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

func mountSessionManagement(r pockets.RouteRegistrar, h *handlers) {
	live := htmlSecured(h.htmlPolicy, h.svc.RequirePrincipal(FirstParty(), Transports(authlogic.TransportCookie), Live(), Browser()))
	mutation := requireBrowserSafeMutation(h.mutation.csrf())
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
		profile := string(sess.Profile)
		if sess.FirstParty() {
			profile = string(session.ProfileFirstParty)
		}
		clientName := ""
		if sess.Profile == session.ProfileDelegated {
			clientName, _ = h.oauth2.Service.ClientDisplayName(r.Context(), sess.Delegation.ClientID)
		}
		m.Sessions = append(m.Sessions, SessionView{ID: sess.ID, Profile: profile, ClientName: clientName, ClientID: sess.Delegation.ClientID,
			Resource: sess.Delegation.Resource, CreatedAt: sess.CreatedAt, ExpiresAt: sess.ExpiresAt, Current: sess.ID == currentID})
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
