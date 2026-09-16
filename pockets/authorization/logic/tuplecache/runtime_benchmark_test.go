package tuplecache_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
)

// These isolate runtime/evaluator cost using the reference backend. The overlap
// case deliberately publishes an unrelated change between the cached read and
// final validation on every operation; it is not a production traffic model.
func BenchmarkTupleCachePublicationOverlap(b *testing.B) {
	for _, mode := range []string{"warm", "idle_poll_during_read", "unrelated_publication_during_read", "capacity_fallback"} {
		b.Run(mode, func(b *testing.B) {
			backend := &hookedBackend{Backend: memory.NewTupleCache()}
			capacity := &capacityBackend{Backend: backend, maxChanges: 1}
			cache, source := fixture(b, []relationships.CreateRelationship{grant}, capacity)
			if err := cache.Poll(b.Context()); err != nil {
				b.Fatal(err)
			}
			if mode == "capacity_fallback" {
				capacity.readError = tuplecache.ErrCapacity
			}
			unrelated := relationships.CreateRelationship{ResourceType: "space", ResourceID: "other", Relation: "viewer", SubjectType: "user", SubjectID: "bob"}
			present := false
			before := cache.Stats()
			b.ReportAllocs()
			for b.Loop() {
				switch mode {
				case "idle_poll_during_read":
					backend.afterRead = func() {
						if err := cache.Poll(b.Context()); err != nil {
							b.Fatal(err)
						}
					}
				case "unrelated_publication_during_read":
					backend.afterRead = func() {
						change := tuplecache.Change{ID: "unrelated"}
						if present {
							change.Before = &unrelated
							source.tuples = source.tuples[:1]
						} else {
							change.After = &unrelated
							source.tuples = append(source.tuples, unrelated)
						}
						present = !present
						source.pending = []tuplecache.Change{change}
						if err := cache.Poll(b.Context()); err != nil {
							b.Fatal(err)
						}
					}
				}
				err := cache.Run(b.Context(), func(ctx context.Context, reads tuplecache.CheckReads) error {
					allowed, err := reads.ForChecks(directModel).CheckRelationWithGroupExpansion(ctx, "space", "s", "viewer", "user", "alice", 100)
					if err == nil && !allowed {
						return fmt.Errorf("unchanged grant was denied")
					}
					return err
				})
				if err != nil {
					b.Fatal(err)
				}
			}
			after := cache.Stats()
			b.ReportMetric(float64(after.Hits-before.Hits)/float64(b.N), "cache_hits/op")
			b.ReportMetric(float64(after.Fallbacks-before.Fallbacks)/float64(b.N), "fallbacks/op")
			b.ReportMetric(float64(after.Publications-before.Publications)/float64(b.N), "publications/op")
		})
	}
}
