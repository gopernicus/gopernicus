package authentication

import (
	"context"
	"slices"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

// contextKey is an unexported type for the client-attribution value stashed on a
// request context, so no other package can collide with or read the raw key.
//
// The identity-in-context value (user id / Principal) no longer lives here: the
// prior "It lives here (not sdk) by design" note is superseded by amendment A-I1,
// which graduated that vocabulary to sdk. RequirePrincipal
// now stashes sdk.Principal via sdk.WithPrincipal, and CurrentUser /
// CurrentPrincipal read it via sdk.PrincipalFromContext. What stays pocket-private is
// the pocket's own request-scoped vocabulary: clientInfo (client attribution for
// audit rows), the live session id, and the Credential the caller presented.
type contextKey int

const (
	clientInfoKey contextKey = iota
	credentialKey
)

// credentialProof records the authority and principal that verified a credential.
// A live session belongs only to this exact proof; copying a context to another
// service or replacing its credential cannot carry liveness into another store.
// owner is a *Service or a standalone *ExternalAuthenticator. externalLiveAt
// stamps a passed external re-check so a nested gate does not repeat it.
type credentialProof struct {
	owner          any
	principal      Principal
	credential     Credential
	liveSessionID  string
	externalLiveAt time.Time
}

func proofFor(ctx context.Context, owner any) (credentialProof, bool) {
	proof, ok := ctx.Value(credentialKey).(credentialProof)
	principal, hasPrincipal := sdk.PrincipalFromContext(ctx)
	return proof, ok && owner != nil && proof.owner == owner && proof.credential.Kind != "" && hasPrincipal && principal == proof.principal
}

func (s *Service) currentProof(ctx context.Context) (credentialProof, bool) {
	return proofFor(ctx, s)
}

func (s *Service) withSessionID(ctx context.Context, id string) context.Context {
	proof, _ := s.currentProof(ctx)
	proof.liveSessionID = id
	return context.WithValue(ctx, credentialKey, proof)
}

// CurrentSessionID returns the live session bound to this service's verified proof.
// Stateless credentials and session-less machine credentials return false.
func (s *Service) CurrentSessionID(ctx context.Context) (string, bool) {
	proof, ok := s.currentProof(ctx)
	if !ok || proof.liveSessionID == "" || proof.liveSessionID != proof.credential.SessionID {
		return "", false
	}
	return proof.liveSessionID, true
}

// CurrentCredential returns the credential verified by this service. A context
// from a different service or a host-stamped principal alone carries no proof.
func (s *Service) CurrentCredential(ctx context.Context) (Credential, bool) {
	proof, ok := s.currentProof(ctx)
	if !ok {
		return Credential{}, false
	}
	return proof.credential.clone(), true
}

func (c Credential) clone() Credential {
	c.Audiences = slices.Clone(c.Audiences)
	c.Identity = c.Identity.Clone()
	return c
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
// User-Agent. It is EXPORTED because the write site lives OUTSIDE authentication service — the
// pocket's HTTP middleware (inbound/http) sets it over the pocket's routes,
// unauthenticated ones included, and every host route behind
// ClientInfoMiddleware, so failed logins, registrations, OAuth callbacks and
// host-route denials all produce attributed audit rows. It is the ONE write point: login
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
// WithClientInfo (empty when none). It is EXPORTED so the sibling invitation service
// can attribute its audit rows from the SAME single carrier source as the rest
// of the audit rail (design §5.1 WI4) without re-plumbing IP/UA — a read-only
// utility, not a widening of the authentication service↔invitation service coupling (which stays the
// resolveInvitations port; authentication service holds no invitation concern).
func ClientInfoFromContext(ctx context.Context) (ip, ua string) {
	info := clientInfoFromContext(ctx)
	return info.ip, info.ua
}

// clearCredential prevents even a failed replacement from exposing earlier proof
// through the returned context. The caller's original context stays unchanged.
func clearCredential(ctx context.Context) context.Context {
	return context.WithValue(sdk.WithPrincipal(ctx, Principal{}), credentialKey, credentialProof{})
}

func (s *Service) withAuthenticatedPrincipal(ctx context.Context, principal Principal, cred Credential) context.Context {
	return withProof(ctx, s, principal, cred)
}

func withProof(ctx context.Context, owner any, principal Principal, cred Credential) context.Context {
	return context.WithValue(sdk.WithPrincipal(ctx, principal), credentialKey, credentialProof{owner: owner, principal: principal, credential: cred.clone()})
}

// withExternalLive stamps the current proof as externally re-checked at at.
func withExternalLive(ctx context.Context, proof credentialProof, at time.Time) context.Context {
	proof.externalLiveAt = at
	return context.WithValue(ctx, credentialKey, proof)
}
