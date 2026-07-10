// Package storetest is the exported conformance suite for the jobs feature's two
// outbound ports: RunQueue exercises a job.QueueRepository and RunSchedules a
// schedule.Repository. Every store that fills them — the in-core memstore, the
// dialect adapters (features/jobs/stores/turso, .../postgres) — runs the same
// suite, so the port doc comments have one executable definition.
//
// The port doc comments are the spec; this suite is their executable form. It
// imports stdlib + sdk + the jobs feature's own packages only (guard G2 forbids
// a driver import here), so features/jobs's own `go test ./...` runs it against
// the memstore reference (see reference_test.go).
//
// Honesty note (design §6.5): against the mutex-backed memstore the
// concurrent-claim and lease-expiry assertions are trivially satisfied — the
// mutex serializes every operation. They are load-bearing only against the real
// dialects (phases 5/7), where FOR UPDATE SKIP LOCKED / busy-timeout discipline
// must make concurrent claims contention-free and stale reclaim atomic; the
// suite is written so a live SQL store constructed with the same short Lease
// honors it with real wall-clock sleeps, not an injected clock.
package storetest

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/jobs/domain/job"
	"github.com/gopernicus/gopernicus/features/jobs/domain/schedule"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/foundation/workers"
)

// Lease is the stale-claim recovery window a queue passed to RunQueue MUST be
// constructed with (e.g. memstore.NewQueue(memstore.WithLease(storetest.Lease))
// or turso.NewQueueStore(db, turso.WithLease(storetest.Lease))). The
// lease-expiry case sleeps past it with a real wall-clock sleep, so every store
// — memory or SQL — reclaims a stale running job the same way.
//
// The value must comfortably EXCEED the slowest supported dialect's
// Claim→Complete round-trip, or the reclaim arm legitimately double-claims
// jobs that are still in flight (observed against remote Turso: a single
// Claim ≈ 222ms, a Claim→Complete cycle ≈ 338ms — a 250ms lease made the
// ConcurrentClaim case fail with 29/60 double-claims while the store was
// provably correct). 3s gives ~9x margin over that; the cost is the
// lease-expiry case sleeping ~3.1s per store run.
const Lease = 3 * time.Second

// suiteBase is a fixed reference instant the schedule cases stamp next_run_at
// values from; the queue cases that turn on real elapsed time use time.Now.
var suiteBase = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

// RunQueue exercises the job.QueueRepository contract against a clean, isolated
// queue obtained from newRepo for each leaf subtest. newRepo MUST return a
// CLEAN, isolated repository per call — SQL harnesses truncate their tables,
// memory harnesses return a fresh instance — and MUST construct it with the
// exported Lease so the lease-expiry case is portable across stores.
func RunQueue(t *testing.T, newRepo func(t *testing.T) job.QueueRepository) {
	t.Helper()

	t.Run("EnqueueAndGet", func(t *testing.T) { testEnqueueAndGet(t, newRepo(t)) })
	t.Run("EnqueueIdempotency", func(t *testing.T) { testEnqueueIdempotency(t, newRepo(t)) })
	t.Run("ClaimEmptyQueue", func(t *testing.T) { testClaimEmpty(t, newRepo(t)) })
	t.Run("ClaimOrdering", func(t *testing.T) { testClaimOrdering(t, newRepo(t)) })
	t.Run("ScheduledForGating", func(t *testing.T) { testScheduledForGating(t, newRepo(t)) })
	t.Run("RetryThenDeadLetter", func(t *testing.T) { testRetryThenDeadLetter(t, newRepo(t)) })
	t.Run("LeaseExpiryReclaim", func(t *testing.T) { testLeaseExpiryReclaim(t, newRepo(t)) })
	t.Run("ListFilterAndPaginate", func(t *testing.T) {
		t.Run("FilterAndStatus", func(t *testing.T) { testQueueListFilterAndPaginate(t, newRepo(t)) })
		runPagedFamily(t, newRepo,
			func(repo job.QueueRepository, ctx context.Context, req crud.ListRequest) (crud.Page[job.Job], error) {
				return repo.List(ctx, job.ListFilter{Kind: "email"}, req)
			},
			seedQueueFamily,
			func(j job.Job) string { return j.ID() },
			func(j job.Job) time.Time { return j.CreatedAt },
		)
	})
	t.Run("ConcurrentClaim", func(t *testing.T) { testConcurrentClaim(t, newRepo(t)) })
}

// RunSchedules exercises the schedule.Repository contract against a clean,
// isolated repository obtained from newRepo for each leaf subtest. newRepo MUST
// return a CLEAN, isolated repository per call (same contract as RunQueue).
func RunSchedules(t *testing.T, newRepo func(t *testing.T) schedule.Repository) {
	t.Helper()

	t.Run("EnsureCreateGetUpsert", func(t *testing.T) { testEnsureUpsert(t, newRepo(t)) })
	t.Run("AbsentNotFound", func(t *testing.T) { testSchedulesAbsent(t, newRepo(t)) })
	t.Run("ListDueEnabledGating", func(t *testing.T) { testListDueGating(t, newRepo(t)) })
	t.Run("ClaimDueCAS", func(t *testing.T) { testClaimDueCAS(t, newRepo(t)) })
	t.Run("SetLastJobAndEnabled", func(t *testing.T) { testSetLastJobAndEnabled(t, newRepo(t)) })
	t.Run("ListPaginate", func(t *testing.T) {
		t.Run("Traversal", func(t *testing.T) { testSchedulesListPaginate(t, newRepo(t)) })
		runPagedFamily(t, newRepo,
			func(repo schedule.Repository, ctx context.Context, req crud.ListRequest) (crud.Page[schedule.Schedule], error) {
				return repo.List(ctx, req)
			},
			seedScheduleFamily,
			func(s schedule.Schedule) string { return s.ID },
			func(s schedule.Schedule) time.Time { return s.CreatedAt },
		)
	})
	t.Run("Delete", func(t *testing.T) { testSchedulesDelete(t, newRepo(t)) })
}

