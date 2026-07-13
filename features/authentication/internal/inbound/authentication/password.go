package authentication

import (
	"errors"
	"net/http"

	"github.com/gopernicus/gopernicus/features/authentication/domain/credential"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// Credential-suite password transport (design §5.2/§5.3). Every route here is a
// cookie-authenticated sensitive mutation: RequireLiveSession has proven the
// caller's session and stamped its id, and the browser-safe-mutation gate has
// applied the allowlisted-Origin + CSRF check (design §9.1). Each handler adds the
// strict JSON body hardening and sets Cache-Control: no-store.

// setPasswordRequest sets an initial password on an account that has none.
type setPasswordRequest struct {
	NewPassword string `json:"new_password"`
}

// removePasswordStartRequest starts the code-gated password removal; it carries no
// fields (the destination is the account's verified recovery identifier, chosen by
// policy, never a body value).
type removePasswordStartRequest struct{}

// removePasswordRequest completes the removal with the delivered remove_password code.
type removePasswordRequest struct {
	Code string `json:"code"`
}

// setPassword / startRemovePassword / removePassword dispatch their POST by
// Content-Type: the JSON arm keeps the existing contract, a form body renders or
// redirects through the HTML surface (only when Views is wired). Both arms call the
// same credential-suite service methods (design §5.2/§5.3/§9.2).
func (h *handlers) setPassword(w http.ResponseWriter, r *http.Request) {
	h.dispatch(w, r, h.setPasswordJSON, h.setPasswordForm)
}

func (h *handlers) startRemovePassword(w http.ResponseWriter, r *http.Request) {
	h.dispatch(w, r, h.startRemovePasswordJSON, h.startRemovePasswordForm)
}

func (h *handlers) removePassword(w http.ResponseWriter, r *http.Request) {
	h.dispatch(w, r, h.removePasswordJSON, h.removePasswordForm)
}

// setPasswordJSON sets an initial password behind a consumed set_password grant, then
// revokes every session and sets fresh cookies for the caller (design §5.2).
func (h *handlers) setPasswordJSON(w http.ResponseWriter, r *http.Request) {
	writeNoStore(w)
	if !requireJSON(w, r) {
		return
	}
	var req setPasswordRequest
	if !strictJSONBody(w, r, &req, maxJSONBodyBytes) {
		return
	}
	userID, sessionID, ok := h.stepUpPrincipal(w, r)
	if !ok {
		return
	}
	pair, err := h.svc.SetPassword(r.Context(), sessionID, userID, req.NewPassword)
	if err != nil {
		writePasswordError(w, err)
		return
	}
	h.svc.SetSessionCookies(w, pair)
	web.RespondJSONOK(w, map[string]string{"status": "password_set"})
}

// startRemovePasswordJSON issues a remove_password code to the caller's verified
// recovery identifier and returns the PII-free delivery receipt (design §5.3).
func (h *handlers) startRemovePasswordJSON(w http.ResponseWriter, r *http.Request) {
	writeNoStore(w)
	if !requireJSON(w, r) {
		return
	}
	var req removePasswordStartRequest
	if !strictJSONBody(w, r, &req, maxJSONBodyBytes) {
		return
	}
	userID, _, ok := h.stepUpPrincipal(w, r)
	if !ok {
		return
	}
	receipt, err := h.svc.StartRemovePassword(r.Context(), userID)
	if err != nil {
		writePasswordError(w, err)
		return
	}
	web.RespondJSONOK(w, stepUpBeginResponse{Status: "sent", Receipt: receipt.Receipt})
}

// removePasswordJSON consumes the delivered code, removes the password through the
// revision-serialized credential rail, revokes every session, and sets fresh
// cookies for the caller (design §5.3).
func (h *handlers) removePasswordJSON(w http.ResponseWriter, r *http.Request) {
	writeNoStore(w)
	if !requireJSON(w, r) {
		return
	}
	var req removePasswordRequest
	if !strictJSONBody(w, r, &req, maxJSONBodyBytes) {
		return
	}
	userID, _, ok := h.stepUpPrincipal(w, r)
	if !ok {
		return
	}
	pair, err := h.svc.RemovePassword(r.Context(), userID, req.Code)
	if err != nil {
		writePasswordError(w, err)
		return
	}
	h.svc.SetSessionCookies(w, pair)
	web.RespondJSONOK(w, map[string]string{"status": "password_removed"})
}

// writePasswordError maps the credential-suite's stable errors to their pinned
// machine codes (design §5.8) before falling back to the generic domain-error
// mapping, so a client can branch on password_already_set / password_not_set /
// cannot_remove_last_method without string-parsing.
func writePasswordError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, authsvc.ErrPasswordAlreadySet):
		web.RespondJSONError(w, web.NewError(http.StatusConflict, "password already set").WithCode("password_already_set"))
	case errors.Is(err, authsvc.ErrPasswordNotSet):
		web.RespondJSONError(w, web.NewError(http.StatusNotFound, "password not set").WithCode("password_not_set"))
	case errors.Is(err, credential.ErrNoLoginMethod):
		web.RespondJSONError(w, web.NewError(http.StatusConflict, "cannot remove last authentication method").WithCode("cannot_remove_last_method"))
	default:
		web.RespondJSONDomainError(w, err)
	}
}
