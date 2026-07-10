// Package authorization is the public surface of the authorization feature
// module: an IAM domain with two INDEPENDENTLY-WIREABLE KINDS.
//
//   - the RELATIONSHIP kind — the ReBAC engine (schema-driven permission checks,
//     group expansion, through-traversal, platform-admin data-tuple bypass,
//     relationship CRUD) over the `iam_relationships` table.
//   - the ROLES kind — minimal opaque-string role assignments (assign/unassign,
//     scoped-or-global HasRole) over the `iam_roles` table.
//
// ReBAC is ONE kind, not the feature's identity. A host wires either kind, both,
// or neither of a given kind's methods matter to it: a nil Repositories field
// turns that kind OFF structurally (deny-by-absence), and calling an unwired
// kind's methods returns a loud per-kind sentinel — never a silent allow.
//
// # Postures
//
// Authorization is "supported, never required": a host may run with no checks
// (posture 1), enforce at its own call sites by composing this feature's kinds
// in its own closure (posture 2 — there is deliberately NO composed Check facade
// here), or adopt a fuller policy surface later (posture 3, the deferred policy
// seam). Consumer seams are Check-ONLY; everything on Service beyond the boolean
// checks is flagship-specific API, never a cross-feature seam (the AV2 split).
//
// The feature is datastore-free and view-free (FS1): it depends on its
// relationship.Storer / role.Storer ports and sdk facilities only. Register
// mounts NO routes — the /authorization/* namespace is reserved for a future
// admin surface.
package authorization

import (
	"context"
	"errors"

	"github.com/gopernicus/gopernicus/features/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/features/authorization/domain/role"
	"github.com/gopernicus/gopernicus/features/authorization/internal/logic/authorizersvc"
	"github.com/gopernicus/gopernicus/features/authorization/internal/logic/rolesvc"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
)

// Construction and per-kind sentinel errors. A misconfigured host fails at
// NewService; calling an unwired kind fails closed at the call site.
var (
	// ErrNoKindConfigured is returned by NewService when neither kind is wired
	// (both Repositories fields nil) — an authorization feature that does nothing.
	ErrNoKindConfigured = errors.New("authorization: no kind configured (Repositories.Relationships and Repositories.Roles are both nil)")

	// ErrModelRequired is returned by NewService for a partial relationship-kind
	// wiring: Repositories.Relationships is set without Config.Model, or Config.Model
	// is set without the repository. The relationship kind needs both.
	ErrModelRequired = errors.New("authorization: Repositories.Relationships and Config.Model must be wired together (both or neither)")

	// ErrRelationshipsNotConfigured is returned by every relationship-kind method
	// when that kind is off (Repositories.Relationships was nil).
	ErrRelationshipsNotConfigured = errors.New("authorization: relationship kind is not configured")

	// ErrRolesNotConfigured is returned by every roles-kind method when that kind
	// is off (Repositories.Roles was nil).
	ErrRolesNotConfigured = errors.New("authorization: roles kind is not configured")

	// ErrUsersetSubjectOnRole is returned by a roles-kind method given a Subject
	// carrying a non-empty Relation. Userset subjects ("group#member") are a
	// relationship-kind concept; silently dropping the field would treat the
	// userset as the group principal itself — a wrong-grant hazard, so it fails
	// closed.
	ErrUsersetSubjectOnRole = errors.New("authorization: roles kind does not accept a userset subject (Subject.Relation must be empty)")
)

// Root aliases — the engine's model/check vocabulary, re-exported so hosts write
// authorization.CheckRequest{Subject: authorization.Subject{…}} without importing
// the internal engine package.
type (
	Subject         = authorizersvc.Subject
	Resource        = authorizersvc.Resource
	CheckRequest    = authorizersvc.CheckRequest
	CheckResult     = authorizersvc.CheckResult
	LookupResult    = authorizersvc.LookupResult
	Schema          = authorizersvc.Schema
	ResourceSchema  = authorizersvc.ResourceSchema
	ResourceTypeDef = authorizersvc.ResourceTypeDef
	RelationDef     = authorizersvc.RelationDef
	SubjectTypeRef  = authorizersvc.SubjectTypeRef
	PermissionRule  = authorizersvc.PermissionRule
	PermissionCheck = authorizersvc.PermissionCheck
)

