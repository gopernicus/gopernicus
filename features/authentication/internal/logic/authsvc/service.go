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
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/apikey"
	"github.com/gopernicus/gopernicus/features/authentication/domain/oauthaccount"
	"github.com/gopernicus/gopernicus/features/authentication/domain/oauthstate"
	"github.com/gopernicus/gopernicus/features/authentication/domain/securityevent"
	"github.com/gopernicus/gopernicus/features/authentication/domain/serviceaccount"
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
	"github.com/gopernicus/gopernicus/features/authentication/domain/verification"
	"github.com/gopernicus/gopernicus/features/authentication/internal/redirect"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/email"
	"github.com/gopernicus/gopernicus/sdk/capabilities/oauth"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
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
	// defaultTokenTTL is the bearer-JWT lifetime when Config.TokenTTL is unset
	// (design §4.4). Kept short: it bounds the JWT revocation-asymmetry window.
	defaultTokenTTL = time.Hour
)

// ErrRateLimited is returned by Login when the per-(email, IP) attempt budget is
// exhausted. It is deliberately distinct from the invalid-credentials error
// (sdk.ErrUnauthorized) so the transport can map it to 429 and clients can back
// off. Checked with errors.Is.
var ErrRateLimited = errors.New("too many login attempts")

// ErrEmailNotVerified is returned by Login when Config.RequireVerifiedEmail is
// set and the caller's email is unverified. It wraps sdk.ErrForbidden so the
// transport maps it to 403 (design §7.1). The public auth package re-exports it
// as auth.ErrEmailNotVerified. Checked with errors.Is.
var ErrEmailNotVerified = fmt.Errorf("email not verified: %w", sdk.ErrForbidden)

// Hasher hashes and verifies passwords. auth.PasswordHasher satisfies it
// structurally; it is declared here rather than imported from the auth package
// so the internal service carries no import cycle with its own host-facing
// package. Accept interfaces, return structs.
type Hasher interface {
	HashPassword(password string) (string, error)
	VerifyPassword(hash, password string) error
}

// invitationResolver is the SINGLE narrow port authsvc holds on the sibling
// invitationsvc (design §6 pin): the register/verify flow calls it to grant a
// just-registered/verified email's pending auto-accept invitations. It is
// declared here (structural) so authsvc carries NO import edge to invitationsvc
// and never holds the Granter. Nil → invitations are off (a no-op).
type invitationResolver interface {
	ResolveInvitations(ctx context.Context, email, subjectType, subjectID string) (int, error)
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
	Logger    *slog.Logger     // nil → slog.Default(); used only for best-effort WARN lines
	// IDs is the app-chosen entity-ID strategy (amended D9): it mints the keys
	// of users, service accounts, API-key records, and security events. The
	// zero value generates default nanoids; cryptids.Database delegates to the
	// store. It NEVER mints secrets — session tokens, verification codes, API
	// key material, and PKCE/nonce values keep their own unconditional random
	// generator regardless of this strategy.
	IDs cryptids.IDGenerator
	// RequireVerifiedEmail, when true, makes Login refuse an unverified user
	// with ErrEmailNotVerified (403). Default false (design §7.1, AV8).
	RequireVerifiedEmail bool

	// SecurityEvents is the optional append-only audit rail (design §5.1,
	// ratified AV9). Nil → no audit trail: the synchronous recording site is a
	// documented no-op. When wired, every sensitive op records synchronously and
	// a write failure is logged at WARN, never failing the auth flow.
	SecurityEvents securityevent.SecurityEventRepository

	// Invitations is the optional resolve-on-registration collaborator (design
	// §6). Nil when invitations are off; when wired (by package auth), Register
	// and Verify grant a just-registered/verified email's pending auto-accept
	// invitations best-effort. It is the ONE port authsvc holds on invitationsvc.
	Invitations invitationResolver

	// OAuth flow collaborators (design §3). Providers empty → the OAuth
	// subsystem is off and its routes are not registered (deny-by-absence); the
	// two oauth repositories and TokenEncrypter may then be nil. When Providers
	// is non-empty the auth package requires both oauth repositories at
	// construction (auth.ErrOAuthReposRequired).
	OAuthAccounts     oauthaccount.OAuthAccountRepository
	OAuthStates       oauthstate.StateRepository
	Providers         []oauth.Provider
	TokenEncrypter    cryptids.Encrypter // nil → provider tokens are dropped (not persisted)
	OAuthCallbackBase string
	RedirectAllowlist []string

	// Machine-identity collaborators (design §4.1). Both nil → the API-key /
	// service-account subsystem is off (routes not registered, the bearer
	// API-key path is inert). The auth package enforces both-or-neither at
	// construction (auth.ErrMachineReposRequired).
	ServiceAccounts serviceaccount.ServiceAccountRepository
	APIKeys         apikey.APIKeyRepository

	// JWT bearer mode (design §4.4, AV6). TokenSigner nil → the mode is off:
	// bearer JWTs are never parsed and POST /auth/token is not registered.
	// TokenTTL is the minted-token lifetime; ≤0 → defaultTokenTTL (1h).
	TokenSigner cryptids.JWTSigner
	TokenTTL    time.Duration
}

