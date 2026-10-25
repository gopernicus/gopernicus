// Package goth is the ui/goth implementation of the authentication pocket's HTML
// rendering port (authentication.Views, design §9.2). It is a sibling module of
// pockets/authentication (FS3/FS4 — presentation defaults ship as views/<pkg>),
// so the pocket core stays sdk-only and never imports templ or ui/goth. This
// adapter owns the domain-to-GOTH translation: it renders every auth page through
// the ui/goth Bundle (its self-hosted fingerprinted assets, primitives, and the
// forms/layouts components) and maps the bundle's browser Requirements into the
// pocket's technology-neutral HTMLResourcePolicy.
//
// Hosts wire it as:
//
//	bundle, _ := goth.New(goth.WithAssetBasePath("/assets/goth"))
//	authViews, _ := authgoth.New(bundle)
//	authentication.WithBrowser(authentication.BrowserConfig{Views: authViews, HTMLPolicy: authViews.HTMLPolicy()})
//
// and serve bundle assets + authgoth.FragmentScriptHandler() under the paths the
// bundle/adapter name. With passkeys, pass the same value as
// authentication.PasskeyConfig.Views and serve authgoth.PasskeyScriptHandler() too. With TOTP, pass it
// as authentication.TOTPConfig.Views and construct it WithQR(rscqr.Modules) for the
// enrollment QR code. Customize by embedding this Views and overriding individual
// methods (the blessed partial-override path).
//
// SECRET DISCIPLINE (design §9.2): no template here renders a password, one-time
// code, magic-link/reset/verification token, HMAC pepper, unmasked recovery
// address, or raw provider token. Secret fields are never repopulated after a
// failure; the reset, magic-link, and OAuth pending-link tokens are read from the URL
// fragment by the externalized fragment-reader script and never appear as a
// server-rendered value.
package goth

import (
	"fmt"
	"net/url"
	"slices"
	"strconv"

	"github.com/a-h/templ"
	inbound "github.com/gopernicus/gopernicus/pockets/authentication/inbound/http"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
	"github.com/gopernicus/gopernicus/ui/goth"
	"github.com/gopernicus/gopernicus/ui/goth/primitives"
)

// _ pins Views to the authentication.Views port at compile time (mirrors the
// stubViews assertion in the pocket's inbound html_test.go).
var _ inbound.Views = Views{}

var _ inbound.PasskeyViews = Views{}

var _ inbound.TOTPViews = Views{}

// Views is the ui/goth-backed renderer. It holds the immutable presentation Bundle
// and the public path the host serves the externalized fragment-reader script at.
// Its zero value is not usable — construct it with New so the Bundle is non-nil.
type Views struct {
	bundle             *goth.Bundle
	fragmentScriptPath string
	passkeyScriptPath  string
	brand              templ.Component
	appName            string
	qr                 QRModules
}

// Option customizes a Views at construction.
type Option func(*viewConfig)

type viewConfig struct {
	fragmentScriptPath string
	passkeyScriptPath  string
	brand              templ.Component
	appName            string
	qr                 QRModules
}

// WithFragmentScriptPath overrides the public URL the reset/magic-link landings load
// the externalized fragment-reader script from. It must match the path the host
// mounts FragmentScriptHandler() under. Empty is ignored (the default is kept).
func WithFragmentScriptPath(path string) Option {
	return func(v *viewConfig) {
		if path != "" {
			v.fragmentScriptPath = path
		}
	}
}

// WithPasskeyScriptPath overrides the public URL the passkey controls load their
// script from. It must match the path the host mounts PasskeyScriptHandler() under.
// Empty is ignored (the default is kept).
func WithPasskeyScriptPath(path string) Option {
	return func(v *viewConfig) {
		if path != "" {
			v.passkeyScriptPath = path
		}
	}
}

