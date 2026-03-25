package httpmid

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gopernicus/gopernicus/core/auth/authentication"
)

// ---------------------------------------------------------------------------
// Authenticator interfaces — decouple middleware from concrete auth service
// ---------------------------------------------------------------------------

// JWTAuthenticator verifies JWT tokens. Implemented by
// [authentication.Authenticator] via its AuthenticateJWT method.
type JWTAuthenticator interface {
	// AuthenticateJWT verifies the JWT signature and extracts claims.
	// No database queries — fast path for read-only endpoints.
	AuthenticateJWT(ctx context.Context, token string) (authentication.Claims, error)

	// SessionTokenName returns the cookie name for session tokens
	// (e.g. "myapp_session"). Used to extract tokens from cookies
	// when no Authorization header is present.
	SessionTokenName() string
}

// SessionAuthenticator extends JWTAuthenticator with full session validation.
// Implemented by [authentication.Authenticator] via its AuthenticateSession method.
type SessionAuthenticator interface {
	JWTAuthenticator

	// AuthenticateSession verifies the JWT, looks up the session in the
	// database, and checks that the user account is active.
	// Returns the user and session records for context storage.
	AuthenticateSession(ctx context.Context, token string) (authentication.User, authentication.Session, error)
}

// APIKeyAuthenticator validates API keys. Implemented by the generated auth
// service when API key support is enabled. See roadmap item #13.
type APIKeyAuthenticator interface {
	// AuthenticateAPIKey validates an API key and returns the ID of the
	// service account that owns it.
	AuthenticateAPIKey(ctx context.Context, key string) (serviceAccountID string, err error)
}

// ---------------------------------------------------------------------------
// Middleware options
// ---------------------------------------------------------------------------
//
// | Option               | Accepts        | Validation              | Context Data                         |
// |----------------------|----------------|-------------------------|--------------------------------------|
// | (default)            | JWT or API key | JWT signature / API key | Subject, SubjectType                 |
// | UserOnly()           | JWT only       | Signature only (fast)   | Subject, SubjectType                 |
// | WithUserSession()    | JWT only       | DB session validation   | Subject, SubjectType, User, Session  |
// | ServiceAccountOnly() | API key only   | API key hash lookup     | Subject, SubjectType                 |

// AuthOption configures the Authenticate middleware.
type AuthOption func(*authConfig)

type authConfig struct {
	fullSession        bool
	userOnly           bool
	serviceAccountOnly bool
}

// UserOnly configures the middleware to only accept user JWTs.
// API keys are rejected. Only the JWT signature is validated (fast, no DB
// queries). Use this for user-specific endpoints that should never be called
// by service accounts.
func UserOnly() AuthOption {
	return func(c *authConfig) { c.userOnly = true }
}

// ServiceAccountOnly configures the middleware to only accept API keys.
// User JWTs are rejected. The authenticator must implement
// [APIKeyAuthenticator]. Use this for webhook endpoints or
// machine-to-machine APIs.
func ServiceAccountOnly() AuthOption {
	return func(c *authConfig) { c.serviceAccountOnly = true }
}

// WithUserSession configures the middleware to validate the user's session in
// the database and load full [authentication.User] and [authentication.Session]
// into the request context.
//
// This implies [UserOnly] — API keys are rejected because they do not have
// associated session data.
//
// The authenticator must implement [SessionAuthenticator].
//
// Without this option, only the JWT signature is validated (fast, no DB
// queries). With this option, the user and session are available via
// [GetUser] and [GetSession].
//
// Use this for:
//   - Auth endpoints (password change, logout, /me)
//   - Endpoints that need full user data
//   - When you need immediate session revocation detection
func WithUserSession() AuthOption {
	return func(c *authConfig) {
		c.fullSession = true
		c.userOnly = true
	}
}

// ---------------------------------------------------------------------------
// Core authentication check — no HTTP response, no logging
// ---------------------------------------------------------------------------

// authFailureKind describes why authentication failed.
type authFailureKind int

