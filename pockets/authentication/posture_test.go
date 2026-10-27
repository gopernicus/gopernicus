package authentication

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	inbound "github.com/gopernicus/gopernicus/pockets/authentication/inbound/http"
	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	delivery "github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	sdkauth "github.com/gopernicus/gopernicus/sdk/capabilities/authentication"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify/email"
	"github.com/gopernicus/gopernicus/sdk/capabilities/oauth"
	environment "github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// starttlsMailer declares opportunistic STARTTLS, the transport production
// refuses without DeliveryConfig.OpportunisticTLSAcknowledged.
type starttlsMailer struct{}

func (starttlsMailer) Send(context.Context, email.Message) error { return nil }
func (starttlsMailer) Capabilities() notify.Capabilities {
	return notify.Capabilities{TransportSecurity: notify.TransportSecurityStartTLS}
}

// stubSecurityEvents is a wired audit rail that records nothing.
type stubSecurityEvents struct{}

func (stubSecurityEvents) Create(_ context.Context, e securityevent.SecurityEvent) (securityevent.SecurityEvent, error) {
	return e, nil
}
func (stubSecurityEvents) List(context.Context, securityevent.ListFilter, list.Request) (list.Page[securityevent.SecurityEvent], error) {
	return list.Page[securityevent.SecurityEvent]{}, nil
}

// devOnlyOAuthProvider declares itself development-only, like a fake provider.
type devOnlyOAuthProvider struct{ stubProvider }

func (devOnlyOAuthProvider) Capabilities() oauth.Capabilities {
	return oauth.Capabilities{DevelopmentOnly: true}
}

// devOnlyExternalProvider declares itself development-only, like a local issuer.
type devOnlyExternalProvider struct{ cookieTestProvider }

func (devOnlyExternalProvider) Capabilities() sdkauth.Capabilities {
	return sdkauth.Capabilities{DevelopmentOnly: true}
}

// postureWiring is a production-valid wiring except for the three acknowledgeable
// conditions: password flows are on, the mailer declares STARTTLS, and
// SecurityEvents is wired only when audit is true.
type postureWiring struct {
	audit, singleFactor, opportunisticTLS, auditAcknowledged bool
	mailer                                                   email.Sender
}

func (w postureWiring) build(mode environment.Mode, log *slog.Logger, extra ...Option) (*Components, error) {
	repos := Repositories{}
	if w.audit {
		repos.SecurityEvents = stubSecurityEvents{}
	}
	mailer := w.mailer
	if mailer == nil {
		mailer = starttlsMailer{}
	}
	opts := []Option{
		WithPassword(PasswordConfig{Hasher: stubHasher{}}),
		WithIdentity(IdentityConfig{IdentifierKeyer: prodKeyer{}, SingleFactorAcknowledged: w.singleFactor}),
		WithAbuseProtection(AbuseProtectionConfig{RateLimiter: durableLimiter{}}),
		WithDelivery(DeliveryConfig{Mailer: mailer, OpportunisticTLSAcknowledged: w.opportunisticTLS}),
		WithBrowser(BrowserConfig{SessionCookie: inbound.CookieConfig{Secure: true}}),
		WithLogger(log),
	}
	if w.auditAcknowledged {
		opts = append(opts, WithAuditDisabledAcknowledged())
	}
	return New(testRepositories(repos), stubSigner{}, mode, delivery.ModeOff, append(opts, extra...)...)
}

func bufferLog() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})), &buf
}

// TestProductionPostureJoinsEveryViolation proves a host repinning onto the
// posture check sees its whole list in one boot, each entry naming the
// acknowledgement that waives it.
func TestProductionPostureJoinsEveryViolation(t *testing.T) {
	log, _ := bufferLog()
	_, err := postureWiring{}.build(environment.ModeProduction, log)
	for _, want := range []error{ErrAuditRequired, ErrSingleFactorUnacknowledged, ErrInsecureDeliveryTransport, notify.ErrOpportunisticTLS, notify.ErrInsecureTransport} {
		if !errors.Is(err, want) {
			t.Errorf("joined posture error does not match %v: %v", want, err)
		}
	}
	if err == nil {
		t.Fatal("production accepted three unacknowledged posture violations")
	}
	for _, field := range []string{"WithAuditDisabledAcknowledged()", "IdentityConfig.SingleFactorAcknowledged", "DeliveryConfig.OpportunisticTLSAcknowledged"} {
		if !strings.Contains(err.Error(), field) {
			t.Errorf("posture error does not name %s: %v", field, err)
		}
	}
}

