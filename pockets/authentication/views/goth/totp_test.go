package goth

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	inbound "github.com/gopernicus/gopernicus/pockets/authentication/inbound/http"
	uigoth "github.com/gopernicus/gopernicus/ui/goth"
)

// testOTPAuthURI is a representative key URI as the pocket builds it.
const testOTPAuthURI = "otpauth://totp/Gopernicus%20CMS:a%40example.com?algorithm=SHA1&digits=6&issuer=Gopernicus%20CMS&period=30&secret=JBSWY3DPEHPK3PXP"

// totpLinks is a representative TOTPLinks with the pages wired.
func totpLinks() inbound.TOTPLinks {
	return inbound.TOTPLinks{
		Manage:             "/auth/account/totp",
		Enroll:             "/auth/totp/enrollment",
		Confirm:            "/auth/totp/enrollment/confirm",
		Remove:             "/auth/totp",
		RecoveryCodes:      "/auth/recovery-codes",
		StepUp:             "/auth/step-up/totp",
		RecoveryCodeStepUp: "/auth/step-up/recovery-code",
		ChooserPage:        "/auth/step-up/mfa",
	}
}

// fakeQR returns a fixed 21×21 matrix whose first row is dark, recording the
// text it was asked to encode.
type fakeQR struct{ text string }

func (f *fakeQR) modules(text string) ([][]bool, error) {
	f.text = text
	m := make([][]bool, 21)
	for y := range m {
		m[y] = make([]bool, 21)
	}
	for x := range m[0] {
		m[0][x] = true
	}
	m[1][0], m[1][2], m[1][3] = true, true, true
	return m, nil
}

func newQRViews(t *testing.T, fn QRModules) Views {
	t.Helper()
	b, err := uigoth.New()
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	v, err := New(b, WithQR(fn))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return v
}

func enrollPage() inbound.TOTPEnrollPage {
	pc := ctx()
	pc.ReturnTo = ""
	pc.FieldErrors = nil
	return inbound.TOTPEnrollPage{
		PageContext: pc, Links: totpLinks(), Secret: "JBSW Y3DP EHPK 3PXP", OTPAuthURI: testOTPAuthURI,
		Account: "a@example.com", Issuer: "Gopernicus CMS", ExpiresAt: "2026-10-25T12:10:00Z",
	}
}

// TestQRSVG_PathQuietZoneAndSize pins the path builder: one rectangle per dark
// run offset by the four-module quiet zone, a view box that includes it, and an
// edge of whole pixels per module of at least 200 px.
func TestQRSVG_PathQuietZoneAndSize(t *testing.T) {
	f := &fakeQR{}
	m, _ := f.modules("")
	img, ok := qrSVG(m)
	if !ok {
		t.Fatal("qrSVG refused a square matrix")
	}
	if want := "M4 4h21v1h-21zM4 5h1v1h-1zM6 5h2v1h-2z"; img.Path != want {
		t.Errorf("path = %q, want %q", img.Path, want)
	}
	if img.ViewBox != "0 0 29 29" {
		t.Errorf("viewBox = %q, want 0 0 29 29", img.ViewBox)
	}
	if img.Pixels != "203" {
		t.Errorf("pixels = %q, want 203 (7 px per module)", img.Pixels)
	}
	if _, ok := qrSVG([][]bool{{true, false}, {true}}); ok {
		t.Error("qrSVG accepted a ragged matrix")
	}
	if _, ok := qrFor(func(string) ([][]bool, error) { return nil, errors.New("too long") }, testOTPAuthURI); ok {
		t.Error("qrFor built an image from a failing encoder")
	}
	if _, ok := qrFor(nil, testOTPAuthURI); ok {
		t.Error("qrFor built an image without an encoder")
	}
}

