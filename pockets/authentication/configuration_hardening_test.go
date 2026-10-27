package authentication

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets"
	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/contactchange"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
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

// configActivitySessions is a sessions store carrying the idle-timeout
// capability; only ProbeSchema runs at construction.
type configActivitySessions struct {
	configSessions
	probeErr error
}

func (configActivitySessions) GetByRefreshHashWithActivity(context.Context, string) (session.Session, session.RefreshMatch, session.Activity, error) {
	return session.Session{}, 0, session.Activity{}, sdk.ErrNotFound
}
func (configActivitySessions) RotateAt(context.Context, string, string, string, time.Time) error {
	return session.ErrRotationConflict
}
func (configActivitySessions) DeleteIfIdle(context.Context, string, session.Activity, int) (bool, error) {
	return false, nil
}
func (s configActivitySessions) ProbeSchema(context.Context) error { return s.probeErr }

// TestSessionLifetimeRefusals pins the root sentinels for the session-lifetime
// refusals, in every mode where the plan says every mode: an incoherent
// access/refresh pair, an idle timeout at or under the access lifetime, an idle
// timeout on a store without the capability, and the probe mapping (only
// sdk.ErrNotFound is schema-missing; anything else is sdk.ErrUnavailable).
func TestSessionLifetimeRefusals(t *testing.T) {
	build := func(sessions session.SessionRepository, sessionsCfg SessionsConfig) error {
		cfg := noPasswordConfig()
		cfg.AccessTokenTTL, cfg.RefreshTTL, cfg.IdleTimeout = sessionsCfg.AccessTokenTTL, sessionsCfg.RefreshTTL, sessionsCfg.IdleTimeout
		_, err := newFixture(testRepositories(Repositories{Sessions: sessions}), cfg)
		return err
	}
	wired := configActivitySessions{}

	if err := build(wired, SessionsConfig{AccessTokenTTL: 2 * time.Hour, RefreshTTL: time.Hour}); !errors.Is(err, ErrAccessTokenTTLExceedsRefreshTTL) || !errors.Is(err, sdk.ErrInvalidInput) || !strings.Contains(err.Error(), "RefreshTTL") {
		t.Errorf("AccessTokenTTL > RefreshTTL = %v, want ErrAccessTokenTTLExceedsRefreshTTL naming RefreshTTL", err)
	}
	for _, idle := range []time.Duration{-time.Minute, time.Minute, 15 * time.Minute} {
		if err := build(wired, SessionsConfig{IdleTimeout: idle}); !errors.Is(err, ErrIdleTimeoutBelowAccessTokenTTL) || !strings.Contains(err.Error(), "AccessTokenTTL") {
			t.Errorf("IdleTimeout %s under the default access lifetime = %v, want ErrIdleTimeoutBelowAccessTokenTTL", idle, err)
		}
	}
	if err := build(configSessions{}, SessionsConfig{IdleTimeout: time.Hour}); !errors.Is(err, ErrIdleTimeoutRequires) {
		t.Errorf("IdleTimeout on a store without session.ActivityRepository = %v, want ErrIdleTimeoutRequires", err)
	}
	if err := build(configSessions{}, SessionsConfig{}); err != nil {
		t.Errorf("unwired on a store without the capability = %v, want nil", err)
	}
	if err := build(wired, SessionsConfig{IdleTimeout: time.Hour}); err != nil {
		t.Errorf("wired on a migrated store = %v, want nil", err)
	}

	missing := configActivitySessions{probeErr: sdk.ErrNotFound}
	if err := build(missing, SessionsConfig{IdleTimeout: time.Hour}); !errors.Is(err, ErrIdleTimeoutSchemaMissing) || errors.Is(err, sdk.ErrUnavailable) || !strings.Contains(err.Error(), "0029_session_activity.sql") {
		t.Errorf("wired on an unmigrated store = %v, want ErrIdleTimeoutSchemaMissing naming 0029", err)
	}
	if err := build(missing, SessionsConfig{}); err != nil {
		t.Errorf("unwired on an unmigrated store = %v, want nil (never probed)", err)
	}
	for _, probeErr := range []error{errors.New("permission denied for table sessions"), context.DeadlineExceeded} {
		err := build(configActivitySessions{probeErr: probeErr}, SessionsConfig{IdleTimeout: time.Hour})
		if !errors.Is(err, sdk.ErrUnavailable) || errors.Is(err, ErrIdleTimeoutSchemaMissing) {
			t.Errorf("probe failure %v = %v, want sdk.ErrUnavailable, not schema-missing", probeErr, err)
		}
	}
}
