package command

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"
)

// revEncrypter is a reversible, non-secret test encrypter. decErr forces the
// undecryptable-payload path.
type revEncrypter struct{ decErr error }

func (revEncrypter) Encrypt(plaintext string) (string, error) { return "enc:" + plaintext, nil }

func (e revEncrypter) Decrypt(ciphertext string) (string, error) {
	if e.decErr != nil {
		return "", e.decErr
	}
	return strings.TrimPrefix(ciphertext, "enc:"), nil
}

// recInit records Initialize/Discard and renders a fixed rendered command.
type recInit struct {
	mu       sync.Mutex
	rendered Envelope
	deliver  bool
	err      error
	inits    int
	discards int
}

func (i *recInit) Initialize(context.Context, Envelope) (Envelope, bool, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.inits++
	if i.err != nil {
		return Envelope{}, false, i.err
	}
	return i.rendered, i.deliver, nil
}

func (i *recInit) Discard(context.Context, Envelope) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.discards++
	return nil
}

func (i *recInit) initCount() int    { i.mu.Lock(); defer i.mu.Unlock(); return i.inits }
func (i *recInit) discardCount() int { i.mu.Lock(); defer i.mu.Unlock(); return i.discards }

// recDeliverer records sends, can fail the first N, and can block until cancellation.
type recDeliverer struct {
	mu        sync.Mutex
	sends     []Envelope
	failFirst int
	block     bool
}

