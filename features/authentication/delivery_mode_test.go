package authentication

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk/capabilities/email"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// ---------------------------------------------------------------------------
// AV3D-0.1 — delivery-mode construction matrix
//
// DeliveryMode is the host's EXPLICIT selection of the outbound-delivery
// execution model; construction never infers it from a non-nil collaborator.
// These cases freeze the vocabulary + validation contract: empty/unknown fails
// loudly; "off" fails when a flow can deliver; "jobs" requires the queue
// capability + encrypter + a production runtime acknowledgment on a durable
// store; "in_process" rejects a durable-status claim and requires the explicit
// production crash-loss acknowledgment. The transport capability checks are
// unchanged (proven by the production cases reusing the same prod transports).
// ---------------------------------------------------------------------------

// deliveryDevConfig is a minimal valid development Config with no DeliveryMode
// selected. Cases set DeliveryMode (and any delivery collaborators) to isolate a
// single dimension.
func deliveryDevConfig() Config {
	return Config{
		Hasher:      stubHasher{},
		Mailer:      stubMailer{},
		TokenSigner: stubSigner{},
		RuntimeMode: RuntimeModeDevelopment,
	}
}

// deliveryProdConfig is a production Config with every always-on production gate
// satisfied EXCEPT the delivery dimension each case sets: production-capable
// transport, a durable limiter, and the PII-free keyer. The delivery-mode block
// runs before those gates, but they are wired so a success case actually
// constructs.
func deliveryProdConfig() Config {
	return Config{
		Hasher:          stubHasher{},
		Mailer:          prodMailer{},
		TokenSigner:     stubSigner{},
		RuntimeMode:     RuntimeModeProduction,
		RateLimiter:     durableLimiter{},
		IdentifierKeyer: prodKeyer{},
	}
}

// stubDispatcher satisfies DeliveryDispatcher (the jobs-mode generic-jobs transport,
// AV3D-3.1) for the construction matrix. None of its methods are driven — the tests
// assert wiring/validation, not delivery flow. It declares no durability metadata, so
// production tolerates it exactly like a bespoke store that proves no negative.
type stubDispatcher struct{}

func (stubDispatcher) Submit(context.Context, string, string, string, []byte) (string, error) {
	return "", nil
}
func (stubDispatcher) Replace(context.Context, string, string, string, []byte) (string, error) {
	return "", nil
}
func (stubDispatcher) LatestStatus(context.Context, string) (string, error) { return "", nil }

// devOnlyMailer is a development-only email transport double: it declares capability
// metadata marking itself DevelopmentOnly (the bundled email.Console posture), so a
// production RuntimeMode rejects it (ErrInsecureDeliveryTransport) even under jobs
// mode with an otherwise complete durable/acknowledged wiring.
type devOnlyMailer struct{}

func (devOnlyMailer) Send(context.Context, email.Message) error { return nil }
func (devOnlyMailer) Capabilities() email.Capabilities {
	return email.Capabilities{TransportSecurity: email.TransportSecurityNone, DevelopmentOnly: true}
}

