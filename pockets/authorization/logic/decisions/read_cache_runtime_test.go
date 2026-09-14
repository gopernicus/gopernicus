package decisions

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/sdk/capabilities/cacher"
)

type cacheTestClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *cacheTestClock) now() time.Time          { c.mu.Lock(); defer c.mu.Unlock(); return c.at }
func (c *cacheTestClock) advance(d time.Duration) { c.mu.Lock(); c.at = c.at.Add(d); c.mu.Unlock() }

type cacheTestSource struct {
	version   CacheVersion
	values    map[string]bool
	observe   func(context.Context) (CacheVersion, error)
	snapshot  func(context.Context, func(context.Context, CacheVersion, CheckReads) error) error
	snapshots int
	reads     int
	eligible  bool
}

func (s *cacheTestSource) CacheBinding() string                  { return "fixture" }
func (s *cacheTestSource) CacheableContext(context.Context) bool { return s.eligible }
func (s *cacheTestSource) Observe(ctx context.Context) (CacheVersion, error) {
	if s.observe != nil {
		return s.observe(ctx)
	}
	return s.version, nil
}
func (s *cacheTestSource) ReadSnapshot(ctx context.Context, fn func(context.Context, CacheVersion, CheckReads) error) error {
	s.snapshots++
	if s.snapshot != nil {
		return s.snapshot(ctx, fn)
	}
	return fn(ctx, s.version, s)
}
func (s *cacheTestSource) ForChecks(relationships.ReadModel) relationships.CheckReader { return nil }
func (s *cacheTestSource) HasExactRole(ctx context.Context, _, _, role, _, _ string) (bool, error) {
	s.reads++
	return s.values[role], ctx.Err()
}
func newTestCacheRuntime(t *testing.T) (*CacheRuntime, *cacheTestSource, *cacheTestClock) {
	t.Helper()
	policy, err := (CachePolicy{Namespace: "fixture", MaxStaleness: time.Second, CacheTimeout: time.Second}).resolve()
	if err != nil {
		t.Fatal(err)
	}
	source := &cacheTestSource{version: CacheVersion{Epoch: "0123456789abcdef0123456789abcdef"}, values: map[string]bool{}, eligible: true}
	clock := &cacheTestClock{at: time.Now()}
	runtime := &CacheRuntime{source: source, cache: cacher.New(cacher.NewMemory(), cacher.WithNamespace(policy.Namespace)), policy: policy, now: clock.now, pollGate: make(chan struct{}, 1), done: make(chan struct{})}
	return runtime, source, clock
}
func roleOperation(direct *int, roles ...string) func(context.Context, CheckReads) (operationResult, error) {
	return func(ctx context.Context, reads CheckReads) (operationResult, error) {
		if reads == nil {
			*direct++
			return operationResult{}, nil
		}
		allowed := true
		for _, role := range roles {
			held, err := reads.HasExactRole(ctx, "user", "u1", role, "doc", "d1")
			if err != nil {
				return operationResult{}, err
			}
			allowed = allowed && held
		}
		return operationResult{result: authmodel.CheckResult{Allowed: allowed}}, nil
	}
}
func TestCacheWholeOperationGenerationFallback(t *testing.T) {
	r, source, _ := newTestCacheRuntime(t)
	ctx := t.Context()
	source.values["first"] = true
	if err := r.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	direct := 0
	first := roleOperation(&direct, "first")
	if result, err := r.run(ctx, first); err != nil || !result.result.Allowed {
		t.Fatalf("fill=%+v/%v", result, err)
	}
	if result, err := r.run(ctx, first); err != nil || !result.result.Allowed || source.snapshots != 1 {
		t.Fatalf("hit=%+v/%v snapshots=%d", result, err, source.snapshots)
	}
	source.version.Generation = 1
	source.values["first"] = false
	source.values["second"] = true
	result, err := r.run(ctx, roleOperation(&direct, "first", "second"))
	if err != nil || result.result.Allowed || source.snapshots != 2 || source.reads != 3 || direct != 0 {
		t.Fatalf("mixed generation=%+v/%v snapshots=%d reads=%d direct=%d", result, err, source.snapshots, source.reads, direct)
	}
	if err := r.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := r.run(ctx, roleOperation(&direct, "first", "second")); err != nil || source.snapshots != 2 {
		t.Fatalf("new generation hit: %v snapshots=%d", err, source.snapshots)
	}
}
func TestCacheFinalAgeAndColdBypass(t *testing.T) {
	r, s, clock := newTestCacheRuntime(t)
	ctx := t.Context()
	s.values["viewer"] = true
	direct := 0
	operation := roleOperation(&direct, "viewer")
	if _, err := r.run(ctx, operation); err != nil || direct != 1 || s.snapshots != 0 {
		t.Fatalf("cold bypass: %v", err)
	}
	if err := r.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := r.run(ctx, operation); err != nil {
		t.Fatal(err)
	}
	advance := true
	result, err := r.run(ctx, func(ctx context.Context, reads CheckReads) (operationResult, error) {
		out, err := operation(ctx, reads)
		if advance {
			clock.advance(time.Second)
			advance = false
		}
		return out, err
	})
	if err != nil || !result.result.Allowed || s.snapshots != 2 {
		t.Fatalf("expired final hit escaped: %+v/%v snapshots=%d", result, err, s.snapshots)
	}
	if _, err := r.run(ctx, operation); err != nil || direct != 2 {
		t.Fatalf("expired bypass: %v direct=%d", err, direct)
	}
	if r.Stats().Ready || r.Stats().HitComplete != 0 {
		t.Fatalf("invalid freshness stats: %+v", r.Stats())
	}
}
func TestCachePollAgeFailureAndRegression(t *testing.T) {
	r, s, clock := newTestCacheRuntime(t)
	s.observe = func(context.Context) (CacheVersion, error) { clock.advance(2 * time.Second); return s.version, nil }
	if err := r.Poll(t.Context()); !errors.Is(err, ErrCacheNotReady) || r.Stats().Ready {
		t.Fatalf("late poll=%v %+v", err, r.Stats())
	}
	s.observe = nil
	s.version.Generation = 3
	if err := r.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("authority offline")
	s.observe = func(context.Context) (CacheVersion, error) { return CacheVersion{}, failure }
	if err := r.Poll(t.Context()); !errors.Is(err, failure) || r.Stats().Ready {
		t.Fatalf("failed poll=%v", err)
	}
	s.observe = nil
	if err := r.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	s.version.Generation = 2
	if err := r.Poll(t.Context()); !errors.Is(err, ErrCacheVersion) {
		t.Fatal(err)
	}
	s.version.Generation = 4
	if err := r.Poll(t.Context()); !errors.Is(err, ErrCacheVersion) || r.Stats().Ready {
		t.Fatalf("regression did not latch: %v", err)
	}
}
func TestCachePollCloseAndCanceledWaiter(t *testing.T) {
	r, s, _ := newTestCacheRuntime(t)
	entered := make(chan struct{})
	released := make(chan struct{})
	var calls atomic.Int32
	s.observe = func(ctx context.Context) (CacheVersion, error) {
		calls.Add(1)
		close(entered)
		<-ctx.Done()
		close(released)
		return CacheVersion{}, ctx.Err()
	}
	result := make(chan error, 1)
	go func() { result <- r.Poll(t.Context()) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("poll did not enter")
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := r.Poll(canceled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, ErrCacheClosed) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("close did not cancel poll")
	}
	<-released
	if calls.Load() != 1 || r.Stats().Ready {
		t.Fatal("closed poll became ready")
	}
	if err := r.Poll(t.Context()); !errors.Is(err, ErrCacheClosed) {
		t.Fatal(err)
	}
}
func TestCacheSnapshotRegressionAndFailedClosureDoNotPublish(t *testing.T) {
	for _, mode := range []string{"regression", "close failure", "evaluation failure"} {
		t.Run(mode, func(t *testing.T) {
			r, s, _ := newTestCacheRuntime(t)
			s.version.Generation = 3
			s.values["viewer"] = true
			if err := r.Poll(t.Context()); err != nil {
				t.Fatal(err)
			}
			failure := errors.New("snapshot failed")
			s.snapshot = func(ctx context.Context, fn func(context.Context, CacheVersion, CheckReads) error) error {
				v := s.version
				if mode == "regression" {
					v.Generation--
				}
				if err := fn(ctx, v, s); err != nil {
					return err
				}
				return failure
			}
			direct := 0
			operation := roleOperation(&direct, "viewer")
			_, err := r.run(t.Context(), func(ctx context.Context, reads CheckReads) (operationResult, error) {
				out, err := operation(ctx, reads)
				if err == nil && mode == "evaluation failure" {
					out.explanation.Steps = []authmodel.ExplainStep{{Kind: authmodel.ExplainKindRole}}
					return out, failure
				}
				return out, err
			})
			if mode == "regression" {
				if !errors.Is(err, ErrCacheVersion) || r.Stats().Ready {
					t.Fatalf("regression=%v", err)
				}
			} else if !errors.Is(err, failure) {
				t.Fatal(err)
			}
			reader := &cacheReads{runtime: r, version: s.version}
			if _, err := reader.HasExactRole(t.Context(), "user", "u1", "viewer", "doc", "d1"); !errors.Is(err, errCacheMiss) {
				t.Fatalf("failed snapshot published: %v", err)
			}
		})
	}
}

