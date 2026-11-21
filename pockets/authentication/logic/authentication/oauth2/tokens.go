package oauth2

import (
	"context"
	"crypto/subtle"
	"errors"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
)

func (s *Service) RedeemCode(ctx context.Context, req CodeRequest) (_ TokenResponse, err error) {
	defer func() { s.auditGrantFailure(ctx, req.ClientID, err) }()
	if req.Code == "" || len(req.Code) > 512 || !validVerifier(req.CodeVerifier) {
		return TokenResponse{}, ErrInvalidGrant
	}
	if req.Resource != s.config.MCPResource {
		return TokenResponse{}, ErrInvalidTarget
	}
	client, err := s.resolveClient(ctx, req.ClientID)
	if err != nil {
		return TokenResponse{}, err
	}
	if !matchRedirect(client.RedirectURIs, req.RedirectURI) {
		return TokenResponse{}, ErrInvalidGrant
	}
	getCode := s.repos.OAuth2.GetCode
	if s.codeAuth != nil {
		getCode = s.codeAuth.GetCodeWithAuthentication
	}
	code, err := getCode(ctx, hashSecret(req.Code))
	if err != nil {
		return TokenResponse{}, invalidGrantError(err)
	}
	now := s.now().UTC()
	if code.Delegation.ClientID != req.ClientID || code.RedirectURI != req.RedirectURI || code.Delegation.Resource != req.Resource ||
		!s.validDelegation(code.Delegation) || code.AuthRevision != code.Delegation.AuthRevision || !now.Before(code.ExpiresAt) || !matchesPKCE(req.CodeVerifier, code.CodeChallenge) {
		return TokenResponse{}, ErrInvalidGrant
	}
	proposed, _ := session.NewSession(code.UserID, s.config.SessionTTL, now)
	proposed.Profile = session.ProfileDelegated
	proposed.Delegation = code.Delegation
	proposed.Authentication = code.Authentication
	rawRefresh := session.NewDelegatedRefreshToken()
	proposed.RefreshTokenHash = hashSecret(rawRefresh)
	response, err := s.signAccess(proposed, false, time.Time{})
	if err != nil {
		return TokenResponse{}, err
	}
	if _, err := s.repos.OAuth2.RedeemCode(ctx, code, proposed, now); err != nil {
		return TokenResponse{}, invalidGrantError(err)
	}
	s.audit(ctx, TypeConnectionCreated, proposed.UserID, proposed.ID, req.ClientID)
	response.RefreshToken = rawRefresh
	return response, nil
}

func (s *Service) Refresh(ctx context.Context, req RefreshRequest) (_ TokenResponse, err error) {
	defer func() { s.auditGrantFailure(ctx, req.ClientID, err) }()
	if !session.IsDelegatedRefreshToken(req.RefreshToken) || len(req.RefreshToken) > 512 {
		return TokenResponse{}, ErrInvalidGrant
	}
	if req.Resource != s.config.MCPResource {
		return TokenResponse{}, ErrInvalidTarget
	}
	if _, err := s.resolveClient(ctx, req.ClientID); err != nil {
		return TokenResponse{}, err
	}
	hash := hashSecret(req.RefreshToken)
	rotation := Refresh{Hash: hash, ClientID: req.ClientID, Resource: req.Resource, Now: s.now().UTC()}
	sess, _, err := s.repos.Sessions.GetByRefreshHash(ctx, hash)
	if errors.Is(err, sdk.ErrNotFound) {
		return s.spentRefresh(ctx, req, rotation, "", "")
	}
	if err != nil {
		return TokenResponse{}, invalidGrantError(err)
	}
	if sess.Profile != session.ProfileDelegated || !s.validDelegation(sess.Delegation) || sess.Delegation.ClientID != req.ClientID || sess.Delegation.Resource != req.Resource || sess.Expired(rotation.Now) || sess.RefreshTokenHash != hash {
		return TokenResponse{}, ErrInvalidGrant
	}
	rawRefresh := session.NewDelegatedRefreshToken()
	rotation.NewHash = hashSecret(rawRefresh)
	response, err := s.signAccess(sess, false, time.Time{})
	if err != nil {
		return TokenResponse{}, err
	}
	rotate := s.repos.OAuth2.RotateRefresh
	if s.grace != nil {
		successor, err := s.config.RefreshReuseEncrypter.Encrypt(rawRefresh)
		if err != nil {
			return TokenResponse{}, err
		}
		rotate = func(ctx context.Context, rotation Refresh) (session.Session, error) {
			return s.grace.RotateRefreshWithSuccessor(ctx, rotation, successor)
		}
	}
	if _, err := rotate(ctx, rotation); err != nil {
		if errors.Is(err, ErrRefreshSpent) {
			// Lost a concurrent rotation: the grace may answer; otherwise the
			// ordinary spent-token path revokes.
			return s.spentRefresh(ctx, req, rotation, sess.UserID, sess.ID)
		}
		if errors.Is(err, ErrRefreshReuse) {
			s.audit(ctx, TypeRefreshReuse, sess.UserID, sess.ID, req.ClientID)
		}
		return TokenResponse{}, invalidGrantError(err)
	}
	s.audit(ctx, TypeRefresh, sess.UserID, sess.ID, req.ClientID)
	response.RefreshToken = rawRefresh
	return response, nil
}