// --- queue cases ---

func testEnqueueAndGet(t *testing.T, repo job.QueueRepository) {
	ctx := context.Background()

	created, err := repo.Enqueue(ctx, job.Enqueue{ID: "j-crud", Kind: "demo.print", Payload: []byte(`{"msg":"hi"}`), Priority: 5, MaxAttempts: 4})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if created.ID() != "j-crud" || created.Status() != string(job.StatusPending) {
		t.Fatalf("Enqueue returned %q status %q, want j-crud/pending", created.ID(), created.Status())
	}

	got, err := repo.Get(ctx, "j-crud")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Kind != "demo.print" || got.Priority != 5 || got.MaxAttempts != 4 {
		t.Errorf("Get mismatch: %+v", got)
	}
	if string(got.Payload) != `{"msg":"hi"}` {
		t.Errorf("Get payload = %s, want the enqueued json", got.Payload)
	}

	if _, err := repo.Get(ctx, "nope"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Get(absent): err=%v, want ErrNotFound", err)
	}
}

func testEnqueueIdempotency(t *testing.T, repo job.QueueRepository) {
	ctx := context.Background()

	if _, err := repo.Enqueue(ctx, job.Enqueue{ID: "dup", Kind: "demo"}); err != nil {
		t.Fatalf("first Enqueue: %v", err)
	}
	if _, err := repo.Enqueue(ctx, job.Enqueue{ID: "dup", Kind: "demo"}); !errors.Is(err, sdk.ErrAlreadyExists) {
		t.Errorf("duplicate id Enqueue: err=%v, want ErrAlreadyExists", err)
	}

	// An empty id is generated, so two empty-id enqueues never collide.
	a, err := repo.Enqueue(ctx, job.Enqueue{Kind: "demo"})
	if err != nil {
		t.Fatalf("empty-id Enqueue a: %v", err)
	}
	b, err := repo.Enqueue(ctx, job.Enqueue{Kind: "demo"})
	if err != nil {
		t.Fatalf("empty-id Enqueue b: %v", err)
	}
	if a.ID() == "" || b.ID() == "" || a.ID() == b.ID() {
		t.Errorf("empty-id enqueues must get distinct non-empty ids, got %q and %q", a.ID(), b.ID())
	}
}

func testClaimEmpty(t *testing.T, repo job.QueueRepository) {
	if _, err := repo.Claim(context.Background(), "w1", time.Now().UTC()); !errors.Is(err, workers.ErrNoWork) {
		t.Errorf("Claim(empty): err=%v, want workers.ErrNoWork", err)
	}
}

func testClaimOrdering(t *testing.T, repo job.QueueRepository) {
	ctx := context.Background()
	now := time.Now().UTC()

	// Two low-priority jobs enqueued oldest-first, then a high-priority job.
	// Priority DESC wins first; within equal priority the older created_at wins.
	// A short real sleep separates created_at so the tie-break survives a store
	// that truncates timestamps to microseconds.
	mustEnqueue(t, repo, job.Enqueue{ID: "low-old", Kind: "demo", Priority: 0, ScheduledFor: now})
	time.Sleep(3 * time.Millisecond)
	mustEnqueue(t, repo, job.Enqueue{ID: "low-new", Kind: "demo", Priority: 0, ScheduledFor: now})
	mustEnqueue(t, repo, job.Enqueue{ID: "high", Kind: "demo", Priority: 10, ScheduledFor: now})

	claimNow := time.Now().UTC()
	order := []string{"high", "low-old", "low-new"}
	for i, want := range order {
		got, err := repo.Claim(ctx, "w1", claimNow)
		if err != nil {
			t.Fatalf("Claim %d: %v", i, err)
		}
		if got.ID() != want {
			t.Fatalf("Claim %d = %q, want %q (priority DESC then created_at)", i, got.ID(), want)
		}
		if got.Status() != string(job.StatusRunning) {
			t.Errorf("claimed job %q status = %q, want running", got.ID(), got.Status())
		}
	}
	if _, err := repo.Claim(ctx, "w1", claimNow); !errors.Is(err, workers.ErrNoWork) {
		t.Errorf("Claim after draining: err=%v, want ErrNoWork", err)
	}
}

func testScheduledForGating(t *testing.T, repo job.QueueRepository) {
	ctx := context.Background()
	base := time.Now().UTC()
	future := base.Add(time.Hour)

	mustEnqueue(t, repo, job.Enqueue{ID: "later", Kind: "demo", ScheduledFor: future})

	// Not due until scheduled_for.
	if _, err := repo.Claim(ctx, "w1", base); !errors.Is(err, workers.ErrNoWork) {
		t.Fatalf("Claim before scheduled_for: err=%v, want ErrNoWork", err)
	}
	got, err := repo.Claim(ctx, "w1", future.Add(time.Minute))
	if err != nil {
		t.Fatalf("Claim at/after scheduled_for: %v", err)
	}
	if got.ID() != "later" {
		t.Errorf("claimed %q, want later", got.ID())
	}
}

