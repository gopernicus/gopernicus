package authsvc

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
)

// enablePasswordless flips the harness service into passwordless mode white-box:
// the enablement matrix is validated by package auth (AV3-7.1), so the internal
// tests set the resolved kind set and public base URL directly.
func enablePasswordless(h *harness, kinds ...string) {
	m := make(map[string]bool, len(kinds))
	for _, k := range kinds {
		m[k] = true
	}
	h.svc.passwordless = m
	h.svc.publicBaseURL = "https://auth.example.com"
}

// recordingLimiter records every Allow key so a test can prove the per-identifier
// AND per-IP start budgets both run (design §4.4). deny toggles refusal.
type recordingLimiter struct {
	mu   sync.Mutex
	keys []string
	deny bool
}

func (l *recordingLimiter) Allow(_ context.Context, key string, _ ratelimiter.Limit) (ratelimiter.Result, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.keys = append(l.keys, key)
	return ratelimiter.Result{Allowed: !l.deny}, nil
}
func (l *recordingLimiter) Reset(context.Context, string) error { return nil }
func (l *recordingLimiter) Close() error                        { return nil }

func (l *recordingLimiter) has(prefix string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, k := range l.keys {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}

// loginChallengeCount counts stored challenge rows of purpose.
func (h *harness) loginChallengeCount(purpose string) int {
	h.ch.mu.Lock()
	defer h.ch.mu.Unlock()
	n := 0
	for _, c := range h.ch.byID {
		if c.Purpose == purpose {
			n++
		}
	}
	return n
}

// loginBindingOf decodes the stored context of the (single) challenge of purpose.
func (h *harness) loginBindingOf(t *testing.T, purpose string) loginBinding {
	t.Helper()
	h.ch.mu.Lock()
	defer h.ch.mu.Unlock()
	for _, c := range h.ch.byID {
		if c.Purpose == purpose {
			var b loginBinding
			if err := json.Unmarshal(c.Context, &b); err != nil {
				t.Fatalf("decode login binding: %v (raw %q)", err, c.Context)
			}
			return b
		}
	}
	t.Fatalf("no challenge of purpose %q", purpose)
	return loginBinding{}
}

// TestStartPasswordlessDisabledKindOrMethod proves request-shape rejections: a
// kind the host did not enable and an unrecognized method are rejected before any
// enqueue, and neither is an account signal.
func TestStartPasswordlessDisabledKindOrMethod(t *testing.T) {
	h := newHarness(t, nil)
	enablePasswordless(h, string(identifier.KindEmail))
	ctx := context.Background()

	if err := h.svc.StartPasswordless(ctx, string(identifier.KindPhone), "+15551112222", ""); !errors.Is(err, ErrPasswordlessKindDisabled) {
		t.Errorf("disabled kind err = %v, want ErrPasswordlessKindDisabled", err)
	}
	if err := h.svc.StartPasswordless(ctx, string(identifier.KindEmail), "a@example.com", "sorcery"); !errors.Is(err, ErrPasswordlessMethodInvalid) {
		t.Errorf("invalid method err = %v, want ErrPasswordlessMethodInvalid", err)
	}
}

// TestStartPasswordlessMalformedAccepted proves a malformed identifier returns the
// same accepted (nil) outcome as a valid one and never enqueues — validity is not
// revealed (design §4.1).
func TestStartPasswordlessMalformedAccepted(t *testing.T) {
	h := newHarness(t, nil)
	enablePasswordless(h, string(identifier.KindEmail))
	repo := swapAsyncQueue(t, h)

	if err := h.svc.StartPasswordless(context.Background(), string(identifier.KindEmail), "not-an-email", ""); err != nil {
		t.Fatalf("StartPasswordless(malformed) = %v, want nil accepted", err)
	}
	if n := repo.jobCount(); n != 0 {
		t.Errorf("malformed identifier enqueued %d jobs, want 0", n)
	}
}

// TestStartPasswordlessEnqueuesOpaqueSamePath proves the enumeration contract: a
// known-verified, an unknown, and a registered-but-unverified identifier each
// enqueue exactly one OPAQUE job on the SAME request path — none is claimed, none
// carries a rendered body, and each carries only the normalized resolution input.
// Resolution happens off-path in the worker, so the three are indistinguishable.
func TestStartPasswordlessEnqueuesOpaqueSamePath(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "known@example.com", "password123456789")
	h.mustVerify(t, "known@example.com")
	h.mustRegister(t, "unverified@example.com", "password123456789") // never verified
	enablePasswordless(h, string(identifier.KindEmail))
	repo := swapAsyncQueue(t, h)

	for _, addr := range []string{"known@example.com", "unverified@example.com", "ghost@example.com"} {
		if err := h.svc.StartPasswordless(context.Background(), string(identifier.KindEmail), addr, ""); err != nil {
			t.Fatalf("StartPasswordless(%q): %v", addr, err)
		}
	}

	jobs := repo.snapshot()
	if len(jobs) != 3 {
		t.Fatalf("enqueued %d jobs, want 3 (one per identifier, same path)", len(jobs))
	}
	for _, j := range jobs {
		if j.purpose != delivery.PurposeMagicLink {
			t.Errorf("job purpose = %q, want %q (email default → link)", j.purpose, delivery.PurposeMagicLink)
		}
		if j.attempt != 0 {
			t.Errorf("job claimed on the request path (attempt=%d); resolution must be off-path", j.attempt)
		}
		// With the identity fakeEncrypter the sealed payload is the plaintext versioned
		// command envelope JSON: opaque stage, resolution input present, no rendered secret.
		payload := string(j.payload)
		if !strings.Contains(payload, `"stage":"opaque"`) {
			t.Errorf("request-path job is not opaque: %s", payload)
		}
		if strings.Contains(payload, `"secret"`) {
			t.Errorf("opaque job leaked a secret field: %s", payload)
		}
		if !strings.Contains(payload, `"resolution_input"`) {
			t.Errorf("opaque job missing resolution input: %s", payload)
		}
	}
}

