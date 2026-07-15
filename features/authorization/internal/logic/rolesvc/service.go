// Package rolesvc is the sealed service of the authorization feature's ROLES
// kind — a deliberately thin layer over role.Storer. It takes plain
// (subjectType, subjectID) pair arguments throughout and NEVER imports the
// relationship engine (authorizersvc): the root socket alone adapts an engine
// Subject into the pair. The one piece of real logic is HasRole's scope rule.
package rolesvc

import (
	"context"
	"fmt"

	"github.com/gopernicus/gopernicus/features/authorization/domain/role"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

var (
	// ErrInvalidRoleAssignment is returned when subject type, subject ID, or role
	// is empty.
	ErrInvalidRoleAssignment = fmt.Errorf("authorization role assignment: %w", sdk.ErrInvalidInput)

	// ErrHalfScopedAssignment is returned when exactly one of the resource-scope
	// fields is set: a scoped assignment requires BOTH resource fields or NEITHER
	// (the empty pair is a global grant).
	ErrHalfScopedAssignment = fmt.Errorf("authorization role scope: %w", sdk.ErrInvalidInput)
)

// Service is the roles kind's capability over role.Storer.
type Service struct {
	store role.Storer
}

// NewService builds the roles service over its store.
func NewService(store role.Storer) *Service {
	return &Service{store: store}
}

// AssignRole grants a subject a role, optionally scoped to a resource. It is
// idempotent (a duplicate is a no-op nil). An empty subject/role or a
// half-scoped resource pair is rejected loudly.
func (s *Service) AssignRole(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) error {
	if err := validateAssignment(subjectType, subjectID, roleName, resourceType, resourceID); err != nil {
		return err
	}
	return s.store.Assign(ctx, role.Assignment{
		SubjectType:  subjectType,
		SubjectID:    subjectID,
		Role:         roleName,
		ResourceType: resourceType,
		ResourceID:   resourceID,
	})
}

// UnassignRole removes an exact assignment. It is idempotent (removing an absent
// assignment is nil). Validation matches AssignRole.
func (s *Service) UnassignRole(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) error {
	if err := validateAssignment(subjectType, subjectID, roleName, resourceType, resourceID); err != nil {
		return err
	}
	return s.store.Unassign(ctx, subjectType, subjectID, roleName, resourceType, resourceID)
}

// HasRole reports whether a subject holds a role at the given scope, applying
// Q5's scope rule: the exact scope is checked first, then — for a SCOPED query —
// the global ("", "") grant is checked as a fallback. A global assignment thus
// satisfies a scoped check, but a scoped assignment never satisfies a different
// scope. Fail-closed: any store error returns (false, err).
func (s *Service) HasRole(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) (bool, error) {
	if err := validateAssignment(subjectType, subjectID, roleName, resourceType, resourceID); err != nil {
		return false, err
	}

	ok, err := s.store.HasExactRole(ctx, subjectType, subjectID, roleName, resourceType, resourceID)
	if err != nil {
		return false, err
	}
	if ok {
		return true, nil
	}

	// Global fallback only when the query was scoped (an unscoped query already
	// checked the global grant above).
	if resourceType != "" || resourceID != "" {
		ok, err := s.store.HasExactRole(ctx, subjectType, subjectID, roleName, "", "")
		if err != nil {
			return false, err
		}
		return ok, nil
	}

	return false, nil
}

// ListRoleAssignmentsBySubject pages a subject's assignments. The subject fields
// are validated symmetrically with the mutation/decision methods (a non-empty
// subject type and ID).
func (s *Service) ListRoleAssignmentsBySubject(ctx context.Context, subjectType, subjectID string, req crud.ListRequest) (crud.Page[role.Assignment], error) {
	if err := validateSubjectFields(subjectType, subjectID); err != nil {
		return crud.Page[role.Assignment]{}, err
	}
	return s.store.ListBySubject(ctx, subjectType, subjectID, req)
}

// ListRoleAssignmentsByResource is the RAW direct-scope listing: it pages the
// assignments stored exactly at (resourceType, resourceID). It never surfaces
// globally-granted subjects — see ListEffectiveRoleGrantsByResource for the
// enumeration that agrees with HasRole. The scope shape is validated
// symmetrically (global-or-fully-scoped; a half-scoped pair is rejected).
func (s *Service) ListRoleAssignmentsByResource(ctx context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[role.Assignment], error) {
	if err := validateResourceScope(resourceType, resourceID); err != nil {
		return crud.Page[role.Assignment]{}, err
	}
	return s.store.ListByResource(ctx, resourceType, resourceID, req)
}

// ListEffectiveRoleGrantsByResource pages the EFFECTIVE role grants on a
// resource: the union of the direct scoped assignments with the global
// assignments that HasRole's scoped fallback satisfies, de-duplicated by
// (subject, role) with explicit provenance. This is the enumeration side of Q5:
// its grant set agrees with HasRole, without rewriting a global assignment as a
// scoped row. The scope shape is validated symmetrically with the other methods.
func (s *Service) ListEffectiveRoleGrantsByResource(ctx context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[role.EffectiveGrant], error) {
	if err := validateResourceScope(resourceType, resourceID); err != nil {
		return crud.Page[role.EffectiveGrant]{}, err
	}
	return s.store.ListEffectiveByResource(ctx, resourceType, resourceID, req)
}

// validateAssignment is the full mutation/decision validation (subject, role,
// and scope), shared by AssignRole, UnassignRole, and HasRole.
func validateAssignment(subjectType, subjectID, roleName, resourceType, resourceID string) error {
	if err := validateSubjectFields(subjectType, subjectID); err != nil {
		return err
	}
	if roleName == "" {
		return ErrInvalidRoleAssignment
	}
	return validateResourceScope(resourceType, resourceID)
}

// validateSubjectFields rejects an empty subject type or ID. Names and IDs stay
// opaque exact strings — only emptiness is a structural error here.
func validateSubjectFields(subjectType, subjectID string) error {
	if subjectType == "" || subjectID == "" {
		return ErrInvalidRoleAssignment
	}
	return nil
}

// validateResourceScope enforces the global-or-fully-scoped shape: both resource
// fields set (a scoped assignment) or both empty (a global assignment). A
// half-scoped pair is a caller error.
func validateResourceScope(resourceType, resourceID string) error {
	if (resourceType == "") != (resourceID == "") {
		return ErrHalfScopedAssignment
	}
	return nil
}
