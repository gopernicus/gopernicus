package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/authjobs"
	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/authmem"
	auth "github.com/gopernicus/gopernicus/features/authentication"
	"github.com/gopernicus/gopernicus/features/jobs"
	"github.com/gopernicus/gopernicus/features/jobs/domain/job"
	jobsmem "github.com/gopernicus/gopernicus/features/jobs/memstore"
	"github.com/gopernicus/gopernicus/sdk/capabilities/email"
	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// This file proves the AV3D-3.2 durable-jobs-mode security properties end to end on
// the host's real composition (auth.Service -> authjobs.Dispatcher -> generic jobs
// fenced queue -> jobs.FencedRuntime -> auth delivery processor), against an
// inspectable in-memory fenced queue (live pgx/turso are AV3D-3.5; env DSNs unset):
//
//   - every persisted payload byte-for-byte is sealed ciphertext: no destination,
//     normalized identifier, code, token, subject, or rendered body in the clear —
//     including the opaque resolution input;
//   - a restart after opaque admission initializes safely, exactly once, and
//     completes delivery;
//   - a restart after the rendered checkpoint (but before a successful send) resends
//     the SAME secret the checkpoint captured, never a newly minted one; and
//   - a restart after provider acceptance (but before completion) is an at-least-once
//     resend of that SAME secret.
//
// "Restart" is modeled by keeping the durable state (the fenced queue and the auth
// repositories) alive while dropping and rebuilding the jobs Service, auth Service,
// and FencedRuntime from that persisted state — a process restart with the DB intact.
// The delivery-encrypter, challenge-pepper, and identifier keys are pinned to stable
// values (as a real multi-instance/restart deployment MUST share them) so a payload
// sealed before a restart is openable after it.

// stableDeliveryEnv pins the key material buildAuthConfig would otherwise generate
// ephemerally per boot, so a payload sealed before a modeled restart can be opened
// after it (the demo.go WARNs say a multi-instance/restart deployment MUST share
// these). AUTH_DELIVERY_ENCRYPTER_KEY is raw 32 bytes; the rest are hex >= 32 bytes.
func stableDeliveryEnv(t *testing.T) {
	t.Helper()
	t.Setenv("AUTH_DELIVERY_ENCRYPTER_KEY", "0123456789abcdef0123456789abcdef")
	t.Setenv("AUTH_CHALLENGE_PEPPER", strings.Repeat("ab", 32))
	t.Setenv("AUTH_IDENTIFIER_KEY", strings.Repeat("cd", 32))
	t.Setenv("AUTH_JWT_SECRET", strings.Repeat("ef", 32))
}

// sealedEnvelope mirrors the internal command.Envelope JSON shape so a host test can
// decrypt a persisted payload and read the plaintext it sealed WITHOUT importing the
// feature-internal command package. The json tags match command.Envelope exactly.
type sealedEnvelope struct {
	Version         int    `json:"version"`
	Kind            string `json:"kind"`
	Purpose         string `json:"purpose"`
	Key             string `json:"key"`
	Stage           string `json:"stage"`
	ResolutionInput string `json:"resolution_input,omitempty"`
	Destination     string `json:"destination,omitempty"`
	Subject         string `json:"subject,omitempty"`
	Body            string `json:"body,omitempty"`
	HTML            string `json:"html,omitempty"`
	Secret          string `json:"secret,omitempty"`
}

// openSealed decrypts a persisted payload with the delivery encrypter and decodes the
// envelope it sealed. A failure means the bytes are not this key's ciphertext.
func openSealed(enc cryptids.Encrypter, payload []byte) (sealedEnvelope, error) {
	plaintext, err := enc.Decrypt(string(payload))
	if err != nil {
		return sealedEnvelope{}, err
	}
	var e sealedEnvelope
	if err := json.Unmarshal([]byte(plaintext), &e); err != nil {
		return sealedEnvelope{}, err
	}
	return e, nil
}

// payloadRecord is one durable payload write the inspecting queue observed.
type payloadRecord struct {
	op      string
	jobID   string
	payload []byte
}