func testRetryThenDeadLetter(t *testing.T, repo job.QueueRepository) {
	ctx := context.Background()
	now := time.Now().UTC()
	const maxAttempts = 2

	mustEnqueue(t, repo, job.Enqueue{ID: "flaky", Kind: "demo", ScheduledFor: now})

	// First failure below the ceiling reschedules to pending and re-claimable.
	if _, err := repo.Claim(ctx, "w1", now); err != nil {
		t.Fatalf("first Claim: %v", err)
	}
	if err := repo.Fail(ctx, "flaky", now, "boom-1", maxAttempts); err != nil {
		t.Fatalf("first Fail: %v", err)
	}
	after1, _ := repo.Get(ctx, "flaky")
	if after1.Status() != string(job.StatusPending) || after1.RetryCount() != 1 {
		t.Fatalf("after first Fail: status=%q retries=%d, want pending/1", after1.Status(), after1.RetryCount())
	}

	// Second failure reaches max_attempts and dead-letters.
	claimed, err := repo.Claim(ctx, "w1", now)
	if err != nil {
		t.Fatalf("re-Claim after retry: %v", err)
	}
	if claimed.ID() != "flaky" {
		t.Fatalf("re-Claim = %q, want flaky", claimed.ID())
	}
	if err := repo.Fail(ctx, "flaky", now, "boom-2", maxAttempts); err != nil {
		t.Fatalf("second Fail: %v", err)
	}
	dead, _ := repo.Get(ctx, "flaky")
	if dead.Status() != string(job.StatusDeadLetter) || dead.RetryCount() != 2 {
		t.Fatalf("after second Fail: status=%q retries=%d, want dead_letter/2", dead.Status(), dead.RetryCount())
	}
	if dead.FailureReason != "boom-2" {
		t.Errorf("failure reason = %q, want boom-2", dead.FailureReason)
	}

	// A dead-lettered job is never claimed again.
	if _, err := repo.Claim(ctx, "w1", now); !errors.Is(err, workers.ErrNoWork) {
		t.Errorf("Claim after dead-letter: err=%v, want ErrNoWork", err)
	}
}

// testLeaseExpiryReclaim proves stale-claim recovery: a running job whose lease
// expired is claimable again. It relies on the store having been constructed
// with the exported Lease and sleeps just past it with a real wall-clock sleep,
// so a live SQL store honors the same case without any injected clock.
func testLeaseExpiryReclaim(t *testing.T, repo job.QueueRepository) {
	ctx := context.Background()
	now := time.Now().UTC()

	mustEnqueue(t, repo, job.Enqueue{ID: "leased", Kind: "demo", ScheduledFor: now})

	first, err := repo.Claim(ctx, "w1", time.Now().UTC())
	if err != nil {
		t.Fatalf("first Claim: %v", err)
	}
	if first.ID() != "leased" {
		t.Fatalf("first Claim = %q, want leased", first.ID())
	}

	// Still leased: a second claim before expiry finds nothing.
	if _, err := repo.Claim(ctx, "w2", time.Now().UTC()); !errors.Is(err, workers.ErrNoWork) {
		t.Fatalf("Claim while leased: err=%v, want ErrNoWork", err)
	}

	time.Sleep(Lease + 100*time.Millisecond)

	reclaimed, err := repo.Claim(ctx, "w2", time.Now().UTC())
	if err != nil {
		t.Fatalf("Claim after lease expiry: %v", err)
	}
	if reclaimed.ID() != "leased" {
		t.Errorf("reclaimed %q, want leased (stale claim should be recovered)", reclaimed.ID())
	}
	if reclaimed.WorkerName != "w2" {
		t.Errorf("reclaimed worker = %q, want w2 (re-leased to the new claimer)", reclaimed.WorkerName)
	}
}

func testQueueListFilterAndPaginate(t *testing.T, repo job.QueueRepository) {
	ctx := context.Background()
	now := time.Now().UTC()

	// Six "email" jobs and two "sms" jobs, all pending.
	var emailIDs []string
	for i := 0; i < 6; i++ {
		id := "email-" + string(rune('a'+i))
		emailIDs = append(emailIDs, id)
		mustEnqueue(t, repo, job.Enqueue{ID: id, Kind: "email", ScheduledFor: now})
	}
	mustEnqueue(t, repo, job.Enqueue{ID: "sms-1", Kind: "sms", ScheduledFor: now})
	mustEnqueue(t, repo, job.Enqueue{ID: "sms-2", Kind: "sms", ScheduledFor: now})

	// Kind filter isolates the email jobs; cursor traversal returns them all
	// exactly once across pages of size 2.
	got := drainPages(t, 2, func(j job.Job) string { return j.ID() },
		func(req crud.ListRequest) (crud.Page[job.Job], error) {
			return repo.List(ctx, job.ListFilter{Kind: "email"}, req)
		})
	assertIDSet(t, ids(got, func(j job.Job) string { return j.ID() }), emailIDs)

	// Status filter: claim one email job, then filter on running.
	if _, err := repo.Claim(ctx, "w1", now); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	running, err := repo.List(ctx, job.ListFilter{Status: job.StatusRunning}, crud.ListRequest{Limit: 10})
	if err != nil {
		t.Fatalf("List running: %v", err)
	}
	if len(running.Items) != 1 || running.Items[0].Status() != string(job.StatusRunning) {
		t.Errorf("List(status=running) = %d items, want exactly 1 running", len(running.Items))
	}
}

