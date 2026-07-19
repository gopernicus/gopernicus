package goth

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/features/authentication"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
	uigoth "github.com/gopernicus/gopernicus/ui/goth"
)

// newViews builds a Views over a default (StylesOnly) ui/goth bundle for tests.
func newViews(t *testing.T) Views {
	t.Helper()
	b, err := uigoth.New(uigoth.Config{})
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	v, err := New(b)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return v
}

// render drains a web.Renderer to a string for markup assertions.
func render(t *testing.T, r web.Renderer) string {
	t.Helper()
	var b strings.Builder
	if err := r.Render(context.Background(), &b); err != nil {
		t.Fatalf("render: %v", err)
	}
	return b.String()
}

// mustContain fails unless every want substring is present.
func mustContain(t *testing.T, name, body string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("%s: missing %q in:\n%s", name, w, body)
		}
	}
}

// mustNotContain fails if any forbidden substring is present.
func mustNotContain(t *testing.T, name, body string, forbid ...string) {
	t.Helper()
	for _, f := range forbid {
		if strings.Contains(body, f) {
			t.Errorf("%s: forbidden %q present in:\n%s", name, f, body)
		}
	}
}

// ctx builds a representative page context: CSRF token, CSP nonce, a validated
// return-to, and one field error. None of these are secrets.
func ctx() authentication.PageContext {
	return authentication.PageContext{
		CSRFToken:   "csrf-abc123",
		CSPNonce:    "nonce-xyz789",
		ReturnTo:    "/dashboard",
		FieldErrors: []authentication.FieldError{{Field: "email", Message: "That email looks invalid."}},
	}
}

// TestNilBundleRejected proves New fails loudly on a nil bundle.
func TestNilBundleRejected(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("New(nil) returned no error")
	}
}

// TestAllPagesRenderZeroModels proves every page renders from a zero model without
// error — the minimal-model smoke test across the whole port.
func TestAllPagesRenderZeroModels(t *testing.T) {
	v := newViews(t)
	render(t, v.Login(authentication.LoginPage{}))
	render(t, v.Register(authentication.RegisterPage{}))
	render(t, v.Verify(authentication.VerifyPage{}))
	render(t, v.ForgotPassword(authentication.ForgotPage{}))
	render(t, v.ResetPassword(authentication.ResetPage{}))
	render(t, v.PasswordlessStart(authentication.PasswordlessStartPage{}))
	render(t, v.PasswordlessCode(authentication.PasswordlessCodePage{}))
	render(t, v.MagicLinkLanding(authentication.MagicLinkPage{}))
	render(t, v.CheckDelivery(authentication.CheckDeliveryPage{}))
	render(t, v.StepUp(authentication.StepUpPage{}))
	render(t, v.AccountSecurity(authentication.AccountSecurityPage{}))
	render(t, v.IdentifierForm(authentication.IdentifierFormPage{}))
	render(t, v.IdentifierForm(authentication.IdentifierFormPage{Mode: "edit", ID: "id1"}))
	render(t, v.PasswordForm(authentication.PasswordFormPage{}))
	render(t, v.PasswordForm(authentication.PasswordFormPage{Mode: "remove"}))
	render(t, v.OAuthUnlink(authentication.OAuthUnlinkPage{}))
	render(t, v.Status(authentication.StatusPage{}))
	render(t, v.Error(authentication.ErrorPage{}))
}

// TestDocumentLoadsGOTHAssets proves every page's chrome loads the ui/goth
// fingerprinted stylesheet from the bundle asset base path (the migration's
// load-bearing "styled via GOTH assets" property).
func TestDocumentLoadsGOTHAssets(t *testing.T) {
	body := render(t, newViews(t).Login(authentication.LoginPage{PageContext: ctx()}))
	mustContain(t, "assets", body,
		`rel="stylesheet"`,
		`/assets/goth/`,
		`.css`,
		`<meta name="referrer" content="no-referrer">`,
	)
}

// TestLogin_LabelsAutocompleteCSRF proves the login form's accessible labels,
// correct autocomplete tokens, the CSRF hidden field, and the validated return-to
// hidden field. The entered email is echoed; the password is never echoed.
func TestLogin_LabelsAutocompleteCSRF(t *testing.T) {
	body := render(t, newViews(t).Login(authentication.LoginPage{
		PageContext: ctx(),
		Email:       "user@example.com",
	}))
	mustContain(t, "Login", body,
		`action="/auth/login"`,
		`autocomplete="email"`,
		`type="email"`,
		`autocomplete="current-password"`,
		`for="email"`,
		`for="password"`,
		`name="csrf_token" value="csrf-abc123"`,
		`name="return_to" value="/dashboard"`,
		`value="user@example.com"`,
		`role="alert"`,
		"That email looks invalid.",
	)
	// The password input carries no value attribute — a failed attempt never
	// repopulates a secret (primitives.Input never echoes a password Value).
	mustNotContain(t, "Login", body, `name="password" value=`, `value="user@example.com" type="password"`)
}

