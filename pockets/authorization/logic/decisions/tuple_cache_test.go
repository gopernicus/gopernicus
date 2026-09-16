package decisions_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
)

type boundTuples struct {
	*memory.Tuples
	binding string
}

func (r *boundTuples) TupleCacheBinding() string { return r.binding }

type testTupleSource struct {
	tuplecache.Source
	binding    string
	durableErr error
	facts      []tuples.Tuple
	durable    func(context.Context, func(context.Context, tuples.Reader) error) error
	reads      int
	ambient    bool
}

func (s *testTupleSource) Binding() string                       { return s.binding }
func (s *testTupleSource) CacheableContext(context.Context) bool { return !s.ambient }
func (s *testTupleSource) Snapshot(context.Context, string) (tuplecache.Snapshot, error) {
	return tuplecache.Snapshot{Full: true, Tuples: s.facts}, nil
}
func (s *testTupleSource) Acknowledge(context.Context, string, string, []string) error { return nil }
func (s *testTupleSource) ReadSnapshot(ctx context.Context, fn func(context.Context, tuples.Reader) error) error {
	s.reads++
	if s.durableErr != nil {
		return s.durableErr
	}
	return s.durable(ctx, fn)
}

type filterValidationBackend struct {
	tuplecache.Backend
	fail bool
}

