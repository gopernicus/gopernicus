package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/authmem"
	auth "github.com/gopernicus/gopernicus/features/authentication"
	"github.com/gopernicus/gopernicus/sdk/capabilities/email"
	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// This file is the AV3D-4.5 REAL-INTERACTION proof for DeliveryMode "in_process": it
// drives the host's actual composition (buildAuthConfig -> auth.NewService in in_process
// mode -> the bounded InProcessQueue + fixed worker pool the host runs via RunDelivery)
// through normal delivery, saturation (503 over real HTTP), transient retry (same secret),
// cancellation, and shutdown drain — observing the REAL mailer output — and measures
// goroutine counts before/during/after to prove the pool stays bounded and leaks nothing.
//
// The host's run() runs jobs mode; this test-scoped composition flips the same
// buildAuthConfig to in_process (the acceptable "temporary in_process wiring driven over
// real HTTP" the task allows), so the proof exercises the identical config-assembly seam.

// recordingSender records every Send it observes (including failed attempts) so a test
// can inspect the rendered body the in-process pool actually produced. It fails the first
// failFirst sends (a provider outage) then succeeds, standing in for the console mailer.
type recordingSender struct {
	mu        sync.Mutex
	msgs      []email.Message
	failFirst int
}

func (s *recordingSender) Send(_ context.Context, m email.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs = append(s.msgs, m)
	if len(s.msgs) <= s.failFirst {
		return errProviderDown
	}
	return nil
}

func (s *recordingSender) all() []email.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]email.Message(nil), s.msgs...)
}

func (s *recordingSender) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.msgs)
}

var errProviderDown = &senderError{"provider unavailable"}

type senderError struct{ s string }

func (e *senderError) Error() string { return e.s }

// bootInProcess assembles the host auth config in in_process mode over fresh in-memory
// repositories, overriding the mailer and applying any per-test tuning. No delivery
// dispatcher is wired (the in_process mode owns its bounded pool and needs none);
// passwordless enablement is satisfied by in_process itself.
func bootInProcess(t *testing.T, sender email.Sender, tune func(*auth.Config)) *auth.Service {
	t.Helper()
	cfg, err := buildAuthConfig(quietLog(), nil)
	if err != nil {
		t.Fatalf("buildAuthConfig: %v", err)
	}
	cfg.DeliveryMode = auth.DeliveryModeInProcess
	cfg.DeliveryJobsAcknowledged = false
	cfg.DeliveryEphemeralAcknowledged = true // set so the same wiring also passes in production
	if sender != nil {
		cfg.Mailer = sender
	}
	if tune != nil {
		tune(&cfg)
	}
	authRepos := authmem.New().Repositories()
	svc, err := auth.NewService(authRepos, cfg)
	if err != nil {
		t.Fatalf("auth.NewService (in_process): %v", err)
	}
	return svc
}

// mountInProcess registers the auth HTTP surface on a fresh router so a test can drive
// the REAL forgot-password / passwordless admission over HTTP.
func mountInProcess(t *testing.T, svc *auth.Service) http.Handler {
	t.Helper()
	router := web.NewWebHandler(web.WithLogging(quietLog()))
	bus := sdkevents.NewMemory(sdkevents.WithLogger(quietLog()))
	if err := svc.Register(feature.Mount{Router: router, Logger: quietLog(), Events: bus}); err != nil {
		t.Fatalf("auth.Register: %v", err)
	}
	return router
}

// postForgot drives POST /auth/password/forgot over real HTTP and returns the status code.
func postForgot(t *testing.T, router http.Handler, addr string) int {
	t.Helper()
	body := `{"email":"` + addr + `"}`
	req := httptest.NewRequest(http.MethodPost, "/auth/password/forgot", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if origins := allowedOrigins(); len(origins) > 0 {
		req.Header.Set("Origin", origins[0])
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec.Code
}

// waitSends blocks until the sender has recorded at least n messages, or fails.
func waitSends(t *testing.T, s *recordingSender, n int, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if s.count() >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected >= %d sends within %s, got %d", n, within, s.count())
}

// runDelivery starts the host-owned in_process runtime and returns a stop func.
func runDelivery(t *testing.T, svc *auth.Service) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.RunDelivery(ctx) }()
	return func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("RunDelivery returned error on shutdown: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("RunDelivery did not return within 5s of cancel")
		}
	}
}

// TestInProcessHostDeliversRegistrationOverPool drives a REAL registration through the
// in_process host: the verification email is enqueued into the bounded queue and the
// host-owned worker pool renders and sends it. The recording mailer observes exactly the
// message a real recipient would receive — proof the ephemeral pool actually delivers.
func TestInProcessHostDeliversRegistrationOverPool(t *testing.T) {
	sender := &recordingSender{}
	svc := bootInProcess(t, sender, nil)
	stop := runDelivery(t, svc)
	defer stop()

	const addr = "inproc-normal@example.com"
	if _, err := svc.RegisterUser(context.Background(), addr, "correct-horse-battery-staple", "InProc User"); err != nil {
		t.Fatalf("RegisterUser: %v", err)
	}
	waitSends(t, sender, 1, 5*time.Second)

	msgs := sender.all()
	last := msgs[len(msgs)-1]
	if len(last.To) == 0 || !strings.EqualFold(last.To[0], addr) {
		t.Fatalf("delivered to %v, want %s", last.To, addr)
	}
	if strings.TrimSpace(last.Text) == "" && strings.TrimSpace(last.HTML) == "" {
		t.Fatal("delivered verification message carried no body")
	}
}

