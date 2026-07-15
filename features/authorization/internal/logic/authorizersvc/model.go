// Package authorizersvc is the sealed evaluation engine of the authorization
// feature's RELATIONSHIP kind: the registered-data permission model (schema
// DSL + validator) and the Check/Lookup engine that evaluates it against a
// relationship.Storer. The roots re-export the model types and DSL; the engine
// methods are promoted onto the feature Service.
//
// The model governs the RELATIONSHIP kind ONLY — the roles kind has no schema
// (opaque strings, package role). Adding a resource type is a code change with
// ZERO migration: relations and permissions are registered data, not columns
// (the EAV-spine philosophy applied to permissions).
package authorizersvc

import "github.com/gopernicus/gopernicus/features/authorization/domain/relationship"

// =============================================================================
// Core check types
// =============================================================================

// PrincipalRef is a concrete decision caller or actor — always a (Type, ID)
// pair, NEVER a userset. It is the only subject a decision request carries: a
// userset relation cannot be expressed here, so no public decision path can
// smuggle one in. It mirrors identity.Principal field-for-field and is directly
// convertible from it (see authorization.PrincipalFrom).
type PrincipalRef struct {
	Type string // "user" or "service_account" (the runtime principal types)
	ID   string
}

// Validate reports whether the principal is structurally usable: both Type and
// ID must be present and well formed (see relationship.ValidateRefField).
func (p PrincipalRef) Validate() error {
	if err := relationship.ValidateRefField("principal type", p.Type); err != nil {
		return err
	}
	return relationship.ValidateRefField("principal id", p.ID)
}

// Resource is what is being accessed.
type Resource struct {
	Type string // "post", "org", "folder"
	ID   string
}

// CheckRequest is a permission-check query. Principal is concrete: a decision
// request never carries a userset relation.
type CheckRequest struct {
	Principal  PrincipalRef
	Permission string // "view", "edit", "delete"
	Resource   Resource
}

// Validate reports whether the request is structurally well formed: the
// principal, the permission, and the resource type/id are all present and well
// formed. It applies no schema knowledge.
func (r CheckRequest) Validate() error {
	if err := r.Principal.Validate(); err != nil {
		return err
	}
	if err := relationship.ValidateRefField("permission", r.Permission); err != nil {
		return err
	}
	if err := relationship.ValidateRefField("resource type", r.Resource.Type); err != nil {
		return err
	}
	return relationship.ValidateRefField("resource id", r.Resource.ID)
}

// CheckResult is the outcome of a permission check.
//
// ReasonCode is the STABLE, coarse machine classification of the decision:
// ReasonGranted when Allowed, ReasonDenied otherwise. It is the CONTRACT surface
// — a host, an audit sink, or the explain trace may switch on it, and it is
// deterministic (equivalent state yields the same code regardless of map
// iteration or which path granted). Reason is non-contract debug text
// ("direct:owner", "through:org->direct:admin", "no matching rule"); its
// vocabulary is not frozen and callers must never switch on it.
type CheckResult struct {
	Allowed    bool
	ReasonCode Reason
	Reason     string
}

// =============================================================================
// LookupResult
// =============================================================================

// LookupResult is returned by the engine's LookupResources — pure schema/tuple
// enumeration.
//
// Contract: IDs is ALWAYS a non-nil slice. An empty slice means the subject has
// access to no resource of that type. There is no admin/unrestricted bypass in
// the engine: a host that wants admin-sees-everything checks for it in its own
// closure BEFORE calling LookupResources and then skips ID filtering.
type LookupResult struct {
	IDs []string
}

// =============================================================================
// Schema types
// =============================================================================

// Schema defines how permissions are computed from relations.
type Schema struct {
	ResourceTypes map[string]ResourceTypeDef
}

// ResourceTypeDef defines the relations and permissions of one resource type.
type ResourceTypeDef struct {
	Relations   map[string]RelationDef
	Permissions map[string]PermissionRule
}

// RelationDef defines what subjects may be assigned a relation.
type RelationDef struct {
	AllowedSubjects []SubjectTypeRef
}

// SubjectTypeRef references a subject type, optionally with a relation
// ("group#member").
type SubjectTypeRef struct {
	Type     string // "user", "service_account", or a schema type like "group"
	Relation string // optional: "member" for group#member
}

// PermissionRule defines how a permission is computed: any of its checks grants
// it (OR/union).
type PermissionRule struct {
	AnyOf []PermissionCheck

	// remove signals that this permission should be deleted during a schema
	// merge (see Remove). Unexported so only the merge machinery honors it.
	remove bool
}

// PermissionCheck is a single check in a permission rule: either a Direct
// relation on this resource, or a Through traversal that checks a Permission on
// the target of a relation.
type PermissionCheck struct {
	Relation string // direct relation on this resource

	Through    string // relation to traverse (e.g. "org", "parent")
	Permission string // permission to check on the traversal target
}

// ResourceSchema pairs a resource type name with its definition. Each domain
// contributes a []ResourceSchema; NewSchema composes them.
type ResourceSchema struct {
	Name string
	Def  ResourceTypeDef
}

// =============================================================================
// Schema DSL helpers
// =============================================================================

// Direct builds a check for a direct relation.
func Direct(relation string) PermissionCheck {
	return PermissionCheck{Relation: relation}
}

// Through builds a check that traverses a relation and checks permission there.
func Through(relation, permission string) PermissionCheck {
	return PermissionCheck{Through: relation, Permission: permission}
}

// AnyOf builds a permission rule from checks (any grants access).
func AnyOf(checks ...PermissionCheck) PermissionRule {
	return PermissionRule{AnyOf: checks}
}

// Remove returns a rule that signals deletion during a schema merge. Use it in
// an override schema to delete a permission defined in the base.
//
// KEEP decision (Z1 task-3, 2026-07-09): the original's merge affordance is
// salvaged faithfully — it is small, self-contained, and MergeResourceType
// depends on it for override composition.
func Remove() PermissionRule {
	return PermissionRule{remove: true}
}

// IsRemove reports whether this rule signals deletion during a merge.
func (r PermissionRule) IsRemove() bool {
	return r.remove
}