// Service implements the auth use cases over the repository ports.
type Service struct {
	users                user.UserRepository
	passwords            user.PasswordRepository
	sessions             session.SessionRepository
	codes                verification.CodeRepository
	tokens               verification.TokenRepository
	hasher               Hasher
	mailer               email.Sender
	mailFrom             string
	limiter              ratelimiter.Limiter
	cookie               CookieConfig
	now                  func() time.Time
	logger               *slog.Logger
	requireVerifiedEmail bool
	// ids is the app-chosen entity-ID strategy (Deps.IDs); zero value → default
	// nanoids. Entity keys only, never secrets.
	ids cryptids.IDGenerator
	// securityEvents is the optional append-only audit rail (design §5.1). Nil →
	// the recordSecurityEvent helper is a no-op (ratified AV9).
	securityEvents securityevent.SecurityEventRepository
	// invitations is the optional resolve-on-registration collaborator (design
	// §6). Nil → resolvePendingInvitations is a no-op.
	invitations invitationResolver
	// tokenHasher is the fixed SHA-256 hasher for session cookie tokens. It is
	// an internal implementation detail, not a host-provided port: the stored
	// session value is always the SHA-256 hash of the cookie (design §7.3).
	tokenHasher *cryptids.SHA256Hasher

	// OAuth flow state (design §3). providers is keyed by Provider.Name();
	// oauthAccounts/oauthStates are the flow's repositories; tokenEncrypter is
	// nil when provider tokens are dropped; redirects guards post-flow
	// destinations. All are zero/empty when the subsystem is off.
	oauthAccounts  oauthaccount.OAuthAccountRepository
	oauthStates    oauthstate.StateRepository
	providers      map[string]oauth.Provider
	tokenEncrypter cryptids.Encrypter
	callbackBase   string
	redirects      redirect.Allowlist

	// Machine-identity state (design §4.1). Both nil when the subsystem is off.
	serviceAccounts serviceaccount.ServiceAccountRepository
	apiKeys         apikey.APIKeyRepository

	// JWT bearer mode (design §4.4). tokenSigner nil → the mode is off (bearer
	// JWTs never parsed, POST /auth/token not registered). tokenTTL is the
	// resolved minted-token lifetime (default applied in NewService).
	tokenSigner cryptids.JWTSigner
	tokenTTL    time.Duration
}

