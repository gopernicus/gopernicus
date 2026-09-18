package oauth2

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
)

const (
	GrantAuthorizationCode = "authorization_code"
	GrantRefreshToken      = "refresh_token"
	GrantTokenExchange     = "urn:ietf:params:oauth:grant-type:token-exchange"
	AccessTokenType        = "urn:ietf:params:oauth:token-type:access_token"
)

var (
	ErrInvalidRequest = errors.New("oauth2: invalid request")
	ErrInvalidClient  = errors.New("oauth2: invalid client")
	ErrInvalidScope   = errors.New("oauth2: scopes are not supported")
	ErrInvalidTarget  = errors.New("oauth2: invalid resource")
	ErrAccessDenied   = errors.New("oauth2: access denied")
)

type UserReader interface {
	Get(context.Context, string) (user.User, error)
}

type SessionReader interface {
	Get(context.Context, string) (session.Session, error)
	GetByRefreshHash(context.Context, string) (session.Session, session.RefreshMatch, error)
}

type Repositories struct {
	OAuth2         Repository
	Users          UserReader
	Sessions       SessionReader
	SecurityEvents securityevent.SecurityEventRepository
}

// TokenVerifier shares the host authenticator's cryptographic delegated-token
// profile. It rejects first-party tokens and returns an error for invalid proof.
// The OAuth service separately checks live storage and exact resource bindings.
type TokenVerifier interface {
	VerifyOAuth2AccessToken(context.Context, string) (AccessToken, error)
}

type AccessToken struct {
	UserID, SessionID, Issuer, Audience, ClientID, OriginClientID, ActorID string
	ExpiresAt                                                              time.Time
}

// Config names one authorization server and one MCP -> API exchange relationship.
// ConfidentialSecretHashes are SHA-256 digests of high-entropy host-managed
// secrets; multiple entries permit rotation without accepting public clients.
type Config struct {
	Issuer, MCPResource, APIResource, ConfidentialClientID string
	ConfidentialSecretHashes                               [][32]byte
	AccessTTL, SessionTTL, CodeTTL, ConsentTTL             time.Duration
	AllowLocalHTTP                                         bool
}

// Metadata exposes immutable public protocol coordinates, never client secrets.
type Metadata struct {
	Issuer, MCPResource, APIResource, ConfidentialClientID string
}

type AuthorizationRequest struct {
	ResponseType, ClientID, RedirectURI, Resource, CodeChallenge, CodeChallengeMethod, State, Scope string
}

type Authorization struct {
	Client                                                   Client
	UserID, DisplayName, Resource, ConsentToken, RedirectURI string
	ExpiresAt                                                time.Time
}

type AuthorizationResult struct {
	RedirectURI, Code, State, Error string
}

type CodeRequest struct {
	Code, ClientID, RedirectURI, Resource, CodeVerifier string
}

type RefreshRequest struct {
	RefreshToken, ClientID, Resource string
}

type RevokeRequest struct {
	Token, ClientID, TokenTypeHint string
}

type ExchangeRequest struct {
	SubjectToken, SubjectTokenType, RequestedTokenType, Resource, Scope, Audience, ActorToken string
}

type TokenResponse struct {
	AccessToken     string `json:"access_token"`
	TokenType       string `json:"token_type"`
	ExpiresIn       int64  `json:"expires_in"`
	RefreshToken    string `json:"refresh_token,omitempty"`
	IssuedTokenType string `json:"issued_token_type,omitempty"`
}

// ClientProof can only be obtained by authenticating to this service. HTTP
// adapters must obtain a fresh proof for each confidential endpoint request.
type ClientProof struct {
	service  *Service
	clientID string
}

type Introspection struct {
	Active    bool   `json:"active"`
	TokenType string `json:"token_type,omitempty"`
	Subject   string `json:"sub,omitempty"`
	UserID    string `json:"user_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Issuer    string `json:"iss,omitempty"`
	Audience  string `json:"aud,omitempty"`
	ClientID  string `json:"client_id,omitempty"`
	ExpiresAt int64  `json:"exp,omitempty"`
}

type Option func(*serviceOptions)

type serviceOptions struct {
	now    func() time.Time
	logger *slog.Logger
}

// WithClock supplies the authoritative time source; production normally uses UTC.
func WithClock(now func() time.Time) Option {
	return func(o *serviceOptions) { o.now = now }
}

func WithLogger(logger *slog.Logger) Option {
	return func(o *serviceOptions) {
		if logger == nil {
			o.logger = slog.Default()
			return
		}
		o.logger = logger
	}
}
