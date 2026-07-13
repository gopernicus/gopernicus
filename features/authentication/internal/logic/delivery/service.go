package delivery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/deliveryjob"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
)

// Stable queue-service errors. Each wraps an sdk kind so callers match with
// errors.Is, never string parsing.
var (
	// ErrRepositoryRequired is returned by NewService when no deliveryjob.Repository
	// is supplied: the durable outbox cannot exist without its store.
	ErrRepositoryRequired = fmt.Errorf("delivery: job repository is required: %w", sdk.ErrInvalidInput)
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
// account-resolution input (ResolutionInput) with an empty Body — the worker
// resolves the account and renders the message off the request path (design
// §6.1.1); a pre-rendered command carries the full Subject/Body/Secret.
type Command struct {
	Kind           string
	Purpose        string
	IdempotencyKey string
	Envelope       Envelope
}

// Receipt is the durable handle Enqueue/Replace returns. Key is the stable
// idempotency key (the value a session-gated status lookup keys on — it survives
// the worker's initialize-time Replace, unlike the job ID); JobID and State are
// the enqueued row's current identity and lifecycle state.
type Receipt struct {
	Key   string
	JobID string
	State string
}

// Service is the durable delivery queue: it seals a Command's envelope and
// enqueues (or replaces) an at-least-once outbox job through the
// deliveryjob.Repository (design §6.1.1). It performs no account resolution and no
// provider send — those happen later in the Worker, off the request path, so a
// known and an unknown identifier share one bounded enqueue path and provider
// latency can never become an enumeration signal.
type Service struct {
	repo deliveryjob.Repository
	enc  cryptids.Encrypter
	now  func() time.Time
}

// ServiceDeps are the collaborators NewService needs. Repo and Encrypter are
// required; Now defaults to time.Now (injected in tests for a deterministic
// clock).
type ServiceDeps struct {
	Repo      deliveryjob.Repository
	Encrypter cryptids.Encrypter
	Now       func() time.Time
}

// NewService builds a Service. A nil Repo is ErrRepositoryRequired and a nil
// Encrypter is ErrEncrypterRequired — both structural, so the queue degrades
// loudly at construction.
func NewService(d ServiceDeps) (*Service, error) {
	if d.Repo == nil {
		return nil, ErrRepositoryRequired
	}
	if d.Encrypter == nil {
		return nil, ErrEncrypterRequired
	}
	now := d.Now
	if now == nil {
		now = time.Now
	}
	return &Service{repo: d.Repo, enc: d.Encrypter, now: now}, nil
}

// Enqueue seals cmd's envelope and enqueues it as a durable job, idempotent by
// IdempotencyKey: a non-terminal job already holding the key is returned unchanged
// (a double-submitted start makes no second job). It never resolves an account or
// calls a provider.
func (s *Service) Enqueue(ctx context.Context, cmd Command) (Receipt, error) {
	job, err := s.build(cmd)
	if err != nil {
		return Receipt{}, err
	}
	stored, err := s.repo.Enqueue(ctx, job)
	if err != nil {
		return Receipt{}, err
	}
	return receipt(stored), nil
}

// Replace seals cmd's envelope and supersedes any prior pending job holding the
// key — the user-requested resend: the earlier pending job is canceled, not left
// to also deliver (design §6.1.1). Cooldown and per-identifier send budgets are
// enforced by the caller before Replace, not here.
func (s *Service) Replace(ctx context.Context, cmd Command) (Receipt, error) {
	job, err := s.build(cmd)
	if err != nil {
		return Receipt{}, err
	}
	stored, err := s.repo.Replace(ctx, job)
	if err != nil {
		return Receipt{}, err
	}
	return receipt(stored), nil
}

// Status is the read-only delivery-status projection a session-gated caller polls
// with its Receipt key (design §6.1.1). It exposes only lifecycle — State, the
// attempt count, and a Failed convenience flag — never the destination or secret,
// which stay sealed in the job payload. Pending reports whether delivery is still
// in flight (StatePending), so a caller learns "still trying" vs "delivered" vs
// "failed" without holding the start request open.
type Status struct {
	State   string
	Attempt int
	Pending bool
	Failed  bool
}

// Status returns the current delivery status for a Receipt's idempotency key. An
// unknown key is sdk.ErrNotFound (the caller's receipt names no live job). It never
// leases, mutates, or resolves an account — a status read cannot affect delivery.
func (s *Service) Status(ctx context.Context, idempotencyKey string) (Status, error) {
	if idempotencyKey == "" {
		return Status{}, fmt.Errorf("delivery: idempotency key is required: %w", sdk.ErrInvalidInput)
	}
	job, err := s.repo.GetLatestByIdempotencyKey(ctx, idempotencyKey)
	if err != nil {
		return Status{}, err
	}
	return Status{
		State:   job.State,
		Attempt: job.AttemptCount,
		Pending: job.State == deliveryjob.StatePending,
		Failed:  job.State == deliveryjob.StateFailed,
	}, nil
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

// build validates cmd and seals its envelope into a fresh, due StatePending job.
// A seal failure (encryption error) surfaces here so an enqueue never persists a
// job it cannot later open.
func (s *Service) build(cmd Command) (deliveryjob.Job, error) {
	if cmd.Kind == "" || cmd.Purpose == "" || cmd.IdempotencyKey == "" {
		return deliveryjob.Job{}, ErrCommandIncomplete
	}
	payload, err := Seal(s.enc, cmd.Envelope)
	if err != nil {
		return deliveryjob.Job{}, err
	}
	now := s.now().UTC()
	return deliveryjob.Job{
		Kind:           cmd.Kind,
		Purpose:        cmd.Purpose,
		IdempotencyKey: cmd.IdempotencyKey,
		Payload:        payload,
		AvailableAt:    now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}, nil
}

// receipt projects a stored job into its durable handle.
func receipt(job deliveryjob.Job) Receipt {
	return Receipt{Key: job.IdempotencyKey, JobID: job.ID, State: job.State}
}