type slowCache struct {
	cacher.Storer
	get func(context.Context) ([]byte, bool, error)
	set func(context.Context) error
}

func (s slowCache) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if s.get != nil {
		return s.get(ctx)
	}
	return s.Storer.Get(ctx, key)
}
func (s slowCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if s.set != nil {
		return s.set(ctx)
	}
	return s.Storer.Set(ctx, key, value, ttl)
}
func TestCacheAttemptTimeoutAndFillFailurePreserveDurableResult(t *testing.T) {
	r, s, _ := newTestCacheRuntime(t)
	s.values["viewer"] = true
	r.policy.CacheTimeout = time.Millisecond
	r.cache = cacher.New(slowCache{get: func(ctx context.Context) ([]byte, bool, error) { <-ctx.Done(); return nil, false, ctx.Err() }, set: func(context.Context) error { return errors.New("fill unavailable") }})
	if err := r.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	direct := 0
	got, err := r.run(t.Context(), roleOperation(&direct, "viewer"))
	if err != nil || !got.result.Allowed || s.snapshots != 1 || direct != 0 {
		t.Fatalf("timeout fallback=%+v/%v snapshots=%d", got, err, s.snapshots)
	}
	if r.Stats().CacheErrors != 2 {
		t.Fatalf("cache errors=%+v", r.Stats())
	}
}

