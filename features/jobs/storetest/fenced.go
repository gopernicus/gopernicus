package storetest

// This file is the FROZEN (AV3D-0.3) conformance SKELETON for the hardened,
// lease-fenced, logical-key queue extension (job.FencedQueueRepository). It is the
// executable form of that port's frozen doc comments, mirroring RunQueue's shape.
//
// No store implements the port yet: the memory/pgx/turso implementations land in
// phase 1 (AV3D-1.1..1.5), which wires RunFencedQueue against them (and, live,
// under -race). Until then every case skips cleanly via requireFenced — the
// reference wiring passes a nil factory — so the jobs module stays green while the
// contract is already spelled out. The assertions below become load-bearing the
// moment phase 1 hands RunFencedQueue a real factory; they must not be weakened to
// make an implementation pass.

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/jobs/domain/job"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/workers"
)

// RunFencedQueue exercises the job.FencedQueueRepository contract against a clean,
// isolated repository obtained from newRepo for each leaf subtest (the RunQueue
// isolation contract). Each case is written in full and gated to skip until a
// phase-1 implementation is wired: pass a real factory (AV3D-1.5) to activate the
// suite, or a nil factory to record the pending contract as a skip.
func RunFencedQueue(t *testing.T, newRepo func(t *testing.T) job.FencedQueueRepository) {
	t.Helper()

	t.Run("UniqueExecutionIDAndLogicalKey", func(t *testing.T) { testFencedIDsAndKeys(t, newRepo) })
	t.Run("EnqueueOnceReturnsActive", func(t *testing.T) { testFencedEnqueueOnce(t, newRepo) })
	t.Run("ReplaceSupersedesActive", func(t *testing.T) { testFencedReplaceSupersedes(t, newRepo) })
	t.Run("LatestByKeyDeterministic", func(t *testing.T) { testFencedLatestByKey(t, newRepo) })
	t.Run("TerminalCanceledAndSuperseded", func(t *testing.T) { testFencedTerminalStates(t, newRepo) })
	t.Run("ConcurrentEnqueueOnceSameKey", func(t *testing.T) { testFencedConcurrentEnqueueOnce(t, newRepo) })
	t.Run("ConcurrentReplaceVsEnqueueOnce", func(t *testing.T) { testFencedConcurrentReplaceVsEnqueueOnce(t, newRepo) })
	t.Run("ReplaceFencesRunningClaim", func(t *testing.T) { testFencedReplaceFencesRunningClaim(t, newRepo) })
	t.Run("ClaimFencingOnStaleLease", func(t *testing.T) { testFencedClaimFencing(t, newRepo) })
	t.Run("CheckpointWhileClaimCurrent", func(t *testing.T) { testFencedCheckpoint(t, newRepo) })
	t.Run("CheckpointBeforeSideEffect", func(t *testing.T) { testFencedCheckpointBeforeSideEffect(t, newRepo) })
	t.Run("CheckpointCrashReclaimReadsBytes", func(t *testing.T) { testFencedCheckpointCrashReclaim(t, newRepo) })
	t.Run("ConcurrentCheckpointVsReplace", func(t *testing.T) { testFencedConcurrentCheckpointVsReplace(t, newRepo) })
	t.Run("RetryAtScheduling", func(t *testing.T) { testFencedRetryAt(t, newRepo) })
	t.Run("PermanentFailureDeadLetter", func(t *testing.T) { testFencedPermanentFail(t, newRepo) })
	t.Run("BoundedTerminalPurge", func(t *testing.T) { testFencedPurge(t, newRepo) })
	t.Run("ConflictNotFoundAlreadyExists", func(t *testing.T) { testFencedErrorKinds(t, newRepo) })
}

// requireFenced returns a clean fenced repository, or skips the case when no
// implementation is wired yet (nil factory or a nil-returning factory). This is
// the gate that keeps the AV3D-0.3 skeleton green until phase 1.
func requireFenced(t *testing.T, newRepo func(t *testing.T) job.FencedQueueRepository) job.FencedQueueRepository {
	t.Helper()
	if newRepo == nil {
		t.Skip("AV3D-0.3 skeleton: no FencedQueueRepository wired yet (implementation lands AV3D-1.5)")
	}
	repo := newRepo(t)
	if repo == nil {
		t.Skip("AV3D-0.3 skeleton: FencedQueueRepository factory returned nil (implementation lands AV3D-1.5)")
	}
	return repo
}

// Phase-1 activation history (no case is deferred any longer): AV3D-1.1 activated
// lease-fencing (ClaimFencingOnStaleLease); AV3D-1.2 activated the logical-key
// admission/supersession and concurrency cases; AV3D-1.3 activated the
// claimed-payload checkpoint cases; AV3D-1.4 activated the retry-at, permanent
// dead-letter, and bounded-purge cases. Every case below now RUNS against the
// memstore reference and stays load-bearing for the pgx/turso live suites at
// AV3D-1.5; none may be re-skipped or weakened to make an implementation pass.

