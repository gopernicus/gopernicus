package authentication

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets"
	inbound "github.com/gopernicus/gopernicus/pockets/authentication/inbound/http"
	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	delivery "github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/maintenance"
	"github.com/gopernicus/gopernicus/sdk"
	environment "github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// stubMaintenance is a wired retention port; construction never calls it.
type stubMaintenance struct{ maintenance.Repository }

// stubErasure is a wired erasure port; construction never calls it.
type stubErasure struct{ user.EraseRepository }

// stubUserAdmin is a wired administration port; construction never calls it.
type stubUserAdmin struct{ user.AdminRepository }

// stubSink is a wired archive sink; construction never calls it.
type stubSink struct{}

func (stubSink) Upload(context.Context, string, io.Reader) error { return nil }
func (stubSink) Exists(context.Context, string) (bool, error)    { return false, nil }
func (stubSink) Download(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("unused")
}
func (stubSink) GetObjectSize(context.Context, string) (int64, error) { return 0, nil }

const maintenanceWarnText = "maintenance_runner=unacknowledged: retention is not enforced"

// maintenanceWiring builds a production-valid pocket with the posture
// acknowledgements and the given retention input.
func maintenanceWiring(mode environment.Mode, repos Repositories, log *slog.Logger, extra ...Option) (*Components, error) {
	repos.SecurityEvents = stubSecurityEvents{}
	opts := []Option{
		WithPassword(PasswordConfig{PasswordFlowsDisabled: true}),
		WithIdentity(IdentityConfig{IdentifierKeyer: prodKeyer{}}),
		WithAbuseProtection(AbuseProtectionConfig{RateLimiter: durableLimiter{}}),
		WithDelivery(DeliveryConfig{Mailer: prodMailer{}}),
		WithBrowser(BrowserConfig{SessionCookie: inbound.CookieConfig{Secure: true}}),
		WithLogger(log),
	}
	return New(testRepositories(repos), stubSigner{}, mode, delivery.ModeOff, append(opts, extra...)...)
}

func TestMaintenanceAcknowledgementWithoutRepositoryIsRefusedInEveryMode(t *testing.T) {
	for _, mode := range []environment.Mode{environment.ModeDevelopment, environment.ModeProduction} {
		log, _ := bufferLog()
		_, err := maintenanceWiring(mode, Repositories{}, log, WithMaintenance(MaintenanceConfig{MaintenanceRunnerAcknowledged: true}))
		if !errors.Is(err, ErrMaintenanceAcknowledgedWithoutRepository) {
			t.Errorf("%s: err = %v, want ErrMaintenanceAcknowledgedWithoutRepository", mode, err)
		}
		if err != nil && !strings.Contains(err.Error(), "own SQL cron") {
			t.Errorf("%s: the refusal does not name the own-cron posture: %v", mode, err)
		}
	}
}

func TestMaintenanceRunnerWarnsOnceInProductionUnacknowledged(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode environment.Mode
		ack  bool
		want int
	}{
		{"production unacknowledged", environment.ModeProduction, false, 1},
		{"production acknowledged", environment.ModeProduction, true, 0},
		{"development unacknowledged", environment.ModeDevelopment, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log, buf := bufferLog()
			comps, err := maintenanceWiring(tc.mode, Repositories{Maintenance: stubMaintenance{}}, log,
				WithMaintenance(MaintenanceConfig{MaintenanceRunnerAcknowledged: tc.ack}))
			if err != nil {
				t.Fatal(err)
			}
			if comps.Maintenance == nil {
				t.Fatal("Components.Maintenance is nil")
			}
			if n := strings.Count(buf.String(), maintenanceWarnText); n != tc.want {
				t.Errorf("maintenance WARNs = %d, want %d:\n%s", n, tc.want, buf.String())
			}
		})
	}
	// A host with no retention port at all still learns it in production.
	log, buf := bufferLog()
	comps, err := maintenanceWiring(environment.ModeProduction, Repositories{}, log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(buf.String(), maintenanceWarnText) != 1 || comps.Maintenance == nil {
		t.Errorf("no-port production: comps.Maintenance=%v logs:\n%s", comps.Maintenance, buf.String())
	}
}

