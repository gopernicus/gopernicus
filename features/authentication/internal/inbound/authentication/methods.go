package authentication

import (
	"net/http"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// GET /auth/methods — the masked credential/identifier inventory (design §5.1). It
// is live-session-gated (RequireLiveSession) because it returns sensitive contact
// and credential inventory, so a revoked access JWT is denied within one
// round-trip. It is a bearer-safe read: GET with no request body, so it skips the
// browser-safe-mutation CSRF gate (that gate protects state-changing routes). The
// handler sets Cache-Control: no-store so the inventory is never retained by a
// shared cache.

// methodsResponse is the GET /auth/methods body (design §5.1). Identifier values
// are masked; the removable hints are advisory (the mutation guard is
// authoritative).
type methodsResponse struct {
	HasPassword bool                       `json:"has_password"`
	OAuth       []oauthMethodResponse      `json:"oauth"`
	Identifiers []identifierMethodResponse `json:"identifiers"`
}

// oauthMethodResponse is one linked provider in the inventory.
type oauthMethodResponse struct {
	Provider  string `json:"provider"`
	LinkedAt  string `json:"linked_at,omitempty"`
	Assurance string `json:"assurance,omitempty"`
	Removable bool   `json:"removable"`
}

// identifierMethodResponse is one active identifier in the inventory. VerifiedAt is
// omitted while the address is unproven; Value is the masked address.
type identifierMethodResponse struct {
	ID         string   `json:"id"`
	Kind       string   `json:"kind"`
	Value      string   `json:"value"`
	VerifiedAt string   `json:"verified_at,omitempty"`
	Uses       []string `json:"uses"`
	Primary    bool     `json:"primary"`
	Removable  bool     `json:"removable"`
}

// methods returns the caller's masked method inventory. RequireLiveSession has
// already validated the session and stashed the user id.
func (h *handlers) methods(w http.ResponseWriter, r *http.Request) {
	writeNoStore(w)
	userID, ok := h.svc.CurrentUser(r.Context())
	if !ok {
		web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
		return
	}
	view, err := h.svc.Methods(r.Context(), userID)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONOK(w, newMethodsResponse(view))
}

// newMethodsResponse renders the service view model into the JSON contract. Empty
// slices render as [] (not null) so a client always sees the arrays.
func newMethodsResponse(view authsvc.MethodsView) methodsResponse {
	out := methodsResponse{
		HasPassword: view.HasPassword,
		OAuth:       make([]oauthMethodResponse, 0, len(view.OAuth)),
		Identifiers: make([]identifierMethodResponse, 0, len(view.Identifiers)),
	}
	for _, o := range view.OAuth {
		entry := oauthMethodResponse{
			Provider:  o.Provider,
			Assurance: o.Assurance,
			Removable: o.Removable,
		}
		if !o.LinkedAt.IsZero() {
			entry.LinkedAt = o.LinkedAt.Format(time.RFC3339)
		}
		out.OAuth = append(out.OAuth, entry)
	}
	for _, m := range view.Identifiers {
		entry := identifierMethodResponse{
			ID:        m.ID,
			Kind:      m.Kind,
			Value:     m.MaskedValue,
			Uses:      usesList(m.Uses.Login, m.Uses.Recovery, m.Uses.Notification),
			Primary:   m.Primary,
			Removable: m.Removable,
		}
		if !m.VerifiedAt.IsZero() {
			entry.VerifiedAt = m.VerifiedAt.Format(time.RFC3339)
		}
		out.Identifiers = append(out.Identifiers, entry)
	}
	return out
}

// usesList renders the role flags as the stable ["login","recovery","notification"]
// ordering, omitting flags that are unset. It is always a non-nil slice so the JSON
// field is [] rather than null.
func usesList(login, recovery, notification bool) []string {
	uses := make([]string, 0, 3)
	if login {
		uses = append(uses, "login")
	}
	if recovery {
		uses = append(uses, "recovery")
	}
	if notification {
		uses = append(uses, "notification")
	}
	return uses
}
