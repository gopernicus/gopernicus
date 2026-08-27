// Package deliverychar is the TRANSPORT-NEUTRAL characterization suite for the
// auth feature's outbound delivery runtime (design §6.1.1; plan
// authv3-delivery-refactor AV3D-0.2). It freezes the OBSERVABLE guarantees of the
// current bespoke worker/queue as black-box cases so a later runtime — the durable
// generic-jobs mode (phase 3) or the bounded in-process mode (phase 4) — can be
// held to the same behavior by implementing one narrow Harness and passing Run.
//
// The suite never names a worker, a job store, a lease, or a queue: those are the
// transport, and the point of this package is that the transport may change while
// the guarantees do not. It speaks only the vocabulary a requester and an operator
// can observe — submit work, run pending work, advance the clock, observe provider
// sends and secret-free status. A Harness adapter maps that vocabulary onto one
// concrete runtime; the current worker is the first adapter (a delivery-package
// test), a generic-jobs runtime and a bounded pool are the next two.
//
// It imports stdlib + sdk only (never the delivery package it characterizes), so a
// Harness adapter can live in the delivery package's own test binary without an
// import cycle, exactly as the storetest reference does for repositories.
package deliverychar

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk/capabilities/notify"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// Neutral defaults for a submission. Purpose is deliberately a bare, non-routing
// token: the characterized runtimes never RENDER it (opaque work is rendered by the
// Initializer, pre-rendered work carries its own Body), so any non-empty purpose is
// enough to exercise admission, retry, and status.
const (
	// Purpose is the neutral, non-empty delivery purpose every case submits under.
	Purpose = "characterization"
	// Kind is the neutral address kind every case delivers over — the single-notifier
	// SMS-style rail, the simplest provider capture surface (the worker suite's own
	// choice), so the neutral cases never branch on email-vs-other transport details.
	Kind = identity.KindPhone
	// Destination is the neutral resolved address a rendered/initialized message
	// targets.
	Destination = "+15550001111"
)

// Rendered is a pre-rendered delivery payload: the resolved Destination, the message
// Body actually handed to the provider, and the Secret carried inside it. It is what
// a non-opaque submission delivers directly, and what an opaque submission's
// Initializer produces once (then reuses byte-for-byte on every retry).
type Rendered struct {
	Destination string
	Body        string
	Secret      string
}

// Submission is one neutral delivery instruction. Key is the PII-free logical key
// that makes a duplicate submit idempotent and a Replace supersede exactly the prior
// active work. An Opaque submission carries only ResolutionInput (the runtime
// resolves + renders it off the request path, through the Scenario Initializer); a
// non-opaque submission carries its Rendered payload directly.
type Submission struct {
	Key             string
	Kind            string
	Purpose         string
	Opaque          bool
	ResolutionInput string
	Rendered        Rendered
}

// Normalized fills neutral defaults so cases stay terse. A Harness adapter applies
// it before mapping a Submission onto its runtime's command shape.
func (s Submission) Normalized() Submission {
	if s.Kind == "" {
		s.Kind = Kind
	}
	if s.Purpose == "" {
		s.Purpose = Purpose
	}
	if s.Opaque && s.ResolutionInput == "" {
		s.ResolutionInput = Destination
	}
	return s
}

// RenderedSubmission is a non-opaque submission keyed by key whose provider Body
// carries secret verbatim — the simplest input for the delivery/retry/crash cases.
func RenderedSubmission(key, secret string) Submission {
	return Submission{
		Key:      key,
		Opaque:   false,
		Rendered: Rendered{Destination: Destination, Body: "deliver:" + secret, Secret: secret},
	}
}

// OpaqueSubmission is an opaque, enumeration-safe start keyed by key: it carries no
// rendered content, so the runtime must resolve + render it off the request path.
func OpaqueSubmission(key string) Submission {
	return Submission{Key: key, Opaque: true, ResolutionInput: Destination}
}

// Observation is the secret-free lifecycle projection a session-gated requester
// polls for a key — the whole of what delivery status is allowed to reveal. It
// carries NO destination and NO secret by construction; a case asserts that a full
// dump of it never contains a submitted secret, so the no-leak guarantee is checked
// at the exact boundary the requester sees.
type Observation struct {
	// State is the runtime's lifecycle word (pending/succeeded/failed/…). Cases treat
	// it opaquely except for the no-leak dump; portable logic uses the booleans.
	State string
	// Attempt is the number of delivery attempts spent.
	Attempt int
	// Pending reports delivery still in flight (not yet terminal).
	Pending bool
	// Failed reports a terminal delivery failure (retry budget exhausted / permanent).
	Failed bool
}

// Terminal reports the observation reached a terminal lifecycle (delivered, skipped,
// failed, or superseded) — nothing more will happen for this key's current work.
func (o Observation) Terminal() bool { return !o.Pending }

// Delivered reports a terminal, non-failed outcome — the requester's success signal.
// A delivered send and a skipped unknown identifier share this state, deliberately
// (they are indistinguishable to the requester).
func (o Observation) Delivered() bool { return !o.Pending && !o.Failed }

// Send is one observed provider call: the resolved recipient and the message. It is
// the ONE place a secret legitimately appears (it is the delivered message); every
// other observable is secret-free. Cases compare Send.Body across retries to prove a
// runtime reuses the identical rendered secret rather than minting a new one.
type Send struct {
	To      string
	Subject string
	Body    string
}

