package authenticationhttp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauthstate"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk/capabilities/oauth"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// registeredRoute is one registration a recordingRegistrar captured.
type registeredRoute struct {
	method, path string
	middleware   []web.Middleware
}

// recordingRegistrar captures every registration instead of serving it.
type recordingRegistrar struct{ routes []registeredRoute }

func (r *recordingRegistrar) Handle(method, path string, _ http.HandlerFunc, middleware ...web.Middleware) {
	r.routes = append(r.routes, registeredRoute{method: method, path: path, middleware: middleware})
}

// TestEveryBundledRouteIsNoStore runs every registered route's middleware chain
// in front of a handler that writes no Cache-Control: whichever gate answers,
// the response carries Cache-Control: no-store.
func TestEveryBundledRouteIsNoStore(t *testing.T) {
	users := newMemUsers()
	svc := newServiceWithFakes(authenticationFixture{
		Users:                   users,
		Identifiers:             newMemIdentifiers(users),
		Passwords:               &memPasswords{m: map[string]string{}},
		Sessions:                &memSessions{m: map[string]session.Session{}},
		Hasher:                  fakeHasher{},
		Limiter:                 ratelimiter.NewMemory(),
		OAuthAccounts:           &memOAuthAccounts{},
		OAuthStates:             &memOAuthStates{m: map[string]oauthstate.State{}},
		Providers:               []oauth.Provider{stubProvider{}},
		OAuthCallbackBase:       "https://app.example.com",
		OAuthNativeRedirectURIs: []string{"com.example.app:/oauth"},
		TokenSigner:             newFakeSigner(),
	})
	rec := &recordingRegistrar{}
	mount(rec, mountDeps{Auth: svc, Views: stubViews{}})
	if len(rec.routes) < 40 {
		t.Fatalf("recorded %d routes; the fixture should mount the password, OAuth and HTML surfaces", len(rec.routes))
	}
	bare := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	for _, route := range rec.routes {
		var final http.Handler = bare
		for i := len(route.middleware) - 1; i >= 0; i-- {
			final = route.middleware[i](final)
		}
		w := httptest.NewRecorder()
		final.ServeHTTP(w, httptest.NewRequest(route.method, strings.ReplaceAll(route.path, "{", "x"), nil))
		if got := w.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s %s answered %d with Cache-Control %q, want no-store", route.method, route.path, w.Code, got)
		}
	}
}

// TestNoStoreOnTheWire proves the header on real responses: the JSON 401s of
// login and token, the HTML login page, and a gate's 401.
func TestNoStoreOnTheWire(t *testing.T) {
	users := newMemUsers()
	svc := newServiceWithFakes(authenticationFixture{Users: users, Identifiers: newMemIdentifiers(users), Hasher: fakeHasher{}, Limiter: ratelimiter.NewMemory(), TokenSigner: newFakeSigner()})
	h := web.NewWebHandler()
	mount(h, mountDeps{Auth: svc, Views: stubViews{}})
	for _, tc := range []struct {
		method, path, body string
		want               int
	}{
		{http.MethodPost, "/auth/login", `{}`, http.StatusUnauthorized},
		{http.MethodPost, "/auth/token", `{}`, http.StatusUnauthorized},
		{http.MethodGet, "/auth/login", "", http.StatusOK},
		{http.MethodGet, "/auth/me", "", http.StatusUnauthorized},
	} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		if tc.body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want || w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s %s = %d Cache-Control %q, want %d no-store", tc.method, tc.path, w.Code, w.Header().Get("Cache-Control"), tc.want)
		}
	}
}
