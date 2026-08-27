package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gopernicus/gopernicus/sdk"
)

// Error represents an HTTP error with status code, message, and optional code.
// When Fields is populated, the error carries per-field validation detail
// (forms reuse this to re-render with inline field errors).
type Error struct {
	Status  int          `json:"-"`
	Message string       `json:"message"`
	Code    string       `json:"code,omitempty"`
	Fields  []FieldError `json:"fields,omitempty"`
}

func (e *Error) Error() string { return e.Message }

// WithCode returns the error with a custom code, overriding the default.
func (e *Error) WithCode(code string) *Error {
	e.Code = code
	return e
}

// NewError creates an error with the given status and message.
func NewError(status int, message string) *Error {
	return &Error{Status: status, Message: message}
}

// ---------------------------------------------------------------------------
// Field-level validation errors
// ---------------------------------------------------------------------------

// FieldError represents a validation error for a single field.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// FieldErrors collects validation errors for individual fields.
// Use Add to accumulate errors, then Err to get a result suitable for
// returning from a Validate method.
//
// AddErr composes with the field validators in sdk/foundation/validation — e.g.
// fe.AddErr("name", validation.MinLength("name", req.Name, 3)) — folding a
// validator's error into per-field detail. There is no import edge in either
// direction; the two packages meet only through the plain error value.
type FieldErrors []FieldError

// Add appends a field error.
func (fe *FieldErrors) Add(field, message string) {
	*fe = append(*fe, FieldError{Field: field, Message: message})
}

// AddErr appends a field error from a validation function. Nil errors are skipped.
func (fe *FieldErrors) AddErr(field string, err error) {
	if err != nil {
		*fe = append(*fe, FieldError{Field: field, Message: err.Error()})
	}
}

// Err returns nil if no errors were collected, or the FieldErrors as an error.
func (fe FieldErrors) Err() error {
	if len(fe) == 0 {
		return nil
	}
	return fe
}

// Error implements the error interface.
func (fe FieldErrors) Error() string {
	if len(fe) == 0 {
		return "validation failed"
	}
	s := fe[0].Field + ": " + fe[0].Message
	if n := len(fe) - 1; n > 0 {
		return s + " (and " + strconv.Itoa(n) + " more)"
	}
	return s
}

// ---------------------------------------------------------------------------
// Status-mapped error constructors
// ---------------------------------------------------------------------------

// ErrValidation returns a 400 error from a [DecodeJSON] error. If the error
// contains [FieldErrors] (from a Validate method), the response includes
// per-field detail. Otherwise the error message is used directly.
//
//	req, err := web.DecodeJSON[MyRequest](r)
//	if err != nil {
//	    web.RespondJSONError(w, web.ErrValidation(err))
//	    return
//	}
func ErrValidation(err error) *Error {
	// A body that overran MaxBytesReader fails the decode read with
	// *http.MaxBytesError — surface the documented 413 rather than a generic
	// 400, so the body-limit contract actually holds.
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return ErrPayloadTooLarge("request body exceeds the maximum allowed size")
	}
	var fe FieldErrors
	if errors.As(err, &fe) {
		return &Error{
			Status:  http.StatusBadRequest,
			Message: "validation failed",
			Code:    "validation_failed",
			Fields:  []FieldError(fe),
		}
	}
	return ErrBadRequest(err.Error())
}

// ErrBadRequest returns a 400 error.
func ErrBadRequest(msg string) *Error {
	return &Error{Status: http.StatusBadRequest, Message: msg, Code: "bad_request"}
}

// ErrPayloadTooLarge returns a 413 error.
func ErrPayloadTooLarge(msg string) *Error {
	return &Error{Status: http.StatusRequestEntityTooLarge, Message: msg, Code: "payload_too_large"}
}

// ErrUnauthorized returns a 401 error.
func ErrUnauthorized(msg string) *Error {
	return &Error{Status: http.StatusUnauthorized, Message: msg, Code: "unauthenticated"}
}

// ErrForbidden returns a 403 error.
func ErrForbidden(msg string) *Error {
	return &Error{Status: http.StatusForbidden, Message: msg, Code: "permission_denied"}
}

// ErrNotFound returns a 404 error.
func ErrNotFound(msg string) *Error {
	return &Error{Status: http.StatusNotFound, Message: msg, Code: "not_found"}
}

// ErrConflict returns a 409 error for a duplicate resource (code
// "already_exists"), pairing with [sdk.ErrAlreadyExists]. For a state
// conflict — an invariant refusing a write, nothing duplicated — use
// [ErrStateConflict].
func ErrConflict(msg string) *Error {
	return &Error{Status: http.StatusConflict, Message: msg, Code: "already_exists"}
}

// ErrStateConflict returns a 409 error for a state conflict (code "conflict"),
// pairing with [sdk.ErrConflict]. For a duplicate resource, use [ErrConflict].
func ErrStateConflict(msg string) *Error {
	return &Error{Status: http.StatusConflict, Message: msg, Code: "conflict"}
}

