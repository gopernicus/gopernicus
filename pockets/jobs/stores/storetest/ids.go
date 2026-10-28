package storetest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/jobs"
	job "github.com/gopernicus/gopernicus/pockets/jobs/logic/queue"
	schedule "github.com/gopernicus/gopernicus/pockets/jobs/logic/schedules"
	"github.com/gopernicus/gopernicus/sdk"
)

// countingPrefix prefixes every id the RunIDs counting generator mints.
const countingPrefix = "fixed-"

// RunIDs exercises id minting end to end: the jobs services (built by jobs.New
// over the repositories newRepos returns, with jobs.WithIDs) mint every job and
// schedule id, the repositories persist them verbatim and never mint, and an
// empty id is rejected with sdk.ErrInvalidInput (never sdk.ErrAlreadyExists).
// newRepos MUST return CLEAN, isolated repositories per call; Queue and
// Schedules are required, and FencedQueue is exercised when set.
//
// The generator counts (fixed-1, fixed-2, …): a constant one would collide on
// the second insert and prove nothing about which layer minted.
func RunIDs(t *testing.T, newRepos func(t *testing.T) jobs.Repositories) {
	t.Helper()

	t.Run("WithIDsReachesJobIDs", func(t *testing.T) { testIDsJobs(t, newRepos(t)) })
	t.Run("WithIDsReachesFencedJobIDs", func(t *testing.T) { testIDsFenced(t, newRepos(t)) })
	t.Run("WithIDsReachesScheduleIDs", func(t *testing.T) { testIDsSchedules(t, newRepos(t)) })
	t.Run("OccurrenceIDIgnoresWithIDs", func(t *testing.T) { testIDsOccurrence(t, newRepos(t)) })
	t.Run("EmptyIDRejected", func(t *testing.T) { testIDsEmptyRejected(t, newRepos(t)) })
	t.Run("EmptyMintRejected", func(t *testing.T) { testIDsEmptyMint(t, newRepos(t)) })
}

// countingIDs returns a generator minting fixed-1, fixed-2, … and its counter.
func countingIDs() (sdk.IDGenerator, *atomic.Int64) {
	var n atomic.Int64
	return sdk.NewIDGenerator(func() (string, error) {
		return fmt.Sprintf("%s%d", countingPrefix, n.Add(1)), nil
	}), &n
}

func newCounted(t *testing.T, repos jobs.Repositories) (*jobs.Components, *atomic.Int64) {
	t.Helper()
	ids, n := countingIDs()
	c, err := jobs.New(repos, jobs.WithIDs(ids))
	if err != nil {
		t.Fatalf("jobs.New: %v", err)
	}
	return c, n
}

func testIDsJobs(t *testing.T, repos jobs.Repositories) {
	ctx := context.Background()
	c, _ := newCounted(t, repos)

	a, err := c.Queue.EnqueueJob(ctx, job.Enqueue{Kind: "demo"})
	if err != nil {
		t.Fatalf("EnqueueJob a: %v", err)
	}
	b, err := c.Queue.EnqueueJob(ctx, job.Enqueue{Kind: "demo"})
	if err != nil {
		t.Fatalf("EnqueueJob b: %v", err)
	}
	if !strings.HasPrefix(a.ID(), countingPrefix) || !strings.HasPrefix(b.ID(), countingPrefix) || a.ID() == b.ID() {
		t.Fatalf("minted job ids = %q, %q; want distinct %q-prefixed ids", a.ID(), b.ID(), countingPrefix)
	}
	got, err := repos.Queue.Get(ctx, a.ID())
	if err != nil || got.ID() != a.ID() {
		t.Fatalf("Get(%q) = %q, %v; want the minted id stored verbatim", a.ID(), got.ID(), err)
	}

	id, err := c.Queue.Enqueue(ctx, "demo", nil)
	if err != nil || !strings.HasPrefix(id, countingPrefix) {
		t.Fatalf("Enqueue = %q, %v; want a %q-prefixed id", id, err, countingPrefix)
	}

	explicit, err := c.Queue.EnqueueJob(ctx, job.Enqueue{ID: "caller-key", Kind: "demo"})
	if err != nil || explicit.ID() != "caller-key" {
		t.Fatalf("explicit EnqueueJob = %q, %v; want the caller's id kept", explicit.ID(), err)
	}
}

func testIDsFenced(t *testing.T, repos jobs.Repositories) {
	if repos.FencedQueue == nil {
		t.Skip("no FencedQueue wired")
	}
	ctx := context.Background()
	c, _ := newCounted(t, repos)

	first, err := c.Queue.EnqueueOnce(ctx, "email", "k1", []byte(`{}`))
	if err != nil || !strings.HasPrefix(first, countingPrefix) {
		t.Fatalf("EnqueueOnce = %q, %v; want a %q-prefixed id", first, err, countingPrefix)
	}
	again, err := c.Queue.EnqueueOnce(ctx, "email", "k1", []byte(`{}`))
	if err != nil || again != first {
		t.Fatalf("idempotent EnqueueOnce = %q, %v; want the active %q", again, err, first)
	}
	replaced, err := c.Queue.Replace(ctx, "email", "k1", []byte(`{}`))
	if err != nil || !strings.HasPrefix(replaced, countingPrefix) || replaced == first {
		t.Fatalf("Replace = %q, %v; want a fresh %q-prefixed id", replaced, err, countingPrefix)
	}
	for _, id := range []string{first, replaced} {
		if got, err := repos.FencedQueue.Get(ctx, id); err != nil || got.JobID != id {
			t.Fatalf("Get(%q) = %q, %v; want the minted id stored verbatim", id, got.JobID, err)
		}
	}
}

