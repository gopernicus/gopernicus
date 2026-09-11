package authentication

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	inbound "github.com/gopernicus/gopernicus/pockets/authentication/inbound/http"
	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	protection "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/protection"
	delivery "github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify/email"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	environment "github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// stubChallenges satisfies challenge.Repository for the enable-time construction
// tests. None is driven — the tests assert wiring, not consume flow.
type stubChallenges struct{}

func (stubChallenges) Replace(context.Context, challenge.Challenge) (challenge.Challenge, error) {
	return challenge.Challenge{}, nil
}
func (stubChallenges) ConsumeCode(context.Context, string, string, []challenge.DigestCandidate, string, int, time.Time) (challenge.Consumed, challenge.ConsumeOutcome, error) {
	return challenge.Consumed{}, challenge.OutcomeNotFound, nil
}
func (stubChallenges) ConsumeToken(context.Context, string, string, time.Time) (challenge.Consumed, error) {
	return challenge.Consumed{}, nil
}
func (stubChallenges) PurgeExpired(context.Context, time.Time, int) (int, error) { return 0, nil }

// prodMailer is a production-capable email double: it declares capability
// metadata and is not development-only, so a production-mode construction
// accepts it. Existing production-capable doubles declare metadata explicitly
// (AV3-0.1 acceptance).
type prodMailer struct{}

func (prodMailer) Send(context.Context, email.Message) error { return nil }
func (prodMailer) Capabilities() notify.Capabilities {
	return notify.Capabilities{TransportSecurity: notify.TransportSecurityTLS}
}

// prodNotifier is a production-capable notifier double declaring metadata.
type prodNotifier struct{}

func (prodNotifier) Send(context.Context, string, string) error { return nil }
func (prodNotifier) Capabilities() notify.Capabilities {
	return notify.Capabilities{TransportSecurity: notify.TransportSecurityTLS}
}

// stubEncrypter satisfies cryptids.Encrypter for the delivery-outbox construction
// tests. No job is delivered here (the tests assert wiring, not seal/open), so it
// is an identity transform.
type stubEncrypter struct{}

func (stubEncrypter) Encrypt(s string) (string, error) { return s, nil }
func (stubEncrypter) Decrypt(s string) (string, error) { return s, nil }

// durableLimiter is a shared/durable rate-limiter double: it positively declares
// itself durable through RateLimiterDurabilityReporter, so production construction
// accepts it (it stands in for a Redis-backed limiter). Its Allow always permits —
// the construction tests assert wiring, not throttling.
type durableLimiter struct{}

func (durableLimiter) Allow(context.Context, string, ratelimiter.Limit) (ratelimiter.Result, error) {
	return ratelimiter.Result{Allowed: true}, nil
}
func (durableLimiter) Reset(context.Context, string) error { return nil }
func (durableLimiter) RateLimiterDurability() authlogic.LimiterDurability {
	return authlogic.LimiterDurability{}
}

// inProcessLimiter is a custom limiter that positively declares itself
// in-process-only — production rejects it (ErrNonDurableRateLimiter) exactly like
// the bundled ratelimiter.Memory default.
type inProcessLimiter struct{ durableLimiter }

func (inProcessLimiter) RateLimiterDurability() authlogic.LimiterDurability {
	return authlogic.LimiterDurability{InProcessOnly: true}
}

// prodKeyer is a production-capable identifier keyer double: it satisfies
// IdentifierKeyer so production construction (which requires the keyer) is
// satisfied. The digest shape is irrelevant here — PII-freeness of the login key is
// proven in the authentication service limiter tests.
type prodKeyer struct{}

func (prodKeyer) IdentifierKey(kind, normalizedValue string) string { return "k:" + kind }

// TestNewServiceRuntimeModeRequired proves an empty RuntimeMode is rejected so a
// host cannot accidentally inherit the development posture (design §8).
func TestNewServiceRuntimeModeRequired(t *testing.T) {
	_, err := New(testRepositories(Repositories{}),
		stubSigner{},
		"",
		"",
		WithPassword(PasswordConfig{Hasher: stubHasher{}}),
		WithDelivery(DeliveryConfig{Mailer: stubMailer{}}))
	if !errors.Is(err, ErrRuntimeModeRequired) {
		t.Errorf("empty RuntimeMode: err=%v, want ErrRuntimeModeRequired", err)
	}
}

