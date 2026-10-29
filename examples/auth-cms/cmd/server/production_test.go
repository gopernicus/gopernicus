package main

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/gopernicus/gopernicus/examples/auth-cms/pockets/authentication/outbound/authmem"
	gowebauthn "github.com/gopernicus/gopernicus/integrations/cryptids/go-webauthn"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	authenticationlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	delivery "github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify/email"
	"github.com/gopernicus/gopernicus/sdk/capabilities/oauth"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	environment "github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// productionKeys are distinct disposable hex fixture keys for the AUTH_* key
// variables (AUTH_TOTP_KEYS a one-key ring beside AUTH_TOTP_ACTIVE_KEY);
// production refuses to mint any of them.
var productionKeys = map[auth.KeyName]string{
	auth.KeyJWT:               strings.Repeat("a1", 32),
	auth.KeyChallengePepper:   strings.Repeat("b2", 32),
	auth.KeyIdentifier:        strings.Repeat("c3", 32),
	auth.KeyDeliveryEncrypter: strings.Repeat("d4", 32),
	auth.KeyTokenEncrypter:    strings.Repeat("e5", 32),
	auth.KeyTOTPRing:          "prod:" + strings.Repeat("f6", 32),
	"AUTH_TOTP_ACTIVE_KEY":    "prod",
}

// prodSender is a production-capable email transport double: it declares
// mandatory TLS and no development-only flag, so the production transport gate
// accepts it (unlike the console sender the host wires by default).
type prodSender struct{}

func (prodSender) Send(context.Context, email.Message) error { return nil }
func (prodSender) Capabilities() notify.Capabilities {
	return notify.Capabilities{TransportSecurity: notify.TransportSecurityTLS, DevelopmentOnly: false}
}

// starttlsSender is a relay that offers only opportunistic STARTTLS, which
// production refuses unless the host acknowledges it.
type starttlsSender struct{}

func (starttlsSender) Send(context.Context, email.Message) error { return nil }
func (starttlsSender) Capabilities() notify.Capabilities {
	return notify.Capabilities{TransportSecurity: notify.TransportSecurityStartTLS}
}

// prodNotifier is a production-capable phone Notifier double: it declares
// non-development-only metadata so phone passwordless has a production transport in
// the valid baseline.
type prodNotifier struct{ kind string }

func (n prodNotifier) Kind() string                             { return n.kind }
func (prodNotifier) Send(context.Context, string, string) error { return nil }
func (prodNotifier) Capabilities() notify.Capabilities {
	return notify.Capabilities{TransportSecurity: notify.TransportSecurityTLS, DevelopmentOnly: false}
}

// durableLimiter is a rate-limiter double that positively declares itself
// shared/durable, so the production non-durable-limiter gate accepts it (unlike the
// in-process ratelimiter.Memory the pocket defaults a nil RateLimiter to).
type durableLimiter struct{}

func (durableLimiter) Allow(context.Context, string, ratelimiter.Limit) (ratelimiter.Result, error) {
	return ratelimiter.Result{Allowed: true}, nil
}
func (durableLimiter) Reset(context.Context, string) error { return nil }
func (durableLimiter) RateLimiterDurability() authenticationlogic.LimiterDurability {
	return authenticationlogic.LimiterDurability{InProcessOnly: false}
}

// setProductionKeys sets the key variables for the test.
func setProductionKeys(t *testing.T) {
	t.Helper()
	for name, value := range productionKeys {
		t.Setenv(string(name), value)
	}
}