// testConcurrentClaim asserts G goroutines draining N jobs receive N DISTINCT
// jobs with no spurious errors. Trivially green against the mutex memstore
// (honesty note) — load-bearing against real dialects where SQLITE_BUSY must
// surface as adapter-internal wait, never a failed claim.
func testConcurrentClaim(t *testing.T, repo job.QueueRepository) {
	ctx := context.Background()
	now := time.Now().UTC()
	const (
		n = 60
		g = 8
	)

	for i := 0; i < n; i++ {
		mustEnqueue(t, repo, job.Enqueue{ID: "c-" + string(rune('A'+i%26)) + "-" + strconv.Itoa(i), Kind: "demo", ScheduledFor: now})
	}

	var (
		mu      sync.Mutex
		claimed = map[string]int{}
		errs2   []error
		wg      sync.WaitGroup
	)
	for w := 0; w < g; w++ {
		wg.Add(1)
		go func(worker string) {
			defer wg.Done()
			for {
				j, err := repo.Claim(ctx, worker, time.Now().UTC())
				if errors.Is(err, workers.ErrNoWork) {
					return
				}
				if err != nil {
					mu.Lock()
					errs2 = append(errs2, err)
					mu.Unlock()
					return
				}
				mu.Lock()
				claimed[j.ID()]++
				mu.Unlock()
				// Complete so a lease expiry can never re-surface this job.
				if err := repo.Complete(ctx, j.ID(), time.Now().UTC()); err != nil {
					mu.Lock()
					errs2 = append(errs2, err)
					mu.Unlock()
				}
			}
		}("w" + strconv.Itoa(w))
	}
	wg.Wait()

	if len(errs2) != 0 {
		t.Fatalf("concurrent claim/complete produced %d spurious errors: %v", len(errs2), errs2[0])
	}
	if len(claimed) != n {
		t.Fatalf("claimed %d distinct jobs, want %d", len(claimed), n)
	}
	for id, count := range claimed {
		if count != 1 {
			t.Errorf("job %q claimed %d times, want exactly 1 (double-claim)", id, count)
		}
	}
}

// --- schedule cases ---

func testEnsureUpsert(t *testing.T, repo schedule.Repository) {
	ctx := context.Background()
	next := suiteBase.Add(time.Hour)

	created, err := repo.Ensure(ctx, schedule.Ensure{Name: "nightly", Kind: "demo.print", Spec: schedule.Spec{Every: time.Hour}}, next)
	if err != nil {
		t.Fatalf("Ensure create: %v", err)
	}
	if created.ID == "" || created.Name != "nightly" || !created.Enabled {
		t.Fatalf("Ensure create returned %+v, want enabled with an id", created)
	}
	if !created.NextRunAt.Equal(next) {
		t.Errorf("create NextRunAt = %v, want %v", created.NextRunAt, next)
	}

	got, err := repo.Get(ctx, created.ID)
	if err != nil || got.Name != "nightly" {
		t.Fatalf("Get: %+v err=%v", got, err)
	}

	// Upsert by Name with the SAME spec keeps the id and NextRunAt, updates
	// kind/payload.
	sameSpec, err := repo.Ensure(ctx, schedule.Ensure{Name: "nightly", Kind: "demo.other", Spec: schedule.Spec{Every: time.Hour}, Payload: []byte(`{"a":1}`)}, next.Add(time.Hour))
	if err != nil {
		t.Fatalf("Ensure same-spec: %v", err)
	}
	if sameSpec.ID != created.ID {
		t.Errorf("same-spec upsert changed id from %q to %q", created.ID, sameSpec.ID)
	}
	if !sameSpec.NextRunAt.Equal(next) {
		t.Errorf("same-spec upsert moved NextRunAt to %v, want unchanged %v", sameSpec.NextRunAt, next)
	}
	if sameSpec.Kind != "demo.other" || string(sameSpec.Payload) != `{"a":1}` {
		t.Errorf("same-spec upsert did not update kind/payload: %+v", sameSpec)
	}

	// Upsert with a CHANGED spec advances NextRunAt to the new next.
	newNext := next.Add(2 * time.Hour)
	changed, err := repo.Ensure(ctx, schedule.Ensure{Name: "nightly", Kind: "demo.other", Spec: schedule.Spec{Every: 30 * time.Minute}}, newNext)
	if err != nil {
		t.Fatalf("Ensure changed-spec: %v", err)
	}
	if changed.ID != created.ID {
		t.Errorf("changed-spec upsert changed id")
	}
	if !changed.NextRunAt.Equal(newNext) {
		t.Errorf("changed-spec NextRunAt = %v, want advanced to %v", changed.NextRunAt, newNext)
	}
}

func testSchedulesAbsent(t *testing.T, repo schedule.Repository) {
	ctx := context.Background()
	if _, err := repo.Get(ctx, "nope"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Get(absent): err=%v, want ErrNotFound", err)
	}
	if err := repo.Delete(ctx, "nope"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Delete(absent): err=%v, want ErrNotFound", err)
	}
}

func testListDueGating(t *testing.T, repo schedule.Repository) {
	ctx := context.Background()

	// s1 due at base, s2 due an hour later.
	s1 := mustEnsure(t, repo, schedule.Ensure{Name: "s1", Kind: "demo", Spec: schedule.Spec{Every: time.Hour}}, suiteBase)
	s2 := mustEnsure(t, repo, schedule.Ensure{Name: "s2", Kind: "demo", Spec: schedule.Spec{Every: time.Hour}}, suiteBase.Add(time.Hour))

	// At base+30m only s1 is due.
	due, err := repo.ListDue(ctx, suiteBase.Add(30*time.Minute), 10)
	if err != nil {
		t.Fatalf("ListDue: %v", err)
	}
	if len(due) != 1 || due[0].ID != s1.ID {
		t.Fatalf("ListDue(base+30m) = %v, want only s1", scheduleIDs(due))
	}

	// At base+2h both are due — until s2 is disabled.
	if due, _ := repo.ListDue(ctx, suiteBase.Add(2*time.Hour), 10); len(due) != 2 {
		t.Fatalf("ListDue(base+2h) = %d, want 2", len(due))
	}
	if err := repo.SetEnabled(ctx, s2.ID, false, suiteBase); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	due, _ = repo.ListDue(ctx, suiteBase.Add(2*time.Hour), 10)
	if len(due) != 1 || due[0].ID != s1.ID {
		t.Errorf("ListDue after disabling s2 = %v, want only s1 (disabled excluded)", scheduleIDs(due))
	}
}

