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

// =============================================================================
// Core check types
// =============================================================================

// Subject is who is requesting access. Relation is optional, naming a userset
// ("group#member") rather than a concrete principal.
type Subject struct {
	Type     string // "user" or "service_account" (the runtime principal types)
	ID       string
	Relation string // optional, for "group#member" style
}

// Resource is what is being accessed.
type Resource struct {
	Type string // "post", "org", "folder"
	ID   string
}

// CheckRequest is a permission-check query.
type CheckRequest struct {
	Subject    Subject
	Permission string // "view", "edit", "delete"
	Resource   Resource
}

// CheckResult is the outcome of a permission check. Reason aids debugging
// ("direct:owner", "through:org->direct:admin", "self", "platform_admin").
type CheckResult struct {
	Allowed bool
	Reason  string
}

// =============================================================================
// LookupResult
// =============================================================================

// LookupResult is returned by the engine's LookupResources.
//
// Using an explicit Unrestricted bool (not a nil sentinel) makes the admin
// bypass visible at every call site and fail-closed: the zero value of bool is
// false, so any accidental early return restricts rather than opens.
//
// Contract: when Unrestricted is false, IDs is always a non-nil slice. An empty
// non-nil slice means the subject has access to no resource. When Unrestricted
// is true the caller must SKIP ID filtering entirely — do not pass IDs to the
// store.
type LookupResult struct {
	Unrestricted bool
	IDs          []string
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
