package oauth2

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
)

func (s *Service) RedeemCode(ctx context.Context, req CodeRequest) (TokenResponse, error) {
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
	if !slices.Contains(client.RedirectURIs, req.RedirectURI) {
		return TokenResponse{}, ErrInvalidGrant
	}
	code, err := s.repos.OAuth2.GetCode(ctx, hashSecret(req.Code))
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

func (s *Service) Refresh(ctx context.Context, req RefreshRequest) (TokenResponse, error) {
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
		// Empty NewHash cannot rotate a current token. The repository can still
		// recognize a spent token and commit its connection's revocation.
		_, replayErr := s.repos.OAuth2.RotateRefresh(ctx, rotation)
		if errors.Is(replayErr, ErrRefreshReuse) {
			s.audit(ctx, TypeRefreshReuse, "", "", req.ClientID)
		}
		if replayErr == nil {
			return TokenResponse{}, ErrInvalidGrant
		}
		return TokenResponse{}, invalidGrantError(replayErr)
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
	if _, err := s.repos.OAuth2.RotateRefresh(ctx, rotation); err != nil {
		if errors.Is(err, ErrRefreshReuse) {
			s.audit(ctx, TypeRefreshReuse, sess.UserID, sess.ID, req.ClientID)
		}
		return TokenResponse{}, invalidGrantError(err)
	}
	s.audit(ctx, TypeRefresh, sess.UserID, sess.ID, req.ClientID)
	response.RefreshToken = rawRefresh
	return response, nil
}

func (s *Service) Exchange(ctx context.Context, proof ClientProof, req ExchangeRequest) (TokenResponse, error) {
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
		return Introspection{}, ErrInvalidClient
	}
	token, _, err := s.liveAccess(ctx, raw)
	if err != nil {
		if errors.Is(err, ErrInvalidGrant) || errors.Is(err, ErrInvalidClient) {
			return Introspection{Active: false}, nil
		}
		return Introspection{}, err
	}
	// This client is the MCP resource server; it cannot inspect API-only or
	// another client's tokens merely because their issuer happens to match.
	if token.Audience != s.config.MCPResource || token.OriginClientID != "" || token.ActorID != "" {
		return Introspection{Active: false}, nil
	}
	return Introspection{Active: true, TokenType: "Bearer", Subject: "user:" + token.UserID, UserID: token.UserID,
		SessionID: token.SessionID, Issuer: token.Issuer, Audience: token.Audience, ClientID: token.ClientID, ExpiresAt: token.ExpiresAt.Unix()}, nil
}

func (s *Service) Revoke(ctx context.Context, req RevokeRequest) error {
	if _, err := s.resolveClient(ctx, req.ClientID); err != nil {
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
			return nil
		}
		return err
	}
	if token.ClientID != req.ClientID || token.OriginClientID != "" || token.ActorID != "" || token.Audience != s.config.MCPResource {
		return nil
	}
	err = s.repos.OAuth2.RevokeGrant(ctx, sess.ID, sess.UserID, req.ClientID)
	if err == nil {
		s.audit(ctx, TypeRevocationRequested, sess.UserID, sess.ID, req.ClientID)
	}
	return err
}
