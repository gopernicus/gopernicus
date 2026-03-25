// Package errs provides common sentinel errors for the domain and data layers.
//
// These errors are transport-agnostic and designed to be wrapped with domain
// context, then checked at boundaries using errors.Is().
//
// Example — defining a domain error:
//
//	var ErrUserNotFound = fmt.Errorf("user: %w", errs.ErrNotFound)
//
// Example — checking at the handler/bridge layer:
//
//	if errors.Is(err, errs.ErrNotFound) {
//	    return web.ErrNotFound("user not found")
//	}
package errs

import "errors"

var (
	// ErrNotFound indicates the requested entity does not exist.
	ErrNotFound = errors.New("not found")

	// ErrAlreadyExists indicates an entity with the same unique key already exists.
	ErrAlreadyExists = errors.New("already exists")

	// ErrInvalidReference indicates a foreign key reference is invalid.
	ErrInvalidReference = errors.New("invalid reference")

	// ErrInvalidInput indicates input violates a constraint (CHECK or NOT NULL).
	ErrInvalidInput = errors.New("invalid input")

	// ErrUnauthorized indicates the caller is not authenticated.
	ErrUnauthorized = errors.New("unauthorized")

	// ErrForbidden indicates the caller lacks permission for the operation.
	ErrForbidden = errors.New("forbidden")

	// ErrConflict indicates a state conflict, such as an optimistic locking failure
	// or invalid state transition. Distinct from ErrAlreadyExists which is about
	// uniqueness constraints.
	ErrConflict = errors.New("conflict")

	// ErrExpired indicates a time-bound resource has expired (tokens, invites, etc.).
	ErrExpired = errors.New("expired")

	// expectedErrors lists all known domain sentinels. Used by IsExpected to
	// distinguish errors that map to a specific HTTP status from unexpected
	// errors that fall through to 500.
	expectedErrors = []error{
		ErrNotFound, ErrAlreadyExists, ErrInvalidReference,
		ErrInvalidInput, ErrUnauthorized, ErrForbidden,
		ErrConflict, ErrExpired,
	}
)

// IsExpected reports whether err wraps a known domain sentinel.
// Errors that return false will map to HTTP 500 in ErrFromDomain
// and typically warrant logging at the bridge layer.
func IsExpected(err error) bool {
	for _, sentinel := range expectedErrors {
		if errors.Is(err, sentinel) {
			return true
		}
	}
	return false
}