func testClaimDueCAS(t *testing.T, repo schedule.Repository) {
	ctx := context.Background()
	now := suiteBase
	s := mustEnsure(t, repo, schedule.Ensure{Name: "cas", Kind: "demo", Spec: schedule.Spec{Every: time.Hour}}, suiteBase)
	slot0 := s.NextRunAt
	slot1 := slot0.Add(time.Hour)

	// A caller holding the current slot wins and advances next_run_at.
	won, err := repo.ClaimDue(ctx, s.ID, slot0, slot1, now)
	if err != nil {
		t.Fatalf("ClaimDue win: %v", err)
	}
	if !won {
		t.Fatal("ClaimDue with the current slot must win")
	}

	// A second caller with the now-stale prev slot loses; next_run_at unchanged.
	lost, err := repo.ClaimDue(ctx, s.ID, slot0, slot0.Add(2*time.Hour), now)
	if err != nil {
		t.Fatalf("ClaimDue stale: %v", err)
	}
	if lost {
		t.Error("ClaimDue with a stale prevNextRunAt must lose")
	}
	after, _ := repo.Get(ctx, s.ID)
	if !after.NextRunAt.Equal(slot1) {
		t.Errorf("next_run_at = %v after a lost CAS, want unchanged %v", after.NextRunAt, slot1)
	}

	// A disabled schedule never wins.
	if err := repo.SetEnabled(ctx, s.ID, false, now); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	if won, _ := repo.ClaimDue(ctx, s.ID, slot1, slot1.Add(time.Hour), now); won {
		t.Error("ClaimDue on a disabled schedule must lose")
	}
}

func testSetLastJobAndEnabled(t *testing.T, repo schedule.Repository) {
	ctx := context.Background()
	s := mustEnsure(t, repo, schedule.Ensure{Name: "last", Kind: "demo", Spec: schedule.Spec{Every: time.Hour}}, suiteBase)

	if err := repo.SetLastJob(ctx, s.ID, "sched_last_1", suiteBase); err != nil {
		t.Fatalf("SetLastJob: %v", err)
	}
	got, _ := repo.Get(ctx, s.ID)
	if got.LastJobID != "sched_last_1" {
		t.Errorf("LastJobID = %q, want sched_last_1", got.LastJobID)
	}

	if err := repo.SetEnabled(ctx, s.ID, false, suiteBase); err != nil {
		t.Fatalf("SetEnabled false: %v", err)
	}
	if got, _ := repo.Get(ctx, s.ID); got.Enabled {
		t.Error("SetEnabled(false) did not disable the schedule")
	}
	if err := repo.SetEnabled(ctx, s.ID, true, suiteBase); err != nil {
		t.Fatalf("SetEnabled true: %v", err)
	}
	if got, _ := repo.Get(ctx, s.ID); !got.Enabled {
		t.Error("SetEnabled(true) did not re-enable the schedule")
	}
}

func testSchedulesListPaginate(t *testing.T, repo schedule.Repository) {
	ctx := context.Background()

	var want []string
	for i := 0; i < 5; i++ {
		name := "sch-" + string(rune('a'+i))
		s := mustEnsure(t, repo, schedule.Ensure{Name: name, Kind: "demo", Spec: schedule.Spec{Every: time.Hour}}, suiteBase.Add(time.Duration(i)*time.Minute))
		want = append(want, s.ID)
	}

	got := drainPages(t, 2, func(s schedule.Schedule) string { return s.ID },
		func(req crud.ListRequest) (crud.Page[schedule.Schedule], error) {
			return repo.List(ctx, req)
		})
	assertIDSet(t, ids(got, func(s schedule.Schedule) string { return s.ID }), want)
}

func testSchedulesDelete(t *testing.T, repo schedule.Repository) {
	ctx := context.Background()
	s := mustEnsure(t, repo, schedule.Ensure{Name: "gone", Kind: "demo", Spec: schedule.Spec{Every: time.Hour}}, suiteBase)

	if err := repo.Delete(ctx, s.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, s.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Get after Delete: err=%v, want ErrNotFound", err)
	}
}

// --- helpers ---

func mustEnqueue(t *testing.T, repo job.QueueRepository, in job.Enqueue) job.Job {
	t.Helper()
	j, err := repo.Enqueue(context.Background(), in)
	if err != nil {
		t.Fatalf("Enqueue %q: %v", in.ID, err)
	}
	return j
}

func mustEnsure(t *testing.T, repo schedule.Repository, in schedule.Ensure, next time.Time) schedule.Schedule {
	t.Helper()
	s, err := repo.Ensure(context.Background(), in, next)
	if err != nil {
		t.Fatalf("Ensure %q: %v", in.Name, err)
	}
	return s
}

// drainPages walks every page of a cursor-paginated List, following NextCursor
// until HasMore is false. It fails on a duplicate id across pages or a page that
// exceeds the limit, and returns the full traversal — the raw material for the
// completeness assertion (the ports promise cursor pagination, not a specific
// sort order, so callers assert set identity, not ordering).
func drainPages[T any](t *testing.T, limit int, id func(T) string, fetch func(crud.ListRequest) (crud.Page[T], error)) []T {
	t.Helper()
	var out []T
	seen := map[string]bool{}
	cursor := ""
	for i := 0; i < 1000; i++ {
		page, err := fetch(crud.ListRequest{Limit: limit, Cursor: cursor})
		if err != nil {
			t.Fatalf("List(page %d): %v", i, err)
		}
		if len(page.Items) > limit {
			t.Fatalf("page %d returned %d items, exceeds limit %d", i, len(page.Items), limit)
		}
		for _, it := range page.Items {
			key := id(it)
			if seen[key] {
				t.Errorf("id %q returned on more than one page", key)
			}
			seen[key] = true
			out = append(out, it)
		}
		if !page.HasMore {
			return out
		}
		if page.NextCursor == "" {
			t.Fatal("List reported HasMore but returned an empty NextCursor")
		}
		cursor = page.NextCursor
	}
	t.Fatal("pagination did not terminate within 1000 pages")
	return nil
}

