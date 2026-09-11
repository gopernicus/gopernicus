package deliverychar

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// errProvider is the neutral transport-failure sentinel a primed Provider returns.
// It carries no destination or secret.
var errProvider = errors.New("deliverychar: provider send failed")

// settleRounds bounds the settle loop so a runtime that never reaches a terminal
// state fails loudly instead of hanging.
const settleRounds = 200

// settleStep is the clock advance between settle rounds — larger than any backoff or
// lease window the cases configure, so retryable work becomes runnable again.
const settleStep = time.Minute

// Run executes the whole transport-neutral characterization suite against newHarness.
// The current worker is the first caller; the generic-jobs and bounded-pool runtimes
// (phases 3-4) call it with their own Factory to prove identical observable behavior.
// Each case builds its own Scenario, so cases never share mutable state.
func Run(t *testing.T, newHarness Factory) {
	t.Helper()
	cases := []struct {
		name string
		run  func(t *testing.T, newHarness Factory)
	}{
		{"DuplicateAdmissionOneExecution", caseDuplicateAdmission},
		{"ReplaceSupersedesPriorActive", caseReplaceSupersedes},
		{"OpaqueInitializationOffRequestPath", caseOpaqueOffRequestPath},
		{"CheckpointPrecedesSendRetryReusesPayload", caseCheckpointBeforeSend},
		{"CrashAfterSendReplaysSameSecret", caseCrashReplaysSameSecret},
		{"UnknownIdentifierSkipsWithoutSend", caseUnknownSkips},
		{"TransientFailureRetriesThenSucceeds", caseTransientRetry},
		{"PermanentFailureIsTerminalAndDiscards", casePermanentTerminal},
		{"ProviderTimeoutIsBounded", caseProviderTimeout},
		{"PurgeRespectsRetention", casePurgeRetention},
		{"StatusIsLifecycleOnlyNoSecretLeak", caseStatusNoLeak},
		{"KnownUnknownRequestPathParity", caseKnownUnknownParity},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.run(t, newHarness) })
	}
}

// settle drains and advances the runtime in bounded rounds until key reaches a
// terminal state, then returns that observation. It advances by more than any
// configured backoff/lease so rescheduled work (retry) and reclaimable work (crash)
// both progress. A key that never settles is a loud failure, not a hang.
func settle(t *testing.T, h Harness, key string) Observation {
	t.Helper()
	for i := 0; i < settleRounds; i++ {
		h.Drain(t)
		obs, ok := h.Status(t, key)
		if ok && obs.Terminal() {
			return obs
		}
		h.Advance(settleStep)
	}
	t.Fatalf("work %q never reached a terminal state", key)
	return Observation{}
}

// caseDuplicateAdmission: a double-submitted start under one key produces exactly one
// active execution — one provider send and one terminal delivery, never two.
func caseDuplicateAdmission(t *testing.T, newHarness Factory) {
	prov := NewProvider(0, false)
	h := newHarness(t, Scenario{Provider: prov})

	k1 := h.Submit(t, RenderedSubmission("dup", "S-DUP"))
	k2 := h.Submit(t, RenderedSubmission("dup", "S-DUP"))
	if k1 != k2 {
		t.Fatalf("duplicate submit returned distinct keys %q != %q", k1, k2)
	}
	obs := settle(t, h, k1)
	if !obs.Delivered() {
		t.Fatalf("duplicate admission did not deliver once: %+v", obs)
	}
	if prov.Count() != 1 {
		t.Fatalf("duplicate admission produced %d sends, want exactly 1", prov.Count())
	}
}

// caseReplaceSupersedes: an explicit resend (Replace) creates fresh work and
// supersedes the prior active work — only the replacement is delivered, and the
// superseded content never reaches the provider.
func caseReplaceSupersedes(t *testing.T, newHarness Factory) {
	prov := NewProvider(0, false)
	h := newHarness(t, Scenario{Provider: prov})

	h.Submit(t, RenderedSubmission("res", "OLD"))
	k := h.Replace(t, RenderedSubmission("res", "NEW"))
	obs := settle(t, h, k)
	if !obs.Delivered() {
		t.Fatalf("replacement did not deliver: %+v", obs)
	}
	sends := prov.Sends()
	if len(sends) != 1 {
		t.Fatalf("replace produced %d sends, want exactly 1 (superseded work must not deliver)", len(sends))
	}
	if !strings.Contains(sends[0].Body, "NEW") || strings.Contains(sends[0].Body, "OLD") {
		t.Fatalf("delivered the wrong generation: %q", sends[0].Body)
	}
}

