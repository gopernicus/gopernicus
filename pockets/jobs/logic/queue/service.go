package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

// Service owns ordinary and fenced queue admission and the worker wake signals.
// Build runtimes from this Service to share those signals by construction: the
// ordinary runtime shares one channel; each fenced runtime registers its own,
// and a fenced admission signals all of them.
type Service struct {
	repo           QueueRepository
	fencedQueue    FencedQueueRepository
	terminalPurger TerminalPurger
	wake           chan struct{}

	// fencedWakes holds one cap-1 channel per fenced runtime built from this
	// Service, so a runtime that does not claim the admitted kind cannot
	// swallow the signal its owner needs.
	fencedMu    sync.Mutex
	fencedWakes []chan struct{}

	maxAttempts int
	now         func() time.Time
	ids         sdk.IDGenerator
}

// NewService validates the queue ports and resolves admission defaults. It starts no workers.
// It resolves the optional TerminalPurger on Repositories.Queue once, logs it as
// terminal_purge=wired|absent through WithLogger's logger (default
// slog.Default), and WARNs when an ordinary
// queue is wired without it.
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
	if cfg.logger == nil {
		cfg.logger = slog.Default()
	}
	purger, _ := repos.Queue.(TerminalPurger)
	logTerminalPurge(cfg.logger, repos, purger != nil)
	return &Service{repo: repos.Queue, fencedQueue: repos.FencedQueue, terminalPurger: purger, wake: make(chan struct{}, 1), maxAttempts: cfg.maxAttempts, now: cfg.clock, ids: cfg.ids.Or(sdk.UUIDv7())}, nil
}

// logTerminalPurge is the construction-time record of the ordinary-queue
// retention capability. The WARN is mode-blind because the queue Service has no
// runtime mode; every bundled store implements TerminalPurger.
func logTerminalPurge(log *slog.Logger, repos Repositories, wired bool) {
	state := "absent"
	if wired {
		state = "wired"
	}
	log.Info("jobs queue service constructed", "ordinary_queue", repos.Queue != nil, "fenced_queue", repos.FencedQueue != nil, "terminal_purge", state)
	if repos.Queue != nil && !wired {
		log.Warn("jobs queue: Repositories.Queue does not implement queue.TerminalPurger — ordinary-queue retention cannot run and PurgeTerminalJobs returns ErrTerminalPurgeRequired", "terminal_purge", state)
	}
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
// ID, MaxAttempts and ScheduledFor defaults, inserts the job, and — only on a
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
	if in.ID == "" {
		id, err := s.mintID()
		if err != nil {
			return Job{}, err
		}
		in.ID = id
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

// registerFencedWake adds a fenced runtime's wake channel.
func (s *Service) registerFencedWake() chan struct{} {
	ch := make(chan struct{}, 1)
	s.fencedMu.Lock()
	s.fencedWakes = append(s.fencedWakes, ch)
	s.fencedMu.Unlock()
	return ch
}

// signalFenced wakes every fenced runtime with the same coalescing,
// non-blocking send as signal; IdleInterval is the backstop for a drop.
func (s *Service) signalFenced() {
	s.fencedMu.Lock()
	defer s.fencedMu.Unlock()
	for _, ch := range s.fencedWakes {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// mintID returns a fresh job id from the service's generator.
func (s *Service) mintID() (string, error) {
	id, err := s.ids.Generate()
	if err != nil {
		return "", fmt.Errorf("jobs: mint job id: %w", err)
	}
	return id, nil
}
