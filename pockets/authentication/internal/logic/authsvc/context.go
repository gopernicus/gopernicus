package authsvc

import "context"

// contextKey is an unexported type for the client-attribution value stashed on a
// request context, so no other package can collide with or read the raw key.
//
// The identity-in-context value (user id / Principal) no longer lives here: the
// prior "It lives here (not sdk) by design" note is superseded by amendment A-I1,
// which graduated that vocabulary to sdk/foundation/identity. RequirePrincipal
// now stashes identity.Principal via identity.WithPrincipal, and CurrentUser /
// CurrentPrincipal read it via identity.FromContext. What stays pocket-private is
// the pocket's own request-scoped vocabulary: clientInfo (client attribution for
// audit rows), the live session id, and the Credential the caller presented.
type contextKey int

const (
	clientInfoKey contextKey = iota
	// sessionIDKey carries the live session's app-minted id stashed by a Live()
	// authenticator so a sensitive-mutation handler can bind a step-up grant or
	// its consume to that exact session (design §5.0). Pocket-private, like
	// clientInfo: session id is request-scoped behavior, not cross-pocket identity.
	sessionIDKey
	// credentialKey carries the Credential the request authenticated with, stashed
	// by RequirePrincipal beside the Principal. Pocket-private, like clientInfo and
	// sessionID: a credential is the pocket's own proof vocabulary, not the
	// cross-pocket identity sdk/foundation/identity carries.
	credentialKey
)

// withSessionID returns a copy of ctx carrying the live session's id. It is set by
// a Live() authenticator once it validates the session, so a handler
// downstream reads the session the caller actually authenticated with rather than
// trusting a body field.
func withSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, sessionIDKey, sessionID)
}

// CurrentSessionID returns the live session id stashed by a Live() authenticator, or
// ("", false) when the request was not gated by it (or was authenticated by a
// session-less machine credential). Sensitive step-up flows read it so a grant is
// always bound to the caller's proven live session.
func (s *Service) CurrentSessionID(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(sessionIDKey).(string)
	return id, ok && id != ""
}

// withCredential returns a copy of ctx carrying the credential the request
// authenticated with. RequirePrincipal writes it once, at the outermost position
// that resolved the request; a nested RequirePrincipal READS it to narrow rather
// than resolving the request a second time.
func withCredential(ctx context.Context, cred Credential) context.Context {
	return context.WithValue(ctx, credentialKey, cred)
}

// currentCredential reads the stashed credential — the nested-narrowing input.
func currentCredential(ctx context.Context) (Credential, bool) {
	cred, ok := ctx.Value(credentialKey).(Credential)
	return cred, ok && cred.Kind != ""
}

// CurrentCredential returns what authenticated the request — the credential kind,
// its transport, and the coordinates of the proof (session id, or key / service
// account / act-as-user for a key) — or false when the request was not gated by
// RequirePrincipal. A handler reads it to tell an act-as-user API key from a
// person's session, which CurrentPrincipal alone cannot distinguish.
func (s *Service) CurrentCredential(ctx context.Context) (Credential, bool) {
	return currentCredential(ctx)
}

// clientInfo is the request's client attribution — the remote IP and User-Agent.
// It is the single source of truth for both login's rate-limit IP key and the
// security-event audit rows (design §5.1 WI4): written ONCE by the pocket's
// HTTP middleware via WithClientInfo, read wherever the service needs it.
type clientInfo struct {
	ip string
	ua string
}

// WithClientInfo returns a copy of ctx carrying the request's client IP and
// User-Agent. It is EXPORTED because the write site lives OUTSIDE authsvc — the
// pocket's HTTP middleware (internal/inbound/authentication) sets it over ALL routes,
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
