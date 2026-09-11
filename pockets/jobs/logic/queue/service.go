package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

// Service owns ordinary and fenced queue admission and the ordinary worker wake signal.
// Build runtimes from this Service to share that signal by construction.
type Service struct {
	repo        QueueRepository
	fencedQueue FencedQueueRepository
	wake        chan struct{}
	maxAttempts int
	now         func() time.Time
}

// NewService validates the queue ports and resolves admission defaults. It starts no workers.
func NewService(repos Repositories, opts ...Option) (*Service, error) {
	cfg := serviceConfig{}
	for _, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("jobs queue: nil option: %w", sdk.ErrInvalidInput)
		}
		opt(&cfg)
	}
	if repos.Queue == nil && repos.FencedQueue == nil {
		return nil, ErrQueueRequired
	}
	if cfg.maxAttempts <= 0 {
		cfg.maxAttempts = defaultMaxAttempts
	}
	if cfg.clock == nil {
		cfg.clock = func() time.Time { return time.Now().UTC() }
	}
	return &Service{repo: repos.Queue, fencedQueue: repos.FencedQueue, wake: make(chan struct{}, 1), maxAttempts: cfg.maxAttempts, now: cfg.clock}, nil
}

// Enqueue is the primitive-typed entry point (stdlib types only), so a consuming
// pocket's own narrow enqueuer port matches it structurally with zero import of
// pockets/jobs.
func (s *Service) Enqueue(ctx context.Context, kind string, payload json.RawMessage) (string, error) {
	j, err := s.EnqueueJob(ctx, Enqueue{Kind: kind, Payload: payload})
	if err != nil {
		return "", err
	}
	return j.ID(), nil
}

// EnqueueJob is the full-fidelity enqueue: it validates the kind, applies the
// MaxAttempts and ScheduledFor defaults, inserts the job, and — only on a
// successful insert — signals the wake channel. A duplicate ID surfaces
// sdk.ErrAlreadyExists from the store and does not signal (nothing new ran).
func (s *Service) EnqueueJob(ctx context.Context, in Enqueue) (Job, error) {
	if s.repo == nil {
		return Job{}, ErrQueueRequired
	}
	if in.Kind == "" {
		return Job{}, fmt.Errorf("jobs: kind is required: %w", sdk.ErrInvalidInput)
	}
	if len(in.Payload) == 0 {
		in.Payload = json.RawMessage(`{}`)
	} else if !json.Valid(in.Payload) {
		return Job{}, fmt.Errorf("jobs: payload must be valid JSON: %w", sdk.ErrInvalidInput)
	}
	if in.MaxAttempts <= 0 {
		in.MaxAttempts = s.maxAttempts
	}
	if in.ScheduledFor.IsZero() {
		in.ScheduledFor = s.now()
	}
	j, err := s.repo.Enqueue(ctx, in)
	if err != nil {
		return Job{}, err
	}
	s.signal()
	return j, nil
}

// signal is the non-blocking send the wake-channel protocol requires: coalesced
// signals collapse into at most one buffered wake, and a full buffer drops the
// send (the poll/idle interval is the backstop).
func (s *Service) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