// spentRefresh handles a presented token that is not current: inside the
// refresh-reuse grace it may get the rotation already issued; otherwise an
// empty-NewHash RotateRefresh, which cannot rotate a current token, lets the
// repository recognize a spent token and commit its connection's revocation.
func (s *Service) spentRefresh(ctx context.Context, req RefreshRequest, rotation Refresh, userID, sessionID string) (TokenResponse, error) {
	if s.grace != nil {
		if response, ok := s.graceRefresh(ctx, req, rotation.Hash, rotation.Now); ok {
			return response, nil
		}
	}
	rotation.NewHash = ""
	_, replayErr := s.repos.OAuth2.RotateRefresh(ctx, rotation)
	if errors.Is(replayErr, ErrRefreshReuse) {
		s.audit(ctx, TypeRefreshReuse, userID, sessionID, req.ClientID)
	}
	if replayErr == nil {
		return TokenResponse{}, ErrInvalidGrant
	}
	return TokenResponse{}, invalidGrantError(replayErr)
}

// graceRefresh answers a refresh of a spent token inside
// Config.RefreshReuseGrace with the rotation already issued: a fresh access
// token and the stored successor refresh token. It qualifies only the
// immediate predecessor (the successor is still the session's current token)
// of a live delegated session on the same client and resource, and writes
// nothing. ok=false sends the caller down the ordinary spent-token path, which
// revokes.
func (s *Service) graceRefresh(ctx context.Context, req RefreshRequest, hash string, now time.Time) (TokenResponse, bool) {
	spent, err := s.grace.GetRefreshSuccessor(ctx, hash)
	if err != nil || spent.Ciphertext == "" || spent.RotatedAt.IsZero() || !now.Before(spent.RotatedAt.Add(s.config.RefreshReuseGrace)) {
		return TokenResponse{}, false
	}
	successor, err := s.config.RefreshReuseEncrypter.Decrypt(spent.Ciphertext)
	if err != nil || !session.IsDelegatedRefreshToken(successor) {
		return TokenResponse{}, false
	}
	sess, err := s.repos.Sessions.Get(ctx, spent.SessionID)
	if err != nil || sess.ID != spent.SessionID || sess.Profile != session.ProfileDelegated || !s.validDelegation(sess.Delegation) ||
		sess.Delegation.ClientID != req.ClientID || sess.Delegation.Resource != req.Resource || sess.Expired(now) ||
		subtle.ConstantTimeCompare([]byte(sess.RefreshTokenHash), []byte(hashSecret(successor))) != 1 {
		return TokenResponse{}, false
	}
	response, err := s.signAccess(sess, false, time.Time{})
	if err != nil {
		return TokenResponse{}, false
	}
	s.auditReason(ctx, TypeRefresh, sess.UserID, sess.ID, req.ClientID, "grace_window")
	response.RefreshToken = successor
	return response, true
}

