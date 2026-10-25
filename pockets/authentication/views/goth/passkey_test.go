package goth

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	inbound "github.com/gopernicus/gopernicus/pockets/authentication/inbound/http"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
	uigoth "github.com/gopernicus/gopernicus/ui/goth"
)

// passkeyLinks is a representative PasskeyLinks with the HTML pages wired.
func passkeyLinks() *inbound.PasskeyLinks {
	return &inbound.PasskeyLinks{
		RegistrationOptions: "/auth/passkeys/registration/options",
		Registration:        "/auth/passkeys/registration",
		AssertionOptions:    "/auth/passkeys/assertion/options",
		Assertion:           "/auth/passkeys/assertion",
		StepUpOptions:       "/auth/step-up/passkey/options",
		StepUp:              "/auth/step-up/passkey",
		Manage:              "/auth/account/passkeys",
		StepUpPage:          "/auth/step-up/passkey",
	}
}

// passkeyScriptTagHTML is the exact, un-nonced tag every passkey page loads.
const passkeyScriptTagHTML = `<script src="/assets/auth/passkey.js" defer></script>`

// TestPasskeyScript_Budget pins the no-build-step budget: at most 200 lines.
func TestPasskeyScript_Budget(t *testing.T) {
	if n := bytes.Count(passkeyScript, []byte("\n")); n > 200 {
		t.Fatalf("passkey.js is %d lines, want <= 200", n)
	}
}

// TestPasskeyScript_ForbiddenTokens proves the script never evaluates strings,
// never writes HTML, and never relies on an inline handler.
func TestPasskeyScript_ForbiddenTokens(t *testing.T) {
	src := string(passkeyScript)
	for _, forbidden := range []string{
		"eval(", "new Function", "Function(", "innerHTML", "outerHTML", "insertAdjacentHTML",
		"document.write", "setAttribute(\"on", ".onclick", ".onsubmit", "javascript:",
		"setTimeout(\"", "setInterval(\"", "localStorage", "sessionStorage",
	} {
		if strings.Contains(src, forbidden) {
			t.Errorf("passkey.js contains forbidden %q", forbidden)
		}
	}
	if !strings.Contains(src, ".textContent") {
		t.Error("passkey.js must write messages through textContent")
	}
}

// TestPasskeyScript_Contract pins the wire contract the script speaks: the JSON
// type and the CSRF double-submit header, the browser JSON parsers with the
// base64url fallback, the in-flight guard, hidden-until-supported controls,
// click-only options, and step-up navigation on aal2 only.
func TestPasskeyScript_Contract(t *testing.T) {
	src := string(passkeyScript)
	mustContain(t, "passkey.js", src,
		`"Content-Type": "application/json"`,
		`"X-CSRF-Token": csrf`,
		`credentials: "same-origin"`,
		"parseCreationOptionsFromJSON",
		"parseRequestOptionsFromJSON",
		"c.toJSON",
		"unb64u(o.challenge)",
		"b64u(r.clientDataJSON)",
		`"aria-busy"`,
		"window.PublicKeyCredential",
		"root.hidden = false",
		"[data-auth-passkey-unsupported]",
		`finish.data.assurance !== "aal2"`,
		`addEventListener("click"`,
		"[data-auth-passkey-status]",
		"[data-auth-passkey-error]",
	)
	// Options are fetched on click only: the single post of the options URL
	// sits inside run, which only the click listener calls.
	if strings.Count(src, "ds.optionsUrl") != 1 || strings.Index(src, "ds.optionsUrl") < strings.Index(src, "async function run") {
		t.Error("the options fetch must happen only inside the click-driven run")
	}
	if strings.Count(src, "run(root, button);") != 1 {
		t.Error("run must be called from the click listener alone")
	}
}

// TestPasskeyScript_Copy pins the user-facing copy: the DOM errors (the U2F-only
// sentence included) and every wire code.
func TestPasskeyScript_Copy(t *testing.T) {
	src := string(passkeyScript)
	mustContain(t, "copy", src,
		`"NotAllowedError"`,
		"this security key can't store a passkey; use your device or a FIDO2 key with a PIN",
		`"InvalidStateError"`,
		"This device already has a passkey for your account.",
		`"SecurityError"`,
		"Passkeys can't be used on this address.",
		"That passkey can't confirm a PIN or biometric",
		"challenge_expired:",
		"passkey_rejected:",
		"passkey_exists:",
		"passkey_limit:",
		"too_many_attempts:",
	)
}

