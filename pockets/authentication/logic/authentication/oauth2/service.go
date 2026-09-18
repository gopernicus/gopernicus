package oauth2

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

type Service struct {
	repos    Repositories
	signer   cryptids.JWTSigner
	resolver ClientResolver
	verifier TokenVerifier
	config   Config
	now      func() time.Time
	logger   *slog.Logger
}

func New(repos Repositories, signer cryptids.JWTSigner, resolver ClientResolver, verifier TokenVerifier, mode environment.Mode, cfg Config, opts ...Option) (*Service, error) {
	if err := environment.ValidateMode(mode); err != nil {
		return nil, err
	}
	for _, d := range []struct {
		name  string
		value any
	}{
		{"OAuth2", repos.OAuth2}, {"Users", repos.Users}, {"Sessions", repos.Sessions}, {"Signer", signer}, {"ClientResolver", resolver}, {"TokenVerifier", verifier},
	} {
		if nilDependency(d.value) {
			return nil, fmt.Errorf("oauth2: %s is required: %w", d.name, sdk.ErrInvalidInput)
		}
	}
	o := serviceOptions{now: time.Now, logger: slog.Default()}
	for _, option := range opts {
		if option == nil {
			return nil, fmt.Errorf("oauth2: nil option: %w", sdk.ErrInvalidInput)
		}
		option(&o)
	}
	if o.now == nil {
		return nil, fmt.Errorf("oauth2: clock is required: %w", sdk.ErrInvalidInput)
	}
	if o.logger == nil || (repos.SecurityEvents != nil && nilDependency(repos.SecurityEvents)) {
		return nil, fmt.Errorf("oauth2: nil logger or typed-nil audit repository: %w", sdk.ErrInvalidInput)
	}
	if cfg.AllowLocalHTTP && mode != environment.ModeDevelopment {
		return nil, fmt.Errorf("oauth2: local HTTP requires development mode: %w", sdk.ErrInvalidInput)
	}
	for _, raw := range []string{cfg.Issuer, cfg.MCPResource, cfg.APIResource} {
		if !validEndpoint(raw, cfg.AllowLocalHTTP, false) {
			return nil, fmt.Errorf("oauth2: invalid canonical issuer/resource URL: %w", sdk.ErrInvalidInput)
		}
	}
	if cfg.MCPResource == cfg.APIResource || strings.TrimSpace(cfg.ConfidentialClientID) != cfg.ConfidentialClientID || cfg.ConfidentialClientID == "" || len(cfg.ConfidentialSecretHashes) == 0 {
		return nil, fmt.Errorf("oauth2: distinct resources and confidential client credentials required: %w", sdk.ErrInvalidInput)
	}
	for _, h := range cfg.ConfidentialSecretHashes {
		if h == ([32]byte{}) || h == sha256.Sum256(nil) {
			return nil, fmt.Errorf("oauth2: empty confidential secret: %w", sdk.ErrInvalidInput)
		}
	}
	for _, ttl := range []struct {
		value             *time.Duration
		fallback, maximum time.Duration
	}{
		{&cfg.AccessTTL, 5 * time.Minute, 15 * time.Minute}, {&cfg.SessionTTL, 30 * 24 * time.Hour, 90 * 24 * time.Hour},
		{&cfg.CodeTTL, time.Minute, 5 * time.Minute}, {&cfg.ConsentTTL, 5 * time.Minute, 15 * time.Minute},
	} {
		if *ttl.value == 0 {
			*ttl.value = ttl.fallback
		}
		if *ttl.value < time.Second || *ttl.value > ttl.maximum {
			return nil, fmt.Errorf("oauth2: lifetime outside supported bounds: %w", sdk.ErrInvalidInput)
		}
	}
	if cfg.AccessTTL > cfg.SessionTTL {
		return nil, fmt.Errorf("oauth2: access lifetime exceeds session lifetime: %w", sdk.ErrInvalidInput)
	}
	cfg.ConfidentialSecretHashes = slices.Clone(cfg.ConfidentialSecretHashes)
	return &Service{repos: repos, signer: signer, resolver: resolver, verifier: verifier, config: cfg, now: o.now, logger: o.logger}, nil
}

func (s *Service) Metadata() Metadata {
	return Metadata{Issuer: s.config.Issuer, MCPResource: s.config.MCPResource, APIResource: s.config.APIResource, ConfidentialClientID: s.config.ConfidentialClientID}
}

