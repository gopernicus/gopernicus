package templ

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/features/authentication"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

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

// TestAllPagesRenderZeroModels proves every page renders from a zero model without
// error — the minimal-model smoke test across the whole port.
func TestAllPagesRenderZeroModels(t *testing.T) {
	v := New()
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

// TestLogin_LabelsAutocompleteCSRF proves the login form's accessible labels,
// correct autocomplete tokens, the CSRF hidden field, and the validated return-to
// hidden field. The entered email is echoed; the password is never echoed.
func TestLogin_LabelsAutocompleteCSRF(t *testing.T) {
	body := render(t, New().Login(authentication.LoginPage{
		PageContext: ctx(),
		Email:       "user@example.com",
	}))
	mustContain(t, "Login", body,
		`action="/auth/login"`,
		`autocomplete="email"`,
		`type="email"`,
		`autocomplete="current-password"`,
		`<label for="email">`,
		`<label for="password">`,
		`name="csrf_token" value="csrf-abc123"`,
		`name="return_to" value="/dashboard"`,
		`value="user@example.com"`,
		`role="alert"`,
		"That email looks invalid.",
	)
	// The password input carries no value attribute — a failed attempt never
	// repopulates a secret.
	if strings.Contains(body, `type="password" autocomplete="current-password" value=`) {
		t.Errorf("Login repopulated the password field:\n%s", body)
	}
}

// TestRegister_NewPassword proves the registration form uses new-password.
func TestRegister_NewPassword(t *testing.T) {
	body := render(t, New().Register(authentication.RegisterPage{PageContext: ctx(), Email: "u@e.com", DisplayName: "Ada"}))
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
	body := render(t, New().Verify(authentication.VerifyPage{
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

// TestReset_FragmentTokenNeverRendered proves the reset token is never a
// server-rendered value: the model carries no token, the form has an empty hidden
// token field the nonced script fills client-side, and no token appears in a query
// string.
func TestReset_FragmentTokenNeverRendered(t *testing.T) {
	body := render(t, New().ResetPassword(authentication.ResetPage{
		PageContext: ctx(),
		RedeemPath:  "/auth/password/reset",
	}))
	mustContain(t, "Reset", body,
		`action="/auth/password/reset"`,
		`autocomplete="new-password"`,
		`name="token" id="reset-token"`,
		`nonce="nonce-xyz789"`,
		"replaceState",
		"<noscript>",
	)
	// The hidden token field ships empty (no value attribute) and no token rides a
	// query string.
	mustNotContain(t, "Reset", body,
		`id="reset-token" value=`,
		"?token=",
		"&token=",
	)
}

// TestMagicLink_NoncedScrubAndManualFallback proves the magic-link landing carries
// no token, reads it from the fragment via a nonced script that scrubs history and
// submits, and offers a visible manual fallback — never a token in a query string.
func TestMagicLink_NoncedScrubAndManualFallback(t *testing.T) {
	body := render(t, New().MagicLinkLanding(authentication.MagicLinkPage{
		PageContext: ctx(),
		RedeemPath:  "/auth/passwordless/redeem",
	}))
	mustContain(t, "MagicLink", body,
		`action="/auth/passwordless/redeem"`,
		`name="token" id="magic-token"`,
		`nonce="nonce-xyz789"`,
		"location.hash",
		"replaceState",
		"form.submit()",
		"<button type=\"submit\">Continue</button>",
		"<noscript>",
	)
	mustNotContain(t, "MagicLink", body,
		`id="magic-token" value=`,
		"?token=",
		"&token=",
	)
}

// TestMagicLink_NoNonceNoScript proves the enumeration-safe degradation: with no
// CSP nonce the page renders no inline script (nothing to bypass CSP), keeping the
// manual fallback form.
func TestMagicLink_NoNonceNoScript(t *testing.T) {
	body := render(t, New().MagicLinkLanding(authentication.MagicLinkPage{
		PageContext: authentication.PageContext{CSRFToken: "t"},
		RedeemPath:  "/auth/passwordless/redeem",
	}))
	mustNotContain(t, "MagicLink no-nonce", body, "<script")
	mustContain(t, "MagicLink no-nonce", body, "Continue")
}

// TestPasswordlessStart_TelAutocompleteByKind proves the identifier input adapts
// its autocomplete/type to the selected kind.
func TestPasswordlessStart_TelAutocompleteByKind(t *testing.T) {
	body := render(t, New().PasswordlessStart(authentication.PasswordlessStartPage{
		PageContext: ctx(),
		Kind:        "phone",
		Kinds:       []string{"email", "phone"},
	}))
	mustContain(t, "PasswordlessStart phone", body,
		`action="/auth/passwordless/start"`,
		`type="tel"`,
		`autocomplete="tel"`,
	)

	emailBody := render(t, New().PasswordlessStart(authentication.PasswordlessStartPage{
		PageContext: ctx(),
		Kind:        "email",
		Kinds:       []string{"email"},
	}))
	mustContain(t, "PasswordlessStart email", emailBody, `autocomplete="email"`, `type="hidden" name="kind" value="email"`)
}

// TestPasswordlessCode_OneTimeCode proves the OTP entry form uses one-time-code and
// carries the plain identifier hidden while displaying the masked one.
func TestPasswordlessCode_OneTimeCode(t *testing.T) {
	body := render(t, New().PasswordlessCode(authentication.PasswordlessCodePage{
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
	pwOnly := render(t, New().StepUp(authentication.StepUpPage{
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

	codeOnly := render(t, New().StepUp(authentication.StepUpPage{
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

// TestAccountSecurity_MaskedInventory proves the inventory renders masked values,
// the has/no-password branches, and management links — no secret appears.
func TestAccountSecurity_MaskedInventory(t *testing.T) {
	body := render(t, New().AccountSecurity(authentication.AccountSecurityPage{
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
	set := render(t, New().PasswordForm(authentication.PasswordFormPage{PageContext: ctx(), Mode: "set"}))
	mustContain(t, "PasswordForm set", set, `action="/auth/password/set"`, `autocomplete="new-password"`)
	mustNotContain(t, "PasswordForm set", set, `autocomplete="current-password"`)

	change := render(t, New().PasswordForm(authentication.PasswordFormPage{PageContext: ctx(), Mode: "change", ShowCurrentPassword: true}))
	mustContain(t, "PasswordForm change", change,
		`action="/auth/password/change"`,
		`autocomplete="current-password"`,
		`autocomplete="new-password"`,
	)

	remove := render(t, New().PasswordForm(authentication.PasswordFormPage{PageContext: ctx(), Mode: "remove", MaskedDestination: "u•••@example.com"}))
	mustContain(t, "PasswordForm remove", remove,
		`action="/auth/password/remove/start"`,
		`action="/auth/password/remove"`,
		`autocomplete="one-time-code"`,
		"u•••@example.com",
	)
}

// TestIdentifierForm_AddEdit proves the add form uses the kind-appropriate input
// and the edit form shows the masked existing value without an address input.
func TestIdentifierForm_AddEdit(t *testing.T) {
	add := render(t, New().IdentifierForm(authentication.IdentifierFormPage{
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

	edit := render(t, New().IdentifierForm(authentication.IdentifierFormPage{
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
		// The remove control posts the same {id} path with action=remove (the HTML twin
		// of the JSON DELETE edge), so the removal journey is reachable from the UI.
		`type="hidden" name="action" value="remove"`,
	)
	mustNotContain(t, "IdentifierForm edit", edit, `name="value"`)
}

// TestIdentifierForm_Confirm proves the ownership-proof code form posts the
// kind-specific confirm edge and echoes no code (the code is delivered out-of-band).
func TestIdentifierForm_Confirm(t *testing.T) {
	confirm := render(t, New().IdentifierForm(authentication.IdentifierFormPage{
		PageContext: ctx(),
		Mode:        "confirm",
		Kind:        "phone",
	}))
	mustContain(t, "IdentifierForm confirm", confirm,
		`action="/auth/identifiers/phone/confirm"`,
		`autocomplete="one-time-code"`,
		`type="hidden" name="kind" value="phone"`,
	)
	// The address input never appears on the confirm form; only the code is entered.
	mustNotContain(t, "IdentifierForm confirm", confirm, `name="value"`)
}

// TestOAuthUnlink_ProviderBound proves the unlink confirmation posts to the same
// provider it names and delivers the code to a masked address it never echoes.
func TestOAuthUnlink_ProviderBound(t *testing.T) {
	body := render(t, New().OAuthUnlink(authentication.OAuthUnlinkPage{
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
	errBody := render(t, New().Error(authentication.ErrorPage{Status: 400, Message: "That link is no longer valid."}))
	mustContain(t, "Error", errBody, "400 Bad Request", "That link is no longer valid.")

	statusBody := render(t, New().Status(authentication.StatusPage{Title: "Check your inbox", Detail: "We sent you a message."}))
	mustContain(t, "Status", statusBody, "Check your inbox", "We sent you a message.")
}

// TestEmbed_OverrideOneMethod proves the blessed partial-override path: an embed of
// the bundled default that overrides only Login keeps every other method's default.
type overrideLogin struct{ Views }

func (overrideLogin) Login(authentication.LoginPage) web.Renderer { return marker("CUSTOM-LOGIN") }

type marker string

func (m marker) Render(_ context.Context, w io.Writer) error {
	_, err := io.WriteString(w, string(m))
	return err
}

func TestEmbed_OverrideOneMethod(t *testing.T) {
	var v authentication.Views = overrideLogin{New()}
	login := render(t, v.Login(authentication.LoginPage{}))
	if !strings.Contains(login, "CUSTOM-LOGIN") {
		t.Fatalf("overridden Login did not render custom marker:\n%s", login)
	}
	// A non-overridden method still renders the bundled default chrome.
	reg := render(t, v.Register(authentication.RegisterPage{}))
	if !strings.Contains(reg, `action="/auth/register"`) {
		t.Fatalf("non-overridden Register did not render bundled default:\n%s", reg)
	}
}

// hostViews is the blessed AV3-8.5 partial-override host type: it embeds the bundled
// templ.Views and overrides only Login and AccountSecurity. Method promotion supplies
// every other method from the bundled default, so a host customizes exactly the two
// pages it cares about while the remaining fourteen keep their secure defaults.
type hostViews struct{ Views }

func (hostViews) Login(authentication.LoginPage) web.Renderer { return marker("HOST-LOGIN") }

func (hostViews) AccountSecurity(authentication.AccountSecurityPage) web.Renderer {
	return marker("HOST-ACCOUNT")
}

// TestEmbed_OverrideLoginAndAccountSecurity proves the AV3-8.5 partial-override
// contract: a host that embeds templ.Views and overrides only Login and
// AccountSecurity gets both overrides AND every promoted default. The host type
// satisfies authentication.Views by embedding alone — the compile-time assignment is
// itself the proof that the fourteen un-overridden methods are supplied.
func TestEmbed_OverrideLoginAndAccountSecurity(t *testing.T) {
	var v authentication.Views = hostViews{New()}

	// Both overrides win.
	if got := render(t, v.Login(authentication.LoginPage{})); !strings.Contains(got, "HOST-LOGIN") {
		t.Fatalf("overridden Login did not render host marker:\n%s", got)
	}
	if got := render(t, v.AccountSecurity(authentication.AccountSecurityPage{})); !strings.Contains(got, "HOST-ACCOUNT") {
		t.Fatalf("overridden AccountSecurity did not render host marker:\n%s", got)
	}

	// Every other method renders the bundled default — a representative sample across
	// public, passwordless, step-up, and account-security surfaces. None leaks the
	// host markers.
	promoted := map[string]string{
		"Register":          render(t, v.Register(authentication.RegisterPage{})),
		"Verify":            render(t, v.Verify(authentication.VerifyPage{})),
		"ForgotPassword":    render(t, v.ForgotPassword(authentication.ForgotPage{})),
		"ResetPassword":     render(t, v.ResetPassword(authentication.ResetPage{RedeemPath: "/auth/password/reset"})),
		"PasswordlessStart": render(t, v.PasswordlessStart(authentication.PasswordlessStartPage{})),
		"PasswordlessCode":  render(t, v.PasswordlessCode(authentication.PasswordlessCodePage{})),
		"MagicLinkLanding":  render(t, v.MagicLinkLanding(authentication.MagicLinkPage{RedeemPath: "/auth/passwordless/redeem"})),
		"CheckDelivery":     render(t, v.CheckDelivery(authentication.CheckDeliveryPage{})),
		"StepUp":            render(t, v.StepUp(authentication.StepUpPage{})),
		"IdentifierForm":    render(t, v.IdentifierForm(authentication.IdentifierFormPage{})),
		"PasswordForm":      render(t, v.PasswordForm(authentication.PasswordFormPage{})),
		"OAuthUnlink":       render(t, v.OAuthUnlink(authentication.OAuthUnlinkPage{})),
		"Status":            render(t, v.Status(authentication.StatusPage{})),
		"Error":             render(t, v.Error(authentication.ErrorPage{})),
	}
	// A landmark from the bundled default proves each promoted method really rendered
	// the shipped template rather than an override.
	wantLandmark := map[string]string{
		"Register":          `action="/auth/register"`,
		"Verify":            `autocomplete="one-time-code"`,
		"ForgotPassword":    `action="/auth/password/forgot"`,
		"ResetPassword":     `action="/auth/password/reset"`,
		"PasswordlessStart": `action="/auth/passwordless/start"`,
		"PasswordlessCode":  `action="/auth/passwordless/verify"`,
		"MagicLinkLanding":  `action="/auth/passwordless/redeem"`,
		"CheckDelivery":     "<html",
		"StepUp":            "<html",
		"IdentifierForm":    "<html",
		"PasswordForm":      "<html",
		"OAuthUnlink":       "<html",
		"Status":            "<html",
		"Error":             "<html",
	}
	for name, body := range promoted {
		if strings.Contains(body, "HOST-LOGIN") || strings.Contains(body, "HOST-ACCOUNT") {
			t.Errorf("promoted %s leaked a host override marker:\n%s", name, body)
		}
		if lm := wantLandmark[name]; !strings.Contains(body, lm) {
			t.Errorf("promoted %s did not render the bundled default (missing %q):\n%s", name, lm, body)
		}
	}
}
