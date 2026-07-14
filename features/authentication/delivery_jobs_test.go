package authentication

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// ---------------------------------------------------------------------------
// AV3D-3.1 — jobs-mode composition seam (DeliveryDispatcher + DeliveryJobRuntime)
//
// A jobs-mode host wires a stdlib-typed DeliveryDispatcher (a composition adapter
// over the generic jobs feature) and reads the DeliveryJobRuntime seam to register
// the delivery processor on the jobs runtime. These tests prove the seam is exposed
// exactly when the dispatcher is wired, that construction/registration start no work
// on the transport, and that producers submit versioned, sealed command envelopes
// through the dispatcher.
// ---------------------------------------------------------------------------

// recordingDispatcher captures every transport call so a test can assert that
// construction and Register start no work, and that producers submit through it.
type recordingDispatcher struct {
	mu      sync.Mutex
	submits []recordedSubmit
}

type recordedSubmit struct {
	op                 string // "submit" or "replace"
	kind, purpose, key string
	payload            []byte
}

func (d *recordingDispatcher) Submit(_ context.Context, kind, purpose, key string, payload []byte) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.submits = append(d.submits, recordedSubmit{op: "submit", kind: kind, purpose: purpose, key: key, payload: payload})
	return "exec-1", nil
}

func (d *recordingDispatcher) Replace(_ context.Context, kind, purpose, key string, payload []byte) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.submits = append(d.submits, recordedSubmit{op: "replace", kind: kind, purpose: purpose, key: key, payload: payload})
	return "exec-2", nil
}

func (d *recordingDispatcher) LatestStatus(context.Context, string) (string, error) {
	return "pending", nil
}

func (d *recordingDispatcher) calls() []recordedSubmit {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]recordedSubmit, len(d.submits))
	copy(out, d.submits)
	return out
}

func jobsModeConfig(disp DeliveryDispatcher) Config {
	c := deliveryDevConfig()
	c.DeliveryMode = DeliveryModeJobs
	c.DeliveryEncrypter = stubEncrypter{}
	c.DeliveryDispatcher = disp
	return c
}

// TestDeliveryJobRuntime_JobsModeExposed proves that wiring a DeliveryDispatcher in
// jobs mode exposes the DeliveryJobRuntime seam (kind + non-nil handler + discard),
// and that neither NewService nor Register touches the transport — the host runs the
// jobs runtime explicitly (Register starts no goroutines).
func TestDeliveryJobRuntime_JobsModeExposed(t *testing.T) {
	disp := &recordingDispatcher{}
	svc, err := NewService(Repositories{}, jobsModeConfig(disp))
	if err != nil {
		t.Fatalf("NewService (jobs mode + dispatcher): %v", err)
	}

	rt, ok := svc.DeliveryJobRuntime()
	if !ok {
		t.Fatal("DeliveryJobRuntime not exposed in jobs mode with a wired dispatcher")
	}
	if rt.Kind != DeliveryJobKind {
		t.Fatalf("rt.Kind = %q, want %q", rt.Kind, DeliveryJobKind)
	}
	if rt.Handle == nil || rt.Discard == nil {
		t.Fatal("DeliveryJobRuntime handler/discard is nil")
	}

	if err := svc.Register(feature.Mount{Router: web.NewWebHandler()}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	// Give any (erroneously) started goroutine a chance to submit/claim before asserting.
	time.Sleep(20 * time.Millisecond)
	if got := len(disp.calls()); got != 0 {
		t.Fatalf("dispatcher touched %d times by NewService/Register — construction must start no work", got)
	}
}

// TestDeliveryJobRuntime_UnavailableWithoutDispatcher proves the seam is closed in
// every mode that is not jobs-over-generic-jobs: in_process (the host runs
// RunDelivery, not a jobs runtime) and off.
func TestDeliveryJobRuntime_UnavailableWithoutDispatcher(t *testing.T) {
	inProc := deliveryDevConfig()
	inProc.DeliveryMode = DeliveryModeInProcess
	inProc.DeliveryEncrypter = stubEncrypter{} // in_process seals its bounded-queue payload (AV3D-4.1)
	svc, err := NewService(Repositories{}, inProc)
	if err != nil {
		t.Fatalf("NewService (in_process): %v", err)
	}
	if _, ok := svc.DeliveryJobRuntime(); ok {
		t.Fatal("DeliveryJobRuntime exposed in in_process mode")
	}

	off := deliveryDevConfig()
	off.DeliveryMode = DeliveryModeOff
	svc2, err := NewService(Repositories{}, off)
	if err != nil {
		t.Fatalf("NewService (off): %v", err)
	}
	if _, ok := svc2.DeliveryJobRuntime(); ok {
		t.Fatal("DeliveryJobRuntime exposed in off mode (no delivery runtime)")
	}
}

// TestJobsModeProducerSubmitsSealedCommand proves a producer (forgot-password) admits
// through the wired dispatcher as an OPAQUE, versioned command envelope — the codec
// swap the jobs-mode dispatcher requires — carrying no secret. With the identity
// stubEncrypter the sealed payload is the plaintext command.Envelope JSON, so a
// canary check confirms the versioned/opaque shape and the absence of a rendered
// secret.
func TestJobsModeProducerSubmitsSealedCommand(t *testing.T) {
	disp := &recordingDispatcher{}
	svc, err := NewService(Repositories{}, jobsModeConfig(disp))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	if err := svc.ForgotPassword(context.Background(), "user@example.com"); err != nil {
		t.Fatalf("ForgotPassword: %v", err)
	}

	calls := disp.calls()
	if len(calls) != 1 {
		t.Fatalf("dispatcher submits = %d, want 1", len(calls))
	}
	c := calls[0]
	if c.op != "submit" {
		t.Fatalf("op = %q, want submit (submit-once)", c.op)
	}
	if c.key == "" {
		t.Fatal("submit carried an empty logical key")
	}
	payload := string(c.payload)
	// The versioned command envelope: opaque stage, current version, no rendered secret.
	if !strings.Contains(payload, `"stage":"opaque"`) {
		t.Fatalf("payload is not an opaque command envelope: %s", payload)
	}
	if !strings.Contains(payload, `"version":1`) {
		t.Fatalf("payload is not the versioned command envelope: %s", payload)
	}
	if strings.Contains(payload, `"secret"`) {
		t.Fatalf("opaque admission leaked a secret field: %s", payload)
	}
}
