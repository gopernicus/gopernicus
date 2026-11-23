package authenticationhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauthstate"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/oauth"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// newRefusingHandler mounts an open-registration surface whose BeforeSession
// hook refuses every sign-in with err.
func newRefusingHandler(t *testing.T, err error) (http.Handler, *registrationViews) {
	t.Helper()
	users := newMemUsers()
	svc := newServiceWithFakes(authenticationFixture{
		Users:                   users,
		Identifiers:             newMemIdentifiers(users),
		Passwords:               &memPasswords{m: map[string]string{}},
		Sessions:                &memSessions{m: map[string]session.Session{}},
		OAuthAccounts:           &memOAuthAccounts{},
		OAuthStates:             &memOAuthStates{m: map[string]oauthstate.State{}},
		Providers:               []oauth.Provider{verifiedProvider{}},
		OAuthCallbackBase:       "https://app.example.com",
		OAuthNativeRedirectURIs: []string{"com.example.app:/callback"},
		TrustOAuthEmail:         func(string, oauth.UserInfo) bool { return true },
		Registration:            authlogic.RegistrationOpen,
		Limiter:                 ratelimiter.NewMemory(),
		BeforeSession:           func(context.Context, authlogic.SignIn) error { return err },
	})
	views := &registrationViews{}
	h := web.NewWebHandler()
	mount(h, mountDeps{Auth: svc, Views: views, Registration: svc.RegistrationMode()})
	return h, views
}

// TestBeforeSessionRefusalOverHTTP pins how a refused sign-in surfaces on each
// lane: the browser OAuth callback lands on ?auth=sign_in_refused with no
// session cookie, the native and JSON lanes answer the hook's status, and the
// HTML login form re-renders with the refusal copy, never the hook's text.
func TestBeforeSessionRefusalOverHTTP(t *testing.T) {
	refusal := fmt.Errorf("hook-private-detail: %w", sdk.ErrForbidden)

	t.Run("browser oauth", func(t *testing.T) {
		h, _ := newRefusingHandler(t, refusal)
		start := do(t, h, "GET", "/auth/oauth/google/start?redirect=/welcome", "")
		loc, _ := url.Parse(start.Header().Get("Location"))
		state := loc.Query().Get("state")
		var flow *http.Cookie
		for _, c := range (&http.Response{Header: start.Header()}).Cookies() {
			if strings.Contains(c.Name, "oauth_") {
				flow = c
			}
		}
		if state == "" || flow == nil {
			t.Fatalf("start = %d %q, want a state and a flow cookie", start.Code, start.Header().Get("Location"))
		}
		rec := do(t, h, "GET", "/auth/oauth/google/callback?state="+state+"&code=c", "", flow)
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/welcome?auth="+authlogic.SignInRefusedCode {
			t.Fatalf("callback = %d %q, want 302 /welcome?auth=sign_in_refused", rec.Code, rec.Header().Get("Location"))
		}
		cleared := false
		for _, c := range (&http.Response{Header: rec.Header()}).Cookies() {
			if c.Name == flow.Name && c.MaxAge < 0 {
				cleared = true
			}
			if c.MaxAge >= 0 && c.Value != "" && !strings.Contains(c.Name, "oauth_") {
				t.Fatalf("a refused sign-in set cookie %q", c.Name)
			}
		}
		if !cleared {
			t.Fatal("the flow cookie was not cleared")
		}
	})

	t.Run("native oauth", func(t *testing.T) {
		h, _ := newRefusingHandler(t, refusal)
		native := doJSONReq(t, h, "/auth/oauth/google/native/start", `{"redirect_uri":"com.example.app:/callback"}`)
		var nflow struct {
			State      string `json:"state"`
			FlowSecret string `json:"flow_secret"`
		}
		_ = json.Unmarshal(native.Body.Bytes(), &nflow)
		done := doJSONReq(t, h, "/auth/oauth/google/native/complete", `{"code":"c","state":"`+nflow.State+`","flow_secret":"`+nflow.FlowSecret+`"}`)
		if done.Code != http.StatusForbidden || strings.Contains(done.Body.String(), "hook-private-detail") {
			t.Fatalf("native complete = %d %s, want 403 without the hook's text", done.Code, done.Body)
		}
	})

	t.Run("json and html login", func(t *testing.T) {
		h, views := newRefusingHandler(t, fmt.Errorf("hook-private-detail: %w", sdk.ErrConflict))
		if rec := doJSONReq(t, h, "/auth/register", `{"email":"staff@example.com","password":"password123456789","display_name":"S"}`); rec.Code != http.StatusCreated {
			t.Fatalf("register = %d %s", rec.Code, rec.Body)
		}
		rec := doJSONReq(t, h, "/auth/login", `{"email":"staff@example.com","password":"password123456789"}`)
		if rec.Code != http.StatusConflict || strings.Contains(rec.Body.String(), "hook-private-detail") || rec.Header().Get("Set-Cookie") != "" {
			t.Fatalf("JSON login = %d %s cookies=%q, want 409, no hook text, no cookie", rec.Code, rec.Body, rec.Header().Values("Set-Cookie"))
		}
		form := doFormReq(t, h, "/auth/login", url.Values{"email": {"staff@example.com"}, "password": {"password123456789"}})
		if form.Code != http.StatusConflict || views.login.Message != signInRefusedErrMsg {
			t.Fatalf("form login = %d message=%q, want 409 with the refusal copy", form.Code, views.login.Message)
		}
	})
}
