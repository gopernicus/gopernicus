package authentication

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/features/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/features/authentication/domain/oauthstate"
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk/capabilities/oauth"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// stubViews is a full, template-free implementation of the Views port for the
// transport tests: every method returns a marker renderer naming the page, so a test
// can assert both that a route mounted and that its handler rendered through the port
// (never a concrete component). It proves the port is satisfiable without importing
// templ — the CMS FS3 shape, adapted to auth.
type stubViews struct{}

// var _ pins the compile-time contract: stubViews satisfies the Views port. The
// bundled views/templ module (AV3-8.2) carries the same assertion.
var _ Views = stubViews{}

type stubRenderer struct{ name string }

func (s stubRenderer) Render(_ context.Context, w io.Writer) error {
	_, err := io.WriteString(w, "PAGE:"+s.name)
	return err
}

func (stubViews) Login(LoginPage) web.Renderer           { return stubRenderer{"login"} }
func (stubViews) Register(RegisterPage) web.Renderer     { return stubRenderer{"register"} }
func (stubViews) Verify(VerifyPage) web.Renderer         { return stubRenderer{"verify"} }
func (stubViews) ForgotPassword(ForgotPage) web.Renderer { return stubRenderer{"forgot"} }
func (stubViews) ResetPassword(ResetPage) web.Renderer   { return stubRenderer{"reset"} }
func (stubViews) PasswordlessStart(PasswordlessStartPage) web.Renderer {
	return stubRenderer{"passwordless_start"}
}
func (stubViews) PasswordlessCode(PasswordlessCodePage) web.Renderer {
	return stubRenderer{"passwordless_code"}
}
func (stubViews) MagicLinkLanding(MagicLinkPage) web.Renderer  { return stubRenderer{"magic"} }
func (stubViews) CheckDelivery(CheckDeliveryPage) web.Renderer { return stubRenderer{"check"} }
func (stubViews) StepUp(StepUpPage) web.Renderer               { return stubRenderer{"stepup"} }
func (stubViews) AccountSecurity(AccountSecurityPage) web.Renderer {
	return stubRenderer{"account"}
}
func (stubViews) IdentifierForm(IdentifierFormPage) web.Renderer { return stubRenderer{"identifier"} }
func (stubViews) PasswordForm(PasswordFormPage) web.Renderer     { return stubRenderer{"password"} }
func (stubViews) OAuthUnlink(OAuthUnlinkPage) web.Renderer       { return stubRenderer{"unlink"} }
func (stubViews) Status(StatusPage) web.Renderer                 { return stubRenderer{"status"} }
func (stubViews) Error(ErrorPage) web.Renderer                   { return stubRenderer{"error"} }

// newHTMLTestHandler builds a real authsvc.Service with BOTH passwordless and OAuth
// enabled — so the full HTML GET inventory (including the deny-by-absence
// passwordless and OAuth pages) is registerable — and mounts the routes with the
// given Views. A nil views proves the API-only posture: no HTML page mounts while the
// JSON API stays.
func newHTMLTestHandler(t *testing.T, views Views) http.Handler {
	t.Helper()
	users := newMemUsers()
	router, err := delivery.NewRouter(delivery.Deps{Mailer: nopMailer{}, MailFrom: "noreply@example.com"})
	if err != nil {
		t.Fatalf("delivery.NewRouter: %v", err)
	}
	svc := authsvc.NewService(authsvc.Deps{
		Users:             users,
		Identifiers:       newMemIdentifiers(users),
		Passwords:         &memPasswords{m: map[string]string{}},
		Sessions:          &memSessions{m: map[string]session.Session{}},
		Challenges:        &memChallenges{byID: map[string]challenge.Challenge{}},
		Protector:         memProtector{},
		Hasher:            fakeHasher{},
		Deliver:           router,
		Queue:             stubQueue{},
		Limiter:           ratelimiter.NewMemory(),
		Cookie:            authsvc.CookieConfig{},
		TokenSigner:       newFakeSigner(),
		OAuthAccounts:     &memOAuthAccounts{},
		OAuthStates:       &memOAuthStates{m: map[string]oauthstate.State{}},
		Providers:         []oauth.Provider{stubProvider{}},
		OAuthCallbackBase: "https://app.example.com",
		Passwordless:      []string{"email"},
		PublicAuthBaseURL: "https://auth.example.com",
	})
	h := web.NewWebHandler()
	Mount(h, svc, nil, nil, "", MutationSecurity{}, views)
	return h
}