func TestNewServiceDeliveryModeMatrix(t *testing.T) {
	cases := []struct {
		name    string
		repos   Repositories
		cfg     func() Config
		wantErr error // nil → construction must succeed
	}{
		{
			name:  "empty mode fails loudly",
			repos: Repositories{},
			cfg: func() Config {
				c := deliveryDevConfig()
				c.DeliveryMode = ""
				return c
			},
			wantErr: ErrDeliveryModeRequired,
		},
		{
			name:  "unknown mode fails loudly",
			repos: Repositories{},
			cfg: func() Config {
				c := deliveryDevConfig()
				c.DeliveryMode = "queue"
				return c
			},
			wantErr: ErrDeliveryModeInvalid,
		},
		{
			name:  "off with no deliverable capability constructs",
			repos: Repositories{},
			cfg: func() Config {
				c := deliveryDevConfig()
				c.DeliveryMode = DeliveryModeOff
				return c
			},
		},
		{
			name:  "off rejects a wired delivery dispatcher",
			repos: Repositories{},
			cfg: func() Config {
				c := deliveryDevConfig()
				c.DeliveryMode = DeliveryModeOff
				c.DeliveryDispatcher = stubDispatcher{}
				c.DeliveryEncrypter = stubEncrypter{} // isolate the off-conflict, not the encrypter gate
				return c
			},
			wantErr: ErrDeliveryOffButDeliverable,
		},
		{
			name:  "off rejects an enabled passwordless flow",
			repos: Repositories{Challenges: stubChallenges{}},
			cfg: func() Config {
				c := deliveryDevConfig()
				c.DeliveryMode = DeliveryModeOff
				c.ChallengeProtector = mustProtector(t)
				c.PublicAuthBaseURL = "https://auth.example.com"
				c.Passwordless = []string{"email"}
				return c
			},
			wantErr: ErrPasswordlessDeliveryRequired,
		},
		{
			// The generic-jobs dispatcher (Config.DeliveryDispatcher) absent → the jobs mode
			// has no send path and fails closed.
			name:  "jobs requires the queue capability",
			repos: Repositories{},
			cfg: func() Config {
				c := deliveryDevConfig()
				c.DeliveryMode = DeliveryModeJobs
				return c
			},
			wantErr: ErrDeliveryQueueRequired,
		},
		{
			// The dispatcher path (AV3D-3.1) is the OTHER jobs-mode queue capability. A wired
			// dispatcher still requires the encrypter — the payload is always sealed.
			name:  "jobs via dispatcher requires an encrypter",
			repos: Repositories{},
			cfg: func() Config {
				c := deliveryDevConfig()
				c.DeliveryMode = DeliveryModeJobs
				c.DeliveryDispatcher = stubDispatcher{}
				return c
			},
			wantErr: ErrDeliveryEncrypterRequired,
		},
		{
			name:  "jobs via dispatcher with encrypter constructs (development)",
			repos: Repositories{},
			cfg: func() Config {
				c := deliveryDevConfig()
				c.DeliveryMode = DeliveryModeJobs
				c.DeliveryDispatcher = stubDispatcher{}
				c.DeliveryEncrypter = stubEncrypter{}
				return c
			},
		},
		{
			// Missing runtime acknowledgment on the dispatcher path: the generic queue is the
			// only send path, so production requires the explicit runtime affirmation.
			name:  "jobs via dispatcher in production requires the runtime acknowledgment",
			repos: Repositories{},
			cfg: func() Config {
				c := deliveryProdConfig()
				c.DeliveryMode = DeliveryModeJobs
				c.DeliveryDispatcher = stubDispatcher{}
				c.DeliveryEncrypter = stubEncrypter{}
				return c
			},
			wantErr: ErrDeliveryJobsUnacknowledged,
		},
		{
			// The dispatcher declares no durability metadata, so production tolerates it (a
			// durable transport is not asked to prove a negative) once acknowledged.
			name:  "jobs via dispatcher in production with acknowledgment constructs",
			repos: Repositories{},
			cfg: func() Config {
				c := deliveryProdConfig()
				c.DeliveryMode = DeliveryModeJobs
				c.DeliveryDispatcher = stubDispatcher{}
				c.DeliveryEncrypter = stubEncrypter{}
				c.DeliveryJobsAcknowledged = true
				return c
			},
		},
		{
			// Development-only transport rejected in production under a complete jobs-mode
			// wiring: the dispatcher/acknowledgment gates pass, then the console mailer is
			// rejected because it leaks OTPs/magic links to logs.
			name:  "jobs in production rejects a development-only transport",
			repos: Repositories{},
			cfg: func() Config {
				c := deliveryProdConfig()
				c.Mailer = devOnlyMailer{}
				c.DeliveryMode = DeliveryModeJobs
				c.DeliveryDispatcher = stubDispatcher{}
				c.DeliveryEncrypter = stubEncrypter{}
				c.DeliveryJobsAcknowledged = true
				return c
			},
			wantErr: ErrInsecureDeliveryTransport,
		},
		{
			// in_process builds a feature-internal bounded delivery queue (the ephemeral
			// runtime, AV3D-4.1) whose payload is always sealed, so the encrypter is
			// REQUIRED even without a wired collaborator — the same fail-closed posture as
			// the jobs-mode queue.
			name:  "in_process requires an encrypter",
			repos: Repositories{},
			cfg: func() Config {
				c := deliveryDevConfig()
				c.DeliveryMode = DeliveryModeInProcess
				return c
			},
			wantErr: ErrDeliveryEncrypterRequired,
		},
		{
			name:  "in_process with an encrypter constructs (development)",
			repos: Repositories{},
			cfg: func() Config {
				c := deliveryDevConfig()
				c.DeliveryMode = DeliveryModeInProcess
				c.DeliveryEncrypter = stubEncrypter{}
				return c
			},
		},
		{
			name:  "in_process in production requires the crash-loss acknowledgment",
			repos: Repositories{},
			cfg: func() Config {
				c := deliveryProdConfig()
				c.DeliveryMode = DeliveryModeInProcess
				c.DeliveryEncrypter = stubEncrypter{}
				return c
			},
			wantErr: ErrDeliveryEphemeralUnacknowledged,
		},
		{
			name:  "in_process in production with the crash-loss acknowledgment constructs",
			repos: Repositories{},
			cfg: func() Config {
				c := deliveryProdConfig()
				c.DeliveryMode = DeliveryModeInProcess
				c.DeliveryEncrypter = stubEncrypter{}
				c.DeliveryEphemeralAcknowledged = true
				return c
			},
		},
		// AV3D-4.5 construction validation: every in-process tuning knob is nil-safe
		// (zero → default), but an invalid bound fails LOUDLY at construction with a typed
		// delivery error. Each case isolates one dimension.
		{
			name:  "in_process rejects a negative worker count",
			repos: Repositories{},
			cfg: func() Config {
				c := deliveryDevConfig()
				c.DeliveryMode = DeliveryModeInProcess
				c.DeliveryEncrypter = stubEncrypter{}
				c.InProcessDelivery = InProcessDeliveryConfig{Workers: -1}
				return c
			},
			wantErr: delivery.ErrInProcessWorkersInvalid,
		},
		{
			name:  "in_process rejects a negative queue capacity",
			repos: Repositories{},
			cfg: func() Config {
				c := deliveryDevConfig()
				c.DeliveryMode = DeliveryModeInProcess
				c.DeliveryEncrypter = stubEncrypter{}
				c.InProcessDelivery = InProcessDeliveryConfig{QueueCapacity: -8}
				return c
			},
			wantErr: delivery.ErrInProcessCapacityInvalid,
		},
		{
			name:  "in_process rejects a negative admission deadline",
			repos: Repositories{},
			cfg: func() Config {
				c := deliveryDevConfig()
				c.DeliveryMode = DeliveryModeInProcess
				c.DeliveryEncrypter = stubEncrypter{}
				c.InProcessDelivery = InProcessDeliveryConfig{AdmissionDeadline: -time.Second}
				return c
			},
			wantErr: delivery.ErrInProcessAdmissionDeadlineInvalid,
		},
		{
			name:  "in_process rejects a negative shutdown deadline",
			repos: Repositories{},
			cfg: func() Config {
				c := deliveryDevConfig()
				c.DeliveryMode = DeliveryModeInProcess
				c.DeliveryEncrypter = stubEncrypter{}
				c.InProcessDelivery = InProcessDeliveryConfig{ShutdownDeadline: -time.Second}
				return c
			},
			wantErr: delivery.ErrInProcessShutdownDeadlineInvalid,
		},
		{
			name:  "in_process rejects a negative attempt cap",
			repos: Repositories{},
			cfg: func() Config {
				c := deliveryDevConfig()
				c.DeliveryMode = DeliveryModeInProcess
				c.DeliveryEncrypter = stubEncrypter{}
				c.InProcessDelivery = InProcessDeliveryConfig{MaxAttempts: -1}
				return c
			},
			wantErr: delivery.ErrInProcessMaxAttemptsInvalid,
		},
		{
			name:  "in_process rejects negative status retention max entries",
			repos: Repositories{},
			cfg: func() Config {
				c := deliveryDevConfig()
				c.DeliveryMode = DeliveryModeInProcess
				c.DeliveryEncrypter = stubEncrypter{}
				c.InProcessDelivery = InProcessDeliveryConfig{StatusMaxEntries: -1}
				return c
			},
			wantErr: delivery.ErrInProcessStatusMaxEntriesInvalid,
		},
		{
			name:  "in_process rejects a negative status retention TTL",
			repos: Repositories{},
			cfg: func() Config {
				c := deliveryDevConfig()
				c.DeliveryMode = DeliveryModeInProcess
				c.DeliveryEncrypter = stubEncrypter{}
				c.InProcessDelivery = InProcessDeliveryConfig{StatusTTL: -time.Minute}
				return c
			},
			wantErr: delivery.ErrInProcessStatusTTLInvalid,
		},
		{
			name:  "in_process rejects status retention smaller than the queue capacity",
			repos: Repositories{},
			cfg: func() Config {
				c := deliveryDevConfig()
				c.DeliveryMode = DeliveryModeInProcess
				c.DeliveryEncrypter = stubEncrypter{}
				// A queued generation must never be retention-evicted, so max entries must be
				// at least the capacity.
				c.InProcessDelivery = InProcessDeliveryConfig{QueueCapacity: 64, StatusMaxEntries: 8}
				return c
			},
			wantErr: delivery.ErrInProcessStatusRetentionTooSmall,
		},
		{
			name:  "in_process with tuned in-bounds knobs constructs",
			repos: Repositories{},
			cfg: func() Config {
				c := deliveryDevConfig()
				c.DeliveryMode = DeliveryModeInProcess
				c.DeliveryEncrypter = stubEncrypter{}
				c.InProcessDelivery = InProcessDeliveryConfig{
					Workers:           4,
					QueueCapacity:     32,
					AdmissionDeadline: 100 * time.Millisecond,
					ShutdownDeadline:  5 * time.Second,
					MaxAttempts:       3,
					StatusMaxEntries:  512,
					StatusTTL:         10 * time.Minute,
				}
				return c
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, err := NewService(tc.repos, tc.cfg())
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err=%v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("err=%v, want nil", err)
			}
			if svc == nil {
				t.Fatal("NewService returned nil Service")
			}
		})
	}
}

