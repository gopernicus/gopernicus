// Package identity is the shared identity-in-context vocabulary and the Resolver
// port: the effective caller (Principal), the well-known subject-type and
// address-kind constants, the convention for carrying a Principal on a request
// context, and the port that turns a Principal into its display/contact Info.
//
// Lineage (AV5): a Principal is a (subject_type, subject_id) string pair, never
// a registry table row. There is one Principal shape platform-wide — the same
// value authentication resolves from a credential and re-exports as its own
// Principal — so a host's authorizer reads it unadapted.
//
// The Resolver port has no default here. Like sdk/oauth, its only
// implementations need a subsystem sdk does not own: identity data (credentials,
// verification, lifecycle) is feature-owned, so a Resolver lives with the
// credential owners — features/authentication is the first. The only behavior
// this package ships is ResolveAll, a strict positional loop over a Resolver.
//
// Fails-closed convention: FromContext reports (Principal, false) when the
// request carried no identity, and a Resolver returns the errs not-found class
// (errs.ErrNotFound) for a principal it cannot resolve. A reader that treats
// absent identity as anonymous-allowed, or a Resolver that fabricates an Info,
// is a bug — absence means deny (401), never permit.
//
// Scope fence: identity vocabulary and the Resolver port only — middleware and
// credential resolution live with the credential owners
// (features/authentication); authorization vocabulary is deliberately absent.
package identity

import "context"

// Subject-type conventions (AV5 — actor references are (subject_type,
// subject_id) string pairs, never a registry table). They match the ReBAC
// Subject vocabulary so a host's authorizer reads them unadapted.
const (
	// User is the subject type for a human user, and for a personal
	// (act-as-user) credential resolved to its human owner.
	User = "user"
	// ServiceAccount is the subject type for a machine identity.
	ServiceAccount = "service_account"
)

// Address-kind conventions. Kinds are open strings; these ship as the
// conventional names, not a closed set — a Resolver may carry any kind.
const (
	// KindEmail is the conventional Address kind for an email address.
	KindEmail = "email"
	// KindPhone is the conventional Address kind for a phone number.
	KindPhone = "phone"
)

// contextKey is an unexported type for the Principal stashed on a request
// context, so no other package can collide with or read the raw key.
type contextKey int

const principalKey contextKey = iota

// Principal is the effective caller: a (Type, ID) subject pair. It is the single
// value type AV5 pins, shared platform-wide.
type Principal struct {
	Type string
	ID   string
}

// Address is how you REACH or IDENTIFY a person out-of-band — an email to send
// to, a phone number to text. It is not a Principal: a Principal is a resolved
// actor reference the platform authorizes against, an Address is a contact or
// claim channel. Kind is an open string (KindEmail, KindPhone are the
// conventional names).
type Address struct {
	Kind  string
	Value string
}

// Info is the display and contact PROJECTION of a Principal — a name to render
// and the addresses to reach it out-of-band. It is never the identity record
// itself: credentials, verification state, and lifecycle stay feature-owned. No
// User struct enters sdk, ever; a Resolver assembles Info from whatever record
// it owns.
type Info struct {
	Principal   Principal
	DisplayName string
	Addresses   []Address
}

// Resolver resolves a Principal to its display and contact Info. Implementations
// live with the credential owners (features/authentication is the first), never
// here — identity data is feature-owned.
//
// Fail CLOSED: an unknown principal type, a missing record, or an unwired
// backing subsystem returns an error satisfying errs.ErrNotFound (checked with
// errors.Is). A Resolver never fabricates an Info for a principal it cannot
// resolve.
//
// One method, v1. A batch Resolve is a future interface-upgrade seam; until a
// consumer needs it, ResolveAll covers the loop.
type Resolver interface {
	Resolve(ctx context.Context, p Principal) (Info, error)
}

// WithPrincipal returns a copy of ctx carrying p. The write site is the
// credential owner's middleware (features/authentication), never this package.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey, p)
}

// FromContext returns the Principal stashed by WithPrincipal, if any. A
// zero-valued (empty-ID) principal reports (Principal{}, false); a reader that
// treats that false as anonymous-allowed is a bug — absence means deny.
func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey).(Principal)
	return p, ok && p.ID != ""
}

// ResolveAll resolves ps in order, returning Infos positionally aligned with the
// input (result[i] is the Info for ps[i]). It is STRICT: the first Resolve error
// aborts and is returned unwrapped, matching the fail-closed posture. A
// lenient/partial batch that collects per-principal errors is part of the future
// batch-upgrade seam, not shipped. An empty or nil ps returns an empty slice and
// a nil error.
func ResolveAll(ctx context.Context, r Resolver, ps []Principal) ([]Info, error) {
	infos := make([]Info, 0, len(ps))
	for _, p := range ps {
		info, err := r.Resolve(ctx, p)
		if err != nil {
			return nil, err
		}
		infos = append(infos, info)
	}
	return infos, nil
}