// htmlPublicPages are the unauthenticated HTML GET pages: they render straight
// through the port with no session, so with Views wired each returns 200 and the
// port's marker body.
var htmlPublicPages = []struct{ path, marker string }{
	{"/auth/login", "login"},
	{"/auth/register", "register"},
	{"/auth/verify", "verify"},
	{"/auth/password/forgot", "forgot"},
	{"/auth/password/reset", "reset"},
	{"/auth/passwordless", "passwordless_start"},
	{"/auth/passwordless/code", "passwordless_code"},
	{"/auth/passwordless/check", "check"},
	{"/auth/magic", "magic"},
}

// htmlGatedPages are the account-security HTML GET pages: they ride
// RequireLiveSession, so without a session they are MOUNTED but denied (never 404).
var htmlGatedPages = []string{
	"/auth/account",
	"/auth/identifiers/new",
	"/auth/identifiers/id1/edit",
	"/auth/password/set",
	"/auth/password/change",
	"/auth/password/remove",
	"/auth/step-up",
	"/auth/oauth/github/unlink",
}

// TestViewsHTMLMountRendersPublicPages proves the public HTML GET inventory mounts
// when Views is non-nil and each handler renders through the port (the marker body).
func TestViewsHTMLMountRendersPublicPages(t *testing.T) {
	h := newHTMLTestHandler(t, stubViews{})
	for _, p := range htmlPublicPages {
		rec := do(t, h, "GET", p.path, "")
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s status = %d, want 200", p.path, rec.Code)
			continue
		}
		if got := rec.Body.String(); !strings.Contains(got, "PAGE:"+p.marker) {
			t.Errorf("GET %s body = %q, want it to render through the port (PAGE:%s)", p.path, got, p.marker)
		}
	}
}

// TestViewsHTMLMountGatesAccountPages proves the account-security HTML pages mount
// when Views is non-nil (they are live-session gated, so without a session they are
// denied — but NEVER 404, which would mean unregistered).
func TestViewsHTMLMountGatesAccountPages(t *testing.T) {
	h := newHTMLTestHandler(t, stubViews{})
	for _, path := range htmlGatedPages {
		rec := do(t, h, "GET", path, "")
		if rec.Code == http.StatusNotFound {
			t.Errorf("GET %s = 404, want the route mounted (a live-session denial, not absence)", path)
		}
	}
}

// TestAPIMountViewsNilHTMLAbsent proves the API-only posture: with a nil Views the
// entire HTML GET inventory is absent while the JSON API stays mounted. A path with
// no JSON twin yields 404; a path whose JSON POST twin is still registered yields 405
// (method not allowed) — both prove the HTML GET handler is unregistered, and
// crucially neither renders a page (never 200).
func TestAPIMountViewsNilHTMLAbsent(t *testing.T) {
	h := newHTMLTestHandler(t, nil)
	assertHTMLAbsent := func(path string) {
		t.Helper()
		rec := do(t, h, "GET", path, "")
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s with nil Views = %d, want 404/405 (HTML GET absent)", path, rec.Code)
		}
	}
	for _, p := range htmlPublicPages {
		assertHTMLAbsent(p.path)
	}
	for _, path := range htmlGatedPages {
		assertHTMLAbsent(path)
	}
}

// TestAPIMountJSONSurvivesNilViews proves the JSON API routes remain registered and
// functional when Views is nil (the HTML gate never touches the JSON contract).
func TestAPIMountJSONSurvivesNilViews(t *testing.T) {
	h := newHTMLTestHandler(t, nil)
	// A representative JSON POST endpoint: with a nil Views it must still be mounted
	// (any non-404 status proves the route exists; bad/absent content yields 4xx).
	for _, path := range []string{"/auth/login", "/auth/register", "/auth/password/forgot"} {
		rec := do(t, h, "POST", path, `{}`)
		if rec.Code == http.StatusNotFound {
			t.Errorf("POST %s with nil Views = 404, want the JSON route still mounted", path)
		}
	}
}
