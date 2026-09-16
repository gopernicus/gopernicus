package tuplecache

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/workers"
)

// Policy separates delivery freshness from tuple storage: tuples do not expire.
// MaxStaleness must be explicitly chosen by the host; it accepts delayed writes.
type Policy struct {
	MaxStaleness time.Duration
	PollInterval time.Duration
	PollTimeout  time.Duration
	ReadTimeout  time.Duration
}

type Option func(*Policy)

func WithPolicy(policy Policy) Option { return func(p *Policy) { *p = policy } }

// PollStats describes the last completed delivery attempt. Changes and Tuples
// are the captured snapshot sizes, not a live query of the source's backlog.
type PollStats struct {
	StartedAt                                       time.Time
	Duration, SnapshotDuration, PublicationDuration time.Duration
	Changes, Tuples                                 int
	Full                                            bool
	FailureStage                                    string
}

type Stats struct {
	Hits, Fallbacks, Publications, Rebuilds, PollFailures uint64
	CapacityFallbacks, CapacityFailures, PollConflicts    uint64
	LastSuccessfulPoll                                    time.Time
	LastPoll                                              PollStats
}

// TupleCache borrows its source/backend. The host drives Poll (a workers.WorkFunc)
// and may wake that same worker after commit. Constructors start no goroutines.
type TupleCache struct {
	source                                            Source
	backend                                           Backend
	policy                                            Policy
	binding                                           string
	gate                                              chan struct{}
	wake                                              chan struct{}
	closed, ready                                     atomic.Bool
	hits, fallbacks, publications, rebuilds, failures atomic.Uint64
	capacityFallbacks, capacityFailures, conflicts    atomic.Uint64
	lastSuccess                                       atomic.Int64
	lastPoll                                          atomic.Pointer[PollStats]
}

func New(source Source, backend Backend, opts ...Option) (*TupleCache, error) {
	p := Policy{}
	for _, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("tuple cache: nil option: %w", sdk.ErrInvalidInput)
		}
		opt(&p)
	}
	if nilValue(source) || nilValue(backend) || source.Binding() == "" || p.MaxStaleness <= 0 {
		return nil, fmt.Errorf("tuple cache: source, backend and positive MaxStaleness required: %w", sdk.ErrInvalidInput)
	}
	if p.PollInterval == 0 {
		p.PollInterval = min(250*time.Millisecond, p.MaxStaleness/4)
	}
	if p.PollTimeout == 0 {
		p.PollTimeout = 30 * time.Second
	}
	if p.ReadTimeout == 0 {
		p.ReadTimeout = 250 * time.Millisecond
	}
	if p.PollInterval <= 0 || p.PollInterval >= p.MaxStaleness || p.PollTimeout <= 0 || p.ReadTimeout <= 0 {
		return nil, fmt.Errorf("tuple cache: invalid policy: %w", sdk.ErrInvalidInput)
	}
	// Shared eligibility must mean the same thing to every process. Bind the
	// freshness policy as well as the source identity, so a more permissive
	// relay cannot silently extend another host's revocation bound.
	binding := fmt.Sprintf("protocol:%d/%s/max-staleness:%d", Protocol, source.Binding(), p.MaxStaleness)
	return &TupleCache{source: source, backend: backend, policy: p, binding: binding, gate: make(chan struct{}, 1), wake: make(chan struct{}, 1)}, nil
}

func nilValue(v any) bool {
	if v == nil {
		return true
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Func, reflect.Map, reflect.Slice, reflect.Chan:
		return r.IsNil()
	}
	return false
}

func (c *TupleCache) Binding() string             { return c.binding }
func (c *TupleCache) PollInterval() time.Duration { return c.policy.PollInterval }
func (c *TupleCache) Close() error                { c.closed.Store(true); return nil }