// TestRegister_NewPassword proves the registration form uses new-password.
func TestRegister_NewPassword(t *testing.T) {
	body := render(t, newViews(t).Register(authentication.RegisterPage{PageContext: ctx(), Email: "u@e.com", DisplayName: "Ada"}))
	mustContain(t, "Register", body,
		`action="/auth/register"`,
		`autocomplete="new-password"`,
		`autocomplete="email"`,
		`value="Ada"`,
		`name="csrf_token"`,
	)
}

// TestVerify_OneTimeCodeHiddenEmail proves the verify form uses one-time-code and
// carries the plain email as a hidden field, while displaying the masked address.
func TestVerify_OneTimeCodeHiddenEmail(t *testing.T) {
	body := render(t, newViews(t).Verify(authentication.VerifyPage{
		PageContext: ctx(),
		Email:       "user@example.com",
		MaskedEmail: "u•••@example.com",
	}))
	mustContain(t, "Verify", body,
		`autocomplete="one-time-code"`,
		`inputmode="numeric"`,
		`type="hidden" name="email" value="user@example.com"`,
		"u•••@example.com",
		`name="csrf_token"`,
	)
}

// TestReset_FragmentTokenExternalizedNeverRendered proves the INITIAL reset render
// carries no server-rendered token and no INLINE script: the hidden token field ships
// EMPTY, the externalized fragment-reader script is loaded same-origin, and the form
// carries the data-* config the reader keys off. No token appears in a query string.
func TestReset_FragmentTokenExternalizedNeverRendered(t *testing.T) {
	body := render(t, newViews(t).ResetPassword(authentication.ResetPage{
		PageContext: ctx(),
		RedeemPath:  "/auth/password/reset",
	}))
	mustContain(t, "Reset", body,
		`action="/auth/password/reset"`,
		`autocomplete="new-password"`,
		`name="token" id="reset-token" value=""`,
		`data-auth-fragment="hash"`,
		`data-auth-fragment-target="reset-token"`,
		`src="/assets/auth/fragment.js"`,
		"<noscript>",
	)
	// No inline script (the reader is externalized) and no token in a query string.
	mustNotContain(t, "Reset", body, "replaceState", "?token=", "&token=")
}

// TestReset_ErrorRerenderRetainsToken proves the IX-11 fix: on a validation-error
// re-render the handler echoes the SUBMITTED token into the hidden field (m.Token) so
// a corrected retry survives the already-scrubbed fragment. The token rides the hidden
// field value only — never a query string or referrer surface.
func TestReset_ErrorRerenderRetainsToken(t *testing.T) {
	const tok = "reset-token-abc123"
	body := render(t, newViews(t).ResetPassword(authentication.ResetPage{
		PageContext: ctx(),
		RedeemPath:  "/auth/password/reset",
		Token:       tok,
	}))
	mustContain(t, "Reset", body, `name="token" id="reset-token" value="`+tok+`"`)
	mustNotContain(t, "Reset", body, "?token="+tok, "&token="+tok)
}

// TestMagicLink_ExternalReaderScrubAndManualFallback proves the magic-link landing
// carries no token, wires the externalized fragment reader (which scrubs history and
// auto-submits), and offers a visible manual fallback — never a token in a query
// string and never an inline script.
func TestMagicLink_ExternalReaderScrubAndManualFallback(t *testing.T) {
	body := render(t, newViews(t).MagicLinkLanding(authentication.MagicLinkPage{
		PageContext: ctx(),
		RedeemPath:  "/auth/passwordless/redeem",
	}))
	mustContain(t, "MagicLink", body,
		`action="/auth/passwordless/redeem"`,
		`name="token" id="magic-token"`,
		`data-auth-fragment="token"`,
		`data-auth-fragment-submit="true"`,
		`src="/assets/auth/fragment.js"`,
		"Continue",
		"<noscript>",
	)
	mustNotContain(t, "MagicLink", body,
		`id="magic-token" value=`,
		"form.submit()",
		"?token=",
		"&token=",
	)
}