// inspectingQueue wraps the reference in-memory fenced queue so a test can (1) capture
// every payload byte-for-byte as it is persisted (enqueue-once / replace / checkpoint),
// (2) signal when a checkpoint lands, and (3) simulate a crash after provider acceptance
// by dropping the first Complete for a target execution (the completion write is lost;
// the job stays claimable so a later runtime resends). All other methods promote from
// the embedded *jobsmem.FencedQueue unchanged.
type inspectingQueue struct {
	*jobsmem.FencedQueue

	mu      sync.Mutex
	records []payloadRecord
	ids     map[string]struct{}

	checkpointCh chan string

	dropCompleteID string
	dropped        bool
	droppedCh      chan string

	// Adversarial gates (AV3D-3.3), read under mu and invoked outside it, set before
	// the runtime starts. gateCheckpointEnter fires as a target handler ENTERS its
	// checkpoint (before the real checkpoint runs) so a test can supersede the claim
	// and prove the ensuing checkpoint is fenced; gateCheckpointDone fires AFTER the
	// real checkpoint returns (with its error) so a test can supersede a successfully
	// checkpointed claim before its send. A closure owns its own id-match and one-shot
	// logic, so gen-2's checkpoints are never gated.
	gateCheckpointEnter func(id string)
	gateCheckpointDone  func(id string, err error)
}

// setGates installs the adversarial checkpoint gates before the runtime starts.
func (q *inspectingQueue) setGates(enter func(id string), done func(id string, err error)) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.gateCheckpointEnter = enter
	q.gateCheckpointDone = done
}

var _ job.FencedQueueRepository = (*inspectingQueue)(nil)

func newInspectingQueue() *inspectingQueue {
	return &inspectingQueue{FencedQueue: jobsmem.NewFencedQueue(), ids: map[string]struct{}{}}
}

func (q *inspectingQueue) record(op, id string, payload json.RawMessage) {
	q.mu.Lock()
	defer q.mu.Unlock()
	b := make([]byte, len(payload))
	copy(b, payload)
	q.records = append(q.records, payloadRecord{op: op, jobID: id, payload: b})
	if id != "" {
		q.ids[id] = struct{}{}
	}
}

func (q *inspectingQueue) snapshot() []payloadRecord {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]payloadRecord, len(q.records))
	copy(out, q.records)
	return out
}

func (q *inspectingQueue) knownIDs() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]string, 0, len(q.ids))
	for id := range q.ids {
		out = append(out, id)
	}
	return out
}

func (q *inspectingQueue) EnqueueOnce(ctx context.Context, in job.Enqueue) (job.Job, error) {
	j, err := q.FencedQueue.EnqueueOnce(ctx, in)
	if err == nil {
		q.record("enqueue_once", j.JobID, in.Payload)
	}
	return j, err
}

func (q *inspectingQueue) Replace(ctx context.Context, in job.Enqueue) (job.Job, error) {
	j, err := q.FencedQueue.Replace(ctx, in)
	if err == nil {
		q.record("replace", j.JobID, in.Payload)
	}
	return j, err
}

func (q *inspectingQueue) Checkpoint(ctx context.Context, id, leaseID string, payload json.RawMessage, now time.Time) error {
	q.mu.Lock()
	enter := q.gateCheckpointEnter
	done := q.gateCheckpointDone
	q.mu.Unlock()
	if enter != nil {
		enter(id)
	}
	err := q.FencedQueue.Checkpoint(ctx, id, leaseID, payload, now)
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
	if done != nil {
		done(id, err)
	}
	return err
}

func (q *inspectingQueue) Complete(ctx context.Context, id, leaseID string, now time.Time) error {
	q.mu.Lock()
	drop := q.dropCompleteID != "" && id == q.dropCompleteID && !q.dropped
	if drop {
		q.dropped = true
	}
	ch := q.droppedCh
	q.mu.Unlock()
	if drop {
		// Simulate a crash after the provider accepted the message but before the
		// completion write commits: the completion is lost and the running claim's
		// lease simply lapses, leaving the job reclaimable for an at-least-once resend.
		if ch != nil {
			select {
			case ch <- id:
			default:
			}
		}
		return nil
	}
	return q.FencedQueue.Complete(ctx, id, leaseID, now)
}