// TestNewServiceDeliveryModeCheckedAfterRuntimeMode proves DeliveryMode validation
// does not mask the required RuntimeMode gate: an empty RuntimeMode reports its own
// error first even when DeliveryMode is also empty.
func TestNewServiceDeliveryModeCheckedAfterRuntimeMode(t *testing.T) {
	c := Config{Hasher: stubHasher{}, Mailer: stubMailer{}, TokenSigner: stubSigner{}}
	if _, err := NewService(Repositories{}, c); !errors.Is(err, ErrRuntimeModeRequired) {
		t.Fatalf("empty RuntimeMode + empty DeliveryMode: err=%v, want ErrRuntimeModeRequired", err)
	}
}

// countingDispatcher records whether any dispatcher method was invoked. It backs the
// "Register starts no worker" proof: if Register (or NewService) ran a delivery
// runtime or drove the transport, it would call the dispatcher, incrementing the
// counter. In jobs mode the feature builds the processor but starts NOTHING — the host
// runs the generic jobs runtime and only request-time producers submit — so after
// Register the counter stays zero.
type countingDispatcher struct{ calls *int64 }

func (d countingDispatcher) Submit(context.Context, string, string, string, []byte) (string, error) {
	atomic.AddInt64(d.calls, 1)
	return "", nil
}
func (d countingDispatcher) Replace(context.Context, string, string, string, []byte) (string, error) {
	atomic.AddInt64(d.calls, 1)
	return "", nil
}
func (d countingDispatcher) LatestStatus(context.Context, string) (string, error) {
	atomic.AddInt64(d.calls, 1)
	return "", nil
}

