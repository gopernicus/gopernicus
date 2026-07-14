package authentication

import "github.com/gopernicus/gopernicus/sdk/foundation/web"

// Views is the authentication feature's HTML rendering port (FS3, design §9.2): the
// optional server-rendered surface — the public credential pages and the
// account-security management pages — renders through it, so a host can swap
// presentation without the handlers binding to concrete templates. Every method
// returns a web.Renderer over the port's own exported view models; the models carry
// only display-safe values.
//
// The bundled default lives in the sibling module
// features/authentication/views/templ. The blessed way to customize is partial
// override: embed that concrete default and override individual methods.
// Implementing every method from scratch (e.g. over sdk/foundation/web.Template) is
// possible but not the sold path. The feature core never imports templ.
//
// A nil Config.Views means the HTML surface is not registered: the HTML GET pages
// and form decoding are absent while the unchanged JSON API routes remain mounted
// (design §9.2). Presentation overrides change rendering only — never route
// security, request decoding, service policy, or error classification.
//
// SECRET DISCIPLINE (design §9.2, task AV3-8.1): no method or view model ever
// carries a password, one-time code, magic-link/reset/verification token, HMAC
// pepper, unmasked recovery address, or raw provider token. Ownership secrets reach
// the browser only through the delivery channel (email/SMS) or, for the magic-link
// and reset landings, the URL fragment the page script reads client-side — never a
// server-rendered value.
type Views interface {
	// Public credential pages (unauthenticated).
	Login(m LoginPage) web.Renderer
	Register(m RegisterPage) web.Renderer
	Verify(m VerifyPage) web.Renderer
	ForgotPassword(m ForgotPage) web.Renderer
	ResetPassword(m ResetPage) web.Renderer

	// Passwordless pages (unauthenticated). MagicLinkLanding reads the token from
	// the URL fragment client-side and POSTs redemption; the token is never a
	// server-rendered value.
	PasswordlessStart(m PasswordlessStartPage) web.Renderer
	PasswordlessCode(m PasswordlessCodePage) web.Renderer
	MagicLinkLanding(m MagicLinkPage) web.Renderer
	CheckDelivery(m CheckDeliveryPage) web.Renderer

	// Step-up (recent-authentication) confirmation page.
	StepUp(m StepUpPage) web.Renderer

	// Account-security pages (live-session gated). AccountSecurity is the masked
	// method inventory; the forms drive identifier/password/OAuth mutations through
	// the same service methods the JSON API uses.
	AccountSecurity(m AccountSecurityPage) web.Renderer
	IdentifierForm(m IdentifierFormPage) web.Renderer
	PasswordForm(m PasswordFormPage) web.Renderer
	OAuthUnlink(m OAuthUnlinkPage) web.Renderer

	// Generic status and error pages.
	Status(m StatusPage) web.Renderer
	Error(m ErrorPage) web.Renderer
}

// FieldError is one accessible, per-field validation message: Field is the form
// control name it associates with (so the template can bind aria-describedby /
// label-for), Message is the human-readable, enumeration-safe copy. Never a secret.
type FieldError struct {
	Field   string
	Message string
}

// PageContext is the shared per-render context every page model embeds (design
// §9.2). It carries only display-safe values: the double-submit CSRF token for the
// page's forms, the CSP nonce a template attaches to its inline script, the
// return-to destination ALREADY validated by the redirect allowlist, a generic
// notice/status message, the masked actor (e.g. a masked signed-in email — never
// the full value), and accessible per-field errors. It never carries a password,
// code, token, or unmasked address.
type PageContext struct {
	// CSRFToken is the double-submit token the page's mutation forms echo back in a
	// hidden field. It is a per-request anti-CSRF value, not a credential.
	CSRFToken string
	// CSPNonce is the nonce a template stamps onto its inline <script> so a
	// restrictive Content-Security-Policy can permit exactly that script.
	CSPNonce string
	// ReturnTo is the post-success destination, ALREADY validated against the exact
	// redirect allowlist by the handler; a template renders it verbatim into a
	// hidden field. Never a Host-derived or attacker-controlled URL.
	ReturnTo string
	// Message is a generic, enumeration-resistant notice ("Check your inbox", "That
	// link is no longer valid") — never a value that distinguishes an
	// unknown/unverified account.
	Message string
	// Actor is the masked identity of the signed-in caller for account-security
	// pages (e.g. "a•••@example.com"), or empty on unauthenticated pages. Always
	// masked, never the full address.
	Actor string
	// FieldErrors are the accessible per-field validation messages to re-render.
	// Secret fields (password, code, token) are never repopulated after a failure.
	FieldErrors []FieldError
}