// TestNewServiceChallengeProtectorRequired proves the enable-time rule (design
// §3.3): wiring the Challenges repository enables the atomic secret rail, which
// requires a ChallengeProtector — nil is rejected with ErrChallengeProtectorRequired.
func TestNewServiceChallengeProtectorRequired(t *testing.T) {
	_, err := New(testRepositories(Repositories{Challenges: stubChallenges{}}),
		stubSigner{},
		environment.ModeDevelopment,
		delivery.ModeOff,
		WithPassword(PasswordConfig{Hasher: stubHasher{}}),
		WithDelivery(DeliveryConfig{Mailer: stubMailer{}}))
	if !errors.Is(err, ErrChallengeProtectorRequired) {
		t.Errorf("Challenges wired without protector: err=%v, want ErrChallengeProtectorRequired", err)
	}
}

// TestNewServiceChallengeSubsystemWiring proves the challenge slot is deny-by-
// absence: a nil Challenges repository tolerates a nil protector, and wiring both
// together constructs cleanly.
func TestNewServiceChallengeSubsystemWiring(t *testing.T) {
	base := constructorConfig{Hasher: stubHasher{}, Mailer: stubMailer{}, TokenSigner: stubSigner{}, RuntimeMode: environment.ModeDevelopment, DeliveryMode: delivery.ModeOff}
	if _, err := newFixture(testRepositories(Repositories{}), base); err != nil {
		t.Errorf("challenge off (nil repo, nil protector): err=%v, want nil", err)
	}
	protector, err := protection.NewHMACChallengeProtector(protection.HMACKeyRing{
		Active: "2026-01",
		Keys:   map[string][]byte{"2026-01": make([]byte, 32)},
	})
	if err != nil {
		t.Fatalf("NewHMACChallengeProtector: %v", err)
	}
	withProtector := base
	withProtector.ChallengeProtector = protector
	if _, err := newFixture(testRepositories(Repositories{Challenges: stubChallenges{}}), withProtector); err != nil {
		t.Errorf("challenge on (repo + protector): err=%v, want nil", err)
	}
}

// TestNewServiceRuntimeModeInvalid proves an unknown RuntimeMode is rejected.
func TestNewServiceRuntimeModeInvalid(t *testing.T) {
	_, err := New(testRepositories(Repositories{}),
		stubSigner{},
		"staging",
		"",
		WithPassword(PasswordConfig{Hasher: stubHasher{}}),
		WithDelivery(DeliveryConfig{Mailer: stubMailer{}}))
	if !errors.Is(err, ErrRuntimeModeInvalid) {
		t.Errorf("unknown RuntimeMode: err=%v, want ErrRuntimeModeInvalid", err)
	}
}

// TestNewServiceRuntimeModeCheckedAfterRequiredCollaborators proves RuntimeMode
// validation does not mask the pre-existing required-collaborator errors (nil
// Hasher/Mailer/TokenSigner still report their own errors first).
func TestNewServiceRuntimeModeCheckedAfterRequiredCollaborators(t *testing.T) {
	if _, err := New(testRepositories(Repositories{}),
		nil,
		"",
		""); !errors.Is(err, ErrHasherRequired) {
		t.Errorf("nil Hasher with empty mode: err=%v, want ErrHasherRequired", err)
	}
	if _, err := New(testRepositories(Repositories{}),
		nil,
		"",
		"",
		WithPassword(PasswordConfig{Hasher: stubHasher{}}),
		WithDelivery(DeliveryConfig{Mailer: stubMailer{}})); !errors.Is(err, ErrTokenSignerRequired) {
		t.Errorf("nil signer with empty mode: err=%v, want ErrTokenSignerRequired", err)
	}
}

// TestNewServiceProductionRejectsConsoleEmail proves the console email sender is
// rejected in production RuntimeMode (design §6.3): it leaks message bodies.
func TestNewServiceProductionRejectsConsoleEmail(t *testing.T) {
	_, err := New(testRepositories(Repositories{}),
		stubSigner{},
		environment.ModeProduction,
		delivery.ModeOff,
		WithPassword(PasswordConfig{Hasher: stubHasher{}}),
		WithDelivery(DeliveryConfig{Mailer: email.NewConsole(nil)}))
	if !errors.Is(err, ErrInsecureDeliveryTransport) {
		t.Errorf("console email in production: err=%v, want ErrInsecureDeliveryTransport", err)
	}
}

