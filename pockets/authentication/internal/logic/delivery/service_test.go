package delivery

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/work"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// The queue requires its dispatcher and encrypter; both are structural.
func TestNewServiceRequiresCollaborators(t *testing.T) {
	if _, err := NewService(ServiceDeps{Encrypter: fakeEncrypter{}}); !errors.Is(err, ErrDispatcherRequired) {
		t.Fatalf("missing dispatcher err=%v, want ErrDispatcherRequired", err)
	}
	if _, err := NewService(ServiceDeps{Dispatcher: newMemDispatcher()}); !errors.Is(err, ErrEncrypterRequired) {
		t.Fatalf("missing encrypter err=%v, want ErrEncrypterRequired", err)
	}
	if !errors.Is(ErrDispatcherRequired, sdk.ErrInvalidInput) || !errors.Is(ErrEncrypterRequired, sdk.ErrInvalidInput) {
		t.Fatal("queue construction errors must wrap sdk.ErrInvalidInput")
	}
}

// Enqueue seals the envelope into the submitted opaque payload: neither the
// destination nor the secret appears in the clear in what the dispatcher receives.
func TestServiceEnqueueSealsPayload(t *testing.T) {
	fd := &fakeDispatcher{state: string(work.StatusPending)}
	// A real AEAD encrypter proves the submitted payload is opaque ciphertext (the
	// passthrough test encrypter would not).
	enc, err := cryptids.NewAESGCM(bytes.Repeat([]byte("k"), 32))
	if err != nil {
		t.Fatalf("NewAESGCM: %v", err)
	}
	svc, _ := NewService(ServiceDeps{Dispatcher: fd, Encrypter: enc, Now: newClock().now})
	rcpt, err := svc.Enqueue(context.Background(), Command{
		Kind:           identity.KindEmail,
		Purpose:        PurposeRegistrationVerification,
		IdempotencyKey: "digest-1",
		Envelope:       Envelope{Destination: "user@example.test", Body: "code 424242", Secret: "424242"},
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if rcpt.Key != "digest-1" || rcpt.JobID == "" || rcpt.State != StatusPending {
		t.Fatalf("receipt = %+v", rcpt)
	}
	if len(fd.submits) != 1 {
		t.Fatalf("expected 1 submit, got %d", len(fd.submits))
	}
	payload := fd.submits[0].payload
	if bytes.Contains(payload, []byte("user@example.test")) || bytes.Contains(payload, []byte("424242")) {
		t.Fatalf("payload leaked a plaintext destination/secret: %q", payload)
	}
}

// Enqueue is idempotent by key: a double-submitted start makes no second execution.
func TestServiceEnqueueIdempotent(t *testing.T) {
	disp := newMemDispatcher()
	svc, _ := NewService(ServiceDeps{Dispatcher: disp, Encrypter: fakeEncrypter{}, Now: newClock().now})
	cmd := Command{Kind: identity.KindEmail, Purpose: PurposeRegistrationVerification, IdempotencyKey: "same", Envelope: Envelope{Destination: "u@x", Body: "b"}}
	a, err := svc.Enqueue(context.Background(), cmd)
	if err != nil {
		t.Fatalf("Enqueue#1: %v", err)
	}
	b, err := svc.Enqueue(context.Background(), cmd)
	if err != nil {
		t.Fatalf("Enqueue#2: %v", err)
	}
	if a.JobID != b.JobID {
		t.Fatalf("second enqueue made a new execution: %s vs %s", a.JobID, b.JobID)
	}
	if got := len(disp.state); got != 1 {
		t.Fatalf("idempotent enqueue created %d executions, want 1", got)
	}
}

// A Command missing any structural field is rejected before touching the transport.
func TestServiceEnqueueCommandIncomplete(t *testing.T) {
	svc, _ := NewService(ServiceDeps{Dispatcher: newMemDispatcher(), Encrypter: fakeEncrypter{}})
	for _, cmd := range []Command{
		{Purpose: PurposeMagicLink, IdempotencyKey: "k"},
		{Kind: identity.KindEmail, IdempotencyKey: "k"},
		{Kind: identity.KindEmail, Purpose: PurposeMagicLink},
	} {
		if _, err := svc.Enqueue(context.Background(), cmd); !errors.Is(err, ErrCommandIncomplete) {
			t.Fatalf("Enqueue(%+v) err=%v, want ErrCommandIncomplete", cmd, err)
		}
	}
}

// A seal (encryption) failure surfaces from Enqueue; nothing is submitted. The
// versioned command codec maps the underlying encrypt error to a static, secret-free
// sentinel (never echoing the plaintext), so the guarantee is: a non-nil error wrapping
// sdk.ErrInvalidInput and no work handed to the dispatcher.
func TestServiceEnqueueEncryptionFailure(t *testing.T) {
	fd := &fakeDispatcher{state: string(work.StatusPending)}
	svc, _ := NewService(ServiceDeps{Dispatcher: fd, Encrypter: fakeEncrypter{encErr: errBoom}})
	_, err := svc.Enqueue(context.Background(), Command{Kind: identity.KindEmail, Purpose: PurposeMagicLink, IdempotencyKey: "k", Envelope: Envelope{Destination: "u@x", Body: "b"}})
	if err == nil || !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("Enqueue err=%v, want a seal failure wrapping sdk.ErrInvalidInput", err)
	}
	if len(fd.submits) != 0 {
		t.Fatalf("work was submitted despite the seal failure")
	}
}

// Replace supersedes any prior pending execution holding the key.
func TestServiceReplaceSupersedes(t *testing.T) {
	disp := newMemDispatcher()
	svc, _ := NewService(ServiceDeps{Dispatcher: disp, Encrypter: fakeEncrypter{}, Now: newClock().now})
	first, _ := svc.Enqueue(context.Background(), Command{Kind: identity.KindEmail, Purpose: PurposeMagicLink, IdempotencyKey: "k", Envelope: Envelope{Destination: "u@x", Body: "old"}})
	second, err := svc.Replace(context.Background(), Command{Kind: identity.KindEmail, Purpose: PurposeMagicLink, IdempotencyKey: "k", Envelope: Envelope{Destination: "u@x", Body: "new"}})
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if first.JobID == second.JobID {
		t.Fatalf("Replace reused the prior execution ID")
	}
	if disp.state[first.JobID] != string(work.StatusSuperseded) {
		t.Fatalf("prior execution = %q, want superseded", disp.state[first.JobID])
	}
	st, err := svc.Status(context.Background(), "k")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.State != StatusPending {
		t.Fatalf("latest status after replace = %q, want pending (the fresh generation)", st.State)
	}
}
