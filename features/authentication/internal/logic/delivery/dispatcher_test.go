package delivery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/work"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// TestDispatcherContract runs the transport-neutral dispatcher contract —
// submit-once, replace, and latest-status semantics — against an in-memory test
// double, so a jobs-mode or in-process dispatcher drops in behind the same contract.
func TestDispatcherContract(t *testing.T) {
	impls := map[string]func(t *testing.T) Dispatcher{
		"memDispatcher": func(t *testing.T) Dispatcher {
			return newMemDispatcher()
		},
	}
	for name, make := range impls {
		t.Run(name, func(t *testing.T) { runDispatcherContract(t, make) })
	}
}

func runDispatcherContract(t *testing.T, make func(t *testing.T) Dispatcher) {
	ctx := context.Background()

	// Submit-once: a second submit under the same logical key returns the existing
	// execution and creates no second one; the latest status is pending.
	t.Run("SubmitOnce", func(t *testing.T) {
		d := make(t)
		id1, err := d.Submit(ctx, JobKind, "registration_verification", "key-1", []byte("payload-1"))
		if err != nil || id1 == "" {
			t.Fatalf("Submit#1 id=%q err=%v", id1, err)
		}
		id2, err := d.Submit(ctx, JobKind, "registration_verification", "key-1", []byte("payload-2"))
		if err != nil {
			t.Fatalf("Submit#2 err=%v", err)
		}
		if id1 != id2 {
			t.Fatalf("submit-once broken: got distinct executions %q and %q", id1, id2)
		}
		st, err := d.LatestStatus(ctx, "key-1")
		if err != nil {
			t.Fatalf("LatestStatus err=%v", err)
		}
		if st != string(work.StatusPending) {
			t.Fatalf("latest status = %q, want %q", st, work.StatusPending)
		}
	})

	// Replace: a resend supersedes the prior active generation and admits a fresh
	// execution; the latest status is the fresh pending, never the superseded one.
	t.Run("Replace", func(t *testing.T) {
		d := make(t)
		id1, err := d.Submit(ctx, JobKind, "magic_link", "key-2", []byte("first"))
		if err != nil {
			t.Fatalf("Submit err=%v", err)
		}
		id2, err := d.Replace(ctx, JobKind, "magic_link", "key-2", []byte("second"))
		if err != nil || id2 == "" {
			t.Fatalf("Replace id=%q err=%v", id2, err)
		}
		if id1 == id2 {
			t.Fatalf("replace reused the prior execution %q", id1)
		}
		st, err := d.LatestStatus(ctx, "key-2")
		if err != nil {
			t.Fatalf("LatestStatus err=%v", err)
		}
		if st != string(work.StatusPending) {
			t.Fatalf("latest status after replace = %q, want %q (the fresh generation)", st, work.StatusPending)
		}
	})

	// Latest status for a key that names no work is a not-found, so a receipt for
	// unknown work is a clean 404 rather than a fabricated lifecycle.
	t.Run("LatestStatusUnknownKey", func(t *testing.T) {
		d := make(t)
		if _, err := d.LatestStatus(ctx, "no-such-key"); !errors.Is(err, sdk.ErrNotFound) {
			t.Fatalf("LatestStatus(unknown) err=%v, want sdk.ErrNotFound", err)
		}
	})
}

// TestNormalizeStatus proves the status normalization is TOTAL: every canonical
// sdk/capabilities/work lifecycle state maps to a stable auth Status, and an
// unrecognized state maps safely to a non-terminal pending (never a false success or
// failure, never an echo of the raw string). Inputs are sourced from the work.Status*
// constants so a drift in the frozen vocabulary cannot silently reopen the fold.
func TestNormalizeStatus(t *testing.T) {
	cases := []struct {
		in      work.Status
		want    Status
		comment string
	}{
		{work.StatusPending, Status{State: StatusPending, Pending: true}, "admitted"},
		{work.StatusRunning, Status{State: StatusPending, Pending: true}, "claimed/in flight"},
		{work.StatusFailed, Status{State: StatusPending, Pending: true}, "retryable, rescheduled"},
		{work.StatusCompleted, Status{State: StatusSucceeded}, "delivered or skipped"},
		{work.StatusDeadLetter, Status{State: StatusFailed, Failed: true}, "terminal failure"},
		{work.StatusCanceled, Status{State: StatusCanceled}, "explicit cancel"},
		{work.StatusSuperseded, Status{State: StatusCanceled}, "replaced by resend"},
		{"unknown_future_state", Status{State: StatusPending, Pending: true}, "safe default"},
		{"", Status{State: StatusPending, Pending: true}, "empty maps safely"},
	}
	for _, c := range cases {
		got := normalizeStatus(string(c.in))
		if got != c.want {
			t.Fatalf("normalizeStatus(%q) [%s] = %+v, want %+v", c.in, c.comment, got, c.want)
		}
		// No generic-vocabulary string ever survives into the projected State: an
		// unrecognized state must not echo the raw input a caller can enumerate on.
		if got.State != StatusPending && got.State != StatusSucceeded && got.State != StatusFailed && got.State != StatusCanceled {
			t.Fatalf("normalizeStatus(%q) produced non-stable state %q", c.in, got.State)
		}
	}
	// A wholly unrecognized state must not be echoed verbatim.
	if normalizeStatus("worker-node-7-timeout").State == "worker-node-7-timeout" {
		t.Fatal("unknown state echoed the raw string into the projection")
	}
}

