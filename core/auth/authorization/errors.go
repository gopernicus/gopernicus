package authorization

import (
	"fmt"

	"github.com/gopernicus/gopernicus/sdk/errs"
)

var (
	// ErrPermissionDenied indicates the subject lacks permission for the operation.
	ErrPermissionDenied = fmt.Errorf("authz: %w", errs.ErrForbidden)

	// ErrCannotRemoveLastOwner indicates the operation would orphan a resource
	// by removing its only owner.
	ErrCannotRemoveLastOwner = fmt.Errorf("authz last owner: %w", errs.ErrConflict)

	// ErrCannotChangeLastOwner indicates the operation would orphan a resource
	// by changing its only owner to a non-owner role.
	ErrCannotChangeLastOwner = fmt.Errorf("authz last owner role: %w", errs.ErrConflict)

	// ErrCannotChangeOwnRole indicates a subject attempted to change their own role.
	ErrCannotChangeOwnRole = fmt.Errorf("authz own role: %w", errs.ErrConflict)

	// ErrInvalidRelation indicates a relationship is not allowed by the schema.
	ErrInvalidRelation = fmt.Errorf("authz relation: %w", errs.ErrInvalidInput)

	// ErrInvalidSchema indicates the schema has structural errors
	// (undefined references, circular through-relations, etc.).
	ErrInvalidSchema = fmt.Errorf("authz schema: %w", errs.ErrInvalidInput)
)
