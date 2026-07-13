package main

import (
	"context"
	"io"
	"log/slog"
	"runtime"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/authmem"
	auth "github.com/gopernicus/gopernicus/features/authentication"
)

// quietLog is a discarding logger so the console transports' dev WARN and the
// ephemeral-key WARNs do not spam the test output (and no key material is printed).
func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
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
	if !cfg.DeliveryWorkerAcknowledged {
		t.Fatal("Config.DeliveryWorkerAcknowledged false: worker lifecycle not affirmed")
	}
	if _, err := auth.NewService(authmem.New().Repositories(), cfg); err != nil {
		t.Fatalf("auth.NewService over development wiring: %v", err)
	}
}

// TestDeliveryWorkerStartStop proves the delivery worker lifecycle the host owns
// (design §6.1.1): RunDeliveryWorker runs until ctx is canceled and then returns
// promptly, leaving no lingering goroutine. The outbox is the only send path, so a
// worker that never stops (or leaks) on shutdown would be a real defect.
func TestDeliveryWorkerStartStop(t *testing.T) {
	cfg, err := buildAuthConfig(quietLog(), nil)
	if err != nil {
		t.Fatalf("buildAuthConfig: %v", err)
	}
	svc, err := auth.NewService(authmem.New().Repositories(), cfg)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}

	base := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.RunDeliveryWorker(ctx) }()

	// Give the worker a moment to enter its claim/idle loop, then stop it.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case werr := <-done:
		if werr != nil {
			t.Fatalf("RunDeliveryWorker returned error on cancel: %v", werr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("delivery worker did not stop within 5s of context cancel")
	}

	// The worker goroutine has returned; allow the scheduler to settle and assert the
	// count is back near baseline (no leaked worker/internal goroutine).
	for i := 0; i < 20; i++ {
		if runtime.NumGoroutine() <= base {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > base {
		t.Fatalf("goroutine leak after worker stop: baseline %d, now %d", base, n)
	}
}
