package authentication

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// Presentation-isolation proofs (AV3-8.5). Two claims are proven here, both without
// importing the bundled templ module (the feature core is sdk-only, FS1):
//
//  1. The Views port is view-technology-neutral: a full implementation can render
//     every method through ONE reusable sdk/foundation/web.Template over a single
//     html/template set — templ is our default, never the contract.
//  2. Overriding presentation cannot bypass security. Swapping the rendering — a
//     partial override, or an entirely different view technology — changes the
//     response BODY only; the security envelope (status, redirect target, session
//     cookie, and the HTML security headers) is byte-identical, because every gate
//     (middleware chain, content-type dispatcher, service call, redirect allowlist,
//     status mapping) lives in the inbound handler, never in a Views method.

// htmlTemplateViews is a full-port implementation of the Views port built on one
// parsed html/template set and one reusable renderer (web.Template). It imports no
// templ. Each method names its template against the shared set; the model is passed
// straight through as template data. This is the AV3-8.5 "small full-port test
// implementation" — the concrete proof that a host can satisfy Views with stdlib
// templates alone.
type htmlTemplateViews struct{ t *template.Template }

// var _ pins htmlTemplateViews to the Views port at compile time.
var _ Views = htmlTemplateViews{}

// newHTMLTemplateViews parses one template per page into a single set. Every template
// carries a stable "TPL:<page>" landmark so a test can assert which page rendered; the
// bodies are deliberately minimal (this proves the seam, not the markup).
func newHTMLTemplateViews() htmlTemplateViews {
	t := template.New("auth")
	for name, body := range map[string]string{
		"login":              `<form action="/auth/login">TPL:login</form>`,
		"register":           `TPL:register`,
		"verify":             `TPL:verify`,
		"forgot":             `TPL:forgot`,
		"reset":              `TPL:reset`,
		"passwordless_start": `TPL:passwordless_start`,
		"passwordless_code":  `TPL:passwordless_code`,
		"magic":              `TPL:magic`,
		"check":              `TPL:check`,
		"stepup":             `TPL:stepup`,
		"account":            `TPL:account`,
		"identifier":         `TPL:identifier`,
		"password":           `TPL:password`,
		"unlink":             `TPL:unlink`,
		"status":             `TPL:status`,
		"error":              `TPL:error`,
	} {
		template.Must(t.New(name).Parse(body))
	}
	return htmlTemplateViews{t: t}
}

func (v htmlTemplateViews) Login(m LoginPage) web.Renderer { return web.Template(v.t, "login", m) }
func (v htmlTemplateViews) Register(m RegisterPage) web.Renderer {
	return web.Template(v.t, "register", m)
}
func (v htmlTemplateViews) Verify(m VerifyPage) web.Renderer { return web.Template(v.t, "verify", m) }
func (v htmlTemplateViews) ForgotPassword(m ForgotPage) web.Renderer {
	return web.Template(v.t, "forgot", m)
}
func (v htmlTemplateViews) ResetPassword(m ResetPage) web.Renderer {
	return web.Template(v.t, "reset", m)
}
func (v htmlTemplateViews) PasswordlessStart(m PasswordlessStartPage) web.Renderer {
	return web.Template(v.t, "passwordless_start", m)
}
func (v htmlTemplateViews) PasswordlessCode(m PasswordlessCodePage) web.Renderer {
	return web.Template(v.t, "passwordless_code", m)
}
func (v htmlTemplateViews) MagicLinkLanding(m MagicLinkPage) web.Renderer {
	return web.Template(v.t, "magic", m)
}
func (v htmlTemplateViews) CheckDelivery(m CheckDeliveryPage) web.Renderer {
	return web.Template(v.t, "check", m)
}
func (v htmlTemplateViews) StepUp(m StepUpPage) web.Renderer { return web.Template(v.t, "stepup", m) }
func (v htmlTemplateViews) AccountSecurity(m AccountSecurityPage) web.Renderer {
	return web.Template(v.t, "account", m)
}
func (v htmlTemplateViews) IdentifierForm(m IdentifierFormPage) web.Renderer {
	return web.Template(v.t, "identifier", m)
}
func (v htmlTemplateViews) PasswordForm(m PasswordFormPage) web.Renderer {
	return web.Template(v.t, "password", m)
}
func (v htmlTemplateViews) OAuthUnlink(m OAuthUnlinkPage) web.Renderer {
	return web.Template(v.t, "unlink", m)
}
func (v htmlTemplateViews) Status(m StatusPage) web.Renderer { return web.Template(v.t, "status", m) }
func (v htmlTemplateViews) Error(m ErrorPage) web.Renderer   { return web.Template(v.t, "error", m) }

// overrideViews embeds a base Views and overrides only Login and AccountSecurity —
// the same partial-override shape a host uses over the bundled templ.Views, expressed
// here against the port so the feature core need not import templ. Method promotion
// supplies every other method from the embedded base.
type overrideViews struct{ Views }

// var _ pins the override to the Views port at compile time (promotion + two overrides
// satisfy the whole interface).
var _ Views = overrideViews{}

func (overrideViews) Login(LoginPage) web.Renderer { return stubRenderer{"OVERRIDE-login"} }
func (overrideViews) AccountSecurity(AccountSecurityPage) web.Renderer {
	return stubRenderer{"OVERRIDE-account"}
}

