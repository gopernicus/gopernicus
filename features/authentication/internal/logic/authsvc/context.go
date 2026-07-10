package authsvc

import "context"

// contextKey is an unexported type for the client-attribution value stashed on a
// request context, so no other package can collide with or read the raw key.
//
// The identity-in-context value (user id / Principal) no longer lives here: the
// prior "It lives here (not sdk) by design" note is superseded by amendment A-I1,
// which graduated that vocabulary to sdk/foundation/identity. RequireUser / RequirePrincipal
// now stash identity.Principal via identity.WithPrincipal, and CurrentUser /
// CurrentPrincipal read it via identity.FromContext. Only clientInfo — client
// attribution for audit rows, behavior not identity — remains feature-private.
type contextKey int

const clientInfoKey contextKey = iota

// clientInfo is the request's client attribution — the remote IP and User-Agent.
// It is the single source of truth for both login's rate-limit IP key and the
// security-event audit rows (design §5.1 WI4): written ONCE by the feature's
// HTTP middleware via WithClientInfo, read wherever the service needs it.
type clientInfo struct {
	ip string
	ua string
}

// WithClientInfo returns a copy of ctx carrying the request's client IP and
// User-Agent. It is EXPORTED because the write site lives OUTSIDE authsvc — the
// feature's HTTP middleware (internal/inbound/authentication) sets it over ALL routes,
// unauthenticated ones included, so failed logins, registrations, and OAuth
// callbacks all produce attributed audit rows. It is the ONE write point: login
// and token issuance read their rate-limit IP from the same carrier, and the
// security-event writer reads IP+UA from it (design §5.1 WI4 — one write point,
// one read path; the separate clientIP request plumbing is retired).
func WithClientInfo(ctx context.Context, ip, ua string) context.Context {
	return context.WithValue(ctx, clientInfoKey, clientInfo{ip: ip, ua: ua})
}

// clientInfoFromContext returns the client attribution stashed by WithClientInfo,
// or the zero value (empty IP/UA) when the request carried none.
func clientInfoFromContext(ctx context.Context) clientInfo {
	info, _ := ctx.Value(clientInfoKey).(clientInfo)
	return info
}

// ClientInfoFromContext returns the request's client IP and User-Agent stashed by
// WithClientInfo (empty when none). It is EXPORTED so the sibling invitationsvc
// can attribute its audit rows from the SAME single carrier source as the rest
// of the audit rail (design §5.1 WI4) without re-plumbing IP/UA — a read-only
// utility, not a widening of the authsvc↔invitationsvc coupling (which stays the
// resolveInvitations port; authsvc holds no invitation concern).
func ClientInfoFromContext(ctx context.Context) (ip, ua string) {
	info := clientInfoFromContext(ctx)
	return info.ip, info.ua
}
