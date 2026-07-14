package main

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/authmem"
	auth "github.com/gopernicus/gopernicus/features/authentication"
	"github.com/gopernicus/gopernicus/sdk/capabilities/email"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// prodSender is a production-capable email transport double: it declares
// non-development-only capability metadata so the production transport gate accepts
// it (unlike the console sender the host wires by default).
type prodSender struct{}

func (prodSender) Send(context.Context, email.Message) error { return nil }
func (prodSender) Capabilities() email.Capabilities {
	return email.Capabilities{TransportSecurity: email.TransportSecurityStartTLS, DevelopmentOnly: false}
}

// prodNotifier is a production-capable phone Notifier double: it declares
// non-development-only metadata so phone passwordless has a production transport in
// the valid baseline.
type prodNotifier struct{ kind string }

func (n prodNotifier) Kind() string                                        { return n.kind }
func (prodNotifier) Notify(context.Context, identity.Address, notify.Message) error { return nil }
func (prodNotifier) Capabilities() notify.Capabilities {
	return notify.Capabilities{TransportSecurity: notify.TransportSecurityTLS, DevelopmentOnly: false}
}

// durableLimiter is a rate-limiter double that positively declares itself
// shared/durable, so the production non-durable-limiter gate accepts it (unlike the
// in-process ratelimiter.Memory the feature defaults a nil RateLimiter to).
type durableLimiter struct{}

func (durableLimiter) Allow(context.Context, string, ratelimiter.Limit) (ratelimiter.Result, error) {
	return ratelimiter.Result{Allowed: true}, nil
}
func (durableLimiter) Reset(context.Context, string) error { return nil }
func (durableLimiter) Close() error                        { return nil }
func (durableLimiter) RateLimiterDurability() auth.LimiterDurability {
	return auth.LimiterDurability{InProcessOnly: false}
}

// productionBaseline returns a production-VALID config + repositories: every v3
// safeguard the memory host would otherwise fail is satisfied (production transports,
// a durable limiter, an HTTPS magic-link base, the generic-jobs delivery dispatcher, an
// acknowledged runtime, and the identifier keyer buildAuthConfig already wires). It
// constructs cleanly in production (proven by TestProductionBaselineConstructs), so
// each negative below isolates exactly one broken safeguard.
func productionBaseline(t *testing.T) (auth.Config, auth.Repositories) {
	t.Helper()
	cfg, err := buildAuthConfig(quietLog(), nil)
	if err != nil {
		t.Fatalf("buildAuthConfig: %v", err)
	}
	cfg.RuntimeMode = auth.RuntimeModeProduction
	cfg.Mailer = prodSender{}
	cfg.Notifiers = []notify.Notifier{prodNotifier{kind: identity.KindPhone}}
	cfg.RateLimiter = durableLimiter{}
	cfg.PublicAuthBaseURL = "https://auth.example.com/auth/magic"
	// run() wires the generic-jobs dispatcher; reproduce it so jobs mode has its queue
	// capability in the baseline.
	cfg.DeliveryDispatcher = jobsDispatcher(t)

	repos := authmem.New().Repositories()
	return cfg, repos
}

// TestProductionBaselineConstructs proves the production-valid baseline actually
// constructs: without it, the negative matrix below could pass vacuously (every case
// erroring for the wrong reason). It is the positive control for the fail-closed
// matrix.
func TestProductionBaselineConstructs(t *testing.T) {
	cfg, repos := productionBaseline(t)
	if _, err := auth.NewService(repos, cfg); err != nil {
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
		mutate func(cfg *auth.Config, repos *auth.Repositories)
		want   error
	}{
		{
			// console: the host's dev email + phone transports leak OTPs/magic links to
			// logs, so production rejects them.
			name: "console_delivery_transports",
			mutate: func(cfg *auth.Config, _ *auth.Repositories) {
				cfg.Mailer = email.NewConsole(quietLog())
				cfg.Notifiers = []notify.Notifier{notify.NewConsole(identity.KindPhone, quietLog())}
			},
			want: auth.ErrInsecureDeliveryTransport,
		},
		{
			// insecure URL (the Views magic-link landing over http): a plain-http
			// public base exposes the single-use token in transit. This is the unsafe
			// Views/public-URL combination — the bundled magic-link page is wired, so
			// its base URL must be HTTPS in production.
			name: "insecure_http_public_auth_base_url",
			mutate: func(cfg *auth.Config, _ *auth.Repositories) {
				cfg.PublicAuthBaseURL = "http://auth.example.com/auth/magic"
			},
			want: auth.ErrPublicAuthBaseURLInsecure,
		},
		{
			// memory limiter: a nil RateLimiter defaults to the in-process
			// ratelimiter.Memory, which enforces only a per-process budget.
			name: "in_process_memory_rate_limiter",
			mutate: func(cfg *auth.Config, _ *auth.Repositories) {
				cfg.RateLimiter = nil
			},
			want: auth.ErrNonDurableRateLimiter,
		},
		{
			// keyer: PII-free rate-limit/idempotency keys need the shared HMAC keyer in
			// production so one identifier maps to one bucket across instances.
			name: "missing_identifier_keyer",
			mutate: func(cfg *auth.Config, _ *auth.Repositories) {
				cfg.IdentifierKeyer = nil
			},
			want: auth.ErrIdentifierKeyerRequired,
		},
		{
			// runtime: the queue is the only send path, so production requires the host
			// to affirm it runs the generic-jobs delivery runtime (jobs.FencedRuntime).
			name: "unacknowledged_delivery_runtime",
			mutate: func(cfg *auth.Config, _ *auth.Repositories) {
				cfg.DeliveryJobsAcknowledged = false
			},
			want: auth.ErrDeliveryJobsUnacknowledged,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, repos := productionBaseline(t)
			tc.mutate(&cfg, &repos)
			_, err := auth.NewService(repos, cfg)
			if !errors.Is(err, tc.want) {
				t.Fatalf("production %s: got err %v, want %v", tc.name, err, tc.want)
			}
		})
	}
}

// TestDevelopmentConsoleTransportWarns observes the one development console warning
// the host is expected to emit (design §6.3): its dev wiring runs console email +
// phone transports, so construction in development mode logs a development-only
// delivery-transport WARN (and never rejects). Production rejects the same wiring
// (TestProductionNegatives); development warns and proceeds.
func TestDevelopmentConsoleTransportWarns(t *testing.T) {
	rec := &recordHandler{}
	cfg, err := buildAuthConfig(slog.New(rec), nil)
	if err != nil {
		t.Fatalf("buildAuthConfig: %v", err)
	}
	if cfg.RuntimeMode != auth.RuntimeModeDevelopment {
		t.Fatalf("host default RuntimeMode = %q, want development", cfg.RuntimeMode)
	}
	// run() wires the generic-jobs dispatcher; reproduce it so jobs-mode construction succeeds.
	cfg.DeliveryDispatcher = jobsDispatcher(t)
	if _, err := auth.NewService(authmem.New().Repositories(), cfg); err != nil {
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
func (h *recordHandler) WithGroup(string) slog.Handler       { return h }

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
