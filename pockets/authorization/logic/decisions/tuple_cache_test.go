package decisions

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
)

type boundTupleRelationships struct {
	*memory.Relationships
	binding string
}

func (r *boundTupleRelationships) TupleCacheBinding() string { return r.binding }

type testTupleSource struct {
	tuplecache.Source
	binding    string
	durableErr error
	tuples     []relationships.CreateRelationship
	durable    func(context.Context, func(context.Context, tuplecache.CheckReads) error) error
	reads      int
	ambient    bool
}

func (s *testTupleSource) Binding() string                       { return s.binding }
func (s *testTupleSource) CacheableContext(context.Context) bool { return !s.ambient }
func (s *testTupleSource) Snapshot(context.Context, string) (tuplecache.Snapshot, error) {
	return tuplecache.Snapshot{Full: true, Tuples: s.tuples}, nil
}
func (s *testTupleSource) Acknowledge(context.Context, string, string, []string) error { return nil }
func (s *testTupleSource) ReadSnapshot(ctx context.Context, fn func(context.Context, tuplecache.CheckReads) error) error {
	s.reads++
	if s.durableErr == nil && s.durable != nil {
		return s.durable(ctx, fn)
	}
	return s.durableErr
}

type filterValidationBackend struct {
	tuplecache.Backend
	fail bool
}

func (b *filterValidationBackend) Read(ctx context.Context, state tuplecache.State, keys []tuplecache.SetKey) ([][]relationships.SubjectRef, error) {
	if b.fail && len(keys) == 0 {
		return nil, tuplecache.ErrUnavailable
	}
	return b.Backend.Read(ctx, state, keys)
}

func TestTupleCacheFilterValidationAndFallback(t *testing.T) {
	store := memory.New()
	grant := relationships.CreateRelationship{ResourceType: "space", ResourceID: "s1", Relation: "viewer", SubjectType: "user", SubjectID: "alice"}
	if err := store.Relationships().CreateRelationships(t.Context(), []relationships.CreateRelationship{grant}); err != nil {
		t.Fatal(err)
	}
	raw := &boundTupleRelationships{Relationships: store.Relationships(), binding: "store"}
	parts, err := relationships.NewService(raw, relationships.Schema{ResourceTypes: map[string]relationships.ResourceTypeDef{
		"space": {Relations: map[string]relationships.RelationDef{"viewer": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}}}}, Permissions: map[string]relationships.PermissionRule{"view": relationships.AnyOf(relationships.Direct("viewer"))}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	source := &testTupleSource{binding: "store", tuples: []relationships.CreateRelationship{grant}, durable: store.ReadSnapshot}
	backend := &filterValidationBackend{Backend: memory.NewTupleCache()}
	service, err := NewService(Readers{Relationships: parts.Service, TupleSource: source}, WithTupleCache(backend, tuplecache.Policy{MaxStaleness: time.Minute}))
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
	}{
		{"empty", nil, nil},
		{"invalid later ID", []string{"s1", ""}, sdk.ErrInvalidInput},
		{"oversized before validation", make([]string, service.Limits().MaxBatchSize+1), authmodel.ErrEvaluationLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := cache.Stats()
			ids, err := service.FilterAuthorized(t.Context(), alice, "view", "space", tc.ids)
			if ids != nil || !errors.Is(err, tc.want) || cache.Stats() != before || source.reads != 0 {
				t.Fatalf("filter should return before reads: %v/%v, stats %+v -> %+v, snapshots %d", ids, err, before, cache.Stats(), source.reads)
			}
		})
	}
	ids, err := service.FilterAuthorized(t.Context(), alice, "view", "space", []string{"s1", "missing", "s1"})
	if err != nil || !slices.Equal(ids, []string{"s1", "s1"}) || cache.Stats().Hits != 1 || source.reads != 0 {
		t.Fatalf("warm direct filter: %v/%v, stats %+v, snapshots %d", ids, err, cache.Stats(), source.reads)
	}
	// Let cached evaluation grant, then invalidate the final receipt check.
	// The durable retry must discard those provisional grants after revocation.
	if err := store.Relationships().DeleteRelationshipTarget(t.Context(), "space", "s1", "viewer", grant.Subject()); err != nil {
		t.Fatal(err)
	}
	backend.fail = true
	ids, err = service.FilterAuthorized(t.Context(), alice, "view", "space", []string{"s1"})
	if err != nil || ids == nil || len(ids) != 0 || source.reads != 1 || cache.Stats().Fallbacks != 1 {
		t.Fatalf("durable retry retained cached grant: %v/%v, stats %+v, snapshots %d", ids, err, cache.Stats(), source.reads)
	}
	source.durableErr = errors.New("durable snapshot failed")
	ids, err = service.FilterAuthorized(t.Context(), alice, "view", "space", []string{"s1"})
	if ids != nil || !errors.Is(err, source.durableErr) || source.reads != 2 {
		t.Fatalf("failed retry leaked provisional IDs: %v/%v, snapshots %d", ids, err, source.reads)
	}
	// Ambient operations use the existing reader, even while Redis could grant
	// from the stale mirror and the standalone snapshot path would fail.
	backend.fail = false
	source.ambient = true
	before := cache.Stats()
	ids, err = service.FilterAuthorized(t.Context(), alice, "view", "space", []string{"s1"})
	if err != nil || ids == nil || len(ids) != 0 || source.reads != 2 || cache.Stats() != before {
		t.Fatalf("ambient filter used mirror or standalone snapshot: %v/%v, stats %+v -> %+v", ids, err, before, cache.Stats())
	}
}

