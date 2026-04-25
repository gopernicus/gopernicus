---
sidebar_position: 1
title: Authentication
---

# Bridge — Authentication

The authentication bridge (`bridge/auth/authentication/`) exposes HTTP endpoints for login, registration, session management, password flows, and OAuth. It is a hand-written case bridge that translates HTTP requests into calls to the [Core Authentication](../../core/auth/authentication.md) package.

## Construction

```go
ab := authentication.New(log, cfg, authenticator, rateLimiter)
```

`Config` is populated from environment variables:

| Field | Env Var | Default | Purpose |
|---|---|---|---|
| `CookieSecure` | `AUTH_COOKIE_SECURE` | `false` | Set `Secure` flag on cookies (forced `true` in production) |
| `CookieDomain` | `AUTH_COOKIE_DOMAIN` | | Cookie `Domain` attribute for cross-subdomain sharing (e.g. `.example.com`) |
| `CallbackBaseURL` | `AUTH_CALLBACK_BASE_URL` | | Base URL for OAuth callbacks (e.g. `https://api.example.com`). If unset, derived from request headers (vulnerable to injection). |
| `Environment` | `ENV` | `development` | When `production`, forces cookie secure |
| `AccessTokenExpiry` | `ACCESS_TOKEN_EXPIRY` | `30m` | JWT access token TTL |
| `RefreshTokenExpiry` | `REFRESH_TOKEN_EXPIRY` | `720h` | Refresh token TTL |
| `CallbackPrefix` | `AUTH_CALLBACK_PREFIX` | `/api/v1/auth` | Path prefix for OAuth callback URLs |
| `MobileRedirectURI` | `OAUTH_MOBILE_REDIRECT_URI` | | Custom scheme URI for mobile OAuth (e.g. `myapp://oauth-callback`) |
| `AllowedFrontends` | `ALLOWED_FRONTENDS` | | Comma-separated origin allow-list for client-supplied URLs (e.g. `https://app.example.com,https://admin.example.com`). Required in production. |

Options: `WithCookieSecure(bool)`, `WithCookieDomain(string)`, `WithCallbackBaseURL(string)`, `WithAllowedFrontends([]string)`.

## Route Registration

```go
authGroup := handler.Group("/auth")
ab.AddHttpRoutes(authGroup, authMid)
```

`authMid` is the `Authenticate` middleware used for protected routes. Public routes use rate limiting instead — each with a scoped key prefix and per-minute limit.

## Endpoints

### Public (Rate Limited)

| Method | Path | Handler | Purpose |
|---|---|---|---|
| POST | `/login` | `httpLogin` | Email + password login |
| POST | `/register` | `httpRegister` | Create account |
| POST | `/verify-email` | `httpVerifyEmail` | Verify email with code |
| POST | `/verify-email/resend` | `httpResendVerification` | Resend verification code |
| POST | `/refresh` | `httpRefreshToken` | Refresh access token |
| POST | `/password-reset/initiate` | `httpInitiatePasswordReset` | Request password reset |
| POST | `/password-reset/confirm` | `httpResetPassword` | Reset password with token |

### Protected (Authenticated)

| Method | Path | Handler | Purpose |
|---|---|---|---|
| POST | `/password-change` | `httpChangePassword` | Change password (requires current) |
| POST | `/logout` | `httpLogout` | End session, clear cookies |
| GET | `/me` | `httpMe` | Current user + session (uses `WithUserSession`) |

### OAuth — Public (Rate Limited)

| Method | Path | Handler | Purpose |
|---|---|---|---|
| POST | `/oauth/initiate` | `httpOAuthInitiate` | Start OAuth flow, get authorization URL |
| GET | `/oauth/start/{provider}` | `httpOAuthStart` | Browser redirect to OAuth provider |
| GET | `/oauth/callback/{provider}` | `httpOAuthCallback` | Web OAuth callback — sets cookies, redirects |
| GET | `/oauth/mobile-redirect/{provider}` | `httpOAuthMobileRedirect` | Redirect proxy to mobile custom scheme |
| POST | `/oauth/callback/mobile/{provider}` | `httpOAuthMobileCallback` | Mobile OAuth callback — returns JSON tokens |
| POST | `/oauth/verify-link` | `httpOAuthVerifyLink` | Verify email code for OAuth account linking |

### OAuth — Protected (Authenticated)

| Method | Path | Handler | Purpose |
|---|---|---|---|
| POST | `/oauth/link` | `httpOAuthLink` | Link OAuth account to current user |
| GET | `/oauth/link/start/{provider}` | `httpOAuthLinkStart` | Browser redirect to link OAuth account |
| DELETE | `/oauth/unlink/{provider}` | `httpOAuthUnlink` | Unlink OAuth account |
| GET | `/oauth/linked` | `httpOAuthGetLinked` | List linked OAuth accounts |

## Dual-Client Design

The bridge supports both web (browser) and API/mobile clients simultaneously:

- **Web clients** use HTTP-only cookies. Login and OAuth callbacks set session and refresh cookies automatically. Logout clears them.
- **API/mobile clients** use the JSON response body. Login returns `access_token` and `refresh_token` in the response. The refresh endpoint accepts tokens from cookies, the `Authorization` header, or the request body — whichever is present.

