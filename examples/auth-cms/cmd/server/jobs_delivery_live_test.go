//go:build livedelivery

// This file is the AV3D-3.5 LIVE run-and-look delivery proof harness. It is
// build-tag gated (`livedelivery`) so the default hermetic `make check` / `go
// test` never compile it and the in-memory host's default build stays free of any
// datastore driver. It runs the SAME jobs-mode composition the host runs
// (auth.Service -> authjobs.Dispatcher -> generic jobs fenced queue ->
// jobs.FencedRuntime -> auth delivery processor), but against a LIVE pgx or turso
// fenced queue instead of the in-memory stand-in the hermetic proofs use.
//
// Run per dialect (both loud-skip without their env, constituting the open owner
// gate the AV3D-1.5 precedent established):
//
//	# postgres
//	POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' \
//	  go test -tags=livedelivery -run TestLiveJobsDeliveryPGX ./cmd/server
//
//	# turso / libsql
//	TURSO_DATABASE_URL=... TURSO_AUTH_TOKEN=... \
//	  go test -tags='livedelivery integration' -run TestLiveJobsDeliveryTurso ./cmd/server
//
// The proofs cover the phase-3 live list: known/unknown opaque admission parity;
// provider timeout + retry off the request path; process restart at each
// checkpoint boundary; resend converging to the latest generation; status and
// events carrying no secrets; and terminal cleanup + bounded purge. The dialect
// files (jobs_delivery_live_pgx_test.go / jobs_delivery_live_turso_test.go) supply
// the live store factory; this file owns the reusable inspecting wrapper and the
// proof bodies, reusing the hermetic harness's untagged helpers where types allow.
package main

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/authjobs"
	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/authmem"
	auth "github.com/gopernicus/gopernicus/features/authentication"
	"github.com/gopernicus/gopernicus/features/jobs"
	"github.com/gopernicus/gopernicus/features/jobs/domain/job"
	"github.com/gopernicus/gopernicus/sdk/capabilities/email"
	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
)

// liveInspectingQueue wraps ANY job.FencedQueueRepository (a live pgx/turso store)
// so a proof can (1) capture every payload byte-for-byte as it is persisted, (2)
// signal when a checkpoint lands, and (3) simulate a crash after provider acceptance
// by dropping the first Complete for a target execution. Unlike the hermetic
// inspectingQueue (which embeds the concrete in-memory store), this embeds the
// INTERFACE so it composes over a live-backed store; all non-overridden methods
// promote from the embedded implementation unchanged.
type liveInspectingQueue struct {
	job.FencedQueueRepository

	mu      sync.Mutex
	records []payloadRecord
	ids     map[string]struct{}

	checkpointCh chan string

	dropCompleteID string
	dropped        bool
	droppedCh      chan string
}

var _ job.FencedQueueRepository = (*liveInspectingQueue)(nil)

func newLiveInspectingQueue(store job.FencedQueueRepository) *liveInspectingQueue {
	return &liveInspectingQueue{FencedQueueRepository: store, ids: map[string]struct{}{}}
}

func (q *liveInspectingQueue) record(op, id string, payload json.RawMessage) {
	q.mu.Lock()
	defer q.mu.Unlock()
	b := make([]byte, len(payload))
	copy(b, payload)
	q.records = append(q.records, payloadRecord{op: op, jobID: id, payload: b})
	if id != "" {
		q.ids[id] = struct{}{}
	}
}

func (q *liveInspectingQueue) snapshot() []payloadRecord {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]payloadRecord, len(q.records))
	copy(out, q.records)
	return out
}

func (q *liveInspectingQueue) knownIDs() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]string, 0, len(q.ids))
	for id := range q.ids {
		out = append(out, id)
	}
	return out
}

func (q *liveInspectingQueue) EnqueueOnce(ctx context.Context, in job.Enqueue) (job.Job, error) {
	j, err := q.FencedQueueRepository.EnqueueOnce(ctx, in)
	if err == nil {
		q.record("enqueue_once", j.JobID, in.Payload)
	}
	return j, err
}

func (q *liveInspectingQueue) Replace(ctx context.Context, in job.Enqueue) (job.Job, error) {
	j, err := q.FencedQueueRepository.Replace(ctx, in)
	if err == nil {
		q.record("replace", j.JobID, in.Payload)
	}
	return j, err
}

