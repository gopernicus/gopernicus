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
// AddErr composes with the field validators in sdk/validation — e.g.
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

// ErrConflict returns a 409 error.
func ErrConflict(msg string) *Error {
	return &Error{Status: http.StatusConflict, Message: msg, Code: "already_exists"}
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

// ErrFromDomain maps a domain error (wrapping sdk/errs sentinels) to a
// [*Error] with the appropriate HTTP status code and a generic, safe message.
//
// This is a catch-all for errors the delivery layer doesn't handle explicitly.
// For user-facing messages, handle specific errors before calling this.
func ErrFromDomain(err error) *Error {
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
		return &Error{Status: http.StatusConflict, Message: "conflict", Code: "conflict"}
	case errors.Is(err, sdk.ErrExpired):
		return ErrGone("expired")
	default:
		return ErrInternal("internal error")
	}
}
