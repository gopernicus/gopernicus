package authentication

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/features/authentication/domain/deliveryjob"
	"github.com/gopernicus/gopernicus/sdk/capabilities/email"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
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
func (prodMailer) Capabilities() email.Capabilities {
	return email.Capabilities{TransportSecurity: email.TransportSecurityTLS}
}

// prodNotifier is a production-capable notifier double declaring metadata.
type prodNotifier struct{ kind string }

func (p prodNotifier) Kind() string                                                 { return p.kind }
func (prodNotifier) Notify(context.Context, identity.Address, notify.Message) error { return nil }
func (prodNotifier) Capabilities() notify.Capabilities {
	return notify.Capabilities{TransportSecurity: notify.TransportSecurityTLS}
}

// stubEncrypter satisfies cryptids.Encrypter for the delivery-outbox construction
// tests. No job is delivered here (the tests assert wiring, not seal/open), so it
// is an identity transform.
type stubEncrypter struct{}

func (stubEncrypter) Encrypt(s string) (string, error) { return s, nil }
func (stubEncrypter) Decrypt(s string) (string, error) { return s, nil }

// stubDeliveryJobs is a delivery repository that declares NO durability metadata.
// Under the "where metadata can identify it" rule (design §8) production tolerates
// it: a durable store is not asked to prove a negative. None of its methods are
// driven — the construction tests assert wiring only.
type stubDeliveryJobs struct{}

func (stubDeliveryJobs) Enqueue(context.Context, deliveryjob.Job) (deliveryjob.Job, error) {
	return deliveryjob.Job{}, nil
}
func (stubDeliveryJobs) Replace(context.Context, deliveryjob.Job) (deliveryjob.Job, error) {
	return deliveryjob.Job{}, nil
}
func (stubDeliveryJobs) Claim(context.Context, time.Time, string, time.Duration) (deliveryjob.Job, error) {
	return deliveryjob.Job{}, nil
}
func (stubDeliveryJobs) Succeed(context.Context, string, string, time.Time) error { return nil }
func (stubDeliveryJobs) Retry(context.Context, string, string, time.Time, string, time.Time) error {
	return nil
}
func (stubDeliveryJobs) Fail(context.Context, string, string, string, time.Time) error { return nil }
func (stubDeliveryJobs) Cancel(context.Context, string, time.Time) error               { return nil }
func (stubDeliveryJobs) PurgeTerminal(context.Context, time.Time, int) (int, error)    { return 0, nil }
func (stubDeliveryJobs) GetLatestByIdempotencyKey(context.Context, string) (deliveryjob.Job, error) {
	return deliveryjob.Job{}, nil
}

// durableDeliveryJobs positively declares itself durable — production accepts it.
type durableDeliveryJobs struct{ stubDeliveryJobs }

func (durableDeliveryJobs) Durability() deliveryjob.Durability { return deliveryjob.Durability{} }

// inProcessDeliveryJobs positively declares itself in-process-only — production
// rejects it (ErrNonDurableDeliveryRepository).
type inProcessDeliveryJobs struct{ stubDeliveryJobs }

func (inProcessDeliveryJobs) Durability() deliveryjob.Durability {
	return deliveryjob.Durability{InProcessOnly: true}
}

// durableLimiter is a shared/durable rate-limiter double: it positively declares
// itself durable through RateLimiterDurabilityReporter, so production construction
// accepts it (it stands in for a Redis-backed limiter). Its Allow always permits —
// the construction tests assert wiring, not throttling.
type durableLimiter struct{}

func (durableLimiter) Allow(context.Context, string, ratelimiter.Limit) (ratelimiter.Result, error) {
	return ratelimiter.Result{Allowed: true}, nil
}
func (durableLimiter) Reset(context.Context, string) error      { return nil }
func (durableLimiter) Close() error                             { return nil }
func (durableLimiter) RateLimiterDurability() LimiterDurability { return LimiterDurability{} }

// inProcessLimiter is a custom limiter that positively declares itself
// in-process-only — production rejects it (ErrNonDurableRateLimiter) exactly like
// the bundled ratelimiter.Memory default.
type inProcessLimiter struct{ durableLimiter }

