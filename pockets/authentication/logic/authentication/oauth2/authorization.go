package oauth2

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
)

type consent struct {
	Request           AuthorizationRequest
	UserID, SessionID string
	AuthRevision      int64
	ExpiresAt         time.Time
}

func (s *Service) PrepareAuthorization(ctx context.Context, req AuthorizationRequest, userID, webSessionID string) (Authorization, error) {
	client, err := s.validateAuthorization(ctx, req)
	if err != nil {
		return Authorization{}, err
	}
	owner, err := s.approvingUser(ctx, userID, webSessionID)
	if err != nil {
		return Authorization{}, err
	}
	snapshot := client
	if snapshot.ExpiresAt.IsZero() {
		snapshot.ExpiresAt = s.now().UTC()
	}
	if latest := s.now().UTC().Add(24 * time.Hour); snapshot.ExpiresAt.After(latest) {
		snapshot.ExpiresAt = latest
	}
	if err := s.repos.OAuth2.PutClient(ctx, snapshot); err != nil {
		return Authorization{}, err
	}
	expires := s.now().UTC().Add(s.config.ConsentTTL)
	payload, err := json.Marshal(consent{Request: req, UserID: userID, SessionID: webSessionID, AuthRevision: owner.AuthRevision, ExpiresAt: expires})
	if err != nil {
		return Authorization{}, err
	}
	token, err := s.signer.Sign(map[string]any{"purpose": "oauth2_consent", "consent": string(payload)}, expires)
	if err != nil {
		return Authorization{}, err
	}
	return Authorization{Client: client, UserID: owner.ID, DisplayName: owner.DisplayName, Resource: req.Resource, ConsentToken: token, RedirectURI: req.RedirectURI, ExpiresAt: expires}, nil
}

func (s *Service) Approve(ctx context.Context, consentToken, userID, webSessionID string) (AuthorizationResult, error) {
	approved, err := s.readConsent(ctx, consentToken, userID, webSessionID)
	if err != nil {
		return AuthorizationResult{}, err
	}
	raw := "oauth2_code." + session.NewRefreshToken()
	now := s.now().UTC()
	code := Code{
		Hash: hashSecret(raw), UserID: userID, AuthRevision: approved.AuthRevision,
		Delegation: session.Delegation{
			AuthRevision: approved.AuthRevision, Issuer: s.config.Issuer, ClientID: approved.Request.ClientID,
			Resource: s.config.MCPResource, ExchangeResource: s.config.APIResource, ExchangeClientID: s.config.ConfidentialClientID,
		},
		RedirectURI: approved.Request.RedirectURI, CodeChallenge: approved.Request.CodeChallenge,
		CreatedAt: now, ExpiresAt: now.Add(s.config.CodeTTL),
	}
	if err := s.repos.OAuth2.CreateCode(ctx, code, webSessionID); err != nil {
		return AuthorizationResult{}, err
	}
	s.audit(ctx, TypeConsentApproved, userID, webSessionID, approved.Request.ClientID)
	return AuthorizationResult{RedirectURI: code.RedirectURI, Code: raw, State: approved.Request.State}, nil
}

func (s *Service) Deny(ctx context.Context, consentToken, userID, webSessionID string) (AuthorizationResult, error) {
	approved, err := s.readConsent(ctx, consentToken, userID, webSessionID)
	if err != nil {
		return AuthorizationResult{}, err
	}
	return AuthorizationResult{RedirectURI: approved.Request.RedirectURI, State: approved.Request.State, Error: "access_denied"}, nil
}

func (s *Service) readConsent(ctx context.Context, raw, userID, webSessionID string) (consent, error) {
	if raw == "" || len(raw) > 16<<10 {
		return consent{}, ErrInvalidRequest
	}
	claims, err := s.signer.Verify(raw)
	if err != nil {
		return consent{}, ErrInvalidRequest
	}
	payload, ok := claims["consent"].(string)
	if claims["purpose"] != "oauth2_consent" || !ok {
		return consent{}, ErrInvalidRequest
	}
	var out consent
	if err := json.Unmarshal([]byte(payload), &out); err != nil {
		return consent{}, ErrInvalidRequest
	}
	if out.UserID != userID || out.SessionID != webSessionID || !s.now().Before(out.ExpiresAt) {
		return consent{}, ErrAccessDenied
	}
	owner, err := s.approvingUser(ctx, userID, webSessionID)
	if err != nil {
		return consent{}, err
	}
	if owner.AuthRevision != out.AuthRevision {
		return consent{}, ErrAccessDenied
	}
	if _, err := s.validateAuthorization(ctx, out.Request); err != nil {
		return consent{}, err
	}
	return out, nil
}

func (s *Service) approvingUser(ctx context.Context, userID, webSessionID string) (user.User, error) {
	if userID == "" || webSessionID == "" {
		return user.User{}, ErrAccessDenied
	}
	owner, err := s.repos.Users.Get(ctx, userID)
	if err != nil {
		if errors.Is(err, sdk.ErrNotFound) {
			return user.User{}, ErrAccessDenied
		}
		return user.User{}, err
	}
	if owner.ID != userID || !owner.Active() {
		return user.User{}, ErrAccessDenied
	}
	browser, err := s.repos.Sessions.Get(ctx, webSessionID)
	if err != nil {
		if errors.Is(err, sdk.ErrNotFound) || errors.Is(err, sdk.ErrExpired) {
			return user.User{}, ErrAccessDenied
		}
		return user.User{}, err
	}
	if browser.ID != webSessionID || browser.UserID != userID || !browser.FirstParty() || browser.Expired(s.now()) {
		return user.User{}, ErrAccessDenied
	}
	return owner, nil
}

func (s *Service) validateAuthorization(ctx context.Context, req AuthorizationRequest) (Client, error) {
	if req.Scope != "" {
		return Client{}, ErrInvalidScope
	}
	if req.ResponseType != "code" || req.CodeChallengeMethod != "S256" || len(req.State) > 2048 || !validChallenge(req.CodeChallenge) {
		return Client{}, ErrInvalidRequest
	}
	if req.Resource != s.config.MCPResource {
		return Client{}, ErrInvalidTarget
	}
	client, err := s.resolveClient(ctx, req.ClientID)
	if err != nil {
		return Client{}, err
	}
	if !slices.Contains(client.RedirectURIs, req.RedirectURI) {
		return Client{}, ErrInvalidRequest
	}
	return client, nil
}

func validChallenge(challenge string) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(challenge)
	return err == nil && len(decoded) == sha256.Size && base64.RawURLEncoding.EncodeToString(decoded) == challenge
}

func validVerifier(verifier string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	for _, c := range []byte(verifier) {
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '.' || c == '_' || c == '~' {
			continue
		}
		return false
	}
	return true
}

func matchesPKCE(verifier, challenge string) bool {
	if !validVerifier(verifier) {
		return false
	}
	digest := sha256.Sum256([]byte(verifier))
	actual := base64.RawURLEncoding.EncodeToString(digest[:])
	return subtle.ConstantTimeCompare([]byte(actual), []byte(challenge)) == 1
}