// testFencedIDsAndKeys: JobID is the unique execution ID; LogicalKey is optional.
// Two keyless enqueues get distinct generated IDs; a duplicate explicit ID is
// rejected already-exists.
func testFencedIDsAndKeys(t *testing.T, newRepo func(t *testing.T) job.FencedQueueRepository) {
	repo := requireFenced(t, newRepo)
	ctx := context.Background()

	a := mustEnqueueOnce(t, repo, job.Enqueue{Kind: "email"})
	b := mustEnqueueOnce(t, repo, job.Enqueue{Kind: "email"})
	if a.ID() == "" || b.ID() == "" || a.ID() == b.ID() {
		t.Errorf("keyless EnqueueOnce must yield distinct non-empty execution ids, got %q and %q", a.ID(), b.ID())
	}

	if _, err := repo.EnqueueOnce(ctx, job.Enqueue{ID: "exec-1", Kind: "email"}); err != nil {
		t.Fatalf("first explicit-id EnqueueOnce: %v", err)
	}
	if _, err := repo.EnqueueOnce(ctx, job.Enqueue{ID: "exec-1", Kind: "email"}); !errors.Is(err, sdk.ErrAlreadyExists) {
		t.Errorf("duplicate execution id: err=%v, want sdk.ErrAlreadyExists", err)
	}
}

// testFencedEnqueueOnce: a second EnqueueOnce under an active logical key returns
// the current active execution, creating no second generation.
func testFencedEnqueueOnce(t *testing.T, newRepo func(t *testing.T) job.FencedQueueRepository) {
	repo := requireFenced(t, newRepo)
	ctx := context.Background()

	first := mustEnqueueOnce(t, repo, job.Enqueue{Kind: "email", LogicalKey: "k1"})
	again, err := repo.EnqueueOnce(ctx, job.Enqueue{Kind: "email", LogicalKey: "k1"})
	if err != nil {
		t.Fatalf("second EnqueueOnce: %v", err)
	}
	if again.ID() != first.ID() {
		t.Errorf("EnqueueOnce on an active key returned %q, want the existing active execution %q", again.ID(), first.ID())
	}
}

// testFencedReplaceSupersedes: Replace supersedes the active generation
// (StatusSuperseded, terminal) and inserts one fresh pending execution.
func testFencedReplaceSupersedes(t *testing.T, newRepo func(t *testing.T) job.FencedQueueRepository) {
	repo := requireFenced(t, newRepo)
	ctx := context.Background()
	now := time.Now().UTC()

	first := mustEnqueueOnce(t, repo, job.Enqueue{Kind: "email", LogicalKey: "k1", ScheduledFor: now})
	second, err := repo.Replace(ctx, job.Enqueue{Kind: "email", LogicalKey: "k1", ScheduledFor: now})
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if second.ID() == first.ID() {
		t.Fatalf("Replace must insert a new execution id, got the prior id %q", first.ID())
	}

	old, err := repo.Get(ctx, first.ID())
	if err != nil {
		t.Fatalf("Get superseded: %v", err)
	}
	if old.JobStatus != job.StatusSuperseded || !old.Terminal() {
		t.Errorf("prior generation status=%q terminal=%v, want superseded/terminal", old.JobStatus, old.Terminal())
	}
	fresh, err := repo.Get(ctx, second.ID())
	if err != nil {
		t.Fatalf("Get replacement: %v", err)
	}
	if fresh.JobStatus != job.StatusPending {
		t.Errorf("replacement status=%q, want pending", fresh.JobStatus)
	}
}

// testFencedLatestByKey: the latest-by-key projection returns the newest
// generation deterministically, even across superseded tombstones.
func testFencedLatestByKey(t *testing.T, newRepo func(t *testing.T) job.FencedQueueRepository) {
	repo := requireFenced(t, newRepo)
	ctx := context.Background()

	mustEnqueueOnce(t, repo, job.Enqueue{Kind: "email", LogicalKey: "k1"})
	latest, err := repo.Replace(ctx, job.Enqueue{Kind: "email", LogicalKey: "k1"})
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}

	got, err := repo.GetLatestByKey(ctx, "k1")
	if err != nil {
		t.Fatalf("GetLatestByKey: %v", err)
	}
	if got.ID() != latest.ID() {
		t.Errorf("GetLatestByKey = %q, want the newest generation %q", got.ID(), latest.ID())
	}
	if _, err := repo.GetLatestByKey(ctx, "absent"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("GetLatestByKey(absent): err=%v, want sdk.ErrNotFound", err)
	}
}

// testFencedTerminalStates: Cancel yields StatusCanceled and Replace yields
// StatusSuperseded — both terminal, both distinct, both stamping TerminalAt.
func testFencedTerminalStates(t *testing.T, newRepo func(t *testing.T) job.FencedQueueRepository) {
	repo := requireFenced(t, newRepo)
	ctx := context.Background()
	now := time.Now().UTC()

	canceled := mustEnqueueOnce(t, repo, job.Enqueue{Kind: "email", LogicalKey: "kc"})
	if err := repo.Cancel(ctx, canceled.ID(), now); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	got, _ := repo.Get(ctx, canceled.ID())
	if got.JobStatus != job.StatusCanceled || !got.Terminal() || got.TerminalAt == nil {
		t.Errorf("canceled job status=%q terminal=%v terminalAt=%v, want canceled/terminal/set", got.JobStatus, got.Terminal(), got.TerminalAt)
	}
	// Cancel is idempotent on an already-canceled job.
	if err := repo.Cancel(ctx, canceled.ID(), now); err != nil {
		t.Errorf("second Cancel: err=%v, want idempotent nil", err)
	}

	superseded := mustEnqueueOnce(t, repo, job.Enqueue{Kind: "email", LogicalKey: "ks"})
	if _, err := repo.Replace(ctx, job.Enqueue{Kind: "email", LogicalKey: "ks"}); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	old, _ := repo.Get(ctx, superseded.ID())
	if old.JobStatus != job.StatusSuperseded || !old.Terminal() {
		t.Errorf("superseded job status=%q terminal=%v, want superseded/terminal", old.JobStatus, old.Terminal())
	}
}