const (
	authFailMissingToken   authFailureKind = iota // no Authorization header or cookie
	authFailWrongTokenType                        // JWT when SA required, or API key when user required
	authFailInvalidToken                          // token validation failed
	authFailInternal                              // authenticator missing required interface
)

// authFailure is returned by checkAuthentication when authentication fails.
type authFailure struct {
	kind    authFailureKind
	err     error  // underlying error (nil for missing token / wrong type)
	message string // log message describing the failure
}

// errorKind maps an auth failure to the corresponding ErrorKind.
func (f *authFailure) errorKind() ErrorKind {
	switch f.kind {
	case authFailInternal:
		return ErrKindInternal
	default:
		return ErrKindUnauthenticated
	}
}

// checkAuthentication performs the core authentication check without writing
// any HTTP response or logging. Returns the enriched context on success, or
// (nil, failure) describing why authentication failed.
func checkAuthentication(
	ctx context.Context,
	authenticator JWTAuthenticator,
	token string,
	cfg *authConfig,
) (context.Context, *authFailure) {
	if token == "" {
		return nil, &authFailure{kind: authFailMissingToken, message: "missing authorization"}
	}

	if IsJWT(token) {
		return checkJWT(ctx, authenticator, token, cfg)
	}
	return checkAPIKey(ctx, authenticator, token, cfg)
}

// checkJWT handles the JWT authentication path.
func checkJWT(
	ctx context.Context,
	authenticator JWTAuthenticator,
	token string,
	cfg *authConfig,
) (context.Context, *authFailure) {
	if cfg.serviceAccountOnly {
		return nil, &authFailure{
			kind:    authFailWrongTokenType,
			message: "JWT provided but service account authentication required",
		}
	}

	if cfg.fullSession {
		sv, ok := authenticator.(SessionAuthenticator)
		if !ok {
			return nil, &authFailure{
				kind:    authFailInternal,
				message: "WithUserSession requires SessionAuthenticator interface",
			}
		}

		user, session, err := sv.AuthenticateSession(ctx, token)
		if err != nil {
			return nil, &authFailure{
				kind:    authFailInvalidToken,
				err:     err,
				message: "session validation failed",
			}
		}

		ctx = SetSubject(ctx, fmt.Sprintf("user:%s", user.UserID))
		ctx = SetSubjectType(ctx, SubjectTypeUser)
		ctx = SetSessionID(ctx, session.SessionID)
		ctx = SetUser(ctx, &user)
		ctx = SetSession(ctx, &session)
		return ctx, nil
	}

	// Fast path — JWT signature only
	claims, err := authenticator.AuthenticateJWT(ctx, token)
	if err != nil {
		return nil, &authFailure{
			kind:    authFailInvalidToken,
			err:     err,
			message: "JWT validation failed",
		}
	}

	ctx = SetSubject(ctx, fmt.Sprintf("user:%s", claims.UserID))
	ctx = SetSubjectType(ctx, SubjectTypeUser)
	return ctx, nil
}

// checkAPIKey handles the API key authentication path.
func checkAPIKey(
	ctx context.Context,
	authenticator JWTAuthenticator,
	token string,
	cfg *authConfig,
) (context.Context, *authFailure) {
	if cfg.userOnly {
		return nil, &authFailure{
			kind:    authFailWrongTokenType,
			message: "API key provided but user authentication required",
		}
	}

	apk, ok := authenticator.(APIKeyAuthenticator)
	if !ok {
		return nil, &authFailure{
			kind:    authFailInternal,
			message: "API key received but authenticator does not implement APIKeyAuthenticator",
		}
	}

	serviceAccountID, err := apk.AuthenticateAPIKey(ctx, token)
	if err != nil {
		return nil, &authFailure{
			kind:    authFailInvalidToken,
			err:     err,
			message: "API key validation failed",
		}
	}

	ctx = SetSubject(ctx, fmt.Sprintf("service_account:%s", serviceAccountID))
	ctx = SetSubjectType(ctx, SubjectTypeServiceAccount)
	return ctx, nil
}