// productionBaseline returns a production-VALID config + repositories: every v3
// safeguard the memory host would otherwise fail is satisfied (the five keys set,
// production transports, a durable limiter, an HTTPS magic-link base, the
// generic-jobs delivery dispatcher, an acknowledged runtime, no fake OAuth
// provider, and single-factor native login acknowledged). buildAuthConfig runs in
// production, so NewKeys decodes the keys rather than minting them. It constructs
// cleanly in production (proven by TestProductionBaselineConstructs), so each
// negative below isolates exactly one broken safeguard.
func productionBaseline(t *testing.T) (authenticationConfig, auth.Repositories) {
	t.Helper()
	setProductionKeys(t)
	cfg, err := buildAuthConfig(quietLog(), environment.ModeProduction, nil)
	if err != nil {
		t.Fatalf("buildAuthConfig: %v", err)
	}
	cfg.SessionCookie.Secure = true
	cfg.OAuthCallbackBase = "https://auth.example.com"
	cfg.Mailer = prodSender{}
	cfg.BodySenders = map[string]delivery.BodySender{sdk.AddressKindPhone: prodNotifier{kind: sdk.AddressKindPhone}}
	cfg.RateLimiter = durableLimiter{}
	cfg.PublicAuthBaseURL = "https://auth.example.com/auth/magic"
	// Production requires an HTTPS reset landing route; the host's own
	// default is derived from callbackBase(), which is plain http in the demo.
	cfg.PasswordResetURL = "https://auth.example.com/auth/password/reset"
	// run() wires the generic-jobs dispatcher; reproduce it so jobs mode has its queue
	// capability in the baseline.
	cfg.DeliveryDispatcher = jobsDispatcher(t)
	// The fake provider declares itself development-only; production refuses it.
	cfg.Providers = nil
	// Passwords and passwordless are one-factor; production asks for the host's
	// explicit acceptance.
	cfg.SingleFactorAcknowledged = true
	// Production refuses a localhost RP ID; this baseline serves a real domain.
	cfg.Passkeys.RelyingParty = productionRelyingParty(t)

	repos := authmem.New().Repositories()
	return cfg, repos
}

// productionRelyingParty is the go-webauthn relying party of a real domain,
// which production admits.
func productionRelyingParty(t *testing.T) *gowebauthn.RelyingParty {
	t.Helper()
	rp, err := passkeyRelyingParty("https://auth.example.com", []string{"https://auth.example.com"})
	if err != nil {
		t.Fatalf("passkeyRelyingParty: %v", err)
	}
	return rp
}

// TestPasskeyRelyingPartyInProduction pins the production refusal over the
// go-webauthn relying party: a real domain constructs, the development
// localhost RP buildAuthConfig derives is refused.
func TestPasskeyRelyingPartyInProduction(t *testing.T) {
	for name, tc := range map[string]struct {
		origin  string
		refused bool
	}{
		"a real domain": {"https://auth.example.com", false},
		"localhost":     {"http://localhost:8082", true},
	} {
		cfg, repos := productionBaseline(t)
		rp, err := passkeyRelyingParty(tc.origin, []string{tc.origin})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		cfg.Passkeys.RelyingParty = rp
		_, err = auth.New(repos, cfg.TokenSigner, environment.ModeProduction, cfg.DeliveryMode, cfg.options()...)
		if got := errors.Is(err, auth.ErrPasskeyDevelopmentRelyingParty); got != tc.refused || (!tc.refused && err != nil) {
			t.Errorf("%s: err = %v, refused %v want %v", name, err, got, tc.refused)
		}
	}
}

// TestProductionBaselineConstructs proves the production-valid baseline actually
// constructs: without it, the negative matrix below could pass vacuously (every case
// erroring for the wrong reason). It is the positive control for the fail-closed
// matrix.
func TestProductionBaselineConstructs(t *testing.T) {
	cfg, repos := productionBaseline(t)
	if cfg.RuntimeMode != environment.ModeProduction {
		t.Fatalf("baseline RuntimeMode = %q, want production", cfg.RuntimeMode)
	}
	if _, err := auth.New(repos, cfg.TokenSigner, cfg.RuntimeMode, cfg.DeliveryMode, cfg.options()...); err != nil {
		t.Fatalf("production-valid baseline should construct, got: %v", err)
	}
}

