package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/authjobs"
	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/authmem"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	delivery "github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/pockets/jobs"
	jobsmem "github.com/gopernicus/gopernicus/pockets/jobs/stores/memory"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify/email"
)

// TestJobsModeDeliveryEndToEnd drives a real register → generic-jobs FencedRuntime →
// delivery-processor → send cycle over the AV3D-3.1 composition wiring the host runs:
// authentication submits an encrypted delivery command through the authjobs.Dispatcher
// to a generic jobs fenced queue, and the host-run jobs.FencedRuntime invokes auth's
// delivery processor, which renders and sends the verification email. It proves the
// whole jobs-mode path end to end on the in-memory stand-in (live stores are AV3D-3.5),
// and that construction/registration start no delivery work — only the host-run runtime
// does.
func TestJobsModeDeliveryEndToEnd(t *testing.T) {
	cfg, err := buildAuthConfig(quietLog(), nil)
	if err != nil {
		t.Fatalf("buildAuthConfig: %v", err)
	}
	cap := &captureSender{}
	cfg.Mailer = cap // capture the rendered verification email instead of logging it
	// This test IS the jobs-mode path: pin the mode rather than inherit whatever
	// AUTH_DELIVERY_MODE says, now that the seam reads it.
	cfg.DeliveryMode = delivery.ModeJobs

	// The generic-jobs delivery stack (mirrors run()): fenced queue -> queue.Service ->
	// dispatcher, wired into the auth Config BEFORE building the auth Service.
	deliveryJobs, err := jobs.New(jobs.Repositories{FencedQueue: jobsmem.NewFencedQueue()})
	if err != nil {
		t.Fatalf("jobs.NewService: %v", err)
	}
	cfg.DeliveryDispatcher = authjobs.NewDispatcher(deliveryJobs.Queue)

	repos := authmem.New().Repositories()

	svc, err := auth.New(repos, cfg.TokenSigner, cfg.RuntimeMode, cfg.DeliveryMode, cfg.options()...)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}

	// Construction order: only NOW — after the auth Service is fully built — is the
	// delivery processor seam read and the jobs runtime built over it.
	rt, ok := svc.Delivery.JobRuntime()
	if !ok {
		t.Fatal("DeliveryJobRuntime unavailable in jobs mode with a wired dispatcher")
	}
	runtime, err := runtimeFromDeliveryConfig(deliveryJobs.Queue, rt, func(c *deliveryRuntimeTestConfig) {
		c.PollInterval = 10 * time.Millisecond
		c.IdleInterval = 10 * time.Millisecond
	})
	if err != nil {
		t.Fatalf("jobs.NewFencedRuntime: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// No delivery work runs until the host starts the runtime.
	if _, err := svc.Authentication.Register(ctx, "e2e@example.com", "correct-horse-battery-staple", "E2E User"); err != nil {
		t.Fatalf("RegisterUser: %v", err)
	}
	if _, ok := cap.latest(); ok {
		t.Fatal("a message was delivered before the host started the delivery runtime")
	}

	runtimeDone := make(chan error, 1)
	go func() { runtimeDone <- runtime.Run(ctx) }()

	var msg email.Message
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if m, ok := cap.latest(); ok {
			msg = m
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case <-runtimeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("delivery runtime did not stop within 5s")
	}

	if len(msg.To) == 0 {
		t.Fatal("no verification email was delivered through the jobs-mode runtime within 5s")
	}
	if got := msg.To[0]; !strings.EqualFold(got, "e2e@example.com") {
		t.Fatalf("verification email delivered to %q, want e2e@example.com", got)
	}
	if msg.HTML == "" && msg.Text == "" {
		t.Fatal("verification email carried no rendered body")
	}
}