// TestFullPortHTMLTemplateViewsRenders proves the stdlib-template full-port
// implementation satisfies the port and renders through the mount for every public
// page — no templ in sight.
func TestFullPortHTMLTemplateViewsRenders(t *testing.T) {
	h := newHTMLTestHandler(t, newHTMLTemplateViews())
	for _, p := range htmlPublicPages {
		rec := do(t, h, "GET", p.path, "")
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", p.path, rec.Code)
			continue
		}
		if got := rec.Body.String(); !strings.Contains(got, "TPL:"+p.marker) {
			t.Errorf("GET %s body = %q, want the html/template page (TPL:%s)", p.path, got, p.marker)
		}
	}
}

// securityEnvelope is the security-relevant projection of a response: everything a
// presentation override must NOT be able to change. It deliberately excludes the body.
type securityEnvelope struct {
	Status             int
	Location           string
	CacheControl       string
	ReferrerPolicy     string
	FrameOptions       string
	ContentTypeOptions string
	CSPPresent         bool
	HasSessionCookie   bool
}

func envelopeOf(rec *httptest.ResponseRecorder) securityEnvelope {
	return securityEnvelope{
		Status:             rec.Code,
		Location:           rec.Header().Get("Location"),
		CacheControl:       rec.Header().Get("Cache-Control"),
		ReferrerPolicy:     rec.Header().Get("Referrer-Policy"),
		FrameOptions:       rec.Header().Get("X-Frame-Options"),
		ContentTypeOptions: rec.Header().Get("X-Content-Type-Options"),
		CSPPresent:         rec.Header().Get("Content-Security-Policy") != "",
		HasSessionCookie:   sessionCookie(rec) != nil,
	}
}

// exercise runs the identical security-sensitive request sequence against a handler and
// returns the envelope of each response. Every request is state-independent (or depends
// only on state this same sequence establishes), so two handlers built with different
// Views produce identical envelopes iff presentation cannot affect security.
func exercise(t *testing.T, h http.Handler) map[string]securityEnvelope {
	t.Helper()
	out := map[string]securityEnvelope{}

	// 1. Public GET renders through the port — the body differs per Views, the security
	//    headers do not.
	out["get_login"] = envelopeOf(do(t, h, "GET", "/auth/login", ""))

	// 2. Cross-site browser form POST is blocked by the origin gate (login CSRF).
	r := httptest.NewRequest("POST", "/auth/login", strings.NewReader(url.Values{
		"email": {"x@example.com"}, "password": {"password123456789"},
	}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "https://evil.example.com")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	crossSite := httptest.NewRecorder()
	h.ServeHTTP(crossSite, r)
	out["cross_site_login"] = envelopeOf(crossSite)

	// 3. An unsupported content type is 415 — the dispatcher boundary, ahead of any
	//    Views method.
	out["unsupported_type"] = envelopeOf(doCT(t, h, "/auth/login", "text/plain", "hello"))

	// 4. Form login for an unknown account re-renders (the body differs) at the mapped
	//    failure status with no session cookie — the service decides, not the page.
	out["form_login_failure"] = envelopeOf(doFormReq(t, h, "/auth/login", url.Values{
		"email": {"ghost@example.com"}, "password": {"password123456789"},
	}))

	// 5. Register + login through the FORM arm establishes a session via 303 PRG — the
	//    same service the JSON arm calls, the same redirect allowlist, the same cookie.
	doFormReq(t, h, "/auth/register", url.Values{
		"email": {"iso@example.com"}, "password": {"password123456789"}, "display_name": {"Iso"},
	})
	out["form_login_success"] = envelopeOf(doFormReq(t, h, "/auth/login", url.Values{
		"email": {"iso@example.com"}, "password": {"password123456789"},
	}))

	// 6. A gated account page with no session redirects to login (the stale-session
	//    gate) — presentation never sees the request.
	out["get_account_no_session"] = envelopeOf(do(t, h, "GET", "/auth/account", ""))

	return out
}

// TestPresentationOverrideCannotBypassSecurity is the AV3-8.5 isolation proof: three
// hosts differing ONLY in presentation — the marker stub, a partial override of
// Login+AccountSecurity, and a completely different view technology (html/template) —
// produce byte-identical security envelopes across the whole sensitive request
// sequence. Swapping a page changes rendering only; it cannot bypass the middleware,
// decoder, service, redirect, or status policy.
func TestPresentationOverrideCannotBypassSecurity(t *testing.T) {
	base := exercise(t, newHTMLTestHandler(t, stubViews{}))
	override := exercise(t, newHTMLTestHandler(t, overrideViews{stubViews{}}))
	htmlTmpl := exercise(t, newHTMLTestHandler(t, newHTMLTemplateViews()))

	for key, want := range base {
		if got := override[key]; got != want {
			t.Errorf("partial override changed the security envelope for %q:\n got  %+v\n want %+v", key, got, want)
		}
		if got := htmlTmpl[key]; got != want {
			t.Errorf("html/template Views changed the security envelope for %q:\n got  %+v\n want %+v", key, got, want)
		}
	}

	// Sanity: the override and the html/template Views really did render different
	// bodies for the overridden Login page (so the envelope-equality above is a
	// non-trivial isolation result, not two identical renderers).
	overrideBody := do(t, newHTMLTestHandler(t, overrideViews{stubViews{}}), "GET", "/auth/login", "").Body.String()
	if !strings.Contains(overrideBody, "OVERRIDE-login") {
		t.Errorf("override Login body = %q, want the override marker", overrideBody)
	}
	tmplBody := do(t, newHTMLTestHandler(t, newHTMLTemplateViews()), "GET", "/auth/login", "").Body.String()
	if !strings.Contains(tmplBody, "TPL:login") {
		t.Errorf("html/template Login body = %q, want the html/template marker", tmplBody)
	}
}
