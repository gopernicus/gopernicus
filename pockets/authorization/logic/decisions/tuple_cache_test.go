package decisions

import (
	"context"
	"errors"
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
}

func (s *testTupleSource) Binding() string                       { return s.binding }
func (s *testTupleSource) CacheableContext(context.Context) bool { return true }
func (s *testTupleSource) Snapshot(context.Context, string) (tuplecache.Snapshot, error) {
	return tuplecache.Snapshot{Full: true}, nil
}
func (s *testTupleSource) Acknowledge(context.Context, string, string, []string) error { return nil }
func (s *testTupleSource) ReadSnapshot(context.Context, func(context.Context, tuplecache.CheckReads) error) error {
	return s.durableErr
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