// testFencedConcurrentEnqueueOnce: many concurrent EnqueueOnce calls under one
// logical key admit EXACTLY ONE execution — every caller receives that single
// active execution id and no second generation is created (one-winner admission).
// Run under -race this proves the atomic enqueue-once has no lost-update window: if
// two goroutines each inserted, the callers would observe distinct ids.
func testFencedConcurrentEnqueueOnce(t *testing.T, newRepo func(t *testing.T) job.FencedQueueRepository) {
	repo := requireFenced(t, newRepo)
	ctx := context.Background()

	const n = 16
	ids := make([]string, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			j, err := repo.EnqueueOnce(ctx, job.Enqueue{Kind: "email", LogicalKey: "race"})
			if err != nil {
				t.Errorf("concurrent EnqueueOnce: %v", err)
				return
			}
			ids[i] = j.ID()
		}(i)
	}
	wg.Wait()

	winner := ids[0]
	if winner == "" {
		t.Fatal("concurrent EnqueueOnce produced an empty execution id")
	}
	for i, id := range ids {
		if id != winner {
			t.Fatalf("EnqueueOnce winner not unique: ids[%d]=%q != ids[0]=%q (a lost-update double insert)", i, id, winner)
		}
	}
	// The single admitted execution is the latest generation and is still active —
	// EnqueueOnce never supersedes, so no tombstone can exist under the key.
	latest, err := repo.GetLatestByKey(ctx, "race")
	if err != nil {
		t.Fatalf("GetLatestByKey: %v", err)
	}
	if latest.ID() != winner {
		t.Errorf("GetLatestByKey = %q, want the single admitted execution %q", latest.ID(), winner)
	}
	if latest.Terminal() {
		t.Errorf("the single admitted execution is terminal (%q); want an active generation", latest.JobStatus)
	}
}

// testFencedConcurrentReplaceVsEnqueueOnce: a Replace racing an EnqueueOnce under
// the same active key resolves to exactly one active generation with no lost
// update. Replace always wins the active slot — it inserts a fresh execution and
// supersedes the prior one — while the concurrent EnqueueOnce observes either the
// pre-Replace generation or the replacement, never creating a competing third
// active generation. Run under -race.
func testFencedConcurrentReplaceVsEnqueueOnce(t *testing.T, newRepo func(t *testing.T) job.FencedQueueRepository) {
	repo := requireFenced(t, newRepo)
	ctx := context.Background()

	seed := mustEnqueueOnce(t, repo, job.Enqueue{Kind: "email", LogicalKey: "race"})

	var (
		wg         sync.WaitGroup
		replaceID  string
		replaceErr error
		enqueueID  string
		enqueueErr error
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		j, err := repo.Replace(ctx, job.Enqueue{Kind: "email", LogicalKey: "race"})
		replaceID, replaceErr = j.ID(), err
	}()
	go func() {
		defer wg.Done()
		j, err := repo.EnqueueOnce(ctx, job.Enqueue{Kind: "email", LogicalKey: "race"})
		enqueueID, enqueueErr = j.ID(), err
	}()
	wg.Wait()

	if replaceErr != nil {
		t.Fatalf("concurrent Replace: %v", replaceErr)
	}
	if enqueueErr != nil {
		t.Fatalf("concurrent EnqueueOnce: %v", enqueueErr)
	}
	if replaceID == seed.ID() {
		t.Fatalf("Replace must insert a fresh execution, got the seed id %q", seed.ID())
	}
	// EnqueueOnce created no third generation: it returned either the seed (it ran
	// before Replace) or the replacement (it ran after).
	if enqueueID != seed.ID() && enqueueID != replaceID {
		t.Errorf("EnqueueOnce returned %q, want the seed %q or the replacement %q (no third generation)", enqueueID, seed.ID(), replaceID)
	}

	// The deterministic active generation is the replacement (newest CreatedAt); the
	// seed is a superseded tombstone regardless of interleaving.
	latest, err := repo.GetLatestByKey(ctx, "race")
	if err != nil {
		t.Fatalf("GetLatestByKey: %v", err)
	}
	if latest.ID() != replaceID {
		t.Errorf("GetLatestByKey = %q, want the replacement %q", latest.ID(), replaceID)
	}
	if latest.Terminal() {
		t.Errorf("the winning generation is terminal (%q); want an active generation", latest.JobStatus)
	}
	seedRow, err := repo.Get(ctx, seed.ID())
	if err != nil {
		t.Fatalf("Get seed: %v", err)
	}
	if seedRow.JobStatus != job.StatusSuperseded || !seedRow.Terminal() {
		t.Errorf("seed generation status=%q terminal=%v, want superseded/terminal", seedRow.JobStatus, seedRow.Terminal())
	}
}