// TestRegisterStartsNoDeliveryWorker proves Register mounts routes without starting
// the delivery runtime (authv3-delivery-refactor standing invariant: "Register
// starts no goroutines. Hosts explicitly run the selected runtime."). A jobs-mode
// service is built over a dispatcher whose calls increment a counter; after Register
// the counter is still zero — delivery activity only happens when a request-time
// producer submits or the host runs the generic jobs runtime.
func TestRegisterStartsNoDeliveryWorker(t *testing.T) {
	var calls int64
	cfg := deliveryDevConfig()
	cfg.DeliveryMode = DeliveryModeJobs
	cfg.DeliveryDispatcher = countingDispatcher{calls: &calls}
	cfg.DeliveryEncrypter = stubEncrypter{}

	svc, err := NewService(Repositories{}, cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if err := svc.Register(feature.Mount{Router: web.NewWebHandler()}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	// Give any (erroneously) started goroutine a chance to run before asserting.
	time.Sleep(20 * time.Millisecond)
	if got := atomic.LoadInt64(&calls); got != 0 {
		t.Fatalf("delivery dispatcher called %d times after Register — a worker was started; Register must start none", got)
	}
}

// TestInProcessRegisterStartsNoRuntime proves the bounded in-process runtime is wired
// in in_process mode but started by NEITHER NewService NOR Register — the host owns the
// lifecycle via RunDelivery (authv3-delivery-refactor AV3D-4.1 / standing invariant
// "Register starts no goroutines"). If construction or Register had started the pool,
// the first RunDelivery below would collide and return ErrInProcessAlreadyRunning; a
// clean nil on cancel proves nothing was running. The rigorous "the pool processes
// nothing until Run" proof lives at the delivery layer (inprocess_test.go).
func TestInProcessRegisterStartsNoRuntime(t *testing.T) {
	cfg := deliveryDevConfig()
	cfg.DeliveryMode = DeliveryModeInProcess
	cfg.DeliveryEncrypter = stubEncrypter{}

	svc, err := NewService(Repositories{}, cfg)
	if err != nil {
		t.Fatalf("NewService (in_process): %v", err)
	}
	if svc.inProcessRuntime == nil {
		t.Fatal("in_process mode did not wire the bounded delivery runtime")
	}
	if err := svc.Register(feature.Mount{Router: web.NewWebHandler()}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	// Give any (erroneously) started goroutine a chance to claim the runtime before the
	// host-owned RunDelivery does.
	time.Sleep(20 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.RunDelivery(ctx) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunDelivery returned %v — construction/Register must start no runtime (a running pool would make this ErrInProcessAlreadyRunning)", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunDelivery did not return after ctx cancel")
	}
}

// TestRunDeliveryNoopWithoutInProcessMode proves RunDelivery is a safe no-op in every
// mode that is not in_process, so a host may call it unconditionally alongside the
// mode-specific runtimes.
func TestRunDeliveryNoopWithoutInProcessMode(t *testing.T) {
	cfg := deliveryDevConfig()
	cfg.DeliveryMode = DeliveryModeOff
	svc, err := NewService(Repositories{}, cfg)
	if err != nil {
		t.Fatalf("NewService (off): %v", err)
	}
	if err := svc.RunDelivery(context.Background()); err != nil {
		t.Fatalf("RunDelivery in off mode = %v, want a no-op nil", err)
	}
}

// mustProtector builds an HMAC challenge protector for the passwordless-off case.
func mustProtector(t *testing.T) ChallengeProtector {
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
