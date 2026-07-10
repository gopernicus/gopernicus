package authsvc

import (
	"context"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/securityevent"
	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
)

// tokenClaimUserID is the JWT claim carrying the authenticated user's id — the
// only claim POST /auth/token sets (design §4.4; the signer adds exp/iat).
const tokenClaimUserID = "user_id"

// TokenEnabled reports whether stateless bearer-JWT mode is wired (design §4.4).
// The transport registers POST /auth/token only when it is true (deny-by-
// absence), and the bearer-JWT arm of RequireUser/RequirePrincipal is inert
// otherwise.
func (s *Service) TokenEnabled() bool {
	return s.tokenSigner != nil
}

// IssueToken authenticates login-shaped credentials and mints a short-TTL bearer
// JWT carrying {user_id} (design §4.4, AV6 — stateless user tokens, no refresh).
// It mirrors Login's discipline exactly: it rate-limits FIRST on the same
// (email, client-IP) key BEFORE any credential work, returns the same generic
// sdk.ErrUnauthorized for every credential mismatch, and honors
// RequireVerifiedEmail (ErrEmailNotVerified, 403) AFTER password verification so
// it never leaks a verified/unverified signal. It returns the signed token and
// its absolute expiry.
//
// The rate-limit IP is read from the request's client-info carrier (WithClientInfo)
// — the single source of truth for IP (design §5.1 WI4); there is no clientIP
// parameter. A successful issuance records a token_issued success event.
func (s *Service) IssueToken(ctx context.Context, emailAddr, password string) (string, time.Time, error) {
	if s.tokenSigner == nil {
		return "", time.Time{}, invalidCredentials()
	}
	clientIP := clientInfoFromContext(ctx).ip
	normalized, err := user.NormalizeEmail(emailAddr)
	if err != nil {
		return "", time.Time{}, invalidCredentials()
	}

	res, err := s.limiter.Allow(ctx, loginKey(normalized, clientIP), ratelimiter.PerMinute(loginAttemptsPerMinute))
	if err != nil {
		return "", time.Time{}, err
	}
	if !res.Allowed {
		return "", time.Time{}, ErrRateLimited
	}

	u, err := s.users.GetByEmail(ctx, normalized)
	if err != nil {
		return "", time.Time{}, invalidCredentials()
	}
	hash, err := s.passwords.Get(ctx, u.ID)
	if err != nil {
		return "", time.Time{}, invalidCredentials()
	}
	if err := s.hasher.VerifyPassword(hash, password); err != nil {
		return "", time.Time{}, invalidCredentials()
	}
	if s.requireVerifiedEmail && !u.EmailVerified {
		return "", time.Time{}, ErrEmailNotVerified
	}

	expiresAt := s.now().Add(s.tokenTTL)
	token, err := s.tokenSigner.Sign(map[string]any{tokenClaimUserID: u.ID}, expiresAt)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign token: %w", err)
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID: u.ID,
		Type:   securityevent.TypeTokenIssued,
		Status: securityevent.StatusSuccess,
	})
	return token, expiresAt, nil
}

// verifyBearer verifies a JWT-shaped bearer and extracts its user id. A signer
// error (bad signature, expired, malformed) or a missing/blank user_id claim
// denies (design §4.4). The caller has already confirmed a TokenSigner is wired.
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