func (inProcessLimiter) RateLimiterDurability() LimiterDurability {
	return LimiterDurability{InProcessOnly: true}
}

// prodKeyer is a production-capable identifier keyer double: it satisfies
// IdentifierKeyer so production construction (which requires the keyer) is
// satisfied. The digest shape is irrelevant here — PII-freeness of the login key is
// proven in the authsvc limiter tests.
type prodKeyer struct{}

func (prodKeyer) IdentifierKey(kind, normalizedValue string) string { return "k:" + kind }

// TestNewServiceRuntimeModeRequired proves an empty RuntimeMode is rejected so a
// host cannot accidentally inherit the development posture (design §8).
func TestNewServiceRuntimeModeRequired(t *testing.T) {
	_, err := NewService(Repositories{}, Config{Hasher: stubHasher{}, Mailer: stubMailer{}, TokenSigner: stubSigner{}})
	if !errors.Is(err, ErrRuntimeModeRequired) {
		t.Errorf("empty RuntimeMode: err=%v, want ErrRuntimeModeRequired", err)
	}
}

// TestNewServiceChallengeProtectorRequired proves the enable-time rule (design
// §3.3): wiring the Challenges repository enables the atomic secret rail, which
// requires a ChallengeProtector — nil is rejected with ErrChallengeProtectorRequired.
func TestNewServiceChallengeProtectorRequired(t *testing.T) {
	_, err := NewService(Repositories{Challenges: stubChallenges{}}, Config{
		Hasher:      stubHasher{},
		Mailer:      stubMailer{},
		TokenSigner: stubSigner{},
		RuntimeMode: RuntimeModeDevelopment,
	})
	if !errors.Is(err, ErrChallengeProtectorRequired) {
		t.Errorf("Challenges wired without protector: err=%v, want ErrChallengeProtectorRequired", err)
	}
}

// TestNewServiceChallengeSubsystemWiring proves the challenge slot is deny-by-
// absence: a nil Challenges repository tolerates a nil protector, and wiring both
// together constructs cleanly.
func TestNewServiceChallengeSubsystemWiring(t *testing.T) {
	base := Config{Hasher: stubHasher{}, Mailer: stubMailer{}, TokenSigner: stubSigner{}, RuntimeMode: RuntimeModeDevelopment}
	if _, err := NewService(Repositories{}, base); err != nil {
		t.Errorf("challenge off (nil repo, nil protector): err=%v, want nil", err)
	}
	protector, err := NewHMACChallengeProtector(HMACKeyRing{
		Active: "2026-01",
		Keys:   map[string][]byte{"2026-01": make([]byte, 32)},
	})
	if err != nil {
		t.Fatalf("NewHMACChallengeProtector: %v", err)
	}
	withProtector := base
	withProtector.ChallengeProtector = protector
	if _, err := NewService(Repositories{Challenges: stubChallenges{}}, withProtector); err != nil {
		t.Errorf("challenge on (repo + protector): err=%v, want nil", err)
	}
}

// TestNewServiceRuntimeModeInvalid proves an unknown RuntimeMode is rejected.
func TestNewServiceRuntimeModeInvalid(t *testing.T) {
	_, err := NewService(Repositories{}, Config{Hasher: stubHasher{}, Mailer: stubMailer{}, TokenSigner: stubSigner{}, RuntimeMode: "staging"})
	if !errors.Is(err, ErrRuntimeModeInvalid) {
		t.Errorf("unknown RuntimeMode: err=%v, want ErrRuntimeModeInvalid", err)
	}
}

// TestNewServiceRuntimeModeCheckedAfterRequiredCollaborators proves RuntimeMode
// validation does not mask the pre-existing required-collaborator errors (nil
// Hasher/Mailer/TokenSigner still report their own errors first).
func TestNewServiceRuntimeModeCheckedAfterRequiredCollaborators(t *testing.T) {
	if _, err := NewService(Repositories{}, Config{}); !errors.Is(err, ErrHasherRequired) {
		t.Errorf("nil Hasher with empty mode: err=%v, want ErrHasherRequired", err)
	}
	if _, err := NewService(Repositories{}, Config{Hasher: stubHasher{}, Mailer: stubMailer{}}); !errors.Is(err, ErrTokenSignerRequired) {
		t.Errorf("nil signer with empty mode: err=%v, want ErrTokenSignerRequired", err)
	}
}

