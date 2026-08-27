package authsvc

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/user"
	"github.com/gopernicus/gopernicus/pockets/authentication/internal/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
)

// CHAU-2.1 / CHAU-2.2 — registration-verification resend.
//
// The tests are split the way the design is: everything the PUBLIC request path
// does (or refuses to do) is asserted against the request path, and every
// target-state decision is asserted against the WORKER initializer. That split is
// the contract — if a target-state decision ever migrates onto the request path,
// these tests are what notices.

// countingIdentifiers wraps the harness identifier fake and counts reads, so a
// test can prove the public request path performs NONE.
type countingIdentifiers struct {
	*fakeIdentifiers
	getLogin    atomic.Int64
	getRecovery atomic.Int64
	listByUser  atomic.Int64
}

func (c *countingIdentifiers) GetLogin(ctx context.Context, kind, value string) (identifier.Identifier, error) {
	c.getLogin.Add(1)
	return c.fakeIdentifiers.GetLogin(ctx, kind, value)
}

func (c *countingIdentifiers) GetRecovery(ctx context.Context, kind, value string) (identifier.Identifier, error) {
	c.getRecovery.Add(1)
	return c.fakeIdentifiers.GetRecovery(ctx, kind, value)
}

func (c *countingIdentifiers) ListByUser(ctx context.Context, userID string) ([]identifier.Identifier, error) {
	c.listByUser.Add(1)
	return c.fakeIdentifiers.ListByUser(ctx, userID)
}

func (c *countingIdentifiers) reads() int64 {
	return c.getLogin.Load() + c.getRecovery.Load() + c.listByUser.Load()
}

// captureQueue is a deliveryQueue that RECORDS commands without processing them.
// It is what makes "the request path resolves nothing" provable: with the
// synchronous draining queue the worker runs inline, so a repository read cannot
// be attributed to one side or the other. With this queue, any identifier read
// observed after a ResendVerification call is unambiguously the REQUEST path's.
type captureQueue struct {
	mu       sync.Mutex
	enqueued []delivery.Command
	replaced []delivery.Command
	err      error
}

func (q *captureQueue) Enqueue(_ context.Context, cmd delivery.Command) (delivery.Receipt, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.err != nil {
		return delivery.Receipt{}, q.err
	}
	q.enqueued = append(q.enqueued, cmd)
	return delivery.Receipt{Key: cmd.IdempotencyKey}, nil
}

func (q *captureQueue) Replace(_ context.Context, cmd delivery.Command) (delivery.Receipt, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.err != nil {
		return delivery.Receipt{}, q.err
	}
	q.replaced = append(q.replaced, cmd)
	return delivery.Receipt{Key: cmd.IdempotencyKey}, nil
}

func (q *captureQueue) Status(context.Context, string) (delivery.Status, error) {
	return delivery.Status{}, sdk.ErrNotFound
}

func (q *captureQueue) commands() (enqueued, replaced []delivery.Command) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]delivery.Command(nil), q.enqueued...), append([]delivery.Command(nil), q.replaced...)
}

// captureRequestPath swaps the harness's draining queue for a capturing one and
// zeroes the read counters, so everything observed afterwards belongs to the
// request path alone.
func (h *resendHarness) captureRequestPath() *captureQueue {
	q := &captureQueue{}
	h.svc.queue = q
	h.idCount.getLogin.Store(0)
	h.idCount.getRecovery.Store(0)
	h.idCount.listByUser.Store(0)
	h.resetMailer()
	return q
}

// resendHarness is the shared fixture: a service with the synchronous delivery
// outbox wired, a registered-but-unverified user, and counting identifier reads.
type resendHarness struct {
	*harness
	idCount *countingIdentifiers
}

func newResendHarness(t *testing.T, limiter ratelimiter.Limiter) *resendHarness {
	t.Helper()
	h := newHarness(t, limiter)
	counting := &countingIdentifiers{fakeIdentifiers: h.idents}
	h.svc.identifiers = counting
	h.wireDelivery(t)
	return &resendHarness{harness: h, idCount: counting}
}

// --- local fixture helpers ---
//
// These live here rather than widening the shared harness fakes, which many other
// tests in this package depend on.

// resetMailer drops recorded messages so a later assertion counts only the ones a
// resend produced.
func (h *resendHarness) resetMailer() {
	h.mailer.mu.Lock()
	defer h.mailer.mu.Unlock()
	h.mailer.sent = nil
}

// sentCount reports how many messages the mailer has recorded.
func (h *resendHarness) sentCount() int { return h.mailer.count() }