// TestTOTPEnroll_QR proves WithQR renders the otpauth:// URI as one inline SVG
// path, dark on a white square, with an accessible name and no script, data:
// URL or style; the key, the intact otpauth:// link and the confirm form sit
// beside it.
func TestTOTPEnroll_QR(t *testing.T) {
	f := &fakeQR{}
	body := render(t, newQRViews(t, f.modules).TOTPEnroll(enrollPage()))
	if f.text != testOTPAuthURI {
		t.Errorf("QR encoded %q, want the page's otpauth:// URI", f.text)
	}
	mustContain(t, "enroll", body,
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 29 29" width="203" height="203" shape-rendering="crispEdges" role="img" aria-label="QR code to set up your authenticator app" data-slot="auth-totp-qr">`,
		`<rect width="100%" height="100%" fill="#fff"></rect>`,
		`<path d="M4 4h21v1h-21zM4 5h1v1h-1zM6 5h2v1h-2z" fill="#000"></path>`,
		"Scan this QR code with your authenticator app.",
		`<code data-slot="auth-totp-secret">JBSW Y3DP EHPK 3PXP</code>`,
		`href="otpauth://totp/Gopernicus%20CMS:a%40example.com?algorithm=SHA1&amp;digits=6&amp;issuer=Gopernicus%20CMS&amp;period=30&amp;secret=JBSWY3DPEHPK3PXP"`,
		`action="/auth/totp/enrollment/confirm"`,
		`name="csrf_token" value="csrf-abc123"`,
		"Account: a@example.com.",
		"2026-10-25T12:10:00Z",
		`href="/auth/account/totp"`,
	)
	if strings.Count(body, "<path") != 1 {
		t.Error("the QR code must be exactly one path")
	}
	svg := regexp.MustCompile(`(?s)<svg.*</svg>`).FindString(body)
	mustNotContain(t, "svg", svg, "<script", "data:", "style", "<image", "nonce")
	mustNotContain(t, "enroll", body, "data:image", "<script")

	input := regexp.MustCompile(`<input[^>]*id="totp-code"[^>]*>`).FindString(body)
	for _, want := range []string{`name="code"`, `inputmode="numeric"`, `autocomplete="off"`, `autocapitalize="none"`, `spellcheck="false"`} {
		if !strings.Contains(input, want) {
			t.Errorf("confirm input missing %s: %s", want, input)
		}
	}
	if strings.Contains(input, "one-time-code") || strings.Contains(input, "value=") {
		t.Errorf("confirm input must not autofill or echo: %s", input)
	}
}

// TestTOTPEnroll_NoQR proves a Views without WithQR (or one whose encoder
// fails) renders the key and the otpauth:// link only.
func TestTOTPEnroll_NoQR(t *testing.T) {
	for name, v := range map[string]Views{
		"unwired": newViews(t),
		"failing": newQRViews(t, func(string) ([][]bool, error) { return nil, errors.New("too long") }),
	} {
		body := render(t, v.TOTPEnroll(enrollPage()))
		mustContain(t, name, body, "Enter this key in your authenticator app:", "JBSW Y3DP EHPK 3PXP", `href="otpauth://totp/`)
		mustNotContain(t, name, body, "<svg", "Scan this QR code")
	}
}

// TestTOTPPage_States proves the manage page's three states: set up (status,
// count, regenerate with expected_generated_at, remove signing out
// everywhere), pending (continue, cancel without signing out) and none (set
// up).
func TestTOTPPage_States(t *testing.T) {
	v := newViews(t)
	enrolled := render(t, v.TOTP(inbound.TOTPPage{
		PageContext: ctx(), Links: totpLinks(), Enrolled: true, EnrolledAt: "2026-10-01T00:00:00Z",
		RecoveryCodesRemaining: 9, RecoveryCodesGeneratedAt: "2026-10-01T00:00:00Z",
	}))
	mustContain(t, "enrolled", enrolled,
		"An authenticator app is set up.", "Set up 2026-10-01T00:00:00Z.", "Not used yet.",
		"You have 9 recovery codes left.",
		`action="/auth/recovery-codes"`,
		`<input type="hidden" name="expected_generated_at" value="2026-10-01T00:00:00Z">`,
		"Generate new recovery codes",
		`action="/auth/totp"`,
		"signs you out on every device.",
		"Remove authenticator app",
		`name="csrf_token" value="csrf-abc123"`,
		`href="/auth/account"`,
	)
	mustNotContain(t, "enrolled", enrolled, "Cancel setup", `action="/auth/totp/enrollment"`)
	mustContain(t, "none left", render(t, v.TOTP(inbound.TOTPPage{PageContext: ctx(), Links: totpLinks(), Enrolled: true})),
		"You have no recovery codes left.")

	pending := render(t, v.TOTP(inbound.TOTPPage{PageContext: ctx(), Links: totpLinks(), Pending: true}))
	mustContain(t, "pending", pending, `href="/auth/account/totp/enroll"`, "Continue setup", `action="/auth/totp"`,
		"Cancelling setup doesn't sign you out.", "Cancel setup")
	mustNotContain(t, "pending", pending, "every device", "Generate new recovery codes")

	none := render(t, v.TOTP(inbound.TOTPPage{PageContext: ctx(), Links: totpLinks()}))
	mustContain(t, "none", none, `action="/auth/totp/enrollment"`, "Set up an authenticator app")
	mustNotContain(t, "none", none, "every device", "Cancel setup", "recovery codes left")

	msg := ctx()
	msg.Message = "Your authenticator app is already set up."
	mustContain(t, "message", render(t, v.TOTP(inbound.TOTPPage{PageContext: msg, Links: totpLinks(), Enrolled: true})),
		`<p role="status" data-slot="auth-message">Your authenticator app is already set up.</p>`)
}

