package authentication

import (
	"context"
	"errors"
	"testing"

	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/externalsubject"
	"github.com/gopernicus/gopernicus/sdk"
	sdkauth "github.com/gopernicus/gopernicus/sdk/capabilities/authentication"
)

type cookieTestProvider struct{}

func (cookieTestProvider) Issuer() string { return "https://idp.example.test" }
func (cookieTestProvider) Authenticate(context.Context, sdkauth.Credential) (sdkauth.Identity, error) {
	return sdkauth.Identity{}, sdkauth.ErrInvalidCredential
}

type cookieTestSubjects struct{}

func (cookieTestSubjects) Get(context.Context, string, string) (externalsubject.ExternalSubject, error) {
	return externalsubject.ExternalSubject{}, sdk.ErrNotFound
}
func (cookieTestSubjects) Link(_ context.Context, s externalsubject.ExternalSubject) (externalsubject.ExternalSubject, error) {
	return s, nil
}
func (cookieTestSubjects) ListByUser(context.Context, string) ([]externalsubject.ExternalSubject, error) {
	return nil, nil
}
func (cookieTestSubjects) Unlink(context.Context, string, string) error { return sdk.ErrNotFound }

// TestExternalCookieNamesReachTheAdapter proves New forwards each provider's
// CookieName to the HTTP adapter, whose collision check then runs.
func TestExternalCookieNamesReachTheAdapter(t *testing.T) {
	build := func(cookie string) error {
		cfg := browserBaseConfig()
		_, err := New(testRepositories(Repositories{ExternalSubjects: cookieTestSubjects{}}), cfg.TokenSigner, cfg.RuntimeMode, cfg.DeliveryMode,
			WithPassword(PasswordConfig{Hasher: cfg.Hasher}),
			WithDelivery(DeliveryConfig{Mailer: cfg.Mailer}),
			WithExternal(authlogic.ExternalConfig{Providers: []authlogic.ExternalProvider{{Name: "idp", Provider: cookieTestProvider{}, CookieName: cookie}}}))
		return err
	}
	if err := build("__session"); err != nil {
		t.Fatalf("distinct provider cookie: %v", err)
	}
	if err := build("session"); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("provider cookie named like the native session cookie = %v, want ErrInvalidInput", err)
	}
}
