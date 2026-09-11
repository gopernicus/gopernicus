package authentication

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets"
	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/contactchange"
	delivery "github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
	environment "github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

func noPasswordConfig() constructorConfig {
	return constructorConfig{
		PasswordFlowsDisabled: true,
		TokenSigner:           stubSigner{},
		RuntimeMode:           environment.ModeDevelopment,
		DeliveryMode:          delivery.ModeOff,
	}
}

func TestConstructionRejectsMissingCoreRepositories(t *testing.T) {
	for _, name := range []string{"Users", "Identifiers", "Sessions", "ActiveSessions", "Passwords"} {
		t.Run(name, func(t *testing.T) {
			repos, cfg := testRepositories(Repositories{}), noPasswordConfig()
			switch name {
			case "Users":
				repos.Users = nil
			case "Identifiers":
				repos.Identifiers = nil
			case "Sessions":
				repos.Sessions = nil
			case "ActiveSessions":
				repos.ActiveSessions = nil
			case "Passwords":
				repos.Passwords = nil
				cfg.PasswordFlowsDisabled = false
				cfg.Hasher = stubHasher{}
			}
			_, err := newFixture(repos, cfg)
			if !errors.Is(err, sdk.ErrInvalidInput) || !strings.Contains(err.Error(), name) {
				t.Fatalf("missing %s: %v", name, err)
			}
		})
	}
}

func TestDisabledFeaturesOmitTheirDependencies(t *testing.T) {
	repos := testRepositories(Repositories{})
	repos.Passwords = nil
	svc, err := newFixture(repos, noPasswordConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Authentication.Login(context.Background(), "user@example.com", "password"); !errors.Is(err, authlogic.ErrPasswordFlowsDisabled) {
		t.Fatalf("disabled login: %v", err)
	}
	if err := svc.HTTP.Register(pockets.Mount{}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("nil mount router: %v", err)
	}
}

func TestPasswordDeliveryRequiresCompleteRail(t *testing.T) {
	cfg := jobsModeConfig(&recordingDispatcher{})
	cfg.PasswordFlowsDisabled = false
	_, err := newFixture(testRepositories(Repositories{}), cfg)
	if !errors.Is(err, sdk.ErrInvalidInput) || !strings.Contains(err.Error(), "password delivery") {
		t.Fatalf("partial password delivery accepted: %v", err)
	}
}

func TestProductionRequiresSecureSessionCookies(t *testing.T) {
	cfg := prodLimiterConfig()
	cfg.RateLimiter = durableLimiter{}
	cfg.SessionCookie.Secure = false
	if _, err := newFixture(testRepositories(Repositories{}), cfg); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("insecure production cookie accepted: %v", err)
	}
	cfg.SessionCookie.Secure = true
	if _, err := newFixture(testRepositories(Repositories{}), cfg); err != nil {
		t.Fatal(err)
	}
}

type configContactChanges struct{ contactchange.Repository }

func TestConstructionRejectsPartialContactChangeRail(t *testing.T) {
	repos := testRepositories(Repositories{})
	repos.ContactChanges = configContactChanges{}
	_, err := newFixture(repos, noPasswordConfig())
	if !errors.Is(err, sdk.ErrInvalidInput) || !strings.Contains(err.Error(), "ContactChanges") {
		t.Fatalf("partial contact rail accepted: %v", err)
	}
}

func TestConstructionRejectsTypedNilDependencies(t *testing.T) {
	repos := testRepositories(Repositories{})
	repos.Users = (*configUsers)(nil)
	if _, err := newFixture(repos, noPasswordConfig()); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("typed nil user store accepted: %v", err)
	}
	repos = testRepositories(Repositories{})
	cfg := noPasswordConfig()
	cfg.TokenSigner = (*stubSigner)(nil)
	if _, err := newFixture(repos, cfg); !errors.Is(err, ErrTokenSignerRequired) {
		t.Fatalf("typed nil signer accepted: %v", err)
	}
	repos.ActiveSessions = (*configActiveSessions)(nil)
	if _, err := newFixture(repos, noPasswordConfig()); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("typed nil active sessions accepted: %v", err)
	}
}
