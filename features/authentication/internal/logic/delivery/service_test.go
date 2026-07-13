package delivery

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/features/authentication/domain/deliveryjob"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// The queue requires its store and encrypter; both are structural.
func TestNewServiceRequiresCollaborators(t *testing.T) {
	if _, err := NewService(ServiceDeps{Encrypter: fakeEncrypter{}}); !errors.Is(err, ErrRepositoryRequired) {
		t.Fatalf("missing repo err=%v, want ErrRepositoryRequired", err)
	}
	if _, err := NewService(ServiceDeps{Repo: newMemRepo()}); !errors.Is(err, ErrEncrypterRequired) {
		t.Fatalf("missing encrypter err=%v, want ErrEncrypterRequired", err)
	}
	if !errors.Is(ErrRepositoryRequired, sdk.ErrInvalidInput) || !errors.Is(ErrEncrypterRequired, sdk.ErrInvalidInput) {
		t.Fatal("queue construction errors must wrap sdk.ErrInvalidInput")
	}
}

// Enqueue seals the envelope into the job's opaque Payload: neither the
// destination nor the secret appears in the clear on the persisted row.
func TestServiceEnqueueSealsPayload(t *testing.T) {
	repo := newMemRepo()
	clk := newClock()
	// A real AEAD encrypter proves the persisted Payload is opaque ciphertext (the
	// passthrough test encrypter would not).
	enc, err := cryptids.NewAESGCM(bytes.Repeat([]byte("k"), 32))
	if err != nil {
		t.Fatalf("NewAESGCM: %v", err)
	}
	svc, _ := NewService(ServiceDeps{Repo: repo, Encrypter: enc, Now: clk.now})
	rcpt, err := svc.Enqueue(context.Background(), Command{
		Kind:           identity.KindEmail,
		Purpose:        PurposeRegistrationVerification,
		IdempotencyKey: "digest-1",
		Envelope:       Envelope{Destination: "user@example.test", Body: "code 424242", Secret: "424242"},
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if rcpt.Key != "digest-1" || rcpt.JobID == "" || rcpt.State != deliveryjob.StatePending {
		t.Fatalf("receipt = %+v", rcpt)
	}
	job, ok := repo.get(rcpt.JobID)
	if !ok {
		t.Fatalf("job not stored")
	}
	if bytes.Contains(job.Payload, []byte("user@example.test")) || bytes.Contains(job.Payload, []byte("424242")) {
		t.Fatalf("payload leaked a plaintext destination/secret: %q", job.Payload)
	}
	// The sealed payload round-trips to the original envelope.
	env, err := Open(enc, job.Payload)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if env.Destination != "user@example.test" || env.Secret != "424242" {
		t.Fatalf("round-trip mismatch: %+v", env)
	}
}

// Enqueue is idempotent by key: a double-submitted start makes no second job.
func TestServiceEnqueueIdempotent(t *testing.T) {
	repo := newMemRepo()
	clk := newClock()
	svc, _ := NewService(ServiceDeps{Repo: repo, Encrypter: fakeEncrypter{}, Now: clk.now})
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
		t.Fatalf("second enqueue made a new job: %s vs %s", a.JobID, b.JobID)
	}
	if repo.countState(deliveryjob.StatePending) != 1 {
		t.Fatalf("idempotent enqueue created more than one pending job")
	}
}

// A Command missing any structural field is rejected before touching the store.
func TestServiceEnqueueCommandIncomplete(t *testing.T) {
	svc, _ := NewService(ServiceDeps{Repo: newMemRepo(), Encrypter: fakeEncrypter{}})
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

// A seal (encryption) failure surfaces from Enqueue; no job is persisted.
func TestServiceEnqueueEncryptionFailure(t *testing.T) {
	repo := newMemRepo()
	svc, _ := NewService(ServiceDeps{Repo: repo, Encrypter: fakeEncrypter{encErr: errBoom}})
	_, err := svc.Enqueue(context.Background(), Command{Kind: identity.KindEmail, Purpose: PurposeMagicLink, IdempotencyKey: "k", Envelope: Envelope{Destination: "u@x", Body: "b"}})
	if !errors.Is(err, errBoom) {
		t.Fatalf("Enqueue err=%v, want errBoom", err)
	}
	if len(repo.jobs) != 0 {
		t.Fatalf("a job was persisted despite the seal failure")
	}
}

// Replace supersedes any prior pending job holding the key.
func TestServiceReplaceSupersedes(t *testing.T) {
	repo := newMemRepo()
	clk := newClock()
	svc, _ := NewService(ServiceDeps{Repo: repo, Encrypter: fakeEncrypter{}, Now: clk.now})
	first, _ := svc.Enqueue(context.Background(), Command{Kind: identity.KindEmail, Purpose: PurposeMagicLink, IdempotencyKey: "k", Envelope: Envelope{Destination: "u@x", Body: "old"}})
	second, err := svc.Replace(context.Background(), Command{Kind: identity.KindEmail, Purpose: PurposeMagicLink, IdempotencyKey: "k", Envelope: Envelope{Destination: "u@x", Body: "new"}})
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if first.JobID == second.JobID {
		t.Fatalf("Replace reused the prior job ID")
	}
	if j, _ := repo.get(first.JobID); j.State != deliveryjob.StateCanceled {
		t.Fatalf("prior job = %q, want canceled", j.State)
	}
	if repo.countState(deliveryjob.StatePending) != 1 {
		t.Fatalf("more than one pending job after replace")
	}
}
