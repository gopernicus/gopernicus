package authentication

import (
	"context"

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
func (s *Service) Authenticate(ctx context.Context, kind CredentialKind, transport Transport, raw string) (context.Context, bool) {
	ctx = clearCredential(ctx)
	if ctx.Err() != nil || (transport != TransportHeader && transport != TransportCookie) {
		return ctx, false
	}
	var principal Principal
	var credential Credential
	var ok bool
	switch kind {
	case CredentialAccessToken:
		principal, credential, ok = s.verifyAccessTokenCredential(raw, transport)
	case CredentialAPIKey:
		if transport != TransportHeader || !s.MachineEnabled() {
			return ctx, false
		}
		principal, credential, ok = s.verifyAPIKeyCredential(ctx, raw)
	default:
		return ctx, false
	}
	if !ok || ctx.Err() != nil {
		return ctx, false
	}
	ctx = s.withAuthenticatedPrincipal(ctx, principal, credential)
	if credential.Profile == session.ProfileDelegated {
		return s.enforceCredentialLiveness(ctx, credential)
	}
	return ctx, true
}

// RequireLive verifies the live session behind a previously authenticated proof.
// API keys already checked their backing row during authentication. A context
// without this service's credential proof always fails.
// Delegated proof is rechecked on every call, including its access-token expiry.
func (s *Service) RequireLive(ctx context.Context) (context.Context, bool) {
	cred, ok := s.CurrentCredential(ctx)
	if !ok || ctx.Err() != nil {
		return clearCredential(ctx), false
	}
	return s.enforceCredentialLiveness(ctx, cred)
}
