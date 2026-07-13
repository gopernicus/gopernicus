package authentication

import inbound "github.com/gopernicus/gopernicus/features/authentication/internal/inbound/authentication"

// Views is the authentication feature's HTML rendering port (FS3, design §9.2).
// Public type alias of the transport-package interface (the CMS A1 precedent), so
// hosts implement/override it without importing internal/. The bundled default lives
// in the sibling module features/authentication/views/templ; the blessed
// customization path is embedding that concrete default and overriding individual
// methods. A nil Config.Views means the HTML surface is not registered — the JSON
// API routes remain mounted, the HTML GET pages and form decoding are absent
// (design §9.2). The feature core never imports templ.
type Views = inbound.Views

// View models re-exported from the transport package so hosts (and the bundled
// views/templ module) can build page models and forms without importing internal/
// (the CMS A1 precedent). None of these models ever carries a password, one-time
// code, magic-link/reset/verification token, HMAC pepper, unmasked recovery address,
// or raw provider token (design §9.2, AV3-8.1 secret discipline).
type (
	// PageContext is the shared per-render context every page model embeds: CSRF
	// token, CSP nonce, allowlist-validated return-to, generic message, masked actor,
	// and accessible per-field errors.
	PageContext = inbound.PageContext
	// FieldError is one accessible, per-field validation message.
	FieldError = inbound.FieldError

	// Public credential page models.
	LoginPage    = inbound.LoginPage
	RegisterPage = inbound.RegisterPage
	VerifyPage   = inbound.VerifyPage
	ForgotPage   = inbound.ForgotPage
	ResetPage    = inbound.ResetPage

	// Passwordless page models.
	PasswordlessStartPage = inbound.PasswordlessStartPage
	PasswordlessCodePage  = inbound.PasswordlessCodePage
	MagicLinkPage         = inbound.MagicLinkPage
	CheckDeliveryPage     = inbound.CheckDeliveryPage

	// Step-up (recent-authentication) page model.
	StepUpPage = inbound.StepUpPage

	// Account-security page models and their masked projections.
	AccountSecurityPage = inbound.AccountSecurityPage
	OAuthMethod         = inbound.OAuthMethod
	IdentifierMethod    = inbound.IdentifierMethod
	IdentifierFormPage  = inbound.IdentifierFormPage
	PasswordFormPage    = inbound.PasswordFormPage
	OAuthUnlinkPage     = inbound.OAuthUnlinkPage

	// Generic status and error page models.
	StatusPage = inbound.StatusPage
	ErrorPage  = inbound.ErrorPage
)
