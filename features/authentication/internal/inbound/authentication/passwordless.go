package authentication

import (
	"errors"
	"net/http"

	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// passwordlessStartRequest is the strict POST /auth/passwordless/start body
// (design §4.3): the identifier kind (email/phone), the identifier value, and an
// optional method (link/code) that defaults per kind in the service. decode
// rejects unknown fields.
type passwordlessStartRequest struct {
	IdentifierKind string `json:"identifier_kind"`
	Identifier     string `json:"identifier"`
	Method         string `json:"method"`
}

// passwordlessVerifyRequest is the strict POST /auth/passwordless/verify body
// (design §4.3): the identifier kind (email/phone), the identifier value, and the
// one-time code the start delivered. decode rejects unknown fields.
type passwordlessVerifyRequest struct {
	IdentifierKind string `json:"identifier_kind"`
	Identifier     string `json:"identifier"`
	Code           string `json:"code"`
}

// passwordlessRedeemRequest is the strict POST /auth/passwordless/redeem body
// (design §4.3): ONLY the opaque magic-link token. decode rejects unknown fields.
// No identifier is supplied here — the login identity is the identifier the token
// was bound to at issue — so the redeem surface carries no enumeration input.
type passwordlessRedeemRequest struct {
	Token string `json:"token"`
}

// mountPasswordless registers the passwordless login routes (design §4.3/§6.4): the
// asynchronous start (AV3-7.2), the OTP verify (AV3-7.3), and the magic-link redeem
// (AV3-7.4). All are POST-only — no GET consumes a token, so a link scanner that
// merely fetches the URL cannot authenticate a user (§6.4). They are UNAUTHENTICATED
// credential-establishment endpoints (§9.1): each carries the allowlisted-Origin
// gate (requireBrowserSafeOrigin) so a cross-site page cannot force a credential
// mint, but NOT the double-submit CSRF gate — there is no pre-existing session, so
// there is no auth_csrf cookie to compare, and a first-time browser sign-in must
// still succeed. A native/bearer client that sends no Origin passes the gate (the
// non-cookie path). The blanket client-info middleware rides them via the wrapping
// registrar for the rate-limit key.
// The start/verify/redeem routes are registered once each; the handler behind each
// is a content-type dispatcher (dispatch.go) that keeps the JSON contract and, when
// Views is wired, adds form handling — there is no duplicate POST registration. The
// origin middleware rides both transports (native JSON clients send no Origin and
// pass; a browser form submit is origin-checked).
func mountPasswordless(r feature.RouteRegistrar, h *handlers) {
	origin := requireBrowserSafeOrigin(h.mutation.csrf())
	r.Handle("POST", "/auth/passwordless/start", h.passwordlessStart, origin)
	r.Handle("POST", "/auth/passwordless/verify", h.passwordlessVerify, origin)
	r.Handle("POST", "/auth/passwordless/redeem", h.passwordlessRedeem, origin)
}

// passwordlessVerify completes the OTP passwordless-login rail (design §4.3): it
// decodes the strict body and delegates to the service, which normalizes,
// rate-limits, re-resolves the current identifier, atomically consumes the login_otp
// bound to it, and mints a session. On success it sets both session cookies and
// returns the access/refresh pair (mirroring refresh/token), so cookie and bearer
// clients alike get a live session. An exhausted verify budget is a generic 429;
// EVERY other failure — invalid/expired/unknown/disabled/context-mismatch/lockout —
// is one generic 401 (design §5.8), so the response never distinguishes the reason.
// A genuine internal failure surfaces as 500, uniform across existence.
func (h *handlers) passwordlessVerifyJSON(w http.ResponseWriter, r *http.Request) {
	// Credential-establishment response guidance (design §6.4/§9.1): the success body
	// carries the session pair, so it is never cached and leaks no referrer.
	writeNoStore(w)
	writeNoReferrer(w)
	var req passwordlessVerifyRequest
	if !strictJSONBody(w, r, &req, maxJSONBodyBytes) {
		return
	}
	pair, err := h.svc.VerifyPasswordless(r.Context(), req.IdentifierKind, req.Identifier, req.Code)
	if err != nil {
		if errors.Is(err, authsvc.ErrPasswordlessRateLimited) {
			web.RespondJSONError(w, web.NewError(http.StatusTooManyRequests, "too many requests").WithCode("rate_limited"))
			return
		}
		// ErrPasswordlessLogin wraps sdk.ErrUnauthorized → one generic 401; an internal
		// failure wraps no sdk kind → 500. Neither distinguishes account existence.
		web.RespondJSONDomainError(w, err)
		return
	}
	h.svc.SetSessionCookies(w, pair)
	web.RespondJSONOK(w, newTokenResponse(pair))
}

// passwordlessStart is the enumeration-safe asynchronous start (design §4.1/§4.3):
// it decodes the strict body and delegates to the service, which normalizes,
// rate-limits, and enqueues an opaque delivery job WITHOUT resolving the account or
// calling a provider. Every successful start — known, unknown, unverified, or a
// malformed identifier — returns the same 202 accepted body, so the response
// cannot distinguish existence. A rate-limit exhaustion is a generic 429; an
// invalid kind/method is a 400 (a deterministic request-shape outcome, not an
// account signal); an internal failure is a 500, uniform across existence.
func (h *handlers) passwordlessStartJSON(w http.ResponseWriter, r *http.Request) {
	// Credential-establishment response guidance (design §6.4/§9.1): uniform no-store /
	// no-referrer across the passwordless surface, indistinguishable across existence.
	writeNoStore(w)
	writeNoReferrer(w)
	var req passwordlessStartRequest
	if !strictJSONBody(w, r, &req, maxJSONBodyBytes) {
		return
	}
	if err := h.svc.StartPasswordless(r.Context(), req.IdentifierKind, req.Identifier, req.Method); err != nil {
		if errors.Is(err, authsvc.ErrPasswordlessRateLimited) {
			web.RespondJSONError(w, web.NewError(http.StatusTooManyRequests, "too many requests").WithCode("rate_limited"))
			return
		}
		// A bounded in-process outbox admission rejection (queue full / shutting down)
		// wraps sdk.ErrUnavailable, so the domain-error writer maps it to an honest 503
		// — never a 202 accepted after dropping the work — identical for a known and an
		// unknown identifier (admission precedes any account lookup).
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONAccepted(w, map[string]string{"status": "accepted"})
}

// passwordlessRedeem completes the magic-link passwordless-login rail (design
// §4.3/§6.4): it decodes the strict {token} body and delegates to the service,
// which throttles, atomically consumes the login_magic_link token, reloads and
// validates the bound CURRENT identifier, and mints a session. On success it sets
// both session cookies and returns the access/refresh pair, exactly like verify, so
// cookie and bearer clients alike get a live session. It is registered POST-only —
// no GET consumes a token (design §6.4), so a link scanner that merely fetches the
// URL cannot authenticate a user. An exhausted redeem budget is a generic 429;
// EVERY other failure — unknown/expired/replayed token, removed/replaced/disabled
// bound identifier — is one generic 401 (design §5.8), so the response never
// distinguishes the reason. Cache-Control: no-store and Referrer-Policy: no-referrer
// ride every response so the pair is never cached and the fragment token never leaks
// via a referrer.
func (h *handlers) passwordlessRedeemJSON(w http.ResponseWriter, r *http.Request) {
	writeNoStore(w)
	writeNoReferrer(w)
	var req passwordlessRedeemRequest
	if !strictJSONBody(w, r, &req, maxJSONBodyBytes) {
		return
	}
	pair, err := h.svc.RedeemPasswordless(r.Context(), req.Token)
	if err != nil {
		if errors.Is(err, authsvc.ErrPasswordlessRateLimited) {
			web.RespondJSONError(w, web.NewError(http.StatusTooManyRequests, "too many requests").WithCode("rate_limited"))
			return
		}
		// ErrPasswordlessLogin wraps sdk.ErrUnauthorized → one generic 401; an internal
		// failure wraps no sdk kind → 500. Neither distinguishes account existence.
		web.RespondJSONDomainError(w, err)
		return
	}
	h.svc.SetSessionCookies(w, pair)
	web.RespondJSONOK(w, newTokenResponse(pair))
}
