---
sidebar_position: 1
title: Authentication
---

# Core — Authentication

The authentication package (`core/auth/authentication/`) handles identity verification — login, registration, session management, password flows, OAuth, API key authentication, and security event logging. It is framework-provided code that applications import and configure, not generated.

The central type is the `Authenticator`. It accepts interfaces for all of its dependencies and is configured via functional options.

## Setup

### Constructor

```go
auth := authentication.NewAuthenticator(
    "myapp",          // app name (used for cookie names: myapp_session, myapp_refresh)
    repos,            // Repositories struct (users, passwords, sessions, codes, tokens)
    hasher,           // PasswordHasher (bcrypt-compatible)
    signer,           // JWTSigner (golangjwt-compatible)
    bus,              // events.Bus for domain event emission
    cfg,              // Config (parsed from environment)
)
```

### Options

| Option | Purpose |
|---|---|
| `WithLogger(log)` | Custom logger (default: `slog.Default()`) |
| `WithOAuth(providers, repo)` | Enable OAuth authentication with provider map and account repository |
| `WithAPIKeys(repo)` | Enable API key authentication |
| `WithProviderTokens(encrypter)` | Store encrypted OAuth provider tokens (access/refresh) |
| `WithAllowedRedirectURIs(uris...)` | Restrict OAuth redirect URIs to an allowlist |
| `WithSecurityEvents(repo)` | Enable security event audit trail |
| `WithAfterUserCreationHooks(hooks...)` | Register post-registration callbacks |

### Configuration

All settings are configurable via environment variables with sensible defaults:

```go
type Config struct {
    AccessTokenExpiry       time.Duration `env:"ACCESS_TOKEN_EXPIRY" default:"30m"`
    RefreshTokenExpiry      time.Duration `env:"REFRESH_TOKEN_EXPIRY" default:"720h"`
    VerificationCodeExpiry  time.Duration `env:"VERIFICATION_CODE_EXPIRY" default:"15m"`
    PasswordResetExpiry     time.Duration `env:"PASSWORD_RESET_EXPIRY" default:"1h"`
    OAuthStateExpiry        time.Duration `env:"OAUTH_STATE_EXPIRY" default:"10m"`
    MaxVerificationAttempts int           `env:"MAX_VERIFICATION_ATTEMPTS" default:"5"`
    JWTIssuer               string        `env:"JWT_ISSUER"`
    JWTAudience             string        `env:"JWT_AUDIENCE"`
}
```

Parse with `environment.ParseEnvTags("APP", &cfg)` to read `APP_ACCESS_TOKEN_EXPIRY`, etc.

## Credential Authentication

### Register

```go
result, err := auth.Register(ctx, email, password, displayName)
// result.UserID, result.VerificationCode
```

Registration creates a user with `EmailVerified=false`, stores the password hash with `Verified=false`, runs any `AfterUserCreationHook` functions, generates a 6-digit verification code, and emits a `VerificationCodeRequestedEvent` for email delivery.

The user cannot log in until their email is verified. This is deliberate — unverified users are safe to leave in the database because they have no access.

**Email enumeration prevention:** if the email is already taken, the method returns a synthetic error indistinguishable in timing from a real registration. The caller sees `ErrCouldNotRegister`; an attacker cannot determine whether the email exists.

### Login

```go
result, err := auth.Login(ctx, email, password)
// result.User, result.SessionID, result.AccessToken, result.RefreshToken
```

Login verifies credentials, checks that the user is active and their email is verified, updates the last login timestamp, and creates a new session.

If credentials are correct but email is not yet verified, the authenticator automatically sends a fresh verification code and returns `ErrEmailNotVerified`. The caller should direct the user to the verification flow rather than showing a generic error.

## Session Management

The authenticator provides two authentication paths with different trade-offs:

### JWT Authentication (Fast Path)

```go
claims, err := auth.AuthenticateJWT(ctx, accessToken)
// claims.UserID
```

Verifies the JWT signature without any database query. This is fast but accepts stale revocations — a revoked session remains valid until the access token expires (default: 30 minutes). Suitable for read-only endpoints where eventual consistency is acceptable.

