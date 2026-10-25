package authpages

import (
	"bytes"
	"context"
	"strings"
	"testing"

	inbound "github.com/gopernicus/gopernicus/pockets/authentication/inbound/http"
	delivery "github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
	uigoth "github.com/gopernicus/gopernicus/ui/goth"
)

// renderMethod executes a web.Renderer to a string.
func renderMethod(t *testing.T, r web.Renderer) string {
	t.Helper()
	var buf bytes.Buffer
	if err := r.Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	return buf.String()
}

// newViews builds the host override over a default ui/goth bundle.
func newViews(t *testing.T) Views {
	t.Helper()
	b, err := uigoth.New()
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	v, err := New(b)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return v
}

// TestLoginOverrideRendersBrandedForm proves the host's Login override renders the
// Gopernicus-CMS-branded page while preserving every field the pocket's dispatcher,
// CSRF gate, and service call require: the canonical /auth/login action, the
// csrf_token and return_to hidden fields, the echoed email, and the correctly
// autocompleted email/password inputs. The password input never carries a value, so
// a failed attempt cannot repopulate it.
func TestLoginOverrideRendersBrandedForm(t *testing.T) {
	v := newViews(t)
	m := inbound.LoginPage{
		PageContext: inbound.PageContext{CSRFToken: "csrf-abc", ReturnTo: "/dashboard"},
		Email:       "user@example.com",
	}
	out := renderMethod(t, v.Login(m))

	for _, want := range []string{
		"Gopernicus CMS",              // host brand marker
		`data-brand="gopernicus-cms"`, // machine-checkable brand marker
		`action="/auth/login"`,        // canonical endpoint unchanged
		`name="csrf_token" value="csrf-abc"`,
		`name="return_to" value="/dashboard"`,
		`name="email"`,
		`autocomplete="email"`,
		`value="user@example.com"`, // echoed non-secret email
		`name="password"`,
		`autocomplete="current-password"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("branded login missing %q\n---\n%s", want, out)
		}
	}
	// The password input must never carry a value attribute (no secret repopulation).
	if strings.Contains(out, `name="password" type="password" autocomplete="current-password" required aria-describedby="password-error" value=`) {
		t.Error("password input carries a value attribute (secret repopulation)")
	}
}

// TestPromotedDefaultsServeOtherPages proves the embed satisfies the whole port: a
// page the host does NOT override (Register) is served by the promoted bundled
// default, so the override changes exactly one page and leaves the rest intact.
func TestPromotedDefaultsServeOtherPages(t *testing.T) {
	v := newViews(t)
	// The compile-time assertion var _ auth.Views = Views{} (in authpages.go) already
	// proves every method exists; this confirms a non-overridden method renders the
	// bundled default rather than a host page.
	out := renderMethod(t, v.Register(inbound.RegisterPage{}))
	if !strings.Contains(out, `action="/auth/register"`) {
		t.Errorf("Register did not render the promoted bundled default:\n%s", out)
	}
	if strings.Contains(out, "Gopernicus CMS") {
		t.Error("Register unexpectedly carries the host brand: the override leaked past Login")
	}
}

// TestEmailOverrideTargetsFeatureNamespace proves the host email override targets the
// pocket's email namespace (so it overrides a bundled LayerCore template) and is a
// distinct facility from the page Views above.
func TestEmailOverrideTargetsFeatureNamespace(t *testing.T) {
	o := EmailOverride()
	if o.Namespace != delivery.Namespace {
		t.Fatalf("EmailOverride Namespace = %q, want %q", o.Namespace, delivery.Namespace)
	}
	data, err := o.FS.ReadFile("templates/verification.html")
	if err != nil {
		t.Fatalf("read embedded override template: %v", err)
	}
	if !strings.Contains(string(data), "Gopernicus CMS") || !strings.Contains(string(data), "{{.Secret}}") {
		t.Fatalf("override template missing brand or {{.Secret}}: %s", data)
	}
}

// TestLoginOverrideRendersPasskeyContract proves the branded Login renders the
// custom-view passkey contract from LoginPage.Passkeys: the hidden sign-in
// control, its data attributes, the empty live regions, and the un-nonced script
// tag of the mounted path — and none of it when passkeys are off.
func TestLoginOverrideRendersPasskeyContract(t *testing.T) {
	v := newViews(t)
	m := inbound.LoginPage{
		PageContext: inbound.PageContext{CSRFToken: "csrf-abc", CSPNonce: "nonce-1", ReturnTo: "/dashboard"},
		Passkeys:    &inbound.PasskeyLinks{AssertionOptions: "/auth/passkeys/assertion/options", Assertion: "/auth/passkeys/assertion"},
	}
	out := renderMethod(t, v.Login(m))
	for _, want := range []string{
		`<script src="/assets/auth/passkey.js" defer></script>`,
		`data-auth-passkey="signin"`,
		`data-options-url="/auth/passkeys/assertion/options"`,
		`data-finish-url="/auth/passkeys/assertion"`,
		`data-csrf="csrf-abc"`,
		`data-return-to="/dashboard" hidden>`,
		`<button type="button" data-auth-passkey-start>Sign in with a passkey</button>`,
		`<p role="status" data-auth-passkey-status></p>`,
		`<p role="alert" data-auth-passkey-error></p>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("branded login missing %q\n---\n%s", want, out)
		}
	}
	if strings.Contains(out, "nonce-1") || strings.Contains(out, "onclick") {
		t.Error("the passkey markup must carry no nonce and no inline handler")
	}

	m.Passkeys = nil
	if off := renderMethod(t, v.Login(m)); strings.Contains(off, "passkey") {
		t.Errorf("login without passkeys renders passkey markup:\n%s", off)
	}
}
