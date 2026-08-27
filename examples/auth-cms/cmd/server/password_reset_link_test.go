package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/authmem"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/sdk/capabilities/email"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
)

// CHAU-5.3 — the password-reset link rail, driven over real HTTP through exported
// host seams.
//
// The load-bearing assertion is that the token extracted FROM THE DELIVERED MAIL
// actually completes a reset and the user can then log in with the new password.
// Finding an `href=` would prove nothing.

const (
	resetLandingURL = "https://app.example.com/reset-password"
	newPassword     = "a-brand-new-passphrase-99"
)

// resetLinkPattern finds the configured landing URL and its query in a body.
var resetLinkPattern = regexp.MustCompile(`https://app\.example\.com/reset-password\?[^\s"'<]+`)

// newResetHost boots the host with a configured reset landing URL.
func newResetHost(t *testing.T) *linkHost {
	t.Helper()
	sender := &recordingSender{}
	svc := bootInProcess(t, sender, func(cfg *auth.Config) {
		cfg.PasswordResetURL = resetLandingURL
	})
	router := mountInProcess(t, svc)
	stop := runDelivery(t, svc)
	t.Cleanup(stop)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	return &linkHost{t: t, srv: srv, svc: svc, sender: sender, origin: allowedOrigins()[0]}
}