// TestNewServiceProductionRejectsMetadatalessEmail proves an email Sender that
// declares no capability metadata is rejected in production (cannot be proven
// safe). stubMailer implements no CapabilityReporter.
func TestNewServiceProductionRejectsMetadatalessEmail(t *testing.T) {
	_, err := New(testRepositories(Repositories{}),
		stubSigner{},
		environment.ModeProduction,
		delivery.ModeOff,
		WithPassword(PasswordConfig{Hasher: stubHasher{}}),
		WithDelivery(DeliveryConfig{Mailer: stubMailer{}}))
	if !errors.Is(err, ErrInsecureDeliveryTransport) {
		t.Errorf("metadata-less email in production: err=%v, want ErrInsecureDeliveryTransport", err)
	}
}

// TestNewServiceProductionRejectsConsoleNotifier proves a development-only
// notifier is rejected in production even when the Mailer is production-capable.
func TestNewServiceProductionRejectsConsoleNotifier(t *testing.T) {
	_, err := New(testRepositories(Repositories{}),
		stubSigner{},
		environment.ModeProduction,
		delivery.ModeOff,
		WithPassword(PasswordConfig{Hasher: stubHasher{}}),
		WithDelivery(DeliveryConfig{Mailer: prodMailer{}, BodySenders: map[string]delivery.BodySender{sdk.AddressKindPhone: notify.NewConsole(nil)}}))
	if !errors.Is(err, ErrInsecureDeliveryTransport) {
		t.Errorf("console notifier in production: err=%v, want ErrInsecureDeliveryTransport", err)
	}
}

// TestNewServiceProductionAcceptsDeclaredTransports proves production
// construction succeeds when every delivery transport declares metadata and is
// not development-only.
func TestNewServiceProductionAcceptsDeclaredTransports(t *testing.T) {
	svc, err := New(testRepositories(Repositories{}),
		stubSigner{},
		environment.ModeProduction,
		delivery.ModeOff,
		WithPassword(PasswordConfig{Hasher: stubHasher{}}),
		WithIdentity(IdentityConfig{IdentifierKeyer: prodKeyer{}}),
		WithAbuseProtection(AbuseProtectionConfig{RateLimiter: durableLimiter{}}),
		WithDelivery(DeliveryConfig{Mailer: prodMailer{}, BodySenders: map[string]delivery.BodySender{"sms": prodNotifier{}}}),
		WithBrowser(BrowserConfig{SessionCookie: inbound.CookieConfig{Secure: true}}))
	if err != nil {
		t.Fatalf("production with declared transports: err=%v, want nil", err)
	}
	if svc == nil {
		t.Fatal("NewService returned nil Service")
	}
}