// Root aliases — the relationship rim types hosts pass to / receive from the
// relationship-kind methods.
type (
	CreateRelationship         = relationship.CreateRelationship
	RelationTarget             = relationship.RelationTarget
	SubjectRelationship        = relationship.SubjectRelationship
	ResourceRelationship       = relationship.ResourceRelationship
	SubjectRelationshipFilter  = relationship.SubjectRelationshipFilter
	ResourceRelationshipFilter = relationship.ResourceRelationshipFilter
)

// Assignment is the roles kind's grant record; hosts construct it via AssignRole
// arguments and receive it from the role listings.
type Assignment = role.Assignment

// Schema DSL, re-exported for host schema construction.
var (
	NewSchema         = authorizersvc.NewSchema
	MergeResourceType = authorizersvc.MergeResourceType
	Direct            = authorizersvc.Direct
	Through           = authorizersvc.Through
	AnyOf             = authorizersvc.AnyOf
	Remove            = authorizersvc.Remove
)

// Repositories is the set of outbound ports the feature needs. Each kind is
// nil-safe: a nil field turns that kind OFF structurally.
type Repositories struct {
	// Relationships backs the ReBAC kind; nil = the relationship kind is off.
	Relationships relationship.Storer
	// Roles backs the roles kind; nil = the roles kind is off.
	Roles role.Storer
}

// Config carries the relationship kind's settings. All three fields are
// relationship-kind-scoped; under a roles-only wiring they are ignored with no
// error (an orphaned tuning field is silent, the auth MailFrom precedent).
type Config struct {
	// Model is the ReBAC schema. Required when Relationships is wired, forbidden
	// otherwise (ErrModelRequired).
	Model Schema
	// MaxTraversalDepth bounds the engine's through-traversal recursion; <= 0
	// resolves to the engine default (10). Engine-only — never passed to a store.
	MaxTraversalDepth int
	// IDs mints each relationship_id at CreateRelationships. The zero value is the
	// nanoid default; a cryptids.Database generator defers to the DDL DEFAULT.
	IDs cryptids.IDGenerator
}

// Service is the authorization feature's host-facing surface. Each kind's method
// family is present unconditionally; an unwired kind's methods fail closed with
// that kind's sentinel. There is no composed Check facade — a host composes the
// kinds in its own closure.
type Service struct {
	relationships *authorizersvc.Service // nil = relationship kind off
	roles         *rolesvc.Service       // nil = roles kind off
}

// NewService validates the (repos, cfg) pair and builds the wired kinds. Zero
// kinds is ErrNoKindConfigured; a relationship kind wired without its Model (or
// vice versa) is ErrModelRequired; an invalid Model is the schema validator's
// loud error. A roles-only wiring succeeds with no Model.
func NewService(repos Repositories, cfg Config) (*Service, error) {
	hasRel := repos.Relationships != nil
	hasRoles := repos.Roles != nil
	if !hasRel && !hasRoles {
		return nil, ErrNoKindConfigured
	}

	modelSet := len(cfg.Model.ResourceTypes) > 0
	if hasRel != modelSet {
		return nil, ErrModelRequired
	}

	svc := &Service{}
	if hasRel {
		eng, err := authorizersvc.NewService(repos.Relationships, cfg.Model, authorizersvc.Config{
			MaxTraversalDepth: cfg.MaxTraversalDepth,
			IDs:               cfg.IDs,
		})
		if err != nil {
			return nil, err
		}
		svc.relationships = eng
	}
	if hasRoles {
		svc.roles = rolesvc.NewService(repos.Roles)
	}
	return svc, nil
}

// Register mounts the feature: it logs one line and registers NO routes (the
// /authorization/* namespace is reserved). It tolerates a zero-value Mount.
func (s *Service) Register(m feature.Mount) error {
	if m.Logger != nil {
		m.Logger.Info("registered authorization feature",
			"relationships", s.relationships != nil,
			"roles", s.roles != nil,
		)
	}
	return nil
}

