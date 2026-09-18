package authenticationhttp

import (
	"net/http"
	"net/url"
	"slices"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauthstate"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk/capabilities/oauth"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

type loginPostureViews struct {
	stubViews
	page  LoginPage
	calls int
}

func (v *loginPostureViews) Login(page LoginPage) web.Renderer {
	v.page = page
	v.calls++
	return v.stubViews.Login(page)
}

func newLoginPostureService(disabled, passwordless, provider bool) *testService {
	users := newMemUsers()
	d := authenticationFixture{
		Users:                 users,
		Identifiers:           newMemIdentifiers(users),
		Passwords:             &memPasswords{m: map[string]string{}},
		Sessions:              &memSessions{m: map[string]session.Session{}},
		PasswordFlowsDisabled: disabled,
		PublicAuthBaseURL:     "https://auth.example.com",
	}
	if passwordless {
		d.Passwordless = []string{"email"}
	}
	if provider {
		d.OAuthAccounts = &memOAuthAccounts{}
		d.OAuthStates = &memOAuthStates{m: map[string]oauthstate.State{}}
		d.Providers = []oauth.Provider{stubProvider{}}
		d.OAuthCallbackBase = "https://auth.example.com"
	}
	return newServiceWithFakes(d)
}

func TestLoginPagePasswordPosture(t *testing.T) {
	for _, tc := range []struct {
		name                             string
		disabled, passwordless, provider bool
	}{
		{"default-password", false, false, false},
		{"all-methods", false, true, true},
		{"oauth-only", true, false, true},
		{"passwordless-only", true, true, false},
		{"passwordless-and-oauth", true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newLoginPostureService(tc.disabled, tc.passwordless, tc.provider)
			views := &loginPostureViews{}
			router := web.NewWebHandler()
			mount(router, mountDeps{Auth: svc, Views: views})
			rec := do(t, router, http.MethodGet, "/auth/login?email=person%40example.com&return_to=%2Fdashboard&auth=signed_out", "")
			if rec.Code != http.StatusOK || views.calls != 1 {
				t.Fatalf("login page status=%d render calls=%d", rec.Code, views.calls)
			}
			page := views.page
			if page.PasswordFlowsDisabled != tc.disabled || page.PasswordlessEnabled != tc.passwordless {
				t.Fatalf("password disabled=%v passwordless enabled=%v, want %v/%v", page.PasswordFlowsDisabled, page.PasswordlessEnabled, tc.disabled, tc.passwordless)
			}
			var providers []string
			if tc.provider {
				providers = []string{"google"}
			}
			if !slices.Equal(page.OAuthProviders, providers) {
				t.Fatalf("providers=%v, want %v", page.OAuthProviders, providers)
			}
			if page.Email != "person@example.com" || page.ReturnTo != "/dashboard" || page.Message != loginOutcomes[outcomeSignedOut] {
				t.Fatalf("login page lost entered address, return target or notice: %+v", page)
			}
			if page.CSRFToken == "" || page.CSPNonce == "" {
				t.Fatal("login page lost security context")
			}
		})
	}
}

func TestLoginPagePasswordPostureKeepsDisabledRoutesAbsent(t *testing.T) {
	svc := newLoginPostureService(true, true, true)
	views := &loginPostureViews{}
	router := web.NewWebHandler()
	mount(router, mountDeps{Auth: svc, Views: views})
	for _, path := range []string{
		"/auth/register", "/auth/verify", "/auth/password/forgot", "/auth/password/reset",
		"/auth/password/set", "/auth/password/change", "/auth/password/remove",
	} {
		rec := do(t, router, http.MethodGet, path, "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("disabled GET %s status=%d, want 404", path, rec.Code)
		}
	}
	for _, path := range []string{"/auth/login", "/auth/register", "/auth/password/forgot", "/auth/password/reset"} {
		rec := doFormReq(t, router, path, url.Values{"email": {"person@example.com"}, "password": {"not-a-password"}})
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("disabled POST %s status=%d, want absent route", path, rec.Code)
		}
	}
	if views.calls != 0 {
		t.Fatal("a disabled password endpoint rendered login")
	}
	if rec := do(t, router, http.MethodGet, "/auth/passwordless", ""); rec.Code != http.StatusOK {
		t.Fatalf("passwordless login disappeared: status=%d", rec.Code)
	}
	if rec := do(t, router, http.MethodGet, "/auth/oauth/google/start", ""); rec.Code != http.StatusFound {
		t.Fatalf("OAuth login disappeared: status=%d", rec.Code)
	}
}

func TestLoginPagePasswordPostureOnFailedForm(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		for _, target := range []struct{ requested, want string }{
			{"/dashboard", "/dashboard"},
			{"https://untrusted.example.com", ""},
		} {
			svc := newLoginPostureService(disabled, true, true)
			views := &loginPostureViews{}
			h := &handlers{svc: svc, views: views}
			// The disabled posture normally has no POST route. Drive its handler
			// directly to pin the failure model independently of route mounting.
			rec := doFormReq(t, http.HandlerFunc(h.loginForm), "/auth/login", url.Values{
				"email": {"missing@example.com"}, "password": {"not-a-password"}, "return_to": {target.requested},
			})
			if rec.Code < 400 || views.calls != 1 || sessionCookie(rec) != nil || refreshCookie(rec) != nil {
				t.Fatalf("disabled=%v: failed login status=%d render calls=%d", disabled, rec.Code, views.calls)
			}
			page := views.page
			if page.PasswordFlowsDisabled != disabled || !page.PasswordlessEnabled || !slices.Equal(page.OAuthProviders, []string{"google"}) {
				t.Fatalf("disabled=%v: failed form lost configured sign-in methods: %+v", disabled, page)
			}
			if page.Email != "missing@example.com" || page.ReturnTo != target.want || page.Message != loginErrMsg {
				t.Fatalf("disabled=%v: failed form lost entered address, safe target or generic error: %+v", disabled, page)
			}
			if page.CSRFToken == "" || page.CSPNonce == "" {
				t.Fatal("failed form lost security context")
			}
		}
	}
}