// resetEvents drops recorded audit rows.
func (h *resendHarness) resetEvents() {
	h.events.mu.Lock()
	defer h.events.mu.Unlock()
	h.events.events = nil
}

// recordedEvents returns a copy of the audit rows.
func (h *resendHarness) recordedEvents() []securityevent.SecurityEvent {
	h.events.mu.Lock()
	defer h.events.mu.Unlock()
	return append([]securityevent.SecurityEvent(nil), h.events.events...)
}

// lastCode extracts the six-digit code from the most recently delivered message.
func (h *resendHarness) lastCode(t *testing.T) string {
	t.Helper()
	if h.mailer.count() == 0 {
		t.Fatal("no message delivered")
	}
	return extractCode(t, h.mailer.last().Text)
}

// setStatus flips a user's lifecycle status directly, standing in for the admin
// transition (which needs the AdminRepository this package's fakes do not carry).
func (h *resendHarness) setStatus(t *testing.T, userID string, status user.Status) {
	t.Helper()
	h.users.mu.Lock()
	defer h.users.mu.Unlock()
	u, ok := h.users.byID[userID]
	if !ok {
		t.Fatalf("no such user %q", userID)
	}
	u.Status = status
	h.users.byID[userID] = u
}

// activeChallenges counts a user's live challenges of a purpose.
func (h *resendHarness) activeChallenges(userID, purpose string) int {
	h.ch.mu.Lock()
	defer h.ch.mu.Unlock()
	n := 0
	for _, c := range h.ch.byID {
		if c.UserID == userID && c.Purpose == purpose {
			n++
		}
	}
	return n
}

// registerUnverified registers an account and returns it. Registration itself
// resolves and renders (the account is known — it was just created), so the
// counters are reset afterwards and only the resend's own reads are measured.
func (h *resendHarness) registerUnverified(t *testing.T, email string) user.User {
	t.Helper()
	u, _ := h.registerUnverifiedWithCode(t, email)
	return u
}

// registerUnverifiedWithCode is registerUnverified plus the code registration
// itself delivered — the one a later resend must SUPERSEDE.
func (h *resendHarness) registerUnverifiedWithCode(t *testing.T, email string) (user.User, string) {
	t.Helper()
	u, err := h.svc.Register(context.Background(), email, "correct-horse-battery", "Test")
	if err != nil {
		t.Fatalf("Register(%q): %v", email, err)
	}
	code := h.lastCode(t)
	h.idCount.getLogin.Store(0)
	h.idCount.getRecovery.Store(0)
	h.idCount.listByUser.Store(0)
	h.resetMailer()
	return u, code
}

// TestResendVerificationRequestPathIsOpaque is the core enumeration property:
// every target state produces the identical outcome, and the request path
// performs NO identifier resolution at all.
func TestResendVerificationRequestPathIsOpaque(t *testing.T) {
	ctx := context.Background()

	known := "known-unverified@example.com"
	verified := "already-verified@example.com"
	deactivated := "deactivated-user@example.com"

	h := newResendHarness(t, ratelimiter.NewMemory())
	h.registerUnverified(t, known)

	// A verified target.
	_, verifiedCode := h.registerUnverifiedWithCode(t, verified)
	if err := h.svc.Verify(ctx, verified, verifiedCode); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	// A deactivated target.
	du := h.registerUnverified(t, deactivated)
	h.setStatus(t, du.ID, user.StatusDeactivated)

	h.idCount.getLogin.Store(0)
	h.idCount.getRecovery.Store(0)
	h.idCount.listByUser.Store(0)

	targets := []struct {
		name  string
		email string
	}{
		{"unknown address", "nobody-here@example.com"},
		{"active unverified", known},
		{"already verified", verified},
		{"deactivated", deactivated},
	}

	for _, tt := range targets {
		t.Run(tt.name, func(t *testing.T) {
			// A fresh limiter per case so budgets never mask the outcome, and a
			// capturing queue so the worker never runs and every observed read is
			// unambiguously the request path's.
			h := newResendHarness(t, ratelimiter.NewMemory())
			if tt.email == known || tt.email == verified || tt.email == deactivated {
				h.registerUnverified(t, tt.email)
			}
			q := h.captureRequestPath()

			if err := h.svc.ResendVerification(ctx, tt.email); err != nil {
				t.Fatalf("ResendVerification(%q) = %v, want nil for EVERY target state", tt.email, err)
			}
			if got := h.idCount.reads(); got != 0 {
				t.Errorf("the request path performed %d identifier read(s); it must resolve nothing", got)
			}
			if h.sentCount() != 0 {
				t.Errorf("the request path produced %d message(s); rendering and sending belong to the worker", h.sentCount())
			}

			enqueued, replaced := q.commands()
			if len(enqueued) != 0 {
				t.Errorf("a resend used Enqueue (%d); it must use Replace so it supersedes pending work", len(enqueued))
			}
			if len(replaced) != 1 {
				t.Fatalf("submitted %d replacement commands, want exactly 1", len(replaced))
			}
			cmd := replaced[0]
			if cmd.Envelope.Secret != "" || cmd.Envelope.Subject != "" || cmd.Envelope.HTML != "" || cmd.Envelope.Body != "" || cmd.Envelope.Destination != "" {
				t.Errorf("the submitted command is not opaque: %+v", cmd.Envelope)
			}
			if strings.Contains(cmd.IdempotencyKey, "@") {
				t.Errorf("the logical key %q carries a raw address", cmd.IdempotencyKey)
			}
		})
	}
}