func (b *filterValidationBackend) Read(ctx context.Context, state tuplecache.State, keys []tuplecache.SetKey) ([][]tuples.Tuple, error) {
	if b.fail && len(keys) == 0 {
		return nil, tuplecache.ErrUnavailable
	}
	return b.Backend.Read(ctx, state, keys)
}
func TestTupleCacheFilterValidationAndFallback(t *testing.T) {
	store := memory.NewTuples()
	grant := tuples.Tuple{Scope: tuples.On("space", "s1"), Relation: "viewer", Subject: tuples.SubjectRef{Type: "user", ID: "alice"}}
	if err := store.ApplyTuples(t.Context(), tuples.Changes{Add: []tuples.Tuple{grant}}); err != nil {
		t.Fatal(err)
	}
	raw := &boundTuples{Tuples: store, binding: "store"}
	source := &testTupleSource{binding: "store", facts: []tuples.Tuple{grant}, durable: store.ReadTupleSnapshot}
	backend := &filterValidationBackend{Backend: memory.NewTupleCache()}
	model := decisions.Model{ResourceTypes: map[string]decisions.ResourceTypeDef{"space": {Relations: map[string]decisions.RelationDef{"viewer": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "user"}}}}, Permissions: map[string]decisions.Expression{"view": decisions.Any(decisions.Direct("viewer"))}}}}
	service, err := decisions.NewService(raw, decisions.WithModel(model), decisions.WithTupleCache(backend, source, tuplecache.Policy{MaxStaleness: time.Minute}))
	if err != nil {
		t.Fatal(err)
	}
	cache := service.TupleCache()
	t.Cleanup(func() { _ = cache.Close() })
	if err := cache.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	alice := authmodel.PrincipalRef{Type: "user", ID: "alice"}
	for _, tc := range []struct {
		name string
		ids  []string
		want error
	}{{"empty", nil, nil}, {"invalid later ID", []string{"s1", ""}, sdk.ErrInvalidInput}, {"oversized", make([]string, service.Limits().MaxBatchSize+1), authmodel.ErrEvaluationLimit}} {
		t.Run(tc.name, func(t *testing.T) {
			before := cache.Stats()
			ids, err := service.FilterAuthorized(t.Context(), alice, "view", "space", tc.ids)
			if ids != nil || !errors.Is(err, tc.want) || cache.Stats() != before || source.reads != 0 {
				t.Fatalf("validation read facts: %v/%v", ids, err)
			}
		})
	}
	ids, err := service.FilterAuthorized(t.Context(), alice, "view", "space", []string{"s1", "missing", "s1"})
	if err != nil || !slices.Equal(ids, []string{"s1", "s1"}) || cache.Stats().Hits != 1 || source.reads != 0 {
		t.Fatalf("warm filter: %v/%v stats=%+v", ids, err, cache.Stats())
	}
	if err := store.ApplyTuples(t.Context(), tuples.Changes{Remove: []tuples.Tuple{grant}}); err != nil {
		t.Fatal(err)
	}
	backend.fail = true
	ids, err = service.FilterAuthorized(t.Context(), alice, "view", "space", []string{"s1"})
	if err != nil || ids == nil || len(ids) != 0 || source.reads != 1 {
		t.Fatalf("durable retry: %v/%v reads=%d", ids, err, source.reads)
	}
	source.durableErr = errors.New("snapshot failed")
	ids, err = service.FilterAuthorized(t.Context(), alice, "view", "space", []string{"s1"})
	if ids != nil || !errors.Is(err, source.durableErr) {
		t.Fatalf("failed retry leaked result: %v/%v", ids, err)
	}
	backend.fail = false
	source.ambient = true
	source.durableErr = nil
	before := cache.Stats()
	ids, err = service.FilterAuthorized(t.Context(), alice, "view", "space", []string{"s1"})
	if err != nil || ids == nil || len(ids) != 0 || source.reads != 3 || cache.Stats() != before {
		t.Fatalf("ambient source: %v/%v reads=%d stats=%+v", ids, err, source.reads, cache.Stats())
	}
}
func TestTupleCacheConstructionRequiresBoundRawSource(t *testing.T) {
	reader := &boundTuples{Tuples: memory.NewTuples(), binding: "store"}
	for _, tc := range []struct {
		name    string
		source  tuplecache.Source
		backend tuplecache.Backend
		want    error
	}{
		{"missing source", nil, memory.NewTupleCache(), sdk.ErrInvalidInput}, {"typed nil source", (*testTupleSource)(nil), memory.NewTupleCache(), sdk.ErrInvalidInput}, {"typed nil backend", &testTupleSource{binding: "store"}, (*memory.TupleCache)(nil), sdk.ErrInvalidInput}, {"other source", &testTupleSource{binding: "other"}, memory.NewTupleCache(), tuplecache.ErrBinding},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decisions.NewService(reader, decisions.WithTupleCache(tc.backend, tc.source, tuplecache.Policy{MaxStaleness: time.Second}))
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
	source := &testTupleSource{binding: "store"}
	service, err := decisions.NewService(reader, decisions.WithTupleCache(memory.NewTupleCache(), source, tuplecache.Policy{MaxStaleness: time.Second}))
	if err != nil || service.TupleCache() == nil {
		t.Fatalf("bound role-only cache: %v", err)
	}
	_ = service.TupleCache().Close()
	service, err = decisions.NewService(reader)
	if err != nil || service.TupleCache() != nil {
		t.Fatalf("default cache: %v", err)
	}
}
func TestTupleCacheFailedDurableRetryDiscardsProvisionalResults(t *testing.T) {
	reader := &boundTuples{Tuples: memory.NewTuples(), binding: "store"}
	grant := tuples.Tuple{Scope: tuples.Global(), Relation: "admin", Subject: tuples.SubjectRef{Type: "user", ID: "alice"}}
	failure := errors.New("durable failed")
	source := &testTupleSource{binding: "store", facts: []tuples.Tuple{grant}, durableErr: failure}
	backend := &filterValidationBackend{Backend: memory.NewTupleCache(), fail: true}
	service, err := decisions.NewService(reader, decisions.WithTupleCache(backend, source, tuplecache.Policy{MaxStaleness: time.Second}))
	if err != nil {
		t.Fatal(err)
	}
	defer service.TupleCache().Close()
	if err := service.TupleCache().Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	result, err := service.Evaluate(t.Context(), authmodel.PrincipalRef{Type: "user", ID: "alice"}, decisions.Role("admin"))
	if !errors.Is(err, failure) || result != (authmodel.CheckResult{}) {
		t.Fatalf("provisional result=%v/%v", result, err)
	}
}

func TestResourceBindingCacheFallbackPinsInputsOnly(t *testing.T) {
	hostFailure := errors.New("host input failed")
	for _, tc := range []struct {
		name       string
		expire     bool
		revoke     bool
		resolveErr error
		wantErr    error
		wantAllow  bool
	}{
		{name: "invalidated allow recomputes facts", revoke: true},
		{name: "attempt timeout preserves parent input", expire: true, wantAllow: true},
		{name: "resolver errors are memoized", resolveErr: hostFailure, wantErr: hostFailure},
		{name: "inapplicability is memoized", resolveErr: decisions.ErrResourceNotApplicable, wantAllow: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := memory.NewTuples()
			grant := tuples.Tuple{Scope: tuples.On("doc", "one"), Relation: "viewer", Subject: tuples.SubjectRef{Type: "user", ID: "alice"}}
			admin := tuples.Tuple{Scope: tuples.Global(), Relation: "admin", Subject: grant.Subject}
			if err := store.ApplyTuples(t.Context(), tuples.Changes{Add: []tuples.Tuple{grant, admin}}); err != nil {
				t.Fatal(err)
			}
			source := &testTupleSource{binding: "binding", facts: []tuples.Tuple{grant, admin}, durable: store.ReadTupleSnapshot}
			backend := &filterValidationBackend{Backend: memory.NewTupleCache()}
			policy := tuplecache.Policy{MaxStaleness: time.Minute, ReadTimeout: 10 * time.Millisecond}
			s, err := decisions.NewService(&boundTuples{Tuples: store, binding: "binding"}, decisions.WithTupleCache(backend, source, policy))
			if err != nil {
				t.Fatal(err)
			}
			cache := s.TupleCache()
			t.Cleanup(func() { _ = cache.Close() })
			if err := cache.Poll(t.Context()); err != nil {
				t.Fatal(err)
			}
			if tc.revoke {
				if err := store.ApplyTuples(t.Context(), tuples.Changes{Remove: []tuples.Tuple{grant}}); err != nil {
					t.Fatal(err)
				}
			}
			backend.fail = !tc.expire
			calls := 0
			parent := t.Context()
			expr := decisions.All(decisions.BindResource("doc", "doc", decisions.RoleIn("viewer")), decisions.BindResource("doc", "doc", decisions.RoleIn("viewer")))
			if tc.resolveErr == decisions.ErrResourceNotApplicable {
				expr = decisions.Any(expr, decisions.Role("admin"))
			}
			got, err := s.EvaluateResolved(parent, authmodel.PrincipalRef{Type: "user", ID: "alice"}, expr, func(ctx context.Context, key string) (authmodel.Resource, error) {
				calls++
				if ctx != parent || key != "doc" {
					t.Fatalf("resolver received attempt context or wrong key: key=%q", key)
				}
				if tc.expire {
					time.Sleep(3 * policy.ReadTimeout)
				}
				if ctx.Err() != nil {
					t.Fatalf("live parent input was canceled: %v", ctx.Err())
				}
				return authmodel.Resource{Type: "doc", ID: "one"}, tc.resolveErr
			})
			if !errors.Is(err, tc.wantErr) || got.Allowed != tc.wantAllow || calls != 1 || source.reads != 1 {
				t.Fatalf("fallback=%+v/%v calls=%d durable=%d", got, err, calls, source.reads)
			}
			if tc.wantErr != nil && got != (authmodel.CheckResult{}) {
				t.Fatalf("provisional result escaped: %+v", got)
			}
		})
	}
}
