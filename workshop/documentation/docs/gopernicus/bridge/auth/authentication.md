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
| `FrontendURL` | `FRONTEND_URL` | | Base URL for OAuth redirects and email links |
| `Environment` | `ENV` | `development` | When `production`, forces cookie secure |
| `AccessTokenExpiry` | `ACCESS_TOKEN_EXPIRY` | `30m` | JWT access token TTL |
| `RefreshTokenExpiry` | `REFRESH_TOKEN_EXPIRY` | `720h` | Refresh token TTL |
| `CallbackPrefix` | `AUTH_CALLBACK_PREFIX` | `/api/v1/auth` | Path prefix for OAuth callback URLs |
| `MobileRedirectURI` | `OAUTH_MOBILE_REDIRECT_URI` | | Custom scheme URI for mobile OAuth (e.g. `myapp://oauth-callback`) |

Options: `WithCookieSecure(bool)`, `WithFrontendURL(string)`.

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

Cookies use `HttpOnly`, `SameSite=Lax`, and a configurable `Secure` flag. Cookie names come from the Core authenticator (`SessionTokenName()`, `RefreshTokenName()`), so they stay consistent across the stack.

## Event Subscribers

`Subscribers` listens for auth events on the event bus and sends emails:

| Event | Email Template | Purpose |
|---|---|---|
| `VerificationCodeRequested` | `authentication:verification` | Send verification code |
| `PasswordResetRequested` | `authentication:password_reset` | Send reset link |
| `OAuthLinkVerificationRequested` | `authentication:oauth_link_verification` | Send OAuth link verification code |

Construction and registration:

```go
subs := authbridge.NewSubscribers(emailer, log, frontendURL)
subs.Register(bus)
```

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
| `subscribers.go` | Event subscribers for email delivery |
| `templates/` | Embedded HTML/text email templates |
