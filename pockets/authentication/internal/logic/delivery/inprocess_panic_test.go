package delivery

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk/capabilities/work"
)

// ---------------------------------------------------------------------------
// IX-08 — in-process delivery panic containment
//
// These prove that a panic in the per-job execution boundary (the provider render/send
// or the engine step) is CONTAINED by the narrow recover in InProcessRuntime.handleOnce:
// the panicking job converts to a terminal (dead_letter) outcome with lifecycle evidence
// (execution ID + a SANITIZED panic value — never the decrypted payload/destination/
// secret), the worker/pool keeps processing subsequent jobs, and shutdown stays clean.
// Without the recover a provider/engine panic propagates out of the worker goroutine and
// takes down the entire host, HTTP included. All run under -race.
// ---------------------------------------------------------------------------

// syncBuffer is a mutex-guarded byte buffer so the runtime's concurrent slog writes are
// race-free while a test captures and asserts over the emitted lifecycle evidence.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// panicProcessor panics on its FIRST Handle invocation (modeling a provider or engine that
// blows up) and completes every subsequent invocation, so a test can prove the worker
// survives the panic and keeps processing. panicBeforeCheckpoint models an engine-step
// panic (before any provider send); otherwise the panic models a provider render/send panic
// after the checkpoint has been taken.
type panicProcessor struct {
	firstDone           atomic.Bool
	panicValue          any
	panicBeforeCheckpnt bool
	completed           atomic.Int32
	discarded           atomic.Int32
}

func (p *panicProcessor) Handle(ctx context.Context, _ string, _ []byte, _ int, checkpoint func(ctx context.Context, sealed []byte) error) error {
	if p.firstDone.CompareAndSwap(false, true) {
		if p.panicBeforeCheckpnt {
			panic(p.panicValue) // engine-step panic: before any provider send
		}
		_ = checkpoint(ctx, nil)
		panic(p.panicValue) // provider render/send panic: after the checkpoint
	}
	if err := checkpoint(ctx, nil); err != nil {
		return err
	}
	p.completed.Add(1)
	return nil
}

func (p *panicProcessor) Discard(context.Context, string, []byte) error {
	p.discarded.Add(1)
	return nil
}

