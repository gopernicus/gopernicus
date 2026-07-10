// Package sdk is the framework kernel: the root of the gopernicus sdk module.
//
// The kernel holds the cross-cutting vocabulary every tier may depend on — the
// transport-agnostic sentinel errors below, and the request-identity context
// vocabulary in context.go (the second promotion, 2026-07-10). Its contract is
// deliberately narrow:
//
//   - It imports the standard library only. The module's go.mod has no require
//     block, so "stdlib only" is a structural fact, not a convention.
//   - It is a leaf within the sdk module. The Go compiler enforces this against
//     every subpackage that imports the kernel (crud, cryptids, email, web, …):
//     the root package cannot import one of its own subpackages without forming
//     an import cycle. Subpackages that do not import the kernel are not caught
//     by the cycle, so guard G12(a) (landing at P5) is the primary enforcement
//     of the no-subpackage-imports rule; the cycle is the belt to its
//     suspenders.
//   - Promotion to the kernel is a deliberate, visible act: adding a file to the
//     root package. Nothing is admitted implicitly.
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
// and typically warrant logging at the delivery layer.
func IsExpected(err error) bool {
	for _, sentinel := range expectedErrors {
		if errors.Is(err, sentinel) {
			return true
		}
	}
	return false
}
