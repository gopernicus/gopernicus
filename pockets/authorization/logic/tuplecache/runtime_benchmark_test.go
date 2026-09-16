package tuplecache_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	tuplefacts "github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
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
			cache, source := fixture(b, []tuplefacts.Tuple{grant}, capacity, tuplecache.WithPolicy(tuplecache.Policy{MaxStaleness: time.Hour}))
			if err := cache.Poll(b.Context()); err != nil {
				b.Fatal(err)
			}
			if mode == "capacity_fallback" {
				capacity.readError = tuplecache.ErrCapacity
			}
			unrelated := tupleFact("space", "other", "viewer", "user", "bob", "")
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
					allowed, err := graph(reads, directModel).CheckRelationWithGroupExpansion(ctx, "space", "s", "viewer", "user", "alice", 100)
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

func BenchmarkCanonicalRoleReads(b *testing.B) {
	for _, size := range []int{1, 16, 128} {
		for _, posture := range []string{"durable", "cold", "warm"} {
			b.Run(fmt.Sprintf("roles=%d/%s", size, posture), func(b *testing.B) {
				facts := make([]tuplefacts.Tuple, size)
				for i := range facts {
					facts[i] = tuplefacts.Tuple{Scope: tuplefacts.Global(), Relation: fmt.Sprintf("role-%03d", i), Subject: grant.Subject}
					if i%2 != 0 {
						facts[i].Scope = grant.Scope
					}
				}
				cache, source := fixture(b, facts, memory.NewTupleCache(), tuplecache.WithPolicy(tuplecache.Policy{MaxStaleness: time.Hour}))
				var snapshots tuplefacts.Snapshotter = cache
				if posture == "durable" {
					snapshots = source.store.Tuples()
				}
				if posture == "warm" {
					if err := cache.Poll(b.Context()); err != nil {
						b.Fatal(err)
					}
				}
				b.ReportAllocs()
				for b.Loop() {
					if err := snapshots.ReadTupleSnapshot(b.Context(), func(ctx context.Context, reader tuplefacts.Reader) error {
						got, err := reader.ContainsMany(ctx, facts)
						if err != nil {
							return err
						}
						if len(got) != size {
							return fmt.Errorf("incomplete role batch")
						}
						for _, held := range got {
							if !held {
								return fmt.Errorf("missing exact role")
							}
						}
						return nil
					}); err != nil {
						b.Fatal(err)
					}
				}
				if posture == "warm" && (cache.Stats().Hits != uint64(b.N) || source.durable != 0) {
					b.Fatalf("warm benchmark lost readiness: hits=%d durable=%d iterations=%d", cache.Stats().Hits, source.durable, b.N)
				}
				b.ReportMetric(float64(cache.Stats().Hits)/float64(b.N), "cache_hits/op")
				b.ReportMetric(float64(source.durable)/float64(b.N), "fallbacks/op")
			})
		}
	}
}