// TestProductionPostureEachViolationAlone isolates every refusal, so no sentinel
// passes only because another condition was present.
func TestProductionPostureEachViolationAlone(t *testing.T) {
	cases := []struct {
		name   string
		wiring postureWiring
		want   error
	}{
		{"nil_security_events", postureWiring{singleFactor: true, opportunisticTLS: true}, ErrAuditRequired},
		{"single_factor", postureWiring{audit: true, opportunisticTLS: true}, ErrSingleFactorUnacknowledged},
		{"starttls_mailer", postureWiring{audit: true, singleFactor: true}, ErrInsecureDeliveryTransport},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			log, _ := bufferLog()
			_, err := tc.wiring.build(environment.ModeProduction, log)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			for _, other := range []error{ErrAuditRequired, ErrSingleFactorUnacknowledged, ErrInsecureDeliveryTransport} {
				if other != tc.want && errors.Is(err, other) {
					t.Errorf("err also matches %v: %v", other, err)
				}
			}
		})
	}
}

// TestProductionPostureAcknowledgedConstructsAndWarns proves every
// acknowledgement admits its condition end to end — the STARTTLS one through
// both the root check and the delivery router's ValidateTransports — and logs
// one posture_waiver WARN naming it.
func TestProductionPostureAcknowledgedConstructsAndWarns(t *testing.T) {
	log, buf := bufferLog()
	if _, err := (postureWiring{singleFactor: true, opportunisticTLS: true, auditAcknowledged: true}).build(environment.ModeProduction, log); err != nil {
		t.Fatalf("acknowledged production posture: %v", err)
	}
	for _, flag := range []string{"AuditDisabledAcknowledged", "SingleFactorAcknowledged", "OpportunisticTLSAcknowledged"} {
		if n := strings.Count(buf.String(), "posture_waiver="+flag); n != 1 {
			t.Errorf("posture_waiver=%s logged %d times, want 1:\n%s", flag, n, buf.String())
		}
	}
}

// TestStartTLSRefusedAtRootWithoutAcknowledgement proves the refusal comes from
// the root check (its error carries the root's transport label), not only from
// the logic constructor's ValidateTransports.
func TestStartTLSRefusedAtRootWithoutAcknowledgement(t *testing.T) {
	log, _ := bufferLog()
	_, err := (postureWiring{audit: true, singleFactor: true}).build(environment.ModeProduction, log)
	if !errors.Is(err, ErrInsecureDeliveryTransport) || !errors.Is(err, notify.ErrOpportunisticTLS) {
		t.Fatalf("err = %v, want ErrInsecureDeliveryTransport wrapping notify.ErrOpportunisticTLS", err)
	}
	if !strings.Contains(err.Error(), "email sender declares opportunistic STARTTLS") {
		t.Errorf("refusal is not the root's labelled error: %v", err)
	}
}

// TestDevelopmentPostureWarnsOnEachCondition proves development constructs and
// warns once per condition production would refuse.
func TestDevelopmentPostureWarnsOnEachCondition(t *testing.T) {
	log, buf := bufferLog()
	if _, err := (postureWiring{}).build(environment.ModeDevelopment, log); err != nil {
		t.Fatalf("development construction: %v", err)
	}
	for _, want := range []string{"WithAuditDisabledAcknowledged()", "IdentityConfig.SingleFactorAcknowledged", "DeliveryConfig.OpportunisticTLSAcknowledged"} {
		if n := strings.Count(buf.String(), want); n != 1 {
			t.Errorf("development WARN naming %s logged %d times, want 1:\n%s", want, n, buf.String())
		}
	}
	if strings.Contains(buf.String(), "posture_waiver") {
		t.Errorf("development logged a waiver with no acknowledgement:\n%s", buf.String())
	}
}