// ErrGone returns a 410 error.
func ErrGone(msg string) *Error {
	return &Error{Status: http.StatusGone, Message: msg, Code: "expired"}
}

// ErrTooManyRequests returns a 429 error.
func ErrTooManyRequests(msg string) *Error {
	return &Error{Status: http.StatusTooManyRequests, Message: msg, Code: "rate_limit_exceeded"}
}

// ErrUnavailable returns a 503 error.
func ErrUnavailable(msg string) *Error {
	return &Error{Status: http.StatusServiceUnavailable, Message: msg, Code: "unavailable"}
}

// ErrInternal returns a 500 error.
func ErrInternal(msg string) *Error {
	return &Error{Status: http.StatusInternalServerError, Message: msg, Code: "internal"}
}

// ---------------------------------------------------------------------------
// Host-facing safe errors
// ---------------------------------------------------------------------------

// SafeDomainError pairs a deliberately public [*Error] with the domain error
// that caused it, so [ErrFromDomain] emits the public body instead of its
// generic per-kind message.
//
// This is an explicit host-seam affordance: a host authorization or grant seam
// (an invitation InviteCheck or Granter refusal, say) refuses through a vendored
// pocket's handler, and that handler responds through [ErrFromDomain]. Wrapping
// is the only way such a refusal can carry a legible sentence to the wire.
//
// It is not a general permission for domain code to put user text on the wire.
// Pocket-internal errors must not use this wrapper — a pocket cannot know
// whether its own sentences are safe in a host's product. Bare sentinels and
// arbitrary errors that merely wrap an [*Error] keep the generic mapping;
// nothing but this wrapper is recognized.
//
// Construction contract: the wrapper's Unwrap returns the cause alone, so
// errors.Is and errors.As continue to match the domain error — including the
// kind switch in [ErrFromDomain] if the public error is ever absent. The public
// [*Error] is deliberately outside the unwrap chain so no other errors.As call
// can mistake it for the request's own error.
//
//	var errAlreadyAttached = web.NewSafeDomainError(
//	    web.ErrStateConflict("already attached to another account"),
//	    sdk.ErrConflict,
//	)
//
//	// The seam refuses; the pocket's handler responds through ErrFromDomain,
//	// and the invitee reads the host's sentence instead of "conflict".
//	// errors.Is(errAlreadyAttached, sdk.ErrConflict) stays true.
type SafeDomainError struct {
	public *Error
	cause  error
}

// NewSafeDomainError wraps cause with a public HTTP error body. Both arguments
// are required: public is the exact body [ErrFromDomain] returns, and cause is
// the domain error callers keep matching with errors.Is.
func NewSafeDomainError(public *Error, cause error) *SafeDomainError {
	return &SafeDomainError{public: public, cause: cause}
}

// HTTPError returns the public error body supplied at construction.
func (e *SafeDomainError) HTTPError() *Error { return e.public }

// Unwrap returns the domain cause, keeping errors.Is and errors.As matches
// against it intact.
func (e *SafeDomainError) Unwrap() error { return e.cause }

func (e *SafeDomainError) Error() string {
	switch {
	case e.public != nil && e.cause != nil:
		return e.public.Message + ": " + e.cause.Error()
	case e.public != nil:
		return e.public.Message
	case e.cause != nil:
		return e.cause.Error()
	default:
		return "safe domain error"
	}
}

// ErrFromDomain maps a domain error (wrapping sdk/errs sentinels) to a
// [*Error] with the appropriate HTTP status code and a generic, safe message.
//
// This is a catch-all for errors the delivery layer doesn't handle explicitly.
// For user-facing messages, handle specific errors before calling this.
//
// The one exception to the generic mapping is [SafeDomainError], the explicit
// host-seam wrapper: its public body is returned as-is. A wrapper carrying no
// public body falls through to the kind switch below.
func ErrFromDomain(err error) *Error {
	var safeErr *SafeDomainError
	if errors.As(err, &safeErr) && safeErr.HTTPError() != nil {
		return safeErr.HTTPError()
	}

	switch {
	case errors.Is(err, sdk.ErrNotFound):
		return ErrNotFound("not found")
	case errors.Is(err, sdk.ErrAlreadyExists):
		return ErrConflict("already exists")
	case errors.Is(err, sdk.ErrUnauthorized):
		return ErrUnauthorized("unauthorized")
	case errors.Is(err, sdk.ErrForbidden):
		return ErrForbidden("forbidden")
	case errors.Is(err, sdk.ErrInvalidInput):
		return ErrBadRequest("invalid input")
	case errors.Is(err, sdk.ErrInvalidReference):
		return ErrBadRequest("invalid reference")
	case errors.Is(err, sdk.ErrConflict):
		return ErrStateConflict("conflict")
	case errors.Is(err, sdk.ErrExpired):
		return ErrGone("expired")
	case errors.Is(err, sdk.ErrUnavailable):
		return ErrUnavailable("unavailable")
	default:
		return ErrInternal("internal error")
	}
}
