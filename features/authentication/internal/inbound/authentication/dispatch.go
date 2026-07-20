package authentication

import (
	"mime"
	"net/http"

	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// Dual JSON/form transport dispatch (design §9.2, task AV3-8.3). Every shared
// mutation endpoint is registered exactly once (routes.go / passwordless.go); the
// handler behind it is a content-type dispatcher that keeps the byte-stable JSON
// contract and, only when a Views port is wired, adds HTML form handling over the
// SAME service methods. There is no duplicate POST route registration (the phase-8
// stop condition) and Accept never reinterprets the request body — the request
// Content-Type alone selects the transport.

// contentKind classifies a request body's transport.
type contentKind int

const (
	// contentJSON is the API transport: application/json, or an absent Content-Type
	// (a non-form body decodes as JSON, preserving the pre-v3 JSON clients that sent
	// no Content-Type — the standing invariant that non-nil Views changes no JSON
	// contract).
	contentJSON contentKind = iota
	// contentForm is the HTML transport: urlencoded or multipart form input.
	contentForm
	// contentUnsupported is any other explicit media type → 415.
	contentUnsupported
)

// classifyContent selects the transport from the request Content-Type. An absent
// Content-Type is treated as JSON so existing API clients that posted a JSON body
// with no header keep working; an explicit non-JSON, non-form type is unsupported.
func classifyContent(r *http.Request) contentKind {
	raw := r.Header.Get("Content-Type")
	if raw == "" {
		return contentJSON
	}
	mediaType, _, err := mime.ParseMediaType(raw)
	if err != nil {
		return contentUnsupported
	}
	switch mediaType {
	case "application/json":
		return contentJSON
	case "application/x-www-form-urlencoded", "multipart/form-data":
		return contentForm
	default:
		return contentUnsupported
	}
}

// dispatch routes a shared mutation endpoint by Content-Type. A JSON body keeps the
// existing API contract; a form body renders/redirects through the HTML surface but
// only when Views is wired (a nil Views leaves the feature API-only, so a form body
// is 415, not a page). Any other content type is 415. This is the single transport
// seam — the JSON and form branches call the same service methods.
func (h *handlers) dispatch(w http.ResponseWriter, r *http.Request, jsonHandler, formHandler http.HandlerFunc) {
	switch classifyContent(r) {
	case contentJSON:
		jsonHandler(w, r)
	case contentForm:
		if h.views == nil {
			unsupportedMediaType(w)
			return
		}
		formHandler(w, r)
	default:
		unsupportedMediaType(w)
	}
}

// unsupportedMediaType writes the generic 415 for a body whose Content-Type the
// endpoint cannot decode (design §9.2). The message names no account and maps no
// gate.
func unsupportedMediaType(w http.ResponseWriter) {
	web.RespondJSONError(w, web.NewError(http.StatusUnsupportedMediaType,
		"unsupported content type").WithCode("unsupported_media_type"))
}

// ---------------------------------------------------------------------------
// Canonical POST dispatch entrypoints. routes.go / passwordless.go register these
// (one registration each); each selects the JSON or form handler by Content-Type.
// ---------------------------------------------------------------------------

func (h *handlers) register(w http.ResponseWriter, r *http.Request) {
	h.dispatch(w, r, h.registerJSON, h.registerForm)
}

func (h *handlers) login(w http.ResponseWriter, r *http.Request) {
	h.dispatch(w, r, h.loginJSON, h.loginForm)
}

func (h *handlers) verify(w http.ResponseWriter, r *http.Request) {
	h.dispatch(w, r, h.verifyJSON, h.verifyForm)
}

func (h *handlers) logout(w http.ResponseWriter, r *http.Request) {
	h.dispatch(w, r, h.logoutJSON, h.logoutForm)
}

func (h *handlers) forgotPassword(w http.ResponseWriter, r *http.Request) {
	h.dispatch(w, r, h.forgotPasswordJSON, h.forgotPasswordForm)
}

func (h *handlers) resetPassword(w http.ResponseWriter, r *http.Request) {
	h.dispatch(w, r, h.resetPasswordJSON, h.resetPasswordForm)
}

func (h *handlers) passwordlessStart(w http.ResponseWriter, r *http.Request) {
	h.dispatch(w, r, h.passwordlessStartJSON, h.passwordlessStartForm)
}

func (h *handlers) passwordlessVerify(w http.ResponseWriter, r *http.Request) {
	h.dispatch(w, r, h.passwordlessVerifyJSON, h.passwordlessVerifyForm)
}

func (h *handlers) passwordlessRedeem(w http.ResponseWriter, r *http.Request) {
	h.dispatch(w, r, h.passwordlessRedeemJSON, h.passwordlessRedeemForm)
}