// logAuthFailure writes structured log entries for authentication failures.
func logAuthFailure(log *slog.Logger, ctx context.Context, f *authFailure) {
	switch f.kind {
	case authFailInternal:
		log.ErrorContext(ctx, "auth middleware: "+f.message)
	case authFailMissingToken:
		// No log — missing tokens are routine (unauthenticated endpoints, health checks).
	default:
		attrs := []any{"message", f.message}
		if f.err != nil {
			attrs = append(attrs, "error", f.err)
		}
		log.WarnContext(ctx, "auth middleware: authentication failed", attrs...)
	}
}

// ---------------------------------------------------------------------------
// Authenticate middleware
// ---------------------------------------------------------------------------

// Authenticate returns HTTP middleware that validates the request's
// authentication token. Tokens are extracted from the Authorization header
// first, falling back to the session cookie (named by the authenticator).
//
// The [ErrorRenderer] controls how errors are presented — pass [JSONErrors]
// for API routes or a custom renderer for HTML routes.
//
// By default it accepts both JWTs and API keys. The token type is
// auto-detected: JWTs have three base64url parts separated by dots; anything
// else is treated as an API key.
//
// Use options to restrict or customize behavior:
//   - [UserOnly]: Only accept user JWTs (reject API keys), signature validation only
//   - [WithUserSession]: Like UserOnly but also validates session in DB and loads user data
//   - [ServiceAccountOnly]: Only accept API keys (reject JWTs)
//
// Context values set:
//   - Subject: "user:{id}" or "service_account:{id}" (always)
//   - SubjectType: SubjectTypeUser or SubjectTypeServiceAccount (always)
//   - SessionID: only with [WithUserSession]
//   - User/Session: only with [WithUserSession]
//
// Example usage:
//
//	jsonErrs := httpmid.JSONErrors{}
//
//	// Default — accept both JWT and API key
//	mux.Handle("GET /api/posts", httpmid.Authenticate(auth, log, jsonErrs)(listPosts))
//
//	// User JWT only, signature validation only
//	mux.Handle("GET /api/me/settings", httpmid.Authenticate(auth, log, jsonErrs, httpmid.UserOnly())(getSettings))
//
//	// HTML routes with custom error rendering
//	mux.Handle("GET /dashboard", httpmid.Authenticate(auth, log, htmlErrs)(dashboardHandler))
func Authenticate(authenticator JWTAuthenticator, log *slog.Logger, errors ErrorRenderer, opts ...AuthOption) func(http.Handler) http.Handler {
	cfg := &authConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	cookieName := authenticator.SessionTokenName()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			token := extractToken(r, cookieName)

			enrichedCtx, failure := checkAuthentication(ctx, authenticator, token, cfg)
			if failure != nil {
				logAuthFailure(log, ctx, failure)
				errors.RenderError(w, r, failure.errorKind())
				return
			}

			next.ServeHTTP(w, r.WithContext(enrichedCtx))
		})
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// extractToken gets the token from the Authorization header first, falling
// back to the named session cookie. Returns "" if no token is found.
func extractToken(r *http.Request, cookieName string) string {
	// Always try Authorization header first.
	if token := ExtractBearerToken(r); token != "" {
		return token
	}

	// Fall back to session cookie.
	if cookieName != "" {
		if cookie, err := r.Cookie(cookieName); err == nil && cookie.Value != "" {
			return cookie.Value
		}
	}

	return ""
}

// ExtractBearerToken parses the Authorization header for a Bearer token.
// Returns "" if the header is missing or not in Bearer format.
func ExtractBearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	parts := strings.SplitN(h, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return parts[1]
}

// IsJWT returns true if the token looks like a JWT (three base64url parts
// separated by dots). Used to distinguish JWTs from opaque API keys.
func IsJWT(token string) bool {
	return strings.Count(token, ".") == 2
}