// Notify is a non-blocking, coalesced hint to deliver committed mutations. The
// host calls it after its outer transaction commits; polling handles lost hints.
func (c *TupleCache) Notify() {
	if c.closed.Load() {
		return
	}
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// WakeChannel connects Notify to workers.WithWakeChannel. Do not close it.
func (c *TupleCache) WakeChannel() <-chan struct{} { return c.wake }
func (c *TupleCache) Stats() Stats {
	stats := Stats{
		Hits: c.hits.Load(), Fallbacks: c.fallbacks.Load(), Publications: c.publications.Load(),
		Rebuilds: c.rebuilds.Load(), PollFailures: c.failures.Load(),
		CapacityFallbacks: c.capacityFallbacks.Load(), CapacityFailures: c.capacityFailures.Load(),
		PollConflicts: c.conflicts.Load(),
	}
	if nanos := c.lastSuccess.Load(); nanos != 0 {
		stats.LastSuccessfulPoll = time.Unix(0, nanos)
	}
	if poll := c.lastPoll.Load(); poll != nil {
		stats.LastPoll = *poll
	}
	return stats
}

// Poll uses a consistent source snapshot for each complete publication. SQL
// acknowledgements delete exact event IDs; sequence gaps are never skipped.
func (c *TupleCache) Poll(ctx context.Context) error { return c.poll(ctx, false) }

// Rebuild replaces the mirror from current authoritative facts, bypassing the
// decoding of obsolete outbox payloads in the bundled SQL sources. It retains
// the same freshness bound, publication gate and exact-ID acknowledgement as
// Poll. Hosts can use it after a backlog exceeds delta publication capacity.
// A concurrent local delivery returns workers.ErrNoWork; retry after it finishes.
func (c *TupleCache) Rebuild(ctx context.Context) error { return c.poll(ctx, true) }

func (c *TupleCache) poll(ctx context.Context, rebuild bool) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.closed.Load() {
		return ErrUnavailable
	}
	if !c.source.CacheableContext(ctx) {
		return fmt.Errorf("tuple cache: ambient delivery: %w", sdk.ErrInvalidInput)
	}
	select {
	case c.gate <- struct{}{}:
		defer func() { <-c.gate }()
	default:
		return workers.ErrNoWork
	}
	report := PollStats{StartedAt: time.Now()}
	stage := "state"
	succeeded := false
	defer func() {
		report.Duration = time.Since(report.StartedAt)
		if !succeeded {
			c.failures.Add(1)
			report.FailureStage = stage
			if errors.Is(err, ErrCapacity) {
				c.capacityFailures.Add(1)
			}
			if errors.Is(err, ErrConflict) {
				c.conflicts.Add(1)
			}
		} else {
			c.lastSuccess.Store(time.Now().UnixNano())
		}
		c.lastPoll.Store(&report)
	}()
	ctx, cancel := context.WithTimeout(ctx, c.policy.PollTimeout)
	defer cancel()
	before, err := c.backend.State(ctx)
	if err != nil {
		return err
	}
	if before.Binding != "" && before.Binding != c.Binding() {
		return ErrBinding
	}
	started := time.Now()
	stage = "snapshot"
	receipt := before.Receipt
	if rebuild {
		receipt = ""
	}
	snapshot, err := c.source.Snapshot(ctx, receipt)
	report.SnapshotDuration = time.Since(started)
	if err != nil {
		return err
	}
	report.Changes, report.Tuples, report.Full = len(snapshot.Changes), len(snapshot.Tuples), snapshot.Full
	if rebuild && !snapshot.Full {
		return ErrUnavailable
	}
	if !snapshot.Full && (before.Binding != c.Binding() || before.Receipt == "" || before.Receipt != snapshot.Receipt) {
		return ErrConflict
	}
	next := before
	if snapshot.Full || len(snapshot.Changes) != 0 {
		var b [16]byte
		if _, err := rand.Read(b[:]); err != nil {
			return err
		}
		next = State{Binding: c.Binding(), Receipt: hex.EncodeToString(b[:])}
	}
	remaining := c.policy.MaxStaleness - time.Since(started)
	stage = "freshness"
	if remaining <= 0 {
		return ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.closed.Load() {
		return ErrUnavailable
	}
	stage = "publication"
	publishing := time.Now()
	err = c.backend.Publish(ctx, before, next, snapshot, remaining)
	report.PublicationDuration = time.Since(publishing)
	if err != nil {
		return err
	}
	if next == before {
		c.ready.Store(true)
		succeeded = true
		return nil
	}
	ids := make([]string, len(snapshot.Changes))
	for i, change := range snapshot.Changes {
		ids[i] = change.ID
	}
	stage = "acknowledgement"
	if err := c.source.Acknowledge(ctx, snapshot.Receipt, next.Receipt, ids); err != nil {
		return err
	}
	c.publications.Add(1)
	if snapshot.Full {
		c.rebuilds.Add(1)
	}
	c.ready.Store(true)
	succeeded = true
	return nil
}

var _ tuples.Snapshotter = (*TupleCache)(nil)

// ReadTupleSnapshot supplies one coherent canonical view, with the same explicit
// freshness policy and whole-operation durable retry as Run. The callback may
// execute twice and must not publish results until this method returns nil.
func (c *TupleCache) ReadTupleSnapshot(ctx context.Context, evaluate func(context.Context, tuples.Reader) error) error {
	return c.Run(ctx, evaluate)
}

// Run evaluates over raw cached reads or retries once inside a durable snapshot.
// Callbacks must be read-only and cannot retain their readers. No result is
// cached. Ambient transactions bypass Redis and borrow the source's bound view.
func (c *TupleCache) Run(ctx context.Context, evaluate func(context.Context, CheckReads) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if evaluate == nil {
		return fmt.Errorf("tuple cache: nil callback: %w", sdk.ErrInvalidInput)
	}
	if !c.source.CacheableContext(ctx) {
		return c.source.ReadSnapshot(ctx, evaluate)
	}
	if !c.closed.Load() && c.ready.Load() {
		valid, err := c.attempt(ctx, evaluate)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if valid {
			c.hits.Add(1)
			return err
		}
		if errors.Is(err, ErrCapacity) {
			c.capacityFallbacks.Add(1)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c.fallbacks.Add(1)
	return c.source.ReadSnapshot(ctx, evaluate)
}

func (c *TupleCache) attempt(ctx context.Context, evaluate func(context.Context, CheckReads) error) (bool, error) {
	cacheCtx, cancel := context.WithTimeout(ctx, c.policy.ReadTimeout)
	defer cancel()
	state, err := c.backend.State(cacheCtx)
	if err != nil {
		return false, err
	}
	if state.Binding != c.Binding() || state.Receipt == "" {
		return false, ErrUnavailable
	}
	reader := &cachedReads{ctx: cacheCtx, backend: c.backend, state: state, sets: make(map[SetKey][]tuples.Tuple)}
	defer func() { reader.closed = true }()
	err = evaluate(cacheCtx, reader)
	_, validationErr := c.backend.Read(cacheCtx, state, nil)
	valid := !reader.failed && validationErr == nil && cacheCtx.Err() == nil && !c.closed.Load()
	if !valid {
		return false, errors.Join(err, reader.failure, validationErr, cacheCtx.Err())
	}
	return valid, err
}
