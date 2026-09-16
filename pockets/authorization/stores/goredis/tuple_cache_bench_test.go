package goredis

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	tuplefacts "github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
)

// Each fixture has N documents granted to one principal: small forward sets and
// one hot reverse set. Setup, process launch and cleanup are outside timings.
func BenchmarkTupleCache(b *testing.B) {
	for _, size := range []int{100, 1000, 10000, 100000} {
		b.Run(fmt.Sprintf("tuples=%d", size), func(b *testing.B) {
			client, _ := startRedis(b, "", false)
			ctx := b.Context()
			rows := make([]tuplefacts.Tuple, size)
			for i := range rows {
				rows[i] = tuple("doc", fmt.Sprintf("%06d", i), "viewer", "user", "alice", "")
			}
			c := cacheWithLimits(b, client, Limits{MaxReadBytes: 16 << 20, MaxMutationBytes: 16 << 20})
			state := tuplecache.State{Binding: "benchmark", Receipt: "initial"}
			if err := c.Publish(ctx, tuplecache.State{}, state, tuplecache.Snapshot{Full: true, Tuples: rows}, time.Hour); err != nil {
				b.Fatal(err)
			}
			keys := []tuplecache.SetKey{reverse(rows[0])}
			bytes := client.HStrLen(ctx, c.key, setField(reverse(rows[0]))).Val()
			// Preload scripts to exclude EVALSHA's initial NOSCRIPT retry.
			if _, err := c.Read(ctx, state, keys); err != nil {
				b.Fatal(err)
			}
			b.Run("read_allowed", func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(bytes)
				for b.Loop() {
					sets, err := c.Read(ctx, state, keys)
					if err != nil || len(sets[0]) != size {
						b.Fatalf("read cardinality/error: %d/%v", len(sets), err)
					}
				}
			})
			b.Run("read_rejected", func(b *testing.B) {
				limited := cacheWithLimits(b, client, Limits{MaxReadBytes: min(int(bytes)-1, defaultMaxReadBytes)})
				b.ReportAllocs()
				for b.Loop() {
					if _, err := limited.Read(ctx, state, keys); !errors.Is(err, tuplecache.ErrCapacity) {
						b.Fatalf("capacity: %v", err)
					}
				}
			})
			b.Run("delta_rejected", func(b *testing.B) {
				limited := cacheWithLimits(b, client, Limits{MaxMutationBytes: int(bytes)})
				snapshot := tuplecache.Snapshot{Changes: []tuplecache.Change{{Before: &rows[0]}}}
				next := tuplecache.State{Binding: state.Binding, Receipt: "rejected"}
				b.ReportAllocs()
				for b.Loop() {
					if err := limited.Publish(ctx, state, next, snapshot, time.Hour); !errors.Is(err, tuplecache.ErrCapacity) {
						b.Fatalf("capacity: %v", err)
					}
				}
			})
			b.Run("delta_allowed", func(b *testing.B) {
				i := 0
				b.ReportAllocs()
				for b.Loop() {
					change := tuplecache.Change{Before: &rows[0]}
					if i%2 != 0 {
						change = tuplecache.Change{After: &rows[0]}
					}
					next := tuplecache.State{Binding: state.Binding, Receipt: fmt.Sprintf("delta-%d", i)}
					if err := c.Publish(ctx, state, next, tuplecache.Snapshot{Changes: []tuplecache.Change{change}}, time.Hour); err != nil {
						b.Fatal(err)
					}
					state = next
					i++
				}
			})
			b.Run("rebuild", func(b *testing.B) {
				i := 0
				b.ReportAllocs()
				for b.Loop() {
					next := tuplecache.State{Binding: state.Binding, Receipt: fmt.Sprintf("full-%d", i)}
					if err := c.Publish(ctx, state, next, tuplecache.Snapshot{Full: true, Tuples: rows}, time.Hour); err != nil {
						b.Fatal(err)
					}
					state = next
					i++
				}
			})
		})
	}
}