func (q *liveInspectingQueue) Checkpoint(ctx context.Context, id, leaseID string, payload json.RawMessage, now time.Time) error {
	err := q.FencedQueueRepository.Checkpoint(ctx, id, leaseID, payload, now)
	if err == nil {
		q.record("checkpoint", id, payload)
		q.mu.Lock()
		ch := q.checkpointCh
		q.mu.Unlock()
		if ch != nil {
			select {
			case ch <- id:
			default:
			}
		}
	}
	return err
}

func (q *liveInspectingQueue) Complete(ctx context.Context, id, leaseID string, now time.Time) error {
	q.mu.Lock()
	drop := q.dropCompleteID != "" && id == q.dropCompleteID && !q.dropped
	if drop {
		q.dropped = true
	}
	ch := q.droppedCh
	q.mu.Unlock()
	if drop {
		// Model a crash after provider acceptance but before the completion commits:
		// the completion write is lost and the running claim's lease simply lapses,
		// leaving the job reclaimable for an at-least-once resend.
		if ch != nil {
			select {
			case ch <- id:
			default:
			}
		}
		return nil
	}
	return q.FencedQueueRepository.Complete(ctx, id, leaseID, now)
}

// liveRenderedSecretFor scans persisted payloads for the sealed rendered payload
// addressed to dest and returns its secret — the value a real recipient receives.
func liveRenderedSecretFor(recs []payloadRecord, enc cryptids.Encrypter, dest string) (string, bool) {
	for _, r := range recs {
		env, err := openSealed(enc, r.payload)
		if err != nil {
			continue
		}
		if env.Stage == "rendered" && strings.EqualFold(env.Destination, dest) && env.Secret != "" {
			return env.Secret, true
		}
	}
	return "", false
}

// liveOpaqueEnqueueID returns the execution id of the opaque admission whose sealed
// resolution input matches identifier (the enumeration-safe start).
func liveOpaqueEnqueueID(recs []payloadRecord, enc cryptids.Encrypter, identifier string) (string, bool) {
	for _, r := range recs {
		if r.op != "enqueue_once" && r.op != "replace" {
			continue
		}
		env, err := openSealed(enc, r.payload)
		if err != nil {
			continue
		}
		if env.Stage == "opaque" && strings.EqualFold(env.ResolutionInput, identifier) {
			return r.jobID, true
		}
	}
	return "", false
}

func liveCountOpaque(recs []payloadRecord, enc cryptids.Encrypter) int {
	n := 0
	for _, r := range recs {
		if env, err := openSealed(enc, r.payload); err == nil && env.Stage == "opaque" {
			n++
		}
	}
	return n
}

// hangingSender blocks each send until its (per-attempt) context is cancelled,
// standing in for a provider that never responds so the runtime's ProcessTimeout
// cuts each attempt and reschedules a retry — all off the request path.
type hangingSender struct {
	mu    sync.Mutex
	calls int
}

func (s *hangingSender) Send(ctx context.Context, _ email.Message) error {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	<-ctx.Done()
	return ctx.Err()
}

func (s *hangingSender) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// secretScanningEmitter records the JSON of every delivery lifecycle event so a
// proof can assert no secret/destination canary ever appears in an emitted event.
type secretScanningEmitter struct {
	mu    sync.Mutex
	blobs [][]byte
	types []string
}

func (e *secretScanningEmitter) Emit(_ context.Context, ev sdkevents.Event, _ ...sdkevents.EmitOption) error {
	b, _ := json.Marshal(ev)
	e.mu.Lock()
	e.blobs = append(e.blobs, b)
	e.types = append(e.types, ev.Type())
	e.mu.Unlock()
	return nil
}

func (e *secretScanningEmitter) snapshot() ([][]byte, []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([][]byte(nil), e.blobs...), append([]string(nil), e.types...)
}

// liveStoreFactory opens a fresh, migrated, empty live fenced queue (a dialect file
// supplies it).
type liveStoreFactory func(t *testing.T) job.FencedQueueRepository

// newLive builds a fresh inspecting live store and fresh in-memory auth repos (the
// delivery durability under proof is the LIVE jobs queue; the auth repos survive the
// modeled restart in-memory exactly as the hermetic proofs do).
func newLive(t *testing.T, open liveStoreFactory) (*liveInspectingQueue, auth.Repositories) {
	t.Helper()
	q := newLiveInspectingQueue(open(t))
	repos := authmem.New().Repositories()
	return q, repos
}