// TestNewServiceDevelopmentWarnsOnConsoleTransport proves a development-only
// transport is permitted in development but emits a startup WARN.
func TestNewServiceDevelopmentWarnsOnConsoleTransport(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	_, err := New(testRepositories(Repositories{}),
		stubSigner{},
		environment.ModeDevelopment,
		delivery.ModeOff,
		WithPassword(PasswordConfig{Hasher: stubHasher{}}),
		WithDelivery(DeliveryConfig{Mailer: email.NewConsole(nil)}),
		WithLogger(log))
	if err != nil {
		t.Fatalf("console email in development: err=%v, want nil", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("development-only delivery transport")) {
		t.Errorf("expected a development-only transport WARN, got: %s", buf.String())
	}
}

// prodDeliveryConfig is a production constructorConfig with declared transports, an outbox
// encrypter, the generic-jobs dispatcher, and the runtime acknowledgment — every
// delivery gate satisfied. Cases override one dimension to isolate a single failing
// gate.
func prodDeliveryConfig() constructorConfig {
	return constructorConfig{
		PasswordFlowsDisabled:    true,
		SessionCookie:            inbound.CookieConfig{Secure: true},
		Hasher:                   stubHasher{},
		Mailer:                   prodMailer{},
		TokenSigner:              stubSigner{},
		RuntimeMode:              environment.ModeProduction,
		DeliveryMode:             delivery.ModeJobs,
		DeliveryDispatcher:       stubDispatcher{},
		DeliveryEncrypter:        stubEncrypter{},
		DeliveryJobsAcknowledged: true,
		// The always-on production requirements (AV3-5.4): a shared/durable limiter
		// and the PII-free identifier keyer, so these delivery-focused cases isolate
		// their own failing dimension rather than tripping the rate-limit gates.
		RateLimiter:     durableLimiter{},
		IdentifierKeyer: prodKeyer{},
	}
}

// TestNewServiceProductionRequiresDeliveryWorkerAcknowledgment proves production
// refuses a wired jobs dispatcher unless the host affirms it runs the generic jobs
// runtime (design §8): the queue is the only send path, so an unacknowledged runtime
// would swallow every message.
func TestNewServiceProductionRequiresDeliveryWorkerAcknowledgment(t *testing.T) {
	cfg := prodDeliveryConfig()
	cfg.DeliveryJobsAcknowledged = false
	_, err := newFixture(testRepositories(Repositories{}), cfg)
	if !errors.Is(err, ErrDeliveryJobsUnacknowledged) {
		t.Errorf("unacknowledged jobs runtime in production: err=%v, want ErrDeliveryJobsUnacknowledged", err)
	}
}

// TestNewServiceProductionMissingEncrypterBeforeAcknowledgment proves the missing-
// encrypter error fires before the acknowledgment check (ordering), so a nil
// encrypter is reported as ErrDeliveryEncrypterRequired even with no acknowledgment.
func TestNewServiceProductionMissingEncrypterBeforeAcknowledgment(t *testing.T) {
	cfg := prodDeliveryConfig()
	cfg.DeliveryEncrypter = nil
	cfg.DeliveryJobsAcknowledged = false
	_, err := newFixture(testRepositories(Repositories{}), cfg)
	if !errors.Is(err, ErrDeliveryEncrypterRequired) {
		t.Errorf("nil encrypter with dispatcher wired: err=%v, want ErrDeliveryEncrypterRequired", err)
	}
}

// TestNewServiceDevelopmentToleratesUnacknowledgedRuntime proves development enforces
// no runtime acknowledgment: a jobs dispatcher with no acknowledgment constructs
// cleanly, while the encrypted-payload requirement still holds (the encrypter is
// wired here).
func TestNewServiceDevelopmentToleratesUnacknowledgedRuntime(t *testing.T) {
	_, err := New(testRepositories(Repositories{}),
		stubSigner{},
		environment.ModeDevelopment,
		delivery.ModeJobs,
		WithPassword(PasswordConfig{Hasher: stubHasher{}, PasswordFlowsDisabled: true}),
		WithDelivery(DeliveryConfig{Mailer: stubMailer{}, DeliveryEncrypter: stubEncrypter{}, DeliveryDispatcher: stubDispatcher{}}))
	if err != nil {
		t.Errorf("unacknowledged jobs runtime in development: err=%v, want nil", err)
	}
}

// TestNewServiceDevelopmentOutboxStillRequiresEncrypter proves the encrypted-
// payload requirement is mode-independent: development with a wired dispatcher and no
// encrypter is still ErrDeliveryEncrypterRequired (design §8 — development permits
// console but still requires encrypted job payloads).
func TestNewServiceDevelopmentOutboxStillRequiresEncrypter(t *testing.T) {
	_, err := New(testRepositories(Repositories{}),
		stubSigner{},
		environment.ModeDevelopment,
		delivery.ModeJobs,
		WithPassword(PasswordConfig{Hasher: stubHasher{}}),
		WithDelivery(DeliveryConfig{Mailer: stubMailer{}, DeliveryDispatcher: stubDispatcher{}}))
	if !errors.Is(err, ErrDeliveryEncrypterRequired) {
		t.Errorf("dev outbox without encrypter: err=%v, want ErrDeliveryEncrypterRequired", err)
	}
}

// prodLimiterConfig is a production constructorConfig with declared transports and the PII-free
// keyer satisfied — every gate green EXCEPT the rate limiter each case supplies, so
// the limiter dimension is isolated.
func prodLimiterConfig() constructorConfig {
	return constructorConfig{
		PasswordFlowsDisabled: true,
		SessionCookie:         inbound.CookieConfig{Secure: true},
		Hasher:                stubHasher{},
		Mailer:                prodMailer{},
		TokenSigner:           stubSigner{},
		RuntimeMode:           environment.ModeProduction,
		DeliveryMode:          delivery.ModeOff,
		IdentifierKeyer:       prodKeyer{},
	}
}

// TestNewServiceProductionRejectsDefaultMemoryLimiter proves production rejects a
// nil RateLimiter (design §4.4/§8): the pocket defaults it to the in-process
// ratelimiter.Memory, whose budget is per-process and would be N× across instances.
func TestNewServiceProductionRejectsDefaultMemoryLimiter(t *testing.T) {
	cfg := prodLimiterConfig() // RateLimiter left nil → in-process memory default
	_, err := newFixture(testRepositories(Repositories{}), cfg)
	if !errors.Is(err, ErrNonDurableRateLimiter) {
		t.Errorf("nil (memory-default) limiter in production: err=%v, want ErrNonDurableRateLimiter", err)
	}
}

// TestNewServiceProductionRejectsExplicitMemoryLimiter proves production rejects an
// explicitly wired ratelimiter.Memory too — the concrete in-process type is caught,
// not only the nil default.
func TestNewServiceProductionRejectsExplicitMemoryLimiter(t *testing.T) {
	cfg := prodLimiterConfig()
	cfg.RateLimiter = ratelimiter.NewMemory()
	_, err := newFixture(testRepositories(Repositories{}), cfg)
	if !errors.Is(err, ErrNonDurableRateLimiter) {
		t.Errorf("explicit memory limiter in production: err=%v, want ErrNonDurableRateLimiter", err)
	}
}

// TestNewServiceProductionRejectsDeclaredInProcessLimiter proves a custom limiter
// that declares itself in-process-only through RateLimiterDurabilityReporter is
// rejected in production, exactly like the bundled memory limiter.
func TestNewServiceProductionRejectsDeclaredInProcessLimiter(t *testing.T) {
	cfg := prodLimiterConfig()
	cfg.RateLimiter = inProcessLimiter{}
	_, err := newFixture(testRepositories(Repositories{}), cfg)
	if !errors.Is(err, ErrNonDurableRateLimiter) {
		t.Errorf("declared in-process limiter in production: err=%v, want ErrNonDurableRateLimiter", err)
	}
}

// TestNewServiceProductionAcceptsDurableLimiter proves a limiter declaring itself
// durable/shared constructs cleanly in production.
func TestNewServiceProductionAcceptsDurableLimiter(t *testing.T) {
	cfg := prodLimiterConfig()
	cfg.RateLimiter = durableLimiter{}
	svc, err := newFixture(testRepositories(Repositories{}), cfg)
	if err != nil {
		t.Fatalf("durable limiter in production: err=%v, want nil", err)
	}
	if svc == nil {
		t.Fatal("NewService returned nil Service")
	}
}

// TestNewServiceProductionRequiresIdentifierKeyer proves production requires the
// shared IdentifierKeyer (design §4.4/§8): PII-free rate-limit keys are always
// active, so a per-instance SHA-256 fallback is not the shared keyed digest a
// multi-instance deployment needs. A durable limiter is supplied so the keyer gate
// is the one under test.
func TestNewServiceProductionRequiresIdentifierKeyer(t *testing.T) {
	cfg := prodLimiterConfig()
	cfg.IdentifierKeyer = nil
	cfg.RateLimiter = durableLimiter{}
	_, err := newFixture(testRepositories(Repositories{}), cfg)
	if !errors.Is(err, ErrIdentifierKeyerRequired) {
		t.Errorf("production without identifier keyer: err=%v, want ErrIdentifierKeyerRequired", err)
	}
}

// TestNewServiceDevelopmentWarnsOnMemoryLimiter proves the in-process memory limiter
// is permitted in development but emits the multi-instance startup WARN (design §4.4).
func TestNewServiceDevelopmentWarnsOnMemoryLimiter(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	_, err := New(testRepositories(Repositories{}),
		stubSigner{},
		environment.ModeDevelopment,
		delivery.ModeOff,
		WithPassword(PasswordConfig{Hasher: stubHasher{}}),
		WithDelivery(DeliveryConfig{Mailer: stubMailer{}}),
		WithLogger(log))
	if err != nil {
		t.Fatalf("memory limiter in development: err=%v, want nil", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("in-process rate limiter")) {
		t.Errorf("expected an in-process rate-limiter WARN, got: %s", buf.String())
	}
}

// TestNewServiceDevelopmentToleratesMissingIdentifierKeyer proves development does
// not require the keyer: the per-instance SHA-256 digest fallback keeps keys
// PII-free without it.
func TestNewServiceDevelopmentToleratesMissingIdentifierKeyer(t *testing.T) {
	if _, err := New(testRepositories(Repositories{}),
		stubSigner{},
		environment.ModeDevelopment,
		delivery.ModeOff,
		WithPassword(PasswordConfig{Hasher: stubHasher{}}),
		WithAbuseProtection(AbuseProtectionConfig{RateLimiter: durableLimiter{}}),
		WithDelivery(DeliveryConfig{Mailer: stubMailer{}})); err != nil {
		t.Errorf("development without identifier keyer: err=%v, want nil", err)
	}
}

// ---------------------------------------------------------------------------
// AV3-7.1 — passwordless enablement construction matrix (design §4.1/§4.2/§6.4/§8)
// ---------------------------------------------------------------------------

// passwordlessProtector builds an HMAC challenge protector for the passwordless
// enablement tests — wiring Repositories.Challenges REQUIRES one.
func passwordlessProtector(t *testing.T) protection.ChallengeProtector {
	t.Helper()
	p, err := protection.NewHMACChallengeProtector(protection.HMACKeyRing{
		Active: "2026-01",
		Keys:   map[string][]byte{"2026-01": make([]byte, 32)},
	})
	if err != nil {
		t.Fatalf("NewHMACChallengeProtector: %v", err)
	}
	return p
}

// passwordlessDevRepos is the fully-wired repository set a passwordless-enabled host
// needs: the atomic challenge rail. The delivery runtime is wired in constructorConfig
// (DeliveryDispatcher), not a repository.
func passwordlessDevRepos() Repositories {
	return Repositories{Challenges: stubChallenges{}}
}

// passwordlessDevConfig is a development constructorConfig with every passwordless enablement
// gate satisfied for the email kind: the challenge protector, the generic-jobs
// dispatcher, the outbox encrypter, and an absolute PublicAuthBaseURL. Cases override
// one dimension to isolate a single failing gate.
func passwordlessDevConfig(t *testing.T) constructorConfig {
	t.Helper()
	return constructorConfig{
		PasswordFlowsDisabled: true,
		Hasher:                stubHasher{},
		Mailer:                stubMailer{},
		TokenSigner:           stubSigner{},
		RuntimeMode:           environment.ModeDevelopment,
		// Passwordless requires a delivery runtime; the wired jobs dispatcher makes
		// "jobs" the mode for every case except the "delivery outbox absent" case, which
		// drops the dispatcher and selects "off".
		DeliveryMode:       delivery.ModeJobs,
		DeliveryDispatcher: stubDispatcher{},
		ChallengeProtector: passwordlessProtector(t),
		DeliveryEncrypter:  stubEncrypter{},
		PublicAuthBaseURL:  "https://auth.example.com",
	}
}

// TestNewServicePasswordlessAbsentByDefault proves the empty (default) Passwordless
// leaves the subsystem OFF: construction succeeds with no passwordless collaborators
// wired and PasswordlessEnabled reports false, so the transport registers no
// passwordless routes (deny-by-absence, design §4.2).
func TestNewServicePasswordlessAbsentByDefault(t *testing.T) {
	svc, err := New(testRepositories(Repositories{}),
		stubSigner{},
		environment.ModeDevelopment,
		delivery.ModeOff,
		WithPassword(PasswordConfig{Hasher: stubHasher{}}),
		WithDelivery(DeliveryConfig{Mailer: stubMailer{}}))
	if err != nil {
		t.Fatalf("no passwordless: err=%v, want nil", err)
	}
	if svc.Authentication.PasswordlessEnabled() {
		t.Error("PasswordlessEnabled() = true with empty PasswordlessConfig.Passwordless, want false")
	}
}

// TestNewServicePasswordlessMatrix drives every partial-wiring combination of the
// passwordless enablement gate (design §4.2/§6.4/§8), asserting the exact
// construction error (or success) and the resulting route presence via
// PasswordlessEnabled.
func TestNewServicePasswordlessMatrix(t *testing.T) {
	cases := []struct {
		name    string
		repos   func() Repositories
		mutate  func(*constructorConfig)
		wantErr error
		enabled bool // asserted only when wantErr is nil
	}{
		{
			name:  "email fully wired",
			repos: passwordlessDevRepos,
			mutate: func(c *constructorConfig) {
				c.Passwordless = []string{"email"}
			},
			enabled: true,
		},
		{
			name:  "invalid kind rejected",
			repos: passwordlessDevRepos,
			mutate: func(c *constructorConfig) {
				c.Passwordless = []string{"push"}
			},
			wantErr: ErrPasswordlessKindInvalid,
		},
		{
			name:  "phone without notifier unsupported",
			repos: passwordlessDevRepos,
			mutate: func(c *constructorConfig) {
				c.Passwordless = []string{"phone"}
			},
			wantErr: ErrPasswordlessKindUnsupported,
		},
		{
			name:  "phone with notifier wired",
			repos: passwordlessDevRepos,
			mutate: func(c *constructorConfig) {
				c.Passwordless = []string{"phone"}
				c.BodySenders = map[string]delivery.BodySender{"phone": prodNotifier{}}
			},
			enabled: true,
		},
		{
			name:  "email and phone both enabled",
			repos: passwordlessDevRepos,
			mutate: func(c *constructorConfig) {
				c.Passwordless = []string{"email", "phone"}
				c.BodySenders = map[string]delivery.BodySender{"phone": prodNotifier{}}
			},
			enabled: true,
		},
		{
			name:  "challenge rail absent",
			repos: func() Repositories { return Repositories{} },
			mutate: func(c *constructorConfig) {
				c.Passwordless = []string{"email"}
				c.ChallengeProtector = nil // no challenge rail wired
			},
			wantErr: ErrPasswordlessChallengeRequired,
		},
		{
			name:  "delivery outbox absent",
			repos: func() Repositories { return Repositories{Challenges: stubChallenges{}} },
			mutate: func(c *constructorConfig) {
				c.Passwordless = []string{"email"}
				// No delivery dispatcher is wired, so "jobs" is not selectable; "off" makes
				// passwordless's own delivery requirement the dimension under test.
				c.DeliveryMode = delivery.ModeOff
				c.DeliveryDispatcher = nil
			},
			wantErr: ErrPasswordlessDeliveryRequired,
		},
		{
			name:  "public base url absent",
			repos: passwordlessDevRepos,
			mutate: func(c *constructorConfig) {
				c.Passwordless = []string{"email"}
				c.PublicAuthBaseURL = ""
			},
			wantErr: ErrPublicAuthBaseURLRequired,
		},
		{
			name:  "public base url not absolute",
			repos: passwordlessDevRepos,
			mutate: func(c *constructorConfig) {
				c.Passwordless = []string{"email"}
				c.PublicAuthBaseURL = "/auth"
			},
			wantErr: ErrPublicAuthBaseURLInvalid,
		},
		{
			name:  "development permits http base url",
			repos: passwordlessDevRepos,
			mutate: func(c *constructorConfig) {
				c.Passwordless = []string{"email"}
				c.PublicAuthBaseURL = "http://localhost:8080"
			},
			enabled: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := passwordlessDevConfig(t)
			tc.mutate(&cfg)
			svc, err := newFixture(testRepositories(tc.repos()), cfg)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err=%v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("err=%v, want nil", err)
			}
			if got := svc.Authentication.PasswordlessEnabled(); got != tc.enabled {
				t.Errorf("PasswordlessEnabled() = %v, want %v", got, tc.enabled)
			}
		})
	}
}

