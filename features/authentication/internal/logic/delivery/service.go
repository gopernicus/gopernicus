package delivery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
)

// Stable queue-service errors. Each wraps an sdk kind so callers match with
// errors.Is, never string parsing.
var (
	// ErrDispatcherRequired is returned by NewService when no Dispatcher is supplied:
	// the delivery queue cannot submit work without a transport. Every mode that can
	// deliver wires one — jobs mode the host-composed generic-jobs dispatcher,
	// in_process mode the feature-internal bounded queue.
	ErrDispatcherRequired = fmt.Errorf("delivery: dispatcher is required: %w", sdk.ErrInvalidInput)
	// ErrEncrypterRequired is returned by NewService when no DeliveryEncrypter is
	// supplied: the payload envelope is always sealed at rest (design §6.1.1), so the
	// queue cannot exist without it.
	ErrEncrypterRequired = fmt.Errorf("delivery: encrypter is required: %w", sdk.ErrInvalidInput)
	// ErrCommandIncomplete is returned by Enqueue/Replace when a Command omits its
	// Kind, Purpose, or IdempotencyKey — all three are structural, never optional.
	ErrCommandIncomplete = fmt.Errorf("delivery: command kind, purpose, and idempotency key are required: %w", sdk.ErrInvalidInput)
)

// Command is one durable-delivery instruction handed to Enqueue/Replace. Kind and
// Purpose route the eventual send; IdempotencyKey is the PII-free digest a caller
// derives from the normalized identifier (through the host IdentifierKeyer) so a
// double-submitted start makes no second job and a user-requested resend replaces
// exactly the prior pending job. Envelope is the plaintext delivery payload; it is
// sealed here and persisted ONLY as ciphertext (there is no plaintext
// destination/message column). An OPAQUE start command carries only the
// account-resolution input (ResolutionInput) with an empty Body — the delivery
// processor resolves the account and renders the message off the request path
// (design §6.1.1); a pre-rendered command carries the full Subject/Body/Secret.
type Command struct {
	Kind           string
	Purpose        string
	IdempotencyKey string
	Envelope       Envelope
}

// Receipt is the durable handle Enqueue/Replace returns. Key is the stable
// idempotency key (the value a session-gated status lookup keys on — it survives
// the processor's initialize-time supersession, unlike the execution ID); JobID and
// State are the submitted work's current identity and lifecycle state.
type Receipt struct {
	Key   string
	JobID string
	State string
}

// Service is the delivery queue: it seals a Command's versioned envelope and submits
// (or replaces) delivery work through the transport-neutral Dispatcher (design
// §6.1.1; AV3D-2.3). It performs no account resolution and no provider send — those
// happen later off the request path, so a known and an unknown identifier share one
// bounded submit path and provider latency can never become an enumeration signal.
// The Dispatcher is the ONLY queue seam it holds: jobs mode wires the durable
// generic-jobs dispatcher, in_process mode the bounded in-process queue.
type Service struct {
	dispatcher Dispatcher
	enc        cryptids.Encrypter
	now        func() time.Time
}

// ServiceDeps are the collaborators NewService needs. Both Dispatcher and Encrypter
// are required (the payload is sealed here and submitted through the transport). Now
// defaults to time.Now (injected in tests for a deterministic clock).
type ServiceDeps struct {
	// Dispatcher is the transport the service submits through.
	Dispatcher Dispatcher
	Encrypter  cryptids.Encrypter
	Now        func() time.Time
}

// NewService builds a Service. A nil Encrypter is ErrEncrypterRequired; a nil
// Dispatcher is ErrDispatcherRequired — both structural, so the queue degrades
// loudly at construction.
func NewService(d ServiceDeps) (*Service, error) {
	if d.Encrypter == nil {
		return nil, ErrEncrypterRequired
	}
	if d.Dispatcher == nil {
		return nil, ErrDispatcherRequired
	}
	now := d.Now
	if now == nil {
		now = time.Now
	}
	return &Service{dispatcher: d.Dispatcher, enc: d.Encrypter, now: now}, nil
}

