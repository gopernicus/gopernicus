package authsvc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/features/authentication/domain/securityevent"
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
)

const (
	// tokenClaimUserID is the access-JWT claim carrying the authenticated user's
	// id (§1.1); RequireUser reads it.
	tokenClaimUserID = "user_id"
	// tokenClaimSessionID is the access-JWT claim carrying the backing session's
	// app-minted id (§1.1); RequireLiveSession and the logout fallback read it.
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
	clientIP := clientInfoFromContext(ctx).ip
	normalized, err := s.normalizeEmail(emailAddr)
	if err != nil {
		return TokenPair{}, invalidCredentials()
	}

	res, err := s.limiter.Allow(ctx, s.loginKey(string(identifier.KindEmail), normalized, clientIP), ratelimiter.PerMinute(loginAttemptsPerMinute))
	if err != nil {
		return TokenPair{}, err
	}
	if !res.Allowed {
		return TokenPair{}, ErrRateLimited
	}

	ident, err := s.identifiers.GetLogin(ctx, string(identifier.KindEmail), normalized)
	if err != nil {
		return TokenPair{}, invalidCredentials()
	}
	u, err := s.users.Get(ctx, ident.UserID)
	if err != nil {
		return TokenPair{}, invalidCredentials()
	}
	hash, err := s.passwords.Get(ctx, u.ID)
	if err != nil {
		return TokenPair{}, invalidCredentials()
	}
	if err := s.hasher.VerifyPassword(hash, password); err != nil {
		return TokenPair{}, invalidCredentials()
	}
	if s.requireVerifiedEmail && !ident.Verified() {
		return TokenPair{}, ErrEmailNotVerified
	}

	pair, err := s.mintSession(ctx, u.ID, s.primaryAuthentication(session.MethodPassword))
	if err != nil {
		return TokenPair{}, err
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID: u.ID,
		Type:   securityevent.TypeTokenIssued,
		Status: securityevent.StatusSuccess,
	})
	return pair, nil
}

// verifyBearer verifies an access JWT and extracts its user id — the stateless
// RequireUser check (§1.2). A signer error (bad signature, expired, malformed) or
// a missing/blank user_id claim denies. It stays user_id-only; the session_id
// reader below serves RequireLiveSession and the logout fallback.
func (s *Service) verifyBearer(raw string) (string, bool) {
	claims, err := s.tokenSigner.Verify(raw)
	if err != nil {
		return "", false
	}
	userID, ok := claims[tokenClaimUserID].(string)
	if !ok || userID == "" {
		return "", false
	}
	return userID, true
}

// verifyBearerClaims verifies an access JWT and extracts both the user id and the
// session id (§1.4). RequireLiveSession uses it: the verified session_id is then
// looked up live. A signer error or a missing/blank user_id claim denies (ok
// false); a present token with no session_id yields ok true and an empty
// sessionID (the caller's live lookup then fails closed).
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

// sessionIDIgnoringExpiry reads the session_id claim from an access JWT WITHOUT
// verifying its signature or expiry — the logout fallback lane (§1.5), where an
// expired access JWT must still surrender its session_id so logout is never a
// no-op. It base64url-decodes the payload segment and reads session_id. The
// result is used SOLELY to target a Delete; the access credential it carries is
// never trusted from this path, so skipping verification here does not authorize
// anything. A malformed token yields "".
func sessionIDIgnoringExpiry(raw string) string {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	sid, _ := claims[tokenClaimSessionID].(string)
	return sid
}
