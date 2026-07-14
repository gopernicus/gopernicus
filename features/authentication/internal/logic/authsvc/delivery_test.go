package authsvc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/email"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify"
)

// Generic job lifecycle states the in-test dispatcher records (mirrors the frozen
// jobs vocabulary the delivery.Service normalizes; declared as literals here since
// they are unexported in the delivery package).
const (
	genPending    = "pending"
	genCompleted  = "completed"
	genDeadLetter = "dead_letter"
	genSuperseded = "superseded"
)

// memDispatcher is a concurrent-safe in-test delivery.Dispatcher. It enforces the
// submit-once/replace/latest-by-key invariants the real transports prove (idempotent
// submit while active, replace supersedes the prior generation, latest-by-key status
// read) so the harness can drive the real delivery.Service seal → submit → process
// path synchronously through the shared command.Engine (via delivery.JobsProcessor).
type memDispatcher struct {
	mu    sync.Mutex
	seq   int
	items map[string]*memDispatchItem // by execution id
	byKey map[string]string           // logical key -> current (latest) execution id
}

type memDispatchItem struct {
	id, key, kind, purpose string
	payload                []byte
	attempt                int
	state                  string
	active                 bool
}

func newMemDispatcher() *memDispatcher {
	return &memDispatcher{items: map[string]*memDispatchItem{}, byKey: map[string]string{}}
}

func (m *memDispatcher) Submit(_ context.Context, kind, purpose, key string, payload []byte) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id, ok := m.byKey[key]; ok {
		if it := m.items[id]; it != nil && it.active {
			return id, nil // idempotent while active
		}
	}
	return m.insertLocked(kind, purpose, key, payload), nil
}

func (m *memDispatcher) Replace(_ context.Context, kind, purpose, key string, payload []byte) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id, ok := m.byKey[key]; ok {
		if it := m.items[id]; it != nil && it.active {
			it.state = genSuperseded
			it.active = false
		}
	}
	return m.insertLocked(kind, purpose, key, payload), nil
}

func (m *memDispatcher) insertLocked(kind, purpose, key string, payload []byte) string {
	m.seq++
	id := fmt.Sprintf("exec-%d", m.seq)
	m.items[id] = &memDispatchItem{id: id, key: key, kind: kind, purpose: purpose, payload: payload, state: genPending, active: true}
	m.byKey[key] = id
	return id
}

func (m *memDispatcher) LatestStatus(_ context.Context, key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.byKey[key]
	if !ok {
		return "", sdk.ErrNotFound
	}
	return m.items[id].state, nil
}

// claimPending returns the oldest pending active item and increments its attempt.
func (m *memDispatcher) claimPending() (*memDispatchItem, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := 1; i <= m.seq; i++ {
		it := m.items[fmt.Sprintf("exec-%d", i)]
		if it != nil && it.active && it.state == genPending {
			it.attempt++
			return it, true
		}
	}
	return nil, false
}

// snapshot returns a copy of every stored item for assertion.
func (m *memDispatcher) snapshot() []memDispatchItem {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]memDispatchItem, 0, len(m.items))
	for _, it := range m.items {
		out = append(out, *it)
	}
	return out
}

// drain runs the shared delivery processor over every pending item to a terminal
// state, so a test that reads the recording mailer right after a service call sees
// the delivered message. It mirrors the host runtime's claim → checkpoint → process
// loop synchronously.
func (m *memDispatcher) drain(t *testing.T, proc *delivery.JobsProcessor) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < 500; i++ {
		it, ok := m.claimPending()
		if !ok {
			return
		}
		checkpoint := func(_ context.Context, sealed []byte) error {
			m.mu.Lock()
			it.payload = sealed
			m.mu.Unlock()
			return nil
		}
		err := proc.Handle(ctx, it.id, it.payload, it.attempt, checkpoint)
		m.mu.Lock()
		var deadPayload []byte
		switch {
		case err == nil:
			it.state = genCompleted
			it.active = false
		case delivery.HandleErrorPermanent(err) || it.attempt >= 5:
			it.state = genDeadLetter
			it.active = false
			deadPayload = it.payload
		default:
			// transient: leave pending for the next drain iteration.
		}
		m.mu.Unlock()
		if deadPayload != nil {
			_ = proc.Discard(ctx, it.id, deadPayload)
		}
	}
	t.Fatalf("delivery drain did not settle (possible loop)")
}