// NewService builds a Service from its dependencies, applying cookie name/path
// defaults and a time.Now clock when unset.
func NewService(d Deps) *Service {
	clock := d.Clock
	if clock == nil {
		clock = time.Now
	}
	logger := d.Logger
	if logger == nil {
		logger = slog.Default()
	}
	cookie := d.Cookie
	if cookie.Name == "" {
		cookie.Name = "session"
	}
	if cookie.Path == "" {
		cookie.Path = "/"
	}
	providers := make(map[string]oauth.Provider, len(d.Providers))
	for _, p := range d.Providers {
		providers[p.Name()] = p
	}
	tokenTTL := d.TokenTTL
	if tokenTTL <= 0 {
		tokenTTL = defaultTokenTTL
	}
	return &Service{
		users:                d.Users,
		passwords:            d.Passwords,
		sessions:             d.Sessions,
		codes:                d.Codes,
		tokens:               d.Tokens,
		hasher:               d.Hasher,
		mailer:               d.Mailer,
		mailFrom:             d.MailFrom,
		limiter:              d.Limiter,
		cookie:               cookie,
		now:                  clock,
		logger:               logger,
		requireVerifiedEmail: d.RequireVerifiedEmail,
		ids:                  d.IDs,
		securityEvents:       d.SecurityEvents,
		invitations:          d.Invitations,
		tokenHasher:          cryptids.NewSHA256Hasher(),
		oauthAccounts:        d.OAuthAccounts,
		oauthStates:          d.OAuthStates,
		providers:            providers,
		tokenEncrypter:       d.TokenEncrypter,
		callbackBase:         d.OAuthCallbackBase,
		redirects:            redirect.New(d.RedirectAllowlist),
		serviceAccounts:      d.ServiceAccounts,
		apiKeys:              d.APIKeys,
		tokenSigner:          d.TokenSigner,
		tokenTTL:             tokenTTL,
	}
}

// Register creates an unverified user, stores the password hash, issues an
// email-verification code, and mails it. The user is persisted before the code
// and email, so a mail failure still leaves an account the user can later
// verify (the error is returned so the caller knows delivery failed). A
// duplicate email surfaces as sdk.ErrAlreadyExists from the user store.
func (s *Service) Register(ctx context.Context, emailAddr, password, displayName string) (user.User, error) {
	if err := validatePassword(password); err != nil {
		return user.User{}, err
	}
	now := s.now()
	u, err := user.NewUser(s.ids, emailAddr, displayName, now)
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
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID: created.ID,
		Type:   securityevent.TypeRegister,
		Status: securityevent.StatusSuccess,
	})
	s.resolvePendingInvitations(ctx, created.Email, created.ID)
	if err := s.sendVerificationEmail(ctx, created, code.Code); err != nil {
		return created, err
	}
	return created, nil
}

// Verify consumes an email-verification code: it marks the code's user verified
// and deletes the code. Unknown/expired codes surface sdk.ErrNotFound /
// sdk.ErrExpired from the store.
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
	if err := s.codes.Delete(ctx, code); err != nil && !errors.Is(err, sdk.ErrNotFound) {
		return err
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID: u.ID,
		Type:   securityevent.TypeEmailVerified,
		Status: securityevent.StatusSuccess,
	})
	s.resolvePendingInvitations(ctx, u.Email, u.ID)
	return nil
}

// resolvePendingInvitations grants a just-registered/verified email's pending
// auto-accept invitations, best-effort (design §6): a nil collaborator (off) or
// any error never affects the register/verify outcome — one failed grant never
// aborts registration. The invitation service audits each grant/failure itself;
// here a resolve error is a coarse WARN line. Called from both Register (so a
// no-verify host still resolves) and Verify; a second pass is a no-op because
// resolved invitations move off pending.
func (s *Service) resolvePendingInvitations(ctx context.Context, email, userID string) {
	if s.invitations == nil {
		return
	}
	if _, err := s.invitations.ResolveInvitations(ctx, email, PrincipalUser, userID); err != nil {
		s.logger.Warn("resolve invitations failed", "error_kind", errKind(err))
	}
}

// EmailForUser returns the normalized email for userID. The invitation HTTP
// handlers key "mine" and the accept identifier check on the caller's own
// address through it, so invitationsvc stays decoupled from the user store (the
// auth feature owns user identity). Unknown id → the store's sdk.ErrNotFound.
func (s *Service) EmailForUser(ctx context.Context, userID string) (string, error) {
	u, err := s.users.Get(ctx, userID)
	if err != nil {
		return "", err
	}
	return u.Email, nil
}