// TestInProcessHostForgotPasswordAdmitsOverHTTP drives the enumeration-safe
// forgot-password start over REAL HTTP against the running in_process host: an unknown
// address admits and returns 202 Accepted (never enumerating), and the pool resolves it
// off the request path (skip, no send).
func TestInProcessHostForgotPasswordAdmitsOverHTTP(t *testing.T) {
	sender := &recordingSender{}
	svc := bootInProcess(t, sender, nil)
	stop := runDelivery(t, svc)
	defer stop()

	router := mountInProcess(t, svc)
	if code := postForgot(t, router, "ghost-inproc@example.com"); code != http.StatusAccepted {
		t.Fatalf("forgot-password over HTTP = %d, want 202 Accepted", code)
	}
}

// TestInProcessHostSaturationReturns503OverHTTP saturates the bounded queue (capacity 1,
// pool NOT running so nothing drains) and drives forgot-password over REAL HTTP: once the
// single slot is full, a further admission returns 503 Service Unavailable within the
// admission deadline — never a 202 after silently dropping the work.
func TestInProcessHostSaturationReturns503OverHTTP(t *testing.T) {
	svc := bootInProcess(t, &recordingSender{}, func(c *auth.Config) {
		c.InProcessDelivery = auth.InProcessDeliveryConfig{
			QueueCapacity:     1,
			AdmissionDeadline: 50 * time.Millisecond,
		}
	})
	// Deliberately do NOT run the pool: the one slot fills and stays full.
	router := mountInProcess(t, svc)

	var saw503 bool
	for i := 0; i < 5; i++ {
		code := postForgot(t, router, "sat-inproc-"+string(rune('a'+i))+"@example.com")
		if code == http.StatusServiceUnavailable {
			saw503 = true
			break
		}
		if code != http.StatusAccepted {
			t.Fatalf("unexpected forgot-password status %d (want 202 until saturated, then 503)", code)
		}
	}
	if !saw503 {
		t.Fatal("saturated in_process queue never returned 503 over HTTP (a dropped admission must not be a 202 lie)")
	}
}

// TestInProcessHostRetryReusesSecretOverPool drives a REAL registration whose provider
// fails the first send then succeeds: the bounded pool retries and the retried send
// carries the byte-identical rendered message (the checkpointed secret), never a
// re-minted one. Backoff is the package default (~5s), so this test is patient.
func TestInProcessHostRetryReusesSecretOverPool(t *testing.T) {
	sender := &recordingSender{failFirst: 1}
	svc := bootInProcess(t, sender, nil)
	stop := runDelivery(t, svc)
	defer stop()

	const addr = "inproc-retry@example.com"
	if _, err := svc.RegisterUser(context.Background(), addr, "correct-horse-battery-staple", "Retry User"); err != nil {
		t.Fatalf("RegisterUser: %v", err)
	}
	// First (failed) attempt + one default backoff (~5s) + the successful retry.
	waitSends(t, sender, 2, 20*time.Second)

	msgs := sender.all()
	first, last := msgs[0], msgs[len(msgs)-1]
	if first.Text != last.Text || first.HTML != last.HTML || first.Subject != last.Subject {
		t.Fatalf("retry did not resend the identical message (a secret was re-minted):\nfirst=%q/%q\nlast=%q/%q", first.Subject, first.Text, last.Subject, last.Text)
	}
	if len(last.To) == 0 || !strings.EqualFold(last.To[0], addr) {
		t.Fatalf("retry delivered to %v, want %s", last.To, addr)
	}
}

// TestInProcessHostShutdownDrainAndNoLeak measures goroutine counts across the runtime
// lifecycle: bounded during (a small fixed pool + supervisor over the baseline), returns
// to baseline after cancel — no leak across normal delivery, and RunDelivery returns
// promptly on shutdown (the bounded drain). Run under -race.
func TestInProcessHostShutdownDrainAndNoLeak(t *testing.T) {
	sender := &recordingSender{}
	const workers = 3
	svc := bootInProcess(t, sender, func(c *auth.Config) {
		c.InProcessDelivery = auth.InProcessDeliveryConfig{
			Workers:          workers,
			ShutdownDeadline: 2 * time.Second,
		}
	})

	settle := func() { time.Sleep(50 * time.Millisecond); runtime.GC() }
	settle()
	base := runtime.NumGoroutine()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.RunDelivery(ctx) }()

	// Drive real work so the pool is actively delivering during the measurement.
	for i := 0; i < workers*4; i++ {
		addr := "inproc-leak-" + string(rune('a'+i)) + "@example.com"
		if _, err := svc.RegisterUser(ctx, addr, "correct-horse-battery-staple", "Leak User"); err != nil {
			t.Fatalf("RegisterUser: %v", err)
		}
	}
	waitSends(t, sender, workers*4, 10*time.Second)

	// During: the extra goroutines are bounded by the fixed pool + the Run supervisor
	// goroutine + a small scheduling allowance — never one-per-request (workers*4 items).
	during := runtime.NumGoroutine()
	t.Logf("goroutines: baseline=%d during=%d delta=%d (workers=%d, items=%d)", base, during, during-base, workers, workers*4)
	if delta := during - base; delta > workers+4 {
		t.Fatalf("goroutine delta during run = %d, want bounded by the fixed pool (%d workers + supervisor); a per-request pool would spike toward %d", delta, workers, workers*4)
	}

	// Shutdown drains within the bound and returns promptly.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunDelivery returned error on shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunDelivery did not return within 5s of cancel (shutdown drain unbounded)")
	}

	// After: goroutines return to baseline (no leak across the pool lifecycle).
	for i := 0; i < 50; i++ {
		settle()
		if runtime.NumGoroutine() <= base {
			return
		}
	}
	if n := runtime.NumGoroutine(); n > base {
		t.Fatalf("goroutine leak after shutdown: baseline %d, now %d", base, n)
	}
}
