package authentication

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/sdk/errs"
)

// AuthenticateJWT verifies the JWT signature and extracts claims without
// any database queries.
//
// This is the fast path: it accepts stale revocations for the access token
// lifetime (default 30 minutes). Suitable for read-only endpoints.
//
// Satisfies the httpmid.JWTAuthenticator interface.
func (a *Authenticator) AuthenticateJWT(_ context.Context, accessToken string) (Claims, error) {
	rawClaims, err := a.signer.Verify(accessToken)
	if err != nil {
		return Claims{}, ErrInvalidCredentials
	}

	userID, ok := rawClaims["user_id"].(string)
	if !ok || userID == "" {
		return Claims{}, ErrInvalidCredentials
	}

	return Claims{UserID: userID}, nil
}

// AuthenticateSession verifies the JWT, looks up the session in the database,
// and checks that the user account is active.
//
// Use this for sensitive operations. For read-only endpoints where
// a 30-minute stale window is acceptable, use [AuthenticateJWT] instead.
//
// Satisfies the httpmid.SessionAuthenticator interface.
func (a *Authenticator) AuthenticateSession(ctx context.Context, accessToken string) (User, Session, error) {
	claims, err := a.AuthenticateJWT(ctx, accessToken)
	if err != nil {
		return User{}, Session{}, err
	}

	tokenHash, err := hashToken(accessToken)
	if err != nil {
		return User{}, Session{}, ErrInvalidCredentials
	}
	session, err := a.repositories.sessions.GetByTokenHash(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, errs.ErrNotFound) {
			return User{}, Session{}, ErrSessionNotFound
		}
		return User{}, Session{}, fmt.Errorf("auth authenticate session: lookup: %w", err)
	}

	if time.Now().UTC().After(session.ExpiresAt) {
		return User{}, Session{}, ErrTokenExpired
	}

	if session.UserID != claims.UserID {
		return User{}, Session{}, ErrInvalidCredentials
	}

	user, err := a.repositories.users.Get(ctx, claims.UserID)
	if err != nil {
		return User{}, Session{}, fmt.Errorf("auth authenticate session: lookup user: %w", err)
	}

	if !user.Active {
		return User{}, Session{}, ErrUserInactive
	}

	return user, session, nil
}

// RefreshToken validates a refresh token and issues new access + refresh tokens.
//
// Implements token rotation: the old refresh token is invalidated and a new
// one issued. Implements reuse detection: if a previously rotated token is
// presented again, all sessions for the user are revoked.
func (a *Authenticator) RefreshToken(ctx context.Context, refreshToken string) (*LoginResult, error) {
	refreshHash, err := hashToken(refreshToken)
	if err != nil {
		return nil, ErrInvalidCredentials
	}

	// Look up session by current refresh token hash.
	session, err := a.repositories.sessions.GetByRefreshHash(ctx, refreshHash)
	if err != nil {
		if errors.Is(err, errs.ErrNotFound) {
			// Not found by current hash — check for reuse of a rotated token.
			return nil, a.detectTokenReuse(ctx, refreshHash)
		}
		return nil, fmt.Errorf("auth refresh: lookup: %w", err)
	}

	now := time.Now().UTC()

	if now.After(session.ExpiresAt) {
		return nil, ErrTokenExpired
	}

	// Verify the user is still active.
	user, err := a.repositories.users.Get(ctx, session.UserID)
	if err != nil {
		return nil, fmt.Errorf("auth refresh: lookup user: %w", err)
	}
	if !user.Active {
		return nil, ErrUserInactive
	}

	// Generate new tokens.
	accessToken, err := a.signer.Sign(map[string]any{"user_id": session.UserID}, now.Add(a.config.AccessTokenExpiry))
	if err != nil {
		return nil, fmt.Errorf("auth refresh: sign access token: %w", err)
	}

	newRefresh, err := generateToken()
	if err != nil {
		return nil, fmt.Errorf("auth refresh: generate refresh token: %w", err)
	}

	// Rotate: save new hashes, keep old refresh hash for reuse detection.
	session.TokenHash = mustHashToken(accessToken)
	session.PreviousRefreshHash = session.RefreshTokenHash
	session.RefreshTokenHash = mustHashToken(newRefresh)
	session.RotationCount++
	session.ExpiresAt = now.Add(a.config.RefreshTokenExpiry)

	if err := a.repositories.sessions.Update(ctx, session); err != nil {
		return nil, fmt.Errorf("auth refresh: update session: %w", err)
	}

	a.log.InfoContext(ctx, "tokens refreshed",
		"user_id", session.UserID,
		"session_id", session.SessionID,
		"rotation_count", session.RotationCount,
	)
	a.logSecurityEvent(ctx, session.UserID, SecEventTokenRefresh, SecStatusSuccess, map[string]any{
		"session_id":     session.SessionID,
		"rotation_count": session.RotationCount,
	})

	return &LoginResult{
		User:         user,
		SessionID:    session.SessionID,
		AccessToken:  accessToken,
		RefreshToken: newRefresh,
	}, nil
}

