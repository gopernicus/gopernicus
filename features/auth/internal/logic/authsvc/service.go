// Package authsvc holds the auth feature's domain services: registration, email
// verification, login (rate-limited), logout, password change, password
// forgot/reset, and session validation — business rules over the repository
// ports, with no SQL. It is internal so it is not part of the feature's public
// SemVer surface; the host-facing surface is package auth (auth.go).
//
// Secret hygiene: passwords are only ever compared through the Hasher (bcrypt's
// compare is constant-time by construction); verification codes and reset
// tokens are opaque values matched by keyed lookup, so no secret is branched on
// byte-by-byte here. Secrets are never logged, and forgot-password never
// reveals whether an email is registered.
package authsvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gopernicus/gopernicus/features/auth/logic/session"
	"github.com/gopernicus/gopernicus/features/auth/logic/user"
	"github.com/gopernicus/gopernicus/features/auth/logic/verification"
	"github.com/gopernicus/gopernicus/sdk/email"
	"github.com/gopernicus/gopernicus/sdk/errs"
	"github.com/gopernicus/gopernicus/sdk/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/web"
)

const (
	// verificationCodeTTL is how long an email-verification code stays valid.
	verificationCodeTTL = 15 * time.Minute
	// passwordResetTTL is how long a password-reset token stays valid.
	passwordResetTTL = time.Hour
	// minPasswordLength is the minimum accepted password length.
	minPasswordLength = 8
	// defaultSessionTTL is used when the cookie config sets no positive MaxAge.
	defaultSessionTTL = 7 * 24 * time.Hour
	// loginAttemptsPerMinute caps failed+successful login attempts per
	// (email, client-IP) window before Login refuses with ErrRateLimited.
	loginAttemptsPerMinute = 5
)

// ErrRateLimited is returned by Login when the per-(email, IP) attempt budget is
// exhausted. It is deliberately distinct from the invalid-credentials error
// (errs.ErrUnauthorized) so the transport can map it to 429 and clients can back
// off. Checked with errors.Is.
var ErrRateLimited = errors.New("too many login attempts")

// Hasher hashes and verifies passwords. auth.PasswordHasher satisfies it
// structurally; it is declared here rather than imported from the auth package
// so the internal service carries no import cycle with its own host-facing
// package. Accept interfaces, return structs.
type Hasher interface {
	HashPassword(password string) (string, error)
	VerifyPassword(hash, password string) error
}

// CookieConfig is the resolved session-cookie policy. The auth package fills it
// from auth.Config.SessionCookie (applying name/path defaults).
type CookieConfig struct {
	Name   string
	Path   string
	Domain string
	Secure bool
	MaxAge int // seconds; also the session lifetime when > 0
}

// Deps are the collaborators the Service needs. The auth package builds this
// after validating the required fields and applying defaults.
type Deps struct {
	Users     user.UserRepository
	Passwords user.PasswordRepository
	Sessions  session.SessionRepository
	Codes     verification.CodeRepository
	Tokens    verification.TokenRepository
	Hasher    Hasher
	Mailer    email.Sender
	MailFrom  string
	Limiter   ratelimiter.Limiter
	Cookie    CookieConfig
	Clock     func() time.Time // nil → time.Now
}

// Service implements the auth use cases over the repository ports.
type Service struct {
	users     user.UserRepository
	passwords user.PasswordRepository
	sessions  session.SessionRepository
	codes     verification.CodeRepository
	tokens    verification.TokenRepository
	hasher    Hasher
	mailer    email.Sender
	mailFrom  string
	limiter   ratelimiter.Limiter
	cookie    CookieConfig
	now       func() time.Time
}

// NewService builds a Service from its dependencies, applying cookie name/path
// defaults and a time.Now clock when unset.
func NewService(d Deps) *Service {
	clock := d.Clock
	if clock == nil {
		clock = time.Now
	}
	cookie := d.Cookie
	if cookie.Name == "" {
		cookie.Name = "session"
	}
	if cookie.Path == "" {
		cookie.Path = "/"
	}
	return &Service{
		users:     d.Users,
		passwords: d.Passwords,
		sessions:  d.Sessions,
		codes:     d.Codes,
		tokens:    d.Tokens,
		hasher:    d.Hasher,
		mailer:    d.Mailer,
		mailFrom:  d.MailFrom,
		limiter:   d.Limiter,
		cookie:    cookie,
		now:       clock,
	}
}

