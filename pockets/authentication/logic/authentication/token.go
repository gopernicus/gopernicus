package authentication

import (
	"context"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
)

const (
	// tokenClaimUserID is the access-JWT claim carrying the authenticated user's
	// id (§1.1); every access-token resolution reads it.
	tokenClaimUserID = "user_id"
	// tokenClaimSessionID is the access-JWT claim carrying the backing session's
	// app-minted id (§1.1); the Live() tier and the logout fallback read it.
	tokenClaimSessionID = "session_id"
)

// TokenEnabled reports whether the bearer-JWT token endpoint is available. The
// signer is now required (D3), so it is always true on a constructed Service; the
// transport keeps gating POST /auth/token on it for symmetry with the other
// subsystems.
func (s *Service) TokenEnabled() bool {
	return s.tokenSigner != nil
}

// IssueToken authenticates login-shaped credentials and mints a session-backed
// TokenPair — the API-flow twin of Login (§1.1, breaking response-contract change
// from AV6's stateless-only token). It mirrors Login's discipline exactly: it
// rate-limits FIRST on the same (email, client-IP) key BEFORE any credential
// work, returns the same generic sdk.ErrUnauthorized for every credential
// mismatch, and honors RequireVerifiedEmail (ErrEmailNotVerified, 403) AFTER
// password verification so it never leaks a verified/unverified signal.
//
// Identity resolves through the login-enabled email identifier (GetLogin), not the
// legacy email column, and the verified gate reads the identifier's proof state.
// The rate-limit IP is read from the request's client-info carrier (WithClientInfo)
// — the single source of truth for IP (design §5.1 WI4); there is no clientIP
// parameter. A successful issuance records a token_issued success event.
func (s *Service) IssueToken(ctx context.Context, emailAddr, password string) (TokenPair, error) {
	if err := s.requirePasswordFlows(); err != nil {
		return TokenPair{}, err
	}
	normalized, err := s.normalizeEmail(emailAddr)
	if err != nil {
		return TokenPair{}, invalidCredentials()
	}

	u, _, err := s.provePassword(ctx, normalized, password)
	if err != nil {
		return TokenPair{}, err
	}

	pair, err := s.mintSession(ctx, u.ID, u.AuthRevision, s.primaryAuthentication(session.MethodPassword))
	if err != nil {
		// A deactivated account is denied as ordinary bad credentials (CHAU-1.5).
		return TokenPair{}, genericIfNotActive(err, invalidCredentials())
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID: u.ID,
		Type:   securityevent.TypeTokenIssued,
		Status: securityevent.StatusSuccess,
	})
	return pair, nil
}

// verifyBearerClaims verifies an access JWT and extracts both the user id and the
// session id (§1.2/§1.4) — the ONE stateless access-token check. RequirePrincipal
// resolves every access-token credential through it, so a Credential always
// carries the claimed session_id; a Live() gate then looks that id up. A signer
// error (bad signature, expired, malformed) or a missing/blank user_id claim
// denies (ok false); a present token with no session_id yields ok true and an
// empty sessionID (the caller's live lookup then fails closed).
func (s *Service) verifyBearerClaims(raw string) (userID, sessionID string, ok bool) {
	claims, err := s.tokenSigner.Verify(raw)
	if err != nil {
		return "", "", false
	}
	userID, _ = claims[tokenClaimUserID].(string)
	if userID == "" {
		return "", "", false
	}
	sessionID, _ = claims[tokenClaimSessionID].(string)
	return userID, sessionID, true
}