// caseOpaqueOffRequestPath: submitting opaque work performs NO resolution and NO
// provider call — the initializer runs only when work is later run, never on the
// admission (request) path.
func caseOpaqueOffRequestPath(t *testing.T, newHarness Factory) {
	prov := NewProvider(0, false)
	init := NewInitializer("OPAQUE-TOK", true)
	h := newHarness(t, Scenario{Provider: prov, Initializer: init})

	k := h.Submit(t, OpaqueSubmission("opq"))

	// Admission resolved nothing and sent nothing.
	if init.Inits() != 0 {
		t.Fatalf("submit resolved on the request path: %d initializations", init.Inits())
	}
	if prov.Count() != 0 {
		t.Fatalf("submit called the provider on the request path: %d sends", prov.Count())
	}
	if obs, ok := h.Status(t, k); !ok || !obs.Pending {
		t.Fatalf("admitted opaque work not pending: obs=%+v ok=%v", obs, ok)
	}

	// Running the work resolves and delivers, off the request path.
	obs := settle(t, h, k)
	if !obs.Delivered() {
		t.Fatalf("opaque work never delivered: %+v", obs)
	}
	if init.Inits() != 1 {
		t.Fatalf("opaque work resolved %d times off-path, want exactly 1", init.Inits())
	}
	if prov.Count() != 1 {
		t.Fatalf("opaque work sent %d times, want exactly 1", prov.Count())
	}
}

// caseCheckpointBeforeSend: an opaque unit is resolved+rendered ONCE and that
// rendered payload is checkpointed before the provider send, so a transient failure
// retries the byte-identical secret rather than re-resolving a fresh one.
func caseCheckpointBeforeSend(t *testing.T, newHarness Factory) {
	prov := NewProvider(1, false) // fail the first send → force a retry
	init := NewInitializer("CHK-9", true)
	h := newHarness(t, Scenario{
		Provider:    prov,
		Initializer: init,
		Backoff:     func(int) time.Duration { return 5 * time.Second },
	})

	k := h.Submit(t, OpaqueSubmission("chk"))
	obs := settle(t, h, k)
	if !obs.Delivered() {
		t.Fatalf("checkpointed work never delivered: %+v", obs)
	}
	if init.Inits() != 1 {
		t.Fatalf("work was re-resolved on retry: %d initializations, want 1 (checkpoint before send)", init.Inits())
	}
	sends := prov.Sends()
	if len(sends) != 2 {
		t.Fatalf("expected one failed + one retried send, got %d", len(sends))
	}
	if sends[0].Body != sends[1].Body {
		t.Fatalf("retry sent a different payload: %q != %q (secret must be reused, not re-minted)", sends[0].Body, sends[1].Body)
	}
	if !strings.Contains(sends[1].Body, "CHK-9") {
		t.Fatalf("retry did not carry the checkpointed secret: %q", sends[1].Body)
	}
}

// caseCrashReplaysSameSecret: a crash after the provider accepts but before the
// completion is recorded may DUPLICATE the send (at-least-once), but the duplicate
// carries the identical secret — a runtime never mints a new one to recover.
func caseCrashReplaysSameSecret(t *testing.T, newHarness Factory) {
	prov := NewProvider(0, false)
	h := newHarness(t, Scenario{
		Provider:         prov,
		CrashCompletions: 1,           // fail recording success once, after the send
		LeaseFor:         time.Second, // short hold so the crashed unit is reclaimable
	})

	k := h.Submit(t, RenderedSubmission("crash", "SAME-SECRET"))
	obs := settle(t, h, k)
	if !obs.Delivered() {
		t.Fatalf("crashed work never recovered to delivered: %+v", obs)
	}
	sends := prov.Sends()
	if len(sends) != 2 {
		t.Fatalf("crash-after-send produced %d sends, want 2 (original + at-least-once replay)", len(sends))
	}
	if sends[0].Body != sends[1].Body {
		t.Fatalf("replay minted a new secret: %q != %q", sends[0].Body, sends[1].Body)
	}
	if !strings.Contains(sends[1].Body, "SAME-SECRET") {
		t.Fatalf("replay did not carry the original secret: %q", sends[1].Body)
	}
}