// TestPasswordlessStart_TelAutocompleteByKind proves the identifier input adapts its
// autocomplete/type to the selected kind.
func TestPasswordlessStart_TelAutocompleteByKind(t *testing.T) {
	v := newViews(t)
	body := render(t, v.PasswordlessStart(authentication.PasswordlessStartPage{
		PageContext: ctx(),
		Kind:        "phone",
		Kinds:       []string{"email", "phone"},
	}))
	mustContain(t, "PasswordlessStart phone", body,
		`action="/auth/passwordless/start"`,
		`type="tel"`,
		`autocomplete="tel"`,
	)

	emailBody := render(t, v.PasswordlessStart(authentication.PasswordlessStartPage{
		PageContext: ctx(),
		Kind:        "email",
		Kinds:       []string{"email"},
	}))
	mustContain(t, "PasswordlessStart email", emailBody, `autocomplete="email"`, `type="hidden" name="kind" value="email"`)
}

// TestPasswordlessCode_OneTimeCode proves the OTP entry form uses one-time-code and
// carries the plain identifier hidden while displaying the masked one.
func TestPasswordlessCode_OneTimeCode(t *testing.T) {
	body := render(t, newViews(t).PasswordlessCode(authentication.PasswordlessCodePage{
		PageContext:      ctx(),
		Kind:             "email",
		Identifier:       "user@example.com",
		MaskedIdentifier: "u•••@example.com",
	}))
	mustContain(t, "PasswordlessCode", body,
		`action="/auth/passwordless/verify"`,
		`autocomplete="one-time-code"`,
		`type="hidden" name="identifier" value="user@example.com"`,
		"u•••@example.com",
	)
}

// TestStepUp_OffersOnlyViableMethods proves step-up renders only the completion
// methods the service reported viable.
func TestStepUp_OffersOnlyViableMethods(t *testing.T) {
	v := newViews(t)
	pwOnly := render(t, v.StepUp(authentication.StepUpPage{
		PageContext:       ctx(),
		Operation:         "remove your password",
		PasswordAvailable: true,
	}))
	mustContain(t, "StepUp password", pwOnly,
		`action="/auth/step-up/password"`,
		`autocomplete="current-password"`,
		"remove your password",
	)
	mustNotContain(t, "StepUp password", pwOnly, `action="/auth/step-up/code"`)

	codeOnly := render(t, v.StepUp(authentication.StepUpPage{
		PageContext:      ctx(),
		MaskedIdentifier: "u•••@example.com",
		CodeAvailable:    true,
	}))
	mustContain(t, "StepUp code", codeOnly,
		`action="/auth/step-up/begin"`,
		`action="/auth/step-up/code"`,
		`autocomplete="one-time-code"`,
		"u•••@example.com",
	)
	mustNotContain(t, "StepUp code", codeOnly, `autocomplete="current-password"`)
}

// TestAccountSecurity_MaskedInventory proves the inventory renders masked values, the
// has/no-password branches, and management links — no secret appears.
func TestAccountSecurity_MaskedInventory(t *testing.T) {
	body := render(t, newViews(t).AccountSecurity(authentication.AccountSecurityPage{
		PageContext: authentication.PageContext{Actor: "a•••@example.com"},
		HasPassword: true,
		OAuth:       []authentication.OAuthMethod{{Provider: "github", Removable: true}},
		Identifiers: []authentication.IdentifierMethod{{ID: "id1", Kind: "email", MaskedValue: "u•••@example.com", Uses: []string{"login", "recovery"}, Primary: true}},
	}))
	mustContain(t, "AccountSecurity", body,
		"a•••@example.com",
		"u•••@example.com",
		"github",
		"login, recovery",
		"(primary)",
		`href="/auth/identifiers/id1/edit"`,
		`href="/auth/password/change"`,
		`href="/auth/oauth/github/unlink"`,
	)
}

// TestPasswordForm_Variants proves the set/change/remove variants render their
// respective controls with correct autocomplete and never echo a secret.
func TestPasswordForm_Variants(t *testing.T) {
	v := newViews(t)
	set := render(t, v.PasswordForm(authentication.PasswordFormPage{PageContext: ctx(), Mode: "set"}))
	mustContain(t, "PasswordForm set", set, `action="/auth/password/set"`, `autocomplete="new-password"`)
	mustNotContain(t, "PasswordForm set", set, `autocomplete="current-password"`)

	change := render(t, v.PasswordForm(authentication.PasswordFormPage{PageContext: ctx(), Mode: "change", ShowCurrentPassword: true}))
	mustContain(t, "PasswordForm change", change,
		`action="/auth/password/change"`,
		`autocomplete="current-password"`,
		`autocomplete="new-password"`,
	)

	remove := render(t, v.PasswordForm(authentication.PasswordFormPage{PageContext: ctx(), Mode: "remove", MaskedDestination: "u•••@example.com"}))
	mustContain(t, "PasswordForm remove", remove,
		`action="/auth/password/remove/start"`,
		`action="/auth/password/remove"`,
		`autocomplete="one-time-code"`,
		"u•••@example.com",
	)
}

