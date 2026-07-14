package authentication

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gopernicus/gopernicus/features/authentication/domain/credential"
	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// Identifier-management transport (design §5.5). Every route here is a
// cookie-authenticated sensitive mutation: RequireLiveSession has proven the caller's
// session and stamped its id, and the browser-safe-mutation gate has applied the
// allowlisted-Origin + CSRF check (design §9.1). The body-carrying handlers add the
// strict JSON hardening; every handler sets Cache-Control: no-store.

// identifierUsesDTO is the requested role flags on an add/change or use-change.
type identifierUsesDTO struct {
	Login        bool `json:"login"`
	Recovery     bool `json:"recovery"`
	Notification bool `json:"notification"`
}

func (d identifierUsesDTO) uses() identifier.Uses {
	return identifier.Uses{Login: d.Login, Recovery: d.Recovery, Notification: d.Notification}
}

// startEmailIdentifierRequest is POST /auth/identifiers/email.
type startEmailIdentifierRequest struct {
	Email       string            `json:"email"`
	Uses        identifierUsesDTO `json:"uses"`
	MakePrimary bool              `json:"make_primary"`
}

// startPhoneIdentifierRequest is POST /auth/identifiers/phone.
type startPhoneIdentifierRequest struct {
	Phone       string            `json:"phone"`
	Uses        identifierUsesDTO `json:"uses"`
	MakePrimary bool              `json:"make_primary"`
}

// confirmIdentifierRequest is POST /auth/identifiers/{email,phone}/confirm.
type confirmIdentifierRequest struct {
	Code string `json:"code"`
}

// patchIdentifierRequest is PATCH /auth/identifiers/{id}.
type patchIdentifierRequest struct {
	Uses        identifierUsesDTO `json:"uses"`
	MakePrimary bool              `json:"make_primary"`
}

// startEmailIdentifier / startPhoneIdentifier / confirmEmailIdentifier /
// confirmPhoneIdentifier dispatch their POST by Content-Type: the JSON arm keeps the
// existing contract, a form body renders or redirects through the HTML surface (only
// when Views is wired). Both arms call the same identifier service methods
// (design §5.5/§9.2). The PATCH/DELETE {id} edges stay JSON-only — an HTML form
// cannot emit those verbs, so the form edit posts to POST /auth/identifiers/{id}
// (identifierEditForm) instead.
func (h *handlers) startEmailIdentifier(w http.ResponseWriter, r *http.Request) {
	h.dispatch(w, r, h.startEmailIdentifierJSON, h.startEmailIdentifierForm)
}

func (h *handlers) startPhoneIdentifier(w http.ResponseWriter, r *http.Request) {
	h.dispatch(w, r, h.startPhoneIdentifierJSON, h.startPhoneIdentifierForm)
}

func (h *handlers) confirmEmailIdentifier(w http.ResponseWriter, r *http.Request) {
	h.dispatch(w, r, h.confirmEmailIdentifierJSON, h.confirmEmailIdentifierForm)
}

func (h *handlers) confirmPhoneIdentifier(w http.ResponseWriter, r *http.Request) {
	h.dispatch(w, r, h.confirmPhoneIdentifierJSON, h.confirmPhoneIdentifierForm)
}

// startEmailIdentifierJSON begins an email add/change: it delivers an ownership-proof
// code to the proposed new address and returns the PII-free delivery receipt.
func (h *handlers) startEmailIdentifierJSON(w http.ResponseWriter, r *http.Request) {
	writeNoStore(w)
	if !requireJSON(w, r) {
		return
	}
	var req startEmailIdentifierRequest
	if !strictJSONBody(w, r, &req, maxJSONBodyBytes) {
		return
	}
	h.startIdentifierChange(w, r, identifier.KindEmail, req.Email, req.Uses, req.MakePrimary)
}

// startPhoneIdentifierJSON begins a phone add/change (design §5.5).
func (h *handlers) startPhoneIdentifierJSON(w http.ResponseWriter, r *http.Request) {
	writeNoStore(w)
	if !requireJSON(w, r) {
		return
	}
	var req startPhoneIdentifierRequest
	if !strictJSONBody(w, r, &req, maxJSONBodyBytes) {
		return
	}
	h.startIdentifierChange(w, r, identifier.KindPhone, req.Phone, req.Uses, req.MakePrimary)
}

// startIdentifierChange is the shared start core: it resolves the live-session
// principal and dispatches the kind-specific add/change to the service.
func (h *handlers) startIdentifierChange(w http.ResponseWriter, r *http.Request, kind identifier.Kind, value string, uses identifierUsesDTO, makePrimary bool) {
	userID, sessionID, ok := h.stepUpPrincipal(w, r)
	if !ok {
		return
	}
	receipt, err := h.svc.StartIdentifierChange(r.Context(), authsvc.IdentifierChangeStart{
		SessionID:   sessionID,
		UserID:      userID,
		Kind:        kind,
		Value:       value,
		Uses:        uses.uses(),
		MakePrimary: makePrimary,
	})
	if err != nil {
		writeIdentifierError(w, err)
		return
	}
	web.RespondJSONOK(w, stepUpBeginResponse{Status: "sent", Receipt: receipt.Receipt})
}

