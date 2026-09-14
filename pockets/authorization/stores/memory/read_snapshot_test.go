package memory

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
)

func TestCacheGenerationAtomicPublication(t *testing.T) {
	ctx := context.Background()
	store := New(WithCacheReads())
	source := store.CacheSource()
	tuple := relationships.CreateRelationship{ResourceType: "document", ResourceID: "d1", Relation: "viewer", SubjectType: "user", SubjectID: "u1"}
	version := func() decisions.CacheVersion {
		t.Helper()
		v, err := source.Observe(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	initial := version()
	if err := store.Relationships().CreateRelationships(ctx, []relationships.CreateRelationship{tuple}); err != nil {
		t.Fatal(err)
	}
	first := version()
	if first.Generation != initial.Generation+1 {
		t.Fatalf("generation=%+v", first)
	}
	if err := store.Relationships().CreateRelationships(ctx, []relationships.CreateRelationship{tuple}); err != nil {
		t.Fatal(err)
	}
	if got := version(); got != first {
		t.Fatalf("duplicate changed generation: %+v", got)
	}
	failure := errors.New("rollback")
	if err := store.rel.st.write(ctx, func(next *state) error { next.rel = nil; return failure }); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if got := version(); got != first {
		t.Fatalf("rollback advanced generation: %+v", got)
	}
	canceled, cancel := context.WithCancel(ctx)
	if err := store.rel.st.write(canceled, func(next *state) error { next.rel = nil; cancel(); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if got := version(); got != first {
		t.Fatalf("cancellation advanced generation: %+v", got)
	}
	store.rel.st.mu.Lock()
	store.rel.st.cacheGeneration = math.MaxInt64
	store.rel.st.mu.Unlock()
	if err := store.Roles().Assign(ctx, roles.Assignment{SubjectType: "user", SubjectID: "u1", Role: "viewer"}); !errors.Is(err, decisions.ErrCacheVersion) {
		t.Fatalf("overflow=%v", err)
	}
	held, err := store.Roles().HasExactRole(ctx, "user", "u1", "viewer", "", "")
	if err != nil || held {
		t.Fatalf("overflow published fact: %v/%v", held, err)
	}
}

func TestCacheSourceOptionalAndIncarnation(t *testing.T) {
	plain := New()
	if plain.CacheSource() != nil || plain.Relationships().CacheBinding() != "" || plain.Roles().CacheBinding() != "" {
		t.Fatal("ordinary memory store enabled protocol")
	}
	if NewRelationships().CacheBinding() != "" || NewRoles().CacheBinding() != "" {
		t.Fatal("standalone stores claim combined binding")
	}
	a, b := New(WithCacheReads()), New(WithCacheReads())
	if a.CacheSource().CacheBinding() == b.CacheSource().CacheBinding() {
		t.Fatal("independent authorities share binding")
	}
}

func TestSnapshotRetainsCallbackCancellation(t *testing.T) {
	store := New(WithCacheReads())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := store.CacheSource().ReadSnapshot(ctx, func(_ context.Context, _ decisions.CacheVersion, reads decisions.CheckReads) error {
		scoped := reads.ForChecks(relationships.ReadModel{})
		cancel()
		fresh := context.Background()
		if _, err := reads.HasExactRole(fresh, "user", "u1", "viewer", "", ""); !errors.Is(err, context.Canceled) {
			t.Fatalf("role forgot snapshot context: %v", err)
		}
		if _, err := scoped.CheckRelationWithGroupExpansion(fresh, "document", "d1", "viewer", "user", "u1", 100); !errors.Is(err, context.Canceled) {
			t.Fatalf("relationship forgot snapshot context: %v", err)
		}
		if _, err := scoped.GetRelationTargets(fresh, "document", "d1", "viewer"); !errors.Is(err, context.Canceled) {
			t.Fatalf("targets forgot snapshot context: %v", err)
		}
		if _, err := scoped.CheckBatchDirect(fresh, "document", []string{"d1"}, "viewer", "user", "u1", 100); !errors.Is(err, context.Canceled) {
			t.Fatalf("batch forgot snapshot context: %v", err)
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled snapshot returned %v", err)
	}
}