func testIDsSchedules(t *testing.T, repos jobs.Repositories) {
	ctx := context.Background()
	c, _ := newCounted(t, repos)
	if c.Schedules == nil {
		t.Fatal("RunIDs needs Repositories.Queue and Repositories.Schedules")
	}

	created, err := c.Schedules.EnsureSchedule(ctx, schedule.Ensure{Name: "nightly", Kind: "demo", Spec: schedule.Spec{Every: time.Hour}})
	if err != nil {
		t.Fatalf("EnsureSchedule create: %v", err)
	}
	if !strings.HasPrefix(created.ID, countingPrefix) {
		t.Fatalf("schedule id = %q, want a %q-prefixed id", created.ID, countingPrefix)
	}
	updated, err := c.Schedules.EnsureSchedule(ctx, schedule.Ensure{Name: "nightly", Kind: "demo.other", Spec: schedule.Spec{Every: time.Hour}})
	if err != nil {
		t.Fatalf("EnsureSchedule update: %v", err)
	}
	if updated.ID != created.ID {
		t.Fatalf("update changed schedule id from %q to %q; the candidate is create-only", created.ID, updated.ID)
	}
	if got, err := repos.Schedules.Get(ctx, created.ID); err != nil || got.ID != created.ID {
		t.Fatalf("Get(%q) = %q, %v; want the minted id stored verbatim", created.ID, got.ID, err)
	}
}

func testIDsOccurrence(t *testing.T, repos jobs.Repositories) {
	ctx := context.Background()
	c, n := newCounted(t, repos)
	if c.Schedules == nil {
		t.Fatal("RunIDs needs Repositories.Queue and Repositories.Schedules")
	}

	slot := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	if _, err := repos.Schedules.Ensure(ctx, schedule.Ensure{ID: "occ-sched", Name: "occ", Kind: "demo", Spec: schedule.Spec{Every: time.Hour}}, slot); err != nil {
		t.Fatalf("seed Ensure: %v", err)
	}
	before := n.Load()
	if err := c.Schedules.WorkFunc(nil)(ctx); err != nil {
		t.Fatalf("WorkFunc: %v", err)
	}
	if after := n.Load(); after != before {
		t.Errorf("firing an occurrence consumed %d generator ids, want 0", after-before)
	}
	want := schedule.OccurrenceID("occ-sched", slot)
	got, err := repos.Queue.Get(ctx, want)
	if err != nil {
		t.Fatalf("Get(%q): %v; want the occurrence enqueued under its deterministic id", want, err)
	}
	if strings.HasPrefix(got.ID(), countingPrefix) {
		t.Errorf("occurrence job id %q came from WithIDs", got.ID())
	}
}

func testIDsEmptyRejected(t *testing.T, repos jobs.Repositories) {
	ctx := context.Background()

	for i := range 2 {
		if _, err := repos.Queue.Enqueue(ctx, job.Enqueue{Kind: "demo", Payload: []byte(`{}`)}); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("empty-id Enqueue #%d: err=%v, want ErrInvalidInput", i+1, err)
		}
	}
	for i := range 2 {
		if _, err := repos.Schedules.Ensure(ctx, schedule.Ensure{Name: "empty", Kind: "demo", Spec: schedule.Spec{Every: time.Hour}}, suiteBase); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("empty-id Ensure #%d: err=%v, want ErrInvalidInput", i+1, err)
		}
	}
	if repos.FencedQueue == nil {
		return
	}
	for i := range 2 {
		if _, err := repos.FencedQueue.EnqueueOnce(ctx, job.Enqueue{Kind: "email", LogicalKey: "k-empty"}); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("empty-id EnqueueOnce #%d: err=%v, want ErrInvalidInput", i+1, err)
		}
		if _, err := repos.FencedQueue.Replace(ctx, job.Enqueue{Kind: "email", LogicalKey: "k-empty"}); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("empty-id Replace #%d: err=%v, want ErrInvalidInput", i+1, err)
		}
	}
}

// testIDsEmptyMint: a generator that yields "" (sdk.DatabaseID) fails loudly at
// the store boundary instead of inserting an empty id that turns the next
// admission into a false sdk.ErrAlreadyExists.
func testIDsEmptyMint(t *testing.T, repos jobs.Repositories) {
	ctx := context.Background()
	c, err := jobs.New(repos, jobs.WithIDs(sdk.NewIDGenerator(sdk.DatabaseID)))
	if err != nil {
		t.Fatalf("jobs.New: %v", err)
	}
	for i := range 2 {
		if _, err := c.Queue.EnqueueJob(ctx, job.Enqueue{Kind: "demo"}); !errors.Is(err, sdk.ErrInvalidInput) || errors.Is(err, sdk.ErrAlreadyExists) {
			t.Errorf("empty-mint EnqueueJob #%d: err=%v, want ErrInvalidInput", i+1, err)
		}
		if _, err := c.Schedules.EnsureSchedule(ctx, schedule.Ensure{Name: "empty", Kind: "demo", Spec: schedule.Spec{Every: time.Hour}}); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("empty-mint EnsureSchedule #%d: err=%v, want ErrInvalidInput", i+1, err)
		}
		if repos.FencedQueue != nil {
			if _, err := c.Queue.EnqueueOnce(ctx, "email", "k-empty", nil); !errors.Is(err, sdk.ErrInvalidInput) || errors.Is(err, sdk.ErrAlreadyExists) {
				t.Errorf("empty-mint EnqueueOnce #%d: err=%v, want ErrInvalidInput", i+1, err)
			}
		}
	}
}