// TestPasskeyScriptHandler serves the script as JavaScript with nosniff, and
// PasskeyScript returns a private copy of the same bytes.
func TestPasskeyScriptHandler(t *testing.T) {
	rec := httptest.NewRecorder()
	PasskeyScriptHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, DefaultPasskeyScriptPath, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/javascript") {
		t.Fatalf("content-type = %q", ct)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
	body, _ := io.ReadAll(rec.Body)
	if !bytes.Equal(body, passkeyScript) {
		t.Fatal("served bytes differ from the embedded script")
	}
	cp := PasskeyScript()
	cp[0] = 'x'
	if passkeyScript[0] == 'x' {
		t.Fatal("PasskeyScript must return a copy")
	}
}

// TestPermissionsPolicyAdmitsPasskeys proves the security-headers preset the
// hosts install never denies the publickey-credentials-* features the passkey
// ceremonies need.
func TestPermissionsPolicyAdmitsPasskeys(t *testing.T) {
	h := web.SecurityHeaders()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/auth/login", nil))
	if pp := rec.Header().Get("Permissions-Policy"); strings.Contains(pp, "publickey-credentials") {
		t.Fatalf("Permissions-Policy %q denies publickey-credentials-*", pp)
	}
}

// TestLogin_PasskeyControl proves the login page renders the hidden passkey
// sign-in control with its data attributes and the un-nonced script tag, and
// renders neither without passkeys.
func TestLogin_PasskeyControl(t *testing.T) {
	v := newViews(t)
	body := render(t, v.Login(inbound.LoginPage{PageContext: ctx(), Passkeys: passkeyLinks()}))
	mustContain(t, "login", body,
		passkeyScriptTagHTML,
		`data-auth-passkey="signin"`,
		`data-options-url="/auth/passkeys/assertion/options"`,
		`data-finish-url="/auth/passkeys/assertion"`,
		`data-csrf="csrf-abc123"`,
		`data-return-to="/dashboard"`,
		`data-auth-passkey-start`,
		"Sign in with a passkey",
		`<p role="status" data-auth-passkey-status></p>`,
		`<p role="alert" data-auth-passkey-error></p>`,
	)
	if !regexp.MustCompile(`data-auth-passkey="signin"[^>]*\shidden>`).MatchString(body) {
		t.Error("the passkey control must render hidden")
	}
	mustNotContain(t, "login", body, "onclick=", `action="/auth/passkeys`)

	off := render(t, v.Login(inbound.LoginPage{PageContext: ctx()}))
	mustNotContain(t, "login without passkeys", off, "passkey.js", "data-auth-passkey")
}