func (s *Service) Exchange(ctx context.Context, proof ClientProof, req ExchangeRequest) (_ TokenResponse, err error) {
	defer func() { s.auditGrantFailure(ctx, proof.clientID, err) }()
	if !s.validProof(proof) {
		return TokenResponse{}, ErrInvalidClient
	}
	if req.Scope != "" {
		return TokenResponse{}, ErrInvalidScope
	}
	if req.Resource != s.config.APIResource || req.Audience != "" {
		return TokenResponse{}, ErrInvalidTarget
	}
	if req.SubjectTokenType != AccessTokenType || (req.RequestedTokenType != "" && req.RequestedTokenType != AccessTokenType) || req.ActorToken != "" || req.SubjectToken == "" {
		return TokenResponse{}, ErrInvalidRequest
	}
	token, sess, err := s.liveAccess(ctx, req.SubjectToken)
	if err != nil {
		return TokenResponse{}, err
	}
	if token.Audience != s.config.MCPResource || token.OriginClientID != "" || token.ActorID != "" || sess.Delegation.ExchangeClientID != proof.clientID {
		return TokenResponse{}, ErrInvalidGrant
	}
	response, err := s.signAccess(sess, true, token.ExpiresAt)
	if err == nil {
		s.audit(ctx, TypeTokenExchanged, sess.UserID, sess.ID, proof.clientID)
	}
	return response, err
}

func (s *Service) Introspect(ctx context.Context, proof ClientProof, raw string) (Introspection, error) {
	if !s.validProof(proof) {
		s.auditGrantFailure(ctx, proof.clientID, ErrInvalidClient)
		return Introspection{}, ErrInvalidClient
	}
	token, sess, err := s.liveAccess(ctx, raw)
	if err != nil {
		if errors.Is(err, ErrInvalidGrant) || errors.Is(err, ErrInvalidClient) {
			s.auditGrantFailure(ctx, proof.clientID, err)
			return Introspection{Active: false}, nil
		}
		return Introspection{}, err
	}
	// This client is the MCP resource server; it cannot inspect API-only or
	// another client's tokens merely because their issuer happens to match.
	if token.Audience != s.config.MCPResource || token.OriginClientID != "" || token.ActorID != "" {
		s.auditGrantFailure(ctx, proof.clientID, ErrInvalidGrant)
		return Introspection{Active: false}, nil
	}
	out := Introspection{Active: true, TokenType: "Bearer", Subject: "user:" + token.UserID, UserID: token.UserID,
		SessionID: token.SessionID, Issuer: token.Issuer, Audience: token.Audience, ClientID: token.ClientID, ExpiresAt: token.ExpiresAt.Unix()}
	if level, kinds, ok := s.recordedAssurance(sess); ok {
		out.Assurance, out.Methods = level, kinds
	}
	return out, nil
}

func (s *Service) Revoke(ctx context.Context, req RevokeRequest) error {
	if _, err := s.resolveClient(ctx, req.ClientID); err != nil {
		s.auditGrantFailure(ctx, req.ClientID, err)
		return err
	}
	if req.Token == "" || len(req.Token) > 16<<10 {
		return nil
	}
	if session.IsDelegatedRefreshToken(req.Token) {
		err := s.repos.OAuth2.RevokeRefresh(ctx, hashSecret(req.Token), req.ClientID)
		if err == nil {
			s.audit(ctx, TypeRevocationRequested, "", "", req.ClientID)
		}
		return err
	}
	token, sess, err := s.liveAccess(ctx, req.Token)
	if err != nil {
		if errors.Is(err, ErrInvalidGrant) || errors.Is(err, ErrInvalidClient) {
			s.auditGrantFailure(ctx, req.ClientID, err)
			return nil
		}
		return err
	}
	if token.ClientID != req.ClientID || token.OriginClientID != "" || token.ActorID != "" || token.Audience != s.config.MCPResource {
		s.auditGrantFailure(ctx, req.ClientID, ErrInvalidGrant)
		return nil
	}
	err = s.repos.OAuth2.RevokeGrant(ctx, sess.ID, sess.UserID, req.ClientID)
	if err == nil {
		s.audit(ctx, TypeRevocationRequested, sess.UserID, sess.ID, req.ClientID)
	}
	return err
}