// Login rate-limits FIRST on (email, client-IP), then verifies the password and
// mints a session, returning the plaintext cookie token (the stored session
// value is that token's hash — see mintSession). Rate-limit exhaustion returns
// ErrRateLimited; any credential mismatch (unknown email, missing password,
// wrong password) returns the same generic sdk.ErrUnauthorized so the response
// cannot distinguish them.
//
// When Config.RequireVerifiedEmail is set, a caller with correct credentials but
// an unverified email is refused with ErrEmailNotVerified (403) — the check runs
// AFTER password verification so it never leaks a verified/unverified signal to
// an unauthenticated attacker. Default off (design §7.1).
//
// The rate-limit IP is read from the request's client-info carrier (WithClientInfo,
// set by the feature middleware) — the single source of truth for IP (design §5.1
// WI4); there is no clientIP parameter. Every exit records a security event: a
// rate-limited attempt is `blocked`, a credential/verification denial is
// `failure`, and a minted session is `success`.
func (s *Service) Login(ctx context.Context, emailAddr, password string) (string, user.User, error) {
	clientIP := clientInfoFromContext(ctx).ip
	normalized, err := user.NormalizeEmail(emailAddr)
	if err != nil {
		s.recordLogin(ctx, "", emailAddr, securityevent.StatusFailure)
		return "", user.User{}, invalidCredentials()
	}

	res, err := s.limiter.Allow(ctx, loginKey(normalized, clientIP), ratelimiter.PerMinute(loginAttemptsPerMinute))
	if err != nil {
		return "", user.User{}, err
	}
	if !res.Allowed {
		s.recordLogin(ctx, "", normalized, securityevent.StatusBlocked)
		return "", user.User{}, ErrRateLimited
	}

	u, err := s.users.GetByEmail(ctx, normalized)
	if err != nil {
		s.recordLogin(ctx, "", normalized, securityevent.StatusFailure)
		return "", user.User{}, invalidCredentials()
	}
	hash, err := s.passwords.Get(ctx, u.ID)
	if err != nil {
		s.recordLogin(ctx, u.ID, normalized, securityevent.StatusFailure)
		return "", user.User{}, invalidCredentials()
	}
	if err := s.hasher.VerifyPassword(hash, password); err != nil {
		s.recordLogin(ctx, u.ID, normalized, securityevent.StatusFailure)
		return "", user.User{}, invalidCredentials()
	}
	if s.requireVerifiedEmail && !u.EmailVerified {
		s.recordLogin(ctx, u.ID, normalized, securityevent.StatusFailure)
		return "", user.User{}, ErrEmailNotVerified
	}

	token, _, err := s.mintSession(ctx, u.ID)
	if err != nil {
		return "", user.User{}, err
	}
	s.recordLogin(ctx, u.ID, normalized, securityevent.StatusSuccess)
	return token, u, nil
}

// recordLogin appends a `login` audit row. The attempted email is an identifier
// (never a secret), so it rides Details to make failed-login auditing useful; a
// resolved userID is attributed when one is known.
func (s *Service) recordLogin(ctx context.Context, userID, email, status string) {
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID:  userID,
		Type:    securityevent.TypeLogin,
		Status:  status,
		Details: map[string]any{"email": email},
	})
}

// Logout deletes the session for the cookie token. A blank token, or one already
// gone, is not an error (logout is idempotent). The token is hashed before the
// delete so it matches the stored value (design §7.3).
func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	hashed, err := s.hashSessionToken(token)
	if err != nil {
		return err
	}
	if err := s.sessions.Delete(ctx, hashed); err != nil && !errors.Is(err, sdk.ErrNotFound) {
		return err
	}
	// The user id is best-effort: RequireUser stashes it on the handler's ctx, so
	// a session-gated logout attributes the row; a raw-token logout leaves it empty.
	uid, _ := s.CurrentUser(ctx)
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID: uid,
		Type:   securityevent.TypeLogout,
		Status: securityevent.StatusSuccess,
	})
	return nil
}