func TestMaintenanceRegistrationLine(t *testing.T) {
	log, buf := bufferLog()
	if _, err := maintenanceWiring(environment.ModeDevelopment, Repositories{Maintenance: stubMaintenance{}}, log); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"maintenance_runner=false", "archive_sink=none", "security_event_retention=9600h0m0s"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("registration line lacks %s:\n%s", want, buf.String())
		}
	}
	if strings.Contains(buf.String(), "level=WARN msg=\"auth: maintenance") {
		t.Errorf("no sink in development warned:\n%s", buf.String())
	}

	log, buf = bufferLog()
	_, err := maintenanceWiring(environment.ModeProduction, Repositories{Maintenance: stubMaintenance{}}, log,
		WithMaintenance(MaintenanceConfig{Config: maintenance.Config{ArchiveSink: stubSink{}, ArchiveLocation: "disk:./archive", SecurityEventRetention: 500 * 24 * time.Hour}, MaintenanceRunnerAcknowledged: true}))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"maintenance_runner=true", "archive_sink=disk:./archive", "security_event_retention=12000h0m0s"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("registration line lacks %s:\n%s", want, buf.String())
		}
	}
}

func TestMaintenancePolicyIsValidatedAtConstruction(t *testing.T) {
	log, _ := bufferLog()
	_, err := maintenanceWiring(environment.ModeDevelopment, Repositories{Maintenance: stubMaintenance{}}, log,
		WithMaintenance(MaintenanceConfig{Config: maintenance.Config{SecurityEventRetention: 30 * 24 * time.Hour}}))
	if !errors.Is(err, maintenance.ErrSecurityEventRetentionTooShort) {
		t.Fatalf("err = %v, want ErrSecurityEventRetentionTooShort", err)
	}
}

func TestUserErasureConstructionInvariants(t *testing.T) {
	log, _ := bufferLog()
	allow := func(context.Context, inbound.UserAdminCheckRequest) error { return nil }

	repos := testRepositories(Repositories{UserErasure: stubErasure{}, SecurityEvents: stubSecurityEvents{}})
	repos.ActiveSessions = nil
	if _, err := New(repos, stubSigner{}, environment.ModeDevelopment, delivery.ModeOff,
		WithPassword(PasswordConfig{PasswordFlowsDisabled: true}), WithDelivery(DeliveryConfig{Mailer: prodMailer{}}), WithLogger(log)); !errors.Is(err, ErrUserErasureRequiresActiveSessions) {
		t.Errorf("erasure without the fenced mint: err = %v", err)
	}
	if _, err := maintenanceWiring(environment.ModeDevelopment, Repositories{UserAdmin: stubUserAdmin{}}, log,
		WithAdministration(AdministrationConfig{UserAdminCheck: allow, UserErasureRoute: true})); !errors.Is(err, ErrUserErasureRouteRequires) {
		t.Errorf("route without the erasure port: err = %v", err)
	}
	if _, err := maintenanceWiring(environment.ModeDevelopment, Repositories{UserErasure: stubErasure{}}, log,
		WithAdministration(AdministrationConfig{UserErasureRoute: true})); !errors.Is(err, ErrUserErasureRouteRequires) {
		t.Errorf("route without UserAdminCheck: err = %v", err)
	}
}

