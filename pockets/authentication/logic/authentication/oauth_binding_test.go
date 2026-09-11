package authentication

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/oauth"
)

func TestFlowProofModeAndProviderBinding(t *testing.T) {
	p := &fakeProvider{name: "google", trust: true, providerUserID: "bound", email: "bound@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil)
	h.svc.providers["github"] = &fakeProvider{name: "github"}
	flow, err := h.svc.StartOAuth(context.Background(), "google", OAuthStartRequest{Mode: OAuthBrowser})
	if err != nil {
		t.Fatal(err)
	}
	if flow.State == flow.FlowSecret || strings.Contains(flow.AuthorizationURL, flow.FlowSecret) {
		t.Fatal("completion proof exposed")
	}
	req := OAuthCallbackRequest{Mode: OAuthBrowser, State: flow.State, FlowSecret: flow.FlowSecret, Code: "code"}
	for _, kind := range []string{"missing proof", "wrong proof", "wrong mode", "wrong provider", "noncanonical state"} {
		bad := req
		provider := "google"
		switch kind {
		case "missing proof":
			bad.FlowSecret = ""
		case "wrong proof":
			bad.FlowSecret = newFlowSecret()
		case "wrong mode":
			bad.Mode = OAuthNative
		case "wrong provider":
			provider = "github"
		case "noncanonical state":
			bad.State += "="
		}
		if _, err := h.svc.OAuthCallback(context.Background(), provider, bad); !errors.Is(err, sdk.ErrNotFound) {
			t.Fatalf("%s accepted: %v", kind, err)
		}
	}
	if res, err := h.svc.OAuthCallback(context.Background(), "google", req); err != nil || res.Action != ActionRegister {
		t.Fatalf("valid proof after invalid attempts: %+v %v", res, err)
	}
	if _, err := h.svc.OAuthCallback(context.Background(), "google", req); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("replay accepted: %v", err)
	}
}

func TestNativeRedirectAndExplicitMode(t *testing.T) {
	p := &fakeProvider{name: "google", trust: true, providerUserID: "native", email: "native@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil, func(d *constructorConfig) { d.OAuthNativeRedirectURIs = []string{"com.example.app:/oauth"} })
	for _, req := range []OAuthStartRequest{{}, {Mode: OAuthNative}, {Mode: OAuthNative, RedirectURI: "com.attacker:/oauth"}, {Mode: OAuthBrowser, RedirectURI: "com.example.app:/oauth"}} {
		if _, err := h.svc.StartOAuth(context.Background(), "google", req); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("invalid start accepted: %+v %v", req, err)
		}
	}
	flow, err := h.svc.StartOAuth(context.Background(), "google", OAuthStartRequest{Mode: OAuthNative, RedirectURI: "com.example.app:/oauth"})
	if err != nil {
		t.Fatal(err)
	}
	req := OAuthCallbackRequest{Mode: OAuthNative, State: flow.State, FlowSecret: flow.FlowSecret, Code: "code"}
	if res, err := h.svc.OAuthCallback(context.Background(), "google", req); err != nil || res.Token == "" || res.RefreshToken == "" {
		t.Fatalf("native completion: %+v %v", res, err)
	}
	if p.exchangedRedirect != "com.example.app:/oauth" {
		t.Fatalf("exchange used %q", p.exchangedRedirect)
	}
}

type malformedProvider struct {
	*fakeProvider
	token *oauth.TokenResponse
	info  *oauth.UserInfo
}

func (p malformedProvider) ExchangeCode(context.Context, string, string, string) (*oauth.TokenResponse, error) {
	return p.token, nil
}
func (p malformedProvider) GetUserInfo(context.Context, string) (*oauth.UserInfo, error) {
	return p.info, nil
}

func TestMalformedProviderCannotCreateAccounts(t *testing.T) {
	for _, test := range []struct {
		name  string
		token *oauth.TokenResponse
		info  *oauth.UserInfo
	}{
		{name: "nil token"},
		{name: "empty token", token: &oauth.TokenResponse{}},
		{name: "nil identity", token: &oauth.TokenResponse{AccessToken: "token", TokenType: "Bearer"}},
		{name: "blank ID", token: &oauth.TokenResponse{AccessToken: "token", TokenType: "Bearer"}, info: &oauth.UserInfo{Email: "user@example.com", EmailVerified: true}},
		{name: "unexpected ID token", token: &oauth.TokenResponse{AccessToken: "token", TokenType: "Bearer", IDToken: "unexpected"}, info: &oauth.UserInfo{ProviderUserID: "id", Email: "user@example.com", EmailVerified: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := &fakeProvider{name: "google", trust: true}
			h := newOAuthHarness(t, p, nil)
			h.svc.providers["google"] = malformedProvider{p, test.token, test.info}
			state := h.startState(t, "")
			if _, err := h.svc.OAuthCallback(context.Background(), "google", h.callbackRequest(state)); err == nil {
				t.Fatal("malformed response accepted")
			}
			if len(h.accounts.m) != 0 || len(h.users.byID) != 0 {
				t.Fatal("malformed identity wrote an account")
			}
		})
	}
}

func TestOIDCDoesNotDowngrade(t *testing.T) {
	p := &fakeProvider{name: "google", oidc: true, trust: true, providerUserID: "id", email: "user@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil)
	state := h.startState(t, "")
	p.oidc = false // the original OIDC wrapper remains; exchange now omits its ID token.
	if _, err := h.svc.OAuthCallback(context.Background(), "google", h.callbackRequest(state)); err == nil {
		t.Fatal("missing ID token downgraded to userinfo")
	}
	if len(h.users.byID) != 0 {
		t.Fatal("OIDC omission created a user")
	}
}

func TestHostEmailTrustIsOptInAndLinkedLoginNeedsNoEmail(t *testing.T) {
	p := &fakeProvider{name: "google", trust: true, providerUserID: "id", email: "user@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil, func(d *constructorConfig) { d.TrustOAuthEmail = nil })
	state := h.startState(t, "")
	if _, err := h.svc.OAuthCallback(context.Background(), "google", h.callbackRequest(state)); !errors.Is(err, ErrProviderEmailUnverified) {
		t.Fatalf("implicit email trust: %v", err)
	}
	h.svc.trustOAuthEmail = func(string, oauth.UserInfo) bool { return true }
	state = h.startState(t, "")
	if _, err := h.svc.OAuthCallback(context.Background(), "google", h.callbackRequest(state)); err != nil {
		t.Fatal(err)
	}
	h.svc.trustOAuthEmail = nil
	p.email = ""
	p.emailVerified = false
	state = h.startState(t, "")
	if res, err := h.svc.OAuthCallback(context.Background(), "google", h.callbackRequest(state)); err != nil || res.Action != ActionLogin {
		t.Fatalf("linked login: %+v %v", res, err)
	}
}