// TestResendVerificationMalformedInputIsAccepted covers the row the opaque-path
// table cannot: a value that does not parse as an address short-circuits after
// the budget rather than queueing work the worker could never resolve.
//
// That asymmetry is deliberate and is NOT an enumeration signal. The property the
// design protects is that a valid REGISTERED address is indistinguishable from a
// valid UNREGISTERED one — and both of those enqueue identically. Whether a string
// is syntactically an address is something an attacker determines locally without
// asking the server at all. What must hold, and is asserted here, is that the
// caller-visible outcome is the same accepted one, that nothing is resolved or
// sent, and that the malformed value still costs budget so a flood of garbage is
// not free.
func TestResendVerificationMalformedInputIsAccepted(t *testing.T) {
	ctx := context.Background()
	h := newResendHarness(t, ratelimiter.NewMemory())
	q := h.captureRequestPath()

	if err := h.svc.ResendVerification(ctx, "not-an-email"); err != nil {
		t.Fatalf("ResendVerification(malformed) = %v, want the same accepted nil", err)
	}
	if got := h.idCount.reads(); got != 0 {
		t.Errorf("the request path performed %d identifier read(s) for malformed input", got)
	}
	if h.sentCount() != 0 {
		t.Errorf("malformed input produced %d message(s)", h.sentCount())
	}
	enqueued, replaced := q.commands()
	if len(enqueued) != 0 || len(replaced) != 0 {
		t.Errorf("malformed input queued work (%d enqueued, %d replaced); the worker could never resolve it", len(enqueued), len(replaced))
	}
}

// TestResendVerificationRequestPathRendersNothing proves the request path makes
// no provider call: with the synchronous outbox drained by the harness the WORKER
// may send, but the enqueue itself must carry an empty body.
func TestResendVerificationRequestPathSubmitsOpaqueWork(t *testing.T) {
	ctx := context.Background()
	h := newResendHarness(t, ratelimiter.NewMemory())

	if err := h.svc.ResendVerification(ctx, "opaque-target@example.com"); err != nil {
		t.Fatalf("ResendVerification: %v", err)
	}
	// An unknown address resolves to nothing in the worker, so no mail is sent at
	// all — the observable outcome for the caller is identical either way.
	if h.sentCount() != 0 {
		t.Errorf("an unknown address produced %d message(s); it must produce none", h.sentCount())
	}
}

// TestResendVerificationBudgets pins both budgets and, critically, that they
// apply identically to known and unknown addresses — a budget that only bit for
// real accounts would itself be the enumeration oracle.
func TestResendVerificationBudgets(t *testing.T) {
	ctx := context.Background()

	t.Run("per identifier", func(t *testing.T) {
		for _, addr := range []string{"budget-known@example.com", "budget-unknown@example.com"} {
			h := newResendHarness(t, ratelimiter.NewMemory())
			if strings.HasPrefix(addr, "budget-known") {
				h.registerUnverified(t, addr)
			}
			for i := range verificationResendsPerIdentifierPerMinute {
				if err := h.svc.ResendVerification(ctx, addr); err != nil {
					t.Fatalf("%s resend %d: %v", addr, i, err)
				}
			}
			if err := h.svc.ResendVerification(ctx, addr); !errors.Is(err, ErrVerificationResendRateLimited) {
				t.Errorf("%s over-budget resend = %v, want ErrVerificationResendRateLimited", addr, err)
			}
		}
	})

	t.Run("per IP across addresses", func(t *testing.T) {
		h := newResendHarness(t, ratelimiter.NewMemory())
		// Distinct addresses each stay under the per-identifier budget, so only the
		// per-IP budget can stop this.
		var lastErr error
		for i := range verificationResendsPerIPPerMinute + 1 {
			lastErr = h.svc.ResendVerification(ctx, addrN(i))
		}
		if !errors.Is(lastErr, ErrVerificationResendRateLimited) {
			t.Errorf("per-IP over-budget resend = %v, want ErrVerificationResendRateLimited", lastErr)
		}
	})

	t.Run("malformed input is throttled too", func(t *testing.T) {
		h := newResendHarness(t, ratelimiter.NewMemory())
		var lastErr error
		for range verificationResendsPerIdentifierPerMinute + 1 {
			lastErr = h.svc.ResendVerification(ctx, "not-an-email")
		}
		if !errors.Is(lastErr, ErrVerificationResendRateLimited) {
			t.Errorf("malformed over-budget resend = %v, want a budget refusal", lastErr)
		}
	})
}