// TestAuditAcknowledgementBesideSecurityEventsIsContradictory proves the
// wiring-fixed waiver is refused beside the rail in every mode.
func TestAuditAcknowledgementBesideSecurityEventsIsContradictory(t *testing.T) {
	for _, mode := range []environment.Mode{environment.ModeDevelopment, environment.ModeProduction} {
		log, _ := bufferLog()
		_, err := (postureWiring{audit: true, auditAcknowledged: true, singleFactor: true, opportunisticTLS: true}).build(mode, log)
		if !errors.Is(err, ErrAuditAcknowledgedWithSecurityEvents) {
			t.Errorf("%s: err = %v, want ErrAuditAcknowledgedWithSecurityEvents", mode, err)
		}
	}
}

// TestEnvironmentVaryingAcknowledgementsAreInert proves the single-factor and
// STARTTLS acknowledgements neither fail nor log when nothing is waived, so one
// binary can run where the condition is absent.
func TestEnvironmentVaryingAcknowledgementsAreInert(t *testing.T) {
	log, buf := bufferLog()
	_, err := New(testRepositories(Repositories{SecurityEvents: stubSecurityEvents{}}), stubSigner{}, environment.ModeProduction, delivery.ModeOff,
		WithPassword(PasswordConfig{PasswordFlowsDisabled: true}),
		WithIdentity(IdentityConfig{IdentifierKeyer: prodKeyer{}, SingleFactorAcknowledged: true}),
		WithAbuseProtection(AbuseProtectionConfig{RateLimiter: durableLimiter{}}),
		WithDelivery(DeliveryConfig{Mailer: prodMailer{}, OpportunisticTLSAcknowledged: true}),
		WithBrowser(BrowserConfig{SessionCookie: inbound.CookieConfig{Secure: true}}),
		WithLogger(log))
	if err != nil {
		t.Fatalf("inert acknowledgements: %v", err)
	}
	if strings.Contains(buf.String(), "posture_waiver") {
		t.Errorf("an inert acknowledgement logged a waiver:\n%s", buf.String())
	}
}

// TestPasswordlessAloneIsSingleFactor proves ruling 1: a magic link or OTP is
// single-factor too, so password flows off is not enough.
func TestPasswordlessAloneIsSingleFactor(t *testing.T) {
	log, _ := bufferLog()
	_, err := New(testRepositories(passwordlessDevRepos()), stubSigner{}, environment.ModeProduction, delivery.ModeJobs,
		WithPassword(PasswordConfig{PasswordFlowsDisabled: true}),
		WithIdentity(IdentityConfig{ChallengeProtector: passwordlessProtector(t), IdentifierKeyer: prodKeyer{}}),
		WithAbuseProtection(AbuseProtectionConfig{RateLimiter: durableLimiter{}}),
		WithDelivery(DeliveryConfig{Mailer: prodMailer{}, DeliveryEncrypter: stubEncrypter{}, DeliveryDispatcher: stubDispatcher{}, DeliveryJobsAcknowledged: true}),
		WithPasswordless(PasswordlessConfig{Passwordless: []string{"email"}}),
		WithLinks(LinksConfig{PublicAuthBaseURL: "https://auth.example.com"}),
		WithBrowser(BrowserConfig{SessionCookie: inbound.CookieConfig{Secure: true}}),
		WithAuditDisabledAcknowledged(),
		WithLogger(log))
	if !errors.Is(err, ErrSingleFactorUnacknowledged) || !strings.Contains(err.Error(), "causes [passwordless]") {
		t.Fatalf("passwordless-only production: err = %v, want ErrSingleFactorUnacknowledged naming the passwordless cause", err)
	}
}

