// Package goth is the ui/goth implementation of the authentication feature's HTML
// rendering port (authentication.Views, design §9.2). It is a sibling module of
// features/authentication (FS3/FS4 — presentation defaults ship as views/<pkg>),
// so the feature core stays sdk-only and never imports templ or ui/goth. This
// adapter owns the domain-to-GOTH translation: it renders every auth page through
// the ui/goth Bundle (its self-hosted fingerprinted assets, primitives, and the
// forms/layouts components) and maps the bundle's browser Requirements into the
// feature's technology-neutral HTMLResourcePolicy.
//
// Hosts wire it as:
//
//	bundle, _ := goth.New(goth.Config{AssetBasePath: "/assets/goth"})
//	authViews, _ := authgoth.New(bundle)
//	authentication.Config{Views: authViews, HTMLPolicy: authViews.HTMLPolicy()}
//
// and serve bundle assets + authgoth.FragmentScriptHandler() under the paths the
// bundle/adapter name. Customize by embedding this Views and overriding individual
// methods (the blessed partial-override path).
//
// SECRET DISCIPLINE (design §9.2): no template here renders a password, one-time
// code, magic-link/reset/verification token, HMAC pepper, unmasked recovery
// address, or raw provider token. Secret fields are never repopulated after a
// failure; the reset and magic-link tokens are read from the URL fragment by the
// externalized fragment-reader script and never appear as a server-rendered value.
package goth

import (
	"github.com/a-h/templ"

	"github.com/gopernicus/gopernicus/features/authentication"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
	"github.com/gopernicus/gopernicus/ui/goth"
)

// _ pins Views to the authentication.Views port at compile time (mirrors the
// stubViews assertion in the feature's inbound html_test.go).
var _ authentication.Views = Views{}

// Views is the ui/goth-backed renderer. It holds the immutable presentation Bundle
// and the public path the host serves the externalized fragment-reader script at.
// Its zero value is not usable — construct it with New so the Bundle is non-nil.
type Views struct {
	bundle             *goth.Bundle
	fragmentScriptPath string
}

// Option customizes a Views at construction.
type Option func(*Views)

// WithFragmentScriptPath overrides the public URL the reset/magic-link landings load
// the externalized fragment-reader script from. It must match the path the host
// mounts FragmentScriptHandler() under. Empty is ignored (the default is kept).
func WithFragmentScriptPath(path string) Option {
	return func(v *Views) {
		if path != "" {
			v.fragmentScriptPath = path
		}
	}
}

// New returns the ui/goth Views over bundle. It returns an error for a nil bundle so
// a misconfigured host fails loudly at construction rather than nil-panicking at the
// first render.
func New(bundle *goth.Bundle, opts ...Option) (Views, error) {
	if bundle == nil {
		return Views{}, errNilBundle
	}
	v := Views{bundle: bundle, fragmentScriptPath: DefaultFragmentScriptPath}
	for _, opt := range opts {
		opt(&v)
	}
	return v, nil
}

// page wraps a page body in the shared GOTH document chrome: the bundle's head
// (fingerprinted theme stylesheet + any profile scripts), the fixed auth meta tags,
// and an optional per-page head extra (the fragment-reader <script> on the
// fragment-token landings).
func (v Views) page(title string, headExtra templ.Component, body templ.Component) web.Renderer {
	return document(v.bundle.Head(), title, headExtra, body)
}

// fragmentHead returns the head extra that loads the externalized fragment-reader
// script for the reset/magic-link landings.
func (v Views) fragmentHead() templ.Component { return fragmentScriptTag(v.fragmentScriptPath) }

// Login renders the password-login form.
func (v Views) Login(m authentication.LoginPage) web.Renderer {
	return v.page("Sign in", nil, loginBody(m))
}

// Register renders the account-creation form.
func (v Views) Register(m authentication.RegisterPage) web.Renderer {
	return v.page("Create your account", nil, registerBody(m))
}

// Verify renders the registration verification form.
func (v Views) Verify(m authentication.VerifyPage) web.Renderer {
	return v.page("Verify your email", nil, verifyBody(m))
}

// ForgotPassword renders the reset-request form.
func (v Views) ForgotPassword(m authentication.ForgotPage) web.Renderer {
	return v.page("Reset your password", nil, forgotBody(m))
}

// ResetPassword renders the fragment-token reset form.
func (v Views) ResetPassword(m authentication.ResetPage) web.Renderer {
	return v.page("Choose a new password", v.fragmentHead(), resetBody(m))
}

// PasswordlessStart renders the passwordless-login start form.
func (v Views) PasswordlessStart(m authentication.PasswordlessStartPage) web.Renderer {
	return v.page("Sign in without a password", nil, passwordlessStartBody(m))
}

// PasswordlessCode renders the one-time-code entry form.
func (v Views) PasswordlessCode(m authentication.PasswordlessCodePage) web.Renderer {
	return v.page("Enter your code", nil, passwordlessCodeBody(m))
}

// MagicLinkLanding renders the fragment-token magic-link landing page.
func (v Views) MagicLinkLanding(m authentication.MagicLinkPage) web.Renderer {
	return v.page("Signing you in", v.fragmentHead(), magicLinkBody(m))
}

// CheckDelivery renders the post-start delivery confirmation.
func (v Views) CheckDelivery(m authentication.CheckDeliveryPage) web.Renderer {
	return v.page("Check your messages", nil, checkDeliveryBody(m))
}

// StepUp renders the recent-authentication confirmation page.
func (v Views) StepUp(m authentication.StepUpPage) web.Renderer {
	return v.page("Confirm it's you", nil, stepUpBody(m))
}

// AccountSecurity renders the masked method inventory.
func (v Views) AccountSecurity(m authentication.AccountSecurityPage) web.Renderer {
	return v.page("Account security", nil, accountSecurityBody(m))
}

// IdentifierForm renders the add/edit identifier form.
func (v Views) IdentifierForm(m authentication.IdentifierFormPage) web.Renderer {
	return v.page(identifierFormTitle(m.Mode), nil, identifierFormBody(m))
}

// PasswordForm renders the set/change/remove password form.
func (v Views) PasswordForm(m authentication.PasswordFormPage) web.Renderer {
	return v.page(passwordPageTitle(m.Mode), nil, passwordFormBody(m))
}

// OAuthUnlink renders the provider-bound unlink confirmation.
func (v Views) OAuthUnlink(m authentication.OAuthUnlinkPage) web.Renderer {
	return v.page("Unlink account", nil, oauthUnlinkBody(m))
}

// Status renders a generic informational page.
func (v Views) Status(m authentication.StatusPage) web.Renderer {
	return v.page(statusTitle(m.Title), nil, statusBody(m))
}

// Error renders a generic error page.
func (v Views) Error(m authentication.ErrorPage) web.Renderer {
	return v.page(statusText(m.Status), nil, errorBody(m))
}