// TestServiceConsumesDispatcher proves the service layer submits through the
// Dispatcher port (submit-once and replace) with the secret-free routing metadata
// and a SEALED payload, and normalizes the dispatcher's generic status into the
// stable auth projection.
func TestServiceConsumesDispatcher(t *testing.T) {
	ctx := context.Background()
	fd := &fakeDispatcher{state: string(work.StatusPending)}
	// A real AEAD proves the service SEALS the payload before handing it to the
	// dispatcher (the passthrough test encrypter would not).
	enc, err := cryptids.NewAESGCM(bytes.Repeat([]byte("k"), 32))
	if err != nil {
		t.Fatalf("NewAESGCM: %v", err)
	}
	svc, err := NewService(ServiceDeps{Dispatcher: fd, Encrypter: enc, Now: newClock().now})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	secret := "otp-778899"
	rcpt, err := svc.Enqueue(ctx, Command{
		Kind:           identity.KindEmail,
		Purpose:        PurposeRegistrationVerification,
		IdempotencyKey: "svc-key",
		Envelope:       Envelope{Destination: "u@x.test", Body: "code " + secret, Secret: secret},
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if len(fd.submits) != 1 {
		t.Fatalf("expected 1 submit, got %d", len(fd.submits))
	}
	sub := fd.submits[0]
	if sub.kind != identity.KindEmail || sub.purpose != PurposeRegistrationVerification || sub.key != "svc-key" {
		t.Fatalf("submit routing metadata = %+v", sub)
	}
	if strings.Contains(string(sub.payload), secret) {
		t.Fatalf("service handed the dispatcher an UNSEALED payload carrying the secret: %q", sub.payload)
	}
	if rcpt.Key != "svc-key" || rcpt.JobID != "exec-sub" || rcpt.State != StatusPending {
		t.Fatalf("receipt = %+v", rcpt)
	}

	if _, err := svc.Replace(ctx, Command{Kind: identity.KindEmail, Purpose: PurposeMagicLink, IdempotencyKey: "svc-key", Envelope: Envelope{Destination: "u@x.test", Body: "b"}}); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if len(fd.replaces) != 1 {
		t.Fatalf("expected 1 replace, got %d", len(fd.replaces))
	}

	// Status normalization runs through the dispatcher for every generic state.
	for _, tc := range []struct {
		state work.Status
		want  Status
	}{
		{work.StatusPending, Status{State: StatusPending, Pending: true}},
		{work.StatusCompleted, Status{State: StatusSucceeded}},
		{work.StatusDeadLetter, Status{State: StatusFailed, Failed: true}},
		{work.StatusCanceled, Status{State: StatusCanceled}},
	} {
		fd.state = string(tc.state)
		st, err := svc.Status(ctx, "svc-key")
		if err != nil {
			t.Fatalf("Status(%q): %v", tc.state, err)
		}
		if st != tc.want {
			t.Fatalf("Status for generic %q = %+v, want %+v", tc.state, st, tc.want)
		}
	}
}

// TestDeliveryStatusNoLeak proves the status projection a session-gated caller
// receives never carries the destination or secret, whether pending or terminal —
// the possession-gated receipt read reveals only lifecycle.
func TestDeliveryStatusNoLeak(t *testing.T) {
	ctx := context.Background()
	fd := &fakeDispatcher{state: string(work.StatusPending)}
	svc, err := NewService(ServiceDeps{Dispatcher: fd, Encrypter: fakeEncrypter{}, Now: newClock().now})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	const secret = "super-secret-code-4242"
	const dest = "victim@example.test"
	if _, err := svc.Enqueue(ctx, Command{
		Kind:           identity.KindEmail,
		Purpose:        PurposeRegistrationVerification,
		IdempotencyKey: "leak-key",
		Envelope:       Envelope{Destination: dest, Body: "your code is " + secret, Secret: secret},
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	assertNoLeak := func(t *testing.T, st Status) {
		t.Helper()
		dump := fmt.Sprintf("%+v | %#v", st, st)
		if strings.Contains(dump, secret) {
			t.Fatalf("status projection leaked the secret: %s", dump)
		}
		if strings.Contains(dump, dest) {
			t.Fatalf("status projection leaked the destination: %s", dump)
		}
		if strings.Contains(dump, "leak-key") {
			// The raw logical key is not part of the projection (the caller supplies it;
			// the response must not echo it back as state).
			t.Fatalf("status projection echoed the raw logical key: %s", dump)
		}
	}

	// Pending projection.
	st, err := svc.Status(ctx, "leak-key")
	if err != nil {
		t.Fatalf("Status(pending): %v", err)
	}
	if st.State != StatusPending || !st.Pending {
		t.Fatalf("pending status = %+v", st)
	}
	assertNoLeak(t, st)

	// Drive the transport to a terminal (dead-letter) state and re-check the projection.
	fd.state = string(work.StatusDeadLetter)
	st, err = svc.Status(ctx, "leak-key")
	if err != nil {
		t.Fatalf("Status(failed): %v", err)
	}
	if st.State != StatusFailed || !st.Failed {
		t.Fatalf("failed status = %+v", st)
	}
	assertNoLeak(t, st)
}

// fakeDispatcher records the calls the service makes so a test can assert the
// service consumes the Dispatcher port with sealed payloads and secret-free routing
// metadata, and can drive status normalization for any generic state.
type fakeDispatcher struct {
	submits  []dispatchCall
	replaces []dispatchCall
	state    string
}

type dispatchCall struct {
	kind, purpose, key string
	payload            []byte
}

func (f *fakeDispatcher) Submit(_ context.Context, kind, purpose, key string, payload []byte) (string, error) {
	f.submits = append(f.submits, dispatchCall{kind, purpose, key, payload})
	return "exec-sub", nil
}

func (f *fakeDispatcher) Replace(_ context.Context, kind, purpose, key string, payload []byte) (string, error) {
	f.replaces = append(f.replaces, dispatchCall{kind, purpose, key, payload})
	return "exec-rep", nil
}

func (f *fakeDispatcher) LatestStatus(_ context.Context, _ string) (string, error) {
	return f.state, nil
}

// memDispatcher is an in-memory Dispatcher test double honoring submit-once,
// replace, and latest-by-key status directly (no repository), so the contract suite
// runs against a second, independent implementation.
type memDispatcher struct {
	mu    sync.Mutex
	seq   int
	byKey map[string]string   // logical key -> current active execution ID
	state map[string]string   // execution ID -> generic lifecycle state
	order map[string][]string // logical key -> execution IDs in submit order
}

func newMemDispatcher() *memDispatcher {
	return &memDispatcher{byKey: map[string]string{}, state: map[string]string{}, order: map[string][]string{}}
}

func (m *memDispatcher) Submit(_ context.Context, _, _, key string, _ []byte) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id, ok := m.byKey[key]; ok {
		return id, nil // submit-once: existing active execution
	}
	return m.insert(key), nil
}

func (m *memDispatcher) Replace(_ context.Context, _, _, key string, _ []byte) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id, ok := m.byKey[key]; ok {
		m.state[id] = string(work.StatusSuperseded)
	}
	return m.insert(key), nil
}

func (m *memDispatcher) insert(key string) string {
	m.seq++
	id := fmt.Sprintf("exec-%d", m.seq)
	m.byKey[key] = id
	m.state[id] = string(work.StatusPending)
	m.order[key] = append(m.order[key], id)
	return id
}

func (m *memDispatcher) LatestStatus(_ context.Context, key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := m.order[key]
	if len(ids) == 0 {
		return "", sdk.ErrNotFound
	}
	return m.state[ids[len(ids)-1]], nil
}