// TestNewServiceProductionRejectsConsoleEmail proves the console email sender is
// rejected in production RuntimeMode (design §6.3): it leaks message bodies.
func TestNewServiceProductionRejectsConsoleEmail(t *testing.T) {
	_, err := NewService(Repositories{}, Config{
		Hasher:      stubHasher{},
		Mailer:      email.NewConsole(nil),
		TokenSigner: stubSigner{},
		RuntimeMode: RuntimeModeProduction,
	})
	if !errors.Is(err, ErrInsecureDeliveryTransport) {
		t.Errorf("console email in production: err=%v, want ErrInsecureDeliveryTransport", err)
	}
}

// TestNewServiceProductionRejectsMetadatalessEmail proves an email Sender that
// declares no capability metadata is rejected in production (cannot be proven
// safe). stubMailer implements no CapabilityReporter.
func TestNewServiceProductionRejectsMetadatalessEmail(t *testing.T) {
	_, err := NewService(Repositories{}, Config{
		Hasher:      stubHasher{},
		Mailer:      stubMailer{},
		TokenSigner: stubSigner{},
		RuntimeMode: RuntimeModeProduction,
	})
	if !errors.Is(err, ErrInsecureDeliveryTransport) {
		t.Errorf("metadata-less email in production: err=%v, want ErrInsecureDeliveryTransport", err)
	}
}

// TestNewServiceProductionRejectsConsoleNotifier proves a development-only
// notifier is rejected in production even when the Mailer is production-capable.
func TestNewServiceProductionRejectsConsoleNotifier(t *testing.T) {
	_, err := NewService(Repositories{}, Config{
		Hasher:      stubHasher{},
		Mailer:      prodMailer{},
		TokenSigner: stubSigner{},
		RuntimeMode: RuntimeModeProduction,
		Notifiers:   []notify.Notifier{notify.NewConsole(identity.KindPhone, nil)},
	})
	if !errors.Is(err, ErrInsecureDeliveryTransport) {
		t.Errorf("console notifier in production: err=%v, want ErrInsecureDeliveryTransport", err)
	}
}