// TestStartPasswordlessDoesNotResolveOnRequestPath proves the request handler never
// resolves the account: GetLogin is untouched until the worker runs off-path.
func TestStartPasswordlessDoesNotResolveOnRequestPath(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "known@example.com", "password123456789")
	h.mustVerify(t, "known@example.com")
	enablePasswordless(h, string(identifier.KindEmail))
	swapAsyncQueue(t, h)

	before := h.idents.loginCalls
	if err := h.svc.StartPasswordless(context.Background(), string(identifier.KindEmail), "known@example.com", ""); err != nil {
		t.Fatalf("StartPasswordless: %v", err)
	}
	if h.idents.loginCalls != before {
		t.Errorf("GetLogin called %d times on the request path, want 0 (resolution is off-path)", h.idents.loginCalls-before)
	}
}

// TestStartPasswordlessRunsBothBudgets proves both the per-identifier and per-IP
// start budgets run before enqueue (design §4.4).
func TestStartPasswordlessRunsBothBudgets(t *testing.T) {
	lim := &recordingLimiter{}
	h := newHarness(t, lim)
	enablePasswordless(h, string(identifier.KindEmail))

	if err := h.svc.StartPasswordless(context.Background(), string(identifier.KindEmail), "who@example.com", ""); err != nil {
		t.Fatalf("StartPasswordless: %v", err)
	}
	if !lim.has("passwordless_start:email:") {
		t.Error("per-identifier start budget not applied")
	}
	if !lim.has("passwordless_start:ip:") {
		t.Error("per-IP start budget not applied")
	}
}

// TestStartPasswordlessRateLimited proves an exhausted budget refuses with the
// generic rate-limit error and enqueues nothing.
func TestStartPasswordlessRateLimited(t *testing.T) {
	h := newHarness(t, denyLimiter{})
	enablePasswordless(h, string(identifier.KindEmail))
	repo := swapAsyncQueue(t, h)

	if err := h.svc.StartPasswordless(context.Background(), string(identifier.KindEmail), "who@example.com", ""); !errors.Is(err, ErrPasswordlessRateLimited) {
		t.Fatalf("rate-limited err = %v, want ErrPasswordlessRateLimited", err)
	}
	if n := repo.jobCount(); n != 0 {
		t.Errorf("rate-limited start enqueued %d jobs, want 0", n)
	}
}

// TestStartPasswordlessWorkerDeliversMagicLink proves the worker (off the request
// path) resolves the active verified login identifier, issues a login_magic_link
// bound to it, builds the link from PublicAuthBaseURL, and delivers it — while an
// unknown identifier produces no challenge and no send.
func TestStartPasswordlessWorkerDeliversMagicLink(t *testing.T) {
	h := newHarness(t, nil)
	u := h.mustRegister(t, "known@example.com", "password123456789")
	h.mustVerify(t, "known@example.com")
	enablePasswordless(h, string(identifier.KindEmail))
	h.mailer.sent = nil // drop the verification mail

	// Unknown identifier: same call shape, no challenge, no mail.
	if err := h.svc.StartPasswordless(context.Background(), string(identifier.KindEmail), "ghost@example.com", "link"); err != nil {
		t.Fatalf("StartPasswordless(unknown): %v", err)
	}
	if c := h.loginChallengeCount(challenge.PurposeLoginMagicLink); c != 0 {
		t.Errorf("magic-link challenge issued for unknown identifier (count=%d)", c)
	}
	if h.mailer.count() != 0 {
		t.Errorf("mail sent for unknown identifier (enumeration leak)")
	}

	// Known verified identifier: link issued, bound, and delivered.
	if err := h.svc.StartPasswordless(context.Background(), string(identifier.KindEmail), "Known@Example.com", "link"); err != nil {
		t.Fatalf("StartPasswordless(known): %v", err)
	}
	if c := h.loginChallengeCount(challenge.PurposeLoginMagicLink); c != 1 {
		t.Fatalf("magic-link challenge count = %d, want 1", c)
	}
	b := h.loginBindingOf(t, challenge.PurposeLoginMagicLink)
	if b.NormalizedValue != "known@example.com" || b.Kind != string(identifier.KindEmail) || b.IdentifierID == "" {
		t.Errorf("login binding = %+v, want the resolved known email identifier of %s", b, u.ID)
	}
	if h.mailer.count() != 1 {
		t.Fatalf("mail count = %d, want 1", h.mailer.count())
	}
	msg := h.mailer.last()
	if len(msg.To) != 1 || msg.To[0] != "known@example.com" {
		t.Errorf("mail To = %v, want known@example.com", msg.To)
	}
	if !strings.Contains(msg.Text, "https://auth.example.com#token=") {
		t.Errorf("magic link not built from PublicAuthBaseURL fragment; body = %q", msg.Text)
	}
}

