package authsvc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/features/authentication/domain/securityevent"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
)

// AV3D-4.4 — enumeration and saturation proof for DeliveryMode "in_process".
//
// The enumeration-safe unauthenticated starts (forgot-password, passwordless) admit an
// OPAQUE command through the bounded in-process queue and perform NO account lookup on
// the request path — the worker resolves off-path. So a capacity-exhausted, closed, or
// shutting-down runtime rejects a known and an unknown identifier IDENTICALLY: admission
// precedes any lookup, so the typed error class carries no existence signal. These tests
// prove the identical-error-class property AND the structural property (no resolver call
// before admission), which is the honest proof — not a fragile wall-clock comparison.

// errQueue is a delivery outbox seam that always fails admission with a fixed error,
// standing in for a saturated/closed in-process runtime at the producer boundary.
type errQueue struct{ err error }

func (q errQueue) Enqueue(context.Context, delivery.Command) (delivery.Receipt, error) {
	return delivery.Receipt{}, q.err
}
func (q errQueue) Replace(context.Context, delivery.Command) (delivery.Receipt, error) {
	return delivery.Receipt{}, q.err
}
func (q errQueue) Status(context.Context, string) (delivery.Status, error) {
	return delivery.Status{}, sdk.ErrNotFound
}

// noopInProcessProcessor satisfies delivery.InProcessProcessor for a runtime whose only
// job in these tests is to reach the CLOSED state on shutdown; it delivers nothing.
type noopInProcessProcessor struct{}

func (noopInProcessProcessor) Handle(context.Context, string, []byte, int, func(context.Context, []byte) error) error {
	return nil
}
func (noopInProcessProcessor) Discard(context.Context, string, []byte) error { return nil }

// saturatedInProcessService builds a real delivery.Service over a bounded InProcessQueue
// whose every slot is already occupied (the runtime is never started, so nothing drains).
// A subsequent Submit/Replace waits at most the short admission deadline and then returns
// ErrDeliveryCapacity — never a silent drop. Each filler uses a distinct logical key so the
// arbiter's submit-once coalescing does not collapse them.
func saturatedInProcessService(t *testing.T, capacity int) *delivery.Service {
	t.Helper()
	q := delivery.NewInProcessQueue(delivery.InProcessQueueConfig{
		Capacity:          capacity,
		AdmissionDeadline: 20 * time.Millisecond,
	})
	dsvc, err := delivery.NewService(delivery.ServiceDeps{Dispatcher: q, Encrypter: fakeEncrypter{}})
	if err != nil {
		t.Fatalf("delivery.NewService: %v", err)
	}
	for i := 0; i < capacity; i++ {
		key := fmt.Sprintf("filler-%d", i)
		if _, err := dsvc.Enqueue(context.Background(), delivery.Command{
			Kind:           "email",
			Purpose:        delivery.PurposePasswordReset,
			IdempotencyKey: key,
			Envelope:       delivery.Envelope{ResolutionInput: key},
		}); err != nil {
			t.Fatalf("fill slot %d: %v", i, err)
		}
	}
	return dsvc
}

// closedInProcessService builds a real delivery.Service over an InProcessQueue whose
// runtime has been run and then cancelled, so admission is CLOSED. Run always calls
// beginShutdown before returning, so once Run returns the queue rejects every admission
// with ErrDeliveryClosed.
func closedInProcessService(t *testing.T) *delivery.Service {
	t.Helper()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	q := delivery.NewInProcessQueue(delivery.InProcessQueueConfig{
		Capacity:          4,
		AdmissionDeadline: 20 * time.Millisecond,
	})
	rt, err := delivery.NewInProcessRuntime(q, noopInProcessProcessor{}, delivery.InProcessRuntimeConfig{
		Workers:          1,
		ShutdownDeadline: time.Second,
		Logger:           quiet,
	})
	if err != nil {
		t.Fatalf("delivery.NewInProcessRuntime: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- rt.Run(ctx) }()
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("runtime Run: %v", err)
	}
	dsvc, err := delivery.NewService(delivery.ServiceDeps{Dispatcher: q, Encrypter: fakeEncrypter{}})
	if err != nil {
		t.Fatalf("delivery.NewService: %v", err)
	}
	return dsvc
}

// resetResolverCounts zeroes the resolver call counters right before a request-path call,
// so the no-lookup-before-admission assertion measures only that call.
func (h *harness) resetResolverCounts() {
	h.idents.mu.Lock()
	h.idents.loginCalls, h.idents.recoveryCalls = 0, 0
	h.idents.mu.Unlock()
}

// assertNoResolverCall fails if any account resolution happened on the request path.
func (h *harness) assertNoResolverCall(t *testing.T) {
	t.Helper()
	h.idents.mu.Lock()
	defer h.idents.mu.Unlock()
	if h.idents.loginCalls != 0 || h.idents.recoveryCalls != 0 {
		t.Fatalf("account lookup occurred on the request path (GetLogin=%d GetRecovery=%d); admission must precede any lookup",
			h.idents.loginCalls, h.idents.recoveryCalls)
	}
}

// TestInProcessForgotPasswordCapacityEnumerationParity proves that under a saturated
// in-process queue, forgot-password returns the SAME typed ErrDeliveryCapacity for a
// known and an unknown address, and resolves NO account on the request path for either.
func TestInProcessForgotPasswordCapacityEnumerationParity(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "known@example.com", "password123456789")
	h.mustVerify(t, "known@example.com")

	h.svc.queue = saturatedInProcessService(t, 1)
	h.resetResolverCounts()

	knownErr := h.svc.ForgotPassword(context.Background(), "known@example.com")
	unknownErr := h.svc.ForgotPassword(context.Background(), "ghost@example.com")

	if !errors.Is(knownErr, delivery.ErrDeliveryCapacity) {
		t.Fatalf("known ForgotPassword under saturation = %v, want ErrDeliveryCapacity", knownErr)
	}
	if !errors.Is(unknownErr, delivery.ErrDeliveryCapacity) {
		t.Fatalf("unknown ForgotPassword under saturation = %v, want ErrDeliveryCapacity", unknownErr)
	}
	h.assertNoResolverCall(t)
}