// ChangePassword verifies the current password, stores the new hash, then
// revokes ALL of the user's sessions and mints a fresh one for the caller,
// returning the new plaintext cookie token (design §7.2, amended 2026-07-07). A
// wrong current password returns sdk.ErrUnauthorized; a too-short new password
// returns sdk.ErrInvalidInput.
//
// Atomicity pin (design §7.2): once the new hash is stored, a DeleteByUser
// failure is RETURNED, never best-effort-logged — the password changed but stale
// sessions may survive, and that must surface to the operator.
func (s *Service) ChangePassword(ctx context.Context, userID, currentPassword, newPassword string) (string, error) {
	hash, err := s.passwords.Get(ctx, userID)
	if err != nil {
		return "", err
	}
	if err := s.hasher.VerifyPassword(hash, currentPassword); err != nil {
		return "", fmt.Errorf("current password is incorrect: %w", sdk.ErrUnauthorized)
	}
	if err := validatePassword(newPassword); err != nil {
		return "", err
	}
	newHash, err := s.hasher.HashPassword(newPassword)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	if err := s.passwords.Set(ctx, userID, newHash); err != nil {
		return "", err
	}
	if err := s.sessions.DeleteByUser(ctx, userID); err != nil {
		return "", err
	}
	token, _, err := s.mintSession(ctx, userID)
	if err != nil {
		return "", err
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID: userID,
		Type:   securityevent.TypePasswordChange,
		Status: securityevent.StatusSuccess,
	})
	return token, nil
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
		if errors.Is(err, sdk.ErrNotFound) {
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
// sdk.ErrNotFound / sdk.ErrExpired; a too-short password returns
// sdk.ErrInvalidInput.
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
	if err := s.tokens.Delete(ctx, token); err != nil && !errors.Is(err, sdk.ErrNotFound) {
		return err
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID: rec.UserID,
		Type:   securityevent.TypePasswordReset,
		Status: securityevent.StatusSuccess,
	})
	return nil
}

// ValidateSession returns the live session for the cookie token. A blank token
// returns sdk.ErrUnauthorized; unknown/expired tokens surface sdk.ErrNotFound
// / sdk.ErrExpired from the store. The token is hashed before the lookup so it
// matches the stored value (design §7.3).
func (s *Service) ValidateSession(ctx context.Context, token string) (session.Session, error) {
	if token == "" {
		return session.Session{}, fmt.Errorf("no session: %w", sdk.ErrUnauthorized)
	}
	hashed, err := s.hashSessionToken(token)
	if err != nil {
		return session.Session{}, err
	}
	return s.sessions.Get(ctx, hashed)
}

// RequireUser is HTTP middleware that gates next on a valid user credential. On
// a missing/invalid/expired credential it writes a 401 JSON error; on success it
// stashes the user id on the request context (read via CurrentUser) and calls
// next. It satisfies web.Middleware via the method value s.RequireUser.
//
// It keeps its exact v1 semantics — the session cookie resolves to a user — and
// gains one addition (design §4.3): when a TokenSigner is wired, an
// Authorization: Bearer JWT resolves to the same user identity. With no
// TokenSigner a bearer JWT is never parsed (A3 behavior unchanged).
func (s *Service) RequireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.resolveUserID(r)
		if !ok {
			writeUnauthorized(w)
			return
		}
		next.ServeHTTP(w, r.WithContext(identity.WithPrincipal(r.Context(), identity.Principal{Type: identity.User, ID: userID})))
	})
}