// WithBrand sets a host-supplied brand component rendered ABOVE every page body
// (typically a logo header). The component owns its entire markup and styling —
// the adapter adds no wrapper element around it, so the host's own classes and
// theme stylesheet govern the presentation. Nil keeps today's unbranded pages.
// The page's <h1> title inside the shell is unaffected (single top-level
// heading per page is preserved).
func WithBrand(brand templ.Component) Option {
	return func(v *viewConfig) { v.brand = brand }
}

// WithAppName suffixes every document <title> with the application name
// ("Sign in · <name>") so browser tabs and history identify the product.
// Empty keeps today's bare page titles.
func WithAppName(name string) Option {
	return func(v *viewConfig) { v.appName = name }
}

// New returns the ui/goth Views over bundle. It returns an error for a nil bundle so
// a misconfigured host fails loudly at construction rather than nil-panicking at the
// first render.
func New(bundle *goth.Bundle, opts ...Option) (Views, error) {
	if bundle == nil {
		return Views{}, errNilBundle
	}
	cfg := viewConfig{fragmentScriptPath: DefaultFragmentScriptPath, passkeyScriptPath: DefaultPasskeyScriptPath}
	for _, opt := range opts {
		if opt == nil {
			return Views{}, fmt.Errorf("authentication goth: nil option: %w", sdk.ErrInvalidInput)
		}
		opt(&cfg)
	}
	return Views{bundle: bundle, fragmentScriptPath: cfg.fragmentScriptPath, passkeyScriptPath: cfg.passkeyScriptPath, brand: cfg.brand, appName: cfg.appName, qr: cfg.qr}, nil
}

// page wraps a page body in the shared GOTH document chrome: the bundle's head
// (fingerprinted theme stylesheet + any profile scripts), the fixed auth meta tags,
// and an optional per-page head extra (the fragment-reader <script> on the
// fragment-token landings).
func (v Views) page(title string, headExtra templ.Component, body templ.Component) web.Renderer {
	if v.appName != "" {
		title = title + " · " + v.appName
	}
	if v.brand != nil {
		body = branded(v.brand, body)
	}
	return document(v.bundle.Head(), title, headExtra, body)
}

// fragmentHead returns the head extra that loads the externalized fragment-reader
// script for the reset/magic-link landings.
func (v Views) fragmentHead() templ.Component { return fragmentScriptTag(v.fragmentScriptPath) }

// passkeyHead returns the head extra that loads the passkey script, or nil when the
// page model carries no passkey links (passkeys off).
func (v Views) passkeyHead(links *inbound.PasskeyLinks) templ.Component {
	if links == nil {
		return nil
	}
	return passkeyScriptTag(v.passkeyScriptPath)
}

// Login renders the sign-in methods enabled by the page model.
func (v Views) Login(m inbound.LoginPage) web.Renderer {
	return v.page("Sign in", v.passkeyHead(m.Passkeys), loginBody(m))
}

// Register renders the account-creation form.
func (v Views) Register(m inbound.RegisterPage) web.Renderer {
	return v.page("Create your account", nil, registerBody(m))
}

// Verify renders the registration verification form.
func (v Views) Verify(m inbound.VerifyPage) web.Renderer {
	return v.page("Verify your email", nil, verifyBody(m))
}

// ForgotPassword renders the reset-request form.
func (v Views) ForgotPassword(m inbound.ForgotPage) web.Renderer {
	return v.page("Reset your password", nil, forgotBody(m))
}

// ResetPassword renders the fragment-token reset form.
func (v Views) ResetPassword(m inbound.ResetPage) web.Renderer {
	return v.page("Choose a new password", v.fragmentHead(), resetBody(m))
}

// PasswordlessStart renders the passwordless-login start form.
func (v Views) PasswordlessStart(m inbound.PasswordlessStartPage) web.Renderer {
	return v.page("Sign in without a password", nil, passwordlessStartBody(m))
}

// PasswordlessCode renders the one-time-code entry form.
func (v Views) PasswordlessCode(m inbound.PasswordlessCodePage) web.Renderer {
	return v.page("Enter your code", nil, passwordlessCodeBody(m))
}