func ids[T any](items []T, id func(T) string) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = id(it)
	}
	return out
}

func assertIDSet(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("traversal returned %d items, want %d", len(got), len(want))
	}
	set := map[string]bool{}
	for _, id := range got {
		set[id] = true
	}
	for _, id := range want {
		if !set[id] {
			t.Errorf("id %q missing from traversal", id)
		}
	}
}

func scheduleIDs(s []schedule.Schedule) []string {
	out := make([]string, len(s))
	for i, sc := range s {
		out[i] = sc.ID
	}
	return out
}

// --- the standard paginated-port case family (copied from P3, scoped to jobs) ---
//
// The queue's filtered List and the schedules List each run the same six cases —
// Order, PrevPage, OffsetMode, WithCount, StaleCursorOrderChange,
// CursorOffsetExclusive — over a small seeded population. Because neither
// Enqueue nor Ensure lets a caller set created_at (the store stamps it), the
// seeds separate rows with a real 2ms sleep so their created_at is strictly
// ordered and distinct — the same wall-clock idiom testClaimOrdering already
// uses. The consequence is the family does NOT exercise a same-created_at
// tiebreak pair for jobs (the port gives no created_at control); the id tiebreak
// is still asserted structurally by the sorted-order comparison.

// pagedCase bundles the scoped list closure with the id/created_at projections a
// case needs.
type pagedCase[T any] struct {
	list      func(ctx context.Context, req crud.ListRequest) (crud.Page[T], error)
	idOf      func(T) string
	createdAt func(T) time.Time
}

// runPagedFamily wires the six standard cases for one paginated port. Each case
// obtains a clean, isolated repo from newRepo and seeds its own population,
// matching the suite's per-leaf isolation contract.
func runPagedFamily[R any, T any](
	t *testing.T,
	newRepo func(t *testing.T) R,
	scope func(repo R, ctx context.Context, req crud.ListRequest) (crud.Page[T], error),
	seed func(t *testing.T, repo R) (created []T, wantTotal int),
	idOf func(T) string,
	createdAt func(T) time.Time,
) {
	t.Helper()

	newCase := func(repo R) pagedCase[T] {
		return pagedCase[T]{
			list:      func(ctx context.Context, req crud.ListRequest) (crud.Page[T], error) { return scope(repo, ctx, req) },
			idOf:      idOf,
			createdAt: createdAt,
		}
	}

	t.Run("Order", func(t *testing.T) {
		repo := newRepo(t)
		created, _ := seed(t, repo)
		runOrderCase(t, newCase(repo), created)
	})
	t.Run("PrevPage", func(t *testing.T) {
		repo := newRepo(t)
		created, _ := seed(t, repo)
		runPrevPageCase(t, newCase(repo), created)
	})
	t.Run("OffsetMode", func(t *testing.T) {
		repo := newRepo(t)
		created, _ := seed(t, repo)
		runOffsetModeCase(t, newCase(repo), created)
	})
	t.Run("WithCount", func(t *testing.T) {
		repo := newRepo(t)
		_, wantTotal := seed(t, repo)
		runWithCountCase(t, newCase(repo), wantTotal)
	})
	t.Run("StaleCursorOrderChange", func(t *testing.T) {
		repo := newRepo(t)
		created, _ := seed(t, repo)
		runStaleCursorCase(t, newCase(repo), created)
	})
	t.Run("CursorOffsetExclusive", func(t *testing.T) {
		repo := newRepo(t)
		_, _ = seed(t, repo)
		runCursorOffsetExclusiveCase(t, newCase(repo))
	})
}

// runOrderCase asserts explicit asc + desc ordering on created_at pages through
// the full population in the correct total order (created_at then id tiebreak in
// the same direction).
func runOrderCase[T any](t *testing.T, pc pagedCase[T], created []T) {
	t.Helper()
	wantAsc := pagedSortedIDs(created, pc.idOf, pc.createdAt, true)
	gotAsc := pageAllOrdered(t, pc, crud.NewOrder("created_at", crud.ASC), 2)
	if !equalStrings(gotAsc, wantAsc) {
		t.Errorf("asc order = %v, want %v", gotAsc, wantAsc)
	}

	wantDesc := pagedSortedIDs(created, pc.idOf, pc.createdAt, false)
	gotDesc := pageAllOrdered(t, pc, crud.NewOrder("created_at", crud.DESC), 2)
	if !equalStrings(gotDesc, wantDesc) {
		t.Errorf("desc order = %v, want %v", gotDesc, wantDesc)
	}
}