// confirmEmailIdentifierJSON / confirmPhoneIdentifierJSON complete an add/change by
// consuming the delivered proof code and applying the verified change.
func (h *handlers) confirmEmailIdentifierJSON(w http.ResponseWriter, r *http.Request) {
	h.confirmIdentifierChange(w, r, identifier.KindEmail)
}

func (h *handlers) confirmPhoneIdentifierJSON(w http.ResponseWriter, r *http.Request) {
	h.confirmIdentifierChange(w, r, identifier.KindPhone)
}

func (h *handlers) confirmIdentifierChange(w http.ResponseWriter, r *http.Request, kind identifier.Kind) {
	writeNoStore(w)
	if !requireJSON(w, r) {
		return
	}
	var req confirmIdentifierRequest
	if !strictJSONBody(w, r, &req, maxJSONBodyBytes) {
		return
	}
	userID, sessionID, ok := h.stepUpPrincipal(w, r)
	if !ok {
		return
	}
	if err := h.svc.ConfirmIdentifierChange(r.Context(), authsvc.IdentifierChangeConfirm{
		SessionID: sessionID,
		UserID:    userID,
		Kind:      kind,
		Code:      req.Code,
	}); err != nil {
		writeIdentifierError(w, err)
		return
	}
	web.RespondJSONOK(w, map[string]string{"status": "confirmed"})
}

// patchIdentifier changes an identifier's uses / primary flag through the
// revision-serialized credential rail (design §5.5).
func (h *handlers) patchIdentifier(w http.ResponseWriter, r *http.Request) {
	writeNoStore(w)
	if !requireJSON(w, r) {
		return
	}
	var req patchIdentifierRequest
	if !strictJSONBody(w, r, &req, maxJSONBodyBytes) {
		return
	}
	userID, sessionID, ok := h.stepUpPrincipal(w, r)
	if !ok {
		return
	}
	if err := h.svc.SetIdentifierUses(r.Context(), authsvc.IdentifierUsesInput{
		SessionID:    sessionID,
		UserID:       userID,
		IdentifierID: web.Param(r, "id"),
		Uses:         req.Uses.uses(),
		MakePrimary:  req.MakePrimary,
	}); err != nil {
		writeIdentifierError(w, err)
		return
	}
	web.RespondJSONOK(w, map[string]string{"status": "updated"})
}

// deleteIdentifier retires an identifier through the revision-serialized credential
// rail (design §5.5). It carries no JSON body; when the removed identifier is the
// primary of its kind, an optional ?replacement=<id> names the identifier promoted to
// primary in the same atomic operation (else the service selects one).
func (h *handlers) deleteIdentifier(w http.ResponseWriter, r *http.Request) {
	writeNoStore(w)
	userID, sessionID, ok := h.stepUpPrincipal(w, r)
	if !ok {
		return
	}
	if err := h.svc.RemoveIdentifier(r.Context(), authsvc.IdentifierRemoveInput{
		SessionID:     sessionID,
		UserID:        userID,
		IdentifierID:  web.Param(r, "id"),
		ReplacementID: strings.TrimSpace(r.URL.Query().Get("replacement")),
	}); err != nil {
		writeIdentifierError(w, err)
		return
	}
	web.RespondJSONOK(w, map[string]string{"status": "removed"})
}

// writeIdentifierError maps the identifier suite's stable errors to their pinned
// machine codes (design §5.8) before the generic domain-error fallback, so a client
// branches on kind_not_supported / rate_limited / verification_required /
// cannot_remove_last_method / identifier_exists without string-parsing.
func writeIdentifierError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, authsvc.ErrKindNotSupported):
		web.RespondJSONError(w, web.NewError(http.StatusBadRequest, "identifier kind not supported").WithCode("kind_not_supported"))
	case errors.Is(err, authsvc.ErrIdentifierChangeRateLimited):
		web.RespondJSONError(w, web.NewError(http.StatusTooManyRequests, "too many identifier change requests").WithCode("rate_limited"))
	case errors.Is(err, identifier.ErrVerificationRequired):
		web.RespondJSONError(w, web.NewError(http.StatusConflict, "identifier must be verified for this use").WithCode("verification_required"))
	case errors.Is(err, credential.ErrNoLoginMethod):
		web.RespondJSONError(w, web.NewError(http.StatusConflict, "cannot remove last authentication method").WithCode("cannot_remove_last_method"))
	case errors.Is(err, sdk.ErrAlreadyExists):
		web.RespondJSONError(w, web.NewError(http.StatusConflict, "identifier already claimed").WithCode("identifier_exists"))
	default:
		// A wrong/expired/locked-out confirmation code surfaces a challenge-rail
		// sentinel; respondDomainError emits the named §5.8 challenge codes.
		respondDomainError(w, err)
	}
}