// TestProductionNegatives is the fail-closed production matrix (design §6.3/§8, V15):
// starting from the valid baseline, each case breaks exactly ONE safeguard and
// asserts construction is rejected with the matching stable error. This proves the
// in-memory proof host — the exact wiring run() ships — CANNOT be flipped to
// production while any single safeguard is unmet.
func TestProductionNegatives(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(cfg *authenticationConfig, repos *auth.Repositories)
		want   error
	}{
		{
			// console: the host's dev email + phone transports leak OTPs/magic links to
			// logs, so production rejects them.
			name: "console_delivery_transports",
			mutate: func(cfg *authenticationConfig, _ *auth.Repositories) {
				cfg.Mailer = email.NewConsole(quietLog())
				cfg.BodySenders = map[string]delivery.BodySender{sdk.AddressKindPhone: notify.NewConsole(quietLog())}
			},
			want: auth.ErrInsecureDeliveryTransport,
		},
		{
			// insecure URL (the Views magic-link landing over http): a plain-http
			// public base exposes the single-use token in transit. This is the unsafe
			// Views/public-URL combination — the bundled magic-link page is wired, so
			// its base URL must be HTTPS in production.
			name: "insecure_http_public_auth_base_url",
			mutate: func(cfg *authenticationConfig, _ *auth.Repositories) {
				cfg.PublicAuthBaseURL = "http://auth.example.com/auth/magic"
			},
			want: auth.ErrPublicAuthBaseURLInsecure,
		},
		{
			// memory limiter: a nil RateLimiter defaults to the in-process
			// ratelimiter.Memory, which enforces only a per-process budget.
			name: "in_process_memory_rate_limiter",
			mutate: func(cfg *authenticationConfig, _ *auth.Repositories) {
				cfg.RateLimiter = nil
			},
			want: auth.ErrNonDurableRateLimiter,
		},
		{
			// keyer: PII-free rate-limit/idempotency keys need the shared HMAC keyer in
			// production so one identifier maps to one bucket across instances.
			name: "missing_identifier_keyer",
			mutate: func(cfg *authenticationConfig, _ *auth.Repositories) {
				cfg.IdentifierKeyer = nil
			},
			want: auth.ErrIdentifierKeyerRequired,
		},
		{
			// runtime: the queue is the only send path, so production requires the host
			// to affirm it runs the generic-jobs delivery runtime (jobs.FencedRuntime).
			name: "unacknowledged_delivery_runtime",
			mutate: func(cfg *authenticationConfig, _ *auth.Repositories) {
				cfg.DeliveryJobsAcknowledged = false
			},
			want: auth.ErrDeliveryJobsUnacknowledged,
		},
		{
			// the host's own fake provider signs in whoever names a code.
			name: "fake_oauth_provider",
			mutate: func(cfg *authenticationConfig, _ *auth.Repositories) {
				cfg.Providers = []oauth.Provider{fakeOAuthProvider{}}
			},
			want: auth.ErrDevelopmentOnlyProvider,
		},
		{
			// no security-event audit rail and no acknowledgement.
			name: "nil_security_events",
			mutate: func(_ *authenticationConfig, repos *auth.Repositories) {
				repos.SecurityEvents = nil
			},
			want: auth.ErrAuditRequired,
		},
		{
			// passwords and passwordless stay on without the acknowledgement.
			name: "single_factor",
			mutate: func(cfg *authenticationConfig, _ *auth.Repositories) {
				cfg.SingleFactorAcknowledged = false
			},
			want: auth.ErrSingleFactorUnacknowledged,
		},
		{
			// an opportunistic-STARTTLS relay without the acknowledgement.
			name: "starttls_mailer",
			mutate: func(cfg *authenticationConfig, _ *auth.Repositories) {
				cfg.Mailer = starttlsSender{}
			},
			want: auth.ErrInsecureDeliveryTransport,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, repos := productionBaseline(t)
			tc.mutate(&cfg, &repos)
			_, err := auth.New(repos, cfg.TokenSigner, cfg.RuntimeMode, cfg.DeliveryMode, cfg.options()...)
			if !errors.Is(err, tc.want) {
				t.Fatalf("production %s: got err %v, want %v", tc.name, err, tc.want)
			}
		})
	}
}

// TestProductionStartTLSAcknowledgedConstructs proves the acknowledgement admits a
// STARTTLS relay end to end through authentication.New — the root check and the
// delivery router's ValidateTransports both honor it.
func TestProductionStartTLSAcknowledgedConstructs(t *testing.T) {
	cfg, repos := productionBaseline(t)
	cfg.Mailer = starttlsSender{}
	cfg.OpportunisticTLSAcknowledged = true
	if _, err := auth.New(repos, cfg.TokenSigner, cfg.RuntimeMode, cfg.DeliveryMode, cfg.options()...); err != nil {
		t.Fatalf("acknowledged STARTTLS relay in production: %v", err)
	}
}