// runPrevPageCase asserts the reverse-probe semantics: the first page has no
// prev; page 2 has a prev whose (partial) window means "previous is the first
// page" and round-trips to page 1's IDs; page 3 has a full prior window whose
// PreviousCursor round-trips to page 2's IDs.
func runPrevPageCase[T any](t *testing.T, pc pagedCase[T], created []T) {
	t.Helper()
	ctx := context.Background()
	desc := pagedSortedIDs(created, pc.idOf, pc.createdAt, false)
	if len(desc) < 6 {
		t.Fatalf("prev-page case needs >= 6 seeded rows, got %d", len(desc))
	}

	page1, err := pc.list(ctx, crud.ListRequest{Limit: 2})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if page1.HasPrev {
		t.Errorf("first page HasPrev = true, want false")
	}
	if got := pagedIDsOf(page1.Items, pc.idOf); !equalStrings(got, desc[0:2]) {
		t.Errorf("page1 = %v, want %v", got, desc[0:2])
	}

	page2, err := pc.list(ctx, crud.ListRequest{Limit: 2, Cursor: page1.NextCursor})
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if !page2.HasPrev {
		t.Errorf("page2 HasPrev = false, want true")
	}
	if got := pagedIDsOf(page2.Items, pc.idOf); !equalStrings(got, desc[2:4]) {
		t.Errorf("page2 = %v, want %v", got, desc[2:4])
	}
	// Only one row precedes the page-1 cursor, so page 2's prior window is
	// partial ⇒ empty PreviousCursor meaning "the previous page is the first page".
	if page2.PreviousCursor != "" {
		t.Errorf("page2 PreviousCursor = %q, want empty (partial prior window)", page2.PreviousCursor)
	}
	back := backPage(t, pc, page2.PreviousCursor)
	if got := pagedIDsOf(back.Items, pc.idOf); !equalStrings(got, desc[0:2]) {
		t.Errorf("previous of page2 = %v, want page1 %v", got, desc[0:2])
	}

	page3, err := pc.list(ctx, crud.ListRequest{Limit: 2, Cursor: page2.NextCursor})
	if err != nil {
		t.Fatalf("page3: %v", err)
	}
	if !page3.HasPrev {
		t.Errorf("page3 HasPrev = false, want true")
	}
	if page3.PreviousCursor == "" {
		t.Errorf("page3 PreviousCursor empty, want a full prior window")
	}
	back3 := backPage(t, pc, page3.PreviousCursor)
	if got := pagedIDsOf(back3.Items, pc.idOf); !equalStrings(got, desc[2:4]) {
		t.Errorf("previous of page3 = %v, want page2 %v", got, desc[2:4])
	}
}

// runOffsetModeCase asserts offset traversal (explicit StrategyOffset) yields the
// identical ID sequence as cursor traversal, HasPrev iff offset > 0, that offset
// pages emit no cursors at any offset, and that Offset 0 under the offset strategy
// is the first page.
func runOffsetModeCase[T any](t *testing.T, pc pagedCase[T], created []T) {
	t.Helper()
	ctx := context.Background()

	cursorIDs := pageAllOrdered(t, pc, crud.Order{}, 2)

	var offsetIDs []string
	for off := 0; off < 100; off += 2 {
		page, err := pc.list(ctx, crud.ListRequest{Strategy: crud.StrategyOffset, Limit: 2, Offset: off})
		if err != nil {
			t.Fatalf("offset page at %d: %v", off, err)
		}
		offsetIDs = append(offsetIDs, pagedIDsOf(page.Items, pc.idOf)...)
		// Offset strategy emits no cursors at any offset — the caller does the
		// offset arithmetic.
		if page.NextCursor != "" || page.PreviousCursor != "" {
			t.Errorf("offset page at %d carried a cursor (next=%q prev=%q)", off, page.NextCursor, page.PreviousCursor)
		}
		if want := off > 0; page.HasPrev != want {
			t.Errorf("offset page at %d HasPrev = %v, want %v", off, page.HasPrev, want)
		}
		if !page.HasMore {
			break
		}
	}
	if !equalStrings(offsetIDs, cursorIDs) {
		t.Errorf("offset traversal = %v, want cursor traversal %v", offsetIDs, cursorIDs)
	}

	// OffsetZero: the offset strategy with Offset 0 is the first page — HasPrev
	// false and no cursors. This is the wart the explicit strategy fixes: Offset 0
	// no longer silently means cursor mode.
	zero, err := pc.list(ctx, crud.ListRequest{Strategy: crud.StrategyOffset, Limit: 2, Offset: 0})
	if err != nil {
		t.Fatalf("offset-zero page: %v", err)
	}
	if zero.HasPrev {
		t.Errorf("offset-zero HasPrev = true, want false")
	}
	if zero.NextCursor != "" || zero.PreviousCursor != "" {
		t.Errorf("offset-zero carried a cursor (next=%q prev=%q)", zero.NextCursor, zero.PreviousCursor)
	}
	if got, want := pagedIDsOf(zero.Items, pc.idOf), cursorIDs[:len(zero.Items)]; !equalStrings(got, want) {
		t.Errorf("offset-zero page = %v, want first page %v", got, want)
	}
}

// runWithCountCase asserts Total equals the filtered row count in both modes and
// is nil when unrequested.
func runWithCountCase[T any](t *testing.T, pc pagedCase[T], wantTotal int) {
	t.Helper()
	ctx := context.Background()

	cursorPage, err := pc.list(ctx, crud.ListRequest{Limit: 2, WithCount: true})
	if err != nil {
		t.Fatalf("cursor+count: %v", err)
	}
	if cursorPage.Total == nil || *cursorPage.Total != int64(wantTotal) {
		t.Errorf("cursor-mode Total = %v, want %d", cursorPage.Total, wantTotal)
	}

	offsetPage, err := pc.list(ctx, crud.ListRequest{Strategy: crud.StrategyOffset, Limit: 2, Offset: 2, WithCount: true})
	if err != nil {
		t.Fatalf("offset+count: %v", err)
	}
	if offsetPage.Total == nil || *offsetPage.Total != int64(wantTotal) {
		t.Errorf("offset-mode Total = %v, want %d", offsetPage.Total, wantTotal)
	}

	noCount, err := pc.list(ctx, crud.ListRequest{Limit: 2})
	if err != nil {
		t.Fatalf("no-count: %v", err)
	}
	if noCount.Total != nil {
		t.Errorf("Total = %v, want nil when count not requested", *noCount.Total)
	}
}

