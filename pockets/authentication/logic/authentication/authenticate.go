package authentication

import (
	"context"
	"strings"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
)

// Authenticate verifies the presented proof before recording its principal and
// credential in the returned context. Kind and transport describe what the host
// accepts; they never substitute for verifying the supplied secret. Callers may
// use this operation from their own transport adapters. Success establishes
// credential proof, not admission to the transport's intended resource: custom
// adapters must require the expected profile and audience. Delegated proof always
// checks a live grant; each new operation on a persistent transport must call
// RequireLive again. The HTTP adapter supplies these checks through Audience.
// Errors match sdk.ErrUnauthorized (deny) or sdk.ErrUnavailable (cannot decide).
func (s *Service) Authenticate(ctx context.Context, kind CredentialKind, transport Transport, raw string) (context.Context, error) {
	ctx = clearCredential(ctx)
	if ctx.Err() != nil || (transport != TransportHeader && transport != TransportCookie) {
		return ctx, errCredentialRejected
	}
	var principal Principal
	var credential Credential
	var ok bool
	switch kind {
	case CredentialAccessToken:
		principal, credential, ok = s.verifyAccessTokenCredential(raw, transport)
	case CredentialAPIKey:
		if transport != TransportHeader || !s.MachineEnabled() {
			return ctx, errCredentialRejected
		}
		principal, credential, ok = s.verifyAPIKeyCredential(ctx, raw)
	case CredentialExternalToken:
		if s.external == nil {
			return ctx, errCredentialRejected
		}
		var err error
		if principal, credential, err = s.external.Verify(ctx, raw, transport); err != nil {
			return ctx, err
		}
		ok = true
	default:
		return ctx, errCredentialRejected
	}
	if !ok || ctx.Err() != nil {
		return ctx, errCredentialRejected
	}
	ctx = s.withAuthenticatedPrincipal(ctx, principal, credential)
	if credential.Profile == session.ProfileDelegated {
		return s.enforceCredentialLiveness(ctx, credential, LivenessLive, true)
	}
	return ctx, nil
}

// RequireLive applies tier to a previously authenticated proof; unsafe reports a
// mutating request. API keys already checked their backing row during
// authentication. A context without this service's credential proof always
// fails. Delegated proof is rechecked on every call, whatever the tier,
// including its access-token expiry. Errors match sdk.ErrUnauthorized (deny) or
// sdk.ErrUnavailable (cannot decide).
func (s *Service) RequireLive(ctx context.Context, tier LivenessTier, unsafe bool) (context.Context, error) {
	cred, ok := s.CurrentCredential(ctx)
	if !ok || ctx.Err() != nil {
		return clearCredential(ctx), errNotLive
	}
	return s.enforceCredentialLiveness(ctx, cred, tier, unsafe)
}

// ClassifyBearer names the kind a JWT-shaped bearer claims from its UNVERIFIED
// iss, so the caller verifies it with exactly one verifier: a wired provider's
// issuer is an external token; every other JWT-shaped bearer is an access token,
// left to the host's TokenSigner to accept or refuse. That keeps tokens from a
// host signer that sets its own iss, or whose payload is not JSON, working as
// they did before external providers existed. It proves nothing.
func (s *Service) ClassifyBearer(raw string) (CredentialKind, bool) {
	if strings.Count(raw, ".") != 2 {
		return "", false
	}
	if iss, present, ok := peekIssuer(raw); ok && present && s.external != nil && s.external.wired(iss) {
		return CredentialExternalToken, true
	}
	return CredentialAccessToken, true
}

// ExternalEnabled reports whether external identity providers are wired.
func (s *Service) ExternalEnabled() bool {
	return s.external != nil
}

// ExternalAuthenticator returns the embedded external identity engine, or nil
// when no provider is wired.
func (s *Service) ExternalAuthenticator() *ExternalAuthenticator {
	return s.external
}
