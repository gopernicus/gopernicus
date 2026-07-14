package main

import (
	"context"
	"io"
	"log/slog"
	"runtime"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/authjobs"
	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/authmem"
	auth "github.com/gopernicus/gopernicus/features/authentication"
	"github.com/gopernicus/gopernicus/features/jobs"
	jobsmem "github.com/gopernicus/gopernicus/features/jobs/memstore"
)

// quietLog is a discarding logger so the console transports' dev WARN and the
// ephemeral-key WARNs do not spam the test output (and no key material is printed).
func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// jobsDispatcher builds the generic-jobs delivery dispatcher over an in-memory fenced
// queue — the jobs-mode delivery transport run() wires, reproduced here so a construction
// test can satisfy the jobs-mode queue capability.
func jobsDispatcher(t *testing.T) auth.DeliveryDispatcher {
	t.Helper()
	deliveryJobs, err := jobs.NewService(jobs.Repositories{FencedQueue: jobsmem.NewFencedQueue()}, jobs.Config{Logger: quietLog()})
	if err != nil {
		t.Fatalf("jobs.NewService: %v", err)
	}
	return authjobs.NewDispatcher(deliveryJobs)
}

// TestBuildAuthConfigConstructs proves the AV3-8.6 development wiring — bundled templ
// Views, browser-safe Origin allowlist, passwordless (email + phone), magic-link base
// URL, and every distinct development secret — constructs the auth service cleanly
// over the in-memory repositories (all v3 ports wired in authmem), with the delivery
// worker acknowledged.
func TestBuildAuthConfigConstructs(t *testing.T) {
	cfg, err := buildAuthConfig(quietLog(), nil)
	if err != nil {
		t.Fatalf("buildAuthConfig: %v", err)
	}
	if cfg.Views == nil {
		t.Fatal("Config.Views is nil: bundled templ Views not wired")
	}
	if len(cfg.AllowedOrigins) == 0 {
		t.Fatal("Config.AllowedOrigins empty: browser-safe gate not wired")
	}
	if len(cfg.Passwordless) == 0 {
		t.Fatal("Config.Passwordless empty: passwordless not enabled")
	}
	if cfg.PublicAuthBaseURL == "" {
		t.Fatal("Config.PublicAuthBaseURL empty: magic-link base URL not wired")
	}
	if cfg.DeliveryMode != auth.DeliveryModeJobs {
		t.Fatalf("Config.DeliveryMode = %q, want %q", cfg.DeliveryMode, auth.DeliveryModeJobs)
	}
	if !cfg.DeliveryJobsAcknowledged {
		t.Fatal("Config.DeliveryJobsAcknowledged false: jobs delivery runtime lifecycle not affirmed")
	}
	// run() wires the generic-jobs dispatcher; reproduce it so the jobs-mode construction
	// succeeds here.
	cfg.DeliveryDispatcher = jobsDispatcher(t)
	if _, err := auth.NewService(authmem.New().Repositories(), cfg); err != nil {
		t.Fatalf("auth.NewService over development wiring: %v", err)
	}
}

// TestDeliveryRuntimeStartStop proves the host-owned in-process delivery-runtime
// lifecycle (authv3-delivery-refactor AV3D-4.1): RunDelivery runs until ctx is canceled
// and then returns promptly, leaving no lingering goroutine. In jobs mode the host runs
// jobs.FencedRuntime (proven in the jobs_delivery tests); this exercises the analogous
// in_process runtime the same host offers as the small-deployment mode.
func TestDeliveryRuntimeStartStop(t *testing.T) {
	cfg, err := buildAuthConfig(quietLog(), nil)
	if err != nil {
		t.Fatalf("buildAuthConfig: %v", err)
	}
	// Flip to the self-contained bounded in-process runtime so RunDelivery owns the pool
	// lifecycle without a generic-jobs composition.
	cfg.DeliveryMode = auth.DeliveryModeInProcess
	svc, err := auth.NewService(authmem.New().Repositories(), cfg)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}

	base := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.RunDelivery(ctx) }()

	// Give the pool a moment to start its workers, then stop it.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case werr := <-done:
		if werr != nil {
			t.Fatalf("RunDelivery returned error on cancel: %v", werr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("delivery runtime did not stop within 5s of context cancel")
	}

	// The pool goroutines have returned; allow the scheduler to settle and assert the
	// count is back near baseline (no leaked worker/internal goroutine).
	for i := 0; i < 20; i++ {
		if runtime.NumGoroutine() <= base {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > base {
		t.Fatalf("goroutine leak after runtime stop: baseline %d, now %d", base, n)
	}
}
