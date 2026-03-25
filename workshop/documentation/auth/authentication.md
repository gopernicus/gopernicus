# Authentication Deep Dive

The `authentication` package (`core/auth/authentication/`) provides security-critical identity verification flows. It handles registration, login, sessions, JWT tokens, email verification, password management, OAuth, and API key authentication.

## The Authenticator Struct

`Authenticator` is the central struct. It is constructed via `NewAuthenticator` with dependency injection:

```go
authenticator := authentication.NewAuthenticator(
    repositories,  // Repositories (users, passwords, sessions, tokens, codes)
    hasher,        // PasswordHasher interface
    signer,        // JWTSigner interface
    bus,           // events.Bus for email delivery
    cfg,           // Config (populated via environment.ParseEnvTags)
    // Optional:
    authentication.WithOAuth(providers, oauthRepo),
    authentication.WithAPIKeys(apiKeyRepo),
    authentication.WithProviderTokens(encrypter),
    authentication.WithAllowedRedirectURIs("https://app.example.com/auth/callback"),
    authentication.WithSecurityEvents(securityEventsRepo),
)
```

### Required Dependencies

- **Repositories** -- built via `NewRepositories(users, passwords, sessions, tokens, codes)`. Each argument satisfies a repository interface defined by the authentication package.
- **PasswordHasher** -- `Hash(password) (string, error)` and `Compare(hash, password) error`. Structurally compatible with `cryptids/bcrypt.Hasher`.
- **JWTSigner** -- `Sign(claims, expiresAt) (string, error)` and `Verify(token) (map[string]any, error)`. Structurally compatible with `cryptids/golangjwt.Signer`.
- **events.Bus** -- required for emitting verification code and password reset events.
- **Config** -- timing and policy settings populated from environment variables.

### Optional Dependencies (via Options)

| Option | Purpose |
|---|---|
| `WithOAuth(providers, repo)` | Enable OAuth authentication flows |
| `WithAPIKeys(repo)` | Enable API key authentication for service accounts |
| `WithProviderTokens(encrypter)` | Store encrypted OAuth provider tokens for later API calls |
| `WithAllowedRedirectURIs(uris...)` | Restrict OAuth redirect URIs to an exact-match allowlist |
| `WithSecurityEvents(repo)` | Enable security event audit logging |
| `WithAfterUserCreationHooks(hooks...)` | Register hooks to run after user creation (both credential and OAuth) |
| `WithLogger(log)` | Override the default `slog.Default()` logger |

## Configuration

`Config` is populated from environment variables via struct tags:

| Field | Env Var | Default |
|---|---|---|
| `AccessTokenExpiry` | `ACCESS_TOKEN_EXPIRY` | 30m |
| `RefreshTokenExpiry` | `REFRESH_TOKEN_EXPIRY` | 720h (30 days) |
| `VerificationCodeExpiry` | `VERIFICATION_CODE_EXPIRY` | 15m |
| `PasswordResetExpiry` | `PASSWORD_RESET_EXPIRY` | 1h |
| `OAuthStateExpiry` | `OAUTH_STATE_EXPIRY` | 10m |
| `MaxVerificationAttempts` | `MAX_VERIFICATION_ATTEMPTS` | 5 |
| `JWTIssuer` | `JWT_ISSUER` | (empty) |
| `JWTAudience` | `JWT_AUDIENCE` | (empty) |

## Registration

`Register(ctx, email, password)` creates a user account:

1. Hashes the password
2. Checks if the email already exists -- if so, returns a synthetic success to prevent email enumeration
3. Creates the user (with `EmailVerified=false`)
4. Stores the password hash
5. Runs any registered `AfterUserCreationHook` functions -- if a hook fails, registration fails (see [Hooks](#hooks))
6. Generates a 6-digit verification code, stores its SHA256 hash
7. Emits `VerificationCodeRequestedEvent` for email delivery
8. Returns `RegisterResult{UserID, VerificationCode}`

Password validation is the caller's responsibility. Use `authentication.ValidatePassword(password)` in your bridge layer to enforce NIST SP 800-63B defaults (8-72 chars, no complexity rules).

## Login

`Login(ctx, email, password)` authenticates with credentials:

1. Looks up the user by email
2. Checks the user is active
3. Verifies the password hash (constant-time comparison)
4. Checks email and password verification status -- if unverified, auto-sends a verification code and returns `ErrEmailNotVerified` or `ErrPasswordNotVerified`
5. Updates last login timestamp
6. Creates a session and returns `LoginResult{UserID, SessionID, AccessToken, RefreshToken}`

## Sessions and JWT Tokens

Each login creates a `Session` record with:
- `TokenHash` -- SHA256 of the access token (JWT)
- `RefreshTokenHash` -- SHA256 of the current refresh token
- `PreviousRefreshHash` -- SHA256 of the rotated-out refresh token (for reuse detection)
- `ExpiresAt` -- session expiry (default 30 days)

The access token is a JWT containing `user_id`, `sub`, `jti`, and optionally `iss` and `aud` claims.

### Two Authentication Modes

| Method | DB Hit | Revocation Lag | Use For |
|---|---|---|---|
| `AuthenticateJWT(ctx, token)` | No | Up to `AccessTokenExpiry` | Read-only endpoints |
| `AuthenticateSession(ctx, token)` | Yes | None | Writes, account changes, sensitive reads |

`AuthenticateSession` verifies the JWT, looks up the session by token hash, checks expiry, and verifies the user is active.

### Token Refresh with Rotation

`RefreshToken(ctx, refreshToken)` implements token rotation:

1. Looks up the session by refresh token hash
2. Generates new access and refresh tokens
3. Stores the old refresh hash in `PreviousRefreshHash` for reuse detection
4. Increments `RotationCount`

If a previously rotated refresh token is presented again (reuse detection), all sessions for that user are revoked immediately. This detects token theft.

## Email Verification

`VerifyEmail(ctx, email, code)` validates a 6-digit code:

1. Looks up the stored code by email and purpose
2. Checks expiry
3. Compares hashes (constant-time)
4. On wrong code: increments attempt counter, deletes code if `MaxVerificationAttempts` exceeded
5. On success: marks email verified, marks password verified, deletes the code, emits `EmailVerifiedEvent`

`ResendVerificationCode(ctx, email)` generates a new code and emits an event. Returns empty string for unknown emails (prevents enumeration).

## Password Management

**Change password** -- `ChangePassword(ctx, userID, sessionID, currentPassword, newPassword, revokeOtherSessions)` verifies the current password, stores the new hash, and optionally revokes all other sessions.

**Password reset flow:**
1. `InitiatePasswordReset(ctx, email)` -- generates an opaque token, emits `PasswordResetRequestedEvent`. For unknown emails, performs dummy work to equalize timing.
2. `ResetPassword(ctx, token, newPassword)` -- validates the token, stores the new hash, marks email and password as verified, revokes all sessions.

## OAuth

When configured with `WithOAuth(providers, repo)`:

1. `InitiateOAuthFlow(ctx, provider, redirectURI)` -- generates PKCE code verifier, state, nonce, and flow secret. Stores state in a verification code. Returns the authorization URL.
2. `HandleOAuthCallback(ctx, state, code, flowSecret)` -- exchanges the authorization code, verifies state/nonce, and either logs in an existing user, registers a new user (running any registered `AfterUserCreationHook` functions), or requires email verification for account linking.
3. `VerifyOAuthLink(ctx, email, code)` -- completes the email verification step for OAuth account linking.

OAuth accounts store the provider user ID, email, verification status, and optionally encrypted access/refresh tokens (via `WithProviderTokens`).

## API Keys

When configured with `WithAPIKeys(repo)`:

`AuthenticateAPIKey(ctx, key)` hashes the key, looks it up via `APIKeyRepository.GetByHash`, checks active status and expiry, and returns the service account ID.

API keys are distinguished from JWTs by format: JWTs have three dot-separated segments; anything else is treated as an API key.

## Repository Interfaces

The authentication package defines these repository interfaces, implemented by satisfiers that wrap your generated repositories:

- `UserRepository` -- Get, GetByEmail, Create, SetEmailVerified, SetLastLogin
- `PasswordRepository` -- GetByUserID, Create, Update, SetVerified
- `SessionRepository` -- Create, GetByTokenHash, GetByRefreshHash, GetByPreviousRefreshHash, Update, Delete, DeleteAllForUser, DeleteAllForUserExcept
- `VerificationTokenRepository` -- Create, Get, Delete, DeleteByUserIDAndPurpose
- `VerificationCodeRepository` -- Create, Get, Delete, IncrementAttempts
- `OAuthAccountRepository` -- GetByProvider, GetByUserID, Create, Delete
- `APIKeyRepository` -- GetByHash
- `SecurityEventRepository` -- Create

## User Deletion

`DeleteUser(ctx, userID)` revokes all sessions immediately and emits `UserDeletionRequestedEvent`. The event subscriber is responsible for cascading deletion across all data stores: passwords, OAuth links, verification codes, security event pseudonymization, and the user record.

## Built-in Security

The package handles these automatically:

- Email enumeration prevention (registration and password reset return identical responses for known/unknown emails)
- Timing attack mitigation (constant-time hash comparison, dummy work on unknown emails)
- Refresh token rotation with reuse detection
- Verification code brute-force lockout (`MaxVerificationAttempts`)
- Session revocation on password reset
- PKCE, state, nonce, and flow secret for OAuth
- Prevention of unlinking the last authentication method
- Security event logging with client IP and User-Agent

## Hooks

The authentication package supports synchronous hooks that run inline during user creation. Hooks fire for both credential-based registration and OAuth flows.

### AfterUserCreationHook

Register hooks via `WithAfterUserCreationHooks` at construction or `AddAfterUserCreationHooks` after construction:

```go
auth := authentication.NewAuthenticator(name, repos, hasher, signer, bus, cfg,
    authentication.WithAfterUserCreationHooks(func(ctx context.Context, user authentication.User) error {
        return invitations.ResolveForUser(ctx, user.UserID, user.Email)
    }),
)
```

**Error handling:** If a hook returns an error, registration fails. User creation is not transactional — the user and password records will already exist in the database. This is safe because the user remains unverified and cannot log in until email verification completes.

| Hook behavior | Return |
|---|---|
| Required work (resolve invitations, provision defaults) | Return the error — registration should fail |
| Best-effort work (analytics, cache warming) | Handle errors internally, return `nil` |

### Hooks vs Events

| Mechanism | When to use |
|---|---|
| Hooks (`AfterUserCreationHook`) | Synchronous, inline — "this must happen as part of user creation" |
| Events (`events.Bus`) | Asynchronous, durable — "notify the world that this happened" (emails, audit logs) |

## Related

- [Auth Architecture Overview](overview.md)
- [Authorization](authorization.md)
- [Auth Middleware](middleware.md)
- [Auth Schema Definition](schema-definition.md)