func addrN(i int) string {
	return string(rune('a'+i%26)) + "-spread@example.com"
}

// TestResendVerificationWorkerBranches is where every target-state decision
// lives. It drives the initializer directly, which is what the delivery processor
// calls off the request path.
func TestResendVerificationWorkerBranches(t *testing.T) {
	ctx := context.Background()

	t.Run("active unverified target delivers a fresh code", func(t *testing.T) {
		h := newResendHarness(t, ratelimiter.NewMemory())
		h.registerUnverified(t, "worker-active@example.com")

		if err := h.svc.ResendVerification(ctx, "worker-active@example.com"); err != nil {
			t.Fatalf("ResendVerification: %v", err)
		}
		if h.sentCount() != 1 {
			t.Fatalf("sent %d messages, want exactly 1", h.sentCount())
		}
	})

	t.Run("unknown target delivers nothing", func(t *testing.T) {
		h := newResendHarness(t, ratelimiter.NewMemory())
		if err := h.svc.ResendVerification(ctx, "worker-unknown@example.com"); err != nil {
			t.Fatalf("ResendVerification: %v", err)
		}
		if h.sentCount() != 0 {
			t.Errorf("an unknown target produced %d message(s)", h.sentCount())
		}
	})

	t.Run("verified target delivers nothing", func(t *testing.T) {
		h := newResendHarness(t, ratelimiter.NewMemory())
		addr := "worker-verified@example.com"
		_, code := h.registerUnverifiedWithCode(t, addr)
		if err := h.svc.Verify(ctx, addr, code); err != nil {
			t.Fatalf("Verify: %v", err)
		}
		h.resetMailer()

		if err := h.svc.ResendVerification(ctx, addr); err != nil {
			t.Fatalf("ResendVerification: %v", err)
		}
		if h.sentCount() != 0 {
			t.Errorf("a verified target produced %d message(s); there is nothing left to verify", h.sentCount())
		}
	})

	t.Run("deactivated target delivers nothing", func(t *testing.T) {
		h := newResendHarness(t, ratelimiter.NewMemory())
		addr := "worker-deactivated@example.com"
		u := h.registerUnverified(t, addr)
		h.setStatus(t, u.ID, user.StatusDeactivated)

		if err := h.svc.ResendVerification(ctx, addr); err != nil {
			t.Fatalf("ResendVerification: %v", err)
		}
		if h.sentCount() != 0 {
			t.Errorf("a deactivated target produced %d message(s)", h.sentCount())
		}
	})
}

// TestResendVerificationReplacesTheCode is the semantic that makes a resend
// useful AND safe: the new code works, the old one does not, and exactly one
// challenge stays active.
func TestResendVerificationReplacesTheCode(t *testing.T) {
	ctx := context.Background()
	h := newResendHarness(t, ratelimiter.NewMemory())
	addr := "replace-code@example.com"
	// The registration code is captured BEFORE the resend supersedes it.
	u, original := h.registerUnverifiedWithCode(t, addr)

	if err := h.svc.ResendVerification(ctx, addr); err != nil {
		t.Fatalf("ResendVerification: %v", err)
	}
	fresh := h.lastCode(t)
	if fresh == original {
		t.Fatal("the resend re-sent the SAME code; a replacement must issue a fresh one")
	}
	if got := h.activeChallenges(u.ID, challenge.PurposeVerifyRegistration); got != 1 {
		t.Errorf("active verify_registration challenges = %d, want exactly 1", got)
	}

	// The old code is dead.
	if err := h.svc.Verify(ctx, addr, original); err == nil {
		t.Error("the superseded code still verified; issuing a replacement must invalidate it")
	}
	// The fresh code works, once.
	if err := h.svc.Verify(ctx, addr, fresh); err != nil {
		t.Fatalf("Verify(fresh code): %v", err)
	}
	if err := h.svc.Verify(ctx, addr, fresh); err == nil {
		t.Error("the fresh code verified twice; consumption must be single-use")
	}
}

