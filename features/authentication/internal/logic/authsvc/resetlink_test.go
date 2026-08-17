package authsvc

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/features/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// CHAU-5.2 / CHAU-5.3 — the password-reset link rail.

// TestBuildPasswordResetURL is the pure-helper table. Every row is something a
// host can actually configure or a user can actually type.
func TestBuildPasswordResetURL(t *testing.T) {
	tests := []struct {
		name       string
		configured string
		token      string
		want       string
	}{
		{
			name:       "plain path",
			configured: "https://app.example.com/reset-password",
			token:      "abc123",
			want:       "https://app.example.com/reset-password?token=abc123",
		},
		{
			name:       "existing non-secret query is preserved",
			configured: "https://app.example.com/reset-password?app=console",
			token:      "abc123",
			want:       "https://app.example.com/reset-password?app=console&token=abc123",
		},
		{
			name:       "trailing slash is not mangled",
			configured: "https://app.example.com/reset-password/",
			token:      "abc123",
			want:       "https://app.example.com/reset-password/?token=abc123",
		},
		{
			name:       "root path",
			configured: "https://app.example.com",
			token:      "abc123",
			want:       "https://app.example.com?token=abc123",
		},
		{
			name:       "token with URL-significant bytes is escaped",
			configured: "https://app.example.com/reset-password",
			token:      "a+b/c=d&e f",
			want:       "https://app.example.com/reset-password?token=a%2Bb%2Fc%3Dd%26e+f",
		},
		{
			name:       "empty configured URL yields no link",
			configured: "",
			token:      "abc123",
			want:       "",
		},
		{
			name:       "empty token yields no link",
			configured: "https://app.example.com/reset-password",
			token:      "",
			want:       "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildPasswordResetURL(tt.configured, tt.token)
			if got != tt.want {
				t.Fatalf("buildPasswordResetURL(%q, %q) = %q, want %q", tt.configured, tt.token, got, tt.want)
			}
			if got == "" {
				return
			}
			// Whatever we produced, parsing it back must recover the EXACT token —
			// this is what the SPA does, and an escaping bug here breaks reset for
			// every user whose token happens to contain a significant byte.
			u, err := url.Parse(got)
			if err != nil {
				t.Fatalf("the produced link does not parse: %v", err)
			}
			if back := u.Query().Get(PasswordResetTokenParam); back != tt.token {
				t.Errorf("round-tripped token = %q, want %q", back, tt.token)
			}
		})
	}
}

// TestBuildPasswordResetURLDoesNotMutateConfiguration proves one send cannot leak
// its token into the next: the configured value is parsed fresh every time.
func TestBuildPasswordResetURLDoesNotMutateConfiguration(t *testing.T) {
	const configured = "https://app.example.com/reset-password?app=console"

	first := buildPasswordResetURL(configured, "token-one")
	second := buildPasswordResetURL(configured, "token-two")

	if strings.Contains(second, "token-one") {
		t.Fatalf("the second link carries the first token: %q", second)
	}
	if !strings.Contains(first, "token-one") || !strings.Contains(second, "token-two") {
		t.Fatalf("links did not carry their own tokens: %q / %q", first, second)
	}
	// The configured constant is untouched by construction (it is a string), but
	// the assertion documents the invariant a *url.URL cache would have broken.
	if configured != "https://app.example.com/reset-password?app=console" {
		t.Error("the configured URL was mutated")
	}
}

// TestPasswordResetMailCarriesConfiguredLink is the end-to-end behavior: a
// forgot-password start produces mail whose link, extracted and redeemed, actually
// resets the password — once.
func TestPasswordResetMailCarriesConfiguredLink(t *testing.T) {
	ctx := context.Background()
	const addr = "reset-link@example.com"
	const configured = "https://app.example.com/reset-password"

	h := newHarness(t, ratelimiter.NewMemory())
	h.svc.passwordResetURL = configured
	h.wireDelivery(t)
	seedVerifiedUser(t, h, addr)

	if err := h.svc.ForgotPassword(ctx, addr); err != nil {
		t.Fatalf("ForgotPassword: %v", err)
	}
	if h.mailer.count() != 1 {
		t.Fatalf("sent %d messages, want 1", h.mailer.count())
	}
	msg := h.mailer.last()

	link := extractResetLink(t, msg.HTML, configured)
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("parse link %q: %v", link, err)
	}
	token := u.Query().Get(PasswordResetTokenParam)
	if token == "" {
		t.Fatalf("the link %q carries no token parameter", link)
	}

	// The plain-text alternative must carry the full link (a user who cannot render
	// HTML still needs it) but must NOT carry a second standalone token.
	if !strings.Contains(msg.Text, link) {
		t.Errorf("the text body does not carry the link:\n%s", msg.Text)
	}
	if strings.Count(msg.Text, token) != 1 {
		t.Errorf("the text body mentions the token %d times; it must appear only inside the link:\n%s",
			strings.Count(msg.Text, token), msg.Text)
	}

	// The token from the LINK resets the password …
	if err := h.svc.ResetPassword(ctx, token, "a-brand-new-passphrase"); err != nil {
		t.Fatalf("ResetPassword with the emailed link token: %v", err)
	}
	// … exactly once.
	if err := h.svc.ResetPassword(ctx, token, "another-passphrase-here"); err == nil {
		t.Error("the reset token was accepted twice; redemption must be single-use")
	}
}

