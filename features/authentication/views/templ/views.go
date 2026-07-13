// Package templ is the bundled default implementation of the authentication
// feature's HTML rendering port (authentication.Views, design §9.2), built on
// a-h/templ components. It is a sibling module of features/authentication
// (FS3/FS4 — presentation defaults ship as views/<pkg>), so the feature core
// stays sdk-only and never imports templ. Hosts wire it as Config.Views:
// templ.New(), and customize by embedding templ.Views and overriding individual
// methods.
//
// SECRET DISCIPLINE (design §9.2): no template here renders a password, one-time
// code, magic-link/reset/verification token, HMAC pepper, unmasked recovery
// address, or raw provider token. Secret fields are never repopulated after a
// failure; the reset and magic-link tokens are read from the URL fragment by a
// CSP-nonced script and never appear as a server-rendered value.
package templ

import (
	"github.com/gopernicus/gopernicus/features/authentication"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// _ pins Views to the authentication.Views port at compile time (mirrors the
// stubViews assertion in the feature's inbound html_test.go).
var _ authentication.Views = Views{}

// Views is the concrete bundled default renderer. It carries no state, so its
// zero value is usable and it is safe to embed for partial overrides.
type Views struct{}

// New returns the bundled default Views.
func New() Views { return Views{} }

// Login renders the password-login form.
func (Views) Login(m authentication.LoginPage) web.Renderer { return Login(m) }

// Register renders the account-creation form.
func (Views) Register(m authentication.RegisterPage) web.Renderer { return Register(m) }

// Verify renders the registration verification form.
func (Views) Verify(m authentication.VerifyPage) web.Renderer { return Verify(m) }

// ForgotPassword renders the reset-request form.
func (Views) ForgotPassword(m authentication.ForgotPage) web.Renderer { return ForgotPassword(m) }

// ResetPassword renders the fragment-token reset form.
func (Views) ResetPassword(m authentication.ResetPage) web.Renderer { return ResetPassword(m) }

// PasswordlessStart renders the passwordless-login start form.
func (Views) PasswordlessStart(m authentication.PasswordlessStartPage) web.Renderer {
	return PasswordlessStart(m)
}

// PasswordlessCode renders the one-time-code entry form.
func (Views) PasswordlessCode(m authentication.PasswordlessCodePage) web.Renderer {
	return PasswordlessCode(m)
}

// MagicLinkLanding renders the fragment-token magic-link landing page.
func (Views) MagicLinkLanding(m authentication.MagicLinkPage) web.Renderer {
	return MagicLinkLanding(m)
}

// CheckDelivery renders the post-start delivery confirmation.
func (Views) CheckDelivery(m authentication.CheckDeliveryPage) web.Renderer {
	return CheckDelivery(m)
}

// StepUp renders the recent-authentication confirmation page.
func (Views) StepUp(m authentication.StepUpPage) web.Renderer { return StepUp(m) }

// AccountSecurity renders the masked method inventory.
func (Views) AccountSecurity(m authentication.AccountSecurityPage) web.Renderer {
	return AccountSecurity(m)
}

// IdentifierForm renders the add/edit identifier form.
func (Views) IdentifierForm(m authentication.IdentifierFormPage) web.Renderer {
	return IdentifierForm(m)
}

// PasswordForm renders the set/change/remove password form.
func (Views) PasswordForm(m authentication.PasswordFormPage) web.Renderer { return PasswordForm(m) }

// OAuthUnlink renders the provider-bound unlink confirmation.
func (Views) OAuthUnlink(m authentication.OAuthUnlinkPage) web.Renderer { return OAuthUnlink(m) }

// Status renders a generic informational page.
func (Views) Status(m authentication.StatusPage) web.Renderer { return Status(m) }

// Error renders a generic error page.
func (Views) Error(m authentication.ErrorPage) web.Renderer { return Error(m) }
