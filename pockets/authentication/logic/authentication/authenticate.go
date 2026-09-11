package authentication

import "context"

// Authenticate verifies the presented proof before recording its principal and
// credential in the returned context. Kind and transport describe what the host
// accepts; they never substitute for verifying the supplied secret. Callers may
// use this operation from their own transport adapters.
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
	return s.withAuthenticatedPrincipal(ctx, principal, credential), true
}

// RequireLive verifies the live session behind a previously authenticated proof.
// API keys already checked their backing row during authentication. A context
// without this service's credential proof always fails.
func (s *Service) RequireLive(ctx context.Context) (context.Context, bool) {
	cred, ok := s.CurrentCredential(ctx)
	if !ok || ctx.Err() != nil {
		return ctx, false
	}
	return s.enforceCredentialLiveness(ctx, cred)
}
