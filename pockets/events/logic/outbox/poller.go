package outbox

import (
	"context"
	"fmt"
	"reflect"

	"github.com/gopernicus/gopernicus/sdk"
	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
	"github.com/gopernicus/gopernicus/sdk/pkg/workers"
)

const defaultBatchSize = 100

// PollerOption configures a Poller at construction.
type PollerOption func(*pollerConfig)

type pollerConfig struct{ batchSize int }

// WithBatchSize sets the number of unpublished entries drained per Poll. A
// non-positive value restores the default (100).
func WithBatchSize(n int) PollerOption {
	return func(c *pollerConfig) { c.batchSize = n }
}

// DeliverFunc completes the host's chosen handoff: Memory.Dispatch waits for
// local handlers; a Redis bus's Publish waits for stream acceptance. Do not pass
// an asynchronous Emit or a discard callback when delivery must be durable.
type DeliverFunc func(context.Context, sdkevents.Event) error

// Poller drains an outbox through an explicit delivery function. It owns no
// goroutines; the host drives Poll with a worker pool. Use one poller per outbox:
// ListUnpublished does not claim rows for competing pollers.
type Poller struct {
	repo      EntryRepository
	deliver   DeliverFunc
	batchSize int
}

// NewPoller reads unpublished entries from repo and hands each to deliver before
// marking it published. It validates dependencies without calling them. Register
// required handlers before starting the poller.
func NewPoller(repo EntryRepository, deliver DeliverFunc, opts ...PollerOption) (*Poller, error) {
	cfg := pollerConfig{batchSize: defaultBatchSize}
	for _, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("events outbox: nil poller option: %w", sdk.ErrInvalidInput)
		}
		opt(&cfg)
	}
	if cfg.batchSize <= 0 {
		cfg.batchSize = defaultBatchSize
	}
	if repo == nil {
		return nil, fmt.Errorf("events: poller needs a repository: %w", sdk.ErrInvalidInput)
	}
	value := reflect.ValueOf(repo)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if value.IsNil() {
			return nil, fmt.Errorf("events: poller needs a repository: %w", sdk.ErrInvalidInput)
		}
	}
	if deliver == nil {
		return nil, fmt.Errorf("events: poller needs a delivery function: %w", sdk.ErrInvalidInput)
	}
	return &Poller{repo: repo, deliver: deliver, batchSize: cfg.batchSize}, nil
}

// Poll satisfies workers.WorkFunc. It returns ErrNoWork for an empty batch and
// leaves entries unpublished on delivery failure or cancellation. A crash or
// failed mark after successful delivery causes replay; handlers must deduplicate
// using EventID. Stop the poller before closing its delivery backend.
func (p *Poller) Poll(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.repo == nil {
		return fmt.Errorf("events: poller needs a repository: %w", sdk.ErrInvalidInput)
	}
	if p.deliver == nil {
		return fmt.Errorf("events: poller needs a delivery function: %w", sdk.ErrInvalidInput)
	}
	entries, err := p.repo.ListUnpublished(ctx, p.batchSize)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return workers.ErrNoWork
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := entry.Record.Validate(); err != nil {
			return err
		}
		if err := p.deliver(ctx, entry.Record.Event()); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := p.repo.MarkPublished(ctx, entry.EventID); err != nil {
			return err
		}
	}
	return nil
}