// TestInProcessPasswordlessCapacityEnumerationParity proves the same saturation parity
// and no-lookup-before-admission property for the passwordless start.
func TestInProcessPasswordlessCapacityEnumerationParity(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "known@example.com", "password123456789")
	h.mustVerify(t, "known@example.com")
	enablePasswordless(h, string(identifier.KindEmail))

	h.svc.queue = saturatedInProcessService(t, 1)
	h.resetResolverCounts()

	kind := string(identifier.KindEmail)
	knownErr := h.svc.StartPasswordless(context.Background(), kind, "known@example.com", "link")
	unknownErr := h.svc.StartPasswordless(context.Background(), kind, "ghost@example.com", "link")

	if !errors.Is(knownErr, delivery.ErrDeliveryCapacity) {
		t.Fatalf("known StartPasswordless under saturation = %v, want ErrDeliveryCapacity", knownErr)
	}
	if !errors.Is(unknownErr, delivery.ErrDeliveryCapacity) {
		t.Fatalf("unknown StartPasswordless under saturation = %v, want ErrDeliveryCapacity", unknownErr)
	}
	h.assertNoResolverCall(t)
}

// TestInProcessClosedRuntimeEnumerationParity proves that a closed (shut-down) in-process
// runtime rejects both forgot-password and passwordless starts with the SAME typed
// ErrDeliveryClosed for known and unknown identifiers, resolving no account on the request
// path — the shutdown outcome is independent of existence.
func TestInProcessClosedRuntimeEnumerationParity(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "known@example.com", "password123456789")
	h.mustVerify(t, "known@example.com")
	enablePasswordless(h, string(identifier.KindEmail))

	h.svc.queue = closedInProcessService(t)
	h.resetResolverCounts()

	kind := string(identifier.KindEmail)
	ctx := context.Background()
	outcomes := map[string]error{
		"forgot known":         h.svc.ForgotPassword(ctx, "known@example.com"),
		"forgot unknown":       h.svc.ForgotPassword(ctx, "ghost@example.com"),
		"passwordless known":   h.svc.StartPasswordless(ctx, kind, "known@example.com", "link"),
		"passwordless unknown": h.svc.StartPasswordless(ctx, kind, "ghost@example.com", "link"),
	}
	for name, err := range outcomes {
		if !errors.Is(err, delivery.ErrDeliveryClosed) {
			t.Fatalf("%s on a closed runtime = %v, want ErrDeliveryClosed", name, err)
		}
	}
	h.assertNoResolverCall(t)
}

// TestInProcessStartsNeverAcceptAfterDrop proves the service boundary never reports an
// accepted (nil) result after admission was dropped: every saturated/closed start returns
// a non-nil failure. The HTTP-class honesty (503, never 202) is proven at the inbound
// layer (see the transport tests).
func TestInProcessStartsNeverAcceptAfterDrop(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "known@example.com", "password123456789")
	h.mustVerify(t, "known@example.com")
	enablePasswordless(h, string(identifier.KindEmail))

	kind := string(identifier.KindEmail)
	ctx := context.Background()

	h.svc.queue = saturatedInProcessService(t, 1)
	if err := h.svc.ForgotPassword(ctx, "known@example.com"); err == nil {
		t.Fatal("ForgotPassword returned nil (accepted) after admission was dropped at capacity")
	}
	if err := h.svc.StartPasswordless(ctx, kind, "known@example.com", "link"); err == nil {
		t.Fatal("StartPasswordless returned nil (accepted) after admission was dropped at capacity")
	}

	h.svc.queue = closedInProcessService(t)
	if err := h.svc.ForgotPassword(ctx, "known@example.com"); err == nil {
		t.Fatal("ForgotPassword returned nil (accepted) after the runtime was closed")
	}
	if err := h.svc.StartPasswordless(ctx, kind, "known@example.com", "link"); err == nil {
		t.Fatal("StartPasswordless returned nil (accepted) after the runtime was closed")
	}
}