// TestNewServiceProductionAcceptsDeclaredTransports proves production
// construction succeeds when every delivery transport declares metadata and is
// not development-only.
func TestNewServiceProductionAcceptsDeclaredTransports(t *testing.T) {
	svc, err := NewService(Repositories{}, Config{
		Hasher:          stubHasher{},
		Mailer:          prodMailer{},
		TokenSigner:     stubSigner{},
		RuntimeMode:     RuntimeModeProduction,
		Notifiers:       []notify.Notifier{prodNotifier{"sms"}},
		RateLimiter:     durableLimiter{},
		IdentifierKeyer: prodKeyer{},
	})
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
	_, err := NewService(Repositories{}, Config{
		Hasher:      stubHasher{},
		Mailer:      email.NewConsole(nil),
		TokenSigner: stubSigner{},
		RuntimeMode: RuntimeModeDevelopment,
		Logger:      log,
	})
	if err != nil {
		t.Fatalf("console email in development: err=%v, want nil", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("development-only delivery transport")) {
		t.Errorf("expected a development-only transport WARN, got: %s", buf.String())
	}
}

// prodDeliveryConfig is a production Config with declared transports, an outbox
// encrypter, and the worker acknowledgment — every delivery gate satisfied EXCEPT
// the repository the test supplies. It isolates one failing dimension per case.
func prodDeliveryConfig() Config {
	return Config{
		Hasher:                     stubHasher{},
		Mailer:                     prodMailer{},
		TokenSigner:                stubSigner{},
		RuntimeMode:                RuntimeModeProduction,
		DeliveryEncrypter:          stubEncrypter{},
		DeliveryWorkerAcknowledged: true,
		// The always-on production requirements (AV3-5.4): a shared/durable limiter
		// and the PII-free identifier keyer, so these delivery-focused cases isolate
		// their own failing dimension rather than tripping the rate-limit gates.
		RateLimiter:     durableLimiter{},
		IdentifierKeyer: prodKeyer{},
	}
}

// TestNewServiceProductionRejectsNonDurableDeliveryRepository proves production
// rejects a delivery repository that identifies itself as in-process-only (design
// §8): a non-durable outbox silently drops delivery on restart.
func TestNewServiceProductionRejectsNonDurableDeliveryRepository(t *testing.T) {
	_, err := NewService(Repositories{DeliveryJobs: inProcessDeliveryJobs{}}, prodDeliveryConfig())
	if !errors.Is(err, ErrNonDurableDeliveryRepository) {
		t.Errorf("in-process delivery repo in production: err=%v, want ErrNonDurableDeliveryRepository", err)
	}
}

// TestNewServiceProductionAcceptsDurableDeliveryRepository proves a repository that
// declares itself durable constructs cleanly in production.
func TestNewServiceProductionAcceptsDurableDeliveryRepository(t *testing.T) {
	svc, err := NewService(Repositories{DeliveryJobs: durableDeliveryJobs{}}, prodDeliveryConfig())
	if err != nil {
		t.Fatalf("durable delivery repo in production: err=%v, want nil", err)
	}
	if svc == nil {
		t.Fatal("NewService returned nil Service")
	}
}

// TestNewServiceProductionToleratesMetadatalessDeliveryRepository proves the "where
// metadata can identify it" rule (design §8): a repository declaring NO durability
// metadata (a real store — pgx/turso — implements no DurabilityReporter) is
// tolerated in production rather than asked to prove a negative.
func TestNewServiceProductionToleratesMetadatalessDeliveryRepository(t *testing.T) {
	if _, err := NewService(Repositories{DeliveryJobs: stubDeliveryJobs{}}, prodDeliveryConfig()); err != nil {
		t.Errorf("metadata-less delivery repo in production: err=%v, want nil", err)
	}
}

// TestNewServiceProductionRequiresDeliveryWorkerAcknowledgment proves production
// refuses a wired outbox unless the host affirms it runs the worker (design §8):
// the outbox is the only send path, so an unacknowledged worker would swallow every
// message.
func TestNewServiceProductionRequiresDeliveryWorkerAcknowledgment(t *testing.T) {
	cfg := prodDeliveryConfig()
	cfg.DeliveryWorkerAcknowledged = false
	_, err := NewService(Repositories{DeliveryJobs: durableDeliveryJobs{}}, cfg)
	if !errors.Is(err, ErrDeliveryWorkerUnacknowledged) {
		t.Errorf("unacknowledged worker in production: err=%v, want ErrDeliveryWorkerUnacknowledged", err)
	}
}

// TestNewServiceProductionMissingEncrypterBeforeDurability proves the missing-
// encrypter error fires before the durability and worker checks (ordering), so a
// nil encrypter is reported as ErrDeliveryEncrypterRequired even for a non-durable
// repo with no acknowledgment.
func TestNewServiceProductionMissingEncrypterBeforeDurability(t *testing.T) {
	cfg := prodDeliveryConfig()
	cfg.DeliveryEncrypter = nil
	cfg.DeliveryWorkerAcknowledged = false
	_, err := NewService(Repositories{DeliveryJobs: inProcessDeliveryJobs{}}, cfg)
	if !errors.Is(err, ErrDeliveryEncrypterRequired) {
		t.Errorf("nil encrypter with outbox wired: err=%v, want ErrDeliveryEncrypterRequired", err)
	}
}

// TestNewServiceDevelopmentToleratesNonDurableOutbox proves development enforces
// neither durability nor the worker acknowledgment: an in-process outbox with no
// acknowledgment constructs cleanly, while the encrypted-payload requirement still
// holds (the encrypter is wired here).
func TestNewServiceDevelopmentToleratesNonDurableOutbox(t *testing.T) {
	_, err := NewService(Repositories{DeliveryJobs: inProcessDeliveryJobs{}}, Config{
		Hasher:            stubHasher{},
		Mailer:            stubMailer{},
		TokenSigner:       stubSigner{},
		RuntimeMode:       RuntimeModeDevelopment,
		DeliveryEncrypter: stubEncrypter{},
	})
	if err != nil {
		t.Errorf("in-process outbox in development: err=%v, want nil", err)
	}
}

// TestNewServiceDevelopmentOutboxStillRequiresEncrypter proves the encrypted-
// payload requirement is mode-independent: development with a wired outbox and no
// encrypter is still ErrDeliveryEncrypterRequired (design §8 — development permits
// console but still requires encrypted job payloads).
func TestNewServiceDevelopmentOutboxStillRequiresEncrypter(t *testing.T) {
	_, err := NewService(Repositories{DeliveryJobs: durableDeliveryJobs{}}, Config{
		Hasher:      stubHasher{},
		Mailer:      stubMailer{},
		TokenSigner: stubSigner{},
		RuntimeMode: RuntimeModeDevelopment,
	})
	if !errors.Is(err, ErrDeliveryEncrypterRequired) {
		t.Errorf("dev outbox without encrypter: err=%v, want ErrDeliveryEncrypterRequired", err)
	}
}

// prodLimiterConfig is a production Config with declared transports and the PII-free
// keyer satisfied — every gate green EXCEPT the rate limiter each case supplies, so
// the limiter dimension is isolated.
func prodLimiterConfig() Config {
	return Config{
		Hasher:          stubHasher{},
		Mailer:          prodMailer{},
		TokenSigner:     stubSigner{},
		RuntimeMode:     RuntimeModeProduction,
		IdentifierKeyer: prodKeyer{},
	}
}

// TestNewServiceProductionRejectsDefaultMemoryLimiter proves production rejects a
// nil RateLimiter (design §4.4/§8): the feature defaults it to the in-process
// ratelimiter.Memory, whose budget is per-process and would be N× across instances.
func TestNewServiceProductionRejectsDefaultMemoryLimiter(t *testing.T) {
	cfg := prodLimiterConfig() // RateLimiter left nil → in-process memory default
	_, err := NewService(Repositories{}, cfg)
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
	_, err := NewService(Repositories{}, cfg)
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
	_, err := NewService(Repositories{}, cfg)
	if !errors.Is(err, ErrNonDurableRateLimiter) {
		t.Errorf("declared in-process limiter in production: err=%v, want ErrNonDurableRateLimiter", err)
	}
}

// TestNewServiceProductionAcceptsDurableLimiter proves a limiter declaring itself
// durable/shared constructs cleanly in production.
func TestNewServiceProductionAcceptsDurableLimiter(t *testing.T) {
	cfg := prodLimiterConfig()
	cfg.RateLimiter = durableLimiter{}
	svc, err := NewService(Repositories{}, cfg)
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
	_, err := NewService(Repositories{}, cfg)
	if !errors.Is(err, ErrIdentifierKeyerRequired) {
		t.Errorf("production without identifier keyer: err=%v, want ErrIdentifierKeyerRequired", err)
	}
}

// TestNewServiceDevelopmentWarnsOnMemoryLimiter proves the in-process memory limiter
// is permitted in development but emits the multi-instance startup WARN (design §4.4).
func TestNewServiceDevelopmentWarnsOnMemoryLimiter(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	_, err := NewService(Repositories{}, Config{
		Hasher:      stubHasher{},
		Mailer:      stubMailer{},
		TokenSigner: stubSigner{},
		RuntimeMode: RuntimeModeDevelopment,
		Logger:      log,
	})
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
	if _, err := NewService(Repositories{}, Config{
		Hasher:      stubHasher{},
		Mailer:      stubMailer{},
		TokenSigner: stubSigner{},
		RuntimeMode: RuntimeModeDevelopment,
		RateLimiter: durableLimiter{},
	}); err != nil {
		t.Errorf("development without identifier keyer: err=%v, want nil", err)
	}
}

// ---------------------------------------------------------------------------
// AV3-7.1 — passwordless enablement construction matrix (design §4.1/§4.2/§6.4/§8)
// ---------------------------------------------------------------------------

// passwordlessProtector builds an HMAC challenge protector for the passwordless
// enablement tests — wiring Repositories.Challenges REQUIRES one.
func passwordlessProtector(t *testing.T) ChallengeProtector {
	t.Helper()
	p, err := NewHMACChallengeProtector(HMACKeyRing{
		Active: "2026-01",
		Keys:   map[string][]byte{"2026-01": make([]byte, 32)},
	})
	if err != nil {
		t.Fatalf("NewHMACChallengeProtector: %v", err)
	}
	return p
}

// passwordlessDevRepos is the fully-wired repository set a passwordless-enabled host
// needs: the atomic challenge rail plus the durable delivery outbox.
func passwordlessDevRepos() Repositories {
	return Repositories{Challenges: stubChallenges{}, DeliveryJobs: durableDeliveryJobs{}}
}

// passwordlessDevConfig is a development Config with every passwordless enablement
// gate satisfied for the email kind: the challenge protector, the outbox encrypter,
// and an absolute PublicAuthBaseURL. Cases override one dimension to isolate a single
// failing gate.
func passwordlessDevConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		Hasher:             stubHasher{},
		Mailer:             stubMailer{},
		TokenSigner:        stubSigner{},
		RuntimeMode:        RuntimeModeDevelopment,
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
	svc, err := NewService(Repositories{}, Config{
		Hasher:      stubHasher{},
		Mailer:      stubMailer{},
		TokenSigner: stubSigner{},
		RuntimeMode: RuntimeModeDevelopment,
	})
	if err != nil {
		t.Fatalf("no passwordless: err=%v, want nil", err)
	}
	if svc.svc.PasswordlessEnabled() {
		t.Error("PasswordlessEnabled() = true with empty Config.Passwordless, want false")
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
		mutate  func(*Config)
		wantErr error
		enabled bool // asserted only when wantErr is nil
	}{
		{
			name:  "email fully wired",
			repos: passwordlessDevRepos,
			mutate: func(c *Config) {
				c.Passwordless = []string{"email"}
			},
			enabled: true,
		},
		{
			name:  "invalid kind rejected",
			repos: passwordlessDevRepos,
			mutate: func(c *Config) {
				c.Passwordless = []string{"push"}
			},
			wantErr: ErrPasswordlessKindInvalid,
		},
		{
			name:  "phone without notifier unsupported",
			repos: passwordlessDevRepos,
			mutate: func(c *Config) {
				c.Passwordless = []string{"phone"}
			},
			wantErr: ErrPasswordlessKindUnsupported,
		},
		{
			name:  "phone with notifier wired",
			repos: passwordlessDevRepos,
			mutate: func(c *Config) {
				c.Passwordless = []string{"phone"}
				c.Notifiers = []notify.Notifier{prodNotifier{"phone"}}
			},
			enabled: true,
		},
		{
			name:  "email and phone both enabled",
			repos: passwordlessDevRepos,
			mutate: func(c *Config) {
				c.Passwordless = []string{"email", "phone"}
				c.Notifiers = []notify.Notifier{prodNotifier{"phone"}}
			},
			enabled: true,
		},
		{
			name:  "challenge rail absent",
			repos: func() Repositories { return Repositories{DeliveryJobs: durableDeliveryJobs{}} },
			mutate: func(c *Config) {
				c.Passwordless = []string{"email"}
				c.ChallengeProtector = nil // no challenge rail wired
			},
			wantErr: ErrPasswordlessChallengeRequired,
		},
		{
			name:  "delivery outbox absent",
			repos: func() Repositories { return Repositories{Challenges: stubChallenges{}} },
			mutate: func(c *Config) {
				c.Passwordless = []string{"email"}
			},
			wantErr: ErrPasswordlessDeliveryRequired,
		},
		{
			name:  "public base url absent",
			repos: passwordlessDevRepos,
			mutate: func(c *Config) {
				c.Passwordless = []string{"email"}
				c.PublicAuthBaseURL = ""
			},
			wantErr: ErrPublicAuthBaseURLRequired,
		},
		{
			name:  "public base url not absolute",
			repos: passwordlessDevRepos,
			mutate: func(c *Config) {
				c.Passwordless = []string{"email"}
				c.PublicAuthBaseURL = "/auth"
			},
			wantErr: ErrPublicAuthBaseURLInvalid,
		},
		{
			name:  "development permits http base url",
			repos: passwordlessDevRepos,
			mutate: func(c *Config) {
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
			svc, err := NewService(tc.repos(), cfg)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err=%v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("err=%v, want nil", err)
			}
			if got := svc.svc.PasswordlessEnabled(); got != tc.enabled {
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
	svc, err := NewService(passwordlessDevRepos(), cfg)
	if err != nil {
		t.Fatalf("email passwordless: err=%v, want nil", err)
	}
	if !svc.svc.PasswordlessKindEnabled("email") {
		t.Error("PasswordlessKindEnabled(email) = false, want true")
	}
	if svc.svc.PasswordlessKindEnabled("phone") {
		t.Error("PasswordlessKindEnabled(phone) = true, want false (not listed)")
	}
}

// TestNewServiceProductionPasswordlessRejectsHTTPBaseURL proves production rejects a
// non-HTTPS magic-link base URL (design §6.4): a link over plain HTTP exposes the
// single-use token in transit. Every other production gate is satisfied so the base
// URL is the dimension under test.
func TestNewServiceProductionPasswordlessRejectsHTTPBaseURL(t *testing.T) {
	_, err := NewService(passwordlessDevRepos(), Config{
		Hasher:                     stubHasher{},
		Mailer:                     prodMailer{},
		TokenSigner:                stubSigner{},
		RuntimeMode:                RuntimeModeProduction,
		ChallengeProtector:         passwordlessProtector(t),
		DeliveryEncrypter:          stubEncrypter{},
		DeliveryWorkerAcknowledged: true,
		RateLimiter:                durableLimiter{},
		IdentifierKeyer:            prodKeyer{},
		PublicAuthBaseURL:          "http://auth.example.com",
		Passwordless:               []string{"email"},
	})
	if !errors.Is(err, ErrPublicAuthBaseURLInsecure) {
		t.Errorf("http base url in production: err=%v, want ErrPublicAuthBaseURLInsecure", err)
	}
}

// TestNewServiceProductionPasswordlessAcceptsFullWiring proves a fully-wired
// production passwordless config constructs cleanly: production-capable transports,
// a durable limiter + identifier keyer, an acknowledged worker, an HTTPS base URL,
// and the challenge + outbox rails all satisfied (design §4.2/§8).
func TestNewServiceProductionPasswordlessAcceptsFullWiring(t *testing.T) {
	svc, err := NewService(passwordlessDevRepos(), Config{
		Hasher:                     stubHasher{},
		Mailer:                     prodMailer{},
		TokenSigner:                stubSigner{},
		RuntimeMode:                RuntimeModeProduction,
		ChallengeProtector:         passwordlessProtector(t),
		DeliveryEncrypter:          stubEncrypter{},
		DeliveryWorkerAcknowledged: true,
		RateLimiter:                durableLimiter{},
		IdentifierKeyer:            prodKeyer{},
		Notifiers:                  []notify.Notifier{prodNotifier{"phone"}},
		PublicAuthBaseURL:          "https://auth.example.com",
		Passwordless:               []string{"email", "phone"},
	})
	if err != nil {
		t.Fatalf("full production passwordless wiring: err=%v, want nil", err)
	}
	if !svc.svc.PasswordlessEnabled() {
		t.Error("PasswordlessEnabled() = false after full production wiring, want true")
	}
}

// TestNewServiceProductionPasswordlessRejectsConsolePhone proves the passwordless
// phone channel must be production-capable in production (design §4.2/§6.3): a
// development-only console notifier standing in for the phone transport is rejected
// by the always-on transport check, so passwordless cannot silently enable an
// insecure channel.
func TestNewServiceProductionPasswordlessRejectsConsolePhone(t *testing.T) {
	_, err := NewService(passwordlessDevRepos(), Config{
		Hasher:                     stubHasher{},
		Mailer:                     prodMailer{},
		TokenSigner:                stubSigner{},
		RuntimeMode:                RuntimeModeProduction,
		ChallengeProtector:         passwordlessProtector(t),
		DeliveryEncrypter:          stubEncrypter{},
		DeliveryWorkerAcknowledged: true,
		RateLimiter:                durableLimiter{},
		IdentifierKeyer:            prodKeyer{},
		Notifiers:                  []notify.Notifier{notify.NewConsole(identity.KindPhone, nil)},
		PublicAuthBaseURL:          "https://auth.example.com",
		Passwordless:               []string{"phone"},
	})
	if !errors.Is(err, ErrInsecureDeliveryTransport) {
		t.Errorf("console phone notifier in production: err=%v, want ErrInsecureDeliveryTransport", err)
	}
}
