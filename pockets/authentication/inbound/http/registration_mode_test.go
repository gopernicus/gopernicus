package authenticationhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauthstate"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk/capabilities/oauth"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// pendingInvitations is an invitation resolver whose lookup admits the listed
// addresses: the registration mode's only question.
type pendingInvitations struct {
	mu      sync.Mutex
	invited map[string]bool
}

func (p *pendingInvitations) ResolveInvitations(context.Context, string, string, string, string) (int, error) {
	return 0, nil
}

func (p *pendingInvitations) HasPendingInvitation(_ context.Context, _, value string) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.invited[value], nil
}

// verifiedProvider is the stub provider asserting a verified email, so an
// unknown identity reaches the register branch.
type verifiedProvider struct{ stubProvider }

func (verifiedProvider) GetUserInfo(context.Context, string) (*oauth.UserInfo, error) {
	return &oauth.UserInfo{ProviderUserID: "g-new", Email: "newcomer@example.com", EmailVerified: true}, nil
}

// registrationViews records the login and register pages it renders.
type registrationViews struct {
	stubViews
	login    LoginPage
	register RegisterPage
}

func (v *registrationViews) Login(page LoginPage) web.Renderer {
	v.login = page
	return v.stubViews.Login(page)
}

func (v *registrationViews) Register(page RegisterPage) web.Renderer {
	v.register = page
	return v.stubViews.Register(page)
}

func newRegistrationHandler(t *testing.T, mode authlogic.RegistrationMode, invitations *pendingInvitations) (http.Handler, *registrationViews) {
	t.Helper()
	users := newMemUsers()
	d := authenticationFixture{
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
		Registration:            mode,
		Limiter:                 ratelimiter.NewMemory(),
	}
	if invitations != nil {
		d.Invitations = invitations
	}
	svc := newServiceWithFakes(d)
	views := &registrationViews{}
	h := web.NewWebHandler()
	mount(h, mountDeps{Auth: svc, Views: views, Registration: svc.RegistrationMode()})
	return h, views
}

func TestRegistrationClosedUnmountsRegistration(t *testing.T) {
	h, views := newRegistrationHandler(t, authlogic.RegistrationClosed, nil)
	if rec := doJSONReq(t, h, "/auth/register", `{"email":"x@example.com","password":"password123456789","display_name":"X"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("POST /auth/register under closed = %d, want 404", rec.Code)
	}
	if rec := do(t, h, "GET", "/auth/register", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("GET /auth/register under closed = %d, want 404", rec.Code)
	}
	if rec := doJSONReq(t, h, "/auth/verify", `{"email":"x@example.com","code":"000000"}`); rec.Code == http.StatusNotFound {
		t.Fatal("POST /auth/verify under closed = 404, want it mounted for pending accounts")
	}
	if rec := do(t, h, "GET", "/auth/login", ""); rec.Code != http.StatusOK || !views.login.RegistrationClosed {
		t.Fatalf("login page = %d RegistrationClosed=%v, want the link hidden", rec.Code, views.login.RegistrationClosed)
	}
	if rec := doFormReq(t, h, "/auth/login", url.Values{"email": {"x@example.com"}, "password": {"wrong-password-123"}}); rec.Code != http.StatusUnauthorized || !views.login.RegistrationClosed {
		t.Fatalf("login form re-render = %d RegistrationClosed=%v, want the link hidden there too", rec.Code, views.login.RegistrationClosed)
	}

	open, openViews := newRegistrationHandler(t, authlogic.RegistrationOpen, nil)
	if rec := do(t, open, "GET", "/auth/login", ""); rec.Code != http.StatusOK || openViews.login.RegistrationClosed {
		t.Fatalf("open login page RegistrationClosed=%v, want false", openViews.login.RegistrationClosed)
	}
}

func TestRegistrationInvitationOnly(t *testing.T) {
	invites := &pendingInvitations{invited: map[string]bool{"invited@example.com": true}}
	h, views := newRegistrationHandler(t, authlogic.RegistrationInvitationOnly, invites)

	rec := doJSONReq(t, h, "/auth/register", `{"email":"stranger@example.com","password":"password123456789","display_name":"S"}`)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"code":"registration_invitation_required"`) {
		t.Fatalf("uninvited register = %d %s, want 403 registration_invitation_required", rec.Code, rec.Body)
	}
	if rec := doJSONReq(t, h, "/auth/register", `{"email":"invited@example.com","password":"password123456789","display_name":"I"}`); rec.Code != http.StatusCreated {
		t.Fatalf("invited register = %d %s, want 201", rec.Code, rec.Body)
	}
	// The invitation is decided first: an existing account without an
	// invitation gets the 403, never the 409.
	invites.mu.Lock()
	invites.invited["invited@example.com"] = false
	invites.mu.Unlock()
	if rec := doJSONReq(t, h, "/auth/register", `{"email":"invited@example.com","password":"password123456789","display_name":"I"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("existing account, no invitation = %d, want 403 not 409", rec.Code)
	}

	form := doFormReq(t, h, "/auth/register", url.Values{"email": {"stranger@example.com"}, "password": {"password123456789"}, "display_name": {"S"}})
	if form.Code != http.StatusForbidden || views.register.Message != registerInvitationErrMsg || views.register.Email != "stranger@example.com" {
		t.Fatalf("form register = %d %+v, want the invitation copy with the email kept", form.Code, views.register)
	}
	if rec := do(t, h, "GET", "/auth/login", ""); rec.Code != http.StatusOK || views.login.RegistrationClosed {
		t.Fatal("invitation_only hid the registration link an invitee needs")
	}
}

// TestOAuthRegistrationRefusal pins both OAuth lanes: the browser callback
// clears the flow cookie and lands on the validated destination with
// ?auth=<code>; the native complete answers the coded 403.
func TestOAuthRegistrationRefusal(t *testing.T) {
	for _, tc := range []struct {
		mode authlogic.RegistrationMode
		code string
	}{
		{authlogic.RegistrationClosed, "registration_closed"},
		{authlogic.RegistrationInvitationOnly, "registration_invitation_required"},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			h, _ := newRegistrationHandler(t, tc.mode, &pendingInvitations{invited: map[string]bool{}})
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
			if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/welcome?auth="+tc.code {
				t.Fatalf("callback = %d %q, want 302 /welcome?auth=%s", rec.Code, rec.Header().Get("Location"), tc.code)
			}
			cleared := false
			for _, c := range (&http.Response{Header: rec.Header()}).Cookies() {
				if c.Name == flow.Name && c.MaxAge < 0 {
					cleared = true
				}
				if c.Name == "session" {
					t.Fatal("a refused registration set a session cookie")
				}
			}
			if !cleared {
				t.Fatal("the flow cookie was not cleared")
			}

			native := doJSONReq(t, h, "/auth/oauth/google/native/start", `{"redirect_uri":"com.example.app:/callback"}`)
			var nflow struct {
				State      string `json:"state"`
				FlowSecret string `json:"flow_secret"`
			}
			_ = json.Unmarshal(native.Body.Bytes(), &nflow)
			done := doJSONReq(t, h, "/auth/oauth/google/native/complete", `{"code":"c","state":"`+nflow.State+`","flow_secret":"`+nflow.FlowSecret+`"}`)
			if done.Code != http.StatusForbidden || !strings.Contains(done.Body.String(), `"code":"`+tc.code+`"`) {
				t.Fatalf("native complete = %d %s, want 403 %s", done.Code, done.Body, tc.code)
			}
		})
	}
}
