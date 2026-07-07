// Package auth is the public surface of the auth feature module: the
// registration entry point (Register), the cross-feature identity capability
// (Service / NewService / RequireUser / CurrentUser), the host-filled ports
// (Repositories), the feature-owned PasswordHasher port, and the customization
// config (Config). Implementation lives in internal/; the domain type and
// repository-interface packages (user, session, verification) are public
// because hosts and store adapters reference them, but the services and
// handlers stay internal.
//
// The feature is datastore-free and view-free: it depends on its repository
// ports and sdk facilities only, never on a concrete store, an integration, or
// a view library. v1 is JSON-API only (see internal/http).
//
// Host-facing surface, all in this file per the feature charter's "<name>.go is
// the feature's entire host-facing surface" rule:
//
//   - Repositories — the five outbound ports a store adapter or host fills.
//   - PasswordHasher — the feature-owned hashing port (integrations/cryptids/
//     bcrypt satisfies it structurally).
//   - Config — required Hasher + Mailer (nil errors at construction), optional
//     RateLimiter (nil → in-memory), MailFrom, SessionCookie.
//   - NewService / Service.RequireUser / Service.CurrentUser — the surface a
//     host wires into another feature (e.g. cms admin gating).
//   - Register — mounts the feature's own HTTP routes.
package auth

import (
	"context"
	"errors"
	"net/http"

	internalhttp "github.com/gopernicus/gopernicus/features/auth/internal/inbound/http"
	"github.com/gopernicus/gopernicus/features/auth/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/features/auth/logic/session"
	"github.com/gopernicus/gopernicus/features/auth/logic/user"
	"github.com/gopernicus/gopernicus/features/auth/logic/verification"
	"github.com/gopernicus/gopernicus/sdk/email"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/ratelimiter"
)

// ErrHasherRequired and ErrMailerRequired are returned by NewService/Register
// when the corresponding required Config field is nil. Unlike cms's safe
// silent defaults (nil Cache disables caching), a password feature with no
// hasher, or one that silently drops verification/reset mail, is a security
// foot-gun — so these degrade loudly at construction, never silently.
var (
	ErrHasherRequired = errors.New("auth: Config.Hasher is required")
	ErrMailerRequired = errors.New("auth: Config.Mailer is required")
)

// PasswordHasher hashes and verifies passwords. It is feature-owned (not an sdk
// facility) because it has one consumer today and none genuinely foreseen
// elsewhere. integrations/cryptids/bcrypt satisfies it structurally, with zero
// import in either direction.
type PasswordHasher interface {
	// HashPassword returns a self-describing hash of password.
	HashPassword(password string) (string, error)
	// VerifyPassword reports whether password matches hash; a mismatch returns
	// a non-nil error. Implementations must compare in constant time.
	VerifyPassword(hash, password string) error
}

// Repositories is the set of outbound ports the feature needs. A store adapter
// (e.g. features/auth/stores/turso) or a host fills it; the feature stays
// dialect-blind. Passwords is split from Users on purpose — credential material
// is stored and access-controlled independently of general user reads.
type Repositories struct {
	Users              user.UserRepository
	Passwords          user.PasswordRepository
	Sessions           session.SessionRepository
	VerificationCodes  verification.CodeRepository
	VerificationTokens verification.TokenRepository
}

// CookieConfig is the session-cookie policy. Zero values are safe: an empty Name
// defaults to "session" and an empty Path to "/". MaxAge is in seconds and also
// sets the session lifetime; a non-positive MaxAge yields a browser session
// cookie backed by a 7-day server session. Secure/Domain are host deployment
// choices (Secure should be true behind TLS). Cookies are always HttpOnly with
// SameSite=Lax.
type CookieConfig struct {
	Name   string
	Path   string
	Domain string
	Secure bool
	MaxAge int
}

// Config carries host-provided collaborators. The Hasher and Mailer are
// REQUIRED (nil → ErrHasherRequired / ErrMailerRequired at construction);
// everything else is optional with a safe default.
type Config struct {
	// Hasher is REQUIRED; nil → ErrHasherRequired.
	Hasher PasswordHasher
	// Mailer is REQUIRED; nil → ErrMailerRequired. Delivers verification and
	// password-reset messages.
	Mailer email.Sender
	// MailFrom is the From address on verification/reset mail.
	MailFrom string
	// RateLimiter throttles login attempts; nil → ratelimiter.NewMemory()
	// (safe-by-default: an in-process limiter, not "unlimited").
	RateLimiter ratelimiter.Limiter
	// SessionCookie configures the session cookie; the zero value is usable.
	SessionCookie CookieConfig
}

// Service is the auth feature's identity capability without HTTP routes — the
// surface a host wires into another feature (its RequireUser middleware, its
// CurrentUser port). It holds no mutable state beyond the shared Repositories/
// Config values.
type Service struct {
	svc *authsvc.Service
}

// NewService builds the auth Service, validating the required Config fields and
// defaulting a nil RateLimiter to an in-memory one. It does not mount HTTP
// routes (see Register for that).
func NewService(repos Repositories, cfg Config) (*Service, error) {
	if cfg.Hasher == nil {
		return nil, ErrHasherRequired
	}
	if cfg.Mailer == nil {
		return nil, ErrMailerRequired
	}
	limiter := cfg.RateLimiter
	if limiter == nil {
		limiter = ratelimiter.NewMemory()
	}
	svc := authsvc.NewService(authsvc.Deps{
		Users:     repos.Users,
		Passwords: repos.Passwords,
		Sessions:  repos.Sessions,
		Codes:     repos.VerificationCodes,
		Tokens:    repos.VerificationTokens,
		Hasher:    cfg.Hasher,
		Mailer:    cfg.Mailer,
		MailFrom:  cfg.MailFrom,
		Limiter:   limiter,
		Cookie: authsvc.CookieConfig{
			Name:   cfg.SessionCookie.Name,
			Path:   cfg.SessionCookie.Path,
			Domain: cfg.SessionCookie.Domain,
			Secure: cfg.SessionCookie.Secure,
			MaxAge: cfg.SessionCookie.MaxAge,
		},
	})
	return &Service{svc: svc}, nil
}

// RequireUser is HTTP middleware gating a route on a valid session. It satisfies
// sdk/web.Middleware via the method value authSvc.RequireUser, so a host passes
// it to another feature (e.g. cms.Config.AdminMiddleware) without either feature
// importing the other.
func (s *Service) RequireUser(next http.Handler) http.Handler {
	return s.svc.RequireUser(next)
}

// CurrentUser returns the authenticated user id on ctx, if any. It structurally
// satisfies a consuming feature's identity port (features/README.md §5's
// CurrentUser) with zero import in either direction.
func (s *Service) CurrentUser(ctx context.Context) (userID string, ok bool) {
	return s.svc.CurrentUser(ctx)
}

// Register wires the auth feature's own HTTP routes onto the host's mount. It
// builds a Service internally (validating the required Config fields) and mounts
// the route table. A host that also needs cross-feature identity builds a second
// Service via NewService — the two point at the same Repositories/Config and
// hold no independent state, an accepted, documented duplication (see the
// auth-feature-design doc, §3). Migrations are registered by the store adapter
// (features/auth/stores/turso), not here — the core is dialect-blind.
func Register(m feature.Mount, repos Repositories, cfg Config) error {
	svc, err := NewService(repos, cfg)
	if err != nil {
		return err
	}
	internalhttp.Mount(m.Router, svc.svc)
	return nil
}