// awaitResetLink waits for a reset message addressed to recipient and returns the
// landing link it carries.
func (h *linkHost) awaitResetLink(t *testing.T, before int, recipient string) string {
	t.Helper()
	deadline := 200
	for range deadline {
		msgs := h.sender.all()
		for i := len(msgs) - 1; i >= before; i-- {
			m := msgs[i]
			if !addressedTo(m.To, recipient) {
				continue
			}
			if link := resetLinkPattern.FindString(m.Text + " " + m.HTML); link != "" {
				return link
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("no reset link delivered to %s", recipient)
	return ""
}

// durableStubLimiter satisfies the limiter port while declaring itself
// shared/durable, so a production-mode construction is not stopped by the
// in-process-limiter gate before it reaches the reset-URL check under test.
type durableStubLimiter struct{}

func (durableStubLimiter) Allow(context.Context, string, ratelimiter.Limit) (ratelimiter.Result, error) {
	return ratelimiter.Result{Allowed: true}, nil
}
func (durableStubLimiter) Reset(context.Context, string) error { return nil }
func (durableStubLimiter) Close() error                        { return nil }

// productionSender declares production-capable transport metadata so a
// production-mode construction reaches the reset-URL check rather than stopping
// at the transport gate. It records nothing — these cases never send.
type productionSender struct{}

func (productionSender) Send(context.Context, email.Message) error { return nil }
func (productionSender) Capabilities() email.Capabilities {
	return email.Capabilities{TransportSecurity: email.TransportSecurityTLS}
}

// TestPasswordResetLinkCompletesTheFlow is the end-to-end walk.
func TestPasswordResetLinkCompletesTheFlow(t *testing.T) {
	host := newResetHost(t)
	const addr = "reset-flow@example.com"
	c := host.signUp(addr)

	before := host.sender.count()
	if resp, body := c.do("POST", "/auth/password/forgot", `{"email":"`+addr+`"}`, nil); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("forgot = %d, want 202; body=%s", resp.StatusCode, body)
	}

	link := host.awaitResetLink(t, before, addr)
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("parse %q: %v", link, err)
	}
	token := u.Query().Get(auth.PasswordResetTokenParam)
	if token == "" {
		t.Fatalf("the delivered link %q carries no token", link)
	}
	if u.Scheme != "https" || u.Host != "app.example.com" || u.Path != "/reset-password" {
		t.Fatalf("the link points somewhere unexpected: %q", link)
	}

	// The token from the LINK completes the reset …
	if resp, body := c.do("POST", "/auth/password/reset",
		`{"token":"`+token+`","password":"`+newPassword+`"}`, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("reset = %d, want 200; body=%s", resp.StatusCode, body)
	}
	// … replay fails …
	if resp, _ := c.do("POST", "/auth/password/reset",
		`{"token":"`+token+`","password":"yet-another-passphrase"}`, nil); resp.StatusCode == http.StatusOK {
		t.Error("the reset token was accepted twice")
	}
	// … the OLD password no longer works …
	fresh := host.newClient()
	if resp, _ := fresh.do("POST", "/auth/login", `{"email":"`+addr+`","password":"`+linkPassword+`"}`, nil); resp.StatusCode == http.StatusOK {
		t.Error("the old password still logs in after a reset")
	}
	// … and the NEW one does.
	if resp, body := fresh.do("POST", "/auth/login", `{"email":"`+addr+`","password":"`+newPassword+`"}`, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("login with the new password = %d, want 200; body=%s", resp.StatusCode, body)
	}
}

// TestPasswordResetLinkIgnoresRequestHeaders is the host-header isolation proof.
// An attacker who can set Host / X-Forwarded-Host / Forwarded on a
// forgot-password request must NOT be able to steer the emailed link at their own
// origin — that is a full account-takeover primitive.
func TestPasswordResetLinkIgnoresRequestHeaders(t *testing.T) {
	host := newResetHost(t)
	const addr = "reset-hostile@example.com"
	c := host.signUp(addr)

	hostile := http.Header{
		"X-Forwarded-Host":  {"evil.example"},
		"X-Forwarded-Proto": {"http"},
		"Forwarded":         {"host=evil.example;proto=http"},
		"X-Original-Host":   {"evil.example"},
	}

	before := host.sender.count()
	req, err := http.NewRequest("POST", host.srv.URL+"/auth/password/forgot", strings.NewReader(`{"email":"`+addr+`"}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Origin", host.origin)
	req.Header.Set("Content-Type", "application/json")
	for k, vs := range hostile {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	// Host is a special field on the request, not a header map entry.
	req.Host = "evil.example"

	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("forgot: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("forgot = %d, want 202", resp.StatusCode)
	}

	link := host.awaitResetLink(t, before, addr)
	if strings.Contains(link, "evil.example") {
		t.Fatalf("the reset link followed a request header: %q", link)
	}
	if !strings.HasPrefix(link, resetLandingURL+"?") {
		t.Fatalf("the reset link is not the configured landing URL: %q", link)
	}
}

// TestPasswordResetProductionRequiresTheURL pins the ratified compatibility
// posture at construction: production refuses to start without it, development
// starts (with a warning) and keeps the legacy raw-token mail.
func TestPasswordResetProductionRequiresTheURL(t *testing.T) {
	base, err := buildAuthConfig(quietLog(), nil)
	if err != nil {
		t.Fatalf("buildAuthConfig: %v", err)
	}
	base.DeliveryMode = auth.DeliveryModeInProcess
	base.DeliveryJobsAcknowledged = false
	base.DeliveryEphemeralAcknowledged = true

	t.Run("production without a reset URL fails loudly", func(t *testing.T) {
		cfg := base
		cfg.RuntimeMode = auth.RuntimeModeProduction
		cfg.PasswordResetURL = ""
		cfg.Mailer = productionSender{} // otherwise the transport gate stops us first
		cfg.Notifiers = nil
		cfg.Passwordless = nil
		cfg.RateLimiter = durableStubLimiter{}

		_, err := auth.NewService(authmem.New().Repositories(), cfg)
		if !errors.Is(err, auth.ErrPasswordResetURLRequired) {
			t.Fatalf("NewService = %v, want ErrPasswordResetURLRequired", err)
		}
	})

	t.Run("production rejects a plain-http reset URL", func(t *testing.T) {
		cfg := base
		cfg.RuntimeMode = auth.RuntimeModeProduction
		cfg.PasswordResetURL = "http://app.example.com/reset-password"
		cfg.Mailer = productionSender{}
		cfg.Notifiers = nil
		cfg.Passwordless = nil
		cfg.RateLimiter = durableStubLimiter{}

		_, err := auth.NewService(authmem.New().Repositories(), cfg)
		if !errors.Is(err, auth.ErrPasswordResetURLInsecure) {
			t.Fatalf("NewService = %v, want ErrPasswordResetURLInsecure", err)
		}
	})

	t.Run("development without a reset URL still constructs", func(t *testing.T) {
		cfg := base
		cfg.RuntimeMode = auth.RuntimeModeDevelopment
		cfg.PasswordResetURL = ""
		if _, err := auth.NewService(authmem.New().Repositories(), cfg); err != nil {
			t.Fatalf("NewService (development) = %v, want nil", err)
		}
	})

	t.Run("a malformed reset URL fails in every mode", func(t *testing.T) {
		cfg := base
		cfg.RuntimeMode = auth.RuntimeModeDevelopment
		cfg.PasswordResetURL = "https://app.example.com/reset#step2"
		if _, err := auth.NewService(authmem.New().Repositories(), cfg); !errors.Is(err, auth.ErrPasswordResetURLInvalid) {
			t.Fatalf("NewService = %v, want ErrPasswordResetURLInvalid", err)
		}
	})
}
