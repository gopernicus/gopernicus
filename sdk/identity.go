package sdk

import (
	"context"
	"sync/atomic"
)

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

type (
	principalContextKey        struct{}
	principalCaptureContextKey struct{}
)

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

// PrincipalCapture records the last principal attached by WithPrincipal on any
// context derived from the one WithPrincipalCapture returned, so code holding
// the outer context (an access logger) sees a principal attached on a derived
// request. It is observability only, never an authorization input: the
// recorded principal may be one the surface then refused. It is safe for
// concurrent use; the last write wins, including a clear (Principal{}).
type PrincipalCapture struct {
	parent *PrincipalCapture
	last   atomic.Pointer[Principal]
}

// WithPrincipalCapture returns a copy of ctx carrying a fresh capture, linked
// to any capture already on ctx so a write reaches every enclosing capture.
// The capture starts from the principal ctx already carries, if any. Install
// one per unit of work (a request, a job run); a long-lived capture on a base
// context records every unit's principal.
func WithPrincipalCapture(ctx context.Context) (context.Context, *PrincipalCapture) {
	parent, _ := ctx.Value(principalCaptureContextKey{}).(*PrincipalCapture)
	c := &PrincipalCapture{parent: parent}
	if p, ok := PrincipalFromContext(ctx); ok {
		c.last.Store(&p)
	}
	return context.WithValue(ctx, principalCaptureContextKey{}, c), c
}

// Principal returns the last principal recorded, under the same completeness
// rule as PrincipalFromContext: none, a clear, or an incomplete principal
// returns Principal{}, false.
func (c *PrincipalCapture) Principal() (Principal, bool) {
	p := c.last.Load()
	if p == nil || p.Type == "" || p.ID == "" {
		return Principal{}, false
	}
	return *p, true
}

// WithPrincipal returns a copy of ctx carrying p. It also records p in every
// PrincipalCapture on ctx (WithPrincipalCapture); the caller's ctx values are
// unchanged.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	if c, ok := ctx.Value(principalCaptureContextKey{}).(*PrincipalCapture); ok {
		for ; c != nil; c = c.parent {
			c.last.Store(&p)
		}
	}
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
