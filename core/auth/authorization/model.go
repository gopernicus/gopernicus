// Package authorization provides relationship-based access control (ReBAC).
//
// The Authorizer evaluates permission checks against a schema that defines
// how relations on resources map to permissions. It supports direct relations,
// through-relation traversal (e.g., post->org->admin), group expansion,
// platform admin bypass, and self-access.
//
// # Schema DSL
//
// Schemas are built using [NewSchema] with [ResourceSchema] slices from each
// domain. Permissions are defined using [Direct], [Through], and [AnyOf]:
//
//	schema := authorization.NewSchema(tenantSchema, projectSchema)
//
// # Permission Checks
//
//	result, err := authz.Check(ctx, authorization.CheckRequest{
//	    Subject:    authorization.Subject{Type: "user", ID: userID},
//	    Permission: "edit",
//	    Resource:   authorization.Resource{Type: "project", ID: projectID},
//	})
//
// # Storer
//
// The [Storer] interface defines the storage contract. Implementations live
// in separate packages (e.g., a pgx-based store).
package authorization

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/sdk/fop"
)

// =============================================================================
// Core Check Types
// =============================================================================

// Subject represents who is requesting access (user, apikey, service).
type Subject struct {
	Type     string // "user", "apikey", "service"
	ID       string
	Relation string // optional, for "group#member" style
}

// Resource represents what is being accessed.
type Resource struct {
	Type string // "post", "org", "folder"
	ID   string
}

// CheckRequest is a permission check query.
type CheckRequest struct {
	Subject    Subject
	Permission string // "view", "edit", "delete"
	Resource   Resource
}

// CheckResult contains the result of a permission check.
type CheckResult struct {
	Allowed bool
	Reason  string // for debugging: "direct:owner", "through:org->direct:admin", etc.
}

// =============================================================================
// Relationship Types
// =============================================================================

// CreateRelationship defines a relationship to create.
// Used with Authorizer.CreateRelationships for batch creation.
type CreateRelationship struct {
	ResourceType    string
	ResourceID      string
	Relation        string
	SubjectType     string
	SubjectID       string
	SubjectRelation *string // optional, for "group#member" style subjects
}

// RelationTarget represents a subject related to a resource.
// Returned by Storer.GetRelationTargets for "through" permission traversal.
type RelationTarget struct {
	SubjectType     string
	SubjectID       string
	SubjectRelation *string // optional, for "group#member" style subjects
}

// SubjectRelationship represents a resource that a subject has a relationship with.
// Returned by Storer.ListRelationshipsBySubject for querying
// "what resources does this subject have access to?"
type SubjectRelationship struct {
	ResourceType string
	ResourceID   string
	Relation     string
	CreatedAt    time.Time
}

// SubjectRelationshipFilter contains optional filters for ListRelationshipsBySubject.
// SubjectType and SubjectID are required method parameters, not part of this filter.
type SubjectRelationshipFilter struct {
	// ResourceType filters to a specific resource type (e.g., "tenant").
	ResourceType *string
	// Relation filters to a specific relation (e.g., "member", "admin").
	Relation *string
}

// ResourceRelationship represents a subject's relationship to a resource.
// Returned by Storer.ListRelationshipsByResource for querying
// "what subjects have access to resource X?"
type ResourceRelationship struct {
	SubjectType string    `json:"subject_type"`
	SubjectID   string    `json:"subject_id"`
	Relation    string    `json:"relation"`
	CreatedAt   time.Time `json:"created_at"`
}

// ResourceRelationshipFilter contains optional filters for ListRelationshipsByResource.
// ResourceType and ResourceID are required method parameters, not part of this filter.
type ResourceRelationshipFilter struct {
	// SubjectType filters to a specific subject type (e.g., "user").
	SubjectType *string
	// Relation filters to a specific relation (e.g., "owner").
	Relation *string
}

// =============================================================================
// Schema Types
// =============================================================================

// Schema defines how permissions are computed from relations.
type Schema struct {
	// Per-resource-type definitions (relations and permissions).
	ResourceTypes map[string]ResourceTypeDef
}

// ResourceTypeDef defines permissions for a specific resource type.
type ResourceTypeDef struct {
	// Relations that can be assigned to this resource type.
	Relations map[string]RelationDef

	// Permissions and how to compute them.
	Permissions map[string]PermissionRule
}

// RelationDef defines what subjects can be assigned a relation.
type RelationDef struct {
	// What subject types can be assigned this relation.
	AllowedSubjects []SubjectTypeRef
}

// SubjectTypeRef references a subject type, optionally with a relation.
type SubjectTypeRef struct {
	Type     string // "user", "group", "apikey", "application", "service"
	Relation string // optional: "member" for group#member
}

// PermissionRule defines how a permission is computed.
type PermissionRule struct {
	// Any of these grants the permission (OR/union).
	AnyOf []PermissionCheck

	// remove signals that this permission should be removed during schema merge.
	remove bool
}

// PermissionCheck is a single check in a permission rule.
type PermissionCheck struct {
	// Direct relation on this resource.
	Relation string

	// OR: Traverse through a relation and check permission there.
	Through    string // relation to traverse (e.g., "org", "parent")
	Permission string // permission to check on the target
}