// TestNewServicePasswordlessKindEnabled proves the resolved kind set maps only the
// listed kinds (design §4.2): the transport-facing per-kind capability check reports
// true for an enabled kind and false for a kind the host did not list.
func TestNewServicePasswordlessKindEnabled(t *testing.T) {
	cfg := passwordlessDevConfig(t)
	cfg.Passwordless = []string{"email"}
	svc, err := newFixture(testRepositories(passwordlessDevRepos()), cfg)
	if err != nil {
		t.Fatalf("email passwordless: err=%v, want nil", err)
	}
	if !svc.Authentication.PasswordlessKindEnabled("email") {
		t.Error("PasswordlessKindEnabled(email) = false, want true")
	}
	if svc.Authentication.PasswordlessKindEnabled("phone") {
		t.Error("PasswordlessKindEnabled(phone) = true, want false (not listed)")
	}
}

// TestNewServiceProductionPasswordlessRejectsHTTPBaseURL proves production rejects a
// non-HTTPS magic-link base URL (design §6.4): a link over plain HTTP exposes the
// single-use token in transit. Every other production gate is satisfied so the base
// URL is the dimension under test.
func TestNewServiceProductionPasswordlessRejectsHTTPBaseURL(t *testing.T) {
	_, err := New(testRepositories(passwordlessDevRepos()),
		stubSigner{},
		environment.ModeProduction,
		delivery.ModeJobs,
		WithPassword(PasswordConfig{Hasher: stubHasher{}}),
		WithIdentity(IdentityConfig{ChallengeProtector: passwordlessProtector(t), IdentifierKeyer: prodKeyer{}}),
		WithAbuseProtection(AbuseProtectionConfig{RateLimiter: durableLimiter{}}),
		WithDelivery(DeliveryConfig{Mailer: prodMailer{}, DeliveryEncrypter: stubEncrypter{}, DeliveryDispatcher: stubDispatcher{}, DeliveryJobsAcknowledged: true}),
		WithPasswordless(PasswordlessConfig{Passwordless: []string{"email"}}),
		WithLinks(LinksConfig{PublicAuthBaseURL: "http://auth.example.com"}))
	if !errors.Is(err, ErrPublicAuthBaseURLInsecure) {
		t.Errorf("http base url in production: err=%v, want ErrPublicAuthBaseURLInsecure", err)
	}
}

