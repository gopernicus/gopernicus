// Package authentication provides security-critical authentication flows.
//
// This package owns login, registration, token refresh (with rotation and
// reuse detection), session validation, password management, email
// verification, and OAuth flows. User projects wrap [Authenticator] to add
// business-specific behavior — custom claims, post-login hooks, email
// delivery — while getting security fixes via go get -u.
//
// # Interfaces
//
// authentication defines its own [PasswordHasher] and [JWTSigner] interfaces.
// They are structurally compatible with the cryptids package, so
// cryptids adapters (bcrypt, golangjwt) satisfy them without any import.
//
// authentication also defines repository interfaces ([UserRepository],
// [SessionRepository], [PasswordRepository], [VerificationRepository],
// [OAuthAccountRepository]) that the user's generated repos implement
// via thin adapters in the scaffolded core/auth layer.
//
// # Hooks
//
// Applications can register [AfterUserCreationHook] functions to run
// synchronously after a user is created (both credential and OAuth flows).
// Use [WithAfterUserCreationHooks] at construction or
// [Authenticator.AddAfterUserCreationHooks] after construction.
//
// If a hook returns an error, registration fails. User creation is not
// transactional, but the unverified user record is harmless — the user
// cannot log in until email verification completes.
//
// For best-effort work (analytics, cache warming), handle errors
// internally and return nil. For required work (resolving invitations,
// provisioning defaults), return the error.
//
// # Events
//
// Authentication emits events via [events.Bus] for email delivery:
//   - [EventTypeVerificationCodeRequested] — after registration
//   - [EventTypePasswordResetRequested] — after password reset initiation
//   - [EventTypeOAuthLinkVerificationRequested] — during OAuth linking
//
// An event bus is required. Use [events/memorybus] for single-process
// deployments. Subscribers in the bridge layer handle email rendering
// and sending. Methods also return codes/tokens for testing convenience.
//
// # Usage
//
//	var cfg authentication.Config
//	environment.ParseEnvTags("APP", &cfg)
//
//	auth := authentication.NewAuthenticator(name, repos, hasher, signer, bus, cfg,
//	    authentication.WithOAuth(providers, oauthRepo),
//	    authentication.WithAfterUserCreationHooks(resolveInvitationsHook),
//	)
//	result, err := auth.Login(ctx, "user@example.com", "password123")
package authentication

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/infrastructure/events"
	"github.com/gopernicus/gopernicus/infrastructure/oauth"
)

// Config holds authentication timing and policy settings.
// Use sdk/environment.ParseEnvTags to populate from environment variables.
//
//	var cfg authentication.Config
//	environment.ParseEnvTags("APP", &cfg) // reads APP_ACCESS_TOKEN_EXPIRY, etc.
type Config struct {
	AccessTokenExpiry      time.Duration `env:"ACCESS_TOKEN_EXPIRY" default:"30m"`
	RefreshTokenExpiry     time.Duration `env:"REFRESH_TOKEN_EXPIRY" default:"720h"` // 30 days
	VerificationCodeExpiry time.Duration `env:"VERIFICATION_CODE_EXPIRY" default:"15m"`
	PasswordResetExpiry    time.Duration `env:"PASSWORD_RESET_EXPIRY" default:"1h"`
	OAuthStateExpiry       time.Duration `env:"OAUTH_STATE_EXPIRY" default:"10m"`
	MaxVerificationAttempts int          `env:"MAX_VERIFICATION_ATTEMPTS" default:"5"`

	// JWTIssuer sets the "iss" claim in access tokens. Optional.
	JWTIssuer   string `env:"JWT_ISSUER"`
	// JWTAudience sets the "aud" claim in access tokens. Optional.
	JWTAudience string `env:"JWT_AUDIENCE"`
}

// NewRepositories creates a Repositories value with all required repository implementations.
func NewRepositories(
	users UserRepository,
	passwords PasswordRepository,
	sessions SessionRepository,
	tokens VerificationTokenRepository,
	codes VerificationCodeRepository,
) Repositories {
	return Repositories{
		users:     users,
		passwords: passwords,
		sessions:  sessions,
		tokens:    tokens,
		codes:     codes,
	}
}

// Repositories represents the data managing repositories that the authentication package requires.
type Repositories struct {
	users     UserRepository
	passwords PasswordRepository
	sessions  SessionRepository
	tokens    VerificationTokenRepository
	codes     VerificationCodeRepository
}

// Authenticator handles all authentication flows: credentials, sessions,
// email verification, password management, and OAuth.
type Authenticator struct {
	// Name is the application name, used to derive cookie names
	// (e.g. "myapp" → "myapp_session").
	name string

	// Required repos.
	repositories Repositories

	// Required deps.
	hasher PasswordHasher
	signer JWTSigner
	log    *slog.Logger
	config Config

	// Optional: OAuth (set via WithOAuth).
	providers map[string]oauth.Provider
	oauthRepo OAuthAccountRepository

	// Optional: provider token storage (set via WithProviderTokens).
	encrypter TokenEncrypter

	// Optional: allowed redirect URIs for OAuth (set via WithAllowedRedirectURIs).
	allowedRedirectURIs map[string]struct{}

	// Optional: API keys (set via WithAPIKeys).
	apiKeys APIKeyRepository

	// Optional: security event logging (set via WithSecurityEvents).
	securityEvents SecurityEventRepository

	// Required: event bus for email delivery (verification codes, password resets).
	bus events.Bus

	// Hooks
	afterUserCreationHooks []AfterUserCreationHook
}