func (d *recDeliverer) Deliver(ctx context.Context, env Envelope) error {
	if d.block {
		<-ctx.Done()
		return ctx.Err()
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sends = append(d.sends, env)
	if len(d.sends) <= d.failFirst {
		return errors.New("send failed to +15550001111 secret=leaked")
	}
	return nil
}

func (d *recDeliverer) count() int { d.mu.Lock(); defer d.mu.Unlock(); return len(d.sends) }

// capCheckpoint captures the last checkpointed bytes and can fail on demand.
type capCheckpoint struct {
	last []byte
	n    int
	err  error
}

func (c *capCheckpoint) Checkpoint(_ context.Context, sealed []byte) error {
	if c.err != nil {
		return c.err
	}
	c.n++
	c.last = sealed
	return nil
}

func renderedEnvelope(secret string) Envelope {
	env, _ := NewRendered("phone", "characterization", "k", "+15550001111", "", "deliver:"+secret, "", secret)
	return env
}

func opaqueEnvelope() Envelope {
	env, _ := NewOpaque("phone", "characterization", "k", "+15550001111")
	return env
}

func newEngine(t *testing.T, encrypter cryptids.Encrypter, deliverer Deliverer, opts ...Option) *Engine {
	t.Helper()
	if encrypter == nil {
		encrypter = revEncrypter{}
	}
	e, err := NewProcessor(encrypter, deliverer, opts...)
	if err != nil {
		t.Fatalf("NewProcessor: %v", err)
	}
	return e
}

func seal(t *testing.T, env Envelope) []byte {
	t.Helper()
	b, err := Seal(revEncrypter{}, env)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	return b
}

// NewProcessor rejects a missing encrypter or deliverer.
func TestNewProcessorRequiresCollaborators(t *testing.T) {
	if _, err := NewProcessor(nil, &recDeliverer{}); !errors.Is(err, ErrEncrypterRequired) {
		t.Fatalf("missing encrypter err=%v, want ErrEncrypterRequired", err)
	}
	if _, err := NewProcessor(revEncrypter{}, nil); !errors.Is(err, ErrDelivererRequired) {
		t.Fatalf("missing deliverer err=%v, want ErrDelivererRequired", err)
	}
}

// A rendered command delivers once and completes.
func TestProcessDeliversRendered(t *testing.T) {
	del := &recDeliverer{}
	e := newEngine(t, nil, del)
	r := e.Process(context.Background(), Claim{Payload: seal(t, renderedEnvelope("S1")), Attempt: 1, Checkpoint: &capCheckpoint{}})
	if r.Outcome != OutcomeCompleted {
		t.Fatalf("outcome = %v, want completed", r.Outcome)
	}
	if del.count() != 1 {
		t.Fatalf("sends = %d, want 1", del.count())
	}
}

// An opaque command is resolved+rendered once, its rendered form checkpointed BEFORE
// the send, and the checkpointed bytes decode to the rendered secret.
func TestProcessCheckpointsBeforeSend(t *testing.T) {
	init := &recInit{rendered: renderedEnvelope("CHK"), deliver: true}
	del := &recDeliverer{}
	chk := &capCheckpoint{}
	e := newEngine(t, nil, del, WithInitializer(init))

	r := e.Process(context.Background(), Claim{Payload: seal(t, opaqueEnvelope()), Attempt: 1, Checkpoint: chk})
	if r.Outcome != OutcomeCompleted {
		t.Fatalf("outcome = %v, want completed", r.Outcome)
	}
	if init.initCount() != 1 {
		t.Fatalf("inits = %d, want 1", init.initCount())
	}
	if chk.n != 1 || del.count() != 1 {
		t.Fatalf("checkpoint=%d sends=%d, want 1/1", chk.n, del.count())
	}
	got, err := Open(revEncrypter{}, chk.last)
	if err != nil {
		t.Fatalf("open checkpointed bytes: %v", err)
	}
	if got.Stage != StageRendered || !strings.Contains(got.Body, "CHK") {
		t.Fatalf("checkpointed payload not the rendered command: %+v", got)
	}
}

// A checkpoint failure aborts before any send and reschedules (bounded retry).
func TestProcessNoSendWhenCheckpointFails(t *testing.T) {
	init := &recInit{rendered: renderedEnvelope("X"), deliver: true}
	del := &recDeliverer{}
	e := newEngine(t, nil, del, WithInitializer(init), WithPolicy(Config{MaxAttempts: 5, Backoff: func(int) time.Duration { return time.Second }}))

	r := e.Process(context.Background(), Claim{Payload: seal(t, opaqueEnvelope()), Attempt: 1, Checkpoint: &capCheckpoint{err: errors.New("stale claim")}})
	if r.Outcome != OutcomeRetry {
		t.Fatalf("outcome = %v, want retry", r.Outcome)
	}
	if del.count() != 0 {
		t.Fatalf("a send happened before/despite a failed checkpoint: %d", del.count())
	}
}

// deliver=false (unknown identifier) skips with no send.
func TestProcessSkipsUnknownIdentifier(t *testing.T) {
	init := &recInit{deliver: false}
	del := &recDeliverer{}
	e := newEngine(t, nil, del, WithInitializer(init))
	r := e.Process(context.Background(), Claim{Payload: seal(t, opaqueEnvelope()), Attempt: 1, Checkpoint: &capCheckpoint{}})
	if r.Outcome != OutcomeSkipped {
		t.Fatalf("outcome = %v, want skipped", r.Outcome)
	}
	if del.count() != 0 {
		t.Fatalf("skip produced a send: %d", del.count())
	}
}

// An opaque command with no initializer fails permanently rather than sending.
func TestProcessOpaqueWithoutInitializerIsPermanent(t *testing.T) {
	e := newEngine(t, nil, &recDeliverer{})
	r := e.Process(context.Background(), Claim{Payload: seal(t, opaqueEnvelope()), Attempt: 1, Checkpoint: &capCheckpoint{}})
	if r.Outcome != OutcomePermanent {
		t.Fatalf("outcome = %v, want permanent", r.Outcome)
	}
}

// An unopenable payload is a permanent failure with a secret-free reason.
func TestProcessUnopenablePayloadIsPermanent(t *testing.T) {
	e := newEngine(t, revEncrypter{decErr: errors.New("bad")}, &recDeliverer{})
	r := e.Process(context.Background(), Claim{Payload: []byte("enc:{}"), Attempt: 1, Checkpoint: &capCheckpoint{}})
	if r.Outcome != OutcomePermanent {
		t.Fatalf("outcome = %v, want permanent", r.Outcome)
	}
}

// A transient deliver failure retries below the cap and fails permanently at it. The
// reason never carries the deliverer's raw error (which names a destination/secret).
func TestProcessClassifiesDeliverFailure(t *testing.T) {
	del := &recDeliverer{failFirst: 100}
	e := newEngine(t, nil, del, WithPolicy(Config{MaxAttempts: 2, Backoff: func(int) time.Duration { return time.Second }}))

	below := e.Process(context.Background(), Claim{Payload: seal(t, renderedEnvelope("S")), Attempt: 1, Checkpoint: &capCheckpoint{}})
	if below.Outcome != OutcomeRetry || below.RetryAt.IsZero() {
		t.Fatalf("below cap = %+v, want retry with a time", below)
	}
	at := e.Process(context.Background(), Claim{Payload: seal(t, renderedEnvelope("S")), Attempt: 2, Checkpoint: &capCheckpoint{}})
	if at.Outcome != OutcomePermanent {
		t.Fatalf("at cap = %+v, want permanent", at)
	}
	for _, r := range []Result{below, at} {
		if strings.Contains(r.Reason, "leaked") || strings.Contains(r.Reason, "+15550001111") {
			t.Fatalf("reason leaked a secret/destination: %q", r.Reason)
		}
	}
}

// A blocked provider is bounded by the provider timeout: Process returns promptly and
// reschedules rather than hanging.
func TestProcessBoundsProviderTimeout(t *testing.T) {
	del := &recDeliverer{block: true}
	e := newEngine(t, nil, del, WithPolicy(Config{MaxAttempts: 10, ProviderTimeout: 20 * time.Millisecond, Backoff: func(int) time.Duration { return time.Minute }}))

	done := make(chan Result, 1)
	go func() {
		done <- e.Process(context.Background(), Claim{Payload: seal(t, renderedEnvelope("S")), Attempt: 1, Checkpoint: &capCheckpoint{}})
	}()
	select {
	case r := <-done:
		if r.Outcome != OutcomeRetry {
			t.Fatalf("timed-out send = %v, want retry", r.Outcome)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Process did not return within the provider timeout (unbounded send)")
	}
}

// Discard opens the checkpointed payload and voids the challenge once; a nil
// initializer is a no-op.
func TestDiscardVoidsChallenge(t *testing.T) {
	init := &recInit{rendered: renderedEnvelope("D"), deliver: true}
	e := newEngine(t, nil, &recDeliverer{}, WithInitializer(init))
	if err := e.Discard(context.Background(), Claim{Payload: seal(t, renderedEnvelope("D"))}); err != nil {
		t.Fatalf("Discard: %v", err)
	}
	if init.discardCount() != 1 {
		t.Fatalf("discards = %d, want 1", init.discardCount())
	}

	noInit := newEngine(t, nil, &recDeliverer{})
	if err := noInit.Discard(context.Background(), Claim{Payload: seal(t, renderedEnvelope("D"))}); err != nil {
		t.Fatalf("nil-initializer Discard should be a no-op, got %v", err)
	}
}

// Engine satisfies both the Processor and Discarder seams a transport accepts.
func TestEngineSatisfiesSeams(t *testing.T) {
	e := newEngine(t, nil, &recDeliverer{})
	var _ Processor = e
	var _ Discarder = e
}

func TestProcessorOptionsResolvePolicyAndClock(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	policy := Config{MaxAttempts: 2, Backoff: func(int) time.Duration { return time.Minute }}
	option := WithPolicy(policy)
	policy.MaxAttempts = -1
	e := newEngine(t, nil, &recDeliverer{}, option, WithClock(func() time.Time { return now }))
	if got := e.classify(1, "retry"); got.Outcome != OutcomeRetry || !got.RetryAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("retry = %+v", got)
	}
	if got := e.classify(2, "stop"); got.Outcome != OutcomePermanent {
		t.Fatalf("terminal = %+v", got)
	}
	if _, err := NewProcessor(revEncrypter{}, &recDeliverer{}, nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("nil option: %v", err)
	}
	if _, err := NewProcessor(revEncrypter{}, &recDeliverer{}, WithPolicy(policy)); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("negative policy: %v", err)
	}
	var initializer *recInit
	if _, err := NewProcessor(revEncrypter{}, &recDeliverer{}, WithInitializer(initializer)); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("typed nil initializer: %v", err)
	}
}
