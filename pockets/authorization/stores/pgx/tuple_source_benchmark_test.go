package pgx

import (
	"fmt"
	"testing"
)

// BenchmarkTupleSourceBacklog needs POSTGRES_TEST_DSN for a disposable database.
// Pending events come from real insert/delete triggers; one current tuple remains.
func BenchmarkTupleSourceBacklog(b *testing.B) {
	for _, count := range []int{0, 1000, 10000} {
		b.Run(fmt.Sprintf("transient-tuples=%d", count), func(b *testing.B) {
			for _, full := range []bool{false, true} {
				b.Run(fmt.Sprintf("full=%t", full), func(b *testing.B) {
					db, cfg := cacheFixture(b, true)
					repos, err := testRepositories(b.Context(), db, cacheOptions(cfg)...)
					if err != nil {
						b.Fatal(err)
					}
					source := repos.TupleSource
					table := cacheTable(cfg, "iam_tuples")
					if _, err := db.Exec(b.Context(), tupleInsert(table, "current")); err != nil {
						b.Fatal(err)
					}
					initial, err := source.Snapshot(b.Context(), "")
					if err != nil {
						b.Fatal(err)
					}
					if err := source.Acknowledge(b.Context(), initial.Receipt, "initial", []string{initial.Changes[0].ID}); err != nil {
						b.Fatal(err)
					}
					if _, err := db.Exec(b.Context(), "INSERT INTO "+table+" SELECT 2,'document','obsolete-'||i,'viewer','user','alice','' FROM generate_series(1,$1::integer) i", count); err != nil {
						b.Fatal(err)
					}
					if _, err := db.Exec(b.Context(), "DELETE FROM "+table+" WHERE resource_id <> 'current'"); err != nil {
						b.Fatal(err)
					}
					receipt := "initial"
					if full {
						receipt = ""
					}
					b.ReportAllocs()
					for b.Loop() {
						snapshot, err := source.Snapshot(b.Context(), receipt)
						if err != nil {
							b.Fatal(err)
						}
						if snapshot.Full != full || len(snapshot.Changes) != 2*count || full && len(snapshot.Tuples) != 1 {
							b.Fatalf("unexpected snapshot: full=%t, changes=%d, tuples=%d", snapshot.Full, len(snapshot.Changes), len(snapshot.Tuples))
						}
					}
					b.ReportMetric(float64(2*count), "events/op")
				})
			}
		})
	}
}