// Register creates an unverified user, stores the password hash, issues an
// email-verification code, and mails it. The user is persisted before the code
// and email, so a mail failure still leaves an account the user can later
// verify (the error is returned so the caller knows delivery failed). A
// duplicate email surfaces as errs.ErrAlreadyExists from the user store.
func (s *Service) Register(ctx context.Context, emailAddr, password, displayName string) (user.User, error) {
	if err := validatePassword(password); err != nil {
		return user.User{}, err
	}
	now := s.now()
	u, err := user.NewUser(emailAddr, displayName, now)
	if err != nil {
		return user.User{}, err
	}
	hash, err := s.hasher.HashPassword(password)
	if err != nil {
		return user.User{}, fmt.Errorf("hash password: %w", err)
	}

	created, err := s.users.Create(ctx, u)
	if err != nil {
		return user.User{}, err
	}
	if err := s.passwords.Set(ctx, created.ID, hash); err != nil {
		return user.User{}, err
	}

	code := verification.NewCode(created.ID, verificationCodeTTL, now)
	if _, err := s.codes.Create(ctx, code); err != nil {
		return user.User{}, err
	}
	if err := s.sendVerificationEmail(ctx, created, code.Code); err != nil {
		return created, err
	}
	return created, nil
}

// Verify consumes an email-verification code: it marks the code's user verified
// and deletes the code. Unknown/expired codes surface errs.ErrNotFound /
// errs.ErrExpired from the store.
func (s *Service) Verify(ctx context.Context, code string) error {
	rec, err := s.codes.Get(ctx, code)
	if err != nil {
		return err
	}
	u, err := s.users.Get(ctx, rec.UserID)
	if err != nil {
		return err
	}
	u.MarkVerified(s.now())
	if _, err := s.users.Update(ctx, u.ID, u); err != nil {
		return err
	}
	if err := s.codes.Delete(ctx, code); err != nil && !errors.Is(err, errs.ErrNotFound) {
		return err
	}
	return nil
}

// Login rate-limits FIRST on (email, client-IP), then verifies the password and
// mints a session. Rate-limit exhaustion returns ErrRateLimited; any credential
// mismatch (unknown email, missing password, wrong password) returns the same
// generic errs.ErrUnauthorized so the response cannot distinguish them.
//
// v1 does not gate login on email verification: the verified flag is tracked
// but a user may sign in before confirming their address (documented v1 scope).
func (s *Service) Login(ctx context.Context, emailAddr, password, clientIP string) (session.Session, user.User, error) {
	normalized, err := user.NormalizeEmail(emailAddr)
	if err != nil {
		return session.Session{}, user.User{}, invalidCredentials()
	}

	res, err := s.limiter.Allow(ctx, loginKey(normalized, clientIP), ratelimiter.PerMinute(loginAttemptsPerMinute))
	if err != nil {
		return session.Session{}, user.User{}, err
	}
	if !res.Allowed {
		return session.Session{}, user.User{}, ErrRateLimited
	}

	u, err := s.users.GetByEmail(ctx, normalized)
	if err != nil {
		return session.Session{}, user.User{}, invalidCredentials()
	}
	hash, err := s.passwords.Get(ctx, u.ID)
	if err != nil {
		return session.Session{}, user.User{}, invalidCredentials()
	}
	if err := s.hasher.VerifyPassword(hash, password); err != nil {
		return session.Session{}, user.User{}, invalidCredentials()
	}

	sess := session.NewSession(u.ID, s.sessionTTL(), s.now())
	created, err := s.sessions.Create(ctx, sess)
	if err != nil {
		return session.Session{}, user.User{}, err
	}
	return created, u, nil
}

// Logout deletes the session for token. A token that is already gone is not an
// error (logout is idempotent).
func (s *Service) Logout(ctx context.Context, token string) error {
	if err := s.sessions.Delete(ctx, token); err != nil && !errors.Is(err, errs.ErrNotFound) {
		return err
	}
	return nil
}

// ChangePassword verifies the current password, then stores a new hash. A wrong
// current password returns errs.ErrUnauthorized; a too-short new password
// returns errs.ErrInvalidInput.
func (s *Service) ChangePassword(ctx context.Context, userID, currentPassword, newPassword string) error {
	hash, err := s.passwords.Get(ctx, userID)
	if err != nil {
		return err
	}
	if err := s.hasher.VerifyPassword(hash, currentPassword); err != nil {
		return fmt.Errorf("current password is incorrect: %w", errs.ErrUnauthorized)
	}
	if err := validatePassword(newPassword); err != nil {
		return err
	}
	newHash, err := s.hasher.HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	return s.passwords.Set(ctx, userID, newHash)
}

// ForgotPassword issues a reset token and mails it — but only when the email is
// registered. An unknown email returns nil with no side effects, so the caller
// cannot use this endpoint to enumerate accounts.
func (s *Service) ForgotPassword(ctx context.Context, emailAddr string) error {
	normalized, err := user.NormalizeEmail(emailAddr)
	if err != nil {
		return nil // never reveal validity/existence
	}
	u, err := s.users.GetByEmail(ctx, normalized)
	if err != nil {
		if errors.Is(err, errs.ErrNotFound) {
			return nil // never reveal whether the email exists
		}
		return err
	}
	tok := verification.NewToken(u.ID, passwordResetTTL, s.now())
	if _, err := s.tokens.Create(ctx, tok); err != nil {
		return err
	}
	return s.sendResetEmail(ctx, u, tok.Token)
}

