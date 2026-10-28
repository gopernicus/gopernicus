package jobs_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/jobs"
	"github.com/gopernicus/gopernicus/pockets/jobs/logic/queue"
	"github.com/gopernicus/gopernicus/pockets/jobs/logic/schedules"
	"github.com/gopernicus/gopernicus/pockets/jobs/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
)

func memoryRepos() jobs.Repositories {
	return jobs.Repositories{Queue: memory.NewQueue(), FencedQueue: memory.NewFencedQueue(), Schedules: memory.NewSchedules()}
}

// mintAll drives the four core minting paths once each and returns the ids in order.
func mintAll(t *testing.T, c *jobs.Components) []string {
	t.Helper()
	ctx := context.Background()
	enqueued, err := c.Queue.EnqueueJob(ctx, queue.Enqueue{Kind: "demo"})
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	once, err := c.Queue.EnqueueOnceIn(ctx, queue.EnqueueOnceInput{Kind: "email", LogicalKey: "k1"})
	if err != nil {
		t.Fatalf("EnqueueOnceIn: %v", err)
	}
	replaced, err := c.Queue.ReplaceIn(ctx, queue.ReplaceInput{Kind: "email", LogicalKey: "k1"})
	if err != nil {
		t.Fatalf("ReplaceIn: %v", err)
	}
	sch, err := c.Schedules.EnsureSchedule(ctx, schedules.Ensure{Name: "nightly", Kind: "demo", Spec: schedules.Spec{Every: time.Hour}})
	if err != nil {
		t.Fatalf("EnsureSchedule: %v", err)
	}
	return []string{enqueued.ID(), once, replaced, sch.ID}
}

func TestWithIDsReachesEveryJobsMint(t *testing.T) {
	var n int
	ids := sdk.NewIDGenerator(func() (string, error) {
		n++
		return fmt.Sprintf("fixed-%d", n), nil
	})
	c, err := jobs.New(memoryRepos(), jobs.WithIDs(ids))
	if err != nil {
		t.Fatal(err)
	}
	got := mintAll(t, c)
	want := []string{"fixed-1", "fixed-2", "fixed-3", "fixed-4"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("minted ids = %v, want %v", got, want)
		}
	}
}

func TestDefaultJobsIDsAreUUIDv7(t *testing.T) {
	c, err := jobs.New(memoryRepos())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range mintAll(t, c) {
		if len(id) != 36 || id[14] != '7' || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
			t.Errorf("default id %q is not a UUIDv7", id)
		}
		switch id[19] {
		case '8', '9', 'a', 'b':
		default:
			t.Errorf("default id %q has a non-RFC 9562 variant", id)
		}
	}
}