// TestNewServiceProductionPasswordlessAcceptsFullWiring proves a fully-wired
// production passwordless config constructs cleanly: production-capable transports,
// a durable limiter + identifier keyer, an acknowledged worker, an HTTPS base URL,
// and the challenge + outbox rails all satisfied (design §4.2/§8).
func TestNewServiceProductionPasswordlessAcceptsFullWiring(t *testing.T) {
	svc, err := New(testRepositories(passwordlessDevRepos()),
		stubSigner{},
		environment.ModeProduction,
		delivery.ModeJobs,
		WithPassword(PasswordConfig{Hasher: stubHasher{}, PasswordFlowsDisabled: true}),
		WithIdentity(IdentityConfig{ChallengeProtector: passwordlessProtector(t), IdentifierKeyer: prodKeyer{}}),
		WithAbuseProtection(AbuseProtectionConfig{RateLimiter: durableLimiter{}}),
		WithDelivery(DeliveryConfig{Mailer: prodMailer{}, BodySenders: map[string]delivery.BodySender{"phone": prodNotifier{}}, DeliveryEncrypter: stubEncrypter{}, DeliveryDispatcher: stubDispatcher{}, DeliveryJobsAcknowledged: true}),
		WithPasswordless(PasswordlessConfig{Passwordless: []string{"email", "phone"}}),
		WithLinks(LinksConfig{PublicAuthBaseURL: "https://auth.example.com"}),
		WithBrowser(BrowserConfig{SessionCookie: inbound.CookieConfig{Secure: true}}))
	if err != nil {
		t.Fatalf("full production passwordless wiring: err=%v, want nil", err)
	}
	if !svc.Authentication.PasswordlessEnabled() {
		t.Error("PasswordlessEnabled() = false after full production wiring, want true")
	}
}