// TestDevelopmentOnlyOAuthProvider proves production refuses a provider that
// declares itself development-only, development warns, and an undeclared
// provider is accepted.
func TestDevelopmentOnlyOAuthProvider(t *testing.T) {
	repos := testRepositories(Repositories{OAuthAccounts: stubOAuthAccounts{}, OAuthStates: stubOAuthStates{}, SecurityEvents: stubSecurityEvents{}})
	build := func(mode environment.Mode, provider oauth.Provider, log *slog.Logger) error {
		_, err := New(repos, stubSigner{}, mode, delivery.ModeOff,
			WithPassword(PasswordConfig{PasswordFlowsDisabled: true}),
			WithIdentity(IdentityConfig{IdentifierKeyer: prodKeyer{}, SingleFactorAcknowledged: true}),
			WithAbuseProtection(AbuseProtectionConfig{RateLimiter: durableLimiter{}}),
			WithDelivery(DeliveryConfig{Mailer: prodMailer{}}),
			WithOAuth(OAuthConfig{Providers: []oauth.Provider{provider}, OAuthCallbackBase: "https://app.example.com"}),
			WithLinks(LinksConfig{OAuthLinkBaseURL: "https://app.example.com/auth/oauth/link"}),
			WithBrowser(BrowserConfig{SessionCookie: inbound.CookieConfig{Secure: true}}),
			WithLogger(log))
		return err
	}
	log, _ := bufferLog()
	err := build(environment.ModeProduction, devOnlyOAuthProvider{}, log)
	if !errors.Is(err, ErrDevelopmentOnlyProvider) || !errors.Is(err, oauth.ErrDevelopmentOnlyProvider) {
		t.Fatalf("production dev-only OAuth provider: err = %v, want ErrDevelopmentOnlyProvider", err)
	}
	if err := build(environment.ModeProduction, stubProvider{}, log); err != nil {
		t.Fatalf("production undeclared OAuth provider: %v", err)
	}
	devLog, buf := bufferLog()
	if err := build(environment.ModeDevelopment, devOnlyOAuthProvider{}, devLog); err != nil {
		t.Fatalf("development dev-only OAuth provider: %v", err)
	}
	if !strings.Contains(buf.String(), "development-only OAuth provider") {
		t.Errorf("development did not warn about the dev-only provider:\n%s", buf.String())
	}
}

// TestDevelopmentOnlyExternalProvider proves the same capability check on an
// external identity provider, through the logic package's sentinel.
func TestDevelopmentOnlyExternalProvider(t *testing.T) {
	repos := testRepositories(Repositories{ExternalSubjects: cookieTestSubjects{}, SecurityEvents: stubSecurityEvents{}})
	build := func(mode environment.Mode, provider sdkauth.Provider, log *slog.Logger) error {
		_, err := New(repos, stubSigner{}, mode, delivery.ModeOff,
			WithPassword(PasswordConfig{PasswordFlowsDisabled: true}),
			WithIdentity(IdentityConfig{IdentifierKeyer: prodKeyer{}, SingleFactorAcknowledged: true}),
			WithAbuseProtection(AbuseProtectionConfig{RateLimiter: durableLimiter{}}),
			WithDelivery(DeliveryConfig{Mailer: prodMailer{}}),
			WithBrowser(BrowserConfig{SessionCookie: inbound.CookieConfig{Secure: true}}),
			WithExternal(authlogic.ExternalConfig{Providers: []authlogic.ExternalProvider{{Name: "idp", Provider: provider}}}),
			WithLogger(log))
		return err
	}
	log, _ := bufferLog()
	err := build(environment.ModeProduction, devOnlyExternalProvider{}, log)
	if !errors.Is(err, ErrDevelopmentOnlyProvider) || !errors.Is(err, authlogic.ErrDevelopmentOnlyProvider) || !errors.Is(err, sdkauth.ErrDevelopmentOnlyProvider) {
		t.Fatalf("production dev-only external provider: err = %v, want ErrDevelopmentOnlyProvider", err)
	}
	if err := build(environment.ModeProduction, cookieTestProvider{}, log); err != nil {
		t.Fatalf("production undeclared external provider: %v", err)
	}
	devLog, buf := bufferLog()
	if err := build(environment.ModeDevelopment, devOnlyExternalProvider{}, devLog); err != nil {
		t.Fatalf("development dev-only external provider: %v", err)
	}
	if !strings.Contains(buf.String(), "development-only external identity provider") {
		t.Errorf("development did not warn about the dev-only provider:\n%s", buf.String())
	}
}