// TestPasskeyPages_ScriptTagHasNoNonce proves every passkey page loads the
// script through the same-origin, un-nonced tag, honouring WithPasskeyScriptPath.
func TestPasskeyPages_ScriptTagHasNoNonce(t *testing.T) {
	b, err := uigoth.New()
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	v, err := New(b, WithPasskeyScriptPath("/custom/passkey.js"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for name, body := range map[string]string{
		"login":   render(t, v.Login(inbound.LoginPage{PageContext: ctx(), Passkeys: passkeyLinks()})),
		"step-up": render(t, v.StepUp(inbound.StepUpPage{PageContext: ctx(), Passkeys: passkeyLinks()})),
		"manage":  render(t, v.Passkeys(inbound.PasskeysPage{PageContext: ctx(), Links: *passkeyLinks()})),
		"passkey": render(t, v.PasskeyStepUp(inbound.PasskeyStepUpPage{PageContext: ctx(), Links: *passkeyLinks(), HasPasskeys: true})),
	} {
		mustContain(t, name, body, `<script src="/custom/passkey.js" defer></script>`)
	}
}

// TestPasskeysPage_Manage proves the manage page lists each passkey with per-row
// rename and remove forms, the sign-out-everywhere confirmation, the policy note
// for a non-removable passkey, and the add control with its optional name.
func TestPasskeysPage_Manage(t *testing.T) {
	pc := ctx()
	pc.ReturnTo = ""
	body := render(t, newViews(t).Passkeys(inbound.PasskeysPage{
		PageContext: pc,
		Links:       *passkeyLinks(),
		Passkeys: []inbound.PasskeyItem{
			{ID: "pk1", Name: "Laptop", CreatedAt: "2026-10-01T00:00:00Z", LastUsedAt: "2026-10-02T00:00:00Z", UserVerified: true, BackupState: true, Removable: true, Action: "/auth/passkeys/pk1"},
			{ID: "pk2", Name: "Key", CreatedAt: "2026-10-03T00:00:00Z", Removable: false, Action: "/auth/passkeys/pk2"},
		},
	}))
	mustContain(t, "manage", body,
		"Laptop", "Key", "Synced", "Confirms your PIN or biometric", "Presence only",
		"Last used 2026-10-02T00:00:00Z.", "Not used yet.",
		`action="/auth/passkeys/pk1"`, `action="/auth/passkeys/pk2"`,
		`name="action" value="rename"`, `name="action" value="remove"`,
		`id="passkey-name-pk1"`, `id="passkey-name-pk2"`,
		"Removing this passkey signs you out on every device.",
		nonRemovableNote,
		`data-auth-passkey="register"`,
		`data-options-url="/auth/passkeys/registration/options"`,
		`data-finish-url="/auth/passkeys/registration"`,
		`data-done-url="/auth/account/passkeys?auth=passkey_added"`,
		`data-auth-passkey-name`,
		`data-auth-passkey-unsupported`,
		`name="csrf_token" value="csrf-abc123"`,
	)
	if strings.Count(body, `value="remove"`) != 1 {
		t.Error("only the removable passkey offers a remove form")
	}
	mustNotContain(t, "manage", body, "data-return-to")

	empty := render(t, newViews(t).Passkeys(inbound.PasskeysPage{PageContext: ctx(), Links: *passkeyLinks()}))
	mustContain(t, "manage empty", empty, "You haven't added a passkey yet.", `data-return-to="/dashboard"`)
}

// TestPasskeyStepUpPage_States proves the three passkey step-up states: no
// passkey → "Add a passkey" returning here; presence-only → the PIN/biometric
// copy plus the control; an AAL2-capable passkey → the control alone.
func TestPasskeyStepUpPage_States(t *testing.T) {
	v := newViews(t)
	pc := ctx()
	pc.ReturnTo = "/demo/mfa/page"
	add := `href="/auth/account/passkeys?return_to=%2Fauth%2Fstep-up%2Fpasskey%3Freturn_to%3D%252Fdemo%252Fmfa%252Fpage"`

	none := render(t, v.PasskeyStepUp(inbound.PasskeyStepUpPage{PageContext: pc, Links: *passkeyLinks()}))
	mustContain(t, "none", none, "You haven't added a passkey yet.", add, "Add a passkey")
	mustNotContain(t, "none", none, `data-auth-passkey="stepup"`)

	presence := render(t, v.PasskeyStepUp(inbound.PasskeyStepUpPage{PageContext: pc, Links: *passkeyLinks(), HasPasskeys: true}))
	mustContain(t, "presence-only", presence, "can't confirm a PIN or biometric", add,
		`data-auth-passkey="stepup"`, `data-options-url="/auth/step-up/passkey/options"`,
		`data-finish-url="/auth/step-up/passkey"`, `data-return-to="/demo/mfa/page"`,
		"Use a passkey", `data-auth-passkey-unsupported`)

	capable := render(t, v.PasskeyStepUp(inbound.PasskeyStepUpPage{PageContext: pc, Links: *passkeyLinks(), HasPasskeys: true, CanReachAAL2: true}))
	mustContain(t, "capable", capable, `data-auth-passkey="stepup"`, "Use a passkey")
	mustNotContain(t, "capable", capable, "Your passkeys can't confirm", add)
}

// TestStepUpAndAccount_PasskeyOptions proves the generic step-up page offers a
// passkey completion and the account page links the manage page, each only when
// passkeys are on.
func TestStepUpAndAccount_PasskeyOptions(t *testing.T) {
	v := newViews(t)
	stepUp := render(t, v.StepUp(inbound.StepUpPage{PageContext: ctx(), PasswordAvailable: true, Passkeys: passkeyLinks()}))
	mustContain(t, "step-up", stepUp, `data-auth-passkey="stepup"`, "Confirm with a passkey", `action="/auth/step-up/password"`)
	mustNotContain(t, "step-up off", render(t, v.StepUp(inbound.StepUpPage{PageContext: ctx()})), "data-auth-passkey", "passkey.js")

	account := render(t, v.AccountSecurity(inbound.AccountSecurityPage{PageContext: ctx(), Passkeys: passkeyLinks()}))
	mustContain(t, "account", account, `href="/auth/account/passkeys"`, "Manage passkeys")
	mustNotContain(t, "account", account, "passkey.js")
	mustNotContain(t, "account off", render(t, v.AccountSecurity(inbound.AccountSecurityPage{PageContext: ctx()})), "Manage passkeys")
}