// LoginPage models the password-login form. Email is the echoed entered address
// (non-secret); the password is never echoed.
type LoginPage struct {
	PageContext
	Email string
	// PasswordlessEnabled reports whether the host wired passwordless login, so the
	// template can offer the alternative; it changes no route security.
	PasswordlessEnabled bool
	// OAuthProviders lists the wired provider names for "sign in with" links.
	OAuthProviders []string
}

// RegisterPage models the registration form. Email and DisplayName are echoed
// entered values (non-secret); the password is never echoed.
type RegisterPage struct {
	PageContext
	Email       string
	DisplayName string
}

// VerifyPage models the registration verification form. MaskedEmail identifies the
// address awaiting proof (masked); the verification code is never echoed.
type VerifyPage struct {
	PageContext
	// Email is the plain address the code was sent to, carried as a hidden field so
	// the verify POST names the account. It is a contact address, not a secret; the
	// masked form (MaskedEmail) is what is displayed.
	Email       string
	MaskedEmail string
}

// ForgotPage models the forgot-password start form. Email is the echoed entered
// address (non-secret).
type ForgotPage struct {
	PageContext
	Email string
}

// ResetPage models the password-reset form reached from a reset link. On the INITIAL
// render Token is empty: the page reads the token from the URL fragment client-side
// and posts it, so it never appears as a server-rendered value or in the referrer
// (design §6.4). On a validation-error RE-render (e.g. a too-short new password), the
// fragment has already been scrubbed, so the handler echoes the SUBMITTED token back
// into Token — the same value the failed POST already carried — so the user can
// correct the password and retry a still-valid reset (IX-11). Echoing the submitted
// token into the hidden field adds no new exposure class: it was already in the POST
// body, the response HTML is not logged, and no fragment/query/referrer copy is
// created. Passwords are never echoed.
type ResetPage struct {
	PageContext
	// RedeemPath is the POST endpoint the form submits the token and new password to.
	RedeemPath string
	// Token is the reset token echoed back ONLY on a validation-error re-render so a
	// corrected retry survives the scrubbed fragment (IX-11). Empty on the initial
	// render, where the client-side fragment reader supplies it instead.
	Token string
}

// PasswordlessStartPage models the passwordless-login start form. Kind selects
// email/phone, Identifier is the echoed entered address (non-secret), Method selects
// link/code where the kind allows.
type PasswordlessStartPage struct {
	PageContext
	Kind       string
	Identifier string
	Method     string
	// Kinds are the enabled passwordless identifier kinds ("email", "phone").
	Kinds []string
}

// PasswordlessCodePage models the OTP entry form. MaskedIdentifier shows where the
// code was delivered (masked); the code is never echoed.
type PasswordlessCodePage struct {
	PageContext
	Kind             string
	MaskedIdentifier string
	// Identifier is the plain address carried as a hidden field so the verify POST
	// re-resolves the same current identifier; it is a contact address, not a secret.
	Identifier string
}

// MagicLinkPage models the magic-link landing page. It carries NO token: the page's
// nonced script reads the token from the URL fragment, scrubs it from history, and
// POSTs redemption; a visible manual action posts the same fragment value without
// ever placing the token in a query string (design §6.4).
type MagicLinkPage struct {
	PageContext
	// RedeemPath is the POST endpoint the landing script (and the manual fallback)
	// submits the fragment-read token to.
	RedeemPath string
}