// TestRecoveryCodesPage proves the codes render once, with the save
// instruction and the "I saved these" link.
func TestRecoveryCodesPage(t *testing.T) {
	codes := []string{"aaaa-bbbb", "cccc-dddd", "eeee-ffff"}
	body := render(t, newViews(t).RecoveryCodes(inbound.RecoveryCodesPage{
		PageContext: ctx(), Links: totpLinks(), Codes: codes, GeneratedAt: "2026-10-25T00:00:00Z", Continue: "/auth/account/totp",
	}))
	mustContain(t, "codes", body,
		"Save these codes somewhere safe", "You won't see them again.",
		`<ol data-slot="auth-recovery-codes">`,
		"<li><code>aaaa-bbbb</code></li>", "<li><code>cccc-dddd</code></li>", "<li><code>eeee-ffff</code></li>",
		`<a href="/auth/account/totp" data-slot="auth-recovery-codes-saved">I saved these codes</a>`,
	)
}

// TestMFAStepUp_Chooser proves the chooser renders only the offered options,
// the passkey control first, each form carrying return_to; the recovery-code
// input takes no numeric mode; the passkey script loads only with the control.
func TestMFAStepUp_Chooser(t *testing.T) {
	v := newViews(t)
	pc := ctx()
	pc.ReturnTo = "/demo/mfa/page"
	pc.FieldErrors = nil

	all := render(t, v.MFAStepUp(inbound.MFAStepUpPage{
		PageContext: pc, Passkeys: passkeyLinks(), TOTP: totpLinks(),
		OfferPasskey: true, OfferTOTP: true, OfferRecoveryCode: true,
	}))
	mustContain(t, "all", all,
		passkeyScriptTagHTML,
		`data-auth-passkey="stepup"`, `data-return-to="/demo/mfa/page"`,
		`action="/auth/step-up/totp"`, `action="/auth/step-up/recovery-code"`,
		`<input type="hidden" name="return_to" value="/demo/mfa/page">`,
	)
	if p, totp, rc := strings.Index(all, `data-auth-passkey="stepup"`), strings.Index(all, `action="/auth/step-up/totp"`), strings.Index(all, `action="/auth/step-up/recovery-code"`); p > totp || totp > rc {
		t.Error("the chooser must order passkey, authenticator code, recovery code")
	}
	if strings.Count(all, `name="return_to"`) != 2 {
		t.Error("both code forms carry return_to")
	}
	rc := regexp.MustCompile(`<input[^>]*id="recovery-code"[^>]*>`).FindString(all)
	for _, want := range []string{`autocomplete="off"`, `autocapitalize="none"`, `spellcheck="false"`} {
		if !strings.Contains(rc, want) {
			t.Errorf("recovery input missing %s: %s", want, rc)
		}
	}
	if strings.Contains(rc, "inputmode") {
		t.Errorf("recovery input must not set an input mode: %s", rc)
	}
	mustNotContain(t, "all", all, "Set up an authenticator app", "Add a passkey")

	totpOnly := render(t, v.MFAStepUp(inbound.MFAStepUpPage{PageContext: pc, Passkeys: passkeyLinks(), TOTP: totpLinks(), OfferTOTP: true, OfferAddPasskey: true}))
	mustContain(t, "totp only", totpOnly, `action="/auth/step-up/totp"`,
		`href="/auth/account/passkeys?return_to=%2Fauth%2Fstep-up%2Fmfa%3Freturn_to%3D%252Fdemo%252Fmfa%252Fpage"`, "Add a passkey")
	mustNotContain(t, "totp only", totpOnly, "passkey.js", `data-auth-passkey="stepup"`, `action="/auth/step-up/recovery-code"`)

	setUp := render(t, v.MFAStepUp(inbound.MFAStepUpPage{PageContext: pc, TOTP: totpLinks(), OfferSetUpTOTP: true}))
	mustContain(t, "set up", setUp, `href="/auth/account/totp"`, "Set up an authenticator app")
	mustNotContain(t, "set up", setUp, "<form", "passkey.js", "Add a passkey")
}