OAuth has separate flows for each:
- **Web**: `GET /oauth/start/{provider}` redirects the browser to the provider, which calls back to `GET /oauth/callback/{provider}`. Cookies are set and the user is redirected to the frontend.
- **Mobile**: `POST /oauth/initiate` returns an `authorization_url` and a `flow_secret`. The mobile app opens the URL in a browser, which redirects through `/oauth/mobile-redirect/{provider}` back to the app's custom scheme. The app posts the code, state, and flow secret to `POST /oauth/callback/mobile/{provider}` and receives JSON tokens.

The `flow_secret` prevents URL scheme interception attacks — a malicious app that intercepts the custom scheme redirect cannot complete the flow without the secret.

## Anti-Enumeration

Several endpoints return identical responses regardless of outcome to prevent account enumeration:

- **Register** — always returns `"check your email for verification"`, even for duplicate emails
- **Resend verification** — always returns success
- **Password reset initiate** — always returns success
- **Login** — always returns 401 for any failure (bad password, unverified email, nonexistent account)

Errors are logged server-side for debugging.

## Session Cookies

Cookies use `HttpOnly`, `SameSite=Lax`, and configurable `Secure` and `Domain` flags. Cookie names come from the Core authenticator (`SessionTokenName()`, `RefreshTokenName()`), so they stay consistent across the stack.

Set `AUTH_COOKIE_DOMAIN` (e.g. `.example.com`) to share sessions across subdomains. When clearing cookies, the same domain must be used or browsers won't delete them.

## Multi-Frontend Support

When running multiple frontends against one auth backend (e.g. main app + admin dashboard on different subdomains), configure:

1. **`ALLOWED_FRONTENDS`** — comma-separated origins. Clients must send `reset_url` (password reset) and `app_origin` (OAuth) in requests; the bridge validates these against the allow-list and uses them for redirects/email links.

2. **`AUTH_COOKIE_DOMAIN`** — parent domain for cross-subdomain session sharing.

3. **`AUTH_CALLBACK_BASE_URL`** — explicit base URL for OAuth callbacks instead of deriving from request headers.

When `ALLOWED_FRONTENDS` is set (strict mode):
- `reset_url` is required in password reset requests
- `app_origin` is required in OAuth initiate requests
- Both are validated against the allow-list

When unset (legacy mode), client-supplied URLs are used without validation (logs a warning at startup).

## OAuth Redirect URI Allowlist

The `POST /oauth/initiate` endpoint accepts a client-supplied `redirect_uri`. To prevent open redirector attacks, configure the core authenticator with an exact-match allowlist. The bridge provides a helper to derive URIs from your configuration:

```go
// In app wiring, after loading config and before creating authenticator:
providers := []string{"google", "github"} // your configured providers

redirectURIs := authbridge.BuildAllowedRedirectURIs(
    cfg.Auth.CallbackBaseURL,  // e.g. "https://api.example.com"
    cfg.Auth.CallbackPrefix,   // e.g. "/api/v1/auth"
    providers,
)

authenticator := authentication.New(
    authCfg,
    userRepo,
    authentication.WithOAuth(providerMap, oauthRepo),
    authentication.WithAllowedRedirectURIs(redirectURIs...),
)
```

`BuildAllowedRedirectURIs` generates two URIs per provider:
- `{base}{prefix}/oauth/callback/{provider}` — web callback
- `{base}{prefix}/oauth/mobile-redirect/{provider}` — mobile redirect proxy

If `callbackBaseURL` is empty, returns nil (no allowlist configured).

## Event Subscribers

`Subscribers` listens for auth events on the event bus and sends emails:

| Event | Email Template | Purpose |
|---|---|---|
| `VerificationCodeRequested` | `authentication:verification` | Send verification code |
| `PasswordResetRequested` | `authentication:password_reset` | Send reset link |
| `OAuthLinkVerificationRequested` | `authentication:oauth_link_verification` | Send OAuth link verification code |

Construction and registration:

```go
// Derive fallback URL for email links from first allowed frontend.
// In strict mode (ALLOWED_FRONTENDS set), reset_url is required in requests
// and this fallback is not used. Pass empty string if not needed.
var frontendURL string
if len(cfg.Auth.AllowedFrontends) > 0 {
    frontendURL = cfg.Auth.AllowedFrontends[0]
}

subs := authbridge.NewSubscribers(emailer, log, frontendURL)
subs.Register(bus)
```

Alternatively, if you already have an `OriginMatcher` instance, use `matcher.Default()` which returns the first origin (canonicalized with explicit port).

Email templates are embedded in the package under `templates/` (HTML + plaintext pairs). Register them with the emailer during app wiring:

```go
emailer.WithContentTemplates("authentication", authbridge.AuthTemplates(), emailer.LayerCore)
```

## Files

| File | Purpose |
|---|---|
| `bridge.go` | `Bridge` struct, `Config`, `New()` constructor, options |
| `http.go` | `HttpRoutes()` and all non-OAuth handlers |
| `oauth.go` | OAuth handlers (initiate, callback, link, unlink) |
| `model.go` | Request/response types for core auth endpoints |
| `oauth_model.go` | Request/response types for OAuth endpoints |
| `cookie.go` | Cookie set/clear helpers |
| `allowlist.go` | `OriginMatcher` for validating client-supplied URLs |
| `subscribers.go` | Event subscribers for email delivery |
| `templates/` | Embedded HTML/text email templates |