// TestIdentifierChangeStartSurfacesDispatchFailure proves the SURFACED-error contract for
// an account-resolved producer (AV3D-2.4 inventory site 6, identifier-change proof): a
// dispatch failure reaches the caller rather than being swallowed.
func TestIdentifierChangeStartSurfacesDispatchFailure(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	userID, _, sessionID := h.mustVerifiedLogin(t, "owner@example.com", "password123456789")

	h.svc.queue = errQueue{err: delivery.ErrDeliveryCapacity}

	_, err := h.svc.StartIdentifierChange(ctx, IdentifierChangeStart{
		SessionID: sessionID, UserID: userID, Kind: identifier.KindEmail,
		Value: "second@example.com", Uses: identifier.Uses{Login: true, Recovery: true, Notification: true},
	})
	if !errors.Is(err, delivery.ErrDeliveryCapacity) {
		t.Fatalf("StartIdentifierChange dispatch failure = %v, want the surfaced ErrDeliveryCapacity", err)
	}
}

// TestIdentifierChangeNoticeDispatchFailureCommitStands proves the COMMITTED-then-notify
// contract for an account-resolved producer (AV3D-2.4 inventory site 7, identifier-change
// notice): when the best-effort notice dispatch fails, the identifier change still commits,
// the confirm is NOT falsely reported as failed, and the committed-but-not-notified state
// is recorded honestly as a coarse, PII-free WARN.
func TestIdentifierChangeNoticeDispatchFailureCommitStands(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	userID, primary, sessionID := h.mustVerifiedLogin(t, "primary@example.com", "password123456789")

	// Start with the working queue so the proof code is delivered and readable.
	if _, err := h.svc.StartIdentifierChange(ctx, IdentifierChangeStart{
		SessionID: sessionID, UserID: userID, Kind: identifier.KindEmail,
		Value: "brand-new@example.com", Uses: identifier.Uses{Login: true, Recovery: true, Notification: true},
	}); err != nil {
		t.Fatalf("StartIdentifierChange: %v", err)
	}
	code := h.mailer.proofCodeFor(t, "brand-new@example.com")

	// The change has NOT committed yet — the notice is enqueued at confirm time. Fail the
	// notice dispatch and capture the honest WARN.
	var logbuf bytes.Buffer
	h.svc.logger = slog.New(slog.NewTextHandler(&logbuf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	h.svc.queue = errQueue{err: delivery.ErrDeliveryCapacity}

	if err := h.svc.ConfirmIdentifierChange(ctx, IdentifierChangeConfirm{
		SessionID: sessionID, UserID: userID, Kind: identifier.KindEmail, Code: code,
	}); err != nil {
		t.Fatalf("ConfirmIdentifierChange must not fail when only the best-effort notice dispatch fails: %v", err)
	}

	// The commit stands: the new identifier is claimed and verified, and the change event
	// is recorded — the commit is neither rolled back nor falsely reported failed.
	it, err := h.idents.GetLogin(ctx, string(identifier.KindEmail), "brand-new@example.com")
	if err != nil {
		t.Fatalf("committed identifier not claimed after notice failure: %v", err)
	}
	if !it.Verified() || it.UserID != userID {
		t.Fatalf("committed identifier = %+v, want verified for %s", it, userID)
	}
	if !hasEvent(h.events, securityevent.TypeEmailChanged, securityevent.StatusSuccess) {
		t.Fatal("email_changed event not recorded — the commit must be reported honestly")
	}

	// The committed-but-not-notified state is recorded honestly: a coarse WARN keyed by
	// error kind, with no PII (no address, no code).
	logs := logbuf.String()
	if !strings.Contains(logs, "identifier-change notice enqueue failed") {
		t.Fatalf("no WARN for the failed notice dispatch; got %q", logs)
	}
	if !strings.Contains(logs, "error_kind") {
		t.Fatalf("WARN missing error_kind; got %q", logs)
	}
	for _, pii := range []string{primary, "brand-new@example.com", code} {
		if strings.Contains(logs, pii) {
			t.Fatalf("WARN leaked PII %q: %q", pii, logs)
		}
	}
}
