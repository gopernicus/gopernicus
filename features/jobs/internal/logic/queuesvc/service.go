// Package queuesvc holds the jobs feature's enqueue use cases: input validation,
// default application, and the load-bearing wake signal. It is internal so it is
// not part of the feature's public SemVer surface; the host-facing surface is
// package jobs (jobs.go).
//
// The Service owns the buffered cap-1 wake channel: every successful enqueue
// performs a non-blocking send on it, and jobs.NewRuntime hands that same
// channel to the queue pool (via workers.WithWakeChannel), so a fresh job runs
// promptly instead of waiting out a poll interval. That coupling is the design's
// named correctness/latency property, wired by construction here.
package queuesvc

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/features/jobs/domain/job"
	"github.com/gopernicus/gopernicus/sdk/errs"
)

// Service implements the enqueue use cases over the queue repository port.
type Service struct {
	repo        job.QueueRepository
	wake        chan struct{}
	maxAttempts int
	now         func() time.Time
}

// NewService builds an enqueue Service over repo. maxAttempts is the default
// applied when an Enqueue sets none; a nil clock defaults to time.Now UTC. It
// allocates the buffered cap-1 wake channel the Service owns.
func NewService(repo job.QueueRepository, maxAttempts int, clock func() time.Time) *Service {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Service{
		repo:        repo,
		wake:        make(chan struct{}, 1),
		maxAttempts: maxAttempts,
		now:         clock,
	}
}

// Wake returns the receive-only wake channel jobs.NewRuntime hands to the queue
// pool. It is the same channel Enqueue/EnqueueJob signal on.
func (s *Service) Wake() <-chan struct{} { return s.wake }

// Enqueue is the primitive-typed entry point (stdlib types only), so a consuming
// feature's own narrow enqueuer port matches it structurally with zero import of
// features/jobs.
func (s *Service) Enqueue(ctx context.Context, kind string, payload json.RawMessage) (string, error) {
	j, err := s.EnqueueJob(ctx, job.Enqueue{Kind: kind, Payload: payload})
	if err != nil {
		return "", err
	}
	return j.ID(), nil
}

// EnqueueJob is the full-fidelity enqueue: it validates the kind, applies the
// MaxAttempts and ScheduledFor defaults, inserts the job, and — only on a
// successful insert — signals the wake channel. A duplicate ID surfaces
// errs.ErrAlreadyExists from the store and does not signal (nothing new ran).
func (s *Service) EnqueueJob(ctx context.Context, in job.Enqueue) (job.Job, error) {
	if in.Kind == "" {
		return job.Job{}, fmt.Errorf("jobs: kind is required: %w", errs.ErrInvalidInput)
	}
	if in.MaxAttempts <= 0 {
		in.MaxAttempts = s.maxAttempts
	}
	if in.ScheduledFor.IsZero() {
		in.ScheduledFor = s.now()
	}
	j, err := s.repo.Enqueue(ctx, in)
	if err != nil {
		return job.Job{}, err
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
