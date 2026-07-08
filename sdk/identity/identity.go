// Package identity is the shared identity-in-context vocabulary: the effective
// caller (Principal), the two well-known subject-type constants, and the
// convention for carrying a Principal on a request context.
//
// Lineage (AV5): a Principal is a (subject_type, subject_id) string pair, never
// a registry table row. There is one Principal shape platform-wide — the same
// value authentication resolves from a credential and re-exports as its own
// Principal — so a host's authorizer reads it unadapted. Like sdk/oauth and
// sdk/errs, this package ships no default implementation because there is
// nothing to implement: it is shared vocabulary, not a facility with swappable
// backends.
//
// Fails-closed convention: FromContext reports (Principal, false) when the
// request carried no identity. A reader that treats absent identity as
// anonymous-allowed is a bug — absence means deny (401), never permit.
//
// Scope fence: vocabulary only — middleware and credential resolution live with
// the credential owners (features/authentication); authorization vocabulary is
// deliberately absent.
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
