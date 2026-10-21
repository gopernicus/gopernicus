// Package authorization is the Zanzibar read protocol: a Checker answers
// whether a concrete principal holds a permission (or relation) on one
// resource, and a Lookup pages the resource IDs it holds a permission on.
// Authorizer is the composite. Writes are not part of this version.
//
// pockets/authorization is the implementation of record (its in-core engine
// adapter) and also a consumer (its lowered policy evaluator). Vendor
// implementations live in integrations/authorization/*. Imported as
// sdkauthorization beside the pocket's own authorization packages.
// authorizationtest pins the observable contract for every implementation.
//
// # Contract rules not visible in the signatures
//
// Identifiers. Every implementation enforces one grammar (ValidateRef): types,
// relations and permissions match ^[a-z][a-z0-9_]{1,62}[a-z0-9]$ (relations
// and permissions at most 50 bytes); IDs match ^[a-zA-Z0-9/_|\-=+]+$ and are
// at most 256 bytes, so "*" is never nameable. An object additionally fits
// len(type)+1+len(id) <= 256 (ValidateObject). A Principal is always
// concrete: a userset can never be expressed through it.
//
// Check. CheckRequest.Permission names a permission OR a relation (the
// Zanzibar meaning). CheckBatch is all-or-nothing: any failed item fails the
// call and no partial results are returned. Results come back in request
// order with the same length; duplicate requests are allowed. A batch larger
// than Limits().MaxCheckBatch is rejected, never truncated.
//
// Lookup. LookupResources enumerates permissions only; naming a relation is
// sdk.ErrInvalidInput. Limit 0 selects the implementation default; a negative
// Limit, or one above Limits().MaxLookupPage, is sdk.ErrInvalidInput. A page
// never holds more than Limit IDs (or the default when Limit is 0). IDs are
// unordered and unique within a page but MAY repeat across pages; an empty
// last page is legal; NextCursor "" means done. No cross-page snapshot is
// promised, and an implementation MAY serve one. A result is never truncated
// and presented as complete: an implementation that cannot enumerate the
// full set returns sdk.ErrUnavailable. Use LookupAll to drain, dedupe and
// bound a lookup.
//
// Cursors. Every implementation wraps its own continuation in the shared
// envelope (EncodeCursor/DecodeCursor): a version, a fingerprint of the
// principal, permission and resource type, then the inner cursor. A foreign,
// stale, expired or malformed After is ErrInvalidCursor.
//
// Errors are classed by sdk sentinel, never by message:
//   - grammar, an oversize batch, an out-of-range Limit, an undeclared
//     resource type, permission or relation, and ErrInvalidCursor wrap
//     sdk.ErrInvalidInput;
//   - an exhausted budget, depth or deadline, truncation, and "cannot
//     enumerate" wrap sdk.ErrUnavailable;
//   - contention wraps sdk.ErrConflict;
//   - a conditional (caveat or condition) answer is ErrConditional, which
//     wraps no sdk class: it is a server-side misconfiguration.
//
// The protocol never mentions a universal grant. A Check on one is
// Allowed=true; a Lookup on one cannot enumerate and is sdk.ErrUnavailable.
//
// SchemaReader and Limiter are optional and discovered by type assertion. A
// decorator over an implementation must forward both, or its consumers lose
// mount-time validation and batch sizing.
package authorization