// booted is one modeled boot of the jobs-mode delivery composition over a shared,
// durable store and auth repositories.
type booted struct {
	svc     *auth.Service
	runtime *jobs.FencedRuntime
	enc     cryptids.Encrypter
	// jobs is the generic jobs Service the composition dispatcher submits/replaces
	// through — exposed so an adversarial test can drive the exact dispatcher -> jobs
	// Replace path (AV3D-3.3).
	jobs *jobs.Service
	// rt is the auth delivery job runtime seam, exposed so a proof can drive the
	// host-owned purge-observe hook (authjobs.PurgeTerminal) (AV3D-3.4).
	rt auth.DeliveryJobRuntime
}

// bootDelivery builds the jobs Service, dispatcher, auth Service, and FencedRuntime
// over the given persistent store and auth repositories — the exact composition run()
// wires. Rebuilding it over the same store models a process restart with the DB
// intact. sender overrides the mailer so a test can observe (or fail) the send; nil
// keeps the console default. The runtime is tuned for fast, deterministic tests.
func bootDelivery(t *testing.T, authRepos auth.Repositories, store job.FencedQueueRepository, sender email.Sender, tune ...func(*jobs.FencedRuntimeConfig)) booted {
	return bootDeliveryEmit(t, authRepos, store, sender, nil, tune...)
}

// bootDeliveryEmit is bootDelivery with an optional delivery lifecycle events emitter
// wired onto the auth config (AV3D-3.4). A nil emitter is the no-observation path
// bootDelivery uses; a non-nil emitter drives the jobs-mode observer.
func bootDeliveryEmit(t *testing.T, authRepos auth.Repositories, store job.FencedQueueRepository, sender email.Sender, emitter sdkevents.Emitter, tune ...func(*jobs.FencedRuntimeConfig)) booted {
	t.Helper()
	deliveryJobs, err := jobs.NewService(jobs.Repositories{FencedQueue: store}, jobs.Config{Logger: quietLog()})
	if err != nil {
		t.Fatalf("jobs.NewService: %v", err)
	}
	cfg, err := buildAuthConfig(quietLog(), nil)
	if err != nil {
		t.Fatalf("buildAuthConfig: %v", err)
	}
	if sender != nil {
		cfg.Mailer = sender
	}
	cfg.DeliveryDispatcher = authjobs.NewDispatcher(deliveryJobs)
	if emitter != nil {
		cfg.DeliveryEventsEmitter = emitter
	}

	svc, err := auth.NewService(authRepos, cfg)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	rt, ok := svc.DeliveryJobRuntime()
	if !ok {
		t.Fatal("DeliveryJobRuntime unavailable in jobs mode with a wired dispatcher")
	}
	opts := []func(*jobs.FencedRuntimeConfig){
		func(c *jobs.FencedRuntimeConfig) {
			c.Logger = quietLog()
			c.PollInterval = 10 * time.Millisecond
			c.IdleInterval = 10 * time.Millisecond
			// A short lease so a dropped/left-reclaimable job becomes claimable again
			// within a test's patience; well above the sub-millisecond processing time.
			c.LeaseFor = 300 * time.Millisecond
			// A high attempt cap and tiny backoff so a transient-fail boot reschedules
			// (rather than dead-letters) and a restart reclaims promptly.
			c.MaxAttempts = 50
			c.Backoff = func(int) time.Duration { return 20 * time.Millisecond }
		},
	}
	// tune overrides the defaults last, so an adversarial test can widen the lease
	// past its orchestration window without touching the restart proofs.
	opts = append(opts, tune...)
	runtime, err := jobs.NewFencedRuntime(deliveryJobs, authjobs.FencedRuntimeConfig(rt, opts...))
	if err != nil {
		t.Fatalf("jobs.NewFencedRuntime: %v", err)
	}
	return booted{svc: svc, runtime: runtime, enc: cfg.DeliveryEncrypter, jobs: deliveryJobs, rt: rt}
}

func runRuntime(rt *jobs.FencedRuntime) (context.CancelFunc, chan error) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- rt.Run(ctx) }()
	return cancel, done
}

func stopRuntime(t *testing.T, cancel context.CancelFunc, done chan error) {
	t.Helper()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("delivery runtime did not stop within 5s")
	}
}

func waitMsgCount(t *testing.T, cap *captureSender, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cap.count() >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected at least %d delivered messages within 5s, got %d", n, cap.count())
}

func waitSignal(t *testing.T, ch chan string, what string) string {
	t.Helper()
	select {
	case id := <-ch:
		return id
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s within 5s", what)
		return ""
	}
}

