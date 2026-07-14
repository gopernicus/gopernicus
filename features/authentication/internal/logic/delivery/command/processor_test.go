package command

import (
	"context"
	"testing"
	"time"
)

// The Result constructors set the right outcome and carry only secret-free reasons.
func TestResultConstructors(t *testing.T) {
	if r := Completed(); r.Outcome != OutcomeCompleted {
		t.Fatalf("Completed outcome = %v", r.Outcome)
	}
	if r := Skipped("unknown identifier"); r.Outcome != OutcomeSkipped || r.Reason != "unknown identifier" {
		t.Fatalf("Skipped = %+v", r)
	}
	when := time.Unix(1700000000, 0)
	if r := Retry(when, "deliver failed"); r.Outcome != OutcomeRetry || !r.RetryAt.Equal(when) {
		t.Fatalf("Retry = %+v", r)
	}
	if r := Permanent("exhausted"); r.Outcome != OutcomePermanent || r.Reason != "exhausted" {
		t.Fatalf("Permanent = %+v", r)
	}
}

// Outcome renders a stable, secret-free token.
func TestOutcomeString(t *testing.T) {
	cases := map[Outcome]string{
		OutcomeCompleted: "completed",
		OutcomeSkipped:   "skipped",
		OutcomeRetry:     "retry",
		OutcomePermanent: "permanent",
		Outcome(99):      "unknown",
	}
	for o, want := range cases {
		if got := o.String(); got != want {
			t.Fatalf("Outcome(%d).String() = %q, want %q", o, got, want)
		}
	}
}

// fakeProcessor confirms the collaborator ports and Processor interface are
// implementable with stdlib types only (no jobs/domain import), locking the contract
// shape AV3D-2.2 fills in.
type fakeProcessor struct {
	init  Initializer
	send  Deliverer
	clock func() time.Time
}

func (p *fakeProcessor) Process(context.Context, Claim) Result { return Completed() }

type fakeInitializer struct{}

func (fakeInitializer) Initialize(_ context.Context, e Envelope) (Envelope, bool, error) {
	return e, true, nil
}
func (fakeInitializer) Discard(context.Context, Envelope) error { return nil }

type fakeDeliverer struct{}

func (fakeDeliverer) Deliver(context.Context, Envelope) error { return nil }

type fakeCheckpointer struct{}

func (fakeCheckpointer) Checkpoint(context.Context, []byte) error { return nil }

// The contract is satisfiable and a Claim carries a claim-scoped Checkpointer.
func TestProcessorContractShape(t *testing.T) {
	var p Processor = &fakeProcessor{init: fakeInitializer{}, send: fakeDeliverer{}, clock: time.Now}
	claim := Claim{Payload: []byte("sealed"), Attempt: 1, Checkpoint: fakeCheckpointer{}}
	if r := p.Process(context.Background(), claim); r.Outcome != OutcomeCompleted {
		t.Fatalf("Process = %+v", r)
	}
	if claim.Checkpoint == nil {
		t.Fatal("claim must carry a checkpointer")
	}
}