// CheckDeliveryPage models the post-start confirmation ("we sent you a link/code").
// MaskedDestination is the masked address; the copy stays generic so it never
// distinguishes an unknown account (design §9.2).
type CheckDeliveryPage struct {
	PageContext
	Kind              string
	MaskedDestination string
	Method            string
}

// StepUpPage models the recent-authentication confirmation page for a sensitive
// operation. Operation is a generic label of what is being confirmed;
// MaskedIdentifier shows where a code would be delivered (masked). No code/password
// is echoed.
type StepUpPage struct {
	PageContext
	Operation        string
	MaskedIdentifier string
	// Purpose and Context bind the earned recent-authentication grant to the exact
	// operation the caller is stepping up for (design §5.0). They are non-secret
	// operation identifiers (e.g. "set_password", or an identifier id as context),
	// carried as hidden form fields so the completion grant matches the mutation the
	// service will require. The service is authoritative; these only route.
	Purpose string
	Context string
	// PasswordAvailable / CodeAvailable report which completion methods the caller
	// may use, so the template offers only viable ones; the safety decision remains
	// the service's, not the template's.
	PasswordAvailable bool
	CodeAvailable     bool
}

// OAuthMethod is one linked provider projected for display: masked/advisory only.
type OAuthMethod struct {
	Provider  string
	LinkedAt  string
	Assurance string
	Removable bool
}

// IdentifierMethod is one active identifier projected for display. MaskedValue is
// the redacted address (never the full value); Uses is the stable role list; the
// hints are advisory (the service guard is authoritative).
type IdentifierMethod struct {
	ID          string
	Kind        string
	MaskedValue string
	VerifiedAt  string
	Uses        []string
	Primary     bool
	Removable   bool
}

// AccountSecurityPage models the masked method inventory (design §5.1). Every
// address is masked; no password/code/token appears.
type AccountSecurityPage struct {
	PageContext
	HasPassword bool
	OAuth       []OAuthMethod
	Identifiers []IdentifierMethod
}

// IdentifierFormPage models the add/edit identifier form. For an edit, MaskedValue
// shows the existing address (masked) and ID names it; for an add, Value echoes the
// entered new address (non-secret). Uses reflect the login/recovery/notification
// selection. No ownership code is echoed.
type IdentifierFormPage struct {
	PageContext
	// Mode is "add" or "edit".
	Mode string
	Kind string
	// ID is the identifier being edited (empty on add).
	ID string
	// Value is the echoed entered new address on add (non-secret); empty on edit.
	Value string
	// MaskedValue is the existing address on edit (masked); empty on add.
	MaskedValue         string
	LoginEnabled        bool
	RecoveryEnabled     bool
	NotificationEnabled bool
	MakePrimary         bool
}

// PasswordFormPage models the set/change/remove password form. Mode selects the
// variant; ShowCurrentPassword requests the current-password field (change);
// MaskedDestination shows where a remove code was delivered (masked). No password or
// code is ever echoed.
type PasswordFormPage struct {
	PageContext
	// Mode is "set", "change", or "remove".
	Mode string
	// ShowCurrentPassword requests the current-password field (the change variant).
	ShowCurrentPassword bool
	// MaskedDestination is the masked recovery address a remove code was sent to
	// (the remove variant); empty otherwise.
	MaskedDestination string
}

// OAuthUnlinkPage models the provider-bound unlink confirmation. Provider names the
// account; MaskedDestination shows where the unlink code was delivered (masked). No
// code/token is echoed.
type OAuthUnlinkPage struct {
	PageContext
	Provider          string
	MaskedDestination string
}

// StatusPage models a generic informational page (delivery status, a completed
// action). Title/Detail are generic, enumeration-resistant copy.
type StatusPage struct {
	PageContext
	Title  string
	Detail string
}

// ErrorPage models a generic error page. Status is the HTTP status; Message is
// generic copy that never distinguishes an unknown/unverified account.
type ErrorPage struct {
	PageContext
	Status  int
	Message string
}
