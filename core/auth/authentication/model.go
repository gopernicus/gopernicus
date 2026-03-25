package authentication

import (
	"context"
	"encoding/json"
	"time"
)

// ---------------------------------------------------------------------------
// Core types — minimal data the auth package needs
// ---------------------------------------------------------------------------

// User is the minimal user data auth needs. The scaffolded wrapper
// maps between this and the full domain User type.
type User struct {
	UserID        string
	Email         string
	DisplayName   string
	EmailVerified bool
	Active        bool // derived from record_state == "active"
}

// Session represents an auth session with token rotation state.
type Session struct {
	SessionID           string
	UserID              string
	TokenHash           string // SHA256 of access token
	RefreshTokenHash    string // SHA256 of current refresh token
	PreviousRefreshHash string // SHA256 of rotated-out refresh token (reuse detection)
	RotationCount       int
	ExpiresAt           time.Time
}

// Password holds a user's password hash and verification status.
//
// Verified indicates this specific credential has been proven to belong to
// the user (via email verification code or password reset token). A newly
// registered user has Verified=false until they complete email verification.
type Password struct {
	UserID   string
	Hash     string
	Verified bool
}

// VerificationCode is a numeric code (e.g. 6-digit email verification).
// Data holds optional JSON metadata (e.g. serialized OAuthState or PendingOAuthLink).
type VerificationCode struct {
	Identifier   string // keyed value, e.g. email address or state parameter
	CodeHash     string // SHA256 of the plaintext code
	Purpose      string // "email_verify", "oauth_link", etc.
	ExpiresAt    time.Time
	Data         []byte // optional JSON metadata
	AttemptCount int    // failed verification attempts; code is deleted after max
}

// VerificationToken is an opaque token (e.g. password reset link).
type VerificationToken struct {
	Identifier string // lookup key, e.g. email address or provider name
	TokenHash  string // SHA256 of the plaintext token
	UserID     string
	Purpose    string // "password_reset"
	ExpiresAt  time.Time
}

// Claims are extracted from a validated access token.
type Claims struct {
	UserID string
}

// APIKey is the read-only view of an API key used for authentication.
// Full CRUD for API keys is handled by generated repository code.
type APIKey struct {
	ID               string
	ServiceAccountID string
	KeyHash          string
	ExpiresAt        *time.Time
	Active           bool
}

// SecurityEvent is the data written to the security events store.
// The auth package only creates events; reading, listing, and deleting
// are the responsibility of user-generated repository code.
//
// IPAddress and UserAgent are populated automatically from context when
// the bridge layer injects client info via [WithClientInfo].
type SecurityEvent struct {
	UserID      string         // empty for anonymous/system events
	EventType   string         // e.g. SecEventLogin
	EventStatus string         // e.g. SecStatusSuccess
	IPAddress   string         // client IP; populated from context
	UserAgent   string         // client User-Agent; populated from context
	Details     map[string]any // optional structured data; adapter handles serialization
}

// ---------------------------------------------------------------------------
// Input types
// ---------------------------------------------------------------------------

// CreateUserInput is the input for creating a new user via [UserRepository].
type CreateUserInput struct {
	Email         string
	DisplayName   string
	EmailVerified bool // true for OAuth with verified email, false for credentials
}

// ---------------------------------------------------------------------------
// Result types — returned by Authenticator methods
// ---------------------------------------------------------------------------

// RegisterResult is returned by [Authenticator.Register].
type RegisterResult struct {
	UserID           string
	VerificationCode string // plaintext 6-digit code; caller delivers to user
}

// LoginResult is returned by [Authenticator.Login] and [Authenticator.RefreshToken].
type LoginResult struct {
	User         User
	SessionID    string
	AccessToken  string
	RefreshToken string
}

// ---------------------------------------------------------------------------
// OAuth types
// ---------------------------------------------------------------------------

// OAuthFlowResult is returned by [Authenticator.InitiateOAuthFlow].
type OAuthFlowResult struct {
	AuthorizationURL string
	State            string
	FlowSecret       string // for mobile flow binding
}

// OAuthCallbackResult is returned by [Authenticator.HandleOAuthCallback]
// and [Authenticator.VerifyOAuthLink].
type OAuthCallbackResult struct {
	UserID       string
	AccessToken  string
	RefreshToken string
	IsNewUser    bool
}