// testFencedReplaceFencesRunningClaim: Replace supersedes even a RUNNING generation
// and fences its claim. After the resend, the old lease holder's Checkpoint,
// Complete, and Fail all return sdk.ErrConflict against the retired generation, and
// the fresh replacement is the one active generation. This is the resend-during
// -delivery case: the queue prevents the superseded worker from recording a
// checkpoint or completion against the retired generation. It CANNOT retract a
// provider call that worker already made — that irreducible race is documented on
// FencedQueueRepository.Replace and is a provider-side effect the queue never sees.
func testFencedReplaceFencesRunningClaim(t *testing.T, newRepo func(t *testing.T) job.FencedQueueRepository) {
	repo := requireFenced(t, newRepo)
	ctx := context.Background()
	now := time.Now().UTC()

	seed := mustEnqueueOnce(t, repo, job.Enqueue{Kind: "email", LogicalKey: "k1", ScheduledFor: now})
	claimed, err := repo.Claim(ctx, time.Now().UTC(), "lease-A", Lease)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if claimed.ID() != seed.ID() {
		t.Fatalf("Claim = %q, want the seed %q", claimed.ID(), seed.ID())
	}

	// A user resend supersedes the still-running generation.
	replacement, err := repo.Replace(ctx, job.Enqueue{Kind: "email", LogicalKey: "k1", ScheduledFor: now})
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if replacement.ID() == seed.ID() {
		t.Fatalf("Replace must insert a fresh execution, got the seed id %q", seed.ID())
	}

	// The old lease holder is fenced out of every transition even though its lease
	// has not yet expired — supersession, not lease expiry, retires the claim.
	staleNow := time.Now().UTC()
	if err := repo.Checkpoint(ctx, seed.ID(), "lease-A", []byte(`{"cipher":"AAAA"}`), staleNow); !errors.Is(err, sdk.ErrConflict) {
		t.Errorf("superseded Checkpoint: err=%v, want sdk.ErrConflict", err)
	}
	if err := repo.Complete(ctx, seed.ID(), "lease-A", staleNow); !errors.Is(err, sdk.ErrConflict) {
		t.Errorf("superseded Complete: err=%v, want sdk.ErrConflict", err)
	}
	if err := repo.Fail(ctx, seed.ID(), "lease-A", "stale", staleNow); !errors.Is(err, sdk.ErrConflict) {
		t.Errorf("superseded Fail: err=%v, want sdk.ErrConflict", err)
	}

	// The superseded generation is a terminal tombstone; the replacement is the
	// single active generation the status projection returns.
	old, err := repo.Get(ctx, seed.ID())
	if err != nil {
		t.Fatalf("Get superseded: %v", err)
	}
	if old.JobStatus != job.StatusSuperseded || !old.Terminal() {
		t.Errorf("superseded generation status=%q terminal=%v, want superseded/terminal", old.JobStatus, old.Terminal())
	}
	latest, err := repo.GetLatestByKey(ctx, "k1")
	if err != nil {
		t.Fatalf("GetLatestByKey: %v", err)
	}
	if latest.ID() != replacement.ID() {
		t.Errorf("GetLatestByKey = %q, want the replacement %q", latest.ID(), replacement.ID())
	}
}

// testFencedClaimFencing: after a lease is reclaimed, the old lease holder cannot
// checkpoint, complete, or fail — every fenced op returns sdk.ErrConflict — while
// the current holder can complete.
func testFencedClaimFencing(t *testing.T, newRepo func(t *testing.T) job.FencedQueueRepository) {
	repo := requireFenced(t, newRepo)
	ctx := context.Background()
	now := time.Now().UTC()

	mustEnqueueOnce(t, repo, job.Enqueue{Kind: "email", LogicalKey: "k1", ScheduledFor: now})

	first, err := repo.Claim(ctx, time.Now().UTC(), "lease-A", Lease)
	if err != nil {
		t.Fatalf("first Claim: %v", err)
	}
	if first.LeaseID != "lease-A" {
		t.Fatalf("first Claim LeaseID=%q, want lease-A", first.LeaseID)
	}

	// Let the lease expire, then reclaim under a fresh lease.
	time.Sleep(Lease + 100*time.Millisecond)
	second, err := repo.Claim(ctx, time.Now().UTC(), "lease-B", Lease)
	if err != nil {
		t.Fatalf("reclaim Claim: %v", err)
	}
	if second.ID() != first.ID() || second.LeaseID != "lease-B" {
		t.Fatalf("reclaim = id %q lease %q, want id %q lease-B", second.ID(), second.LeaseID, first.ID())
	}

	// The stale lease holder is fenced out of every mutating op.
	staleNow := time.Now().UTC()
	if err := repo.Checkpoint(ctx, first.ID(), "lease-A", []byte(`{"x":1}`), staleNow); !errors.Is(err, sdk.ErrConflict) {
		t.Errorf("stale Checkpoint: err=%v, want sdk.ErrConflict", err)
	}
	if err := repo.Complete(ctx, first.ID(), "lease-A", staleNow); !errors.Is(err, sdk.ErrConflict) {
		t.Errorf("stale Complete: err=%v, want sdk.ErrConflict", err)
	}
	if err := repo.Fail(ctx, first.ID(), "lease-A", "stale", staleNow); !errors.Is(err, sdk.ErrConflict) {
		t.Errorf("stale Fail: err=%v, want sdk.ErrConflict", err)
	}

	// The current holder completes cleanly, and a repeat from the same holder is
	// idempotent (the current owner can complete/fail idempotently, AV3D-1.1).
	if err := repo.Complete(ctx, second.ID(), "lease-B", time.Now().UTC()); err != nil {
		t.Errorf("current Complete: %v", err)
	}
	if err := repo.Complete(ctx, second.ID(), "lease-B", time.Now().UTC()); err != nil {
		t.Errorf("idempotent repeat Complete from the current holder: err=%v, want nil", err)
	}
}