// TestAcknowledgementsHaveNoEnvTag pins every acknowledgement as programmatic:
// hosts run ParseEnvTags over the option records, so a same-named variable must
// never reach one.
func TestAcknowledgementsHaveNoEnvTag(t *testing.T) {
	for _, key := range []string{
		"AUTH_SINGLE_FACTOR_ACKNOWLEDGED", "SINGLE_FACTOR_ACKNOWLEDGED", "SingleFactorAcknowledged",
		"AUTH_OPPORTUNISTIC_TLS_ACKNOWLEDGED", "OPPORTUNISTIC_TLS_ACKNOWLEDGED", "OpportunisticTLSAcknowledged",
		"AUTH_AUDIT_DISABLED_ACKNOWLEDGED", "AUDIT_DISABLED_ACKNOWLEDGED", "AuditDisabledAcknowledged",
	} {
		t.Setenv(key, "true")
	}
	for _, namespace := range []string{"", "AUTH"} {
		var identity IdentityConfig
		var deliveryConfig DeliveryConfig
		var cfg constructorConfig
		for _, target := range []any{&identity, &deliveryConfig, &cfg} {
			if err := environment.ParseEnvTags(namespace, target); err != nil {
				t.Fatalf("ParseEnvTags(%q, %T): %v", namespace, target, err)
			}
		}
		if identity.SingleFactorAcknowledged || cfg.SingleFactorAcknowledged {
			t.Errorf("ParseEnvTags(%q) filled SingleFactorAcknowledged", namespace)
		}
		if deliveryConfig.OpportunisticTLSAcknowledged || cfg.OpportunisticTLSAcknowledged {
			t.Errorf("ParseEnvTags(%q) filled OpportunisticTLSAcknowledged", namespace)
		}
		if cfg.AuditDisabledAcknowledged {
			t.Errorf("ParseEnvTags(%q) filled AuditDisabledAcknowledged", namespace)
		}
	}
}

// TestSessionLifetimeBootLine proves the one session-lifetime boot line
// names the resolved lifetimes and the floor, and that production WARNs, but
// boots, on an access lifetime over DefaultAccessTokenTTLCeiling.
func TestSessionLifetimeBootLine(t *testing.T) {
	wiring := postureWiring{singleFactor: true, opportunisticTLS: true, auditAcknowledged: true}

	log, buf := bufferLog()
	if _, err := wiring.build(environment.ModeProduction, log); err != nil {
		t.Fatalf("default lifetimes: %v", err)
	}
	if want := `msg="auth: session lifetime" access_token_ttl=15m0s refresh_ttl=168h0m0s idle_timeout=off idle_source=default activity=refresh_rotation idle_scope=first_party live_floor=enforced`; strings.Count(buf.String(), want) != 1 {
		t.Errorf("default boot line missing %q:\n%s", want, buf.String())
	}
	if strings.Contains(buf.String(), "access_token_ttl_long") {
		t.Errorf("default access lifetime logged the ceiling WARN:\n%s", buf.String())
	}

	log, buf = bufferLog()
	if _, err := wiring.build(environment.ModeProduction, log, WithSessions(SessionsConfig{AccessTokenTTL: 2 * time.Hour})); err != nil {
		t.Fatalf("production AccessTokenTTL 2h must boot (a WARN, not a refusal): %v", err)
	}
	if want := "posture_warning=access_token_ttl_long access_token_ttl=2h0m0s ceiling=1h0m0s"; strings.Count(buf.String(), want) != 1 {
		t.Errorf("production ceiling WARN %q not logged once:\n%s", want, buf.String())
	}
	if !strings.Contains(buf.String(), "access_token_ttl=2h0m0s refresh_ttl=168h0m0s") {
		t.Errorf("boot line does not carry the configured access lifetime:\n%s", buf.String())
	}

	log, buf = bufferLog()
	if _, err := wiring.build(environment.ModeDevelopment, log, WithSessions(SessionsConfig{AccessTokenTTL: 2 * time.Hour})); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "access_token_ttl_long") {
		t.Errorf("development logged the production ceiling WARN:\n%s", buf.String())
	}
}

