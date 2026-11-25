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

type pollerConfig struct {
	batchSize int
	lock      Lock
	lockSet   bool
}

// WithBatchSize sets the number of unpublished entries drained per Poll. A
// non-positive value restores the default (100).
func WithBatchSize(n int) PollerOption {
	return func(c *pollerConfig) { c.batchSize = n }
}

// WithLock makes Poll drain only while it holds l, so pollers armed on several
// instances drain one at a time. A Poll that finds the lock held elsewhere reads
// nothing and returns workers.ErrNoWork, so a standby idles on its pool's normal
// interval. A nil lock is rejected at NewPoller. Without WithLock, Poll never
// locks and each armed poller drains independently.
func WithLock(l Lock) PollerOption {
	return func(c *pollerConfig) {
		c.lock = l
		c.lockSet = true
	}
}

// DeliverFunc completes the host's chosen handoff: Memory.Dispatch waits for
// local handlers; a Redis bus's Publish waits for stream acceptance. Do not pass
// an asynchronous Emit or a discard callback when delivery must be durable.
type DeliverFunc func(context.Context, sdkevents.Event) error

// Poller drains an outbox through an explicit delivery function. It owns no
// goroutines; the host drives Poll with a worker pool. ListUnpublished does not
// claim rows, so run one poller per outbox, or give every poller a shared Lock
// (WithLock) so only one drains at a time.
type Poller struct {
	repo      EntryRepository
	deliver   DeliverFunc
	batchSize int
	lock      Lock
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
	if cfg.lockSet && isNil(cfg.lock) {
		return nil, fmt.Errorf("events outbox: nil lock: %w", sdk.ErrInvalidInput)
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
	return &Poller{repo: repo, deliver: deliver, batchSize: cfg.batchSize, lock: cfg.lock}, nil
}

// Poll satisfies workers.WorkFunc. It returns ErrNoWork for an empty batch and
// leaves entries unpublished on delivery failure or cancellation. A crash or
// failed mark after successful delivery causes replay; handlers must deduplicate
// using EventID. Stop the poller before closing its delivery backend. With a
// Lock, Poll drains only while holding it and returns ErrNoWork when another
// poller holds it.
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
	if p.lock == nil {
		return p.drain(ctx)
	}
	ran, err := p.lock.TryRun(ctx, p.drain)
	if !ran && err == nil {
		return workers.ErrNoWork
	}
	return err
}

// drain delivers and marks one oldest-first batch.
func (p *Poller) drain(ctx context.Context) error {
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

// isNil reports whether v is nil or a typed nil a method call would dereference.
func isNil(v any) bool {
	if v == nil {
		return true
	}
	value := reflect.ValueOf(v)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	}
	return false
}
