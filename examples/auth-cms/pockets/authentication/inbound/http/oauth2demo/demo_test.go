package oauth2demo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	authhttp "github.com/gopernicus/gopernicus/pockets/authentication/inbound/http"
	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
)

type emptyIdentity struct{}

func (emptyIdentity) CurrentCredential(context.Context) (authlogic.Credential, bool) {
	return authlogic.Credential{}, false
}
func (emptyIdentity) CurrentPrincipal(context.Context) (authlogic.Principal, bool) {
	return authlogic.Principal{}, false
}

func testDemo(t *testing.T) *Demo {
	t.Helper()
	demo, err := New(Config{BaseURL: "http://localhost:8082", ClientID: "http://localhost:8082/oauth-demo/client.json", ConfidentialClientID: "mcp", ConfidentialSecret: "local test secret"}, &authhttp.Adapter{}, emptyIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	return demo
}

func TestDemoStateAndCSRFBelongToSeparateBrowserSlot(t *testing.T) {
	demo := testDemo(t)
	start := httptest.NewRecorder()
	demo.start(start, httptest.NewRequest(http.MethodGet, "/oauth-demo/start?slot=a", nil))
	if start.Code != http.StatusSeeOther {
		t.Fatalf("start: %d", start.Code)
	}
	cookies := start.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "oauth_demo_a" || !cookies[0].HttpOnly || cookies[0].Path != "/oauth-demo" {
		t.Fatalf("connection cookie: %+v", cookies)
	}
	location, err := url.Parse(start.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if location.Query().Get("code_challenge_method") != "S256" || location.Query().Get("resource") != demo.config.BaseURL+"/oauth-demo/mcp" {
		t.Fatalf("authorization request: %v", location.Query())
	}
	id := cookies[0].Value
	conn := demo.connections[id]
	if conn.CSRF == id || conn.State == "" || conn.Verifier == "" {
		t.Fatal("CSRF and connection credential must be distinct")
	}
	conn.Tokens = oauth2.TokenResponse{AccessToken: "server-only-access", RefreshToken: "server-only-refresh"}
	demo.connections[id] = conn
	home := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/oauth-demo", nil)
	request.AddCookie(cookies[0])
	demo.home(home, request)
	for _, secret := range []string{id, conn.State, conn.Verifier, conn.Tokens.AccessToken, conn.Tokens.RefreshToken} {
		if strings.Contains(home.Body.String(), secret) {
			t.Error("credential material rendered in demo page")
		}
	}
	for _, tc := range []struct{ slot, csrf, origin string }{{"b", conn.CSRF, ""}, {"a", "bad", ""}, {"a", conn.CSRF, "https://foreign.example"}} {
		form := url.Values{"slot": {tc.slot}, "csrf": {tc.csrf}}
		r := httptest.NewRequest(http.MethodPost, "/oauth-demo/disconnect", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", tc.origin)
		r.AddCookie(cookies[0])
		w := httptest.NewRecorder()
		if _, _, ok := demo.requestConnection(w, r); ok {
			t.Fatalf("foreign slot/CSRF/origin accepted: %+v", tc)
		}
	}
	callback := httptest.NewRequest(http.MethodGet, "/oauth-demo/callback?code=never&state=wrong", nil)
	callback.AddCookie(cookies[0])
	w := httptest.NewRecorder()
	demo.callback(w, callback)
	if w.Code != http.StatusBadRequest || demo.connections[id].State != conn.State {
		t.Fatalf("unmatched callback consumed pending authorization: %d", w.Code)
	}
}

func TestDemoMCPNeverAcceptsWebCookieAsBearer(t *testing.T) {
	demo := testDemo(t)
	r := httptest.NewRequest(http.MethodGet, "/oauth-demo/mcp", nil)
	r.AddCookie(&http.Cookie{Name: "access_token", Value: "web-access-token"})
	w := httptest.NewRecorder()
	demo.mcp(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("MCP admitted a browser cookie: %d", w.Code)
	}
}