// waitLiveJobTerminal polls the live store until job id reaches a terminal state, so a
// proof stops its runtime only AFTER the durable Complete/Fail has committed. Against a
// live database, stopRuntime's ctx-cancel can arrive in the window between a successful
// Send and its Complete commit; the fenced runner then — correctly, per its at-least-once
// contract — leaves the job reclaimable, and a later worker resends it into a subsequent
// proof phase. That resend is a legitimate product behavior the harness itself
// manufactured by cutting the runtime mid-completion; a real deployment never does so.
// The in-memory twin never observes it because its Complete is an instant map write that
// always wins the race. Widening the stop margin to the durable terminal state removes
// the artifact without weakening any semantic assertion.
func waitLiveJobTerminal(t *testing.T, store *liveInspectingQueue, id string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if j, err := store.Get(context.Background(), id); err == nil && j.Terminal() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job %s did not reach a terminal state within 5s", id)
}

// waitLiveQueueDrained polls until every job the live store has observed is terminal, so
// a SETUP runtime (draining registration verification before the proof's real work) is
// stopped only after that work durably completed — otherwise the verification job is left
// reclaimable (see waitLiveJobTerminal) and resends into the proof's observation window.
func waitLiveQueueDrained(t *testing.T, store *liveInspectingQueue) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		allTerminal := true
		for _, id := range store.knownIDs() {
			j, err := store.Get(context.Background(), id)
			if err != nil {
				continue
			}
			if !j.Terminal() {
				allTerminal = false
				break
			}
		}
		if allTerminal {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("live queue did not drain to all-terminal within 5s")
}

// runLiveDeliveryProofs runs the AV3D-3.5 live proof list against the given dialect's
// fenced queue factory. Each subtest opens a fresh store (migrated + truncated) so
// leaves are isolated, and drives the real jobs-mode composition through bootDelivery.
func runLiveDeliveryProofs(t *testing.T, dialect string, open liveStoreFactory) {
	t.Run(dialect+"/KnownUnknownOpaqueAdmissionParity", func(t *testing.T) {
		liveKnownUnknownParity(t, open)
	})
	t.Run(dialect+"/ProviderTimeoutAndRetryOffRequestPath", func(t *testing.T) {
		liveProviderTimeoutRetry(t, open)
	})
	t.Run(dialect+"/RestartAfterOpaqueAdmission", func(t *testing.T) {
		liveRestartAfterOpaqueAdmission(t, open)
	})
	t.Run(dialect+"/RestartAfterCheckpointResendsSameSecret", func(t *testing.T) {
		liveRestartAfterCheckpoint(t, open)
	})
	t.Run(dialect+"/RestartAfterProviderAcceptanceResendsSameSecret", func(t *testing.T) {
		liveRestartAfterProviderAcceptance(t, open)
	})
	t.Run(dialect+"/ResendConvergesToLatestGeneration", func(t *testing.T) {
		liveResendConvergesToLatest(t, open)
	})
	t.Run(dialect+"/StatusAndEventsContainNoSecrets", func(t *testing.T) {
		liveStatusAndEventsNoSecrets(t, open)
	})
	t.Run(dialect+"/TerminalCleanupAndPurge", func(t *testing.T) {
		liveTerminalCleanupAndPurge(t, open)
	})
}

// liveAdmitVerifiedForgotPassword registers + verifies an account (draining the
// verification delivery) and admits an opaque forgot-password start with no runtime
// running, over the live store. It returns the delivery encrypter and the opaque
// execution id — the "state before the crash" the restart proofs build on.
func liveAdmitVerifiedForgotPassword(t *testing.T, store *liveInspectingQueue, repos auth.Repositories, addr string) (cryptids.Encrypter, string) {
	t.Helper()
	ctx := context.Background()
	drain := &captureSender{}
	admit := bootDelivery(t, repos, store, drain)
	if _, err := admit.svc.RegisterUser(ctx, addr, "correct-horse-battery-staple", "Live User"); err != nil {
		t.Fatalf("RegisterUser: %v", err)
	}
	c0, d0 := runRuntime(admit.runtime)
	waitMsgCount(t, drain, 1)
	// Drain the verification to a DURABLE terminal state before stopping, so it is not
	// left reclaimable to resend into a later phase (see waitLiveJobTerminal).
	waitLiveQueueDrained(t, store)
	stopRuntime(t, c0, d0)

	code, ok := liveRenderedSecretFor(store.snapshot(), admit.enc, addr)
	if !ok {
		t.Fatal("no rendered verification payload for the registered address")
	}
	if err := admit.svc.Verify(ctx, addr, code); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if err := admit.svc.ForgotPassword(ctx, addr); err != nil {
		t.Fatalf("ForgotPassword: %v", err)
	}
	fid, ok := liveOpaqueEnqueueID(store.snapshot(), admit.enc, addr)
	if !ok {
		t.Fatal("opaque forgot-password admission not found in the live store")
	}
	return admit.enc, fid
}

