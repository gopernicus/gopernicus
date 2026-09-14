package decisions

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/cacher"
)

var (
	ErrSnapshotClosed = fmt.Errorf("authorization cache snapshot closed: %w", sdk.ErrUnavailable)
	ErrCacheVersion   = fmt.Errorf("authorization cache version invalid: %w", sdk.ErrUnavailable)
	ErrCacheClosed    = fmt.Errorf("authorization cache runtime closed: %w", sdk.ErrUnavailable)
	ErrCacheNotReady  = fmt.Errorf("authorization cache protocol is not ready: %w", sdk.ErrUnavailable)
)

// CacheVersion identifies one committed fact generation within a store incarnation.
// It is internal cache coordination, not an authorization proof or mutation token.
type CacheVersion struct {
	Epoch      string
	Generation int64
}

// Validate rejects malformed metadata and generations outside the protocol range.
func (v CacheVersion) Validate() error {
	if len(v.Epoch) != 32 || v.Generation < 0 {
		return ErrCacheVersion
	}
	for _, c := range v.Epoch {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return ErrCacheVersion
		}
	}
	return nil
}

// CheckReads is a sequential callback-scoped capability. All methods, including
// readers returned by ForChecks, fail with ErrSnapshotClosed after callback exit.
type CheckReads interface {
	relationships.CheckReadSource
	HasExactRole(context.Context, string, string, string, string, string) (bool, error)
}

// CacheSource binds authoritative metadata and consistent check snapshots to one
// store. Callbacks run once; all views close on success, error, cancellation or
// panic. Neither callbacks nor their readers may escape to concurrent goroutines.
// Observe must never renew freshness from an ambient or lagging replica snapshot.
type CacheSource interface {
	CacheBinding() string
	CacheableContext(context.Context) bool
	Observe(context.Context) (CacheVersion, error)
	ReadSnapshot(context.Context, func(context.Context, CacheVersion, CheckReads) error) error
}

// CachePolicy explicitly accepts delayed observation of completed revocations.
// MaxStaleness is required; EntryTTL only controls storage reclamation.
type CachePolicy struct {
	Namespace      string
	MaxStaleness   time.Duration
	PollInterval   time.Duration
	PollTimeout    time.Duration
	CacheTimeout   time.Duration
	EntryTTL       time.Duration
	MaxEntryBytes  int
	MaxFillBytes   int
	MaxFillEntries int
}

func (p CachePolicy) resolve() (CachePolicy, error) {
	invalid := func() (CachePolicy, error) {
		return CachePolicy{}, fmt.Errorf("authorization cache policy: %w", sdk.ErrInvalidInput)
	}
	if strings.TrimSpace(p.Namespace) == "" || p.MaxStaleness <= 0 {
		return invalid()
	}
	if p.PollInterval == 0 {
		p.PollInterval = min(250*time.Millisecond, p.MaxStaleness/4)
	}
	if p.PollTimeout == 0 {
		p.PollTimeout = min(time.Second, p.MaxStaleness/4)
	}
	if p.PollInterval <= 0 || p.PollTimeout <= 0 || p.PollInterval >= p.MaxStaleness || p.PollTimeout >= p.MaxStaleness-p.PollInterval {
		return invalid()
	}
	if p.CacheTimeout == 0 {
		p.CacheTimeout = 25 * time.Millisecond
	}
	if p.EntryTTL == 0 {
		p.EntryTTL = 5 * time.Minute
	}
	if p.MaxEntryBytes == 0 {
		p.MaxEntryBytes = 64 << 10
	}
	if p.MaxFillBytes == 0 {
		p.MaxFillBytes = 1 << 20
	}
	if p.MaxFillEntries == 0 {
		p.MaxFillEntries = 256
	}
	if p.CacheTimeout <= 0 || p.EntryTTL <= 0 || p.MaxEntryBytes <= 0 || p.MaxFillBytes < p.MaxEntryBytes || p.MaxFillEntries <= 0 {
		return invalid()
	}
	return p, nil
}

// CacheStats contains aggregate operational state without keys or principals.
type CacheStats struct {
	HitComplete    uint64
	MissFallback   uint64
	BypassCold     uint64
	BypassExpired  uint64
	BypassContext  uint64
	BypassClosed   uint64
	CacheErrors    uint64
	SkippedFills   uint64
	PollFailures   uint64
	ObservationAge time.Duration
	Generation     int64
	Ready          bool
}

// CacheRuntime borrows its source and cacher, starts cold, and owns no goroutine.
// Hosts poll it and close it before closing borrowed resources.
type CacheRuntime struct {
	log         *slog.Logger
	logState    string
	lastLog     time.Time
	mu          sync.Mutex
	source      CacheSource
	cache       *cacher.Cache
	policy      CachePolicy
	closed      bool
	latched     bool
	haveVersion bool
	version     CacheVersion
	observedAt  time.Time
	stats       CacheStats
	now         func() time.Time
	pollGate    chan struct{}
	done        chan struct{}
	pollCancel  context.CancelFunc
}