// TestNewServiceProductionPasswordlessRejectsConsolePhone proves the passwordless
// phone channel must be production-capable in production (design §4.2/§6.3): a
// development-only console notifier standing in for the phone transport is rejected
// by the always-on transport check, so passwordless cannot silently enable an
// insecure channel.
func TestNewServiceProductionPasswordlessRejectsConsolePhone(t *testing.T) {
	_, err := New(testRepositories(passwordlessDevRepos()),
		stubSigner{},
		environment.ModeProduction,
		delivery.ModeJobs,
		WithPassword(PasswordConfig{Hasher: stubHasher{}}),
		WithIdentity(IdentityConfig{ChallengeProtector: passwordlessProtector(t), IdentifierKeyer: prodKeyer{}}),
		WithAbuseProtection(AbuseProtectionConfig{RateLimiter: durableLimiter{}}),
		WithDelivery(DeliveryConfig{Mailer: prodMailer{}, BodySenders: map[string]delivery.BodySender{sdk.AddressKindPhone: notify.NewConsole(nil)}, DeliveryEncrypter: stubEncrypter{}, DeliveryDispatcher: stubDispatcher{}, DeliveryJobsAcknowledged: true}),
		WithPasswordless(PasswordlessConfig{Passwordless: []string{"phone"}}),
		WithLinks(LinksConfig{PublicAuthBaseURL: "https://auth.example.com"}))
	if !errors.Is(err, ErrInsecureDeliveryTransport) {
		t.Errorf("console phone notifier in production: err=%v, want ErrInsecureDeliveryTransport", err)
	}
}
