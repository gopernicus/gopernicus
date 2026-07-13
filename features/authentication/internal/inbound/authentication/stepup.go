package authentication

import (
	"net/http"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// Step-up (recent-authentication grant) transport (design §5.0). Every route here
// is a cookie-authenticated sensitive mutation: RequireLiveSession has proven the
// caller's session and stamped its id, and the browser-safe-mutation gate has
// applied the allowlisted-Origin + CSRF check (design §9.1). Each handler adds the
// strict JSON body hardening and sets Cache-Control: no-store, since the responses
// concern the caller's credential posture.

// stepUpBeginRequest starts a step-up: the operation the earned grant will
// authorize (purpose + context) and the identifier kind to deliver the code to
// (empty → email).
type stepUpBeginRequest struct {
	Purpose string `json:"purpose"`
	Context string `json:"context"`
	Kind    string `json:"kind"`
}

// stepUpBeginResponse reports the step-up code was enqueued and hands back the
// PII-free delivery receipt the caller can poll (design §6.1.1). It never carries a
// destination or code.
type stepUpBeginResponse struct {
	Status  string `json:"status"`
	Receipt string `json:"receipt"`
}

// stepUpPasswordRequest completes a step-up by re-verifying the existing password.
type stepUpPasswordRequest struct {
	Purpose  string `json:"purpose"`
	Context  string `json:"context"`
	Password string `json:"password"`
}

// stepUpCodeRequest completes a step-up by consuming the delivered code.
type stepUpCodeRequest struct {
	Purpose string `json:"purpose"`
	Context string `json:"context"`
	Code    string `json:"code"`
}

// stepUpCompleteResponse reports a grant was earned and when it expires. The grant
// itself is server-side and single-use; the caller only needs to know it is now
// authorized to perform the bound mutation until ExpiresAt.
type stepUpCompleteResponse struct {
	Status    string `json:"status"`
	ExpiresAt string `json:"expires_at"`
}

// beginStepUp / completeStepUpPassword / completeStepUpCode dispatch their POST by
// Content-Type: the JSON arm keeps the existing contract, a form body renders or
// redirects through the HTML surface (only when Views is wired). Both arms call the
// same step-up service methods (design §5.0/§9.2).
func (h *handlers) beginStepUp(w http.ResponseWriter, r *http.Request) {
	h.dispatch(w, r, h.beginStepUpJSON, h.beginStepUpForm)
}

func (h *handlers) completeStepUpPassword(w http.ResponseWriter, r *http.Request) {
	h.dispatch(w, r, h.completeStepUpPasswordJSON, h.completeStepUpPasswordForm)
}

func (h *handlers) completeStepUpCode(w http.ResponseWriter, r *http.Request) {
	h.dispatch(w, r, h.completeStepUpCodeJSON, h.completeStepUpCodeForm)
}

// beginStepUpJSON issues and delivers a step-up code to an existing verified identifier.
func (h *handlers) beginStepUpJSON(w http.ResponseWriter, r *http.Request) {
	writeNoStore(w)
	if !requireJSON(w, r) {
		return
	}
	var req stepUpBeginRequest
	if !strictJSONBody(w, r, &req, maxJSONBodyBytes) {
		return
	}
	userID, sessionID, ok := h.stepUpPrincipal(w, r)
	if !ok {
		return
	}
	receipt, err := h.svc.BeginStepUp(r.Context(), authsvc.StepUpStart{
		SessionID: sessionID,
		UserID:    userID,
		Purpose:   req.Purpose,
		Context:   req.Context,
		Kind:      req.Kind,
	})
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONOK(w, stepUpBeginResponse{Status: "sent", Receipt: receipt.Receipt})
}

// completeStepUpPasswordJSON earns a grant by re-verifying the caller's password.
func (h *handlers) completeStepUpPasswordJSON(w http.ResponseWriter, r *http.Request) {
	writeNoStore(w)
	if !requireJSON(w, r) {
		return
	}
	var req stepUpPasswordRequest
	if !strictJSONBody(w, r, &req, maxJSONBodyBytes) {
		return
	}
	userID, sessionID, ok := h.stepUpPrincipal(w, r)
	if !ok {
		return
	}
	grant, err := h.svc.CompleteStepUpWithPassword(r.Context(), authsvc.StepUpCompletion{
		SessionID: sessionID,
		UserID:    userID,
		Purpose:   req.Purpose,
		Context:   req.Context,
	}, req.Password)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONOK(w, stepUpCompleteResponse{Status: "verified", ExpiresAt: grant.ExpiresAt.Format(time.RFC3339)})
}

// completeStepUpCodeJSON earns a grant by consuming the delivered step-up code.
func (h *handlers) completeStepUpCodeJSON(w http.ResponseWriter, r *http.Request) {
	writeNoStore(w)
	if !requireJSON(w, r) {
		return
	}
	var req stepUpCodeRequest
	if !strictJSONBody(w, r, &req, maxJSONBodyBytes) {
		return
	}
	userID, sessionID, ok := h.stepUpPrincipal(w, r)
	if !ok {
		return
	}
	grant, err := h.svc.CompleteStepUpWithIdentifierCode(r.Context(), authsvc.StepUpCompletion{
		SessionID: sessionID,
		UserID:    userID,
		Purpose:   req.Purpose,
		Context:   req.Context,
	}, req.Code)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONOK(w, stepUpCompleteResponse{Status: "verified", ExpiresAt: grant.ExpiresAt.Format(time.RFC3339)})
}

// stepUpPrincipal resolves the caller's user id and live session id that
// RequireLiveSession stamped. A step-up grant is always bound to the session the
// caller proved, never to a body field, so a missing session id denies.
func (h *handlers) stepUpPrincipal(w http.ResponseWriter, r *http.Request) (userID, sessionID string, ok bool) {
	userID, uok := h.svc.CurrentUser(r.Context())
	sessionID, sok := h.svc.CurrentSessionID(r.Context())
	if !uok || !sok {
		web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
		return "", "", false
	}
	return userID, sessionID, true
}