func (s *Service) ReadCache() *CacheRuntime {
	if s == nil {
		return nil
	}
	return s.readCache
}
func (r *CacheRuntime) PollInterval() time.Duration { return r.policy.PollInterval }
func (r *CacheRuntime) Stats() CacheStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.stats
	if r.haveVersion {
		out.Generation = r.version.Generation
		out.ObservationAge = max(0, r.now().Sub(r.observedAt))
	}
	out.Ready = out.Ready && r.now().Before(r.observedAt.Add(r.policy.MaxStaleness))
	return out
}
func (r *CacheRuntime) Close() error {
	var event string
	defer func() { r.emitTransition(event) }()
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.closed {
		r.closed = true
		r.stats.Ready = false
		event = r.transitionLocked("closed")
		close(r.done)
		if r.pollCancel != nil {
			r.pollCancel()
		}
	}
	return nil
}
func (r *CacheRuntime) Poll(ctx context.Context) error {
	var event string
	defer func() { r.emitTransition(event) }()
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.done:
		return ErrCacheClosed
	case r.pollGate <- struct{}{}:
	}
	defer func() { <-r.pollGate }()
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ErrCacheClosed
	}
	if r.latched {
		r.mu.Unlock()
		return ErrCacheVersion
	}
	pollCtx, cancel := context.WithTimeout(ctx, r.policy.PollTimeout)
	r.pollCancel = cancel
	start := r.now()
	r.mu.Unlock()
	version, err := r.source.Observe(pollCtx)
	pollErr := pollCtx.Err()
	cancel()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pollCancel = nil
	if r.closed {
		return ErrCacheClosed
	}
	if r.latched {
		return ErrCacheVersion
	}
	if err == nil {
		err = version.Validate()
	}
	if err == nil && r.haveVersion && (version.Epoch != r.version.Epoch || version.Generation < r.version.Generation) {
		err = ErrCacheVersion
	}
	if pollErr != nil && !errors.Is(err, ErrCacheVersion) {
		err = pollErr
	}
	if err != nil {
		r.stats.Ready = false
		r.stats.PollFailures++
		if errors.Is(err, ErrCacheVersion) {
			r.latched = true
			event = r.transitionLocked("latched")
		} else {
			event = r.transitionLocked("unavailable")
		}
		return err
	}
	r.haveVersion = true
	r.version = version
	r.observedAt = start
	r.stats.Ready = r.now().Before(start.Add(r.policy.MaxStaleness))
	if !r.stats.Ready {
		event = r.transitionLocked("unavailable")
		return ErrCacheNotReady
	}
	event = r.transitionLocked("healthy")
	return nil
}

type observation struct {
	version CacheVersion
	started time.Time
}

func (r *CacheRuntime) capture(ctx context.Context) (observation, bool) {
	if !r.source.CacheableContext(ctx) {
		r.mu.Lock()
		r.stats.BypassContext++
		r.mu.Unlock()
		return observation{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case r.closed:
		r.stats.BypassClosed++
	case !r.stats.Ready:
		r.stats.BypassCold++
	case !r.now().Before(r.observedAt.Add(r.policy.MaxStaleness)):
		r.stats.BypassExpired++
	default:
		return observation{r.version, r.observedAt}, true
	}
	return observation{}, false
}
func (r *CacheRuntime) valid(observed observation) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.closed && !r.latched && r.stats.Ready && r.version == observed.version && r.now().Before(observed.started.Add(r.policy.MaxStaleness))
}
func (r *CacheRuntime) latch() {
	r.mu.Lock()
	r.latched = true
	r.stats.Ready = false
	event := r.transitionLocked("latched")
	r.mu.Unlock()
	r.emitTransition(event)
}
func (r *CacheRuntime) count(update func(*CacheStats)) { r.mu.Lock(); update(&r.stats); r.mu.Unlock() }

func newCacheRuntime(cfg config) (*CacheRuntime, error) {
	if cfg.cacher == nil {
		return nil, nil
	}
	if typedNil(cfg.cacher) {
		return nil, fmt.Errorf("authorization: typed nil cacher: %w", sdk.ErrInvalidInput)
	}
	policy, err := cfg.cachePolicy.resolve()
	if err != nil {
		return nil, err
	}
	if cfg.CacheSource == nil || typedNil(cfg.CacheSource) {
		return nil, fmt.Errorf("authorization: cache source required: %w", sdk.ErrInvalidInput)
	}
	binding := cfg.CacheSource.CacheBinding()
	if binding == "" {
		return nil, fmt.Errorf("authorization: empty cache binding: %w", sdk.ErrInvalidInput)
	}
	for _, reader := range []any{cfg.Relationships, cfg.Roles} {
		if reader == nil || typedNil(reader) {
			continue
		}
		bound, ok := reader.(interface{ CacheBinding() string })
		if !ok || bound.CacheBinding() != binding {
			return nil, fmt.Errorf("authorization: reader cache binding mismatch: %w", sdk.ErrInvalidInput)
		}
	}
	return &CacheRuntime{log: slog.Default(), source: cfg.CacheSource, cache: cacher.New(cfg.cacher, cacher.WithNamespace(policy.Namespace)), policy: policy, now: time.Now, pollGate: make(chan struct{}, 1), done: make(chan struct{})}, nil
}

// Repeated states never log. Transient flapping is capped at one record per
// minute; permanent latching and closure each get one additional lifecycle record.
// No backend error text, keys, namespace or principal data enters these records.
func (r *CacheRuntime) transitionLocked(state string) string {
	if state == r.logState {
		return ""
	}
	previous := r.logState
	r.logState = state
	if r.log == nil || (previous == "" && state == "healthy") {
		return ""
	}
	now := r.now()
	if state != "latched" && state != "closed" && !r.lastLog.IsZero() && now.Sub(r.lastLog) < time.Minute {
		return ""
	}
	r.lastLog = now
	return state
}

// Host handlers may inspect this runtime; never call them under either lock.
func (r *CacheRuntime) emitTransition(state string) {
	if state == "" {
		return
	}
	if state == "unavailable" || state == "latched" {
		r.log.Warn("authorization cache state changed", "state", state)
	} else {
		r.log.Info("authorization cache state changed", "state", state)
	}
}
