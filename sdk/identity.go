package sdk

import "context"

// Conventional subject types. Hosts may define additional types.
const (
	// PrincipalTypeUser identifies a user principal.
	PrincipalTypeUser = "user"
	// PrincipalTypeServiceAccount identifies a service-account principal.
	PrincipalTypeServiceAccount = "service_account"
)

// Conventional address kinds. Hosts may define additional kinds.
const (
	// AddressKindEmail identifies an email address.
	AddressKindEmail = "email"
	// AddressKindPhone identifies a phone number.
	AddressKindPhone = "phone"
)

type principalContextKey struct{}

// Principal identifies the effective caller by subject type and ID.
// Authentication establishes the caller; protected endpoints decide whether
// one is required.
type Principal struct {
	Type string
	ID   string
}

// IdentityAddress is an email, phone number, or other identifier/contact channel.
// Its presence does not establish permission to send notifications.
type IdentityAddress struct {
	Kind  string
	Value string
}

// IdentityInfo projects a principal's display name and addresses. Credentials,
// lifecycle, verification, and notification eligibility remain with the identity owner.
type IdentityInfo struct {
	Principal   Principal
	DisplayName string
	Addresses   []IdentityAddress
}

// IdentityResolver looks up display/contact information. An unknown principal type,
// missing record, or unwired backing subsystem returns an error satisfying
// ErrNotFound; implementations must not fabricate a successful result.
type IdentityResolver interface {
	Resolve(ctx context.Context, p Principal) (IdentityInfo, error)
}

// WithPrincipal returns a copy of ctx carrying p.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, p)
}

// PrincipalFromContext returns the caller when both Type and ID are present.
// An absent or incomplete principal returns Principal{}, false.
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalContextKey{}).(Principal)
	if !ok || p.Type == "" || p.ID == "" {
		return Principal{}, false
	}
	return p, true
}
