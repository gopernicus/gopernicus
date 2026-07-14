package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/authmem"
	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/deliveryhealth"
	auth "github.com/gopernicus/gopernicus/features/authentication"
	"github.com/gopernicus/gopernicus/sdk/capabilities/email"
	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
)

// This file is the AV3D-5.3 REAL-INTERACTION proof for the host-composed delivery health
// surface (internal/deliveryhealth). It drives the bounded in_process composition over real
// HTTP + the real worker pool and hits the health endpoint (deliveryhealth.Handler over
// httptest) to assert the four distinguishable states: runtime not-started vs running,
// backlog visible under saturation, dead-letter count incrementing after a permanent
// failure, and an observer-failure signal when the emitter errors. It closes with a no-leak
// test proving the serialized health output carries no canary recipient or secret.

// failingBus always errors, standing in for a broken delivery-events bus so the
// observer-failure path is exercised end to end.
type failingBus struct{}

func (failingBus) Emit(context.Context, sdkevents.Event, ...sdkevents.EmitOption) error {
	return errProviderDown
}

// bootHealth assembles the in_process host composition WITH the health surface wired exactly
// as run() does for DELIVERY_MODE=in_process: the health emitter wraps the delivery-events
// emitter, and the depth source reads the auth Service's InProcessQueueDepth. A nil emitter
// defaults to a real in-memory bus; a nil sender keeps the console mailer.
func bootHealth(t *testing.T, sender email.Sender, emitter sdkevents.Emitter, tune func(*auth.Config)) (*auth.Service, *deliveryhealth.Health) {
	t.Helper()
	cfg, err := buildAuthConfig(quietLog(), nil)
	if err != nil {
		t.Fatalf("buildAuthConfig: %v", err)
	}
	cfg.DeliveryMode = auth.DeliveryModeInProcess
	cfg.DeliveryJobsAcknowledged = false
	cfg.DeliveryEphemeralAcknowledged = true
	cfg.DeliveryDispatcher = nil
	if sender != nil {
		cfg.Mailer = sender
	}

	h := deliveryhealth.New(string(auth.DeliveryModeInProcess))
	if emitter == nil {
		emitter = sdkevents.NewMemory(sdkevents.WithLogger(quietLog()))
	}
	cfg.DeliveryEventsEmitter = h.Emitter(emitter)

	if tune != nil {
		tune(&cfg)
	}
	svc, err := auth.NewService(authmem.New().Repositories(), cfg)
	if err != nil {
		t.Fatalf("auth.NewService (in_process + health): %v", err)
	}
	h.SetDepthSource(svc.InProcessQueueDepth)
	return svc, h
}

// runDeliveryHealth starts the host-owned in_process runtime bracketed by the health
// lifecycle markers exactly as run() does, and returns a stop func.
func runDeliveryHealth(t *testing.T, svc *auth.Service, h *deliveryhealth.Health) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	h.MarkStarted()
	go func() { defer h.MarkStopped(); done <- svc.RunDelivery(ctx) }()
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

// getHealth drives the health endpoint over httptest and decodes the bounded snapshot.
func getHealth(t *testing.T, h *deliveryhealth.Health) deliveryhealth.Snapshot {
	t.Helper()
	rec := httptest.NewRecorder()
	h.Handler()(rec, httptest.NewRequest(http.MethodGet, "/healthz/delivery", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/healthz/delivery = %d, want 200", rec.Code)
	}
	var s deliveryhealth.Snapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		t.Fatalf("decode health snapshot: %v", err)
	}
	return s
}

