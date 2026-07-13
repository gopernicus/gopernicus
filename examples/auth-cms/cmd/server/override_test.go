package main

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/authmem"
	auth "github.com/gopernicus/gopernicus/features/authentication"
	"github.com/gopernicus/gopernicus/sdk/capabilities/email"
)

// TestOverrideSystemsAreDistinct proves the host wires BOTH override systems and that
// they are separate facilities (design §6.2/§9.2, AV3-8.9): Config.Views swaps a
// browser PAGE (the branded Login), while Config.EmailContentTemplates swaps an
// EMAIL body (the branded verification content). They are different Config fields of
// different types feeding different subsystems.
func TestOverrideSystemsAreDistinct(t *testing.T) {
	cfg, err := buildAuthConfig(quietLog(), nil)
	if err != nil {
		t.Fatalf("buildAuthConfig: %v", err)
	}
	if cfg.Views == nil {
		t.Fatal("Config.Views is nil: the host page override is not wired")
	}
	if len(cfg.EmailContentTemplates) == 0 {
		t.Fatal("Config.EmailContentTemplates empty: the host email override is not wired")
	}
	// The email override targets the feature's email namespace, not any page facility.
	if got := cfg.EmailContentTemplates[0].Namespace; got != auth.EmailContentNamespace {
		t.Fatalf("email override Namespace = %q, want %q", got, auth.EmailContentNamespace)
	}
}

// captureSender records every email it is asked to send so a test can inspect the
// rendered body. It declares no capability metadata, which development tolerates.
type captureSender struct {
	mu   sync.Mutex
	msgs []email.Message
}

func (c *captureSender) Send(_ context.Context, m email.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.msgs = append(c.msgs, m)
	return nil
}

func (c *captureSender) latest() (email.Message, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.msgs) == 0 {
		return email.Message{}, false
	}
	return c.msgs[len(c.msgs)-1], true
}

// TestEmailLayerAppOverrideWins drives a real register → durable-worker → send cycle
// on the host wiring (dev posture, the branded email override in place, a capturing
// mailer) and proves the host's LayerApp verification template WON over the feature's
// LayerCore default: the rendered body carries the Gopernicus-CMS brand copy and not
// the bundled default copy. This is the host-level demonstration that the email
// override system actually takes effect, distinct from the page Views override.
func TestEmailLayerAppOverrideWins(t *testing.T) {
	cfg, err := buildAuthConfig(quietLog(), nil)
	if err != nil {
		t.Fatalf("buildAuthConfig: %v", err)
	}
	cap := &captureSender{}
	cfg.Mailer = cap // capture the rendered verification email instead of logging it

	svc, err := auth.NewService(authmem.New().Repositories(), cfg)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// The outbox is the only send path, so run the worker to drain the verification
	// job registration enqueues.
	workerDone := make(chan error, 1)
	go func() { workerDone <- svc.RunDeliveryWorker(ctx) }()

	if _, err := svc.RegisterUser(ctx, "brand@example.com", "correct-horse-battery-staple", "Brand User"); err != nil {
		t.Fatalf("RegisterUser: %v", err)
	}

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
	case <-workerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("delivery worker did not stop within 5s")
	}

	if msg.HTML == "" {
		t.Fatal("no verification email was rendered/sent within 5s")
	}
	if !strings.Contains(msg.HTML, "Gopernicus CMS verification code") {
		t.Fatalf("verification email did not use the host LayerApp override; HTML=%q", msg.HTML)
	}
	if strings.Contains(msg.HTML, "Confirm your email address to finish setting up your account") {
		t.Fatal("verification email still carries the bundled LayerCore copy: the LayerApp override did not win")
	}
}