// testFencedCheckpoint: a checkpoint from the current lease atomically replaces the
// payload BYTE-FOR-BYTE — including arbitrary non-UTF8 encrypted ciphertext — while
// preserving job identity, attempts, logical key, schedule, and running status. It
// changes ONLY the payload. A stale lease cannot checkpoint. This is the durable
// checkpoint an opaque delivery records before its side effect so every retry
// resends the identical rendered secret.
func testFencedCheckpoint(t *testing.T, newRepo func(t *testing.T) job.FencedQueueRepository) {
	repo := requireFenced(t, newRepo)
	ctx := context.Background()
	now := time.Now().UTC()

	seed := mustEnqueueOnce(t, repo, job.Enqueue{Kind: "email", LogicalKey: "k1", Payload: []byte(`{"stage":"opaque"}`), ScheduledFor: now})
	claimed, err := repo.Claim(ctx, time.Now().UTC(), "lease-A", Lease)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	// Arbitrary encrypted ciphertext: non-UTF8 bytes that are not valid JSON. The
	// store must persist and return them byte-for-byte — a BYTEA/BLOB column, never a
	// TEXT column that would mangle, re-encode, or reject them.
	rendered := []byte{0x00, 0x01, 0xff, 0xfe, 0x80, 0x7f, 0x00, 0xab}
	if err := repo.Checkpoint(ctx, claimed.ID(), "lease-A", rendered, time.Now().UTC()); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}

	got, err := repo.Get(ctx, claimed.ID())
	if err != nil {
		t.Fatalf("Get after checkpoint: %v", err)
	}
	if !bytes.Equal(got.Payload, rendered) {
		t.Errorf("checkpointed payload = %#v, want byte-exact %#v", []byte(got.Payload), rendered)
	}
	// Checkpoint replaces only the payload: identity, attempts, logical key, schedule,
	// and running status are preserved.
	if got.ID() != claimed.ID() {
		t.Errorf("checkpoint changed identity: id=%q, want %q", got.ID(), claimed.ID())
	}
	if got.JobStatus != job.StatusRunning {
		t.Errorf("checkpoint changed status=%q, want running", got.JobStatus)
	}
	if got.Retries != claimed.Retries {
		t.Errorf("checkpoint changed attempts: retries=%d, want %d", got.Retries, claimed.Retries)
	}
	if got.LogicalKey != seed.LogicalKey {
		t.Errorf("checkpoint changed logical key: %q, want %q", got.LogicalKey, seed.LogicalKey)
	}
	if !got.ScheduledFor.Equal(seed.ScheduledFor) {
		t.Errorf("checkpoint changed schedule: %v, want %v", got.ScheduledFor, seed.ScheduledFor)
	}

	// A stale lease cannot checkpoint.
	if err := repo.Checkpoint(ctx, claimed.ID(), "lease-Z", []byte(`{"x":1}`), time.Now().UTC()); !errors.Is(err, sdk.ErrConflict) {
		t.Errorf("stale-lease Checkpoint: err=%v, want sdk.ErrConflict", err)
	}
}

// testFencedCheckpointBeforeSideEffect proves the consumer-ordering guarantee the
// checkpoint enables: a caller checkpoints BEFORE its external side effect and does
// not perform that side effect unless the checkpoint succeeded. The queue supplies
// the seam — Checkpoint returns an error (sdk.ErrConflict for a stale or superseded
// lease) — and a correctly-ordered consumer returns without sending on that error.
// The pattern is structural (the caller checks the error), so this case locks the
// ordering against a modeled consumer; FencedQueueRepository.Checkpoint states the
// same contract. There is no FencedRunner checkpoint seam yet — no consumer wires
// one until the phase-2 auth processor — so the ordering is proven here, not in the
// kernel.
func testFencedCheckpointBeforeSideEffect(t *testing.T, newRepo func(t *testing.T) job.FencedQueueRepository) {
	repo := requireFenced(t, newRepo)
	ctx := context.Background()
	now := time.Now().UTC()

	// checkpointThenSend models the processor step: checkpoint the rendered payload,
	// and only invoke the side effect (send) once the checkpoint has succeeded.
	sent := false
	checkpointThenSend := func(id, lease string, payload []byte) error {
		if err := repo.Checkpoint(ctx, id, lease, payload, time.Now().UTC()); err != nil {
			return err
		}
		sent = true
		return nil
	}

	mustEnqueueOnce(t, repo, job.Enqueue{Kind: "email", LogicalKey: "k1", ScheduledFor: now})
	claimed, err := repo.Claim(ctx, time.Now().UTC(), "lease-A", Lease)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	// Current lease: the checkpoint succeeds, so the side effect proceeds.
	if err := checkpointThenSend(claimed.ID(), "lease-A", []byte(`{"cipher":"AAAA"}`)); err != nil {
		t.Fatalf("checkpointThenSend under the current lease: %v", err)
	}
	if !sent {
		t.Fatal("side effect did not run after a successful checkpoint")
	}

	// Stale lease: the checkpoint is fenced, so the side effect MUST NOT run.
	sent = false
	if err := checkpointThenSend(claimed.ID(), "lease-STALE", []byte(`{"cipher":"BBBB"}`)); !errors.Is(err, sdk.ErrConflict) {
		t.Errorf("fenced checkpointThenSend: err=%v, want sdk.ErrConflict", err)
	}
	if sent {
		t.Error("side effect ran after a fenced checkpoint — checkpoint failure did not prevent the side effect")
	}
}