func liveKnownUnknownParity(t *testing.T, open liveStoreFactory) {
	stableDeliveryEnv(t)
	store, repos := newLive(t, open)
	ctx := context.Background()
	const known = "live-known@example.com"
	const unknown = "live-unknown@example.com"

	drain := &captureSender{}
	setup := bootDelivery(t, repos, store, drain)
	if _, err := setup.svc.RegisterUser(ctx, known, "correct-horse-battery-staple", "Known"); err != nil {
		t.Fatalf("RegisterUser: %v", err)
	}
	c0, d0 := runRuntime(setup.runtime)
	waitMsgCount(t, drain, 1)
	// Drain the verification durably before stopping so it is not left reclaimable to
	// resend into the observation phase below (see waitLiveJobTerminal).
	waitLiveQueueDrained(t, store)
	stopRuntime(t, c0, d0)
	code, ok := liveRenderedSecretFor(store.snapshot(), setup.enc, known)
	if !ok {
		t.Fatal("no rendered verification payload for the known address")
	}
	if err := setup.svc.Verify(ctx, known, code); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	opaqueBefore := liveCountOpaque(store.snapshot(), setup.enc)
	if err := setup.svc.ForgotPassword(ctx, known); err != nil {
		t.Fatalf("ForgotPassword(known): %v", err)
	}
	if err := setup.svc.ForgotPassword(ctx, unknown); err != nil {
		t.Fatalf("ForgotPassword(unknown): %v", err)
	}
	// Both starts admit exactly one opaque job with NO provider call on the request
	// path (parity: the known and unknown paths are indistinguishable at admission).
	if got := liveCountOpaque(store.snapshot(), setup.enc) - opaqueBefore; got != 2 {
		t.Fatalf("opaque admissions added = %d, want 2 (known + unknown parity)", got)
	}
	if drain.count() != 1 {
		t.Fatalf("provider called on the forgot-password request path (count=%d, want the 1 verification only)", drain.count())
	}

	// Run the runtime: the known start delivers exactly one reset; the unknown start
	// resolves to no recovery identifier and SKIPS with no provider call — both reach a
	// non-failed terminal (no enumeration signal in status).
	deliver := &captureSender{}
	run := bootDelivery(t, repos, store, deliver)
	cancel, done := runRuntime(run.runtime)
	waitMsgCount(t, deliver, 1)
	time.Sleep(300 * time.Millisecond) // catch any (erroneous) send for the unknown
	stopRuntime(t, cancel, done)
	if deliver.count() != 1 {
		t.Fatalf("delivered %d messages, want exactly 1 (known reset only; unknown skips)", deliver.count())
	}
	if len(deliver.all()[0].To) == 0 || !strings.EqualFold(deliver.all()[0].To[0], known) {
		t.Fatalf("the single delivery was not the known reset: %v", deliver.all()[0].To)
	}
}

