// Package sdk provides the framework's common vocabulary and small primitives:
// errors and validation faults, request/trace/span and principal context values,
// identity projections, configurable ID generation, pointer reads and URL slugs.
//
// Root SDK imports only the standard library, never an SDK subpackage. Every
// framework tier may use it. Subpackages provide coherent mechanisms or
// capabilities, with integrations supplying external-library implementations.
// Hosts own configuration, identity records and application policy; these helpers
// do not start services or install global configuration.
//
// The sentinels are transport-agnostic and designed to be wrapped with domain
// context, then checked at boundaries using errors.Is().
//
// Example — defining a domain error:
//
//	var ErrArticleNotFound = fmt.Errorf("article: %w", sdk.ErrNotFound)
//
// Example — checking at the delivery layer:
//
//	if errors.Is(err, sdk.ErrNotFound) {
//	    return web.ErrNotFound("article not found")
//	}
package sdk

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

	// ErrUnavailable indicates the operation cannot be accepted right now:
	// transient saturation/backpressure, a shutting-down runtime, or a degraded
	// dependency. The caller should retry later WITHOUT changing the request. This
	// is distinct from ErrConflict, which is state contention (an optimistic-lock
	// failure or invalid transition) where a retry typically needs a DIFFERENT
	// request. Transports map this to 503 Service Unavailable.
	ErrUnavailable = errors.New("unavailable")

	// expectedErrors lists the domain sentinels recognized by IsExpected.
	expectedErrors = []error{
		ErrNotFound, ErrAlreadyExists, ErrInvalidReference,
		ErrInvalidInput, ErrUnauthorized, ErrForbidden,
		ErrConflict, ErrExpired, ErrUnavailable,
	}
)

// IsExpected reports whether err wraps a known domain sentinel.
// This is domain classification, not an HTTP status prediction: transports
// also recognize typed validation errors and explicit public-error adapters.
func IsExpected(err error) bool {
	for _, sentinel := range expectedErrors {
		if errors.Is(err, sentinel) {
			return true
		}
	}
	return false
}