// TestIdentifierForm_AddEdit proves the add form uses the kind-appropriate input and
// the edit form shows the masked existing value without an address input.
func TestIdentifierForm_AddEdit(t *testing.T) {
	v := newViews(t)
	add := render(t, v.IdentifierForm(authentication.IdentifierFormPage{
		PageContext:  ctx(),
		Mode:         "add",
		Kind:         "phone",
		LoginEnabled: true,
	}))
	mustContain(t, "IdentifierForm add", add,
		`action="/auth/identifiers/phone"`,
		`type="tel"`,
		`autocomplete="tel"`,
		`name="login" value="true" checked`,
	)

	edit := render(t, v.IdentifierForm(authentication.IdentifierFormPage{
		PageContext: ctx(),
		Mode:        "edit",
		Kind:        "email",
		ID:          "id1",
		MaskedValue: "u•••@example.com",
	}))
	mustContain(t, "IdentifierForm edit", edit,
		`action="/auth/identifiers/id1"`,
		"u•••@example.com",
		`type="hidden" name="id" value="id1"`,
		`type="hidden" name="action" value="remove"`,
	)
	mustNotContain(t, "IdentifierForm edit", edit, `name="value"`)
}

// TestIdentifierForm_Confirm proves the ownership-proof code form posts the
// kind-specific confirm edge and echoes no code (the code is delivered out-of-band).
func TestIdentifierForm_Confirm(t *testing.T) {
	confirm := render(t, newViews(t).IdentifierForm(authentication.IdentifierFormPage{
		PageContext: ctx(),
		Mode:        "confirm",
		Kind:        "phone",
	}))
	mustContain(t, "IdentifierForm confirm", confirm,
		`action="/auth/identifiers/phone/confirm"`,
		`autocomplete="one-time-code"`,
		`type="hidden" name="kind" value="phone"`,
	)
	mustNotContain(t, "IdentifierForm confirm", confirm, `name="value"`)
}

// TestOAuthUnlink_ProviderBound proves the unlink confirmation posts to the same
// provider it names and delivers the code to a masked address it never echoes.
func TestOAuthUnlink_ProviderBound(t *testing.T) {
	body := render(t, newViews(t).OAuthUnlink(authentication.OAuthUnlinkPage{
		PageContext:       ctx(),
		Provider:          "github",
		MaskedDestination: "u•••@example.com",
	}))
	mustContain(t, "OAuthUnlink", body,
		`action="/auth/oauth/github/unlink/start"`,
		`action="/auth/oauth/github/unlink"`,
		`autocomplete="one-time-code"`,
		"u•••@example.com",
	)
}

// TestErrorStatus_GenericCopy proves the error/status pages render generic copy and
// the HTTP status text.
func TestErrorStatus_GenericCopy(t *testing.T) {
	v := newViews(t)
	errBody := render(t, v.Error(authentication.ErrorPage{Status: 400, Message: "That link is no longer valid."}))
	mustContain(t, "Error", errBody, "400 Bad Request", "That link is no longer valid.")

	statusBody := render(t, v.Status(authentication.StatusPage{Title: "Check your inbox", Detail: "We sent you a message."}))
	mustContain(t, "Status", statusBody, "Check your inbox", "We sent you a message.")
}

// TestEmbed_OverrideOneMethod proves the blessed partial-override path: an embed of
// the ui/goth Views that overrides only Login keeps every other method's default.
type overrideLogin struct{ Views }

func (overrideLogin) Login(authentication.LoginPage) web.Renderer { return marker("CUSTOM-LOGIN") }

type marker string

func (m marker) Render(_ context.Context, w io.Writer) error {
	_, err := io.WriteString(w, string(m))
	return err
}

func TestEmbed_OverrideOneMethod(t *testing.T) {
	var v authentication.Views = overrideLogin{newViews(t)}
	login := render(t, v.Login(authentication.LoginPage{}))
	if !strings.Contains(login, "CUSTOM-LOGIN") {
		t.Fatalf("overridden Login did not render custom marker:\n%s", login)
	}
	reg := render(t, v.Register(authentication.RegisterPage{}))
	if !strings.Contains(reg, `action="/auth/register"`) {
		t.Fatalf("non-overridden Register did not render the ui/goth default:\n%s", reg)
	}
}