// testFencedCheckpointCrashReclaim proves durability across a crash: a worker
// checkpoints its rendered payload, then "crashes" — its lease lapses with no
// Complete or Fail. A later worker reclaims the same execution and reads the
// checkpointed bytes verbatim, so a retry after a crash resends the identical
// rendered secret rather than re-rendering. Uses a real lease-expiry sleep.
func testFencedCheckpointCrashReclaim(t *testing.T, newRepo func(t *testing.T) job.FencedQueueRepository) {
	repo := requireFenced(t, newRepo)
	ctx := context.Background()
	now := time.Now().UTC()

	mustEnqueueOnce(t, repo, job.Enqueue{Kind: "email", LogicalKey: "k1", Payload: []byte(`{"stage":"opaque"}`), ScheduledFor: now})
	claimed, err := repo.Claim(ctx, time.Now().UTC(), "lease-A", Lease)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	rendered := []byte{0x00, 0xff, 0x10, 0x80, 0x01, 0xfe}
	if err := repo.Checkpoint(ctx, claimed.ID(), "lease-A", rendered, time.Now().UTC()); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}

	// The worker crashes: no Complete/Fail. The lease lapses and the job is reclaimed.
	time.Sleep(Lease + 100*time.Millisecond)
	reclaimed, err := repo.Claim(ctx, time.Now().UTC(), "lease-B", Lease)
	if err != nil {
		t.Fatalf("reclaim after crash: %v", err)
	}
	if reclaimed.ID() != claimed.ID() {
		t.Fatalf("reclaim = %q, want the crashed execution %q", reclaimed.ID(), claimed.ID())
	}
	if !bytes.Equal(reclaimed.Payload, rendered) {
		t.Errorf("reclaimed payload = %#v, want the checkpointed bytes %#v", []byte(reclaimed.Payload), rendered)
	}
}

// testFencedConcurrentCheckpointVsReplace: a Checkpoint under the running claim
// racing a Replace of the same logical key has ONE valid outcome and never loses the
// current generation. Either the checkpoint lands before the replace (it succeeds,
// and the seed carries the checkpointed bytes into its superseded tombstone) or the
// replace lands first (the checkpoint is fenced with sdk.ErrConflict) — no other
// result is legal. In both interleavings the fresh replacement is the single active
// generation the status projection returns, so no current generation is lost. Run
// under -race.
func testFencedConcurrentCheckpointVsReplace(t *testing.T, newRepo func(t *testing.T) job.FencedQueueRepository) {
	repo := requireFenced(t, newRepo)
	ctx := context.Background()
	now := time.Now().UTC()

	seed := mustEnqueueOnce(t, repo, job.Enqueue{Kind: "email", LogicalKey: "race", ScheduledFor: now})
	claimed, err := repo.Claim(ctx, time.Now().UTC(), "lease-A", Lease)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if claimed.ID() != seed.ID() {
		t.Fatalf("Claim = %q, want the seed %q", claimed.ID(), seed.ID())
	}

	rendered := []byte{0xde, 0xad, 0xbe, 0xef}
	var (
		wg            sync.WaitGroup
		checkpointErr error
		replaceID     string
		replaceErr    error
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		checkpointErr = repo.Checkpoint(ctx, seed.ID(), "lease-A", rendered, time.Now().UTC())
	}()
	go func() {
		defer wg.Done()
		j, err := repo.Replace(ctx, job.Enqueue{Kind: "email", LogicalKey: "race", ScheduledFor: now})
		replaceID, replaceErr = j.ID(), err
	}()
	wg.Wait()

	if replaceErr != nil {
		t.Fatalf("concurrent Replace: %v", replaceErr)
	}
	// The checkpoint either won the race (nil) or was fenced by the supersession
	// (conflict). Any other error is a contract violation.
	if checkpointErr != nil && !errors.Is(checkpointErr, sdk.ErrConflict) {
		t.Fatalf("concurrent Checkpoint: err=%v, want nil or sdk.ErrConflict", checkpointErr)
	}

	// The replacement is the single active generation regardless of interleaving —
	// no current generation was lost.
	latest, err := repo.GetLatestByKey(ctx, "race")
	if err != nil {
		t.Fatalf("GetLatestByKey: %v", err)
	}
	if latest.ID() != replaceID {
		t.Errorf("GetLatestByKey = %q, want the replacement %q", latest.ID(), replaceID)
	}
	if latest.Terminal() {
		t.Errorf("the current generation is terminal (%q); want an active generation", latest.JobStatus)
	}

	// The seed is a superseded terminal tombstone. If the checkpoint won the race it
	// carries the checkpointed bytes; if it was fenced it keeps its pre-checkpoint
	// payload. Either is valid — the invariant is that the seed is retired and the
	// replacement is live.
	seedRow, err := repo.Get(ctx, seed.ID())
	if err != nil {
		t.Fatalf("Get seed: %v", err)
	}
	if seedRow.JobStatus != job.StatusSuperseded || !seedRow.Terminal() {
		t.Errorf("seed generation status=%q terminal=%v, want superseded/terminal", seedRow.JobStatus, seedRow.Terminal())
	}
	if checkpointErr == nil && !bytes.Equal(seedRow.Payload, rendered) {
		t.Errorf("checkpoint reported success but seed payload = %#v, want the checkpointed bytes %#v", []byte(seedRow.Payload), rendered)
	}
}