// Enqueue seals cmd's envelope and submits it as a durable job, idempotent by
// IdempotencyKey: a non-terminal job already holding the key is returned unchanged
// (a double-submitted start makes no second job). It never resolves an account or
// calls a provider.
func (s *Service) Enqueue(ctx context.Context, cmd Command) (Receipt, error) {
	payload, err := s.seal(cmd)
	if err != nil {
		return Receipt{}, err
	}
	id, err := s.dispatcher.Submit(ctx, cmd.Kind, cmd.Purpose, cmd.IdempotencyKey, payload)
	if err != nil {
		return Receipt{}, err
	}
	return Receipt{Key: cmd.IdempotencyKey, JobID: id, State: StatusPending}, nil
}

// Replace seals cmd's envelope and supersedes any prior pending job holding the
// key — the user-requested resend: the earlier pending job is canceled, not left
// to also deliver (design §6.1.1). Cooldown and per-identifier send budgets are
// enforced by the caller before Replace, not here.
func (s *Service) Replace(ctx context.Context, cmd Command) (Receipt, error) {
	payload, err := s.seal(cmd)
	if err != nil {
		return Receipt{}, err
	}
	id, err := s.dispatcher.Replace(ctx, cmd.Kind, cmd.Purpose, cmd.IdempotencyKey, payload)
	if err != nil {
		return Receipt{}, err
	}
	return Receipt{Key: cmd.IdempotencyKey, JobID: id, State: StatusPending}, nil
}

// Status is the read-only delivery-status projection a session-gated caller polls
// with its Receipt key (design §6.1.1). It exposes only lifecycle — State, a Pending
// convenience flag, and a Failed convenience flag — never the destination, secret,
// worker name, failure text, or raw logical key. State is one of the stable auth
// status words (StatusPending/StatusSucceeded/StatusFailed/StatusCanceled); Pending
// reports whether delivery is still in flight, so a caller learns "still trying" vs
// "delivered" vs "failed" without holding the start request open.
//
// Attempt is retained for projection stability but is NOT carried by the
// transport-neutral lifecycle seam (see normalizeStatus); it reads 0.
type Status struct {
	State   string
	Attempt int
	Pending bool
	Failed  bool
}

// Status returns the current delivery status for a Receipt's idempotency key,
// normalizing the transport's generic lifecycle state into the stable auth
// projection. An unknown key is sdk.ErrNotFound (the caller's receipt names no live
// job). It never leases, mutates, or resolves an account — a status read cannot
// affect delivery.
func (s *Service) Status(ctx context.Context, idempotencyKey string) (Status, error) {
	if idempotencyKey == "" {
		return Status{}, fmt.Errorf("delivery: idempotency key is required: %w", sdk.ErrInvalidInput)
	}
	state, err := s.dispatcher.LatestStatus(ctx, idempotencyKey)
	if err != nil {
		return Status{}, err
	}
	return normalizeStatus(state), nil
}

// ErrStatusUnknown is a convenience for callers matching an unknown-key status read
// (the receipt names no job). It wraps sdk.ErrNotFound.
var ErrStatusUnknown = fmt.Errorf("delivery: no job for receipt: %w", sdk.ErrNotFound)

// StatusOrUnknown is Status with a not-found normalized to ErrStatusUnknown so a
// handler can branch on one sentinel.
func (s *Service) StatusOrUnknown(ctx context.Context, idempotencyKey string) (Status, error) {
	st, err := s.Status(ctx, idempotencyKey)
	if errors.Is(err, sdk.ErrNotFound) {
		return Status{}, ErrStatusUnknown
	}
	return st, err
}

// seal validates cmd's structural fields and seals its envelope into the versioned,
// opaque command payload the Dispatcher stores and the delivery processor opens. A
// seal failure (encryption error) surfaces here so a submit never persists work it
// cannot later open, and the error propagates unchanged (it is a local seal, not a
// payload-carrying codec error).
func (s *Service) seal(cmd Command) ([]byte, error) {
	if cmd.Kind == "" || cmd.Purpose == "" || cmd.IdempotencyKey == "" {
		return nil, ErrCommandIncomplete
	}
	return sealCommand(s.enc, cmd)
}
