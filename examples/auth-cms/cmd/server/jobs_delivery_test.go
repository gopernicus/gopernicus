package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/authjobs"
	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/authmem"
	auth "github.com/gopernicus/gopernicus/features/authentication"
	"github.com/gopernicus/gopernicus/features/jobs"
	jobsmem "github.com/gopernicus/gopernicus/features/jobs/memstore"
	"github.com/gopernicus/gopernicus/sdk/capabilities/email"
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

	// The generic-jobs delivery stack (mirrors run()): fenced queue -> jobs.Service ->
	// dispatcher, wired into the auth Config BEFORE building the auth Service.
	deliveryJobs, err := jobs.NewService(jobs.Repositories{FencedQueue: jobsmem.NewFencedQueue()}, jobs.Config{Logger: quietLog()})
	if err != nil {
		t.Fatalf("jobs.NewService: %v", err)
	}
	cfg.DeliveryDispatcher = authjobs.NewDispatcher(deliveryJobs)

	repos := authmem.New().Repositories()

	svc, err := auth.NewService(repos, cfg)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}

	// Construction order: only NOW — after the auth Service is fully built — is the
	// delivery processor seam read and the jobs runtime built over it.
	rt, ok := svc.DeliveryJobRuntime()
	if !ok {
		t.Fatal("DeliveryJobRuntime unavailable in jobs mode with a wired dispatcher")
	}
	runtime, err := jobs.NewFencedRuntime(deliveryJobs, authjobs.FencedRuntimeConfig(rt,
		func(c *jobs.FencedRuntimeConfig) {
			c.Logger = quietLog()
			c.PollInterval = 10 * time.Millisecond
			c.IdleInterval = 10 * time.Millisecond
		}))
	if err != nil {
		t.Fatalf("jobs.NewFencedRuntime: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// No delivery work runs until the host starts the runtime.
	if _, err := svc.RegisterUser(ctx, "e2e@example.com", "correct-horse-battery-staple", "E2E User"); err != nil {
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