// drainingQueue wraps the real delivery.Service and drains the shared processor
// synchronously after each enqueue, so a test that reads the recording mailer right
// after a service call sees the delivered message.
type drainingQueue struct {
	svc  *delivery.Service
	disp *memDispatcher
	proc *delivery.JobsProcessor
	t    *testing.T
}

func (d *drainingQueue) Enqueue(ctx context.Context, cmd delivery.Command) (delivery.Receipt, error) {
	r, err := d.svc.Enqueue(ctx, cmd)
	if err != nil {
		return r, err
	}
	d.disp.drain(d.t, d.proc)
	return r, nil
}

func (d *drainingQueue) Replace(ctx context.Context, cmd delivery.Command) (delivery.Receipt, error) {
	r, err := d.svc.Replace(ctx, cmd)
	if err != nil {
		return r, err
	}
	d.disp.drain(d.t, d.proc)
	return r, nil
}

func (d *drainingQueue) Status(ctx context.Context, key string) (delivery.Status, error) {
	return d.svc.Status(ctx, key)
}

// wireSyncDelivery builds the real delivery seam (a router over mailer, the
// delivery.Service queue over an in-test dispatcher, and a delivery.JobsProcessor —
// the SAME command.Engine both real modes run — whose Initializer is svc) and injects
// it into svc white-box, wrapped in a draining queue that makes the send path
// synchronous so a test can assert on the delivered mail right after a service call.
// It returns the in-mem dispatcher for status assertions. Notifiers routes non-email
// kinds.
func wireSyncDelivery(t *testing.T, svc *Service, mailer email.Sender, notifiers map[string]notify.Notifier) *memDispatcher {
	t.Helper()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	router, err := delivery.NewRouter(delivery.Deps{Mailer: mailer, MailFrom: "noreply@example.com", Notifiers: notifiers, Logger: quiet})
	if err != nil {
		t.Fatalf("delivery.NewRouter: %v", err)
	}
	disp := newMemDispatcher()
	enc := fakeEncrypter{}
	dsvc, err := delivery.NewService(delivery.ServiceDeps{Dispatcher: disp, Encrypter: enc})
	if err != nil {
		t.Fatalf("delivery.NewService: %v", err)
	}
	proc, err := delivery.NewJobsProcessor(delivery.JobsProcessorDeps{
		Encrypter:   enc,
		Router:      router,
		Initializer: svc,
	})
	if err != nil {
		t.Fatalf("delivery.NewJobsProcessor: %v", err)
	}
	svc.deliver = router
	svc.queue = &drainingQueue{svc: dsvc, disp: disp, proc: proc, t: t}
	return disp
}

// wireDelivery injects the synchronous outbox into the main harness service.
func (h *harness) wireDelivery(t *testing.T) {
	t.Helper()
	h.deliveryRepo = wireSyncDelivery(t, h.svc, h.mailer, nil)
}

// blockingSender is an email.Sender that blocks in Send until released — the
// "blocking fake provider" the enumeration-timing test runs the processor against to
// prove the unauthenticated start never waits on the provider.
type blockingSender struct{ release chan struct{} }