// TestLivenessFloorRelaxedWaiver proves BundledRouteAuthLivenessFloorRelaxed
// logs one posture_waiver in production and a plain WARN in development, the
// boot line reports live_floor=relaxed, and no environment variable sets it.
func TestLivenessFloorRelaxedWaiver(t *testing.T) {
	wiring := postureWiring{singleFactor: true, opportunisticTLS: true, auditAcknowledged: true}
	relaxed := WithBrowser(BrowserConfig{SessionCookie: inbound.CookieConfig{Secure: true}, BundledRouteAuthLivenessFloorRelaxed: true})

	log, buf := bufferLog()
	if _, err := wiring.build(environment.ModeProduction, log, relaxed); err != nil {
		t.Fatalf("production relaxed floor: %v", err)
	}
	if n := strings.Count(buf.String(), "posture_waiver=BundledRouteAuthLivenessFloorRelaxed"); n != 1 {
		t.Errorf("posture_waiver=BundledRouteAuthLivenessFloorRelaxed logged %d times, want 1:\n%s", n, buf.String())
	}
	if !strings.Contains(buf.String(), "live_floor=relaxed") {
		t.Errorf("boot line does not report live_floor=relaxed:\n%s", buf.String())
	}

	log, buf = bufferLog()
	if _, err := wiring.build(environment.ModeDevelopment, log, relaxed); err != nil {
		t.Fatalf("development relaxed floor: %v", err)
	}
	if strings.Contains(buf.String(), "posture_waiver=BundledRouteAuthLivenessFloorRelaxed") || strings.Count(buf.String(), "BrowserConfig.BundledRouteAuthLivenessFloorRelaxed") != 1 {
		t.Errorf("development relaxed floor: want one plain WARN and no waiver:\n%s", buf.String())
	}

	log, buf = bufferLog()
	if _, err := wiring.build(environment.ModeProduction, log); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "BundledRouteAuthLivenessFloorRelaxed") {
		t.Errorf("an enforced floor logged the hatch:\n%s", buf.String())
	}

	for _, key := range []string{"AUTH_BUNDLED_ROUTE_AUTH_LIVENESS_FLOOR_RELAXED", "BUNDLED_ROUTE_AUTH_LIVENESS_FLOOR_RELAXED", "BundledRouteAuthLivenessFloorRelaxed"} {
		t.Setenv(key, "true")
	}
	for _, namespace := range []string{"", "AUTH"} {
		var browser BrowserConfig
		var cfg constructorConfig
		for _, target := range []any{&browser, &cfg} {
			if err := environment.ParseEnvTags(namespace, target); err != nil {
				t.Fatalf("ParseEnvTags(%q, %T): %v", namespace, target, err)
			}
		}
		if browser.BundledRouteAuthLivenessFloorRelaxed || cfg.LivenessFloorRelaxed {
			t.Errorf("ParseEnvTags(%q) filled the liveness-floor hatch", namespace)
		}
	}
}

// TestExternalUnboundedAuthenticationAgeWarns proves the root constructor
// carries the logic package's production WARN for an external provider with
// neither a RevocationChecker nor MaxAuthenticationAge, and that a bound
// silences it.
func TestExternalUnboundedAuthenticationAgeWarns(t *testing.T) {
	repos := testRepositories(Repositories{ExternalSubjects: cookieTestSubjects{}, SecurityEvents: stubSecurityEvents{}})
	build := func(p authlogic.ExternalProvider) string {
		t.Helper()
		log, buf := bufferLog()
		p.Name, p.Provider = "idp", cookieTestProvider{}
		_, err := New(repos, stubSigner{}, environment.ModeProduction, delivery.ModeOff,
			WithPassword(PasswordConfig{PasswordFlowsDisabled: true}),
			WithIdentity(IdentityConfig{IdentifierKeyer: prodKeyer{}, SingleFactorAcknowledged: true}),
			WithAbuseProtection(AbuseProtectionConfig{RateLimiter: durableLimiter{}}),
			WithDelivery(DeliveryConfig{Mailer: prodMailer{}}),
			WithBrowser(BrowserConfig{SessionCookie: inbound.CookieConfig{Secure: true}}),
			WithExternal(authlogic.ExternalConfig{Providers: []authlogic.ExternalProvider{p}}),
			WithLogger(log))
		if err != nil {
			t.Fatalf("production external provider: %v", err)
		}
		return buf.String()
	}
	if logs := build(authlogic.ExternalProvider{}); strings.Count(logs, "external_authentication_age=unbounded provider=idp revocation_checker=none") != 1 ||
		!strings.Contains(logs, `msg="auth: external provider lifetime" provider=idp max_authentication_age=off revocation_checker=none fail_open=false`) {
		t.Errorf("unbounded provider: want the WARN once and the boot line:\n%s", logs)
	}
	if logs := build(authlogic.ExternalProvider{MaxAuthenticationAge: time.Hour}); strings.Contains(logs, "external_authentication_age=unbounded") ||
		!strings.Contains(logs, "max_authentication_age=1h0m0s") {
		t.Errorf("bounded provider: want no WARN and the bound on the boot line:\n%s", logs)
	}
}
