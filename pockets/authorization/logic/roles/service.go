// Package roles owns role assignments, store contracts and role reads.
// Service applies exact/global scope rules and validates list requests.
// Trusted raw changes require a separately constructed Writer; actor-facing
// changes belong to mutations.Service and its host-supplied guard.
package roles

import (
	"context"
	"fmt"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
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
	store Storer
}

// NewService builds the roles service over its store.
func NewService(store Storer) (*Service, error) {
	if isNil(store) {
		return nil, fmt.Errorf("authorization: roles store is required: %w", sdk.ErrInvalidInput)
	}
	return &Service{store: store}, nil
}

// HasRole reports whether a subject holds a role at the given scope, applying
// Q5's scope rule: the exact scope is checked first, then — for a SCOPED query —
// the global ("", "") grant is checked as a fallback. A global assignment thus
// satisfies a scoped check, but a scoped assignment never satisfies a different
// scope. Fail-closed: any store error returns (false, err).
//
// It delegates to [Service.HasRoleWhere] and drops the provenance, so the scope
// rule lives in exactly one place.
func (s *Service) HasRole(ctx context.Context, principal authmodel.PrincipalRef, roleName, resourceType, resourceID string) (bool, error) {
	if s == nil {
		return false, ErrRolesNotConfigured
	}
	held, _, err := s.HasRoleWhere(ctx, principal.Type, principal.ID, roleName, resourceType, resourceID)
	return held, err
}

// HasRoleWhere is HasRole with PROVENANCE: the same Q5 scope rule (exact scope,
// then the global fallback for a scoped query), additionally reporting WHERE the
// grant was found — role.ProvenanceDirect when the exact-scope row matched,
// role.ProvenanceGlobal when the ("", "") fallback did, "" when not held. A role
// held at BOTH scopes reports direct (the more specific), so callers are
// deterministic regardless of row order. An UNSCOPED query's exact row IS the
// global assignment, so it reports direct — matching the effective listing,
// which has no fallback at global scope.
//
// Fail-closed: any store error returns (false, "", err).
func (s *Service) HasRoleWhere(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) (held bool, provenance string, err error) {
	if s == nil {
		return false, "", ErrRolesNotConfigured
	}
	if err := validateAssignment(subjectType, subjectID, roleName, resourceType, resourceID); err != nil {
		return false, "", err
	}

	return ResolveScope(ctx, resourceType, resourceID, func(ctx context.Context, scopeType, scopeID string) (bool, error) {
		return s.store.HasExactRole(ctx, subjectType, subjectID, roleName, scopeType, scopeID)
	})
}

// HasExactRole exposes the validated exact-read primitive to the composite's
// call-local batch memo. Scope fallback remains role.ResolveScope's policy.
func (s *Service) HasExactRole(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) (bool, error) {
	if s == nil {
		return false, ErrRolesNotConfigured
	}
	if err := validateAssignment(subjectType, subjectID, roleName, resourceType, resourceID); err != nil {
		return false, err
	}
	return s.store.HasExactRole(ctx, subjectType, subjectID, roleName, resourceType, resourceID)
}

// LookupResourceIDsBySubjectAndRoles is the roles kind's resource-id lookup for
// a PAGED enumeration (plan A3b): unrestricted=true (with nil ids) when the
// subject holds any of roles GLOBALLY, else the distinct scoped resource ids of
// resourceType at which the subject holds any of them — sorted ascending in byte
// order, strictly after `after`, at most limit.
//
// It is a passthrough: the caller (the decision surface) passes the compiled
// granting roles, and this service applies no model knowledge — the roles kind
// never sees the decision engine. The three reference fields are validated as
// the other methods validate them; a lookup is always type-scoped, so an empty
// resourceType is rejected rather than read as a global query.
func (s *Service) LookupResourceIDsBySubjectAndRoles(ctx context.Context, subjectType, subjectID, resourceType string, roles []string, after string, limit int) ([]string, bool, error) {
	if s == nil {
		return nil, false, ErrRolesNotConfigured
	}
	if err := validateSubjectFields(subjectType, subjectID); err != nil {
		return nil, false, err
	}
	if resourceType == "" {
		return nil, false, ErrInvalidRoleAssignment
	}
	return s.store.LookupResourceIDsBySubjectAndRoles(ctx, subjectType, subjectID, resourceType, roles, after, limit)
}

// ListRoleAssignmentsBySubject pages a subject's assignments. The subject fields
// are validated symmetrically with the mutation/decision methods (a non-empty
// subject type and ID).
func (s *Service) ListRoleAssignmentsBySubject(ctx context.Context, principal authmodel.PrincipalRef, req list.Request) (list.Page[Assignment], error) {
	if s == nil {
		return list.Page[Assignment]{}, ErrRolesNotConfigured
	}
	subjectType, subjectID := principal.Type, principal.ID
	if err := validateSubjectFields(subjectType, subjectID); err != nil {
		return list.Page[Assignment]{}, err
	}
	return s.store.ListBySubject(ctx, subjectType, subjectID, req)
}

// ListRoleAssignmentsByResource is the RAW direct-scope listing: it pages the
// assignments stored exactly at (resourceType, resourceID). It never surfaces
// globally-granted subjects — see ListEffectiveRoleGrantsByResource for the
// enumeration that agrees with HasRole. The scope shape is validated
// symmetrically (global-or-fully-scoped; a half-scoped pair is rejected).
func (s *Service) ListRoleAssignmentsByResource(ctx context.Context, resourceType, resourceID string, req list.Request) (list.Page[Assignment], error) {
	if s == nil {
		return list.Page[Assignment]{}, ErrRolesNotConfigured
	}
	if err := validateResourceScope(resourceType, resourceID); err != nil {
		return list.Page[Assignment]{}, err
	}
	return s.store.ListByResource(ctx, resourceType, resourceID, req)
}

// ListEffectiveRoleGrantsByResource pages the EFFECTIVE role grants on a
// resource: the union of the direct scoped assignments with the global
// assignments that HasRole's scoped fallback satisfies, de-duplicated by
// (subject, role) with explicit provenance. This is the enumeration side of Q5:
// its grant set agrees with HasRole, without rewriting a global assignment as a
// scoped row. The scope shape is validated symmetrically with the other methods.
func (s *Service) ListEffectiveRoleGrantsByResource(ctx context.Context, resourceType, resourceID string, req list.Request) (list.Page[EffectiveGrant], error) {
	if s == nil {
		return list.Page[EffectiveGrant]{}, ErrRolesNotConfigured
	}
	if err := validateResourceScope(resourceType, resourceID); err != nil {
		return list.Page[EffectiveGrant]{}, err
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
	if err := validateResourceScope(resourceType, resourceID); err != nil {
		return err
	}
	return (Assignment{
		SubjectType: subjectType, SubjectID: subjectID, Role: roleName,
		ResourceType: resourceType, ResourceID: resourceID,
	}).Validate()
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