// TestProductionRefusesUnsetKeys proves production never mints a key: each
// needed AUTH_* key variable unset is reported by name, all in one error, and the
// optional provider-token key is not among them.
func TestProductionRefusesUnsetKeys(t *testing.T) {
	needed := []auth.KeyName{auth.KeyJWT, auth.KeyChallengePepper, auth.KeyIdentifier, auth.KeyDeliveryEncrypter, auth.KeyTOTPRing}
	for _, missing := range needed {
		t.Run(string(missing), func(t *testing.T) {
			setProductionKeys(t)
			t.Setenv(string(missing), "")
			_, err := buildAuthConfig(quietLog(), environment.ModeProduction, nil)
			if !errors.Is(err, auth.ErrKeyRequired) || !strings.Contains(err.Error(), string(missing)) {
				t.Fatalf("err = %v, want ErrKeyRequired naming %s", err, missing)
			}
		})
	}
	t.Run("all", func(t *testing.T) {
		for name := range productionKeys {
			t.Setenv(string(name), "")
		}
		_, err := buildAuthConfig(quietLog(), environment.ModeProduction, nil)
		if !errors.Is(err, auth.ErrKeyRequired) {
			t.Fatalf("err = %v, want ErrKeyRequired", err)
		}
		for _, name := range needed {
			if !strings.Contains(err.Error(), string(name)) {
				t.Errorf("joined error does not name %s: %v", name, err)
			}
		}
		if strings.Contains(err.Error(), string(auth.KeyTokenEncrypter)) {
			t.Errorf("the optional token key was reported: %v", err)
		}
	})
}

// TestAuthDebugRefusedOutsideDevelopment proves AUTH_DEBUG=1 stops run() before
// anything is built, with its own sentinel naming the variable.
func TestAuthDebugRefusedOutsideDevelopment(t *testing.T) {
	t.Setenv("AUTH_DEBUG", "1")
	err := run(context.Background(), quietLog(), environment.ModeProduction)
	if !errors.Is(err, errDebugRouteOutsideDevelopment) || !strings.Contains(err.Error(), "AUTH_DEBUG") {
		t.Fatalf("run(production, AUTH_DEBUG=1) = %v, want errDebugRouteOutsideDevelopment naming AUTH_DEBUG", err)
	}
	if err := refuseDebugRouteOutsideDevelopment(environment.ModeDevelopment); err != nil {
		t.Errorf("development with AUTH_DEBUG=1: %v", err)
	}
	t.Setenv("AUTH_DEBUG", "")
	if err := refuseDebugRouteOutsideDevelopment(environment.ModeProduction); err != nil {
		t.Errorf("production without AUTH_DEBUG: %v", err)
	}
}

// TestDevelopmentConsoleTransportWarns observes the one development console warning
// the host is expected to emit (design §6.3): its dev wiring runs console email +
// phone transports, so construction in development mode logs a development-only
// delivery-transport WARN (and never rejects). Production rejects the same wiring
// (TestProductionNegatives); development warns and proceeds. AUTH_RUNTIME_MODE no
// longer reaches the config: main resolves the mode once.
func TestDevelopmentConsoleTransportWarns(t *testing.T) {
	t.Setenv("AUTH_RUNTIME_MODE", "production")
	t.Setenv("AUTH_DELIVERY_MODE", "")
	rec := &recordHandler{}
	cfg, err := buildAuthConfig(slog.New(rec), environment.ModeDevelopment, nil)
	if err != nil {
		t.Fatalf("buildAuthConfig: %v", err)
	}
	if cfg.RuntimeMode != environment.ModeDevelopment {
		t.Fatalf("RuntimeMode = %q, want the development mode passed in", cfg.RuntimeMode)
	}
	// run() wires the generic-jobs dispatcher; reproduce it so jobs-mode construction succeeds.
	cfg.DeliveryDispatcher = jobsDispatcher(t)
	if _, err := auth.New(authmem.New().Repositories(), cfg.TokenSigner, cfg.RuntimeMode, cfg.DeliveryMode, cfg.options()...); err != nil {
		t.Fatalf("development construction should succeed, got: %v", err)
	}
	if !rec.hasWarnContaining("development-only delivery transport") {
		t.Fatal("expected a development-only delivery-transport WARN during development construction")
	}
}

// recordHandler is a minimal slog.Handler that captures each record's level and
// message so a test can assert a specific WARN fired.
type recordHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *recordHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *recordHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r)
	return nil
}
func (h *recordHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordHandler) WithGroup(string) slog.Handler      { return h }

// hasWarnContaining reports whether a WARN-level record's message contains sub.
func (h *recordHandler) hasWarnContaining(sub string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if r.Level == slog.LevelWarn && strings.Contains(r.Message, sub) {
			return true
		}
	}
	return false
}