// failingSender records call counts and always errors, standing in for a provider
// outage so the processor checkpoints then fails the send.
type failingSender struct {
	mu    sync.Mutex
	calls int
}

func (s *failingSender) Send(_ context.Context, _ email.Message) error {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	return errors.New("provider unavailable")
}

func (s *failingSender) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// captureSender helpers (the type lives in override_test.go).
func (c *captureSender) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.msgs)
}

func (c *captureSender) all() []email.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]email.Message(nil), c.msgs...)
}

// renderedSecretFor finds the sealed rendered payload addressed to dest and returns its
// secret (an OTP/verification code or reset token) — the value a real recipient would
// receive. It scans persisted payloads, so it works before any runtime has run for a
// rendered (synchronously issued) admission like registration verification.
func renderedSecretFor(q *inspectingQueue, enc cryptids.Encrypter, dest string) (string, bool) {
	for _, r := range q.snapshot() {
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

// opaqueEnqueueID returns the execution id of the opaque admission whose sealed
// resolution input matches identifier — the enumeration-safe start (forgot-password /
// passwordless) that the worker resolves off the request path.
func opaqueEnqueueID(q *inspectingQueue, enc cryptids.Encrypter, identifier string) (string, bool) {
	for _, r := range q.snapshot() {
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

func countOpaque(q *inspectingQueue, enc cryptids.Encrypter) int {
	n := 0
	for _, r := range q.snapshot() {
		if env, err := openSealed(enc, r.payload); err == nil && env.Stage == "opaque" {
			n++
		}
	}
	return n
}

// drivePasswordlessStart admits an enumeration-safe passwordless login start through
// the mounted HTTP surface (the only reachable entry point), so the byte-inspection
// proof covers the passwordless opaque admission alongside forgot-password. It asserts
// the start did not error at the server; the real assertion is that a new opaque
// payload was persisted (checked by the caller).
func drivePasswordlessStart(t *testing.T, svc *auth.Service, identifier string) {
	t.Helper()
	router := web.NewWebHandler(web.WithLogging(quietLog()))
	bus := sdkevents.NewMemory(sdkevents.WithLogger(quietLog()))
	if err := svc.Register(feature.Mount{Router: router, Logger: quietLog(), Events: bus}); err != nil {
		t.Fatalf("auth.Register: %v", err)
	}
	body := `{"identifier_kind":"email","identifier":"` + identifier + `"}`
	req := httptest.NewRequest(http.MethodPost, "/auth/passwordless/start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if origins := allowedOrigins(); len(origins) > 0 {
		req.Header.Set("Origin", origins[0])
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code >= 500 {
		t.Fatalf("passwordless start returned %d: %s", rec.Code, rec.Body.String())
	}
}

// TestJobsModeSealsEveryPersistedPayload drives registration verification (rendered),
// forgot-password (opaque), and passwordless (opaque) through the jobs-mode path, then
// scans every persisted payload byte-for-byte: no destination, normalized identifier,
// code, token, subject, or rendered body appears in plaintext — including the opaque
// resolution input. The assertion is tied to the ACTUAL plaintext each flow carried
// (recovered by decrypting the payloads with the delivery encrypter), and a positive
// control proves the plaintext really contained the address and a secret, so the
// absence-in-ciphertext result is not vacuous.
func TestJobsModeSealsEveryPersistedPayload(t *testing.T) {
	stableDeliveryEnv(t)

	store := newInspectingQueue()
	authRepos := authmem.New().Repositories()

	cap := &captureSender{}
	b := bootDelivery(t, authRepos, store, cap)

	ctx := context.Background()
	const addr = "seal-recipient@example.com"

	if _, err := b.svc.RegisterUser(ctx, addr, "correct-horse-battery-staple", "Seal User"); err != nil {
		t.Fatalf("RegisterUser: %v", err)
	}
	code, ok := renderedSecretFor(store, b.enc, addr)
	if !ok {
		t.Fatal("no rendered verification payload found for the registered address")
	}
	if err := b.svc.Verify(ctx, addr, code); err != nil {
		t.Fatalf("Verify (needed so forgot-password resolves and renders): %v", err)
	}
	if err := b.svc.ForgotPassword(ctx, addr); err != nil {
		t.Fatalf("ForgotPassword: %v", err)
	}

	opaqueBefore := countOpaque(store, b.enc)
	drivePasswordlessStart(t, b.svc, addr)
	if countOpaque(store, b.enc) <= opaqueBefore {
		t.Fatal("passwordless start did not persist a new opaque admission")
	}

	// Run the runtime so the opaque starts initialize, checkpoint their rendered
	// payloads, and deliver — exercising the checkpoint write on the durable path.
	cancel, done := runRuntime(b.runtime)
	// Verification (rendered) + forgot-password (opaque, checkpointed) both deliver.
	waitMsgCount(t, cap, 2)
	time.Sleep(300 * time.Millisecond) // grace for the passwordless delivery too
	stopRuntime(t, cancel, done)

	// Recover the ACTUAL plaintext each persisted payload carried, and pin the
	// externally observed canaries (the recipient address and the delivered code).
	var canaries []string
	sawAddress := false
	sawSecret := false
	for _, r := range store.snapshot() {
		env, err := openSealed(b.enc, r.payload)
		if err != nil {
			t.Fatalf("a persisted payload was not openable with the delivery key (op=%s): %v", r.op, err)
		}
		for _, v := range []string{env.Destination, env.ResolutionInput, env.Subject, env.Body, env.HTML, env.Secret} {
			if v != "" {
				canaries = append(canaries, v)
			}
		}
		if strings.EqualFold(env.Destination, addr) || strings.EqualFold(env.ResolutionInput, addr) {
			sawAddress = true
		}
		if env.Secret != "" {
			sawSecret = true
		}
	}
	if !sawAddress {
		t.Fatal("positive control failed: no decrypted payload carried the recipient address")
	}
	if !sawSecret {
		t.Fatal("positive control failed: no decrypted payload carried a secret")
	}
	// The externally observed plaintext: the recipient address and the code/token a
	// real user received.
	canaries = append(canaries, addr, strings.ToLower(addr))
	for _, m := range cap.all() {
		if len(m.To) > 0 {
			canaries = append(canaries, m.To[0])
		}
		if m.Subject != "" {
			canaries = append(canaries, m.Subject)
		}
	}

	// Durable bytes to scan: every persisted payload (ciphertext) plus the plaintext
	// non-payload columns (Kind / LogicalKey / FailureReason) of every stored job.
	var cipherBlobs [][]byte
	for _, r := range store.snapshot() {
		cipherBlobs = append(cipherBlobs, r.payload)
	}
	var metaBlobs [][]byte
	for _, id := range store.knownIDs() {
		j, err := store.FencedQueue.Get(ctx, id)
		if err != nil {
			continue
		}
		metaBlobs = append(metaBlobs, []byte(j.Kind), []byte(j.LogicalKey), []byte(j.FailureReason))
	}

	for _, canary := range canaries {
		if canary == "" {
			continue
		}
		for _, b := range cipherBlobs {
			if strings.Contains(string(b), canary) {
				t.Fatalf("plaintext %q leaked into a durable payload (ciphertext must be opaque)", canary)
			}
		}
		// A short numeric secret can coincidentally appear as a substring of a hex
		// LogicalKey without being a real leak; only the payload check is meaningful
		// for those. Long canaries (addresses, subjects, bodies) must be absent from
		// the plaintext metadata columns too.
		if len(canary) >= 12 {
			for _, b := range metaBlobs {
				if strings.Contains(string(b), canary) {
					t.Fatalf("plaintext %q leaked into a durable job metadata column", canary)
				}
			}
		}
	}
}

// admitVerifiedForgotPassword registers and verifies an account, draining the rendered
// verification delivery so it does not linger and get resent after a modeled restart,
// then admits an opaque forgot-password start with no runtime running. It returns the
// delivery encrypter and the opaque execution id of the forgot-password job. This is
// the shared "state before the crash" the restart proofs build on.
func admitVerifiedForgotPassword(t *testing.T, store *inspectingQueue, authRepos auth.Repositories, addr string) (cryptids.Encrypter, string) {
	t.Helper()
	ctx := context.Background()
	drain := &captureSender{}
	admit := bootDelivery(t, authRepos, store, drain)
	if _, err := admit.svc.RegisterUser(ctx, addr, "correct-horse-battery-staple", "Restart User"); err != nil {
		t.Fatalf("RegisterUser: %v", err)
	}
	// Drain the verification job so only the forgot-password job is pending at restart.
	c0, d0 := runRuntime(admit.runtime)
	waitMsgCount(t, drain, 1)
	stopRuntime(t, c0, d0)

	code, ok := renderedSecretFor(store, admit.enc, addr)
	if !ok {
		t.Fatal("no rendered verification payload for the registered address")
	}
	if err := admit.svc.Verify(ctx, addr, code); err != nil {
		t.Fatalf("Verify (needed so forgot-password resolves and renders): %v", err)
	}
	if err := admit.svc.ForgotPassword(ctx, addr); err != nil {
		t.Fatalf("ForgotPassword: %v", err)
	}
	fid, ok := opaqueEnqueueID(store, admit.enc, addr)
	if !ok {
		t.Fatal("opaque forgot-password admission not found in the store")
	}
	return admit.enc, fid
}

// TestRestartAfterOpaqueAdmissionInitializesSafely admits an opaque forgot-password
// start with NO runtime running (admission only), then models a crash by building a
// fresh jobs Service + auth Service + runtime over the surviving store and repos. The
// restart initializes the opaque work safely, exactly once, and completes delivery.
func TestRestartAfterOpaqueAdmissionInitializesSafely(t *testing.T) {
	stableDeliveryEnv(t)

	store := newInspectingQueue()
	authRepos := authmem.New().Repositories()
	ctx := context.Background()
	const addr = "opaque-restart@example.com"

	enc, fid := admitVerifiedForgotPassword(t, store, authRepos, addr)
	_ = enc
	// The admitted work is opaque and untouched (no initialization ran on the request
	// path).
	if j, err := store.FencedQueue.Get(ctx, fid); err != nil {
		t.Fatalf("Get admitted job: %v", err)
	} else if j.JobStatus != job.StatusPending {
		t.Fatalf("admitted opaque job status = %q, want pending (no request-path init)", j.JobStatus)
	}

	// Restart: a fresh runtime over the surviving state.
	cap := &captureSender{}
	restarted := bootDelivery(t, authRepos, store, cap)
	cancel, done := runRuntime(restarted.runtime)
	waitMsgCount(t, cap, 1)
	time.Sleep(200 * time.Millisecond) // catch any (erroneous) duplicate
	stopRuntime(t, cancel, done)

	msgs := cap.all()
	if len(msgs) != 1 {
		t.Fatalf("delivered %d messages, want exactly one (exactly-one active execution)", len(msgs))
	}
	if len(msgs[0].To) == 0 || !strings.EqualFold(msgs[0].To[0], addr) {
		t.Fatalf("reset delivered to %v, want %s", msgs[0].To, addr)
	}
	final, err := store.FencedQueue.Get(ctx, fid)
	if err != nil {
		t.Fatalf("Get final job: %v", err)
	}
	if final.JobStatus != job.StatusCompleted {
		t.Fatalf("post-restart job status = %q, want completed", final.JobStatus)
	}
}

// TestRestartAfterCheckpointResendsSameSecret crashes AFTER the rendered checkpoint but
// before a successful send (a provider outage during the first boot), rebuilds the
// runtime, and proves the retry resends the SAME secret the checkpoint captured — never
// a newly minted one.
func TestRestartAfterCheckpointResendsSameSecret(t *testing.T) {
	stableDeliveryEnv(t)

	store := newInspectingQueue()
	authRepos := authmem.New().Repositories()
	ctx := context.Background()
	const addr = "checkpoint-restart@example.com"

	enc, fid := admitVerifiedForgotPassword(t, store, authRepos, addr)
	store.checkpointCh = make(chan string, 8)

	// Boot with a failing provider: the processor resolves, renders, CHECKPOINTS the
	// rendered payload, then the send fails. Stop the runtime the instant the
	// checkpoint lands — the "crash after checkpoint, before a successful send".
	fail := &failingSender{}
	crashBoot := bootDelivery(t, authRepos, store, fail)
	cancel1, done1 := runRuntime(crashBoot.runtime)
	if got := waitSignal(t, store.checkpointCh, "the rendered checkpoint"); got != fid {
		t.Fatalf("checkpoint landed for %q, want the forgot-password job %q", got, fid)
	}
	stopRuntime(t, cancel1, done1)
	if fail.count() == 0 {
		// The send is attempted immediately after the checkpoint; a zero count would
		// mean we stopped before the processor reached the provider at all.
		t.Log("note: crash captured right at the checkpoint; provider not yet reached")
	}

	// The checkpointed secret: what a correct retry must resend, unchanged.
	crashed, err := store.FencedQueue.Get(ctx, fid)
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
	checkpointedSecret := env.Secret

	// Restart with a healthy provider: the retry reads the checkpointed rendered
	// payload, skips re-initialization, and resends the SAME secret.
	cap := &captureSender{}
	restarted := bootDelivery(t, authRepos, store, cap)
	cancel2, done2 := runRuntime(restarted.runtime)
	waitMsgCount(t, cap, 1)
	stopRuntime(t, cancel2, done2)

	msg := cap.all()[0]
	if !strings.Contains(msg.Text, checkpointedSecret) && !strings.Contains(msg.HTML, checkpointedSecret) {
		t.Fatal("the resent message did not carry the checkpointed secret (a new secret was minted on retry)")
	}
	// The stored payload's secret is still the checkpointed one — no re-mint occurred.
	after, err := store.FencedQueue.Get(ctx, fid)
	if err != nil {
		t.Fatalf("Get post-restart job: %v", err)
	}
	envAfter, err := openSealed(enc, after.Payload)
	if err != nil {
		t.Fatalf("open post-restart payload: %v", err)
	}
	if envAfter.Secret != checkpointedSecret {
		t.Fatalf("post-restart secret changed (%q != %q): a new secret was minted", envAfter.Secret, checkpointedSecret)
	}
}

// TestRestartAfterProviderAcceptanceResendsSameSecret crashes AFTER the provider
// accepted the message but before the completion committed, rebuilds the runtime, and
// proves the resend is at-least-once with the SAME secret — the identical rendered
// message, never a newly minted one.
func TestRestartAfterProviderAcceptanceResendsSameSecret(t *testing.T) {
	stableDeliveryEnv(t)

	store := newInspectingQueue()
	authRepos := authmem.New().Repositories()
	ctx := context.Background()
	const addr = "accept-restart@example.com"

	_, fid := admitVerifiedForgotPassword(t, store, authRepos, addr)
	store.droppedCh = make(chan string, 1)

	// Boot with a healthy provider but drop the first completion for this job: the
	// provider accepts the first message, then the completion write is lost — the
	// "crash after provider acceptance, before completion". Stop the runtime as soon as
	// the drop happens so this boot does not itself reclaim and resend.
	store.dropCompleteID = fid
	cap := &captureSender{}
	crashBoot := bootDelivery(t, authRepos, store, cap)
	cancel1, done1 := runRuntime(crashBoot.runtime)
	waitMsgCount(t, cap, 1)
	waitSignal(t, store.droppedCh, "the dropped completion")
	stopRuntime(t, cancel1, done1)

	first := cap.all()[0]

	// Restart: the reclaimed job (lease lapsed) resends the identical rendered message.
	restarted := bootDelivery(t, authRepos, store, cap)
	cancel2, done2 := runRuntime(restarted.runtime)
	waitMsgCount(t, cap, 2)
	stopRuntime(t, cancel2, done2)

	msgs := cap.all()
	second := msgs[len(msgs)-1]
	if !sameRenderedMessage(first, second) {
		t.Fatalf("resend was not the identical message: first=%+v second=%+v", first, second)
	}
	final, err := store.FencedQueue.Get(ctx, fid)
	if err != nil {
		t.Fatalf("Get final job: %v", err)
	}
	if final.JobStatus != job.StatusCompleted {
		t.Fatalf("post-restart job status = %q, want completed", final.JobStatus)
	}
}

// sameRenderedMessage reports whether two delivered messages are the identical rendered
// content (recipient, subject, and both bodies) — proof a resend carried the SAME
// secret rather than a freshly minted one.
func sameRenderedMessage(a, b email.Message) bool {
	if len(a.To) != len(b.To) {
		return false
	}
	for i := range a.To {
		if a.To[i] != b.To[i] {
			return false
		}
	}
	return a.Subject == b.Subject && a.Text == b.Text && a.HTML == b.HTML
}