// =============================================================================
// Relationship kind (fails closed with ErrRelationshipsNotConfigured when off)
// =============================================================================

// Check evaluates a permission check.
func (s *Service) Check(ctx context.Context, req CheckRequest) (CheckResult, error) {
	if s.relationships == nil {
		return CheckResult{}, ErrRelationshipsNotConfigured
	}
	return s.relationships.Check(ctx, req)
}

// CheckBatch evaluates multiple permission checks.
func (s *Service) CheckBatch(ctx context.Context, reqs []CheckRequest) ([]CheckResult, error) {
	if s.relationships == nil {
		return nil, ErrRelationshipsNotConfigured
	}
	return s.relationships.CheckBatch(ctx, reqs)
}

// FilterAuthorized returns only the resource IDs the subject can access.
func (s *Service) FilterAuthorized(ctx context.Context, subject Subject, permission, resourceType string, resourceIDs []string) ([]string, error) {
	if s.relationships == nil {
		return nil, ErrRelationshipsNotConfigured
	}
	return s.relationships.FilterAuthorized(ctx, subject, permission, resourceType, resourceIDs)
}

// LookupResources returns the resource IDs of a type the subject can access.
func (s *Service) LookupResources(ctx context.Context, subject Subject, permission, resourceType string) (LookupResult, error) {
	if s.relationships == nil {
		return LookupResult{}, ErrRelationshipsNotConfigured
	}
	return s.relationships.LookupResources(ctx, subject, permission, resourceType)
}

// CreateRelationships validates and persists a batch of relationship tuples,
// minting each relationship_id from Config.IDs.
func (s *Service) CreateRelationships(ctx context.Context, relationships []CreateRelationship) error {
	if s.relationships == nil {
		return ErrRelationshipsNotConfigured
	}
	return s.relationships.CreateRelationships(ctx, relationships)
}

// DeleteRelationship removes a specific relationship tuple.
func (s *Service) DeleteRelationship(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) error {
	if s.relationships == nil {
		return ErrRelationshipsNotConfigured
	}
	return s.relationships.DeleteRelationship(ctx, resourceType, resourceID, relation, subjectType, subjectID)
}

// DeleteResourceRelationships removes all relationships for a resource.
func (s *Service) DeleteResourceRelationships(ctx context.Context, resourceType, resourceID string) error {
	if s.relationships == nil {
		return ErrRelationshipsNotConfigured
	}
	return s.relationships.DeleteResourceRelationships(ctx, resourceType, resourceID)
}

// DeleteByResourceAndSubject removes all relations a subject holds on a resource.
func (s *Service) DeleteByResourceAndSubject(ctx context.Context, resourceType, resourceID, subjectType, subjectID string) error {
	if s.relationships == nil {
		return ErrRelationshipsNotConfigured
	}
	return s.relationships.DeleteByResourceAndSubject(ctx, resourceType, resourceID, subjectType, subjectID)
}

// RemoveMember removes a subject from a resource with last-owner protection.
func (s *Service) RemoveMember(ctx context.Context, resourceType, resourceID, subjectType, subjectID string) error {
	if s.relationships == nil {
		return ErrRelationshipsNotConfigured
	}
	return s.relationships.RemoveMember(ctx, resourceType, resourceID, subjectType, subjectID)
}

// ValidateRelation reports whether a relationship is allowed by the schema.
func (s *Service) ValidateRelation(resourceType, relation, subjectType string) error {
	if s.relationships == nil {
		return ErrRelationshipsNotConfigured
	}
	return s.relationships.ValidateRelation(resourceType, relation, subjectType)
}

// ValidateRelationships validates every relationship against the schema.
func (s *Service) ValidateRelationships(relationships []CreateRelationship) error {
	if s.relationships == nil {
		return ErrRelationshipsNotConfigured
	}
	return s.relationships.ValidateRelationships(relationships)
}

// GetSchema returns the relationship kind's schema.
func (s *Service) GetSchema() (Schema, error) {
	if s.relationships == nil {
		return Schema{}, ErrRelationshipsNotConfigured
	}
	return s.relationships.GetSchema(), nil
}