// MagicLinkLanding renders the fragment-token magic-link landing page.
func (v Views) MagicLinkLanding(m inbound.MagicLinkPage) web.Renderer {
	return v.page("Signing you in", v.fragmentHead(), magicLinkBody(m))
}

// CheckDelivery renders the post-start delivery confirmation.
func (v Views) CheckDelivery(m inbound.CheckDeliveryPage) web.Renderer {
	return v.page("Check your messages", nil, checkDeliveryBody(m))
}

// OAuthLinkLanding renders the fragment-token OAuth pending-link landing.
func (v Views) OAuthLinkLanding(m inbound.OAuthLinkPage) web.Renderer {
	return v.page("Finish linking your account", v.fragmentHead(), oauthLinkBody(m))
}

// StepUp renders the recent-authentication confirmation page.
func (v Views) StepUp(m inbound.StepUpPage) web.Renderer {
	return v.page("Confirm it's you", v.passkeyHead(m.Passkeys), stepUpBody(m))
}

// AccountSecurity renders the masked method inventory.
func (v Views) AccountSecurity(m inbound.AccountSecurityPage) web.Renderer {
	return v.page("Account security", nil, accountSecurityBody(m))
}

// IdentifierForm renders the add/edit identifier form.
func (v Views) IdentifierForm(m inbound.IdentifierFormPage) web.Renderer {
	return v.page(identifierFormTitle(m.Mode), nil, identifierFormBody(m))
}

// PasswordForm renders the set/change/remove password form.
func (v Views) PasswordForm(m inbound.PasswordFormPage) web.Renderer {
	return v.page(passwordPageTitle(m.Mode), nil, passwordFormBody(m))
}

// OAuthUnlink renders the provider-bound unlink confirmation.
func (v Views) OAuthUnlink(m inbound.OAuthUnlinkPage) web.Renderer {
	return v.page("Unlink account", nil, oauthUnlinkBody(m))
}

// Passkeys renders the passkey manage page: the list, rename, remove and add.
func (v Views) Passkeys(m inbound.PasskeysPage) web.Renderer {
	return v.page("Passkeys", v.passkeyHead(&m.Links), passkeysBody(m))
}

// PasskeyStepUp renders the passkey step-up page a Browser() + RequireMFA() gate
// redirects an AAL1 cookie session to.
func (v Views) PasskeyStepUp(m inbound.PasskeyStepUpPage) web.Renderer {
	return v.page("Confirm it's you", v.passkeyHead(&m.Links), passkeyStepUpBody(m))
}

// TOTP renders the authenticator-app manage page.
func (v Views) TOTP(m inbound.TOTPPage) web.Renderer {
	return v.page("Authenticator app", nil, totpBody(m))
}

// TOTPEnroll renders the enrollment page: the QR code (WithQR), the grouped key,
// the otpauth:// link and the confirm form.
func (v Views) TOTPEnroll(m inbound.TOTPEnrollPage) web.Renderer {
	qr, ok := qrFor(v.qr, m.OTPAuthURI)
	return v.page("Set up an authenticator app", nil, totpEnrollBody(m, qr, ok))
}

// RecoveryCodes renders a fresh recovery-code set, shown once.
func (v Views) RecoveryCodes(m inbound.RecoveryCodesPage) web.Renderer {
	return v.page("Save your recovery codes", nil, recoveryCodesBody(m))
}

// MFAStepUp renders the "Confirm it's you" chooser. The passkey script loads only
// when the passkey control is offered.
func (v Views) MFAStepUp(m inbound.MFAStepUpPage) web.Renderer {
	var links *inbound.PasskeyLinks
	if m.OfferPasskey {
		links = m.Passkeys
	}
	return v.page("Confirm it's you", v.passkeyHead(links), mfaStepUpBody(m))
}

// Status renders a generic informational page.
func (v Views) Status(m inbound.StatusPage) web.Renderer {
	return v.page(statusTitle(m.Title), nil, statusBody(m))
}

// Error renders a generic error page.
func (v Views) Error(m inbound.ErrorPage) web.Renderer {
	return v.page(statusText(m.Status), nil, errorBody(m))
}

