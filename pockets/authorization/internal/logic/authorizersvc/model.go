// Package authorizersvc is the sealed evaluation engine of the authorization
// pocket's RELATIONSHIP kind: the registered-data permission model (schema
// DSL + validator) and the Check/Lookup engine that evaluates it against a
// relationship.Storer. The roots re-export the model types and DSL; the engine
// methods are promoted onto the pocket Service. The package also owns the
// kind-agnostic decision VOCABULARY (CheckRequest, CheckResult, Reason,
// LookupResult, Explanation, EvaluationLimits) and the ONE HTTP gate body
// (Gates over Checker/Declarer): those are shared contracts every
// decision-capable kind speaks, not relationship semantics. The trigger to
// extract them into a neutral package is a third kind (the policy seam).
//
// The schema governs the RELATIONSHIP kind ONLY. The roles kind has its own,
// separate model — decisionsvc.RoleModel, in the compositions tier that also
// owns the composite decider — and the two never declare the same (resource
// type, permission) pair. Adding a resource type here is a code change with
// ZERO migration: relations and permissions are registered data, not columns
// (the EAV-spine philosophy applied to permissions).
package authorizersvc

import (
	"fmt"

	"github.com/gopernicus/gopernicus/pockets/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/sdk"
)

// ErrInvalidCursor reports a LookupRequest.After the decision surface refuses:
// malformed base64/JSON, an unknown cursor version, a fingerprint bound to a
// DIFFERENT query (another principal, permission, resource type, owning kind,
// or owning model), or a structurally invalid resource id inside it. It wraps
// sdk.ErrInvalidInput (HTTP 400) — a client presenting it restarts the
// enumeration from page one; it is never an evaluation-budget outcome.
var ErrInvalidCursor = fmt.Errorf("authorization: invalid lookup cursor: %w", sdk.ErrInvalidInput)

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
// LookupRequest / LookupResult
// =============================================================================

// LookupRequest is the struct-input form of an enumeration query — what
// LookupResourcesIn takes, beside CheckRequest in this same vocabulary. It is a
// SIBLING of the positional LookupResources, never a replacement: that method
// sits on host-defined ports and on the internal kind interface, so its
// signature does not change, and a new field is additive here with zero
// signature churn.
//
// LookupResourcesIn is PAGED. Limit is a PAGE SIZE and After is the previous
// page's continuation:
//
//   - Limit 0 means MaxLookupResults — one full page's worth. It does NOT mean
//     unbounded (nothing here is), and it deliberately does NOT follow
//     crud.ListRequest, where 0 means DefaultLimit.
//   - Limit GREATER than MaxLookupResults is rejected by the decision surface
//     (which knows the resolved limits) as sdk.ErrInvalidInput: the budget
//     bounds the page size, so a page may not be asked to exceed it.
//   - A negative Limit is a validation error wrapping sdk.ErrInvalidInput (a
//     limit is not a reference, so it is not relationship.ErrInvalidRef), which
//     hosts map to 400 through the pocket's error mapper.
//   - Limit NEVER weakens the evaluation budget. The budget still bounds every
//     INTERMEDIATE node and every self-hierarchy root set, so an enumeration
//     whose intermediate work overflows is ErrEvaluationLimit on every page —
//     never a short list presented as complete.
//   - After is the NextCursor of the previous page; "" starts at the beginning.
//     It is opaque: the decision surface decodes it against the query it is
//     bound to (principal, permission, resource type, owning kind, owning model
//     digest) and refuses a foreign or stale one with ErrInvalidCursor. A
//     non-empty After is validated by that decode, not by Validate below.
//   - Unrestricted passes through untouched and IGNORES both fields: there are
//     no IDs to page, and the host must skip ID filtering entirely.
type LookupRequest struct {
	Principal    PrincipalRef
	Permission   string
	ResourceType string
	Limit        int
	After        string
}

// Validate reports whether the request is structurally well formed: the
// principal, the permission, and the resource type are all present and well
// formed, and Limit is not negative. It applies no schema knowledge.
func (r LookupRequest) Validate() error {
	if err := r.Principal.Validate(); err != nil {
		return err
	}
	if err := relationship.ValidateRefField("permission", r.Permission); err != nil {
		return err
	}
	if err := relationship.ValidateRefField("resource type", r.ResourceType); err != nil {
		return err
	}
	if r.Limit < 0 {
		return fmt.Errorf("authorization: lookup limit must not be negative, got %d: %w", r.Limit, sdk.ErrInvalidInput)
	}
	return nil
}

// LookupResult is the enumeration result of LookupResources.
//
// Contract: IDs is ALWAYS a non-nil slice. Unrestricted reports that the
// principal may access EVERY resource of the type because a role that grants
// the permission is held GLOBALLY — in which case IDs is empty and the host
// must skip ID filtering entirely rather than treat the empty slice as "none".
// Only the roles kind produces Unrestricted; the relationship kind is pure
// tuple enumeration and never does.
//
// An empty IDs with Unrestricted false means the subject has access to no
// resource of that type. There is no admin/unrestricted bypass in the
// relationship engine: a host that wants admin-sees-everything checks for it in
// its own closure BEFORE calling LookupResources and then skips ID filtering.
//
// HasMore reports that the PAGED surface (LookupResourcesIn) has at least one
// more ID after this page, and NextCursor is the opaque continuation to pass
// back as LookupRequest.After. NextCursor is set only when HasMore is true; an
// Unrestricted answer and the classic LookupResources never set either.
type LookupResult struct {
	IDs          []string
	Unrestricted bool

	HasMore    bool
	NextCursor string

	// Deprecated: Truncated is HasMore under its v0.7.0 name; it is set to the
	// same value and removed in the next minor.
	Truncated bool
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