// GetPermissionsForRelation returns the permissions a relation grants on a type.
func (s *Service) GetPermissionsForRelation(resourceType, relation string) ([]string, error) {
	if s.relationships == nil {
		return nil, ErrRelationshipsNotConfigured
	}
	return s.relationships.GetPermissionsForRelation(resourceType, relation), nil
}

// GetRelationTargets returns all subjects with a specific relation to a resource.
func (s *Service) GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]RelationTarget, error) {
	if s.relationships == nil {
		return nil, ErrRelationshipsNotConfigured
	}
	return s.relationships.GetRelationTargets(ctx, resourceType, resourceID, relation)
}

// ListRelationshipsBySubject pages the resources a subject relates to.
func (s *Service) ListRelationshipsBySubject(ctx context.Context, subjectType, subjectID string, filter SubjectRelationshipFilter, req crud.ListRequest) (crud.Page[SubjectRelationship], error) {
	if s.relationships == nil {
		return crud.Page[SubjectRelationship]{}, ErrRelationshipsNotConfigured
	}
	return s.relationships.ListRelationshipsBySubject(ctx, subjectType, subjectID, filter, req)
}

// ListRelationshipsByResource pages the subjects related to a resource.
func (s *Service) ListRelationshipsByResource(ctx context.Context, resourceType, resourceID string, filter ResourceRelationshipFilter, req crud.ListRequest) (crud.Page[ResourceRelationship], error) {
	if s.relationships == nil {
		return crud.Page[ResourceRelationship]{}, ErrRelationshipsNotConfigured
	}
	return s.relationships.ListRelationshipsByResource(ctx, resourceType, resourceID, filter, req)
}

// =============================================================================
// Roles kind (fails closed with ErrRolesNotConfigured when off; a userset
// Subject is rejected with ErrUsersetSubjectOnRole)
// =============================================================================

// AssignRole grants a subject a role, optionally scoped to a resource.
func (s *Service) AssignRole(ctx context.Context, subject Subject, roleName, resourceType, resourceID string) error {
	if s.roles == nil {
		return ErrRolesNotConfigured
	}
	if subject.Relation != "" {
		return ErrUsersetSubjectOnRole
	}
	return s.roles.AssignRole(ctx, subject.Type, subject.ID, roleName, resourceType, resourceID)
}

// UnassignRole removes an exact role assignment.
func (s *Service) UnassignRole(ctx context.Context, subject Subject, roleName, resourceType, resourceID string) error {
	if s.roles == nil {
		return ErrRolesNotConfigured
	}
	if subject.Relation != "" {
		return ErrUsersetSubjectOnRole
	}
	return s.roles.UnassignRole(ctx, subject.Type, subject.ID, roleName, resourceType, resourceID)
}

// HasRole reports whether a subject holds a role at a scope (with the global
// fallback: a global grant satisfies a scoped check).
func (s *Service) HasRole(ctx context.Context, subject Subject, roleName, resourceType, resourceID string) (bool, error) {
	if s.roles == nil {
		return false, ErrRolesNotConfigured
	}
	if subject.Relation != "" {
		return false, ErrUsersetSubjectOnRole
	}
	return s.roles.HasRole(ctx, subject.Type, subject.ID, roleName, resourceType, resourceID)
}

// ListRoleAssignmentsBySubject pages a subject's role assignments.
func (s *Service) ListRoleAssignmentsBySubject(ctx context.Context, subject Subject, req crud.ListRequest) (crud.Page[Assignment], error) {
	if s.roles == nil {
		return crud.Page[Assignment]{}, ErrRolesNotConfigured
	}
	if subject.Relation != "" {
		return crud.Page[Assignment]{}, ErrUsersetSubjectOnRole
	}
	return s.roles.ListRoleAssignmentsBySubject(ctx, subject.Type, subject.ID, req)
}

// ListRoleAssignmentsByResource pages the assignments scoped to a resource
// (direct-scope only).
func (s *Service) ListRoleAssignmentsByResource(ctx context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[Assignment], error) {
	if s.roles == nil {
		return crud.Page[Assignment]{}, ErrRolesNotConfigured
	}
	return s.roles.ListRoleAssignmentsByResource(ctx, resourceType, resourceID, req)
}