// Provider is the neutral recording transport a Harness wires as its only sender. It
// is a notify.Notifier so every characterized runtime (all deliver through notify)
// can wire it unchanged. It records every send in order and can be primed to fail the
// first N sends (transient/permanent paths) or to block until the context is
// canceled (the provider-timeout bound). It is concurrency-safe for contention cases.
type Provider struct {
	mu        sync.Mutex
	kind      string
	sends     []Send
	failFirst int
	block     bool
}

// NewProvider builds a recording provider for Kind. failFirst sends return an error
// (transient failure); block makes every send wait for context cancellation.
func NewProvider(failFirst int, block bool) *Provider {
	return &Provider{kind: Kind, failFirst: failFirst, block: block}
}

// Kind reports the address kind this provider handles (the neutral SMS-style rail).
func (p *Provider) Kind() string { return p.kind }

// Notify records the send and applies the primed failure/block behavior.
func (p *Provider) Notify(ctx context.Context, to identity.Address, msg notify.Message) error {
	if p.block {
		<-ctx.Done()
		return ctx.Err()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sends = append(p.sends, Send{To: to.Value, Subject: msg.Subject, Body: msg.Body})
	if len(p.sends) <= p.failFirst {
		return errProvider
	}
	return nil
}

// Sends returns a snapshot of every observed provider call, in order.
func (p *Provider) Sends() []Send {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Send, len(p.sends))
	copy(out, p.sends)
	return out
}

// Count reports how many provider sends have been observed.
func (p *Provider) Count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.sends)
}

// Initializer is the neutral opaque-resolution collaborator: it stands for the auth
// service's off-request-path resolve + challenge-issue + render step. A Harness
// invokes it exactly once per opaque submission that becomes deliverable, and never
// on retry (the runtime checkpoints its Rendered result). Deliver=false is the
// unknown/ineligible identifier that terminates with no send. It counts its
// Initialize and Discard calls so cases can assert "rendered once" and "challenge
// discarded on terminal failure" without seeing any secret through this seam.
type Initializer struct {
	mu           sync.Mutex
	rendered     Rendered
	deliver      bool
	err          error
	initCalls    int
	discardCalls int
}

// NewInitializer builds an opaque resolver that renders secret and reports deliver.
// deliver=false models an unknown/ineligible identifier (nothing to deliver).
func NewInitializer(secret string, deliver bool) *Initializer {
	return &Initializer{
		rendered: Rendered{Destination: Destination, Body: "deliver:" + secret, Secret: secret},
		deliver:  deliver,
	}
}

// Resolve is the neutral Initialize: it records the call and returns the pre-rendered
// payload (or "nothing to deliver"). A Harness adapter maps this onto its runtime's
// initializer port. It is intentionally deterministic — the runtime, not this stub,
// owns the checkpoint that makes the result reused byte-for-byte on retry.
func (i *Initializer) Resolve() (r Rendered, deliver bool, err error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.initCalls++
	if i.err != nil {
		return Rendered{}, false, i.err
	}
	return i.rendered, i.deliver, nil
}

// Discarded is the neutral Discard: it records the best-effort challenge void a
// runtime performs once when opaque work fails terminally.
func (i *Initializer) Discarded() {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.discardCalls++
}

// Inits reports how many times the runtime resolved+rendered this work. It must be 1
// across any number of retries (checkpoint-before-send: render once, reuse).
func (i *Initializer) Inits() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.initCalls
}

// Discards reports how many times the runtime voided the minted challenge (1 after a
// terminal failure of deliverable opaque work).
func (i *Initializer) Discards() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.discardCalls
}

// Scenario is the neutral wiring one Harness is built from. Provider is required.
// Initializer is nil for pre-rendered-only cases (an opaque submission with no
// initializer must fail loudly). The remaining knobs mirror the tuning every
// characterized runtime must honor: a finite attempt budget, a context-cancellable
// backoff, a provider deadline inside the claim, a terminal retention window, a claim
// hold, and a crash-after-send count (fail the completion record N times, so a send
// is duplicated and the same secret is replayed — never a new one minted).
type Scenario struct {
	Provider         *Provider
	Initializer      *Initializer
	MaxAttempts      int
	Backoff          func(attempt int) time.Duration
	ProviderTimeout  time.Duration
	PurgeRetention   time.Duration
	LeaseFor         time.Duration
	CrashCompletions int
}

// Harness is the narrow seam every delivery runtime implements so the neutral cases
// can drive it: admit work, run all currently-runnable work to quiescence, advance
// the runtime clock, trigger terminal purge, and read secret-free status. A Harness
// is single-scenario — Factory builds a fresh one per case — and need not be
// concurrency-safe beyond what a case drives.
type Harness interface {
	// Submit admits sub as new work and returns its logical key. A duplicate key is
	// idempotent (no second active execution).
	Submit(t *testing.T, sub Submission) string
	// Replace admits sub as fresh work that supersedes all prior active work under its
	// key, returning the key.
	Replace(t *testing.T, sub Submission) string
	// Drain runs every currently-runnable unit of work until nothing is immediately
	// runnable (a unit rescheduled into the future is not runnable until Advance). It
	// must RETURN — a blocked provider is bounded by the scenario's ProviderTimeout.
	Drain(t *testing.T)
	// Advance moves the runtime clock forward by d (retry backoff, claim expiry, purge
	// retention).
	Advance(d time.Duration)
	// Purge runs one terminal-cleanup sweep and reports the number removed.
	Purge(t *testing.T) int
	// Status is the secret-free lifecycle projection for key. ok=false means the key
	// names no work (an unknown-key status read).
	Status(t *testing.T, key string) (obs Observation, ok bool)
}

// Factory builds a fresh Harness for one Scenario. A runtime under characterization
// provides one Factory and passes it to Run.
type Factory func(t *testing.T, s Scenario) Harness