// ClientDisplayName reads an informational snapshot only. It never authorizes
// a request; every credential operation still calls the current trust resolver.
func (s *Service) ClientDisplayName(ctx context.Context, id string) (string, error) {
	client, err := s.repos.OAuth2.GetClient(ctx, id)
	if errors.Is(err, sdk.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return client.Name, nil
}

func (s *Service) AuthenticateClient(clientID, secret string) (ClientProof, error) {
	presented := sha256.Sum256([]byte(secret))
	matched := 0
	for _, allowed := range s.config.ConfidentialSecretHashes {
		matched |= subtle.ConstantTimeCompare(presented[:], allowed[:])
	}
	if clientID != s.config.ConfidentialClientID || secret == "" || matched != 1 {
		return ClientProof{}, ErrInvalidClient
	}
	return ClientProof{service: s, clientID: clientID}, nil
}

func (s *Service) validProof(proof ClientProof) bool {
	return proof.service == s && proof.clientID == s.config.ConfidentialClientID
}

func (s *Service) resolveClient(ctx context.Context, id string) (Client, error) {
	if !validEndpoint(id, s.config.AllowLocalHTTP, true) || id == s.config.ConfidentialClientID {
		return Client{}, ErrInvalidClient
	}
	client, err := s.resolver.Resolve(ctx, id)
	if err != nil {
		return Client{}, err
	}
	if client.ID != id || len(client.RedirectURIs) == 0 || len(client.RedirectURIs) > 32 {
		return Client{}, ErrInvalidClient
	}
	for _, redirect := range client.RedirectURIs {
		if !validEndpoint(redirect, s.config.AllowLocalHTTP, true) {
			return Client{}, ErrInvalidClient
		}
		u, _ := url.Parse(redirect)
		values, err := url.ParseQuery(u.RawQuery)
		if err != nil {
			return Client{}, ErrInvalidClient
		}
		for _, reserved := range []string{"code", "state", "error", "error_description", "error_uri", "iss"} {
			if values.Has(reserved) {
				return Client{}, ErrInvalidClient
			}
		}
	}
	client.RedirectURIs = slices.Clone(client.RedirectURIs)
	return client, nil
}

func validEndpoint(raw string, localHTTP, query bool) bool {
	u, err := url.Parse(raw)
	if err != nil || raw == "" || len(raw) > 2048 || strings.TrimSpace(raw) != raw || u.Host == "" || u.User != nil || u.Fragment != "" || strings.Contains(raw, "#") || u.Opaque != "" || u.RawPath != "" || (!query && (u.RawQuery != "" || u.ForceQuery)) || u.Host != strings.ToLower(u.Host) {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	if !localHTTP || u.Scheme != "http" {
		return false
	}
	if u.Hostname() == "localhost" {
		return true
	}
	ip, err := netip.ParseAddr(u.Hostname())
	return err == nil && ip.IsLoopback()
}

func nilDependency(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}

func hashSecret(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

func invalidGrantError(err error) error {
	if errors.Is(err, sdk.ErrNotFound) || errors.Is(err, sdk.ErrExpired) || errors.Is(err, session.ErrUserNotActive) || errors.Is(err, sdk.ErrUnauthorized) {
		return ErrInvalidGrant
	}
	return err
}

func (s *Service) signAccess(sess session.Session, exchanged bool, limit time.Time) (TokenResponse, error) {
	now := s.now().UTC()
	expires := now.Add(s.config.AccessTTL)
	if sess.ExpiresAt.Before(expires) {
		expires = sess.ExpiresAt
	}
	if !limit.IsZero() && limit.Before(expires) {
		expires = limit
	}
	if expires.Sub(now) < time.Second {
		return TokenResponse{}, ErrInvalidGrant
	}
	audience, clientID := sess.Delegation.Resource, sess.Delegation.ClientID
	claims := map[string]any{
		"user_id": sess.UserID, "sub": "user:" + sess.UserID, "session_id": sess.ID,
		"session_profile": string(session.ProfileDelegated), "iss": s.config.Issuer,
		"jti": session.NewRefreshToken(),
	}
	if exchanged {
		audience, clientID = sess.Delegation.ExchangeResource, sess.Delegation.ExchangeClientID
		claims["origin_client_id"] = sess.Delegation.ClientID
		claims["act"] = map[string]any{"sub": clientID}
	}
	claims["aud"] = []string{audience}
	claims["client_id"] = clientID
	raw, err := s.signer.Sign(claims, expires)
	if err != nil {
		return TokenResponse{}, err
	}
	response := TokenResponse{AccessToken: raw, TokenType: "Bearer", ExpiresIn: int64(expires.Sub(now) / time.Second)}
	if exchanged {
		response.IssuedTokenType = AccessTokenType
	}
	return response, nil
}

func (s *Service) validDelegation(d session.Delegation) bool {
	return d.Issuer == s.config.Issuer && d.Resource == s.config.MCPResource && d.ExchangeResource == s.config.APIResource &&
		d.ExchangeClientID == s.config.ConfidentialClientID && d.ClientID != "" && d.ClientID != d.ExchangeClientID && d.AuthRevision >= 0
}

func (s *Service) liveAccess(ctx context.Context, raw string) (AccessToken, session.Session, error) {
	token, err := s.verifier.VerifyOAuth2AccessToken(ctx, raw)
	if err != nil {
		return AccessToken{}, session.Session{}, invalidGrantError(err)
	}
	if token.UserID == "" || token.SessionID == "" || token.Issuer != s.config.Issuer || !s.now().Before(token.ExpiresAt) {
		return AccessToken{}, session.Session{}, ErrInvalidGrant
	}
	sess, err := s.repos.Sessions.Get(ctx, token.SessionID)
	if err != nil {
		return AccessToken{}, session.Session{}, invalidGrantError(err)
	}
	if sess.ID != token.SessionID || sess.UserID != token.UserID || sess.Profile != session.ProfileDelegated || sess.Expired(s.now()) || token.ExpiresAt.After(sess.ExpiresAt) || !s.validDelegation(sess.Delegation) {
		return AccessToken{}, session.Session{}, ErrInvalidGrant
	}
	d := sess.Delegation
	direct := token.Audience == d.Resource && token.ClientID == d.ClientID && token.OriginClientID == "" && token.ActorID == ""
	exchanged := token.Audience == d.ExchangeResource && token.ClientID == d.ExchangeClientID && token.OriginClientID == d.ClientID && token.ActorID == d.ExchangeClientID
	if !direct && !exchanged {
		return AccessToken{}, session.Session{}, ErrInvalidGrant
	}
	owner, err := s.repos.Users.Get(ctx, token.UserID)
	if err != nil {
		return AccessToken{}, session.Session{}, invalidGrantError(err)
	}
	if owner.ID != token.UserID || !owner.Active() {
		return AccessToken{}, session.Session{}, ErrInvalidGrant
	}
	if _, err := s.resolveClient(ctx, d.ClientID); err != nil {
		return AccessToken{}, session.Session{}, err
	}
	return token, sess, nil
}