### Session Authentication (Strict Path)

```go
user, session, err := auth.AuthenticateSession(ctx, accessToken)
```

Verifies the JWT, then looks up the session in the database and checks that both the session and user are still active. Use this for write operations or anywhere immediate revocation matters.

### Token Refresh

```go
result, err := auth.RefreshToken(ctx, refreshToken)
// result.AccessToken, result.RefreshToken (new tokens)
```

Implements refresh token rotation: the old refresh token is invalidated and a new pair (access + refresh) is issued. The session stores the previous refresh token hash for reuse detection.

**Reuse detection:** if a previously rotated refresh token is presented, this indicates a potential token theft. The authenticator immediately revokes all sessions for the affected user and returns `ErrTokenReuse`. This forces re-authentication across all devices.

### Logout

```go
err := auth.Logout(ctx, userID, sessionID)
```

Deletes the session. The access token remains valid until it expires (use session authentication if immediate revocation is required).

## Email Verification

### Verify Email

```go
err := auth.VerifyEmail(ctx, email, code)
```

Validates the 6-digit code against the stored hash. On success, marks the user's email as verified and emits an `EmailVerifiedEvent`.

**Brute-force protection:** each failed attempt increments a counter. After `MaxVerificationAttempts` (default: 5), the code is deleted and `ErrTooManyAttempts` is returned. The user must request a new code.

### Resend Verification Code

```go
code, err := auth.ResendVerificationCode(ctx, email)
```

Generates a new code and emits a `VerificationCodeRequestedEvent`. Silently succeeds if the email is not found (enumeration prevention).

## Password Management

### Change Password

```go
err := auth.ChangePassword(ctx, userID, sessionID, currentPassword, newPassword, revokeOtherSessions)
```

Verifies the current password, stores the new hash, and optionally revokes all other sessions. Marks the password as verified (the user proved ownership by supplying the current password).

### Password Reset Flow

**Initiate:**

```go
token, err := auth.InitiatePasswordReset(ctx, email, authentication.WithResetURL("https://app.example.com/reset"))
```

Generates an opaque reset token and emits a `PasswordResetRequestedEvent` with the token and reset URL for email delivery. Performs timing-constant dummy work if the email is not found (enumeration prevention).

**Complete:**

```go
err := auth.ResetPassword(ctx, token, newPassword)
```

Validates the token, stores the new password hash, marks both email and password as verified (the reset token proves email ownership), and revokes all existing sessions.

### Password Policy

`ValidatePassword(password)` enforces NIST SP 800-63B defaults:

- Minimum 8 characters
- Maximum 72 characters (bcrypt truncation boundary)
- No complexity rules (NIST recommends against them)

The authenticator does not check breached password lists — that is an I/O concern for the caller to implement if desired.

## OAuth

OAuth is opt-in via `WithOAuth(providers, repo)`. The authenticator handles the full flow: state management, PKCE, code exchange, account linking, and email verification.

### Initiate Flow

```go
result, err := auth.InitiateOAuthFlow(ctx, provider, redirectURI, mobileRedirectURI)
// result.AuthorizationURL — redirect the user here
// result.State            — for CSRF verification
// result.FlowSecret       — for mobile flow binding (empty if not mobile)
```

Generates PKCE code verifier, state parameter, and optional nonce (for OpenID Connect providers). If a redirect URI allowlist is configured, validates the URI against it (exact match per RFC 9700).

**Mobile flows:** when `mobileRedirectURI` is non-empty, a flow secret is generated and its SHA256 hash is stored with the state. The mobile client must present the original flow secret in the callback to prove it initiated the flow.

### Handle Callback

```go
result, err := auth.HandleOAuthCallback(ctx, provider, code, state, flowSecret)
// result.UserID, result.AccessToken, result.RefreshToken, result.IsNewUser
```

Exchanges the authorization code for tokens, retrieves user info from the provider, and handles three scenarios:

1. **Existing linked account** — logs the user in directly
2. **Existing user with matching email** — initiates a pending link that requires email verification (prevents account takeover). Returns `ErrOAuthVerificationRequired`
3. **New user** — creates user and OAuth link. If the provider's email is not verified or not trusted, sends a verification code and returns `ErrOAuthAccountUnverified`

### Verify OAuth Link

```go
result, err := auth.VerifyOAuthLink(ctx, email, code)
```

Completes a pending OAuth link after the user verifies their email with the 6-digit code. Creates the OAuth account, marks email as verified, and logs the user in.

### Link / Unlink Accounts

```go
// Link a new provider to an existing user
err := auth.LinkOAuthAccount(ctx, userID, provider, code, state)

// Unlink a provider
err := auth.UnlinkOAuthAccount(ctx, userID, provider)

// List linked providers
accounts, err := auth.GetLinkedAccounts(ctx, userID)
```

Unlinking is guarded: if the user has no password and no other linked providers, the operation returns `ErrCannotUnlinkLastMethod` to prevent locking the user out.

### Provider Token Storage

When `WithProviderTokens(encrypter)` is configured, the authenticator encrypts and stores the OAuth provider's access and refresh tokens alongside the account link. This enables server-side API calls to the provider on behalf of the user (e.g., fetching GitHub repos, Google Calendar events).

## API Key Authentication

API key authentication is opt-in via `WithAPIKeys(repo)`.

```go
serviceAccountID, err := auth.AuthenticateAPIKey(ctx, key)
```

Hashes the provided key (SHA256), looks it up in the repository, and checks that it is active and not expired. Returns the associated service account ID.

API key CRUD is handled by the generated `apikeys` repository — the authenticator only handles the read-only authentication path.

## Events

The authenticator emits domain events for side effects that should be handled outside the auth package (typically email delivery). Subscribe to these on the `events.Bus`.

| Event | Emitted when | Typical subscriber action |
|---|---|---|
| `VerificationCodeRequestedEvent` | Registration, resend, or login with unverified email | Send verification email with 6-digit code |
| `PasswordResetRequestedEvent` | Password reset initiated | Send reset email with token link |
| `OAuthLinkVerificationRequestedEvent` | Existing user links OAuth with unverified email | Send verification email with 6-digit code |
| `EmailVerifiedEvent` | Email successfully verified | Resolve pending invitations, provision defaults |
| `UserDeletionRequestedEvent` | User deletion initiated | Cascade cleanup (passwords, sessions, OAuth links, domain data) |

Events carry the data needed for the subscriber to act — user ID, email, display name, code/token, and expiration. The authenticator never sends emails directly.

## Hooks

```go
type AfterUserCreationHook func(ctx context.Context, user User) error
```

Hooks run synchronously after user creation (both credential and OAuth registration). They block registration on error, but the user record has already been created — returning an error does not roll back the user.

Common uses: resolve pending invitations, provision default permissions, trigger onboarding workflows.

## Security

### Built-In Protections

- **Token rotation** — refresh tokens are single-use; each refresh issues a new pair
- **Reuse detection** — replaying a rotated refresh token revokes all user sessions
- **Brute-force protection** — verification codes locked out after `MaxVerificationAttempts`
- **Email enumeration prevention** — registration and password reset perform timing-constant work for unknown emails
- **Constant-time comparison** — all token and code verification uses constant-time hash comparison
- **OAuth account verification** — new OAuth links require email verification unless the provider is trusted
- **Last auth method guard** — cannot unlink the last OAuth provider if no password exists
- **Session revocation on password reset** — all sessions revoked when password is reset
- **PKCE + state + nonce** — OAuth flows use PKCE code verifier, state parameter, and nonce (for OIDC)
- **Mobile flow binding** — OAuth flows can be bound to a mobile session via flow secret

### Caller Responsibilities

These concerns are intentionally left to the application:

- **Password validation** — call `ValidatePassword()` before registration or password change
- **Rate limiting** — throttle login, registration, password reset, and verification endpoints
- **Email delivery** — subscribe to events and send emails
- **Client info injection** — wire `WithClientInfo(ctx, ClientInfo{IP, UserAgent})` in HTTP middleware for security event logging
- **Security event retention** — define retention periods for audit logs (GDPR compliance)
- **User deletion cascade** — subscribe to `UserDeletionRequestedEvent` and clean up domain data
- **OAuth redirect URI allowlist** — configure `WithAllowedRedirectURIs()` for production
- **Breached password checking** — validate passwords against breach databases if desired

## Satisfiers

The authentication package defines its own repository interfaces (`UserRepository`, `SessionRepository`, etc.) and never imports generated repository packages. Satisfiers in `core/auth/authentication/satisfiers/` bridge the gap:

```go
// satisfiers/users.go
type UserSatisfier struct {
    repo usersRepo  // generated users.Repository
}

func (s *UserSatisfier) GetByEmail(ctx context.Context, email string) (authentication.User, error) {
    u, err := s.repo.GetByEmail(ctx, email)
    // ... map generated User → authentication.User
    return authentication.User{
        UserID:        u.UserID,
        Email:         u.Email,
        EmailVerified: u.EmailVerified,
        Active:        u.RecordState == "active",
    }, err
}
```

Each satisfier translates between the generated entity types and the authentication package's domain types. The app layer wires them together at startup.

| Satisfier | Auth interface | Generated repository |
|---|---|---|
| `UserSatisfier` | `UserRepository` | `auth/users` |
| `PasswordSatisfier` | `PasswordRepository` | `auth/userpasswords` |
| `SessionSatisfier` | `SessionRepository` | `auth/sessions` |
| `VerificationCodeSatisfier` | `VerificationCodeRepository` | `auth/verificationcodes` |
| `VerificationTokenSatisfier` | `VerificationTokenRepository` | `auth/verificationtokens` |
| `OAuthAccountSatisfier` | `OAuthAccountRepository` | `auth/oauthaccounts` |
| `APIKeySatisfier` | `APIKeyRepository` | `auth/apikeys` |
| `SecurityEventSatisfier` | `SecurityEventRepository` | `auth/securityevents` |

## Crypto Interfaces

The authenticator depends on three crypto interfaces. These are structurally compatible with `infrastructure/cryptids` adapters — no import required.

| Interface | Methods | Compatible with |
|---|---|---|
| `PasswordHasher` | `Hash(password) (string, error)`, `Compare(hash, password) error` | `cryptids/bcrypt` |
| `JWTSigner` | `Sign(claims) (string, error)`, `Verify(token) (Claims, error)` | `cryptids/golangjwt` |
| `TokenEncrypter` | `Encrypt(plaintext) (string, error)`, `Decrypt(ciphertext) (string, error)` | `cryptids/aesgcm` |

## Errors

All errors wrap base types from `sdk/errs`, allowing callers to check at either level.

**Credentials:**
`ErrInvalidCredentials`, `ErrEmailNotVerified`, `ErrPasswordNotVerified`, `ErrEmailAlreadyExists`, `ErrUserInactive`, `ErrCouldNotRegister`

**Sessions:**
`ErrSessionNotFound`, `ErrTokenExpired`, `ErrTokenReuse`

**Verification:**
`ErrCodeExpired`, `ErrCodeInvalid`, `ErrTooManyAttempts`, `ErrEmailAlreadyVerified`

**OAuth:**
`ErrUnsupportedProvider`, `ErrInvalidOAuthState`, `ErrOAuthAccountNotLinked`, `ErrCannotUnlinkLastMethod`, `ErrOAuthAccountExists`, `ErrOAuthVerificationRequired`, `ErrOAuthAccountUnverified`, `ErrInvalidFlowSecret`, `ErrOAuthNotConfigured`, `ErrInvalidRedirectURI`

**API Keys:**
`ErrAPIKeyNotFound`, `ErrAPIKeyExpired`, `ErrAPIKeyInactive`, `ErrAPIKeysNotConfigured`

See also: [Bridge Authentication](../../bridge/auth/authentication.md) for the HTTP endpoints that expose these flows.