// testFencedRetryAt: Reschedule moves a claimed job to a future availableAt and
// clears the lease; it is not claimable before that time and is claimable at/after.
func testFencedRetryAt(t *testing.T, newRepo func(t *testing.T) job.FencedQueueRepository) {
	repo := requireFenced(t, newRepo)
	ctx := context.Background()
	// Truncate to microsecond so the caller-supplied retry-at compares Equal after
	// a round-trip through a microsecond-precision column (postgres TIMESTAMPTZ) as
	// well as the nanosecond memory/TEXT stores — a precision alignment, not a
	// relaxation of the schedule contract.
	now := time.Now().UTC().Truncate(time.Microsecond)

	mustEnqueueOnce(t, repo, job.Enqueue{Kind: "email", LogicalKey: "k1", ScheduledFor: now})
	claimed, err := repo.Claim(ctx, now, "lease-A", Lease)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	retryAt := now.Add(time.Hour)
	if err := repo.Reschedule(ctx, claimed.ID(), "lease-A", retryAt, "transient", now); err != nil {
		t.Fatalf("Reschedule: %v", err)
	}

	// Reschedule clears the lease and moves the job back to pending, recording the
	// reason — retry-at is a durable requeue, not a live claim, so the busy-loop the
	// unfenced immediate-requeue would cause cannot happen here.
	backoff, err := repo.Get(ctx, claimed.ID())
	if err != nil {
		t.Fatalf("Get rescheduled: %v", err)
	}
	if backoff.JobStatus != job.StatusPending {
		t.Errorf("rescheduled status=%q, want pending", backoff.JobStatus)
	}
	if backoff.LeaseID != "" || backoff.Leased(now) {
		t.Errorf("rescheduled job still leased: leaseID=%q leased=%v, want cleared", backoff.LeaseID, backoff.Leased(now))
	}
	if !backoff.ScheduledFor.Equal(retryAt) {
		t.Errorf("rescheduled ScheduledFor=%v, want retry-at %v", backoff.ScheduledFor, retryAt)
	}
	if backoff.FailureReason != "transient" {
		t.Errorf("rescheduled FailureReason=%q, want %q", backoff.FailureReason, "transient")
	}
	// The lease Reschedule cleared is fenced: the old holder cannot complete it.
	if err := repo.Complete(ctx, claimed.ID(), "lease-A", now); !errors.Is(err, sdk.ErrConflict) {
		t.Errorf("Complete under the rescheduled-away lease: err=%v, want sdk.ErrConflict", err)
	}

	// Not due before retryAt — a busy-loop worker polling every minute never
	// re-serves it until its retry time.
	if _, err := repo.Claim(ctx, now.Add(time.Minute), "lease-B", Lease); !errors.Is(err, workers.ErrNoWork) {
		t.Errorf("Claim before retry-at: err=%v, want workers.ErrNoWork", err)
	}
	// Due at/after retryAt.
	again, err := repo.Claim(ctx, retryAt.Add(time.Minute), "lease-C", Lease)
	if err != nil {
		t.Fatalf("Claim after retry-at: %v", err)
	}
	if again.ID() != claimed.ID() {
		t.Errorf("re-claim = %q, want %q", again.ID(), claimed.ID())
	}
}

// testFencedPermanentFail: Fail permanently dead-letters and the job is never
// claimed again.
func testFencedPermanentFail(t *testing.T, newRepo func(t *testing.T) job.FencedQueueRepository) {
	repo := requireFenced(t, newRepo)
	ctx := context.Background()
	now := time.Now().UTC()

	mustEnqueueOnce(t, repo, job.Enqueue{Kind: "email", LogicalKey: "k1", ScheduledFor: now})
	claimed, err := repo.Claim(ctx, now, "lease-A", Lease)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := repo.Fail(ctx, claimed.ID(), "lease-A", "permanent", now); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	dead, _ := repo.Get(ctx, claimed.ID())
	if dead.JobStatus != job.StatusDeadLetter || !dead.Terminal() {
		t.Errorf("failed job status=%q terminal=%v, want dead_letter/terminal", dead.JobStatus, dead.Terminal())
	}
	if _, err := repo.Claim(ctx, now.Add(Lease+time.Minute), "lease-B", Lease); !errors.Is(err, workers.ErrNoWork) {
		t.Errorf("Claim after dead-letter: err=%v, want workers.ErrNoWork", err)
	}
}

