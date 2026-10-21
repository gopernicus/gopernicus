package authorization

import (
	"context"
	"errors"
	"fmt"

	"github.com/gopernicus/gopernicus/sdk"
)

var (
	// ErrInvalidCursor rejects a LookupRequest.After that is malformed, of an
	// unknown version, expired, or bound to another query. It matches
	// sdk.ErrInvalidInput; the caller restarts from the first page.
	ErrInvalidCursor = fmt.Errorf("authorization: invalid lookup cursor: %w", sdk.ErrInvalidInput)

	// ErrConditional reports a conditional (caveat or condition) answer.
	// Conditions are outside this protocol, so the answer is neither an allow
	// nor a deny. It wraps no sdk class: it is a server-side misconfiguration.
	ErrConditional = errors.New("authorization: conditional answer is not supported")
)

// Checker answers permission and relation questions.
type Checker interface {
	Check(ctx context.Context, req CheckRequest) (Decision, error)
	// CheckBatch is all-or-nothing: any failed item fails the call. Results come
	// back in request order with the same length; duplicate requests are allowed.
	CheckBatch(ctx context.Context, reqs []CheckRequest) ([]Decision, error)
}

// Lookup pages the resource IDs a principal holds a permission on.
type Lookup interface {
	// LookupResources never presents a truncated result as complete: an
	// implementation that cannot enumerate the full set returns sdk.ErrUnavailable.
	LookupResources(ctx context.Context, req LookupRequest) (LookupPage, error)
}

// Authorizer is the composite read protocol.
type Authorizer interface {
	Checker
	Lookup
}

// SchemaReader is optional, discovered by type assertion. It reports what the
// implementation's schema declares, without I/O, so a consumer can validate a
// policy before serving traffic.
type SchemaReader interface {
	DeclaresResource(resourceType string) bool
	// DeclaresPermission reports a declared permission OR relation.
	DeclaresPermission(resourceType, permissionOrRelation string) bool
	DeclaresSubject(resourceType, relation, subjectType string) bool
}

// Limiter is optional, discovered by type assertion.
type Limiter interface {
	Limits() Limits
}

// Principal is a concrete caller (Type, ID). It is never a userset: the
// protocol has no field that names one.
type Principal = sdk.Principal

// Resource is one object.
type Resource struct {
	Type string
	ID   string
}

// CheckRequest asks whether Principal holds Permission on Resource.
// Permission is a permission OR a relation (the Zanzibar meaning).
type CheckRequest struct {
	Principal  Principal
	Permission string
	Resource   Resource
}

// Decision is one answer. It is a struct so a later freshness token is additive.
type Decision struct {
	Allowed bool
}

// LookupRequest selects one page of resource IDs.
type LookupRequest struct {
	Principal    Principal
	Permission   string
	ResourceType string
	// Limit 0 selects the implementation default; < 0 or > Limits().MaxLookupPage
	// is sdk.ErrInvalidInput.
	Limit int
	// After is the previous page's NextCursor; "" starts at the beginning. It is
	// opaque: a foreign, stale or malformed value is ErrInvalidCursor.
	After string
}

// LookupPage is one page of a lookup.
type LookupPage struct {
	// IDs holds at most Limit IDs, unique within the page; they MAY repeat
	// across pages and are unordered. An empty last page is legal.
	IDs []string
	// NextCursor is "" when the lookup is done.
	NextCursor string
}

// Limits are an implementation's per-call bounds.
type Limits struct {
	MaxCheckBatch int
	MaxLookupPage int
}
