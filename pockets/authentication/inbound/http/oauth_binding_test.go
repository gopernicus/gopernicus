package authenticationhttp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauthstate"
)

func TestBrowserFlowRequiresItsOwnCookie(t *testing.T) {
	f := newPendingLinkFixture(t, stubViews{})
	first := do(t, f.h, "GET", "/auth/oauth/google/start", "")
	second := do(t, f.h, "GET", "/auth/oauth/google/start", "")
	firstURL, _ := url.Parse(first.Header().Get("Location"))
	secondURL, _ := url.Parse(second.Header().Get("Location"))
	firstCookies, secondCookies := first.Result().Cookies(), second.Result().Cookies()
	if len(firstCookies) != 1 || len(secondCookies) != 1 {
		t.Fatal("start must set one proof cookie")
	}
	a, b := firstCookies[0], secondCookies[0]
	if a.Name == b.Name || a.Value == b.Value {
		t.Fatal("concurrent flows share proof")
	}
	if !a.HttpOnly || !a.Secure || a.Domain != "" || a.Path != "/" || a.SameSite != http.SameSiteLaxMode || !strings.HasPrefix(a.Name, "__Host-") {
		t.Fatalf("unsafe proof cookie: %+v", a)
	}
	if strings.Contains(first.Header().Get("Location"), a.Value) {
		t.Fatal("proof leaked into URL")
	}
	callback := "/auth/oauth/google/callback?code=x&state=" + firstURL.Query().Get("state")
	for _, cookies := range [][]*http.Cookie{nil, secondCookies} {
		denied := do(t, f.h, "GET", callback, "", cookies...)
		if denied.Code != 404 || len(denied.Result().Cookies()) != 0 {
			t.Fatalf("foreign browser redeemed state: %d", denied.Code)
		}
	}
	for _, flow := range []struct {
		state  string
		cookie *http.Cookie
	}{{firstURL.Query().Get("state"), a}, {secondURL.Query().Get("state"), b}} {
		response := do(t, f.h, "GET", "/auth/oauth/google/callback?code=x&state="+flow.state, "", flow.cookie)
		if response.Code != 302 {
			t.Fatalf("original flow could not complete: %d %s", response.Code, response.Body)
		}
		cleared := response.Result().Cookies()
		if len(cleared) != 1 || cleared[0].Name != flow.cookie.Name || cleared[0].MaxAge != -1 {
			t.Fatal("completed flow cookie not cleared")
		}
	}
}

// Owned HTTP server, real JSON requests, no cookie jar and no real provider or
// mail traffic. The fixture's same-email identity exercises pending linking.
func TestNativeHTTPPendingLinkHasNoCookies(t *testing.T) {
	f := newPendingLinkFixture(t, stubViews{})
	server := httptest.NewServer(f.h)
	defer server.Close()
	post := func(path string, body any, want int) []byte {
		t.Helper()
		encoded, _ := json.Marshal(body)
		response, err := server.Client().Post(server.URL+path, "application/json", strings.NewReader(string(encoded)))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != want {
			t.Fatalf("%s: status=%d body=%s", path, response.StatusCode, data)
		}
		if len(response.Cookies()) != 0 || response.Header.Get("Location") != "" {
			t.Fatal("native response used cookies/redirect")
		}
		if !strings.Contains(response.Header.Get("Cache-Control"), "no-store") {
			t.Fatal("native response can be cached")
		}
		return data
	}
	var flow authlogic.OAuthStart
	data := post("/auth/oauth/google/native/start", map[string]string{"redirect_uri": "com.example.app:/oauth"}, 200)
	if err := json.Unmarshal(data, &flow); err != nil {
		t.Fatal(err)
	}
	if flow.State == "" || flow.FlowSecret == "" || strings.Contains(flow.AuthorizationURL, flow.FlowSecret) {
		t.Fatal("invalid native start")
	}
	complete := map[string]string{"code": "code", "state": flow.State, "flow_secret": flow.FlowSecret}
	var pending oauthNativeResponse
	if err := json.Unmarshal(post("/auth/oauth/google/native/complete", complete, 200), &pending); err != nil {
		t.Fatal(err)
	}
	if pending.Action != authlogic.ActionPendingLink || pending.AccessToken != "" {
		t.Fatalf("pending result: %+v", pending)
	}
	post("/auth/oauth/google/native/complete", complete, 404)
	var token string
	f.states.mu.Lock()
	for key, state := range f.states.m {
		if state.Purpose == oauthstate.PurposePendingLink {
			token = key
		}
	}
	f.states.mu.Unlock()
	if token == "" {
		t.Fatal("pending proof not issued")
	}
	var linked oauthNativeResponse
	if err := json.Unmarshal(post("/auth/oauth/native/verify-link", map[string]string{"token": token}, 200), &linked); err != nil {
		t.Fatal(err)
	}
	if linked.Action != authlogic.ActionLinked || linked.AccessToken == "" || linked.RefreshToken == "" || linked.TokenType != "Bearer" {
		t.Fatalf("native credentials not returned: %+v", linked)
	}
	post("/auth/oauth/native/verify-link", map[string]string{"token": token}, 404)
}
