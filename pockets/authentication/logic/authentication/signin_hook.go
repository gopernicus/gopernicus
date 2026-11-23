package authentication

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
)

// SignInHookTimeout bounds the host's BeforeSession hook. It runs under the
// request context, so a dropped request stops it too.
const SignInHookTimeout = 10 * time.Second

// SignInRefusedCode is the ?auth= outcome a browser OAuth callback lands on
// when the host's BeforeSession hook refuses the sign-in.
const SignInRefusedCode = "sign_in_refused"

// ErrSignInRefusedByHook wraps, beside the hook's own error, a sign-in the
// host's BeforeSession hook refused. It has no sdk kind of its own: the hook's
// error carries the kind, so sdk.ErrForbidden or sdk.ErrConflict refuses with
// that status and an unkinded error is a 500.
var ErrSignInRefusedByHook = errors.New("auth: sign-in refused by the host's BeforeSession hook")

// SignIn is one primary authentication about to mint a session.
type SignIn struct {
	UserID string
	// Method is the primary method: password, oauth, a passkey method, or a
	// passwordless code or link method.
	Method session.MethodKind
	// Created reports that this flow created the user: an OAuth registration
	// or a passwordless link that provisioned the account. A refused sign-in
	// keeps the created user, so the next attempt arrives with Created false.
	Created bool
	// Identifiers are the user's active, verified, login-enabled identifiers,
	// primary first, as of the sign-in.
	Identifiers []sdk.IdentityAddress
}

// SignInHook is the host's pre-session step (SessionsConfig.BeforeSession). It
// runs on every primary sign-in after the pocket's own checks (the credential
// proof, the active user, the provider link) and before the session is minted.
// A non-nil error refuses the sign-in and no token is returned.
//
// The hook and the session are two stores, never atomic. If the hook succeeds
// and the mint then fails (a revision race, a deactivated user), the host's
// write stands; the hook must be idempotent so the next sign-in finds its
// record and changes nothing. A passwordless link that provisions the account
// is the one exception to "before the mint": the account and the session
// commit in one transaction, so the hook runs after it and a refusal deletes
// the new session before any token is handed out.
//
// It does not run on a refresh, a step-up, a delegated or OAuth 2 token
// issue, or a password change, set or removal.
type SignInHook func(ctx context.Context, in SignIn) error

// SignInRefusal is a browser OAuth sign-in the host's BeforeSession hook
// refused with sdk.ErrForbidden or sdk.ErrConflict. It carries the flow's
// validated destination so the transport lands the browser there with
// SignInRefusedCode, never a session. It unwraps to the wrapped hook error.
type SignInRefusal struct {
	RedirectTo string
	err        error
}

func (e *SignInRefusal) Error() string { return e.err.Error() }

func (e *SignInRefusal) Unwrap() error { return e.err }

// WithSignInHook sets the host's BeforeSession hook. Nil runs no hook.
func WithSignInHook(hook SignInHook) Option {
	return func(c *constructorConfig) {
		c.BeforeSession = hook
	}
}

// runBeforeSession calls the host's BeforeSession hook, when one is set, under
// the request context bounded by SignInHookTimeout. The WARN carries the user
// id, the method and the error kind only, never the hook's text.
func (s *Service) runBeforeSession(ctx context.Context, userID string, method session.MethodKind, created bool) error {
	if s.beforeSession == nil {
		return nil
	}
	addresses, err := projectAddressesWhere(ctx, s.identifiers, userID, func(it identifier.Identifier) bool { return it.LoginEnabled })
	if err != nil {
		return err
	}
	hookCtx, cancel := context.WithTimeout(ctx, SignInHookTimeout)
	defer cancel()
	if err := s.beforeSession(hookCtx, SignIn{UserID: userID, Method: method, Created: created, Identifiers: addresses}); err != nil {
		s.logger.Warn("auth: sign-in refused by BeforeSession", "user_id", userID, "method", string(method), "error_kind", ErrorKind(err))
		return fmt.Errorf("%w: %w", ErrSignInRefusedByHook, err)
	}
	return nil
}

// IsSignInRefusal reports whether err is a BeforeSession refusal the host
// kinded as sdk.ErrForbidden or sdk.ErrConflict, the refusals a sign-in
// surfaces as such.
func IsSignInRefusal(err error) bool {
	return errors.Is(err, ErrSignInRefusedByHook) && (errors.Is(err, sdk.ErrForbidden) || errors.Is(err, sdk.ErrConflict))
}

// hookOutcome classifies a BeforeSession error for the flow's audit row: a
// kinded refusal is blocked with reason host_refused; anything else (a
// timeout, an unkinded error) is a failure without a reason.
func hookOutcome(err error) (status, reason string) {
	if IsSignInRefusal(err) {
		return securityevent.StatusBlocked, securityevent.ReasonHostRefused
	}
	return securityevent.StatusFailure, ""
}

// signInRefusal turns a kinded BeforeSession refusal on the OAuth callback into
// the browser-landable SignInRefusal; any other error passes through.
func signInRefusal(err error, redirectTo string) error {
	if IsSignInRefusal(err) {
		return &SignInRefusal{RedirectTo: redirectTo, err: err}
	}
	return err
}