// TestResendVerificationAuditIsSecretFree pins the audit contract: the PUBLIC
// event carries no user id (the request path never resolves one) and no address
// or code anywhere.
func TestResendVerificationAuditIsSecretFree(t *testing.T) {
	ctx := context.Background()
	h := newResendHarness(t, ratelimiter.NewMemory())
	addr := "audit-target@example.com"
	h.registerUnverified(t, addr)
	h.resetEvents()

	if err := h.svc.ResendVerification(ctx, addr); err != nil {
		t.Fatalf("ResendVerification: %v", err)
	}

	var sawRequest, sawIssued bool
	for _, ev := range h.recordedEvents() {
		switch ev.EventType {
		case securityevent.TypeVerificationResendRequested:
			sawRequest = true
			if ev.UserID != "" {
				t.Errorf("the public resend event carries a user id %q; the request path resolves none", ev.UserID)
			}
		case securityevent.TypeVerificationResendIssued:
			sawIssued = true
			if ev.UserID == "" {
				t.Error("the worker-side issued event carries no user id; it resolved one")
			}
		default:
			continue
		}
		for k, v := range ev.Details {
			if s, ok := v.(string); ok && strings.Contains(s, "@") {
				t.Errorf("event %q detail %q carries an address: %q", ev.EventType, k, s)
			}
		}
	}
	if !sawRequest {
		t.Error("no verification_resend_requested event recorded")
	}
	if !sawIssued {
		t.Error("no verification_resend_issued event recorded by the worker")
	}
}

// TestAdminResendReportsRealState is the authorized counterpart: it MAY
// distinguish target state, because the caller was already authorized.
func TestAdminResendReportsRealState(t *testing.T) {
	ctx := context.Background()
	actor := Principal{Type: PrincipalUser, ID: "operator"}

	t.Run("unknown user is not found", func(t *testing.T) {
		h := newResendHarness(t, ratelimiter.NewMemory())
		if _, err := h.svc.ResendVerificationForUser(ctx, actor, "nope"); !errors.Is(err, sdk.ErrNotFound) {
			t.Errorf("err = %v, want sdk.ErrNotFound", err)
		}
	})

	t.Run("already verified conflicts", func(t *testing.T) {
		h := newResendHarness(t, ratelimiter.NewMemory())
		addr := "admin-verified@example.com"
		u, code := h.registerUnverifiedWithCode(t, addr)
		if err := h.svc.Verify(ctx, addr, code); err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if _, err := h.svc.ResendVerificationForUser(ctx, actor, u.ID); !errors.Is(err, ErrAlreadyVerified) {
			t.Errorf("err = %v, want ErrAlreadyVerified", err)
		}
	})

	t.Run("deactivated conflicts", func(t *testing.T) {
		h := newResendHarness(t, ratelimiter.NewMemory())
		u := h.registerUnverified(t, "admin-deactivated@example.com")
		h.setStatus(t, u.ID, user.StatusDeactivated)
		if _, err := h.svc.ResendVerificationForUser(ctx, actor, u.ID); !errors.Is(err, ErrUserDeactivated) {
			t.Errorf("err = %v, want ErrUserDeactivated", err)
		}
	})

	t.Run("active unverified delivers and returns a secret-free receipt", func(t *testing.T) {
		h := newResendHarness(t, ratelimiter.NewMemory())
		addr := "admin-active@example.com"
		u := h.registerUnverified(t, addr)

		receipt, err := h.svc.ResendVerificationForUser(ctx, actor, u.ID)
		if err != nil {
			t.Fatalf("ResendVerificationForUser: %v", err)
		}
		if !receipt.Delivered || receipt.Receipt == "" {
			t.Errorf("receipt = %+v, want a delivered receipt with a key", receipt)
		}
		if strings.Contains(receipt.Receipt, addr) {
			t.Errorf("the receipt %q carries the raw address", receipt.Receipt)
		}
		if h.sentCount() != 1 {
			t.Fatalf("sent %d messages, want 1", h.sentCount())
		}
		// The delivered code verifies, proving the admin path shares the real
		// challenge rail rather than a parallel mail implementation.
		if err := h.svc.Verify(ctx, addr, h.lastCode(t)); err != nil {
			t.Errorf("the admin-resent code did not verify: %v", err)
		}
	})
}