// detectTokenReuse checks if a refresh hash matches a previously rotated
// token. If so, all sessions for that user are revoked (security breach).
func (a *Authenticator) detectTokenReuse(ctx context.Context, refreshHash string) error {
	session, err := a.repositories.sessions.GetByPreviousRefreshHash(ctx, refreshHash)
	if err != nil {
		// Not found as a previous hash either — genuinely invalid token.
		return ErrInvalidCredentials
	}

	// Reuse detected! Revoke all sessions for this user.
	a.log.WarnContext(ctx, "refresh token reuse detected — revoking all sessions",
		"user_id", session.UserID,
		"session_id", session.SessionID,
	)
	a.logSecurityEvent(ctx, session.UserID, SecEventTokenReuse, SecStatusSuspicious, map[string]any{
		"session_id": session.SessionID,
	})

	if err := a.repositories.sessions.DeleteAllForUser(ctx, session.UserID); err != nil {
		a.log.ErrorContext(ctx, "failed to revoke sessions after reuse detection",
			"user_id", session.UserID, "error", err)
	}

	return ErrTokenReuse
}

// Logout deletes a session. The userID is verified at the database layer —
// the session is only deleted if it belongs to the given user.
func (a *Authenticator) Logout(ctx context.Context, userID, sessionID string) error {
	if err := a.repositories.sessions.Delete(ctx, userID, sessionID); err != nil {
		return fmt.Errorf("auth logout: %w", err)
	}
	a.logSecurityEvent(ctx, userID, SecEventLogout, SecStatusSuccess, map[string]any{
		"session_id": sessionID,
	})
	return nil
}

// ---------------------------------------------------------------------------
// Internal: session creation
// ---------------------------------------------------------------------------

// createSession generates tokens and creates a new session for the user.
func (a *Authenticator) createSession(ctx context.Context, user User) (*LoginResult, error) {
	userID := user.UserID

	now := time.Now().UTC()

	// Build JWT claims with standard fields (RFC 7519).
	jti, err := generateToken()
	if err != nil {
		return nil, fmt.Errorf("auth: generate jti: %w", err)
	}
	claims := map[string]any{
		"sub":     userID,
		"user_id": userID, // backward compatibility
		"jti":     jti,
	}
	if a.config.JWTIssuer != "" {
		claims["iss"] = a.config.JWTIssuer
	}
	if a.config.JWTAudience != "" {
		claims["aud"] = a.config.JWTAudience
	}

	accessToken, err := a.signer.Sign(claims, now.Add(a.config.AccessTokenExpiry))
	if err != nil {
		return nil, fmt.Errorf("auth: sign access token: %w", err)
	}

	refreshToken, err := generateToken()
	if err != nil {
		return nil, fmt.Errorf("auth: generate refresh token: %w", err)
	}

	session, err := a.repositories.sessions.Create(ctx, Session{
		UserID:           userID,
		TokenHash:        mustHashToken(accessToken),
		RefreshTokenHash: mustHashToken(refreshToken),
		ExpiresAt:        now.Add(a.config.RefreshTokenExpiry),
	})
	if err != nil {
		return nil, fmt.Errorf("auth: create session: %w", err)
	}

	return &LoginResult{
		User:         user,
		SessionID:    session.SessionID,
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	}, nil
}