func TestTupleCacheConstructionRequiresBoundRawSource(t *testing.T) {
	raw := &boundTupleRelationships{Relationships: memory.NewRelationships(), binding: "store"}
	parts, err := relationships.NewService(raw, relationships.Schema{ResourceTypes: map[string]relationships.ResourceTypeDef{
		"space": {Relations: map[string]relationships.RelationDef{"viewer": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}}}}, Permissions: map[string]relationships.PermissionRule{"view": relationships.AnyOf(relationships.Direct("viewer"))}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	roleReader, err := roles.NewService(memory.New().Roles())
	if err != nil {
		t.Fatal(err)
	}
	good := config{Readers: Readers{Relationships: parts.Service, TupleSource: &testTupleSource{binding: "store"}}, backend: memory.NewTupleCache(), tuplePolicy: tuplecache.Policy{MaxStaleness: time.Second}}
	for _, tc := range []struct {
		name   string
		mutate func(*config)
		want   error
	}{
		{"missing source", func(c *config) { c.TupleSource = nil }, sdk.ErrInvalidInput},
		{"typed nil source", func(c *config) { c.TupleSource = (*testTupleSource)(nil) }, sdk.ErrInvalidInput},
		{"typed nil backend", func(c *config) { c.backend = (*memory.TupleCache)(nil) }, sdk.ErrInvalidInput},
		{"no relationship reader", func(c *config) { c.Relationships = nil }, sdk.ErrInvalidInput},
		{"other source", func(c *config) { c.TupleSource = &testTupleSource{binding: "other"} }, tuplecache.ErrBinding},
		{"unbound role reader", func(c *config) { c.Roles = roleReader }, tuplecache.ErrBinding},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := good
			tc.mutate(&cfg)
			if _, err := newTupleCache(cfg); !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
	cache, err := newTupleCache(good)
	if err != nil || cache == nil {
		t.Fatalf("bound source: %v", err)
	}
	_ = cache.Close()
	good.backend = nil
	cache, err = newTupleCache(good)
	if err != nil || cache != nil {
		t.Fatalf("nil backend allocated runtime: %v", err)
	}
}

type invalidTupleReads struct{ tuplecache.Backend }

func (b invalidTupleReads) Read(context.Context, tuplecache.State, []tuplecache.SetKey) ([][]relationships.SubjectRef, error) {
	return nil, tuplecache.ErrUnavailable
}

func TestTupleCacheFailedDurableRetryDiscardsProvisionalResults(t *testing.T) {
	failure := errors.New("durable snapshot failed")
	source := &testTupleSource{binding: "store", durableErr: failure}
	cache, err := tuplecache.New(source, invalidTupleReads{Backend: memory.NewTupleCache()}, tuplecache.WithPolicy(tuplecache.Policy{MaxStaleness: time.Second}))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	if err := cache.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	service := &Service{tupleCache: cache}
	called := 0
	result, err := service.runTupleCache(t.Context(), func(context.Context, tuplecache.CheckReads) (operationResult, error) {
		called++
		return operationResult{result: authmodel.CheckResult{Allowed: true}, batch: []authmodel.CheckResult{{Allowed: true}}}, nil
	})
	if called != 1 || !errors.Is(err, failure) || result.result.Allowed || result.batch != nil {
		t.Fatalf("provisional result escaped failed retry: %+v/%v calls=%d", result, err, called)
	}
}