// resolveUserID resolves the request's user identity for RequireUser. When a
// TokenSigner is wired and the request carries a JWT-shaped bearer, the JWT is
// authoritative (a bad one denies, never falling through to the cookie —
// design §4.3/§4.4); otherwise it keeps RequireUser's v1 semantics: the session
// cookie resolves to its user. With no TokenSigner a bearer JWT is never parsed.
func (s *Service) resolveUserID(r *http.Request) (string, bool) {
	if s.tokenSigner != nil {
		if raw, ok := bearerToken(r); ok && isJWTToken(raw) {
			return s.verifyBearer(raw)
		}
	}
	c, err := r.Cookie(s.cookie.Name)
	if err != nil {
		return "", false
	}
	sess, err := s.ValidateSession(r.Context(), c.Value)
	if err != nil {
		return "", false
	}
	return sess.UserID, true
}

// CurrentUser returns the authenticated user id stashed by RequireUser, if any.
// It is the cross-feature identity port other features consume structurally
// (features/README.md §5's CurrentUser).
func (s *Service) CurrentUser(ctx context.Context) (string, bool) {
	p, ok := identity.FromContext(ctx)
	if !ok || p.Type != identity.User {
		return "", false
	}
	return p.ID, true
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

// mintSession creates a fresh session for userID, persists it under the HASHED
// token, and returns the plaintext cookie value alongside the stored session
// (whose Token field is the hash). It is the single mint path Login and
// ChangePassword share (design §7.3, plan-cut WI1/WI3); future OAuth-/JWT-minted
// sessions route through it too, so no call site persists a raw token.
func (s *Service) mintSession(ctx context.Context, userID string) (string, session.Session, error) {
	sess := session.NewSession(userID, s.sessionTTL(), s.now())
	token := sess.Token
	hashed, err := s.hashSessionToken(token)
	if err != nil {
		return "", session.Session{}, err
	}
	sess.Token = hashed
	created, err := s.sessions.Create(ctx, sess)
	if err != nil {
		return "", session.Session{}, err
	}
	return token, created, nil
}

// hashSessionToken returns the stored form of a session cookie token — its
// SHA-256 hex digest. This is the ONLY place a session token is hashed (design
// §7.3): mintSession's create, ValidateSession's get, and Logout's delete all
// route through it, so the plaintext cookie value never reaches a repository and
// no second call site can drift.
func (s *Service) hashSessionToken(token string) (string, error) {
	return s.tokenHasher.Hash(token)
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
	return fmt.Errorf("invalid email or password: %w", sdk.ErrUnauthorized)
}

// validatePassword enforces the minimum password length.
func validatePassword(pw string) error {
	if len(pw) < minPasswordLength {
		return fmt.Errorf("password must be at least %d characters: %w", minPasswordLength, sdk.ErrInvalidInput)
	}
	return nil
}

// RateLimitByIP returns middleware that throttles a PUBLIC route on the client
// IP, refusing with a 429 once the per-minute budget is spent. It reuses the
// feature's configured RateLimiter (the one Login uses) and reads the IP from
// the client-info carrier, so an unauthenticated route (invitation decline, the
// design §6 case) is protected with no new plumbing. A limiter error fails OPEN
// (the request proceeds) — the in-memory default never errors, and a public
// decline must not be blocked by a limiter outage.
func (s *Service) RateLimitByIP(keyPrefix string, perMinute int) web.Middleware {
	keyFunc := func(r *http.Request) string {
		return keyPrefix + ":" + clientInfoFromContext(r.Context()).ip
	}
	rejectFunc := func(w http.ResponseWriter, _ *http.Request, _ ratelimiter.Result) {
		writeTooManyRequests(w)
	}
	return ratelimiter.Middleware(s.limiter, ratelimiter.PerMinute(perMinute), keyFunc, rejectFunc)
}

// writeUnauthorized writes a 401 JSON error via the shared sdk responder, so
// RequireUser's rejection matches the feature's other error responses (FS9).
func writeUnauthorized(w http.ResponseWriter) {
	web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
}

// writeTooManyRequests writes a 429 JSON error via the shared sdk responder, so
// a rate-limited public route matches the feature's error shape (FS9).
func writeTooManyRequests(w http.ResponseWriter) {
	web.RespondJSONError(w, web.NewError(http.StatusTooManyRequests, "too many requests").WithCode("rate_limited"))
}
