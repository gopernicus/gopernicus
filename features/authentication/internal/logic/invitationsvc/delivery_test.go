package invitationsvc

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/email"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify"
)

// fakeEncrypter is a reversible, non-secret test encrypter so the sealed command
// payload round-trips for the processor.
type fakeEncrypter struct{}

func (fakeEncrypter) Encrypt(plaintext string) (string, error)  { return "enc:" + plaintext, nil }
func (fakeEncrypter) Decrypt(ciphertext string) (string, error) { return strings.TrimPrefix(ciphertext, "enc:"), nil }

// Generic job lifecycle states the in-test dispatcher records (declared as literals
// here since they are unexported in the delivery package).
const (
	genPending    = "pending"
	genCompleted  = "completed"
	genDeadLetter = "dead_letter"
	genSuperseded = "superseded"
)

// memDispatcher is a concurrent-safe in-test delivery.Dispatcher enforcing the
// submit-once/replace/latest-by-key invariants the real transports prove, so newSvc
// drives the real delivery.Service seal → submit → process path synchronously through
// the shared command.Engine (via delivery.JobsProcessor).
type memDispatcher struct {
	mu    sync.Mutex
	seq   int
	items map[string]*memDispatchItem // by execution id
	byKey map[string]string           // logical key -> current (latest) execution id
}

type memDispatchItem struct {
	id, key string
	payload []byte
	attempt int
	state   string
	active  bool
}

func newMemDispatcher() *memDispatcher {
	return &memDispatcher{items: map[string]*memDispatchItem{}, byKey: map[string]string{}}
}

func (m *memDispatcher) Submit(_ context.Context, _, _, key string, payload []byte) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id, ok := m.byKey[key]; ok {
		if it := m.items[id]; it != nil && it.active {
			return id, nil
		}
	}
	return m.insertLocked(key, payload), nil
}

func (m *memDispatcher) Replace(_ context.Context, _, _, key string, payload []byte) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id, ok := m.byKey[key]; ok {
		if it := m.items[id]; it != nil && it.active {
			it.state = genSuperseded
			it.active = false
		}
	}
	return m.insertLocked(key, payload), nil
}

func (m *memDispatcher) insertLocked(key string, payload []byte) string {
	m.seq++
	id := fmt.Sprintf("exec-%d", m.seq)
	m.items[id] = &memDispatchItem{id: id, key: key, payload: payload, state: genPending, active: true}
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

// drain runs the shared delivery processor over every pending item to a terminal state,
// so the invitation/member-added mail lands on the recording transports synchronously.
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
		}
		m.mu.Unlock()
		if deadPayload != nil {
			_ = proc.Discard(ctx, it.id, deadPayload)
		}
	}
	t.Fatalf("delivery drain did not settle (possible loop)")
}

// drainingQueue wraps the real delivery.Service and drains the shared processor after
// each enqueue so the invitation/member-added mail lands on the recording transports
// synchronously (invitationsvc only enqueues pre-rendered commands, so the processor
// needs no Initializer).
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

// wireSyncDelivery injects the real synchronous delivery seam into svc white-box so the
// invitation/member-added send sites submit and the shared processor delivers to the
// wired transports within the call.
func wireSyncDelivery(t *testing.T, svc *Service, mailer email.Sender, notifiers map[string]notify.Notifier) {
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
	proc, err := delivery.NewJobsProcessor(delivery.JobsProcessorDeps{Encrypter: enc, Router: router})
	if err != nil {
		t.Fatalf("delivery.NewJobsProcessor: %v", err)
	}
	svc.deliverer = router
	svc.queue = &drainingQueue{svc: dsvc, disp: disp, proc: proc, t: t}
}