// runStaleCursorCase asserts a cursor minted under a different sort field is
// treated as the first page (no error, no skew). The order field is the only
// staleness key the cursor codec carries, so a token authored for a different
// column decodes to nil and the store returns the first page.
func runStaleCursorCase[T any](t *testing.T, pc pagedCase[T], created []T) {
	t.Helper()
	ctx := context.Background()

	first, err := pc.list(ctx, crud.ListRequest{Limit: 2})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}

	stale, err := crud.EncodeCursor("updated_at", pc.createdAt(created[0]), pc.idOf(created[0]))
	if err != nil {
		t.Fatalf("EncodeCursor: %v", err)
	}
	got, err := pc.list(ctx, crud.ListRequest{Limit: 2, Cursor: stale})
	if err != nil {
		t.Fatalf("stale cursor: err=%v, want first page", err)
	}
	if g, w := pagedIDsOf(got.Items, pc.idOf), pagedIDsOf(first.Items, pc.idOf); !equalStrings(g, w) {
		t.Errorf("stale cursor = %v, want first page %v (treated as first page)", g, w)
	}
	if got.HasPrev {
		t.Errorf("stale-cursor first page HasPrev = true, want false")
	}
}

// runCursorOffsetExclusiveCase asserts each per-strategy conflict is rejected
// with the invalid-input kind: a cursor strategy carrying a non-zero offset, and
// an offset strategy carrying a cursor.
func runCursorOffsetExclusiveCase[T any](t *testing.T, pc pagedCase[T]) {
	t.Helper()
	ctx := context.Background()
	if _, err := pc.list(ctx, crud.ListRequest{Strategy: crud.StrategyCursor, Limit: 2, Offset: 2}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("cursor strategy + offset: err=%v, want ErrInvalidInput", err)
	}
	if _, err := pc.list(ctx, crud.ListRequest{Strategy: crud.StrategyOffset, Limit: 2, Cursor: "anything"}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("offset strategy + cursor: err=%v, want ErrInvalidInput", err)
	}
}

// backPage requests the previous page: an empty previousCursor means "the
// previous page is the first page", so a first-page request is issued.
func backPage[T any](t *testing.T, pc pagedCase[T], previousCursor string) crud.Page[T] {
	t.Helper()
	page, err := pc.list(context.Background(), crud.ListRequest{Limit: 2, Cursor: previousCursor})
	if err != nil {
		t.Fatalf("previous page: %v", err)
	}
	return page
}

// pageAllOrdered pages forward through the whole population under order, threading
// order into every request, and returns the collected ids in traversal order.
func pageAllOrdered[T any](t *testing.T, pc pagedCase[T], order crud.Order, limit int) []string {
	t.Helper()
	ctx := context.Background()
	var out []string
	cursor := ""
	for i := 0; i < 100; i++ { // bound against a runaway cursor
		page, err := pc.list(ctx, crud.ListRequest{Limit: limit, Cursor: cursor, Order: order})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		out = append(out, pagedIDsOf(page.Items, pc.idOf)...)
		if !page.HasMore || page.NextCursor == "" {
			return out
		}
		cursor = page.NextCursor
	}
	t.Fatalf("pageAllOrdered did not terminate")
	return nil
}

// pagedSortedIDs returns the ids of items sorted by (created_at, id) in the given
// direction — the total order every paginated port must page in.
func pagedSortedIDs[T any](items []T, idOf func(T) string, createdAt func(T) time.Time, asc bool) []string {
	sorted := append([]T(nil), items...)
	sort.Slice(sorted, func(i, j int) bool {
		ti, tj := createdAt(sorted[i]), createdAt(sorted[j])
		if !ti.Equal(tj) {
			if asc {
				return ti.Before(tj)
			}
			return ti.After(tj)
		}
		if asc {
			return idOf(sorted[i]) < idOf(sorted[j])
		}
		return idOf(sorted[i]) > idOf(sorted[j])
	})
	return pagedIDsOf(sorted, idOf)
}

// pagedIDsOf projects each item's id.
func pagedIDsOf[T any](items []T, idOf func(T) string) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = idOf(it)
	}
	return out
}

// equalStrings reports whether two string slices are element-wise equal.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// seedQueueFamily seeds six "email" jobs (the in-scope population) plus two "sms"
// jobs a Kind filter must exclude, separating each email enqueue by a real 2ms
// sleep so their created_at is strictly ordered and distinct. wantTotal is 6 —
// the filtered count, not the eight rows written.
func seedQueueFamily(t *testing.T, repo job.QueueRepository) ([]job.Job, int) {
	t.Helper()
	now := time.Now().UTC()
	created := make([]job.Job, 0, 6)
	for i := 0; i < 6; i++ {
		j := mustEnqueue(t, repo, job.Enqueue{ID: fmt.Sprintf("fam-email-%d", i), Kind: "email", ScheduledFor: now})
		created = append(created, j)
		time.Sleep(2 * time.Millisecond)
	}
	// Foreign rows under a different kind must not be listed or counted.
	mustEnqueue(t, repo, job.Enqueue{ID: "fam-sms-0", Kind: "sms", ScheduledFor: now})
	mustEnqueue(t, repo, job.Enqueue{ID: "fam-sms-1", Kind: "sms", ScheduledFor: now})
	return created, len(created)
}

// seedScheduleFamily seeds six schedules under distinct names, separating each
// Ensure by a real 2ms sleep so their created_at is strictly ordered and
// distinct. wantTotal is 6 — the schedules List has no filter.
func seedScheduleFamily(t *testing.T, repo schedule.Repository) ([]schedule.Schedule, int) {
	t.Helper()
	created := make([]schedule.Schedule, 0, 6)
	for i := 0; i < 6; i++ {
		s := mustEnsure(t, repo, schedule.Ensure{Name: fmt.Sprintf("fam-sch-%d", i), Kind: "demo", Spec: schedule.Spec{Every: time.Hour}}, suiteBase.Add(time.Duration(i)*time.Minute))
		created = append(created, s)
		time.Sleep(2 * time.Millisecond)
	}
	return created, len(created)
}