func TestUserErasureRouteMountsOnlyOnTheOptIn(t *testing.T) {
	allow := func(context.Context, inbound.UserAdminCheckRequest) error { return nil }
	for _, optIn := range []bool{false, true} {
		log, _ := bufferLog()
		comps, err := maintenanceWiring(environment.ModeDevelopment, Repositories{UserAdmin: stubUserAdmin{}, UserErasure: stubErasure{}}, log,
			WithAdministration(AdministrationConfig{UserAdminCheck: allow, UserErasureRoute: optIn}))
		if err != nil {
			t.Fatalf("opt-in=%v: %v", optIn, err)
		}
		if !comps.Authentication.UserErasureEnabled() {
			t.Fatal("the erasure port did not reach the service")
		}
		h := web.NewWebHandler()
		if err := comps.HTTP.Register(pockets.Mount{Router: h}); err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/auth/admin/users/u1/erase", strings.NewReader(`{}`)))
		mounted := rec.Code != http.StatusNotFound
		if mounted != optIn {
			t.Errorf("opt-in=%v: POST erase = %d (mounted=%v)", optIn, rec.Code, mounted)
		}
	}
}

// storeReachedErasure is an erasure port that reports being reached.
type storeReachedErasure struct{ user.EraseRepository }

func (storeReachedErasure) Erase(context.Context, user.EraseInput) (user.Erasure, error) {
	return user.Erasure{}, errors.New("the erasure store was reached")
}

// TestBeforeEraseWiring: a hook without the erasure port is contradictory
// wiring; with it, the hook reaches EraseUser.
func TestBeforeEraseWiring(t *testing.T) {
	log, _ := bufferLog()
	var calls []string
	hook := func(_ context.Context, actor sdk.Principal, id string) error {
		calls = append(calls, actor.ID+">"+id)
		return fmt.Errorf("refused by the test hook: %w", sdk.ErrConflict)
	}
	if _, err := maintenanceWiring(environment.ModeDevelopment, Repositories{UserAdmin: stubUserAdmin{}}, log,
		WithAdministration(AdministrationConfig{BeforeErase: hook})); !errors.Is(err, ErrBeforeEraseWithoutErasure) {
		t.Fatalf("hook without the erasure port: err = %v, want ErrBeforeEraseWithoutErasure", err)
	}
	comps, err := maintenanceWiring(environment.ModeDevelopment, Repositories{UserAdmin: stubUserAdmin{}, UserErasure: storeReachedErasure{}}, log,
		WithAdministration(AdministrationConfig{BeforeErase: hook}))
	if err != nil {
		t.Fatalf("hook with the erasure port: %v", err)
	}
	_, _, err = comps.Authentication.EraseUser(context.Background(), sdk.Principal{Type: sdk.PrincipalTypeUser, ID: "admin"}, "target")
	if !errors.Is(err, authlogic.ErrEraseRefusedByHook) || !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("EraseUser = %v, want the hook's refusal", err)
	}
	if len(calls) != 1 || calls[0] != "admin>target" {
		t.Fatalf("hook calls = %v", calls)
	}
}

func TestMaintenanceAcknowledgementHasNoEnvTag(t *testing.T) {
	for _, key := range []string{
		"AUTH_MAINTENANCE_RUNNER_ACKNOWLEDGED", "MAINTENANCE_RUNNER_ACKNOWLEDGED", "MaintenanceRunnerAcknowledged",
		"AUTH_USER_ERASURE_ROUTE", "USER_ERASURE_ROUTE", "UserErasureRoute",
	} {
		t.Setenv(key, "true")
	}
	for _, namespace := range []string{"", "AUTH"} {
		var mcfg MaintenanceConfig
		var admin AdministrationConfig
		var cfg constructorConfig
		for _, target := range []any{&mcfg, &admin, &cfg} {
			if err := environment.ParseEnvTags(namespace, target); err != nil {
				t.Fatalf("ParseEnvTags(%q, %T): %v", namespace, target, err)
			}
		}
		if mcfg.MaintenanceRunnerAcknowledged || cfg.MaintenanceRunnerAcknowledged {
			t.Errorf("ParseEnvTags(%q) filled MaintenanceRunnerAcknowledged", namespace)
		}
		if admin.UserErasureRoute || cfg.UserErasureRoute {
			t.Errorf("ParseEnvTags(%q) filled UserErasureRoute", namespace)
		}
	}
}