// NewAuthenticator creates a new Authenticator with dependency injection.
//
// cfg should be populated via environment.ParseEnvTags to pick up sensible
// defaults from struct tags.
//
// bus is the event bus for emitting authentication events (verification codes,
// password resets). Use events/memorybus for single-process deployments.
func NewAuthenticator(
	name string,
	repositories Repositories,
	hasher PasswordHasher,
	signer JWTSigner,
	bus events.Bus,
	cfg Config,
	opts ...Option,
) *Authenticator {
	a := &Authenticator{
		name:         name,
		repositories: repositories,
		hasher:       hasher,
		signer:       signer,
		bus:          bus,
		log:          slog.Default(),
		config:       cfg,
	}

	for _, opt := range opts {
		opt(a)
	}

	return a
}

// SessionTokenName returns the cookie name for the session (access) token,
// derived from the application name (e.g. "myapp" → "myapp_session").
func (a *Authenticator) SessionTokenName() string {
	return fmt.Sprintf("%s_session", strings.ToLower(a.name))
}

// RefreshTokenName returns the cookie name for the refresh token,
// derived from the application name (e.g. "myapp" → "myapp_refresh").
func (a *Authenticator) RefreshTokenName() string {
	return fmt.Sprintf("%s_refresh", strings.ToLower(a.name))
}

// ---------------------------------------------------------------------------
// Options
// ---------------------------------------------------------------------------

// Option configures an [Authenticator].
type Option func(*Authenticator)

// WithLogger sets the logger. Defaults to slog.Default().
func WithLogger(log *slog.Logger) Option {
	return func(a *Authenticator) { a.log = log }
}

// WithOAuth enables OAuth authentication. Pass the provider map and the
// repository for storing linked accounts.
func WithOAuth(providers map[string]oauth.Provider, repo OAuthAccountRepository) Option {
	return func(a *Authenticator) {
		a.providers = providers
		a.oauthRepo = repo
	}
}

// WithAPIKeys enables API key authentication. The lookup interface provides
// read-only access to API key records for authentication. Full CRUD for API
// keys is handled by generated repository code.
func WithAPIKeys(lookup APIKeyRepository) Option {
	return func(a *Authenticator) { a.apiKeys = lookup }
}

// WithProviderTokens enables encrypted storage of OAuth provider access and
// refresh tokens. When configured, tokens received during OAuth flows are
// encrypted via the [TokenEncrypter] and stored in [OAuthAccount] records.
//
// This allows application code to later make API calls to the provider
// (e.g., GitHub, Google) on behalf of the user by decrypting the stored tokens.
//
// If not configured, provider tokens are discarded after the OAuth flow completes.
func WithProviderTokens(encrypter TokenEncrypter) Option {
	return func(a *Authenticator) { a.encrypter = encrypter }
}

// WithAllowedRedirectURIs restricts OAuth redirect URIs to an exact-match
// allowlist per RFC 9700. If configured, [Authenticator.InitiateOAuthFlow]
// rejects any redirectURI not in the list. If not configured, any URI is
// accepted (not recommended for production).
func WithAllowedRedirectURIs(uris ...string) Option {
	return func(a *Authenticator) {
		a.allowedRedirectURIs = make(map[string]struct{}, len(uris))
		for _, u := range uris {
			a.allowedRedirectURIs[u] = struct{}{}
		}
	}
}

// WithSecurityEvents enables security event logging. Events are written
// to the provided repository for audit trail purposes. If not configured,
// no security events are recorded. Security event logging never fails
// auth flows — errors are logged as warnings.
func WithSecurityEvents(repo SecurityEventRepository) Option {
	return func(a *Authenticator) { a.securityEvents = repo }
}

// WithAfterUserCreationHooks registers hooks to run after a user is created
// (both credential-based registration and OAuth flows). If any hook returns
// an error, registration fails. For best-effort work, handle errors
// internally and return nil.
func WithAfterUserCreationHooks(hooks ...AfterUserCreationHook) Option {
	return func(a *Authenticator) {
		a.afterUserCreationHooks = append(a.afterUserCreationHooks, hooks...)
	}
}

// AddAfterUserCreationHooks registers hooks to run after user creation.
// Can be called after construction for cases where hook dependencies
// aren't available at NewAuthenticator time.
func (a *Authenticator) AddAfterUserCreationHooks(hooks ...AfterUserCreationHook) {
	a.afterUserCreationHooks = append(a.afterUserCreationHooks, hooks...)
}

// runAfterUserCreationHooks executes all registered after-user-creation hooks.
// Returns the first error encountered — remaining hooks are not executed.
func (a *Authenticator) runAfterUserCreationHooks(ctx context.Context, user User) error {
	for _, hook := range a.afterUserCreationHooks {
		if err := hook(ctx, user); err != nil {
			return fmt.Errorf("after user creation hook: %w", err)
		}
	}
	return nil
}