// TestStartPasswordlessCodeMethodEmail proves the explicit code method issues a
// login_otp and delivers a sign-in code over the email transport.
func TestStartPasswordlessCodeMethodEmail(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "otp@example.com", "password123456789")
	h.mustVerify(t, "otp@example.com")
	enablePasswordless(h, string(identifier.KindEmail))
	h.mailer.sent = nil

	if err := h.svc.StartPasswordless(context.Background(), string(identifier.KindEmail), "otp@example.com", "code"); err != nil {
		t.Fatalf("StartPasswordless(code): %v", err)
	}
	if c := h.loginChallengeCount(challenge.PurposeLoginOTP); c != 1 {
		t.Fatalf("login_otp challenge count = %d, want 1", c)
	}
	if h.loginChallengeCount(challenge.PurposeLoginMagicLink) != 0 {
		t.Error("code method issued a magic-link challenge")
	}
	if h.mailer.count() != 1 {
		t.Fatalf("mail count = %d, want 1", h.mailer.count())
	}
	if code := extractCode(t, h.mailer.last().Text); len(code) != 6 {
		t.Errorf("sign-in code = %q, want six digits", code)
	}
}

// TestStartPasswordlessPhoneDefaultsToCode proves phone defaults to the OTP method
// and delivers over SMS to a verified login-enabled phone identifier.
func TestStartPasswordlessPhoneDefaultsToCode(t *testing.T) {
	h := newHarness(t, nil)
	h.enablePhone(t)
	enablePasswordless(h, string(identifier.KindPhone))
	now := time.Now()
	h.users.byID["phoneuser"] = user.User{ID: "phoneuser"}
	h.idents.byID["ph1"] = identifier.Identifier{
		ID: "ph1", UserID: "phoneuser", Kind: identifier.KindPhone, NormalizedValue: "+15551112222",
		VerifiedAt: now, LoginEnabled: true, CreatedAt: now,
	}

	if err := h.svc.StartPasswordless(context.Background(), string(identifier.KindPhone), "+1 (555) 111-2222", ""); err != nil {
		t.Fatalf("StartPasswordless(phone): %v", err)
	}
	if c := h.loginChallengeCount(challenge.PurposeLoginOTP); c != 1 {
		t.Fatalf("login_otp challenge count = %d, want 1", c)
	}
	code := h.phone.codeFor(t, "+15551112222")
	if len(code) != 6 {
		t.Errorf("SMS sign-in code = %q, want six digits", code)
	}
}

// TestStartPasswordlessUnverifiedNoSend proves a registered-but-unverified login
// identifier resolves nothing off-path: no challenge, no send (design §2.3/V3).
func TestStartPasswordlessUnverifiedNoSend(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "unverified@example.com", "password123456789") // not verified
	enablePasswordless(h, string(identifier.KindEmail))
	h.mailer.sent = nil

	if err := h.svc.StartPasswordless(context.Background(), string(identifier.KindEmail), "unverified@example.com", "link"); err != nil {
		t.Fatalf("StartPasswordless(unverified): %v", err)
	}
	if c := h.loginChallengeCount(challenge.PurposeLoginMagicLink); c != 0 {
		t.Errorf("challenge issued for unverified identifier (count=%d)", c)
	}
	if h.mailer.count() != 0 {
		t.Errorf("mail sent for unverified identifier")
	}
}

// swapAsyncQueue replaces the harness's synchronous draining queue with a plain
// async delivery.Service over a fresh in-mem dispatcher, so a start only SUBMITS (the
// processor never runs on the request path) and the test can inspect the raw
// commands.
func swapAsyncQueue(t *testing.T, h *harness) *memDispatcher {
	t.Helper()
	disp := newMemDispatcher()
	dsvc, err := delivery.NewService(delivery.ServiceDeps{Dispatcher: disp, Encrypter: fakeEncrypter{}})
	if err != nil {
		t.Fatalf("delivery.NewService: %v", err)
	}
	h.svc.queue = dsvc
	return disp
}

// jobCount returns the number of submitted commands.
func (m *memDispatcher) jobCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.items)
}