// OAuthAccount represents an OAuth provider linked to a user.
//
// AccountVerified indicates this specific credential has been proven to belong
// to the user. When the provider reports a verified email matching the user's
// email, AccountVerified is set to true immediately. Otherwise, email
// verification via a 6-digit code is required before the account can be used
// for login.
//
// When [WithProviderTokens] is configured, AccessToken and RefreshToken hold
// encrypted ciphertext (via [TokenEncrypter]). Application code that needs to
// make API calls on behalf of the user must decrypt these using the same encrypter.
type OAuthAccount struct {
	UserID                string
	Provider              string
	ProviderUserID        string
	ProviderEmail         string
	ProviderEmailVerified bool
	AccountVerified       bool // credential-level verification
	LinkedAt              time.Time
	AccessToken           string          // encrypted; empty if provider tokens not stored
	RefreshToken          string          // encrypted; empty if no refresh token
	TokenExpiresAt        time.Time       // zero if unknown
	TokenType             string          // usually "Bearer"
	Scope                 string          // space-separated OAuth scopes
	IDToken               string          // OpenID Connect ID token
	ProfileData           json.RawMessage // provider profile info (name, avatar, etc.)
}

// OAuthState is the temporary state stored during an OAuth flow.
type OAuthState struct {
	State             string
	Provider          string
	CodeVerifier      string
	Nonce             string
	RedirectURI       string
	MobileRedirectURI string // custom scheme URI for mobile redirect (e.g. myapp://oauth-callback); empty for web flows
	UserID            string // non-empty when linking to an existing user
	FlowSecretHash    string // SHA256 hash of flow secret for mobile session binding; empty for web flows
}

// PendingOAuthLink holds data for an OAuth link awaiting email verification.
//
// When [WithProviderTokens] is configured, AccessToken and RefreshToken hold
// encrypted ciphertext (via [TokenEncrypter]). They are never stored in plaintext.
type PendingOAuthLink struct {
	Email                 string    `json:"email"`
	Provider              string    `json:"provider"`
	ProviderUserID        string    `json:"provider_user_id"`
	ProviderEmail         string    `json:"provider_email"`
	ProviderEmailVerified bool      `json:"provider_email_verified"`
	UserID                string    `json:"user_id,omitempty"`
	AccessToken           string    `json:"access_token,omitempty"`  // encrypted
	RefreshToken          string    `json:"refresh_token,omitempty"` // encrypted
	TokenExpiresAt        time.Time `json:"token_expires_at,omitempty"`
}

// Verification purpose constants.
const (
	PurposeEmailVerify   = "email_verify"
	PurposePasswordReset = "password_reset"
	PurposeOAuthLink     = "oauth_link"
	PurposeOAuthState    = "oauth_state"
	PurposePendingLink   = "pending_oauth_link"
)

// Security event type constants.
const (
	SecEventRegister           = "register"
	SecEventLogin              = "login"
	SecEventLogout             = "logout"
	SecEventTokenRefresh       = "token_refresh"
	SecEventTokenReuse         = "token_reuse"
	SecEventPasswordChange     = "password_change"
	SecEventPasswordResetInit  = "password_reset_init"
	SecEventPasswordReset      = "password_reset"
	SecEventEmailVerified      = "email_verified"
	SecEventVerificationResent = "verification_resent"
	SecEventOAuthLogin         = "oauth_login"
	SecEventOAuthRegister      = "oauth_register"
	SecEventOAuthLinkVerified  = "oauth_link_verified"
	SecEventOAuthLinked        = "oauth_linked"
	SecEventOAuthUnlinked      = "oauth_unlinked"
	SecEventAPIKeyAuth         = "apikey_auth"
	SecEventUserDeleted        = "user_deleted"
)

// Security event status constants.
const (
	SecStatusSuccess    = "success"
	SecStatusFailure    = "failure"
	SecStatusBlocked    = "blocked"
	SecStatusSuspicious = "suspicious"
)

// ---------------------------------------------------------------------------
// Repository interfaces — implemented by user's generated repos via adapters
// ---------------------------------------------------------------------------

