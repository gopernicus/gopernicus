package authentication

import (
	"testing"

	"github.com/gopernicus/gopernicus/sdk/capabilities/oauth"
	environment "github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

type namedOAuthProvider struct {
	stubProvider
	name string
}

func (p *namedOAuthProvider) Name() string { return p.name }

func TestOAuthConfigValidation(t *testing.T) {
	cfg := constructorConfig{RuntimeMode: environment.ModeProduction, Providers: []oauth.Provider{stubProvider{}}, OAuthCallbackBase: "https://app.example/prefix"}
	if err := validateOAuthConfig(cfg); err != nil {
		t.Fatal(err)
	}
	for _, base := range []string{"", "http://app.example", "https://app.example/", "https://app.example/prefix/", "https://app.example/path?x=1", "https://app.example/#", "https://user:secret@app.example", "https://app.example/a/../b"} {
		bad := cfg
		bad.OAuthCallbackBase = base
		if err := validateOAuthConfig(bad); err == nil {
			t.Fatalf("invalid callback base accepted: %q", base)
		}
	}
	var typedNil *namedOAuthProvider
	for _, providers := range [][]oauth.Provider{{nil}, {typedNil}, {stubProvider{}, stubProvider{}}, {&namedOAuthProvider{name: "bad/provider"}}} {
		bad := cfg
		bad.Providers = providers
		if err := validateOAuthConfig(bad); err == nil {
			t.Fatal("invalid providers accepted")
		}
	}
	for _, uri := range []string{"com.example.app:/oauth", "https://app.example/mobile/callback", "http://127.0.0.1:54321/oauth", "http://[::1]:54321/oauth"} {
		good := cfg
		good.OAuthNativeRedirectURIs = []string{uri}
		if err := validateOAuthConfig(good); err != nil {
			t.Fatalf("registered native URI %q: %v", uri, err)
		}
	}
	for _, uri := range []string{"", "/relative", "https:/missing", "http://foreign.example/callback", "http://localhost:54321/oauth", "com.example:/oauth#secret"} {
		bad := cfg
		bad.OAuthNativeRedirectURIs = []string{uri}
		if err := validateOAuthConfig(bad); err == nil {
			t.Fatalf("invalid native URI accepted: %q", uri)
		}
	}
}
