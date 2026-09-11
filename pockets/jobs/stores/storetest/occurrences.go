package storetest

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	schedule "github.com/gopernicus/gopernicus/pockets/jobs/logic/schedules"
)

// RunOccurrences checks atomic admission and immutable, retryable dispatch.
func RunOccurrences(t *testing.T, fresh func(*testing.T) schedule.Repository) {
	t.Run("AttemptRotation", func(t *testing.T) {
		repo := fresh(t)
		ctx := context.Background()
		slot := suiteBase
		var ids []string
		for _, name := range []string{"a", "b", "c"} {
			sch := mustEnsure(t, repo, schedule.Ensure{Name: name, Kind: "owned", Spec: schedule.Spec{Every: time.Hour}}, slot)
			if won, err := repo.ClaimDue(ctx, sch, slot.Add(time.Hour), slot); err != nil || !won {
				t.Fatalf("claim %v %v", won, err)
			}
			ids = append(ids, schedule.OccurrenceID(sch.ID, slot))
		}
		seen := map[string]bool{}
		for range 3 {
			pending, err := repo.ListPending(ctx, 1, []string{"owned"})
			if err != nil || len(pending) != 1 {
				t.Fatalf("pending %v %v", pending, err)
			}
			item := pending[0]
			if seen[item.JobID] {
				t.Fatal("attempted same row while another remained unattempted")
			}
			seen[item.JobID] = true
			if found, err := repo.RecordAttempt(ctx, item.JobID); err != nil || !found {
				t.Fatalf("record %v %v", found, err)
			}
		}
		pending, err := repo.ListPending(ctx, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range pending {
			if item.Attempts != 1 {
				t.Fatalf("persisted attempts=%d", item.Attempts)
			}
		}
		if err := repo.AckOccurrence(ctx, ids[0], slot); err != nil {
			t.Fatal(err)
		}
		if found, err := repo.RecordAttempt(ctx, ids[0]); err != nil || found {
			t.Fatalf("stale attempt admitted: %v %v", found, err)
		}
	})
	t.Run("ClaimSnapshotAndRecovery", func(t *testing.T) {
		repo := fresh(t)
		ctx := context.Background()
		slot := suiteBase.Add(123 * time.Microsecond)
		original := schedule.Ensure{Name: "snapshot", Kind: "a", TenantID: "old", Spec: schedule.Spec{Every: time.Hour}, Payload: []byte(`{"v":1}`)}
		sch := mustEnsure(t, repo, original, slot)
		original.TenantID = "new"
		original.Payload = []byte(`{"v":2}`)
		mustEnsure(t, repo, original, slot)
		won, err := repo.ClaimDue(ctx, sch, slot.Add(time.Hour), slot)
		if err != nil || !won {
			t.Fatalf("claim: %v, %v", won, err)
		}
		pending, err := repo.ListPending(ctx, 1, []string{"a"})
		if err != nil || len(pending) != 1 {
			t.Fatalf("pending: %v, %v", pending, err)
		}
		first := pending[0]
		if first.TenantID != "new" || string(first.Payload) != `{"v":2}` {
			t.Fatalf("claim must snapshot current template: %+v", first)
		}
		pending[0].Payload[5] = '9'
		again, _ := repo.ListPending(ctx, 1, []string{"a"})
		if string(again[0].Payload) != `{"v":2}` {
			t.Fatal("pending payload aliases store")
		}
		if other, _ := repo.ListPending(ctx, 1, []string{"b"}); len(other) != 0 {
			t.Fatal("pending filter leaked kind")
		}
		current, _ := repo.Get(ctx, sch.ID)
		if won, _ := repo.ClaimDue(ctx, current, slot.Add(2*time.Hour), slot.Add(time.Hour)); won {
			t.Fatal("second occurrence admitted before acknowledgement")
		}
		original.Kind = "b"
		original.Payload = []byte(`{"v":3}`)
		mustEnsure(t, repo, original, slot)
		if err := repo.SetEnabled(ctx, sch.ID, false, slot); err != nil {
			t.Fatal(err)
		}
		if err := repo.Delete(ctx, sch.ID); err != nil {
			t.Fatal(err)
		}
		after, _ := repo.ListPending(ctx, 1, []string{"a"})
		if len(after) != 1 || after[0].JobID != first.JobID || string(after[0].Payload) != `{"v":2}` {
			t.Fatalf("admitted work lost/rewritten: %+v", after)
		}
		if err := repo.AckOccurrence(ctx, first.JobID, slot); err != nil {
			t.Fatal(err)
		}
		if err := repo.AckOccurrence(ctx, first.JobID, slot); err != nil {
			t.Fatal("repeated ack", err)
		}
		if after, _ := repo.ListPending(ctx, 0, nil); len(after) != 0 {
			t.Fatal("ack left pending row")
		}
	})
	t.Run("MetadataAndStaleAck", func(t *testing.T) {
		repo := fresh(t)
		ctx := context.Background()
		slot := suiteBase
		sch := mustEnsure(t, repo, schedule.Ensure{Name: "metadata", Kind: "a", Spec: schedule.Spec{Every: time.Second}}, slot)
		if won, err := repo.ClaimDue(ctx, sch, slot.Add(time.Second), slot); err != nil || !won {
			t.Fatalf("claim: %v %v", won, err)
		}
		id1 := schedule.OccurrenceID(sch.ID, slot)
		if err := repo.AckOccurrence(ctx, id1, slot); err != nil {
			t.Fatal(err)
		}
		current, _ := repo.Get(ctx, sch.ID)
		if current.LastJobID != id1 || current.LastRunAt == nil || !current.LastRunAt.Equal(slot) {
			t.Fatalf("metadata: %+v", current)
		}
		if current.UpdatedAt.Before(sch.UpdatedAt) {
			t.Fatal("ack rewound UpdatedAt")
		}
		if won, err := repo.ClaimDue(ctx, current, slot.Add(2*time.Second), slot.Add(time.Second)); err != nil || !won {
			t.Fatalf("second claim: %v %v", won, err)
		}
		id2 := schedule.OccurrenceID(sch.ID, slot.Add(time.Second))
		if err := repo.AckOccurrence(ctx, id2, slot.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		if err := repo.AckOccurrence(ctx, id1, slot.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
		current, _ = repo.Get(ctx, sch.ID)
		if current.LastJobID != id2 || !current.LastRunAt.Equal(slot.Add(time.Second)) {
			t.Fatalf("stale ack rewrote metadata: %+v", current)
		}
	})
	t.Run("SpecGuard", func(t *testing.T) {
		repo := fresh(t)
		ctx := context.Background()
		slot := suiteBase
		sch := mustEnsure(t, repo, schedule.Ensure{Name: "spec", Kind: "a", Spec: schedule.Spec{Every: time.Hour}}, slot)
		mustEnsure(t, repo, schedule.Ensure{Name: "spec", Kind: "a", Spec: schedule.Spec{Every: 2 * time.Hour}}, slot)
		if won, err := repo.ClaimDue(ctx, sch, slot.Add(time.Hour), slot); err != nil || won {
			t.Fatalf("stale spec won: %v %v", won, err)
		}
	})
	t.Run("ConcurrentEnsureAndClaim", func(t *testing.T) {
		repo := fresh(t)
		ctx := context.Background()
		slot := suiteBase
		in := schedule.Ensure{Name: "concurrent", Kind: "a", Spec: schedule.Spec{Every: time.Hour}}
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := repo.Ensure(ctx, in, slot); err != nil {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
		due, err := repo.ListDue(ctx, slot, 0, nil)
		if err != nil || len(due) != 1 {
			t.Fatalf("concurrent Ensure: %v %v", due, err)
		}
		sch := due[0]
		var wins atomic.Int32
		for range 8 {
			wg.Add(2)
			go func() {
				defer wg.Done()
				if _, err := repo.Ensure(ctx, in, slot); err != nil {
					t.Error(err)
				}
			}()
			go func() {
				defer wg.Done()
				won, err := repo.ClaimDue(ctx, sch, slot.Add(time.Hour), slot)
				if err != nil {
					t.Error(err)
				}
				if won {
					wins.Add(1)
				}
			}()
		}
		wg.Wait()
		if wins.Load() != 1 {
			t.Fatalf("wins=%d", wins.Load())
		}
		current, _ := repo.Get(ctx, sch.ID)
		if !current.NextRunAt.Equal(slot.Add(time.Hour)) {
			t.Fatal("Ensure rewound claimed slot")
		}
		pending, err := repo.ListPending(ctx, 0, nil)
		if err != nil || len(pending) != 1 {
			t.Fatalf("pending: %v %v", pending, err)
		}
	})
}