// waitHealth polls the health endpoint until pred holds or the deadline passes.
func waitHealth(t *testing.T, h *deliveryhealth.Health, within time.Duration, pred func(deliveryhealth.Snapshot) bool) deliveryhealth.Snapshot {
	t.Helper()
	deadline := time.Now().Add(within)
	var last deliveryhealth.Snapshot
	for time.Now().Before(deadline) {
		last = getHealth(t, h)
		if pred(last) {
			return last
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("health predicate not satisfied within %s; last snapshot=%+v", within, last)
	return last
}

// TestDeliveryHealthNotStartedVsRunning proves the endpoint distinguishes a delivery runtime
// the host has not started from a running one, over the REAL runtime lifecycle.
func TestDeliveryHealthNotStartedVsRunning(t *testing.T) {
	svc, h := bootHealth(t, &recordingSender{}, nil, nil)

	if s := getHealth(t, h); s.Runtime != "not_started" {
		t.Fatalf("before start: runtime = %q, want not_started (snapshot=%+v)", s.Runtime, s)
	}
	if s := getHealth(t, h); s.Mode != "in_process" {
		t.Fatalf("mode = %q, want in_process", s.Mode)
	}

	stop := runDeliveryHealth(t, svc, h)
	waitHealth(t, h, 2*time.Second, func(s deliveryhealth.Snapshot) bool { return s.Runtime == "running" })

	stop()
	waitHealth(t, h, 2*time.Second, func(s deliveryhealth.Snapshot) bool { return s.Runtime == "not_started" })
}

// TestDeliveryHealthBacklogUnderSaturation saturates the bounded queue (capacity 1, pool NOT
// running) via REAL forgot-password admissions over HTTP and asserts the health endpoint
// shows the queue saturated/backlogged — distinct from an idle queue.
func TestDeliveryHealthBacklogUnderSaturation(t *testing.T) {
	svc, h := bootHealth(t, &recordingSender{}, nil, func(c *auth.Config) {
		c.InProcessDelivery = auth.InProcessDeliveryConfig{
			QueueCapacity:     1,
			AdmissionDeadline: 50 * time.Millisecond,
		}
	})
	// Deliberately do NOT run the pool: the one slot fills and stays full.
	router := mountInProcess(t, svc)

	// Idle queue: not saturated.
	if s := getHealth(t, h); s.Saturated || s.Capacity != 1 {
		t.Fatalf("idle queue: got saturated=%v capacity=%d, want saturated=false capacity=1 (snapshot=%+v)", s.Saturated, s.Capacity, s)
	}

	// Drive admissions until at least one 202 fills the single slot (further ones 503).
	for i := 0; i < 5; i++ {
		_ = postForgot(t, router, "sat-health-"+string(rune('a'+i))+"@example.com")
	}
	s := getHealth(t, h)
	if !s.Saturated || s.Queued < s.Capacity {
		t.Fatalf("saturated queue not visible: queued=%d capacity=%d saturated=%v (snapshot=%+v)", s.Queued, s.Capacity, s.Saturated, s)
	}
	if s.Runtime != "not_started" {
		t.Fatalf("runtime = %q, want not_started (pool never run)", s.Runtime)
	}
}

// TestDeliveryHealthDeadLetterIncrements drives a REAL registration whose provider always
// fails with MaxAttempts=1, so the bounded pool dead-letters on the first attempt, and
// asserts the health endpoint's dead_lettered counter increments.
func TestDeliveryHealthDeadLetterIncrements(t *testing.T) {
	svc, h := bootHealth(t, &recordingSender{failFirst: 1_000}, nil, func(c *auth.Config) {
		c.InProcessDelivery = auth.InProcessDeliveryConfig{MaxAttempts: 1}
	})
	stop := runDeliveryHealth(t, svc, h)
	defer stop()

	if _, err := svc.RegisterUser(context.Background(), "deadletter-health@example.com", "correct-horse-battery-staple", "DL User"); err != nil {
		t.Fatalf("RegisterUser: %v", err)
	}
	s := waitHealth(t, h, 5*time.Second, func(s deliveryhealth.Snapshot) bool { return s.DeadLettered >= 1 })
	if s.Delivered != 0 {
		t.Fatalf("dead-letter run should not report a delivery: %+v", s)
	}
}

// TestDeliveryHealthObserverFailureVisible wires a delivery-events emitter that always errors
// and drives a REAL successful registration; the delivered lifecycle observation's emit
// fails, and the health endpoint's observer_failures counter increments.
func TestDeliveryHealthObserverFailureVisible(t *testing.T) {
	svc, h := bootHealth(t, &recordingSender{}, failingBus{}, nil)
	stop := runDeliveryHealth(t, svc, h)
	defer stop()

	if _, err := svc.RegisterUser(context.Background(), "observerfail-health@example.com", "correct-horse-battery-staple", "OF User"); err != nil {
		t.Fatalf("RegisterUser: %v", err)
	}
	waitHealth(t, h, 5*time.Second, func(s deliveryhealth.Snapshot) bool { return s.ObserverFailures >= 1 })
}

// TestDeliveryHealthNoLeak drives a REAL delivery with a canary recipient and asserts the
// serialized health output carries neither the recipient address nor the delivered secret —
// the health surface is counters/gauges/enums only.
func TestDeliveryHealthNoLeak(t *testing.T) {
	const canaryAddr = "canary-leak-health@secret.example"
	sender := &recordingSender{}
	svc, h := bootHealth(t, sender, nil, nil)
	stop := runDeliveryHealth(t, svc, h)
	defer stop()

	if _, err := svc.RegisterUser(context.Background(), canaryAddr, "correct-horse-battery-staple", "Canary User"); err != nil {
		t.Fatalf("RegisterUser: %v", err)
	}
	waitSends(t, sender, 1, 5*time.Second)

	// The delivered secret the recipient actually received (the canary payload the health
	// surface must never echo).
	msg := sender.all()[0]

	rec := httptest.NewRecorder()
	h.Handler()(rec, httptest.NewRequest(http.MethodGet, "/healthz/delivery", nil))
	body := rec.Body.String()

	canaries := []string{canaryAddr, "canary-leak-health", "secret.example"}
	if to := strings.TrimSpace(msg.Text); to != "" {
		canaries = append(canaries, to)
	}
	if msg.Subject != "" {
		canaries = append(canaries, msg.Subject)
	}
	for _, c := range canaries {
		if c != "" && strings.Contains(body, c) {
			t.Fatalf("health output leaked a canary value %q; body=%s", c, body)
		}
	}
}