func liveProviderTimeoutRetry(t *testing.T, open liveStoreFactory) {
	stableDeliveryEnv(t)
	store, repos := newLive(t, open)
	ctx := context.Background()
	const addr = "live-timeout@example.com"

	_, fid := liveAdmitVerifiedForgotPassword(t, store, repos, addr)

	// ForgotPassword already returned off the request path (no provider call). Boot with
	// a hanging provider and a ProcessTimeout safely inside the lease: each attempt times
	// out and reschedules a retry-at — the provider latency and retry live entirely off
	// the request path. The timeout is sized well above the per-attempt initialization
	// round-trips (Claim/Checkpoint) so every attempt reaches the hanging Send before the
	// timeout fires — otherwise a live-DB init slower than a tight timeout would cut an
	// attempt before the provider is called, letting Retries outpace the provider count —
	// yet stays comfortably below the 3s lease so the lease never lapses mid-attempt.
	hang := &hangingSender{}
	boot := bootDelivery(t, repos, store, hang, func(c *jobs.FencedRuntimeConfig) {
		c.LeaseFor = 3 * time.Second
		c.ProcessTimeout = 400 * time.Millisecond
		c.MaxAttempts = 50
		c.Backoff = func(int) time.Duration { return 20 * time.Millisecond }
	})
	cancel, done := runRuntime(boot.runtime)
	deadline := time.Now().Add(10 * time.Second)
	var retries int
	for time.Now().Before(deadline) {
		if j, err := store.Get(ctx, fid); err == nil {
			retries = j.Retries
		}
		// Wait for BOTH the durable retry count and the provider-attempt count to reach 2.
		// The retry counter (persisted on reschedule) and the provider counter (bumped at
		// Send entry) advance in different goroutines; breaking on retries alone races the
		// provider count, which lags by up to a poll interval on a live store. Requiring
		// both here removes the race without relaxing either semantic assertion below.
		if retries >= 2 && hang.count() >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if retries < 2 {
		t.Fatalf("job retried %d times under a hanging provider, want >= 2 (timeout+retry off request path)", retries)
	}
	if hang.count() < 2 {
		t.Fatalf("provider attempted %d times, want >= 2", hang.count())
	}
	j, err := store.Get(ctx, fid)
	if err != nil {
		t.Fatalf("Get retrying job: %v", err)
	}
	if j.JobStatus == job.StatusDeadLetter || j.JobStatus == job.StatusCompleted {
		t.Fatalf("job terminal (%q) while provider hangs, want a non-terminal retrying state", j.JobStatus)
	}
	stopRuntime(t, cancel, done)

	// Restart with a healthy provider: the retry resolves the checkpointed work and
	// delivers, proving the retries were transient and off the request path.
	deliver := &captureSender{}
	restart := bootDelivery(t, repos, store, deliver)
	c2, d2 := runRuntime(restart.runtime)
	waitMsgCount(t, deliver, 1)
	// Wait for the durable completion to commit before stopping — otherwise the final
	// stop races the Complete write and the job is (correctly) left reclaimable/running.
	waitLiveJobTerminal(t, store, fid)
	stopRuntime(t, c2, d2)
	final, err := store.Get(ctx, fid)
	if err != nil {
		t.Fatalf("Get final job: %v", err)
	}
	if final.JobStatus != job.StatusCompleted {
		t.Fatalf("post-recovery status = %q, want completed", final.JobStatus)
	}
}

func liveRestartAfterOpaqueAdmission(t *testing.T, open liveStoreFactory) {
	stableDeliveryEnv(t)
	store, repos := newLive(t, open)
	ctx := context.Background()
	const addr = "live-opaque-restart@example.com"

	_, fid := liveAdmitVerifiedForgotPassword(t, store, repos, addr)
	if j, err := store.Get(ctx, fid); err != nil {
		t.Fatalf("Get admitted job: %v", err)
	} else if j.JobStatus != job.StatusPending {
		t.Fatalf("admitted opaque job status = %q, want pending (no request-path init)", j.JobStatus)
	}

	deliver := &captureSender{}
	restart := bootDelivery(t, repos, store, deliver)
	cancel, done := runRuntime(restart.runtime)
	waitMsgCount(t, deliver, 1)
	// Wait for the durable completion before stopping (guards the completed assertion);
	// then keep the grace window to catch any erroneous duplicate delivery.
	waitLiveJobTerminal(t, store, fid)
	time.Sleep(200 * time.Millisecond)
	stopRuntime(t, cancel, done)
	if deliver.count() != 1 {
		t.Fatalf("delivered %d messages, want exactly 1 (exactly-one active execution)", deliver.count())
	}
	final, err := store.Get(ctx, fid)
	if err != nil {
		t.Fatalf("Get final job: %v", err)
	}
	if final.JobStatus != job.StatusCompleted {
		t.Fatalf("post-restart status = %q, want completed", final.JobStatus)
	}
}

func liveRestartAfterCheckpoint(t *testing.T, open liveStoreFactory) {
	stableDeliveryEnv(t)
	store, repos := newLive(t, open)
	ctx := context.Background()
	const addr = "live-checkpoint-restart@example.com"

	enc, fid := liveAdmitVerifiedForgotPassword(t, store, repos, addr)
	store.checkpointCh = make(chan string, 8)

	fail := &failingSender{}
	crash := bootDelivery(t, repos, store, fail)
	c1, d1 := runRuntime(crash.runtime)
	if got := waitSignal(t, store.checkpointCh, "the rendered checkpoint"); got != fid {
		t.Fatalf("checkpoint landed for %q, want the forgot-password job %q", got, fid)
	}
	stopRuntime(t, c1, d1)

	crashed, err := store.Get(ctx, fid)
	if err != nil {
		t.Fatalf("Get checkpointed job: %v", err)
	}
	env, err := openSealed(enc, crashed.Payload)
	if err != nil {
		t.Fatalf("open checkpointed payload: %v", err)
	}
	if env.Stage != "rendered" || env.Secret == "" {
		t.Fatalf("checkpointed payload stage=%q secret-empty=%v, want a rendered secret", env.Stage, env.Secret == "")
	}
	checkpointed := env.Secret

	deliver := &captureSender{}
	restart := bootDelivery(t, repos, store, deliver)
	c2, d2 := runRuntime(restart.runtime)
	waitMsgCount(t, deliver, 1)
	stopRuntime(t, c2, d2)
	msg := deliver.all()[0]
	if !strings.Contains(msg.Text, checkpointed) && !strings.Contains(msg.HTML, checkpointed) {
		t.Fatal("the resent message did not carry the checkpointed secret (a new secret was minted on retry)")
	}
	after, err := store.Get(ctx, fid)
	if err != nil {
		t.Fatalf("Get post-restart job: %v", err)
	}
	envAfter, err := openSealed(enc, after.Payload)
	if err != nil {
		t.Fatalf("open post-restart payload: %v", err)
	}
	if envAfter.Secret != checkpointed {
		t.Fatalf("post-restart secret changed (%q != %q): a new secret was minted", envAfter.Secret, checkpointed)
	}
}

func liveRestartAfterProviderAcceptance(t *testing.T, open liveStoreFactory) {
	stableDeliveryEnv(t)
	store, repos := newLive(t, open)
	ctx := context.Background()
	const addr = "live-accept-restart@example.com"

	_, fid := liveAdmitVerifiedForgotPassword(t, store, repos, addr)
	store.droppedCh = make(chan string, 1)
	store.dropCompleteID = fid

	deliver := &captureSender{}
	crash := bootDelivery(t, repos, store, deliver)
	c1, d1 := runRuntime(crash.runtime)
	waitMsgCount(t, deliver, 1)
	waitSignal(t, store.droppedCh, "the dropped completion")
	stopRuntime(t, c1, d1)
	first := deliver.all()[0]

	restart := bootDelivery(t, repos, store, deliver)
	c2, d2 := runRuntime(restart.runtime)
	waitMsgCount(t, deliver, 2)
	// The resend's completion is NOT dropped (the drop is one-shot); wait for it to
	// commit before stopping so the final status assertion sees the durable completed.
	waitLiveJobTerminal(t, store, fid)
	stopRuntime(t, c2, d2)
	second := deliver.all()[len(deliver.all())-1]
	if !sameRenderedMessage(first, second) {
		t.Fatalf("resend was not the identical message: first=%+v second=%+v", first, second)
	}
	final, err := store.Get(ctx, fid)
	if err != nil {
		t.Fatalf("Get final job: %v", err)
	}
	if final.JobStatus != job.StatusCompleted {
		t.Fatalf("post-restart status = %q, want completed", final.JobStatus)
	}
}

func liveResendConvergesToLatest(t *testing.T, open liveStoreFactory) {
	stableDeliveryEnv(t)
	store, repos := newLive(t, open)
	ctx := context.Background()
	const addr = "live-resend@example.com"

	_, fid := liveAdmitVerifiedForgotPassword(t, store, repos, addr)

	// The gen-1 admission's logical key + sealed opaque bytes. A resend re-admits the
	// same enumeration-safe start as a fresh generation via the composition Replace path
	// (jobs.Service.Replace), superseding gen-1.
	gen1, err := store.Get(ctx, fid)
	if err != nil {
		t.Fatalf("Get gen-1: %v", err)
	}
	var opaqueBytes json.RawMessage
	for _, r := range store.snapshot() {
		if r.jobID == fid {
			opaqueBytes = append(json.RawMessage(nil), r.payload...)
			break
		}
	}
	if len(opaqueBytes) == 0 {
		t.Fatal("gen-1 opaque payload not captured")
	}
	deliver := &captureSender{}
	boot := bootDelivery(t, repos, store, deliver)
	gen2ID, err := boot.jobs.Replace(ctx, auth.DeliveryJobKind, gen1.LogicalKey, opaqueBytes)
	if err != nil {
		t.Fatalf("Replace (resend): %v", err)
	}
	if gen2ID == fid {
		t.Fatal("Replace returned the same execution id — a fresh generation was expected")
	}

	cancel, done := runRuntime(boot.runtime)
	waitMsgCount(t, deliver, 1)
	// Let the gen-2 completion commit before stopping (guards the completed assertion);
	// keep the grace window to catch any erroneous second-generation delivery.
	waitLiveJobTerminal(t, store, gen2ID)
	time.Sleep(300 * time.Millisecond)
	stopRuntime(t, cancel, done)

	after1, err := store.Get(ctx, fid)
	if err != nil {
		t.Fatalf("Get gen-1 after resend: %v", err)
	}
	if after1.JobStatus != job.StatusSuperseded {
		t.Fatalf("gen-1 status = %q, want superseded", after1.JobStatus)
	}
	latest, err := store.GetLatestByKey(ctx, gen1.LogicalKey)
	if err != nil {
		t.Fatalf("GetLatestByKey: %v", err)
	}
	if latest.JobID != gen2ID {
		t.Fatalf("latest-by-key = %q, want the fresh generation %q", latest.JobID, gen2ID)
	}
	if latest.JobStatus != job.StatusCompleted {
		t.Fatalf("latest generation status = %q, want completed", latest.JobStatus)
	}
	if deliver.count() != 1 {
		t.Fatalf("delivered %d messages, want exactly 1 (only the latest generation sends)", deliver.count())
	}
}

func liveStatusAndEventsNoSecrets(t *testing.T, open liveStoreFactory) {
	stableDeliveryEnv(t)
	store, repos := newLive(t, open)
	ctx := context.Background()
	const addr = "live-nosecret@example.com"

	em := &secretScanningEmitter{}
	drain := &captureSender{}
	setup := bootDeliveryEmit(t, repos, store, drain, em)
	if _, err := setup.svc.RegisterUser(ctx, addr, "correct-horse-battery-staple", "NoSecret"); err != nil {
		t.Fatalf("RegisterUser: %v", err)
	}
	c0, d0 := runRuntime(setup.runtime)
	waitMsgCount(t, drain, 1)
	// Drain the verification durably before stopping so it is not left reclaimable to
	// resend into the observation phase (see waitLiveJobTerminal).
	waitLiveQueueDrained(t, store)
	stopRuntime(t, c0, d0)
	code, ok := liveRenderedSecretFor(store.snapshot(), setup.enc, addr)
	if !ok {
		t.Fatal("no rendered verification payload")
	}
	if err := setup.svc.Verify(ctx, addr, code); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if err := setup.svc.ForgotPassword(ctx, addr); err != nil {
		t.Fatalf("ForgotPassword: %v", err)
	}
	fid, ok := liveOpaqueEnqueueID(store.snapshot(), setup.enc, addr)
	if !ok {
		t.Fatal("opaque forgot-password admission not found")
	}
	deliver := &captureSender{}
	run := bootDeliveryEmit(t, repos, store, deliver, em)
	cancel, done := runRuntime(run.runtime)
	waitMsgCount(t, deliver, 1)
	stopRuntime(t, cancel, done)

	opaque, err := store.Get(ctx, fid)
	if err != nil {
		t.Fatalf("Get opaque job: %v", err)
	}
	resetSecret := ""
	if env, err := openSealed(setup.enc, opaque.Payload); err == nil {
		resetSecret = env.Secret
	}
	canaries := []string{addr, strings.ToLower(addr), code}
	if resetSecret != "" {
		canaries = append(canaries, resetSecret)
	}
	for _, m := range deliver.all() {
		if len(m.To) > 0 {
			canaries = append(canaries, m.To[0])
		}
		if m.Subject != "" {
			canaries = append(canaries, m.Subject)
		}
	}

	for _, canary := range canaries {
		if canary == "" {
			continue
		}
		for _, r := range store.snapshot() {
			if strings.Contains(string(r.payload), canary) {
				t.Fatalf("plaintext %q leaked into a durable payload (ciphertext must be opaque)", canary)
			}
		}
		if len(canary) >= 12 {
			for _, id := range store.knownIDs() {
				j, err := store.Get(ctx, id)
				if err != nil {
					continue
				}
				for _, col := range []string{j.Kind, j.LogicalKey, j.FailureReason} {
					if strings.Contains(col, canary) {
						t.Fatalf("plaintext %q leaked into a durable job metadata column", canary)
					}
				}
			}
		}
	}

	// The status projection is lifecycle-only and resolves by the PII-free logical key
	// (== the receipt key).
	st, err := run.svc.DeliveryStatus(ctx, opaque.LogicalKey)
	if err != nil {
		t.Fatalf("DeliveryStatus: %v", err)
	}
	if st.State == "" {
		t.Fatal("DeliveryStatus returned an empty state")
	}

	// Emitted lifecycle events carry no secret/destination canary.
	blobs, types := em.snapshot()
	if len(types) == 0 {
		t.Fatal("no lifecycle events were emitted (observer not wired?)")
	}
	// A short numeric OTP can coincidentally appear inside a JSON timestamp or
	// correlation id, so scan events only for the longer canaries (addresses,
	// subjects, and the high-entropy reset token) — the payload/metadata scan above
	// already proved the short code never persists.
	for _, b := range blobs {
		for _, canary := range canaries {
			if len(canary) >= 8 && strings.Contains(string(b), canary) {
				t.Fatalf("plaintext %q leaked into an emitted delivery event", canary)
			}
		}
	}
}

func liveTerminalCleanupAndPurge(t *testing.T, open liveStoreFactory) {
	stableDeliveryEnv(t)
	store, repos := newLive(t, open)
	ctx := context.Background()

	// A garbage (unopenable) payload under the delivery kind is structurally permanent:
	// it dead-letters on the first attempt and fires the discard hook (terminal cleanup).
	// Count the discards by wrapping rt.Discard and building the runtime over the wrapper.
	boot := bootDelivery(t, repos, store, &captureSender{})
	var discarded int
	var discardMu sync.Mutex
	rt := boot.rt
	origDiscard := rt.Discard
	rt.Discard = func(ctx context.Context, executionID string, payload []byte) error {
		discardMu.Lock()
		discarded++
		discardMu.Unlock()
		if origDiscard != nil {
			return origDiscard(ctx, executionID, payload)
		}
		return nil
	}
	runtime, err := jobs.NewFencedRuntime(boot.jobs, authjobs.FencedRuntimeConfig(rt, func(c *jobs.FencedRuntimeConfig) {
		c.Logger = quietLog()
		c.PollInterval = 10 * time.Millisecond
		c.IdleInterval = 10 * time.Millisecond
		c.LeaseFor = 300 * time.Millisecond
		c.MaxAttempts = 50
		c.Backoff = func(int) time.Duration { return 20 * time.Millisecond }
	}))
	if err != nil {
		t.Fatalf("build runtime with wrapped discard: %v", err)
	}

	bad, err := store.EnqueueOnce(ctx, job.Enqueue{Kind: auth.DeliveryJobKind, LogicalKey: "live-bad-key", Payload: json.RawMessage(`"not-ciphertext"`)})
	if err != nil {
		t.Fatalf("EnqueueOnce garbage: %v", err)
	}
	cancel, done := runRuntime(runtime)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if j, err := store.Get(ctx, bad.JobID); err == nil && j.JobStatus == job.StatusDeadLetter {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	stopRuntime(t, cancel, done)

	dead, err := store.Get(ctx, bad.JobID)
	if err != nil {
		t.Fatalf("Get dead-lettered job: %v", err)
	}
	if dead.JobStatus != job.StatusDeadLetter {
		t.Fatalf("garbage payload status = %q, want dead_letter (permanent)", dead.JobStatus)
	}
	discardMu.Lock()
	dc := discarded
	discardMu.Unlock()
	if dc == 0 {
		t.Fatal("discard hook did not run after dead-letter (terminal cleanup missing)")
	}

	// Bounded purge (host-driven, no auth-specific SQL): a terminal generation older than
	// the cutoff is removed; a non-terminal generation survives.
	survivor, err := store.EnqueueOnce(ctx, job.Enqueue{Kind: auth.DeliveryJobKind, LogicalKey: "live-survivor-key", Payload: json.RawMessage(`"pending"`), ScheduledFor: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatalf("EnqueueOnce survivor: %v", err)
	}
	removed, err := authjobs.PurgeTerminal(ctx, boot.jobs, boot.rt, time.Now().Add(time.Minute), 100)
	if err != nil {
		t.Fatalf("PurgeTerminal: %v", err)
	}
	if removed < 1 {
		t.Fatalf("purge removed %d, want >= 1 (the dead-lettered terminal generation)", removed)
	}
	if _, err := store.Get(ctx, survivor.JobID); err != nil {
		t.Fatalf("non-terminal survivor was purged: %v", err)
	}
}
