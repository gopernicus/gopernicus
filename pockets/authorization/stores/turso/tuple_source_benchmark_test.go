package turso

import (
	"fmt"
	"testing"
)

// BenchmarkTupleSourceBacklog uses a disposable local SQLite WAL database.
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
					if _, err := db.Exec(b.Context(), `INSERT INTO iam_tuples VALUES (2,'document','current','viewer','user','alice','')`); err != nil {
						b.Fatal(err)
					}
					initial, err := source.Snapshot(b.Context(), "")
					if err != nil {
						b.Fatal(err)
					}
					if err := source.Acknowledge(b.Context(), initial.Receipt, "initial", []string{initial.Changes[0].ID}); err != nil {
						b.Fatal(err)
					}
					if _, err := db.Exec(b.Context(), `WITH RECURSIVE ids(i) AS (SELECT 1 WHERE ? > 0 UNION ALL SELECT i+1 FROM ids WHERE i < ?)
INSERT INTO iam_tuples SELECT 2,'document','obsolete-'||i,'viewer','user','alice','' FROM ids`, count, count); err != nil {
						b.Fatal(err)
					}
					if _, err := db.Exec(b.Context(), "DELETE FROM iam_tuples WHERE resource_id <> 'current'"); err != nil {
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