// caseUnknownSkips: an unknown/ineligible identifier resolves to "nothing to
// deliver" and terminates WITHOUT a provider call — indistinguishable, to the
// requester, from a delivered known identifier.
func caseUnknownSkips(t *testing.T, newHarness Factory) {
	prov := NewProvider(0, false)
	init := NewInitializer("never", false) // deliver=false: unknown identifier
	h := newHarness(t, Scenario{Provider: prov, Initializer: init})

	k := h.Submit(t, OpaqueSubmission("unknown"))
	obs := settle(t, h, k)
	if prov.Count() != 0 {
		t.Fatalf("unknown identifier produced %d sends, want 0", prov.Count())
	}
	if obs.Failed || !obs.Delivered() {
		t.Fatalf("unknown identifier must terminate as a non-failed success (indistinguishable), got %+v", obs)
	}
	if init.Inits() != 1 {
		t.Fatalf("unknown identifier resolved %d times, want 1", init.Inits())
	}
}

// caseTransientRetry: a transient provider failure reschedules with backoff; a later
// run delivers the identical secret and succeeds within the attempt budget.
func caseTransientRetry(t *testing.T, newHarness Factory) {
	prov := NewProvider(1, false) // one transient failure
	h := newHarness(t, Scenario{
		Provider: prov,
		Backoff:  func(int) time.Duration { return 5 * time.Second },
	})

	k := h.Submit(t, RenderedSubmission("transient", "RETRY-OK"))
	obs := settle(t, h, k)
	if !obs.Delivered() {
		t.Fatalf("transient failure did not recover to delivered: %+v", obs)
	}
	sends := prov.Sends()
	if len(sends) != 2 {
		t.Fatalf("transient path produced %d sends, want 2", len(sends))
	}
	for _, s := range sends {
		if !strings.Contains(s.Body, "RETRY-OK") {
			t.Fatalf("a retry changed the secret: %q", s.Body)
		}
	}
}

// casePermanentTerminal: a persistently failing provider exhausts the finite attempt
// budget, transitions to a terminal FAILED state, and discards the minted challenge
// exactly once — without leaking the secret through status.
func casePermanentTerminal(t *testing.T, newHarness Factory) {
	prov := NewProvider(1_000_000, false) // never succeeds
	init := NewInitializer("PERMA-SECRET", true)
	h := newHarness(t, Scenario{
		Provider:    prov,
		Initializer: init,
		MaxAttempts: 2,
		Backoff:     func(int) time.Duration { return time.Second },
	})

	k := h.Submit(t, OpaqueSubmission("perma"))
	obs := settle(t, h, k)
	if !obs.Failed {
		t.Fatalf("exhausted work did not fail terminally: %+v", obs)
	}
	if init.Discards() != 1 {
		t.Fatalf("terminal failure discarded the challenge %d times, want 1", init.Discards())
	}
	if leaked := dump(obs); strings.Contains(leaked, "PERMA-SECRET") {
		t.Fatalf("terminal status leaked the secret: %s", leaked)
	}
}

// caseProviderTimeout: a provider that blocks is bounded by the runtime's provider
// deadline — a single run RETURNS promptly (no hang, no leaked goroutine) and leaves
// the work rescheduled rather than stuck.
func caseProviderTimeout(t *testing.T, newHarness Factory) {
	prov := NewProvider(0, true) // blocks until context cancellation
	h := newHarness(t, Scenario{
		Provider:        prov,
		ProviderTimeout: 20 * time.Millisecond,
		MaxAttempts:     10,
		Backoff:         func(int) time.Duration { return time.Minute },
	})

	k := h.Submit(t, RenderedSubmission("timeout", "S"))

	done := make(chan struct{})
	go func() {
		h.Drain(t)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Drain did not return within the provider-timeout window (unbounded send)")
	}

	obs, ok := h.Status(t, k)
	if !ok || !obs.Pending {
		t.Fatalf("timed-out work must be rescheduled pending, got obs=%+v ok=%v", obs, ok)
	}
}

// casePurgeRetention: terminal work older than the retention window is purged, while
// recent and still-pending work survives.
func casePurgeRetention(t *testing.T, newHarness Factory) {
	prov := NewProvider(0, false)
	h := newHarness(t, Scenario{Provider: prov, PurgeRetention: time.Hour})

	old := h.Submit(t, RenderedSubmission("old", "S"))
	if obs := settle(t, h, old); !obs.Delivered() {
		t.Fatalf("retention fixture did not deliver: %+v", obs)
	}
	live := h.Submit(t, RenderedSubmission("live", "S"))

	if n := h.Purge(t); n != 0 {
		t.Fatalf("purge removed %d rows before retention elapsed, want 0", n)
	}
	h.Advance(2 * time.Hour)
	if n := h.Purge(t); n != 1 {
		t.Fatalf("purge removed %d rows past retention, want exactly 1 (the terminal one)", n)
	}
	if obs, ok := h.Status(t, live); !ok || !obs.Pending {
		t.Fatalf("purge removed still-pending work: obs=%+v ok=%v", obs, ok)
	}
}

