package goth

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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
		"[data-auth-passkey-require-uv]",
		`mode === "register" && Boolean(uv && uv.checked)`,
		`requireUV ? { user_verification: "required" } : {}`,
		`requireUV && finish.status === 401 && finish.data.code === "passkey_rejected"`,
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
		"That passkey didn't confirm a PIN or biometric. Try again and use Face ID, Touch ID or your PIN.",
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
		`Added <time datetime="2026-10-01T00:00:00Z">Oct 1, 2026 at 00:00 UTC</time>.`,
		`Last used <time datetime="2026-10-02T00:00:00Z">Oct 2, 2026 at 00:00 UTC</time>.`, "Not used yet.",
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
		`<label for="passkey-require-uv" data-slot="auth-passkey-require-uv">`,
		`id="passkey-require-uv"`,
		`data-auth-passkey-require-uv`,
		"Require Face ID, Touch ID or a PIN",
	)
	if strings.Count(body, `value="remove"`) != 1 {
		t.Error("only the removable passkey offers a remove form")
	}
	mustNotContain(t, "manage", body, "data-return-to")

	empty := render(t, newViews(t).Passkeys(inbound.PasskeysPage{PageContext: ctx(), Links: *passkeyLinks()}))
	mustContain(t, "manage empty", empty, "You haven't added a passkey yet.", `data-return-to="/dashboard"`)
}

// TestPasskeysPage_RequireUVDefault proves the "Require Face ID, Touch ID or a
// PIN" box starts checked while the user holds no AAL2-capable passkey (none, or
// presence-only ones) and unchecked once they hold one.
func TestPasskeysPage_RequireUVDefault(t *testing.T) {
	v := newViews(t)
	presenceOnly := inbound.PasskeyItem{ID: "pk2", Name: "Key", Action: "/auth/passkeys/pk2"}
	verified := inbound.PasskeyItem{ID: "pk1", Name: "Laptop", UserVerified: true, Action: "/auth/passkeys/pk1"}
	for name, tc := range map[string]struct {
		passkeys []inbound.PasskeyItem
		checked  bool
	}{
		"none":          {nil, true},
		"presence-only": {[]inbound.PasskeyItem{presenceOnly}, true},
		"verified":      {[]inbound.PasskeyItem{presenceOnly, verified}, false},
	} {
		body := render(t, v.Passkeys(inbound.PasskeysPage{PageContext: ctx(), Links: *passkeyLinks(), Passkeys: tc.passkeys}))
		box := regexp.MustCompile(`<input[^>]*id="passkey-require-uv"[^>]*>`).FindString(body)
		if box == "" {
			t.Fatalf("%s: no require-uv checkbox", name)
		}
		if got := strings.Contains(box, " checked"); got != tc.checked {
			t.Errorf("%s: checked = %v, want %v (%s)", name, got, tc.checked, box)
		}
	}
}

// TestPasskeysPage_ConfirmLink proves the manage page offers the passkey
// step-up, returning here, once the user holds an AAL2-capable passkey (the
// add and remove gate then needs a recent passkey check), and not before.
func TestPasskeysPage_ConfirmLink(t *testing.T) {
	v := newViews(t)
	presenceOnly := inbound.PasskeyItem{ID: "pk2", Name: "Key", Action: "/auth/passkeys/pk2"}
	verified := inbound.PasskeyItem{ID: "pk1", Name: "Laptop", UserVerified: true, Action: "/auth/passkeys/pk1"}
	link := `href="/auth/step-up/passkey?return_to=%2Fauth%2Faccount%2Fpasskeys"`

	with := render(t, v.Passkeys(inbound.PasskeysPage{PageContext: ctx(), Links: *passkeyLinks(), Passkeys: []inbound.PasskeyItem{presenceOnly, verified}}))
	mustContain(t, "verified", with, "needs a recent passkey check", link, "Confirm with a passkey")
	for name, passkeys := range map[string][]inbound.PasskeyItem{"none": nil, "presence-only": {presenceOnly}} {
		body := render(t, v.Passkeys(inbound.PasskeysPage{PageContext: ctx(), Links: *passkeyLinks(), Passkeys: passkeys}))
		mustNotContain(t, name, body, "Confirm with a passkey")
	}
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

// TestPasskeyStepUpLink_ArrivesChecked proves the step-up page's "Add a passkey"
// link carries return_to intact and lands on a manage page whose require-uv box
// is checked: the link shows only without an AAL2-capable passkey, the same
// predicate that checks the box, so it needs no parameter of its own.
func TestPasskeyStepUpLink_ArrivesChecked(t *testing.T) {
	v := newViews(t)
	pc := ctx()
	pc.ReturnTo = "/demo/mfa/page"
	stepUp := render(t, v.PasskeyStepUp(inbound.PasskeyStepUpPage{PageContext: pc, Links: *passkeyLinks(), HasPasskeys: true}))
	href := regexp.MustCompile(`href="(/auth/account/passkeys\?[^"]*)"`).FindStringSubmatch(stepUp)
	if href == nil {
		t.Fatal("no Add a passkey link")
	}
	u, err := url.Parse(href[1])
	if err != nil {
		t.Fatal(err)
	}
	if got := u.Query().Get("return_to"); got != "/auth/step-up/passkey?return_to=%2Fdemo%2Fmfa%2Fpage" {
		t.Fatalf("return_to = %q", got)
	}
	manage := pc
	manage.ReturnTo = u.Query().Get("return_to")
	body := render(t, v.Passkeys(inbound.PasskeysPage{PageContext: manage, Links: *passkeyLinks(),
		Passkeys: []inbound.PasskeyItem{{ID: "pk2", Name: "Key", Action: "/auth/passkeys/pk2"}}}))
	if box := regexp.MustCompile(`<input[^>]*id="passkey-require-uv"[^>]*>`).FindString(body); !strings.Contains(box, " checked") {
		t.Error("the manage page reached from the step-up link must arrive checked")
	}
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