// ResourceSchema pairs a resource type name with its definition.
// Used with NewSchema to register resource types from bridges.
type ResourceSchema struct {
	Name string
	Def  ResourceTypeDef
}

// =============================================================================
// Schema DSL Helpers
// =============================================================================

// Direct creates a check for a direct relation.
func Direct(relation string) PermissionCheck {
	return PermissionCheck{Relation: relation}
}

// Through creates a check that traverses a relation and checks permission there.
func Through(relation, permission string) PermissionCheck {
	return PermissionCheck{Through: relation, Permission: permission}
}

// AnyOf creates a permission rule from multiple checks (any grants access).
func AnyOf(checks ...PermissionCheck) PermissionRule {
	return PermissionRule{AnyOf: checks}
}

// Remove returns a PermissionRule that signals deletion during schema merge.
// Use this in override schemas to remove a permission defined in the base.
func Remove() PermissionRule {
	return PermissionRule{remove: true}
}

// IsRemove reports whether this rule signals deletion during merge.
func (r PermissionRule) IsRemove() bool {
	return r.remove
}

// =============================================================================
// LookupResult
// =============================================================================

// LookupResult is returned by LookupResources.
//
// Using an explicit Unrestricted bool (not a nil sentinel) makes the admin
// bypass visible at every call site and fail-closed: the zero value of bool
// is false, so any accidental early return restricts rather than opens.
//
// Contract: when Unrestricted is false, IDs is always a non-nil slice.
// An empty non-nil slice means the subject has no access to any resource.
type LookupResult struct {
	// Unrestricted is true when the subject has platform-admin bypass.
	// The caller must skip ID filtering entirely — do not pass IDs to the store.
	Unrestricted bool

	// IDs holds the authorized resource IDs. Meaningful only when !Unrestricted.
	// Always non-nil when Unrestricted is false.
	IDs []string
}

// =============================================================================
// Storer Interface
// =============================================================================

// Storer defines the storage contract for the authorization engine.
//
// Implementations handle relationship persistence and querying. The interface
// is intentionally lean — only what the Authorizer needs for permission checks,
// relationship CRUD, and listing. Business logic (last-owner guards, role
// change validation) lives on the Authorizer, not the store.
type Storer interface {
	// -----------------------------------------------------------------
	// Permission Checks
	// -----------------------------------------------------------------

	// CheckRelationWithGroupExpansion checks if a subject has a relation to a resource,
	// including indirect relations via group membership.
	CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error)

	// GetRelationTargets returns all subjects that have a specific relation to a resource.
	// Used for "through" permission traversal.
	GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]RelationTarget, error)

	// CheckRelationExists checks if a specific relationship tuple exists.
	// Used for platform admin checks.
	CheckRelationExists(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error)

	// CheckBatchDirect performs a batch permission check across multiple resource IDs.
	// Returns a map of resourceID -> allowed for a single relation check across multiple resources.
	// Optimized for list operations (e.g., filtering 50 posts by read permission).
	CheckBatchDirect(ctx context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string) (map[string]bool, error)

	// -----------------------------------------------------------------
	// Relationship CRUD
	// -----------------------------------------------------------------

	// CreateRelationships creates multiple relationships in batch.
	CreateRelationships(ctx context.Context, relationships []CreateRelationship) error

	// DeleteResourceRelationships removes all relationships for a resource.
	DeleteResourceRelationships(ctx context.Context, resourceType, resourceID string) error

	// DeleteRelationship removes a specific relationship tuple.
	DeleteRelationship(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) error

	// DeleteByResourceAndSubject removes all relations a subject holds on a specific resource.
	DeleteByResourceAndSubject(ctx context.Context, resourceType, resourceID, subjectType, subjectID string) error

	// -----------------------------------------------------------------
	// Counts
	// -----------------------------------------------------------------

	// CountByResourceAndRelation counts relationships by resource and relation.
	// Used for last-owner protection checks.
	CountByResourceAndRelation(ctx context.Context, resourceType, resourceID, relation string) (int, error)

	// -----------------------------------------------------------------
	// Listing
	// -----------------------------------------------------------------

	// ListRelationshipsBySubject returns all resources that a subject has relationships with.
	// Used for "what resources of type X does this subject have access to?" queries.
	ListRelationshipsBySubject(ctx context.Context, subjectType, subjectID string, filter SubjectRelationshipFilter, orderBy fop.Order, page fop.PageStringCursor) ([]SubjectRelationship, fop.Pagination, error)

	// ListRelationshipsByResource returns all subjects that have relationships with a resource.
	// Used for "what subjects have access to resource X?" queries.
	ListRelationshipsByResource(ctx context.Context, resourceType, resourceID string, filter ResourceRelationshipFilter, orderBy fop.Order, page fop.PageStringCursor) ([]ResourceRelationship, fop.Pagination, error)

	// -----------------------------------------------------------------
	// LookupResources
	// -----------------------------------------------------------------

	// LookupResourceIDs returns resource IDs where the subject has any of the
	// given direct relations (with group expansion).
	LookupResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType, subjectID string) ([]string, error)

	// LookupResourceIDsByRelationTarget returns resource IDs that have a specific
	// relation pointing to any of the given target IDs. Used for through-relation
	// traversal in LookupResources.
	LookupResourceIDsByRelationTarget(ctx context.Context, resourceType, relation, targetType string, targetIDs []string) ([]string, error)
}
