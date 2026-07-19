// Package authpages is this host's REAL partial override of the authentication
// feature's HTML surface (design §9.2, AV3-8.9): it embeds the ui/goth
// views/goth.Views and overrides exactly ONE page — Login — with a
// Gopernicus-CMS-branded template rendered through sdk/foundation/web.Template
// (stdlib html/template, no templ import here). Every other page is served by the
// promoted ui/goth default, so the override changes presentation ONLY: route
// security, request decoding, service policy, redirect resolution, and error
// classification all live in the feature's inbound handlers, never in a Views
// method (proven byte-identical in AV3-8.5's isolation tests). Because it embeds
// authgoth.Views, HTMLPolicy() is promoted too, so the host wires the exact CSP the
// ui/goth pages need from this same value (ui-goth GOTH-7.2).
//
// It also carries the host's email LayerApp content override (EmailOverride), so
// this one package demonstrates BOTH override systems side by side and proves they
// are DISTINCT facilities: Config.Views swaps a browser PAGE, Config.
// EmailContentTemplates swaps a transactional EMAIL body. They share no type and
// feed different subsystems (the inbound HTML handlers vs. the delivery router).
//
// SECRET DISCIPLINE (design §9.2): the branded Login template renders no password,
// code, or token — the password input carries no value attribute, so a failed
// attempt never repopulates it, and only the display-safe LoginPage fields
// (CSRF token, validated return-to, echoed email, field errors) reach the browser.
package authpages

import (
	"embed"
	"html/template"

	auth "github.com/gopernicus/gopernicus/features/authentication"
	authgoth "github.com/gopernicus/gopernicus/features/authentication/views/goth"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
	uigoth "github.com/gopernicus/gopernicus/ui/goth"
)

// loginTemplateName is the parsed template Login executes.
const loginTemplateName = "login"

//go:embed templates/verification.html
var brandEmailFS embed.FS

// Views embeds the ui/goth authgoth.Views and overrides only Login. Every method
// the host does not override is promoted from the embedded default, so Views
// satisfies auth.Views in full (the compile assertion below) while presenting the
// host's own Login page. The embedded authgoth.Views also promotes HTMLPolicy(), the
// CSP the host wires so the ui/goth pages load their assets.
type Views struct {
	authgoth.Views
	login *template.Template
}

// _ pins Views to the authentication.Views port: the embedded default supplies the
// fifteen non-overridden methods, and Login below supplies the sixteenth.
var _ auth.Views = Views{}

// New returns the host override over the ui/goth bundle, with its branded Login
// template parsed once. It fails loudly if the ui/goth Views cannot be constructed
// (a nil bundle).
func New(bundle *uigoth.Bundle) (Views, error) {
	gv, err := authgoth.New(bundle)
	if err != nil {
		return Views{}, err
	}
	return Views{
		Views: gv,
		login: template.Must(template.New(loginTemplateName).Parse(loginHTML)),
	}, nil
}

// Login renders the Gopernicus-CMS-branded sign-in page. It posts to the same
// canonical /auth/login endpoint the bundled default does, with the same field
// names (email, password, csrf_token, return_to) so the feature's dispatcher,
// CSRF/origin gate, service call, and PRG are unchanged — only the chrome differs.
func (v Views) Login(m auth.LoginPage) web.Renderer {
	return web.Template(v.login, loginTemplateName, m)
}

// EmailOverride is the host's email LayerApp content override (design §6.2): a
// Gopernicus-CMS-branded verification email body that replaces the feature's
// LayerCore "authentication:verification" template. It still renders {{.Secret}}
// (the one-time code), so the verification flow is unbroken — only the copy is
// host-branded. This is the SECOND override system, wired through
// Config.EmailContentTemplates, distinct from the Views page override above.
func EmailOverride() auth.EmailContentTemplate {
	return auth.EmailContentTemplate{Namespace: auth.EmailContentNamespace, FS: brandEmailFS}
}

// loginHTML is the branded sign-in page. It is deliberately plain and asset-free so
// it renders within the feature's restrictive CSP (default-src 'none', no inline
// style, no third-party asset); the visible "Gopernicus CMS" branding and the
// data-brand marker make the override observable in the run-and-look. The password
// input has no value attribute, so a failed sign-in never repopulates it.
const loginHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="no-referrer">
<title>Sign in — Gopernicus CMS</title>
</head>
<body>
<header><p data-brand="gopernicus-cms">Gopernicus CMS</p></header>
<main>
<h1>Sign in to Gopernicus CMS</h1>
{{if .Message}}<p role="status">{{.Message}}</p>{{end}}
{{if .FieldErrors}}<div role="alert"><p>Please fix the following:</p><ul>{{range .FieldErrors}}<li><a href="#{{.Field}}">{{.Message}}</a></li>{{end}}</ul></div>{{end}}
<form method="post" action="/auth/login" autocomplete="on">
<input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
{{if .ReturnTo}}<input type="hidden" name="return_to" value="{{.ReturnTo}}">{{end}}
<p>
<label for="email">Email</label><br>
<input id="email" name="email" type="email" inputmode="email" autocomplete="email" value="{{.Email}}" required aria-describedby="email-error">
{{range .FieldErrors}}{{if eq .Field "email"}}<span class="field-error" id="email-error">{{.Message}}</span>{{end}}{{end}}
</p>
<p>
<label for="password">Password</label><br>
<input id="password" name="password" type="password" autocomplete="current-password" required aria-describedby="password-error">
{{range .FieldErrors}}{{if eq .Field "password"}}<span class="field-error" id="password-error">{{.Message}}</span>{{end}}{{end}}
</p>
<button type="submit">Sign in</button>
</form>
<p><a href="/auth/password/forgot">Forgot your password?</a></p>
<p><a href="/auth/register">Create an account</a></p>
{{if .PasswordlessEnabled}}<p><a href="/auth/passwordless">Sign in without a password</a></p>{{end}}
{{if .OAuthProviders}}<ul>{{range .OAuthProviders}}<li><a href="/auth/oauth/{{.}}/start" rel="nofollow">Continue with {{.}}</a></li>{{end}}</ul>{{end}}
</main>
</body>
</html>
`
