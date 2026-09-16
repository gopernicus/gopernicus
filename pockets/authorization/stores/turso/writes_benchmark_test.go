package turso

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
)

// Distinct tenant IDs remove row contention; SQLite's write intent still
// serializes the guarded read and actual alternating grant/revoke on every call.
func BenchmarkGuardedWriterContentionSQLite(b *testing.B) {
	db, _ := cacheFixture(b, false, 8)
	repos, err := Repositories(b.Context(), db)
	if err != nil {
		b.Fatal(err)
	}
	for _, workers := range []int{1, 8} {
		b.Run(fmt.Sprintf("tenants=%d", workers), func(b *testing.B) {
			for i := range workers {
				if err := repos.Relationships.DeleteResourceRelationships(b.Context(), "tenant", fmt.Sprintf("tenant-%d", i)); err != nil {
					b.Fatal(err)
				}
			}
			var next atomic.Int64
			var wg sync.WaitGroup
			errors := make(chan error, workers)
			b.ReportAllocs()
			b.ResetTimer()
			for i := range workers {
				wg.Add(1)
				go func() {
					defer wg.Done()
					cmd := mutations.Command{Target: mutations.Target{Kind: mutations.TargetResource, Type: "tenant", ID: fmt.Sprintf("tenant-%d", i)}, Operation: mutations.OpGrant, Relationships: []mutations.RelationshipRow{{Relation: "viewer", Subject: relationships.SubjectRef{Type: "user", ID: "alice"}}}}
					guard := func(ctx context.Context, view mutations.StoreDecisionView) error {
						_, err := view.CheckRelation(ctx, cmd.Target, "viewer", "user", "alice")
						return err
					}
					for next.Add(1) <= int64(b.N) {
						if _, err := repos.Mutations.ApplyGuarded(b.Context(), cmd, guard, nil); err != nil {
							errors <- err
							return
						}
						if cmd.Operation == mutations.OpGrant {
							cmd.Operation = mutations.OpRevoke
						} else {
							cmd.Operation = mutations.OpGrant
						}
					}
				}()
			}
			wg.Wait()
			b.StopTimer()
			close(errors)
			for err := range errors {
				b.Fatal(err)
			}
		})
	}
}
