# authentication

Package `authentication` provides security-critical authentication flows: registration, login, session management, token refresh with rotation and reuse detection, password management, email verification, OAuth, and API key authentication.

This package handles the **security logic**. The caller (your bridge/application layer) is responsible for everything listed below.

## Caller Responsibilities

### Password Validation

The `Authenticator` does **not** enforce password policy internally. `Register`, `ChangePassword`, and `ResetPassword` accept any non-empty string.

Use [ValidatePassword] in your bridge layer's request validation to enforce NIST SP 800-63B defaults (8-72 chars, no complexity rules):

```go
errs.Add(authentication.ValidatePassword(req.Password))
```

The default `authbridge` package already calls this. If you eject the bridge, keep calling it.

For additional hardening, consider checking passwords against known-breached lists (e.g. HaveIBeenPwned) as a separate I/O-bound step in your handler.

### Rate Limiting

The `Authenticator` does not rate-limit any endpoint. Your HTTP middleware must handle:

- **Login**: Throttle by IP and by email (e.g. 5 failures per minute per IP, 10 per email)
- **Registration**: Throttle by IP to prevent mass account creation
- **Password reset**: Throttle by IP to prevent abuse
- **Verification code resend**: Throttle by IP and email
- **API key authentication**: Throttle by IP

Verification codes have built-in attempt counting (`MaxVerificationAttempts`), but this only protects against brute-forcing a single code — not against requesting new codes repeatedly.

Use `infrastructure/ratelimiter` with `bridge/protocol/httpmid` to apply rate limiting at the middleware layer.

### User Deletion (GDPR Art. 17)

`DeleteUser` revokes all sessions immediately and emits a `UserDeletionRequestedEvent`. Your event subscriber must handle the actual data cleanup:

- Delete the password hash
- Delete OAuth account links
- Delete verification codes and tokens (keyed by email)
- Pseudonymize security events (replace user ID with a random token, strip email from details — do not delete the events outright, as the audit trail has legitimate interest under GDPR Art. 6(1)(f))
- Delete or anonymize the user record
- Delete any domain-specific data (orders, profiles, etc.)

```go
bus.Subscribe(authentication.EventTypeUserDeletionRequested, func(ctx context.Context, event events.Event) error {
    e := event.(authentication.UserDeletionRequestedEvent)
    // Cascade deletion across all repositories...
    return nil
})
```

### Security Event Retention

When `WithSecurityEvents` is configured, the `Authenticator` writes events containing IP addresses and User-Agent strings. These are personal data under GDPR (CJEU *Breyer v. Germany*, C-582/14). Your application must:

- Define a retention period (6-12 months is typical for security logs)
- Implement periodic cleanup (e.g. a `sdk/workers` job that deletes events older than the retention period)
- Document the lawful basis as **legitimate interest** (Art. 6(1)(f)) — do not use consent, as withdrawal would defeat the security purpose
- Include IP/UA logging in your privacy notice

### Email Delivery

The `Authenticator` emits events but does not send emails. Subscribe to these events in your bridge layer:

| Event | Action |
|---|---|
| `auth.verification_code_requested` | Send 6-digit code to user's email |
| `auth.password_reset_requested` | Send reset link (with token) to user's email |
| `auth.oauth_link_verification_requested` | Send 6-digit code for OAuth link confirmation |
| `auth.user_deletion_requested` | Cascade data cleanup (see above) |

### Client Info Injection

For security events to include IP and User-Agent, wire the `httpmid.ClientInfo` middleware into your global middleware stack:

```go
handler.Use(
    httpmid.TrustProxies(1),  // resolve real IP behind proxy (optional)
    httpmid.ClientInfo(),      // inject IP + User-Agent into auth context
)
```

`ClientInfo` uses the IP resolved by `TrustProxies` if available, otherwise falls back to `RemoteAddr`. Without this middleware, security events will have empty IP/UA fields.

### Choosing the Right Auth Check

| Method | DB Hit | Revocation Lag | Use For |
|---|---|---|---|
| `AuthenticateJWT` | No | Up to `AccessTokenExpiry` (default 30m) | Read-only endpoints, low-sensitivity |
| `AuthenticateSession` | Yes | None | Writes, account changes, sensitive reads |

After a password reset or session revocation, `AuthenticateJWT` will still accept the old JWT until it expires. Sensitive endpoints **must** use `AuthenticateSession`.

### OAuth Redirect URI Allowlist

If OAuth is enabled, configure `WithAllowedRedirectURIs` in production. Without it, any redirect URI is accepted — this is an open redirect vulnerability.

```go
authentication.WithAllowedRedirectURIs(
    "https://app.example.com/auth/callback",
    "myapp://auth/callback", // mobile deep link
)
```

### Session Limits

The `Authenticator` does not limit concurrent sessions per user. If this matters for your security posture, implement session eviction in your bridge layer (e.g. delete the oldest session when a new login exceeds the limit).

## What the Package Handles

You do **not** need to implement any of this — it's built in:

- Email enumeration prevention (registration + password reset)
- Timing attack mitigation (constant-time hash comparison, dummy work on unknown emails)
- Refresh token rotation with reuse detection (revokes all sessions on replay)
- Verification code brute-force lockout (`MaxVerificationAttempts`)
- Session revocation on password reset
- PKCE, state, nonce, and flow secret for OAuth
- OAuth account linking with email verification (prevents account takeover)
- Prevention of unlinking the last authentication method
- Encrypted provider token storage (via `WithProviderTokens`)
- Security event logging with client info (via `WithSecurityEvents`)
