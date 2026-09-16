package model

import (
	"fmt"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"

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

// PrincipalRef is the concrete caller shared by checks and mutation commands.

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
	if err := tuples.ValidateRefField("permission", r.Permission); err != nil {
		return err
	}
	if err := tuples.ValidateRefField("resource type", r.Resource.Type); err != nil {
		return err
	}
	return tuples.ValidateRefField("resource id", r.Resource.ID)
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
//     list.Request, where 0 means DefaultLimit.
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
	if err := tuples.ValidateRefField("permission", r.Permission); err != nil {
		return err
	}
	if err := tuples.ValidateRefField("resource type", r.ResourceType); err != nil {
		return err
	}
	if r.Limit < 0 {
		return fmt.Errorf("authorization: lookup limit must not be negative, got %d: %w", r.Limit, sdk.ErrInvalidInput)
	}
	return nil
}

// LookupResult is the enumeration result of a named permission expression.
// Successful results have non-nil IDs. Unrestricted means the expression grants
// access to every resource ID of the requested type; IDs is then empty and the
// host skips authorization-ID filtering. This must follow from explicit policy,
// such as a satisfied global Role branch, never implicit scope fallback.
//
// Empty IDs with Unrestricted false means no matching resource. All/Any combine
// finite and unrestricted results by intersection/union, respectively.
//
// HasMore and NextCursor describe a paged result. NextCursor is the opaque value
// to pass as LookupRequest.After. Unrestricted and unpaged results have no cursor.
type LookupResult struct {
	IDs          []string
	Unrestricted bool

	HasMore    bool
	NextCursor string
}
