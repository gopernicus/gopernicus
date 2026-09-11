// Package jobs assembles optional queue and schedule services. Hosts may use the
// public logic packages directly or build both components with New.
package jobs

import (
	"fmt"

	"github.com/gopernicus/gopernicus/pockets/jobs/logic/queue"
	"github.com/gopernicus/gopernicus/pockets/jobs/logic/schedules"
	"github.com/gopernicus/gopernicus/sdk"
)

// Repositories supplies the independent persistence capabilities.
type Repositories struct {
	Queue       queue.QueueRepository
	FencedQueue queue.FencedQueueRepository
	Schedules   schedules.Repository
}

// Components holds the built services. Schedules is nil for a queue-only host.
// Runtime construction is explicit: queue.NewRuntime(Queue, handlers, queue.WithScheduler(Schedules)).
type Components struct {
	Queue     *queue.Service
	Schedules *schedules.Service
}

// New validates repositories and builds the queue and optional schedule service.
// It starts no workers and registers no HTTP routes.
func New(repos Repositories, opts ...Option) (*Components, error) {
	cfg := config{}
	for _, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("jobs: nil constructor option: %w", sdk.ErrInvalidInput)
		}
		opt(&cfg)
	}
	q, err := queue.NewService(queue.Repositories{Queue: repos.Queue, FencedQueue: repos.FencedQueue},
		queue.WithMaxAttempts(cfg.maxAttempts),
		queue.WithClock(cfg.clock),
	)
	if err != nil {
		return nil, err
	}
	c := &Components{Queue: q}
	if repos.Queue != nil && repos.Schedules != nil {
		c.Schedules, err = schedules.NewService(repos.Schedules,
			schedules.WithEnqueuer(q),
			schedules.WithCronParser(cfg.cron),
			schedules.WithBatchSize(cfg.scheduleBatch),
		)
		if err != nil {
			return nil, err
		}
	}
	return c, nil
}