// testFencedPurge: PurgeTerminal removes at most limit terminal jobs whose
// TerminalAt is at or before the cutoff, and never a non-terminal job.
func testFencedPurge(t *testing.T, newRepo func(t *testing.T) job.FencedQueueRepository) {
	repo := requireFenced(t, newRepo)
	ctx := context.Background()
	now := time.Now().UTC()

	// Three terminal (canceled) jobs at now, one live pending job, and one terminal
	// job canceled AFTER the cutoff (so purge-by-terminal-time must spare it).
	for i := 0; i < 3; i++ {
		j := mustEnqueueOnce(t, repo, job.Enqueue{Kind: "email"})
		if err := repo.Cancel(ctx, j.ID(), now); err != nil {
			t.Fatalf("Cancel %d: %v", i, err)
		}
	}
	live := mustEnqueueOnce(t, repo, job.Enqueue{Kind: "email", ScheduledFor: now})
	future := mustEnqueueOnce(t, repo, job.Enqueue{Kind: "email"})
	if err := repo.Cancel(ctx, future.ID(), now.Add(time.Hour)); err != nil {
		t.Fatalf("Cancel future: %v", err)
	}

	// Bounded by limit: three terminal-at-or-before-cutoff jobs exist, but the batch
	// removes at most the requested 2.
	cutoff := now.Add(time.Minute)
	removed, err := repo.PurgeTerminal(ctx, cutoff, 2)
	if err != nil {
		t.Fatalf("PurgeTerminal: %v", err)
	}
	if removed != 2 {
		t.Errorf("PurgeTerminal(limit 2) removed %d, want the bounded 2", removed)
	}
	// A second bounded batch drains the third due terminal job and no more (the
	// future-terminal job is past the cutoff; the live job is non-terminal).
	removed, err = repo.PurgeTerminal(ctx, cutoff, 2)
	if err != nil {
		t.Fatalf("second PurgeTerminal: %v", err)
	}
	if removed != 1 {
		t.Errorf("second PurgeTerminal removed %d, want the single remaining due terminal job", removed)
	}
	// The live job and the after-cutoff terminal job both survive: purge is by
	// terminal time, and never touches a non-terminal job.
	if _, err := repo.Get(ctx, live.ID()); err != nil {
		t.Errorf("live job removed by purge: %v", err)
	}
	if _, err := repo.Get(ctx, future.ID()); err != nil {
		t.Errorf("after-cutoff terminal job removed by purge: %v", err)
	}
}

// testFencedErrorKinds: deterministic sdk error kinds for the not-found,
// already-exists, and conflict edges.
func testFencedErrorKinds(t *testing.T, newRepo func(t *testing.T) job.FencedQueueRepository) {
	repo := requireFenced(t, newRepo)
	ctx := context.Background()
	now := time.Now().UTC()

	if _, err := repo.Get(ctx, "nope"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Get(absent): err=%v, want sdk.ErrNotFound", err)
	}
	if err := repo.Complete(ctx, "nope", "lease-A", now); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Complete(absent): err=%v, want sdk.ErrNotFound", err)
	}
	if err := repo.Cancel(ctx, "nope", now); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Cancel(absent): err=%v, want sdk.ErrNotFound", err)
	}

	// A completed job cannot be canceled — a terminal-state conflict. Prove this
	// against an otherwise-empty store so Claim (oldest-due first) returns exactly
	// the job under test and not an unrelated leftover pending generation.
	done := mustEnqueueOnce(t, repo, job.Enqueue{Kind: "email", ScheduledFor: now})
	claimed, err := repo.Claim(ctx, now, "lease-A", Lease)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if claimed.ID() != done.ID() {
		t.Fatalf("Claim = %q, want the only pending job %q", claimed.ID(), done.ID())
	}
	if err := repo.Complete(ctx, claimed.ID(), "lease-A", now); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if err := repo.Cancel(ctx, done.ID(), now); !errors.Is(err, sdk.ErrConflict) {
		t.Errorf("Cancel(completed): err=%v, want sdk.ErrConflict", err)
	}

	if _, err := repo.EnqueueOnce(ctx, job.Enqueue{ID: "dup", Kind: "email"}); err != nil {
		t.Fatalf("first EnqueueOnce: %v", err)
	}
	if _, err := repo.EnqueueOnce(ctx, job.Enqueue{ID: "dup", Kind: "email"}); !errors.Is(err, sdk.ErrAlreadyExists) {
		t.Errorf("duplicate execution id: err=%v, want sdk.ErrAlreadyExists", err)
	}
}

// mustEnqueueOnce inserts one execution via EnqueueOnce, failing the test on error.
func mustEnqueueOnce(t *testing.T, repo job.FencedQueueRepository, in job.Enqueue) job.Job {
	t.Helper()
	j, err := repo.EnqueueOnce(context.Background(), in)
	if err != nil {
		t.Fatalf("EnqueueOnce %q/%q: %v", in.ID, in.LogicalKey, err)
	}
	return j
}
