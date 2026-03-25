package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gopernicus/gopernicus/sdk/errs"
)

// Error represents an HTTP error with status code, message, and optional code.
// When Fields is populated, the response includes per-field validation detail.
type Error struct {
	Status  int          `json:"-"`
	Message string       `json:"message"`
	Code    string       `json:"code,omitempty"`
	Fields  []FieldError `json:"fields,omitempty"`
}

func (e *Error) Error() string { return e.Message }

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
// returning from Validate().
//
//	var errs web.FieldErrors
//	if r.Email == "" {
//	    errs.Add("email", "is required")
//	}
//	if r.Password == "" {
//	    errs.Add("password", "is required")
//	}
//	return errs.Err()
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

// ErrValidation returns a 400 error from a DecodeJSON error. If the error
// contains [FieldErrors] (from a Validate method), the response includes
// per-field detail. Otherwise the error message is used directly (e.g.
// "email is required; password must be at least 8 characters").
//
//	req, err := web.DecodeJSON[MyRequest](r)
//	if err != nil {
//	    web.RespondJSONError(w, web.ErrValidation(err))
//	    return
//	}
func ErrValidation(err error) *Error {
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
// This is a catch-all for errors the bridge doesn't handle explicitly.
// For user-facing messages, handle specific errors before calling this:
//
//	if errors.Is(err, authentication.ErrEmailNotVerified) {
//	    web.RespondJSONError(w, web.ErrForbidden("email not verified"))
//	    return
//	}
//	web.RespondJSONDomainError(w, err) // generic fallback
func ErrFromDomain(err error) *Error {
	switch {
	case errors.Is(err, errs.ErrNotFound):
		return ErrNotFound("not found")
	case errors.Is(err, errs.ErrAlreadyExists):
		return ErrConflict("already exists")
	case errors.Is(err, errs.ErrUnauthorized):
		return ErrUnauthorized("unauthorized")
	case errors.Is(err, errs.ErrForbidden):
		return ErrForbidden("forbidden")
	case errors.Is(err, errs.ErrInvalidInput):
		return ErrBadRequest("invalid input")
	case errors.Is(err, errs.ErrInvalidReference):
		return ErrBadRequest("invalid reference")
	case errors.Is(err, errs.ErrConflict):
		return &Error{Status: http.StatusConflict, Message: "conflict", Code: "conflict"}
	case errors.Is(err, errs.ErrExpired):
		return ErrGone("expired")
	default:
		return ErrInternal("internal error")
	}
}