// passkeyControlProps configures one passkeyControl: the ceremony mode passkey.js
// runs, its routes, the double-submit token, where a success navigates, and the
// button.
type passkeyControlProps struct {
	Mode       string
	OptionsURL string
	FinishURL  string
	CSRFToken  string
	ReturnTo   string
	DoneURL    string
	Variant    primitives.ButtonVariant
	Label      string
}

// passkeyVerificationText describes whether a passkey confirms a PIN or biometric.
func passkeyVerificationText(userVerified bool) string {
	if userVerified {
		return "Confirms your PIN or biometric"
	}
	return "Presence only: can't confirm a PIN or biometric"
}

// passkeyLastUsedText describes a passkey's last use.
func passkeyLastUsedText(lastUsedAt string) string {
	if lastUsedAt == "" {
		return "Not used yet."
	}
	return "Last used " + lastUsedAt + "."
}

// passkeyNameID is a passkey row's rename input id.
func passkeyNameID(id string) string { return "passkey-name-" + id }

// passkeyAddedURL is where the manage page goes after an add without a return-to.
func passkeyAddedURL(links inbound.PasskeyLinks) string {
	if links.Manage == "" {
		return ""
	}
	return links.Manage + "?auth=passkey_added"
}

// holdsVerifiedPasskey reports whether any passkey is AAL2-capable (registered
// with user verification), the predicate behind PasskeyStepUpPage.CanReachAAL2.
func holdsVerifiedPasskey(passkeys []inbound.PasskeyItem) bool {
	return slices.ContainsFunc(passkeys, func(p inbound.PasskeyItem) bool { return p.UserVerified })
}

// confirmPasskeyHref links the manage page to the passkey step-up page and back.
// Once the user holds an AAL2-capable passkey, adding or removing one needs a
// passkey check in the last five minutes; without this link the add control's
// 403 would leave them no way to earn it.
func confirmPasskeyHref(links inbound.PasskeyLinks) string {
	return links.StepUpPage + "?return_to=" + url.QueryEscape(links.Manage)
}

// addPasskeyHref links the step-up page to the manage page, returning to the
// step-up page (and its own return-to) once the passkey is added.
func addPasskeyHref(links inbound.PasskeyLinks, returnTo string) string {
	back := links.StepUpPage
	if returnTo != "" {
		back += "?return_to=" + url.QueryEscape(returnTo)
	}
	return links.Manage + "?return_to=" + url.QueryEscape(back)
}

// totpEnrollPageHref is the enrollment page, which re-reads the pending setup:
// it sits under the manage page.
func totpEnrollPageHref(links inbound.TOTPLinks) string { return links.Manage + "/enroll" }

// totpLastUsedText describes the authenticator app's last use.
func totpLastUsedText(lastUsedAt string) string {
	if lastUsedAt == "" {
		return "Not used yet."
	}
	return "Last used " + lastUsedAt + "."
}

// recoveryCodesRemainingText describes how many recovery codes are left.
func recoveryCodesRemainingText(n int) string {
	switch n {
	case 0:
		return "You have no recovery codes left. Generate new ones so you can still get in without your app."
	case 1:
		return "You have 1 recovery code left."
	default:
		return "You have " + strconv.Itoa(n) + " recovery codes left."
	}
}

// chooserHref links to the "Confirm it's you" chooser, returning to returnTo.
func chooserHref(links inbound.TOTPLinks, returnTo string) string {
	if returnTo == "" {
		return links.ChooserPage
	}
	return links.ChooserPage + "?return_to=" + url.QueryEscape(returnTo)
}

// chooserAddPasskeyHref links the chooser to the passkey manage page, coming
// back to the chooser (and its own return-to) once the passkey is added.
func chooserAddPasskeyHref(passkeys inbound.PasskeyLinks, totp inbound.TOTPLinks, returnTo string) string {
	return passkeys.Manage + "?return_to=" + url.QueryEscape(chooserHref(totp, returnTo))
}