func (b *blockingSender) Send(ctx context.Context, _ email.Message) error {
	select {
	case <-b.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// TestForgotPasswordEnqueuesOpaqueSamePath proves the enumeration contract (design
// §4.1): a known-verified and an unknown address traverse the SAME request path —
// each submits exactly one OPAQUE command (only the normalized identifier, no rendered
// body, no account resolution) and neither is claimed on the request path. The
// processor resolves off the request path, so known and unknown are indistinguishable.
func TestForgotPasswordEnqueuesOpaqueSamePath(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "known@example.com", "password123456789")
	h.mustVerify(t, "known@example.com")

	// Swap the synchronous draining queue for the plain async service so
	// ForgotPassword only submits (the processor never runs on the request path).
	enc := fakeEncrypter{}
	disp := newMemDispatcher()
	dsvc, err := delivery.NewService(delivery.ServiceDeps{Dispatcher: disp, Encrypter: enc})
	if err != nil {
		t.Fatalf("delivery.NewService: %v", err)
	}
	h.svc.queue = dsvc

	for _, addr := range []string{"known@example.com", "ghost@example.com"} {
		if err := h.svc.ForgotPassword(context.Background(), addr); err != nil {
			t.Fatalf("ForgotPassword(%q): %v", addr, err)
		}
	}

	items := disp.snapshot()
	if len(items) != 2 {
		t.Fatalf("submitted %d commands, want 2 (one per address, same path)", len(items))
	}
	for _, it := range items {
		if it.attempt != 0 {
			t.Errorf("command claimed on the request path (attempt=%d); resolution must be off-path", it.attempt)
		}
		// With the identity fakeEncrypter the sealed payload is the plaintext versioned
		// command envelope JSON: opaque stage, no rendered secret.
		payload := string(it.payload)
		if !strings.Contains(payload, `"stage":"opaque"`) {
			t.Errorf("request-path command is not opaque: %s", payload)
		}
		if strings.Contains(payload, `"secret"`) {
			t.Errorf("opaque admission leaked a secret field: %s", payload)
		}
	}
}

// TestForgotPasswordDoesNotBlockOnProvider proves a blocking provider does not delay
// the unauthenticated start (phase acceptance): with the processor draining against a
// provider that blocks indefinitely, ForgotPassword returns promptly for both a known
// and an unknown address, and their handler timing is comparable.
func TestForgotPasswordDoesNotBlockOnProvider(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "known@example.com", "password123456789")
	h.mustVerify(t, "known@example.com")

	enc := fakeEncrypter{}
	disp := newMemDispatcher()
	blocking := &blockingSender{release: make(chan struct{})}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	router, err := delivery.NewRouter(delivery.Deps{Mailer: blocking, MailFrom: "noreply@example.com", Logger: quiet})
	if err != nil {
		t.Fatalf("delivery.NewRouter: %v", err)
	}
	dsvc, err := delivery.NewService(delivery.ServiceDeps{Dispatcher: disp, Encrypter: enc})
	if err != nil {
		t.Fatalf("delivery.NewService: %v", err)
	}
	proc, err := delivery.NewJobsProcessor(delivery.JobsProcessorDeps{Encrypter: enc, Router: router, Initializer: h.svc})
	if err != nil {
		t.Fatalf("delivery.NewJobsProcessor: %v", err)
	}
	h.svc.queue = dsvc

	// A background drainer parks in the blocking provider send; the request-path
	// submit must never wait on it.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ctx.Err() == nil {
			it, ok := disp.claimPending()
			if !ok {
				time.Sleep(time.Millisecond)
				continue
			}
			_ = proc.Handle(ctx, it.id, it.payload, it.attempt, func(_ context.Context, sealed []byte) error {
				disp.mu.Lock()
				it.payload = sealed
				disp.mu.Unlock()
				return nil
			})
			disp.mu.Lock()
			it.active = false
			it.state = genCompleted
			disp.mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		cancel()
		close(blocking.release)
		<-done
	})

	timeStart := func(addr string) time.Duration {
		start := time.Now()
		if err := h.svc.ForgotPassword(context.Background(), addr); err != nil {
			t.Fatalf("ForgotPassword(%q): %v", addr, err)
		}
		return time.Since(start)
	}
	known := timeStart("known@example.com")
	unknown := timeStart("ghost@example.com")

	const bound = 200 * time.Millisecond
	if known > bound || unknown > bound {
		t.Fatalf("start blocked on the provider: known=%v unknown=%v (bound %v)", known, unknown, bound)
	}
}

// TestDeliveryStatusReflectsState proves the session-gated status projection reads
// the delivery transport through the read-only port: after a delivered command the
// receipt key reports succeeded, and an unknown receipt is sdk.ErrNotFound.
func TestDeliveryStatusReflectsState(t *testing.T) {
	h := newHarness(t, nil)
	h.wireDelivery(t)
	ctx := context.Background()

	if _, err := h.svc.queue.Enqueue(ctx, delivery.Command{
		Kind:           "email",
		Purpose:        delivery.PurposeRegistrationVerification,
		IdempotencyKey: "status-key",
		Envelope:       delivery.Envelope{Destination: "s@example.com", Body: "hi", HTML: "<p>hi</p>"},
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	st, err := h.svc.DeliveryStatus(ctx, "status-key")
	if err != nil {
		t.Fatalf("DeliveryStatus: %v", err)
	}
	if st.State != delivery.StatusSucceeded {
		t.Errorf("status state = %q, want %q (drained)", st.State, delivery.StatusSucceeded)
	}
	if _, err := h.svc.DeliveryStatus(ctx, "no-such-receipt"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("unknown receipt err = %v, want ErrNotFound", err)
	}
}