// TestTOTPLinks_AccountStepUpPasskey proves the account page links the manage
// page, the generic step-up page links the chooser, and the passkey step-up
// page offers "Use an authenticator app instead" — each only when the TOTP
// pages are wired.
func TestTOTPLinks_AccountStepUpPasskey(t *testing.T) {
	v := newViews(t)
	links := totpLinks()
	jsonOnly := totpLinks()
	jsonOnly.Manage, jsonOnly.ChooserPage, jsonOnly.Remove = "", "", ""

	account := render(t, v.AccountSecurity(inbound.AccountSecurityPage{PageContext: ctx(), TOTP: &links}))
	mustContain(t, "account", account, `href="/auth/account/totp"`, "Manage authenticator app and recovery codes")
	for name, m := range map[string]inbound.AccountSecurityPage{"off": {PageContext: ctx()}, "json-only": {PageContext: ctx(), TOTP: &jsonOnly}} {
		mustNotContain(t, "account "+name, render(t, v.AccountSecurity(m)), "Manage authenticator app")
	}

	chooser := `href="/auth/step-up/mfa?return_to=%2Fdashboard"`
	stepUp := render(t, v.StepUp(inbound.StepUpPage{PageContext: ctx(), PasswordAvailable: true, TOTP: &links}))
	mustContain(t, "step-up", stepUp, chooser, "Use an authenticator app or a recovery code")
	mustNotContain(t, "step-up off", render(t, v.StepUp(inbound.StepUpPage{PageContext: ctx(), TOTP: &jsonOnly})), "authenticator app")

	none := render(t, v.PasskeyStepUp(inbound.PasskeyStepUpPage{PageContext: ctx(), Links: *passkeyLinks(), TOTP: &links}))
	mustContain(t, "passkey none", none, chooser, "Use an authenticator app instead")
	capable := render(t, v.PasskeyStepUp(inbound.PasskeyStepUpPage{PageContext: ctx(), Links: *passkeyLinks(), HasPasskeys: true, CanReachAAL2: true, TOTP: &links}))
	unsupported := regexp.MustCompile(`<p data-auth-passkey-unsupported>[^\n]*?</p>`).FindString(capable)
	if !strings.Contains(unsupported, chooser) {
		t.Errorf("the unsupported note must offer the authenticator app: %s", unsupported)
	}
	if strings.Count(capable, "Use an authenticator app instead") != 1 {
		t.Error("an AAL2-capable holder sees the authenticator link inside the unsupported note only")
	}
	mustNotContain(t, "passkey off", render(t, v.PasskeyStepUp(inbound.PasskeyStepUpPage{PageContext: ctx(), Links: *passkeyLinks(), HasPasskeys: true, CanReachAAL2: true})), "authenticator app")
}

// TestTOTPPages_RenderZeroModels proves every TOTP page renders from a zero
// model.
func TestTOTPPages_RenderZeroModels(t *testing.T) {
	v := newQRViews(t, (&fakeQR{}).modules)
	render(t, v.TOTP(inbound.TOTPPage{}))
	render(t, v.TOTPEnroll(inbound.TOTPEnrollPage{}))
	render(t, v.RecoveryCodes(inbound.RecoveryCodesPage{}))
	render(t, v.MFAStepUp(inbound.MFAStepUpPage{}))
	render(t, v.MFAStepUp(inbound.MFAStepUpPage{OfferPasskey: true}))
}