// caseStatusNoLeak: status is lifecycle-only. At no observed point — pending or
// delivered — does the secret-free projection contain the submitted secret.
func caseStatusNoLeak(t *testing.T, newHarness Factory) {
	const secret = "S3CR3T-LEAK-CANARY"
	prov := NewProvider(0, false)
	h := newHarness(t, Scenario{Provider: prov})

	k := h.Submit(t, RenderedSubmission("status", secret))
	obs, ok := h.Status(t, k)
	if !ok || !obs.Pending {
		t.Fatalf("submitted work not pending: obs=%+v ok=%v", obs, ok)
	}
	if strings.Contains(dump(obs), secret) {
		t.Fatalf("pending status leaked the secret: %s", dump(obs))
	}
	final := settle(t, h, k)
	if !final.Delivered() {
		t.Fatalf("work never delivered: %+v", final)
	}
	if strings.Contains(dump(final), secret) {
		t.Fatalf("terminal status leaked the secret: %s", dump(final))
	}
	// An unknown key reveals no work at all.
	if obs, ok := h.Status(t, "no-such-key"); ok {
		t.Fatalf("unknown-key status returned work: %+v", obs)
	}
}

// caseKnownUnknownParity: the REQUEST PATH is identical for a known and an unknown
// identifier — both admit to the same pending shape and both terminate without a
// failure surfaced to the requester — so provider work (present for known, absent for
// unknown) is never an enumeration signal on the path the requester observes.
func caseKnownUnknownParity(t *testing.T, newHarness Factory) {
	// Known: resolves and delivers.
	knownProv := NewProvider(0, false)
	knownInit := NewInitializer("K", true)
	known := newHarness(t, Scenario{Provider: knownProv, Initializer: knownInit})
	kk := known.Submit(t, OpaqueSubmission("known"))
	knownAdmit, okK := known.Status(t, kk)

	// Unknown: resolves to nothing.
	unknownProv := NewProvider(0, false)
	unknownInit := NewInitializer("U", false)
	unknown := newHarness(t, Scenario{Provider: unknownProv, Initializer: unknownInit})
	uk := unknown.Submit(t, OpaqueSubmission("unknown"))
	unknownAdmit, okU := unknown.Status(t, uk)

	// Request-path parity: identical admission shape, no send on either path yet.
	if !okK || !okU || knownAdmit != unknownAdmit {
		t.Fatalf("admission shapes diverge: known=%+v(%v) unknown=%+v(%v)", knownAdmit, okK, unknownAdmit, okU)
	}
	if knownProv.Count() != 0 || unknownProv.Count() != 0 {
		t.Fatalf("a provider was called on the request path: known=%d unknown=%d", knownProv.Count(), unknownProv.Count())
	}

	// Both terminate without a requester-visible failure. The known path additionally
	// performs a real send and the unknown path performs none, but neither the send
	// nor its absence changes the non-failed terminal the requester polls — that is the
	// enumeration guarantee. (Under a genuine provider OUTAGE a known delivery can end
	// FAILED while an unknown ends succeeded; that divergence is transport failure, not
	// a steady-state request-path signal, and is the documented at-least-once/skip
	// behavior — not asserted here.)
	knownFinal := settle(t, known, kk)
	unknownFinal := settle(t, unknown, uk)
	if knownFinal.Failed || unknownFinal.Failed {
		t.Fatalf("a steady-state path surfaced failure: known=%+v unknown=%+v", knownFinal, unknownFinal)
	}
	if knownProv.Count() != 1 {
		t.Fatalf("known identifier did not deliver exactly once: %d", knownProv.Count())
	}
	if unknownProv.Count() != 0 {
		t.Fatalf("unknown identifier produced a send: %d", unknownProv.Count())
	}
}

// dump renders an observation for a leak assertion: every field a requester could
// observe, so a secret hiding in any of them is caught.
func dump(o Observation) string {
	return fmt.Sprintf("%+v", o)
}
