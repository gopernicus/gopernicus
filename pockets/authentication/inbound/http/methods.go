package authenticationhttp

import (
	"net/http"
	"time"

	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// GET /auth/methods — the masked credential/identifier inventory (design §5.1). It
// rides the SessionSecurityReads authenticator because it returns sensitive contact
// and credential inventory, so a revoked access JWT is denied within one
// round-trip. It is a bearer-safe read: GET with no request body, so it skips the
// browser-safe-mutation CSRF gate (that gate protects state-changing routes). The
// handler sets Cache-Control: no-store so the inventory is never retained by a
// shared cache. The response also names the calling session's assurance and,
// with TOTP enabled, the authenticator app and recovery codes, so an SPA picks
// its step-up after an MFA 401.

// methodsResponse is the GET /auth/methods body (design §5.1). Identifier values
// are masked; the removable hints are advisory (the mutation guard is
// authoritative).
type methodsResponse struct {
	HasPassword bool                       `json:"has_password"`
	OAuth       []oauthMethodResponse      `json:"oauth"`
	Identifiers []identifierMethodResponse `json:"identifiers"`
	// Passkeys is present only when passkeys are enabled; an SPA reads it to
	// choose a step-up (a user-verified passkey reaches AAL2).
	Passkeys *[]passkeyMethodResponse `json:"passkeys,omitempty"`
	// Assurance is the calling session's level (aal1 or aal2), so an SPA that
	// meets an MFA 401 knows it must step up; omitted for a token minted before
	// the aal claim.
	Assurance string `json:"assurance,omitempty"`
	// TOTP and RecoveryCodes are present only when TOTP is enabled: whether an
	// authenticator app is confirmed, and how many recovery codes remain.
	TOTP          *totpMethodResponse          `json:"totp,omitempty"`
	RecoveryCodes *recoveryCodesMethodResponse `json:"recovery_codes,omitempty"`
}

// totpMethodResponse is the authenticator app in the inventory.
type totpMethodResponse struct {
	Enrolled   bool   `json:"enrolled"`
	LastUsedAt string `json:"last_used_at,omitempty"`
}

// recoveryCodesMethodResponse is the recovery codes left in the inventory.
type recoveryCodesMethodResponse struct {
	Remaining int `json:"remaining"`
}

// passkeyMethodResponse is one passkey in the inventory: the list item plus the
// advisory removable hint.
type passkeyMethodResponse struct {
	passkeyResponse
	Removable bool `json:"removable"`
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

// methods returns the caller's masked method inventory. The authenticator has
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
	out := newMethodsResponse(view)
	if cred, ok := h.svc.CurrentCredential(r.Context()); ok {
		out.Assurance = string(cred.Assurance)
	}
	web.RespondJSONOK(w, out)
}

// newMethodsResponse renders the service view model into the JSON contract. Empty
// slices render as [] (not null) so a client always sees the arrays.
func newMethodsResponse(view authlogic.MethodsView) methodsResponse {
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
	if view.Passkeys != nil {
		passkeys := make([]passkeyMethodResponse, 0, len(view.Passkeys))
		for _, p := range view.Passkeys {
			item := passkeyMethodResponse{Removable: p.Removable, passkeyResponse: passkeyResponse{
				ID: p.ID, Name: p.Name, CreatedAt: p.CreatedAt.UTC().Format(time.RFC3339),
				UserVerified: p.UserVerified, BackupEligible: p.BackupEligible, BackupState: p.BackupState,
				Transports: append([]string{}, p.Transports...),
			}}
			if p.LastUsedAt != nil {
				item.LastUsedAt = p.LastUsedAt.UTC().Format(time.RFC3339)
			}
			passkeys = append(passkeys, item)
		}
		out.Passkeys = &passkeys
	}
	if view.TOTP != nil {
		out.TOTP = &totpMethodResponse{Enrolled: view.TOTP.Enrolled, LastUsedAt: rfc3339(view.TOTP.LastUsedAt)}
	}
	if view.RecoveryCodes != nil {
		out.RecoveryCodes = &recoveryCodesMethodResponse{Remaining: view.RecoveryCodes.Remaining}
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