// UserRepository provides the user lookups and mutations auth needs.
type UserRepository interface {
	Get(ctx context.Context, id string) (User, error)
	GetByEmail(ctx context.Context, email string) (User, error)
	Create(ctx context.Context, input CreateUserInput) (User, error)
	SetEmailVerified(ctx context.Context, id string) error
	SetLastLogin(ctx context.Context, id string, at time.Time) error
}

// PasswordRepository manages password hashes, stored separately from users.
type PasswordRepository interface {
	GetByUserID(ctx context.Context, userID string) (Password, error)
	Create(ctx context.Context, userID, hash string) error
	Update(ctx context.Context, userID, hash string) error

	// SetVerified marks a password as verified (credential-level verification).
	// Called when the user proves email ownership via verification code or
	// password reset token.
	SetVerified(ctx context.Context, userID string) error
}

// SessionRepository manages auth sessions with token rotation support.
type SessionRepository interface {
	Create(ctx context.Context, session Session) (Session, error)
	GetByTokenHash(ctx context.Context, hash string) (Session, error)
	GetByRefreshHash(ctx context.Context, hash string) (Session, error)
	GetByPreviousRefreshHash(ctx context.Context, hash string) (Session, error)
	Update(ctx context.Context, session Session) error
	Delete(ctx context.Context, userID, sessionID string) error
	DeleteAllForUser(ctx context.Context, userID string) error
	DeleteAllForUserExcept(ctx context.Context, userID, exceptSessionID string) error
}

// VerificationTokenRepository manages tokens (opaque).
type VerificationTokenRepository interface {
	Create(ctx context.Context, code VerificationToken) error
	Get(ctx context.Context, identifier, purpose string) (VerificationToken, error)
	Delete(ctx context.Context, identifier, purpose string) error

	// DeleteByUserIDAndPurpose removes all tokens for a user and purpose.
	// Used to invalidate previous password reset tokens when a new one is issued.
	DeleteByUserIDAndPurpose(ctx context.Context, userID, purpose string) error
}

// VerificationCodeRepository manages verification codes (6-digit).
type VerificationCodeRepository interface {
	Create(ctx context.Context, code VerificationCode) error
	Get(ctx context.Context, identifier, purpose string) (VerificationCode, error)
	Delete(ctx context.Context, identifier, purpose string) error

	// IncrementAttempts atomically increments the attempt counter and returns
	// the new count. Used for brute force protection on verification codes.
	IncrementAttempts(ctx context.Context, identifier, purpose string) (int, error)
}

// OAuthAccountRepository manages OAuth provider accounts linked to users.
type OAuthAccountRepository interface {
	GetByProvider(ctx context.Context, provider, providerUserID string) (OAuthAccount, error)
	GetByUserID(ctx context.Context, userID string) ([]OAuthAccount, error)
	Create(ctx context.Context, account OAuthAccount) error
	Delete(ctx context.Context, userID, provider string) error
}

// APIKeyRepository provides the API key lookups auth needs.
// Only read-only access is required for authentication; full CRUD
// is handled by generated repository code.
type APIKeyRepository interface {
	GetByHash(ctx context.Context, hash string) (APIKey, error)
}

// SecurityEventRepository stores security-related events for audit trails.
// Only Create is required — the auth package is a write-only producer.
// Full CRUD (list, filter, delete) is handled by generated repository code.
type SecurityEventRepository interface {
	Create(ctx context.Context, event SecurityEvent) error
}

// ---------------------------------------------------------------------------
// Crypto interfaces — structurally compatible with cryptids.*
// ---------------------------------------------------------------------------

// PasswordHasher hashes and compares passwords.
// cryptids/bcrypt.Hasher satisfies this interface.
type PasswordHasher interface {
	Hash(password string) (string, error)
	Compare(hash, password string) error
}

// JWTSigner signs and verifies JWTs.
// cryptids/golangjwt.Signer satisfies this interface.
type JWTSigner interface {
	Sign(claims map[string]any, expiresAt time.Time) (string, error)
	Verify(token string) (map[string]any, error)
}

// TokenEncrypter encrypts and decrypts OAuth provider tokens for storage.
// cryptids/aesgcm.Encrypter satisfies this interface.
//
// Used when [WithProviderTokens] is configured. Provider access and refresh
// tokens are encrypted before storage and must be decrypted by application
// code when making API calls on behalf of the user.
type TokenEncrypter interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(ciphertext string) (string, error)
}