// ResetPassword redeems a reset token: it stores the new password hash for the
// token's user and deletes the token. Unknown/expired tokens surface
// errs.ErrNotFound / errs.ErrExpired; a too-short password returns
// errs.ErrInvalidInput.
func (s *Service) ResetPassword(ctx context.Context, token, newPassword string) error {
	if err := validatePassword(newPassword); err != nil {
		return err
	}
	rec, err := s.tokens.Get(ctx, token)
	if err != nil {
		return err
	}
	newHash, err := s.hasher.HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	if err := s.passwords.Set(ctx, rec.UserID, newHash); err != nil {
		return err
	}
	if err := s.tokens.Delete(ctx, token); err != nil && !errors.Is(err, errs.ErrNotFound) {
		return err
	}
	return nil
}

// ValidateSession returns the live session for token. A blank token returns
// errs.ErrUnauthorized; unknown/expired tokens surface errs.ErrNotFound /
// errs.ErrExpired from the store.
func (s *Service) ValidateSession(ctx context.Context, token string) (session.Session, error) {
	if token == "" {
		return session.Session{}, fmt.Errorf("no session: %w", errs.ErrUnauthorized)
	}
	return s.sessions.Get(ctx, token)
}

// RequireUser is HTTP middleware that gates next on a valid session cookie. On a
// missing/invalid/expired session it writes a 401 JSON error; on success it
// stashes the user id on the request context (read via CurrentUser) and calls
// next. It satisfies web.Middleware via the method value s.RequireUser.
func (s *Service) RequireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(s.cookie.Name)
		if err != nil {
			writeUnauthorized(w)
			return
		}
		sess, err := s.ValidateSession(r.Context(), c.Value)
		if err != nil {
			writeUnauthorized(w)
			return
		}
		next.ServeHTTP(w, r.WithContext(withUserID(r.Context(), sess.UserID)))
	})
}

// CurrentUser returns the authenticated user id stashed by RequireUser, if any.
// It is the cross-feature identity port other features consume structurally
// (features/README.md §5's CurrentUser).
func (s *Service) CurrentUser(ctx context.Context) (string, bool) {
	return userIDFromContext(ctx)
}

// SetSessionCookie writes the session cookie carrying token per the cookie
// policy (HttpOnly + SameSite=Lax always; Secure/Domain/MaxAge from config).
func (s *Service) SetSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.cookie.Name,
		Value:    token,
		Path:     s.cookie.Path,
		Domain:   s.cookie.Domain,
		Secure:   s.cookie.Secure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   s.cookie.MaxAge,
	})
}

// ClearSessionCookie expires the session cookie on the client.
func (s *Service) ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.cookie.Name,
		Value:    "",
		Path:     s.cookie.Path,
		Domain:   s.cookie.Domain,
		Secure:   s.cookie.Secure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// SessionCookieName returns the configured session-cookie name.
func (s *Service) SessionCookieName() string { return s.cookie.Name }

// sessionTTL derives the session lifetime from the cookie MaxAge, falling back
// to defaultSessionTTL when MaxAge is not positive.
func (s *Service) sessionTTL() time.Duration {
	if s.cookie.MaxAge > 0 {
		return time.Duration(s.cookie.MaxAge) * time.Second
	}
	return defaultSessionTTL
}

func (s *Service) sendVerificationEmail(ctx context.Context, u user.User, code string) error {
	msg := email.Message{
		From:    s.mailFrom,
		To:      []string{u.Email},
		Subject: "Verify your email",
		Text:    "Your verification code is: " + code,
	}
	if err := s.mailer.Send(ctx, msg); err != nil {
		return fmt.Errorf("send verification email: %w", err)
	}
	return nil
}

func (s *Service) sendResetEmail(ctx context.Context, u user.User, token string) error {
	msg := email.Message{
		From:    s.mailFrom,
		To:      []string{u.Email},
		Subject: "Reset your password",
		Text:    "Use this token to reset your password: " + token,
	}
	if err := s.mailer.Send(ctx, msg); err != nil {
		return fmt.Errorf("send reset email: %w", err)
	}
	return nil
}

// loginKey derives the rate-limit key for a login attempt from the normalized
// email and the client IP.
func loginKey(normalizedEmail, clientIP string) string {
	return "login:" + normalizedEmail + "|" + clientIP
}

// invalidCredentials is the single generic error returned for every credential
// mismatch so the response cannot distinguish "no such user" from "wrong
// password".
func invalidCredentials() error {
	return fmt.Errorf("invalid email or password: %w", errs.ErrUnauthorized)
}

// validatePassword enforces the minimum password length.
func validatePassword(pw string) error {
	if len(pw) < minPasswordLength {
		return fmt.Errorf("password must be at least %d characters: %w", minPasswordLength, errs.ErrInvalidInput)
	}
	return nil
}

// writeUnauthorized writes a 401 JSON error mirroring the internal/http shape,
// so RequireUser's rejection matches the feature's other error responses.
func writeUnauthorized(w http.ResponseWriter) {
	e := web.ErrUnauthorized("authentication required")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.Status)
	_ = json.NewEncoder(w).Encode(e)
}