func TestCacheEvaluatorDeadlineFallsBackButLimitDoesNot(t *testing.T) {
	for _, genuine := range []bool{false, true} {
		r, s, _ := newTestCacheRuntime(t)
		r.policy.CacheTimeout = time.Millisecond
		if err := r.Poll(t.Context()); err != nil {
			t.Fatal(err)
		}
		attempts := 0
		_, err := r.run(t.Context(), func(ctx context.Context, _ CheckReads) (operationResult, error) {
			attempts++
			if attempts == 1 {
				<-ctx.Done()
				if genuine {
					return operationResult{}, authmodel.ErrEvaluationLimit
				}
				return operationResult{}, ctx.Err()
			}
			return operationResult{}, nil
		})
		if genuine {
			if !errors.Is(err, authmodel.ErrEvaluationLimit) || s.snapshots != 0 {
				t.Fatalf("genuine limit changed: %v snapshots=%d", err, s.snapshots)
			}
		} else if err != nil || s.snapshots != 1 {
			t.Fatalf("evaluator timeout did not fallback: %v snapshots=%d", err, s.snapshots)
		}
	}
}

func TestCacheTransitionLogsAreBoundedAndRedacted(t *testing.T) {
	r, s, clock := newTestCacheRuntime(t)
	var output bytes.Buffer
	r.log = slog.New(slog.NewTextHandler(&output, nil))
	if err := r.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	s.observe = func(context.Context) (CacheVersion, error) {
		return CacheVersion{}, errors.New("secret principal backend payload")
	}
	for range 3 {
		_ = r.Poll(t.Context())
	}
	if strings.Count(output.String(), "state=unavailable") != 1 || strings.Contains(output.String(), "secret") {
		t.Fatalf("unsafe/flooding logs: %s", output.String())
	}
	s.observe = nil
	clock.advance(time.Minute)
	if err := r.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if strings.Count(output.String(), "state=healthy") != 1 || strings.Count(output.String(), "state=closed") != 1 {
		t.Fatalf("lifecycle logs: %s", output.String())
	}
}

func TestCacheCallerCancellationNeverFallsBack(t *testing.T) {
	r, s, _ := newTestCacheRuntime(t)
	if err := r.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r.cache = cacher.New(slowCache{get: func(ctx context.Context) ([]byte, bool, error) { cancel(); return nil, false, ctx.Err() }})
	direct := 0
	_, err := r.run(ctx, roleOperation(&direct, "viewer"))
	if !errors.Is(err, context.Canceled) || s.snapshots != 0 || direct != 0 {
		t.Fatalf("caller cancel=%v snapshots=%d direct=%d", err, s.snapshots, direct)
	}
}
func TestCacheMalformedLatePollLatches(t *testing.T) {
	r, s, _ := newTestCacheRuntime(t)
	r.policy.PollTimeout = time.Millisecond
	s.observe = func(ctx context.Context) (CacheVersion, error) { <-ctx.Done(); return CacheVersion{}, ErrCacheVersion }
	if err := r.Poll(t.Context()); !errors.Is(err, ErrCacheVersion) {
		t.Fatalf("late malformed poll: %v", err)
	}
	s.observe = nil
	if err := r.Poll(t.Context()); !errors.Is(err, ErrCacheVersion) {
		t.Fatalf("malformed poll did not latch: %v", err)
	}
}