// TestInProcessRuntimeProviderPanicDeadLettersAndContinues proves a provider-send panic is
// contained: the job dead-letters with secret-free evidence, the panicking payload never
// appears in that evidence, and the worker keeps processing subsequent jobs.
func TestInProcessRuntimeProviderPanicDeadLettersAndContinues(t *testing.T) {
	t.Parallel()
	const secret = "TOP-SECRET-DECRYPTED-PAYLOAD-9f2c"
	logbuf := &syncBuffer{}
	log := slog.New(slog.NewTextHandler(logbuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	q := NewInProcessQueue(InProcessQueueConfig{Capacity: 8})
	// The provider panics with a value that EMBEDS the secret, proving sanitization: a
	// string panic is reduced to its Go type, so the embedded secret can never reach a log.
	proc := &panicProcessor{panicValue: fmt.Sprintf("provider exploded sending to %s", secret)}
	rt, err := NewInProcessRuntime(q, proc, InProcessRuntimeConfig{Workers: 1, ShutdownDeadline: time.Second, Logger: log})
	if err != nil {
		t.Fatalf("NewInProcessRuntime: %v", err)
	}

	// The panicking job carries the secret as its payload too, so we can assert the payload
	// never leaks into the panic evidence.
	boom, err := q.Submit(context.Background(), "authentication.delivery", "verification", "boom", []byte(secret))
	if err != nil {
		t.Fatalf("Submit boom: %v", err)
	}
	if _, err := q.Submit(context.Background(), "authentication.delivery", "verification", "ok", []byte("healthy")); err != nil {
		t.Fatalf("Submit ok: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- rt.Run(ctx) }()

	waitFor(t, 3*time.Second, func() bool {
		boomState, _ := q.LatestStatus(context.Background(), "boom")
		okState, _ := q.LatestStatus(context.Background(), "ok")
		return boomState == string(work.StatusDeadLetter) && okState == string(work.StatusCompleted)
	})
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned a non-nil error after a contained panic: %v", err)
	}

	if got := proc.completed.Load(); got != 1 {
		t.Fatalf("healthy job completions = %d, want 1 — the worker did not survive the panic to process the next job", got)
	}
	if got := proc.discarded.Load(); got < 1 {
		t.Fatalf("dead-lettered challenge discards = %d, want >= 1 — the terminal path did not run", got)
	}

	out := logbuf.String()
	if !strings.Contains(out, "panic recovered in in-process delivery job") {
		t.Fatalf("panic recover evidence missing from the log; got:\n%s", out)
	}
	if !strings.Contains(out, boom) {
		t.Fatalf("panic evidence does not name the panicking execution ID %q; got:\n%s", boom, out)
	}
	if strings.Contains(out, secret) {
		t.Fatalf("SECRET LEAK: the decrypted payload/panic content appears in the lifecycle evidence")
	}
}

// TestInProcessRuntimeEngineStepPanicDeadLetters proves an engine-step panic (before any
// provider send) is contained identically: terminal dead-letter, secret-free evidence, and
// the worker keeps running.
func TestInProcessRuntimeEngineStepPanicDeadLetters(t *testing.T) {
	t.Parallel()
	const secret = "ENGINE-SECRET-4a17"
	logbuf := &syncBuffer{}
	log := slog.New(slog.NewTextHandler(logbuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	q := NewInProcessQueue(InProcessQueueConfig{Capacity: 8})
	proc := &panicProcessor{panicValue: fmt.Errorf("engine step failed: %s", secret), panicBeforeCheckpnt: true}
	rt, err := NewInProcessRuntime(q, proc, InProcessRuntimeConfig{Workers: 1, ShutdownDeadline: time.Second, Logger: log})
	if err != nil {
		t.Fatalf("NewInProcessRuntime: %v", err)
	}

	boom, err := q.Submit(context.Background(), "authentication.delivery", "verification", "boom", []byte(secret))
	if err != nil {
		t.Fatalf("Submit boom: %v", err)
	}
	if _, err := q.Submit(context.Background(), "authentication.delivery", "verification", "ok", []byte("healthy")); err != nil {
		t.Fatalf("Submit ok: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- rt.Run(ctx) }()

	waitFor(t, 3*time.Second, func() bool {
		boomState, _ := q.LatestStatus(context.Background(), "boom")
		okState, _ := q.LatestStatus(context.Background(), "ok")
		return boomState == string(work.StatusDeadLetter) && okState == string(work.StatusCompleted)
	})
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned a non-nil error after a contained engine-step panic: %v", err)
	}

	if got := proc.completed.Load(); got != 1 {
		t.Fatalf("healthy job completions = %d, want 1 — the worker did not survive the engine-step panic", got)
	}
	out := logbuf.String()
	if !strings.Contains(out, boom) {
		t.Fatalf("panic evidence does not name the panicking execution ID %q; got:\n%s", boom, out)
	}
	if strings.Contains(out, secret) {
		t.Fatalf("SECRET LEAK: the engine-step panic content appears in the lifecycle evidence")
	}
}

// TestInProcessRuntimePanicCleanShutdown proves that after a job panics, the runtime still
// shuts down cleanly (Run returns nil) — a contained panic never wedges a worker slot.
func TestInProcessRuntimePanicCleanShutdown(t *testing.T) {
	t.Parallel()
	q := NewInProcessQueue(InProcessQueueConfig{Capacity: 4})
	proc := &panicProcessor{panicValue: "boom"}
	rt, err := NewInProcessRuntime(q, proc, InProcessRuntimeConfig{Workers: 2, ShutdownDeadline: time.Second})
	if err != nil {
		t.Fatalf("NewInProcessRuntime: %v", err)
	}

	if _, err := q.Submit(context.Background(), "authentication.delivery", "verification", "boom", []byte("x")); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- rt.Run(ctx) }()

	waitFor(t, 3*time.Second, func() bool {
		s, _ := q.LatestStatus(context.Background(), "boom")
		return s == string(work.StatusDeadLetter)
	})
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run did not shut down cleanly after a contained panic: %v", err)
	}
}

// TestSanitizePanicSurfacesRuntimeErrorsHidesArbitraryValues proves the sanitizer surfaces a
// runtime-generated panic verbatim (no application data) but reduces any other value to its
// Go type so a secret-bearing panic value never reaches the evidence.
func TestSanitizePanicSurfacesRuntimeErrorsHidesArbitraryValues(t *testing.T) {
	t.Parallel()

	// A non-runtime value (string / error) must be reduced to its type, never its content.
	if got := sanitizePanic("secret-payload"); strings.Contains(got, "secret-payload") {
		t.Fatalf("sanitizePanic leaked a string panic value: %q", got)
	}
	if got := sanitizePanic(fmt.Errorf("boom: secret-payload")); strings.Contains(got, "secret-payload") {
		t.Fatalf("sanitizePanic leaked an error panic value: %q", got)
	}

	// A runtime error carries no application data, so its message is safe to surface.
	var m map[string]string
	func() {
		defer func() {
			rec := recover()
			if rec == nil {
				t.Fatal("expected a runtime panic from a nil-map write")
			}
			if got := sanitizePanic(rec); !strings.Contains(got, "nil map") {
				t.Fatalf("sanitizePanic dropped a safe runtime-error message: %q", got)
			}
		}()
		m["k"] = "v" // nil map write: a runtime.Error
	}()
}

// compile assertion: the panic fake satisfies the runtime's processor seam.
var _ InProcessProcessor = (*panicProcessor)(nil)
