package httpmid

import (
	"net/http"

	"github.com/gopernicus/gopernicus/sdk/web"
)

// ErrorKind describes the category of middleware failure for error rendering.
type ErrorKind int

const (
	// ErrKindUnauthenticated indicates no authenticated subject was found.
	ErrKindUnauthenticated ErrorKind = iota

	// ErrKindBadRequest indicates a malformed request (e.g., missing resource ID).
	ErrKindBadRequest

	// ErrKindInternal indicates an internal system error.
	ErrKindInternal

	// ErrKindForbidden indicates the subject lacks permission.
	ErrKindForbidden
)

// ErrorRenderer renders error responses for middleware. Every middleware that
// writes error responses accepts an ErrorRenderer, making the response format
// explicit at the route level.
//
// The framework provides [JSONErrors] for API routes. Apps implement their
// own renderer for HTML routes using templates, layouts, and branding.
//
// Example HTML implementation:
//
//	type HTMLErrors struct{}
//
//	func (HTMLErrors) RenderError(w http.ResponseWriter, r *http.Request, kind httpmid.ErrorKind) {
//	    switch kind {
//	    case httpmid.ErrKindUnauthenticated:
//	        http.Redirect(w, r, "/login?return_to="+url.QueryEscape(r.URL.String()), http.StatusSeeOther)
//	    case httpmid.ErrKindForbidden:
//	        w.WriteHeader(http.StatusForbidden)
//	        templates.Forbidden().Render(r.Context(), w)
//	    default:
//	        w.WriteHeader(http.StatusInternalServerError)
//	        templates.ServerError().Render(r.Context(), w)
//	    }
//	}
type ErrorRenderer interface {
	RenderError(w http.ResponseWriter, r *http.Request, kind ErrorKind)
}

// JSONErrors is the built-in [ErrorRenderer] for JSON API routes.
//
//	jsonErrs := httpmid.JSONErrors{}
//	mux.Handle("GET /api/posts/{id}", httpmid.AuthorizeParam(authorizer, log, jsonErrs, "post", "read", "id")(handler))
type JSONErrors struct{}

// RenderError writes a JSON error response for the given error kind.
func (JSONErrors) RenderError(w http.ResponseWriter, _ *http.Request, kind ErrorKind) {
	switch kind {
	case ErrKindUnauthenticated:
		web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
	case ErrKindBadRequest:
		web.RespondJSONError(w, web.ErrBadRequest("bad request"))
	case ErrKindInternal:
		web.RespondJSONError(w, web.ErrInternal("internal error"))
	case ErrKindForbidden:
		web.RespondJSONError(w, web.ErrForbidden("permission denied"))
	}
}