// TestPasswordResetLegacyFallback pins the development compatibility posture: with
// no configured URL the bundled template still prints the raw token, so a local
// console flow keeps working mid-migration.
func TestPasswordResetLegacyFallback(t *testing.T) {
	ctx := context.Background()
	const addr = "reset-legacy@example.com"

	h := newHarness(t, ratelimiter.NewMemory())
	h.svc.passwordResetURL = "" // the development fallback
	h.wireDelivery(t)
	seedVerifiedUser(t, h, addr)

	if err := h.svc.ForgotPassword(ctx, addr); err != nil {
		t.Fatalf("ForgotPassword: %v", err)
	}
	msg := h.mailer.last()
	if strings.Contains(msg.HTML, "href=") {
		t.Errorf("the legacy fallback rendered a link:\n%s", msg.HTML)
	}
	if !strings.Contains(strings.ToLower(msg.HTML), "token") {
		t.Errorf("the legacy fallback did not render the raw token:\n%s", msg.HTML)
	}
}

// TestPasswordResetRenderKeepsSecretAlongsideLink pins the one-window
// compatibility promise from the render side: even though the BUNDLED template is
// now link-only, the rendered envelope still carries the Secret.
//
// Two things depend on that. An app content-template override that still reads
// {{.Secret}} keeps working — Router.Render merges req.Secret into the template
// data map alongside req.Data, so both variables are in scope. And the terminal
// delivery-failure Discard path needs the secret to void the never-delivered
// challenge; a render that dropped it would leave a live reset token behind.
func TestPasswordResetRenderKeepsSecretAlongsideLink(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, ratelimiter.NewMemory())
	h.wireDelivery(t)

	const token = "the-reset-token"
	const link = "https://app.example.com/reset-password?token=" + token

	env, err := h.svc.deliver.Render(ctx, delivery.Request{
		Kind:            identity.KindEmail,
		Purpose:         delivery.PurposePasswordReset,
		Destination:     "someone@example.com",
		ResolutionInput: "someone@example.com",
		Secret:          token,
		Data:            map[string]any{"Link": link},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if env.Secret != token {
		t.Errorf("rendered envelope Secret = %q, want the issued token", env.Secret)
	}
	if !strings.Contains(env.HTML, link) {
		t.Errorf("the bundled body does not carry the link:\n%s", env.HTML)
	}
	// The bundled body is link-only: the token must appear nowhere except inside
	// the link itself.
	if strings.Count(env.HTML, token) != strings.Count(env.HTML, link) {
		t.Errorf("the bundled body prints the raw token outside the link:\n%s", env.HTML)
	}
}

// TestPasswordResetUnknownAddressSendsNothing re-pins the enumeration property
// alongside the link change: adding a link must not have added a resolution
// signal.
func TestPasswordResetUnknownAddressSendsNothing(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, ratelimiter.NewMemory())
	h.svc.passwordResetURL = "https://app.example.com/reset-password"
	h.wireDelivery(t)

	if err := h.svc.ForgotPassword(ctx, "nobody-at-all@example.com"); err != nil {
		t.Fatalf("ForgotPassword(unknown) = %v, want nil", err)
	}
	if h.mailer.count() != 0 {
		t.Errorf("an unknown address produced %d message(s)", h.mailer.count())
	}
}

// extractResetLink pulls the first occurrence of the configured base plus its
// query out of a rendered HTML body.
func extractResetLink(t *testing.T, html, base string) string {
	t.Helper()
	i := strings.Index(html, base)
	if i < 0 {
		t.Fatalf("the rendered body does not contain the configured base %q:\n%s", base, html)
	}
	rest := html[i:]
	end := strings.IndexAny(rest, "\"'< \n\t")
	if end < 0 {
		end = len(rest)
	}
	return rest[:end]
}

// seedVerifiedUser registers an account and verifies its primary email, so the
// recovery identifier the reset rail resolves is proven.
func seedVerifiedUser(t *testing.T, h *harness, addr string) {
	t.Helper()
	ctx := context.Background()
	if _, err := h.svc.Register(ctx, addr, "correct-horse-battery", "Reset Tester"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	code := extractCode(t, h.mailer.last().Text)
	if err := h.svc.Verify(ctx, addr, code); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	h.mailer.mu.Lock()
	h.mailer.sent = nil
	h.mailer.mu.Unlock()
	_ = challenge.PurposePasswordReset
}
