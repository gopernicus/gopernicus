package delivery

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/work"
)

// Bounded in-process delivery defaults (AV3D-4.1/4.2). Each zero field in the config
// structs below selects its default. They mirror the "bounded everywhere" standing
// invariant: a FIXED worker count, a FINITE queue capacity, a SHORT admission
// deadline (admission never blocks unbounded), a BOUNDED shutdown drain (a provider
// ignoring cancellation cannot stall shutdown past the bound), and a BOUNDED status
// retention (a FINITE maximum entry count and a TTL — the latest-by-key status map
// never grows with process lifetime).
const (
	// defaultInProcessWorkers is the fixed worker-pool size — never one goroutine per
	// request. The pool runs exactly this many delivery goroutines regardless of load.
	defaultInProcessWorkers = 2
	// defaultInProcessCapacity is the finite queue depth. Once full, admission waits at
	// most the admission deadline for a slot, then rejects with ErrDeliveryCapacity.
	defaultInProcessCapacity = 256
	// defaultInProcessAdmissionDeadline bounds how long an enqueue waits for a free slot
	// before returning ErrDeliveryCapacity. Admission never blocks unbounded.
	defaultInProcessAdmissionDeadline = 250 * time.Millisecond
	// defaultInProcessShutdownDeadline bounds how long Run waits for in-flight workers to
	// observe cancellation and return after the host cancels the runtime context.
	defaultInProcessShutdownDeadline = 15 * time.Second
	// defaultInProcessStatusMaxEntries bounds the latest-by-key status map (AV3D-4.2).
	// Once the map holds this many keys a fresh admission evicts the oldest terminal
	// entry, so retention never grows with process lifetime. It is comfortably larger
	// than the default queue capacity + worker pool so an in-flight generation is never
	// evicted (full knob validation, incl. maxEntries >= capacity, is AV3D-4.5).
	defaultInProcessStatusMaxEntries = 4096
	// defaultInProcessStatusTTL bounds how long a TERMINAL latest-by-key status is
	// retained (AV3D-4.2). After it elapses the key reads as unknown (sdk.ErrNotFound)
	// and is evicted. An ACTIVE (queued/processing) generation is never TTL-evicted.
	defaultInProcessStatusTTL = 30 * time.Minute
	// defaultInProcessMaxAttempts caps process-local delivery attempts before a transient
	// failure becomes a terminal dead-letter (AV3D-4.3). It mirrors the command.Engine
	// default so the bounded pool and the durable jobs runtime share one attempt budget.
	defaultInProcessMaxAttempts = 5
	// defaultInProcessBackoffBase is the first retry delay; each further attempt doubles
	// it up to defaultInProcessBackoffCap (AV3D-4.3). It mirrors command.Engine's default.
	defaultInProcessBackoffBase = 5 * time.Second
	// defaultInProcessBackoffCap ceilings the exponential retry backoff (AV3D-4.3).
	defaultInProcessBackoffCap = 5 * time.Minute
)

// Bounded in-process delivery errors. Each wraps a stable sdk kind so callers match
// with errors.Is (never string parsing), and each is secret-free.
//
// The transient admission rejections (queue at capacity, runtime shutting down) wrap
// sdk.ErrUnavailable: the request cannot be accepted right now but is retriable
// unchanged (backpressure/shutdown), so a non-HTTP caller gets the retry-correct
// taxonomy and transports map it to 503. The supersession fence keeps sdk.ErrConflict:
// a stale generation losing to a newer one is state contention, not backpressure. Both
// mappings are uniform for a known and an unknown identifier (admission precedes any
// account lookup), so neither becomes an enumeration signal.
var (
	// ErrDeliveryCapacity is returned by Submit/Replace when the bounded queue is full
	// and no slot frees within the admission deadline. The work is NOT accepted (never a
	// silent drop); the caller may retry later.
	ErrDeliveryCapacity = fmt.Errorf("delivery: in-process delivery queue is at capacity: %w", sdk.ErrUnavailable)
	// ErrDeliveryClosed is returned by Submit/Replace once the runtime is shutting down
	// (its context was canceled): admission is closed and no new work is accepted.
	ErrDeliveryClosed = fmt.Errorf("delivery: in-process delivery runtime is not accepting work (shutting down): %w", sdk.ErrUnavailable)
	// ErrDeliverySuperseded is returned to a worker whose in-flight generation was
	// replaced by a newer one (AV3D-4.2): its checkpoint (and, by the same fence, its
	// completion) is rejected so a stale, superseded execution can never clobber the
	// current generation's status. It is secret-free.
	ErrDeliverySuperseded = fmt.Errorf("delivery: in-process delivery work was superseded by a newer generation: %w", sdk.ErrConflict)
	// ErrInProcessQueueRequired is returned by NewInProcessRuntime when the shared queue
	// is nil — the runtime cannot drain a queue that does not exist.
	ErrInProcessQueueRequired = fmt.Errorf("delivery: in-process runtime requires a queue: %w", sdk.ErrInvalidInput)
	// ErrInProcessProcessorRequired is returned by NewInProcessRuntime when the processor
	// is nil — the pool has nothing to run.
	ErrInProcessProcessorRequired = fmt.Errorf("delivery: in-process runtime requires a processor: %w", sdk.ErrInvalidInput)
	// ErrInProcessAlreadyRunning is returned by Run when the runtime is already running.
	// The pool is single-owner: one host lifecycle drives it.
	ErrInProcessAlreadyRunning = fmt.Errorf("delivery: in-process runtime is already running: %w", sdk.ErrConflict)
)

// errInProcessStatusUnknown is the honest "no status retained for this key" result of
// LatestStatus: an unknown, never-admitted, or evicted key. It wraps sdk.ErrNotFound
// so the service normalizes it through the existing unknown projection.
var errInProcessStatusUnknown = fmt.Errorf("delivery: no in-process delivery status retained for key: %w", sdk.ErrNotFound)

// errInProcessDeliveryPanic is the coarse, static, secret-free PERMANENT failure recorded
// for a delivery job that panicked in the provider render/send or the engine step. It
// carries the permanent disposition (HandleErrorPermanent reports true) so a panicking job
// dead-letters IMMEDIATELY instead of crashing the worker and taking down the host. The
// sanitized panic value and stack are logged at the recover boundary (handleOnce), never
// stored in the reason — the dead-letter reason stays a stable token, never the panic's
// raw content (which a buggy or hostile provider could seed with a decrypted payload,
// destination, or secret).
var errInProcessDeliveryPanic = &jobFailure{reason: "delivery: in-process delivery job panicked", permanent: true}

// Bounded in-process delivery CONSTRUCTION errors (AV3D-4.5). Each wraps
// sdk.ErrInvalidInput so a host's wiring fails LOUDLY at construction and callers match
// with errors.Is — never string parsing. The knobs are nil-safe: a ZERO value selects
// its package default, so only a NEGATIVE bound (or a retention window smaller than the
// queue it must cover) is rejected. This is the fail-closed posture: an invalid bound
// is never silently coerced to a default.
var (
	// ErrInProcessCapacityInvalid is returned when the configured queue capacity is
	// negative.
	ErrInProcessCapacityInvalid = fmt.Errorf("delivery: in-process queue capacity must not be negative: %w", sdk.ErrInvalidInput)
	// ErrInProcessAdmissionDeadlineInvalid is returned when the configured admission
	// deadline is negative.
	ErrInProcessAdmissionDeadlineInvalid = fmt.Errorf("delivery: in-process admission deadline must not be negative: %w", sdk.ErrInvalidInput)
	// ErrInProcessStatusMaxEntriesInvalid is returned when the configured status
	// retention maximum entry count is negative.
	ErrInProcessStatusMaxEntriesInvalid = fmt.Errorf("delivery: in-process status max entries must not be negative: %w", sdk.ErrInvalidInput)
	// ErrInProcessStatusTTLInvalid is returned when the configured status retention TTL
	// is negative.
	ErrInProcessStatusTTLInvalid = fmt.Errorf("delivery: in-process status TTL must not be negative: %w", sdk.ErrInvalidInput)
	// ErrInProcessStatusRetentionTooSmall is returned when the effective status
	// retention maximum entry count is smaller than the effective queue capacity: a
	// queued or in-flight generation must never be retention-evicted, so the status map
	// must hold at least one entry per queued slot.
	ErrInProcessStatusRetentionTooSmall = fmt.Errorf("delivery: in-process status max entries must be at least the queue capacity (a queued generation must never be evicted): %w", sdk.ErrInvalidInput)
	// ErrInProcessWorkersInvalid is returned when the configured worker count is
	// negative.
	ErrInProcessWorkersInvalid = fmt.Errorf("delivery: in-process worker count must not be negative: %w", sdk.ErrInvalidInput)
	// ErrInProcessShutdownDeadlineInvalid is returned when the configured shutdown
	// deadline is negative.
	ErrInProcessShutdownDeadlineInvalid = fmt.Errorf("delivery: in-process shutdown deadline must not be negative: %w", sdk.ErrInvalidInput)
	// ErrInProcessMaxAttemptsInvalid is returned when the configured attempt cap is
	// negative.
	ErrInProcessMaxAttemptsInvalid = fmt.Errorf("delivery: in-process max attempts must not be negative: %w", sdk.ErrInvalidInput)
)

// inProcessItem is one admitted unit of delivery work carried through the bounded
// queue. payload is the sealed command envelope; attempt is the number of process
// attempts already spent (1 on first admission — the process-local retry that grows
// it lands in AV3D-4.3). executionID is the opaque unit-of-work ID (never a recipient
// or the logical key). kind/purpose/logicalKey are the secret-free routing metadata.
// gen is the arbiter-assigned generation number the worker fences against: a queued or
// in-flight item whose gen no longer matches its key's current record was superseded
// and must not start or record a transition (AV3D-4.2).
type inProcessItem struct {
	executionID string
	kind        string
	purpose     string
	logicalKey  string
	payload     []byte
	attempt     int
	gen         uint64
}

// keyRecord is the arbiter's per-logical-key state (AV3D-4.2). It holds the CURRENT
// (highest) generation admitted for the key, its opaque execution ID, and its lifecycle
// state, plus retention bookkeeping. The map holds exactly one record per key — the
// latest generation — so LatestStatus is deterministic latest-by-key, and a superseded
// generation is fenced simply by comparing an item's gen against the record's gen.
type keyRecord struct {
	// gen is the current (highest) generation admitted under this key. A worker whose
	// item.gen != gen was superseded.
	gen uint64
	// execID is the opaque execution ID of the current generation.
	execID string
	// state is the sdk/capabilities/work lifecycle state folded to the auth projection
	// by normalizeStatus.
	state work.Status
	// active reports whether the current generation is queued or processing (a
	// coalesce target for submit-once, and never eligible for eviction).
	active bool
	// admitting reports whether the current generation is still being admitted to the
	// bounded channel. A concurrent same-key Submit waits on ready rather than admitting
	// a duplicate or coalescing onto a reservation that may still roll back.
	admitting bool
	// ready is closed when the current admitting reservation resolves (commits or rolls
	// back), unblocking a concurrent same-key Submit so it can re-evaluate.
	ready chan struct{}
	// seq is the insertion order used to evict the oldest terminal entry under the
	// maximum-entries bound.
	seq uint64
	// updatedAt is the last-transition time used for TTL eviction of terminal entries.
	updatedAt time.Time
}

// InProcessQueueConfig configures the bounded admission queue and its process-local
// arbiter. Every zero field selects its package default.
type InProcessQueueConfig struct {
	// Capacity is the finite queue depth; 0 → defaultInProcessCapacity.
	Capacity int
	// AdmissionDeadline bounds how long an enqueue waits for a free slot; 0 →
	// defaultInProcessAdmissionDeadline.
	AdmissionDeadline time.Duration
	// StatusMaxEntries is the finite maximum number of latest-by-key status records the
	// arbiter retains; 0 → defaultInProcessStatusMaxEntries. Beyond it, a fresh
	// admission evicts the oldest terminal record.
	StatusMaxEntries int
	// StatusTTL bounds how long a terminal latest-by-key status is retained; 0 →
	// defaultInProcessStatusTTL.
	StatusTTL time.Duration
	// Now overrides the time source (primarily for tests); nil → time.Now.
	Now func() time.Time
}

// Validate reports whether the queue knobs are well-formed (AV3D-4.5): a negative
// capacity, admission deadline, status max-entries, or status TTL is a loud
// construction error, and the effective status retention must be at least the effective
// queue capacity so a queued generation is never evicted. A zero knob is valid (it
// selects the package default). It returns a typed error wrapping sdk.ErrInvalidInput.
func (c InProcessQueueConfig) Validate() error {
	if c.Capacity < 0 {
		return fmt.Errorf("%w: %d", ErrInProcessCapacityInvalid, c.Capacity)
	}
	if c.AdmissionDeadline < 0 {
		return fmt.Errorf("%w: %s", ErrInProcessAdmissionDeadlineInvalid, c.AdmissionDeadline)
	}
	if c.StatusMaxEntries < 0 {
		return fmt.Errorf("%w: %d", ErrInProcessStatusMaxEntriesInvalid, c.StatusMaxEntries)
	}
	if c.StatusTTL < 0 {
		return fmt.Errorf("%w: %s", ErrInProcessStatusTTLInvalid, c.StatusTTL)
	}
	capacity := c.Capacity
	if capacity <= 0 {
		capacity = defaultInProcessCapacity
	}
	maxEntries := c.StatusMaxEntries
	if maxEntries <= 0 {
		maxEntries = defaultInProcessStatusMaxEntries
	}
	if maxEntries < capacity {
		return fmt.Errorf("%w: max entries %d < capacity %d", ErrInProcessStatusRetentionTooSmall, maxEntries, capacity)
	}
	return nil
}

// InProcessQueue is the bounded admission side AND process-local arbiter of the
// in-process delivery runtime (AV3D-4.1/4.2). It implements Dispatcher, so
// delivery.Service submits sealed command envelopes through it exactly as it would
// through the durable jobs dispatcher, and the InProcessRuntime drains it with a fixed
// worker pool.
//
// Under ONE lock it provides submit-once/replace/latest semantics with logical keys and
// generations:
//
//   - Submit COALESCES onto the active (queued or processing) generation for a logical
//     key — a duplicate returns the active execution ID and admits no second item.
//   - Replace SUPERSEDES prior generations: a superseded generation that is still queued
//     never starts, and a superseded generation that is in flight has its checkpoint and
//     completion fenced (ErrDeliverySuperseded) so a stale execution cannot record a
//     transition over the current one.
//   - LatestStatus reports the current generation's lifecycle state — deterministic
//     latest-by-key — folded to the auth projection by the service.
//
// Status retention is BOUNDED: a finite maximum entry count and a TTL, so the map never
// grows with process lifetime. An evicted or expired key reads as unknown
// (sdk.ErrNotFound).
//
// It is a SEPARATE object from the runtime on purpose: admission must be wired into the
// delivery service BEFORE the account resolver (authService) exists, while the processor
// the pool runs is built AFTER it. Sharing this bounded buffer and arbiter between the
// early-built admitter and the late-built runtime breaks that construction cycle with no
// mutable late-binding.
//
// It is EPHEMERAL and process-local: NOTHING here survives a restart — queued work, the
// de-duplication/generation state, and the retained status are all lost — and it provides
// no cross-instance coordination. That is the honest weaker guarantee of DeliveryMode
// "in_process"; the durable posture is DeliveryMode "jobs".
type InProcessQueue struct {
	items             chan inProcessItem
	admissionDeadline time.Duration
	now               func() time.Time

	// mu is the single arbiter lock. It guards admission-closed state, the per-key
	// generation records (coalescing + supersession fencing), the monotonic sequence,
	// and the bounded, TTL'd latest-by-key status retention.
	mu         sync.Mutex
	closed     bool
	done       chan struct{}
	keys       map[string]*keyRecord
	seq        uint64
	maxEntries int
	ttl        time.Duration
}

// compile assertion: the bounded queue is a drop-in Dispatcher.
var _ Dispatcher = (*InProcessQueue)(nil)

// NewInProcessQueue builds the bounded admission queue and arbiter, filling defaults. It
// starts no goroutine — the InProcessRuntime that drains it does, and only when the host
// runs it.
func NewInProcessQueue(cfg InProcessQueueConfig) *InProcessQueue {
	capacity := cfg.Capacity
	if capacity <= 0 {
		capacity = defaultInProcessCapacity
	}
	admission := cfg.AdmissionDeadline
	if admission <= 0 {
		admission = defaultInProcessAdmissionDeadline
	}
	maxEntries := cfg.StatusMaxEntries
	if maxEntries <= 0 {
		maxEntries = defaultInProcessStatusMaxEntries
	}
	ttl := cfg.StatusTTL
	if ttl <= 0 {
		ttl = defaultInProcessStatusTTL
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &InProcessQueue{
		items:             make(chan inProcessItem, capacity),
		admissionDeadline: admission,
		now:               now,
		done:              make(chan struct{}),
		keys:              make(map[string]*keyRecord),
		maxEntries:        maxEntries,
		ttl:               ttl,
	}
}

// Submit admits payload under logicalKey and returns the execution ID of the active
// generation for that key. It is submit-once: when an active (queued or processing)
// generation already holds the key, Submit COALESCES onto it — it admits no second item
// and returns the existing execution ID. Otherwise it admits a fresh generation.
//
// It never blocks unbounded: if the queue is full it waits at most the admission
// deadline for a slot, then returns ErrDeliveryCapacity; once the runtime is shutting
// down it returns ErrDeliveryClosed. It never silently drops work.
func (q *InProcessQueue) Submit(ctx context.Context, kind, purpose, logicalKey string, payload []byte) (string, error) {
	return q.admit(ctx, kind, purpose, logicalKey, payload, false)
}

// Replace admits payload under logicalKey as a fresh generation that SUPERSEDES every
// prior generation for the key (AV3D-4.2): a still-queued prior generation never starts,
// and an in-flight prior generation has its checkpoint and completion fenced
// (ErrDeliverySuperseded) so it cannot record a transition over this one. It admits with
// the same bounded semantics as Submit (ErrDeliveryCapacity / ErrDeliveryClosed).
//
// Race honesty: replacement cannot retract a provider call already in flight — a
// superseded worker mid-send may still deliver the older message; the fence only prevents
// it from RECORDING a checkpoint or completion. This is the documented ephemeral-mode
// weakness; the freshly rendered generation supersedes the old proof where the auth flow
// supports replacement.
func (q *InProcessQueue) Replace(ctx context.Context, kind, purpose, logicalKey string, payload []byte) (string, error) {
	return q.admit(ctx, kind, purpose, logicalKey, payload, true)
}

// LatestStatus reports the generic lifecycle state of the current generation holding
// logicalKey (AV3D-4.2), deterministic latest-by-key. An unknown, never-admitted, or
// retention-evicted/expired key returns errInProcessStatusUnknown (sdk.ErrNotFound),
// normalized by the service through the existing unknown projection. It never fabricates
// a false pending/succeeded/failed.
func (q *InProcessQueue) LatestStatus(_ context.Context, logicalKey string) (string, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	rec := q.keys[logicalKey]
	if rec == nil {
		return "", errInProcessStatusUnknown
	}
	if q.expiredLocked(rec) {
		delete(q.keys, logicalKey)
		return "", errInProcessStatusUnknown
	}
	return string(rec.state), nil
}

// admit reserves and enqueues a generation under the arbiter, or coalesces onto an
// existing active generation (Submit only). It holds the single arbiter lock ONLY for the
// fast generational decision and never during the bounded channel send, so admission for
// other keys is not serialized behind one blocked send. The whole call is bounded by a
// single admission-deadline timer.
func (q *InProcessQueue) admit(ctx context.Context, kind, purpose, logicalKey string, payload []byte, replace bool) (string, error) {
	timer := time.NewTimer(q.admissionDeadline)
	defer timer.Stop()

	for {
		q.mu.Lock()
		if q.closed {
			q.mu.Unlock()
			return "", ErrDeliveryClosed
		}
		rec := q.keys[logicalKey]

		// Submit-once coalescing: a duplicate onto an active generation admits no second
		// item. If the active generation is still admitting, wait for that reservation to
		// resolve rather than coalesce onto work that may still roll back.
		if !replace && rec != nil && rec.active {
			if !rec.admitting {
				execID := rec.execID
				q.mu.Unlock()
				return execID, nil
			}
			ready := rec.ready
			done := q.done
			q.mu.Unlock()
			select {
			case <-ready:
				continue // the reservation resolved; re-evaluate under the lock
			case <-done:
				return "", ErrDeliveryClosed
			case <-ctx.Done():
				return "", ctx.Err()
			case <-timer.C:
				return "", ErrDeliveryCapacity
			}
		}

		// Reserve a fresh generation. For Replace this OVERWRITES the key's record, so any
		// prior generation (queued or in flight) is now superseded — its gen no longer
		// matches and the worker fences it.
		q.seq++
		gen := q.seq
		execID := fmt.Sprintf("inproc-%d", gen)
		ready := make(chan struct{})
		q.keys[logicalKey] = &keyRecord{
			gen:       gen,
			execID:    execID,
			state:     work.StatusPending,
			active:    true,
			admitting: true,
			ready:     ready,
			seq:       gen,
			updatedAt: q.now(),
		}
		q.evictLocked()
		done := q.done
		q.mu.Unlock()

		item := inProcessItem{
			executionID: execID,
			kind:        kind,
			purpose:     purpose,
			logicalKey:  logicalKey,
			payload:     payload,
			attempt:     1,
			gen:         gen,
		}

		sendErr := q.sendItem(ctx, item, done, timer)

		q.mu.Lock()
		cur := q.keys[logicalKey]
		if sendErr != nil {
			// Roll back the reservation, but only if a later Replace has not already
			// advanced the key past this generation.
			if cur != nil && cur.gen == gen {
				delete(q.keys, logicalKey)
			}
			close(ready)
			q.mu.Unlock()
			return "", sendErr
		}
		// Commit: mark the reservation admitted so a concurrent same-key Submit coalesces.
		if cur != nil && cur.gen == gen {
			cur.admitting = false
		}
		close(ready)
		q.mu.Unlock()
		return execID, nil
	}
}

// sendItem enqueues one item into the bounded channel within the shared admission timer
// or returns a typed capacity/closed error. It is race-free against shutdown: the queue
// channel is never closed (senders never panic); the done channel signals a shutting-down
// runtime.
func (q *InProcessQueue) sendItem(ctx context.Context, item inProcessItem, done <-chan struct{}, timer *time.Timer) error {
	select {
	case q.items <- item:
		return nil
	case <-done:
		return ErrDeliveryClosed
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	select {
	case q.items <- item:
		return nil
	case <-done:
		return ErrDeliveryClosed
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ErrDeliveryCapacity
	}
}

// evictLocked keeps the latest-by-key status map bounded (AV3D-4.2). It first sweeps
// terminal entries past their TTL, then, while the map still exceeds the maximum entry
// count, evicts the oldest TERMINAL entry. An ACTIVE (queued/processing) generation is
// never evicted, so fencing a live generation is never broken by retention — the active
// set is itself bounded by the queue capacity plus the worker pool. Caller holds q.mu.
func (q *InProcessQueue) evictLocked() {
	if q.ttl > 0 {
		for k, rec := range q.keys {
			if q.expiredLocked(rec) {
				delete(q.keys, k)
			}
		}
	}
	for len(q.keys) > q.maxEntries {
		var (
			oldestKey string
			oldestSeq uint64
			found     bool
		)
		for k, rec := range q.keys {
			if rec.active {
				continue
			}
			if !found || rec.seq < oldestSeq {
				oldestKey, oldestSeq, found = k, rec.seq, true
			}
		}
		if !found {
			// Every remaining entry is active; it cannot be evicted without breaking a
			// live generation's fence. The active set is inherently bounded.
			return
		}
		delete(q.keys, oldestKey)
	}
}

// expiredLocked reports whether a terminal record has outlived the retention TTL. An
// active record never expires. Caller holds q.mu.
func (q *InProcessQueue) expiredLocked(rec *keyRecord) bool {
	return q.ttl > 0 && !rec.active && q.now().Sub(rec.updatedAt) >= q.ttl
}

// claim transitions a queued generation to processing IF it is still the current
// generation for its key (AV3D-4.2). A superseded (replaced) or retention-evicted
// generation returns false and is NEVER processed — this is how a replaced, still-queued
// generation never starts. Caller is a worker about to process item.
func (q *InProcessQueue) claim(item inProcessItem) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	rec := q.keys[item.logicalKey]
	if rec == nil || rec.gen != item.gen {
		return false
	}
	rec.state = work.StatusRunning
	rec.updatedAt = q.now()
	return true
}

// checkpoint is the fenced checkpoint the pool passes into the processor (AV3D-4.2). It
// rejects with ErrDeliverySuperseded when item's generation is no longer current, so a
// superseded in-flight execution cannot checkpoint over the current generation. AV3D-4.2
// boundary: it persists no rendered payload (the in-memory rendered-payload checkpoint
// that makes a process-local retry reuse the same secret is AV3D-4.3) — the fence is the
// deliverable here.
func (q *InProcessQueue) checkpoint(item inProcessItem) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	rec := q.keys[item.logicalKey]
	if rec == nil || rec.gen != item.gen {
		return ErrDeliverySuperseded
	}
	rec.updatedAt = q.now()
	return nil
}

// settle records the terminal lifecycle state of a processed generation, FENCED by
// generation: a superseded or evicted generation records NOTHING (its transition is
// discarded), so a stale execution can never clobber the current generation's status.
// A completed outcome records completed, a permanent failure records dead_letter, and a
// transient failure records failed (retryable); the current generation is marked
// inactive. The runtime (AV3D-4.3) records a completed outcome through this and a
// terminal dead-letter through deadLetter (which reports whether it recorded, so the
// challenge is discarded only after a recorded terminal).
func (q *InProcessQueue) settle(item inProcessItem, err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	rec := q.keys[item.logicalKey]
	if rec == nil || rec.gen != item.gen {
		return
	}
	switch {
	case err == nil:
		rec.state = work.StatusCompleted
	case HandleErrorPermanent(err):
		rec.state = work.StatusDeadLetter
	default:
		rec.state = work.StatusFailed
	}
	rec.active = false
	rec.updatedAt = q.now()
}

// deadLetter records a terminal dead-letter for a processed generation, FENCED by
// generation (AV3D-4.3): a superseded or evicted generation records NOTHING and returns
// false, so a stale execution can neither dead-letter over the current generation nor
// trigger a discard of its challenge. It reports whether the terminal was recorded, so
// the runtime discards the minted challenge only AFTER a recorded terminal.
func (q *InProcessQueue) deadLetter(item inProcessItem) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	rec := q.keys[item.logicalKey]
	if rec == nil || rec.gen != item.gen {
		return false
	}
	rec.state = work.StatusDeadLetter
	rec.active = false
	rec.updatedAt = q.now()
	return true
}

// current reports whether item is still the current generation for its key (AV3D-4.3) —
// the fence a worker re-checks before RESENDING on a retry. A rendered retry skips the
// checkpoint (the engine only checkpoints an opaque start's freshly rendered payload), so
// this is the point that stops a generation superseded during (or before) the backoff
// wait from starting a fresh provider call. Caller must not hold q.mu.
func (q *InProcessQueue) current(item inProcessItem) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	rec := q.keys[item.logicalKey]
	return rec != nil && rec.gen == item.gen
}

// Depth reports the number of items currently queued (admitted but not yet claimed by a
// worker) and the fixed queue capacity. It is a bounded, secret-free read for host
// operational health (AV3D-5.3): a queued count at capacity indicates a saturated,
// backlogged queue. It carries no recipient, payload, or logical key — only two counts.
// The channel-length read is safe to call concurrently with admission and draining.
func (q *InProcessQueue) Depth() (queued, capacity int) {
	return len(q.items), cap(q.items)
}

// entryCount reports the number of retained latest-by-key records. It exists for the
// retention-bound proof (the map stays <= maximum entries).
func (q *InProcessQueue) entryCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.keys)
}

// beginShutdown closes admission: subsequent admits return ErrDeliveryClosed and any
// admit blocked waiting for a slot unblocks with ErrDeliveryClosed. It never closes the
// item channel (in-flight senders must not panic). It is idempotent.
func (q *InProcessQueue) beginShutdown() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.closed = true
	close(q.done)
}

// InProcessProcessor delivers one claimed in-process command and discards the minted
// challenge of one that failed terminally. *JobsProcessor satisfies it — the SAME
// transport-neutral command.Engine both delivery modes run — so the bounded pool reuses
// the exact open→initialize→checkpoint→deliver→classify policy AND the same terminal
// challenge discard, applying the SAME provider timeout, error classification, and
// observer transitions as jobs mode. It is accepted as an interface so the pool,
// admission, retry, and lifecycle can be proven with a fake processor, independent of the
// real engine.
//
// Handle's checkpoint persists a freshly rendered sealed payload before any provider send.
// The pool passes a FENCED checkpoint (it rejects a superseded generation) that ALSO
// records the sealed payload in the in-memory work item, so a process-local retry of an
// initialized command reuses the SAME secret rather than re-resolving/re-minting one
// (AV3D-4.3). Handle returns nil for a completed/skipped outcome, a plain transient error
// to retry with bounded backoff, and a permanent error (HandleErrorPermanent) to
// dead-letter immediately.
//
// Discard voids the challenge of a dead-lettered command. The runtime invokes it ONLY
// after it has recorded the terminal dead-letter, is idempotent, and a discard failure
// never resurrects the generation.
type InProcessProcessor interface {
	Handle(ctx context.Context, executionID string, payload []byte, attempt int, checkpoint func(ctx context.Context, sealed []byte) error) error
	Discard(ctx context.Context, executionID string, payload []byte) error
}

// InProcessRuntimeConfig configures the fixed worker pool and its bounded process-local
// retry (AV3D-4.3). Every zero field selects its package default.
type InProcessRuntimeConfig struct {
	// Workers is the FIXED pool size; 0 → defaultInProcessWorkers. The runtime runs
	// exactly this many delivery goroutines, never one per request.
	Workers int
	// ShutdownDeadline bounds how long Run waits for in-flight workers to observe
	// cancellation and return after the host cancels ctx; 0 → defaultInProcessShutdownDeadline.
	ShutdownDeadline time.Duration
	// MaxAttempts caps process-local delivery attempts before a transient failure becomes
	// a terminal dead-letter; 0 → defaultInProcessMaxAttempts. It mirrors the command.Engine
	// attempt budget so the bounded pool and the durable jobs runtime cap identically.
	MaxAttempts int
	// Backoff maps a just-spent attempt number (1-based) to the delay before the next
	// attempt; nil selects a capped exponential (defaultInProcessBackoffBase doubling to
	// defaultInProcessBackoffCap). The wait is context-cancellable and occupies a worker
	// slot for its (bounded) duration — the pool reschedules nothing; see backoffWait.
	Backoff func(attempt int) time.Duration
	// Logger is the runtime's operational logger; nil → slog.Default().
	Logger *slog.Logger
}

// Validate reports whether the runtime knobs are well-formed (AV3D-4.5): a negative
// worker count, shutdown deadline, or attempt cap is a loud construction error. A zero
// knob is valid (it selects the package default). It returns a typed error wrapping
// sdk.ErrInvalidInput.
func (c InProcessRuntimeConfig) Validate() error {
	if c.Workers < 0 {
		return fmt.Errorf("%w: %d", ErrInProcessWorkersInvalid, c.Workers)
	}
	if c.ShutdownDeadline < 0 {
		return fmt.Errorf("%w: %s", ErrInProcessShutdownDeadlineInvalid, c.ShutdownDeadline)
	}
	if c.MaxAttempts < 0 {
		return fmt.Errorf("%w: %d", ErrInProcessMaxAttemptsInvalid, c.MaxAttempts)
	}
	return nil
}

// InProcessRuntime runs the bounded, fixed-size worker pool that drains an
// InProcessQueue through the delivery processor (AV3D-4.1). It owns the pool lifecycle:
// Run launches exactly Workers goroutines, blocks until the host cancels ctx, then
// stops admission and drains in-flight work within the shutdown deadline.
//
// It is the host-owned runtime the overview names for DeliveryMode "in_process":
// construction (NewService) starts no goroutine, and Register starts no goroutine — the
// host explicitly runs this via Service.RunDelivery. Accepted, in-flight work is LOST
// on a crash or a hard shutdown-deadline expiry, and the queue's de-duplication and
// status are lost on restart; this mode never claims durability or cross-instance
// coordination.
type InProcessRuntime struct {
	queue            *InProcessQueue
	processor        InProcessProcessor
	workers          int
	shutdownDeadline time.Duration
	maxAttempts      int
	backoff          func(attempt int) time.Duration
	log              *slog.Logger
	running          atomic.Bool
}

// NewInProcessRuntime builds the runtime over the shared bounded queue and the built
// processor, filling defaults. A nil queue or processor is a construction error. It
// starts nothing; the host runs Run.
func NewInProcessRuntime(queue *InProcessQueue, processor InProcessProcessor, cfg InProcessRuntimeConfig) (*InProcessRuntime, error) {
	if queue == nil {
		return nil, ErrInProcessQueueRequired
	}
	if processor == nil {
		return nil, ErrInProcessProcessorRequired
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	workers := cfg.Workers
	if workers <= 0 {
		workers = defaultInProcessWorkers
	}
	shutdown := cfg.ShutdownDeadline
	if shutdown <= 0 {
		shutdown = defaultInProcessShutdownDeadline
	}
	maxAttempts := cfg.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultInProcessMaxAttempts
	}
	backoff := cfg.Backoff
	if backoff == nil {
		backoff = defaultInProcessBackoff
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &InProcessRuntime{
		queue:            queue,
		processor:        processor,
		workers:          workers,
		shutdownDeadline: shutdown,
		maxAttempts:      maxAttempts,
		backoff:          backoff,
		log:              log,
	}, nil
}

// Run launches the fixed worker pool and blocks until the host cancels ctx, then drains
// gracefully within the shutdown deadline and returns. Cancelling ctx stops admission,
// lets in-flight provider calls observe cancellation (the worker context is derived
// from ctx and propagated into each Handle), and bounds the drain: a provider ignoring
// cancellation cannot stall shutdown past ShutdownDeadline. Run may run only once at a
// time (ErrInProcessAlreadyRunning otherwise). It returns nil after a clean drain.
func (r *InProcessRuntime) Run(ctx context.Context) error {
	if !r.running.CompareAndSwap(false, true) {
		return ErrInProcessAlreadyRunning
	}
	defer r.running.Store(false)

	r.log.InfoContext(ctx, "in-process delivery runtime starting",
		"workers", r.workers,
		"capacity", cap(r.queue.items),
		"admission_deadline", r.queue.admissionDeadline,
		"shutdown_deadline", r.shutdownDeadline,
	)

	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	for i := 0; i < r.workers; i++ {
		wg.Add(1)
		workerID := fmt.Sprintf("inproc-delivery-worker-%d", i+1)
		go r.worker(workerCtx, &wg, workerID)
	}

	// Block until the host cancels ctx (shutdown). workerCtx is derived from ctx, so it
	// is done at the same instant and the workers observe cancellation immediately.
	<-ctx.Done()

	// Stop admission first: no new work is accepted and any admit blocked on capacity
	// unblocks with ErrDeliveryClosed. Queued-but-unstarted items are dropped — this
	// mode is explicitly ephemeral (a restart, or a shutdown, loses queued work).
	r.queue.beginShutdown()

	// Bounded drain: give in-flight workers up to the shutdown deadline to observe
	// cancellation and return, then stop regardless.
	if !waitBounded(&wg, r.shutdownDeadline) {
		r.log.WarnContext(context.Background(),
			"in-process delivery runtime shutdown deadline exceeded; abandoning in-flight work",
			"shutdown_deadline", r.shutdownDeadline,
		)
	}
	r.log.InfoContext(context.Background(), "in-process delivery runtime stopped")
	return nil
}

// worker is the loop for a single pool goroutine: it pulls one item and processes it,
// returning promptly once ctx is canceled. It never starts a fresh provider call after
// cancellation — a just-pulled item is dropped (ephemeral) rather than begun — while an
// already-in-flight call runs under the canceled context and aborts.
func (r *InProcessRuntime) worker(ctx context.Context, wg *sync.WaitGroup, workerID string) {
	defer wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case item := <-r.queue.items:
			if ctx.Err() != nil {
				// Cancellation raced the receive: do not begin a new provider call.
				return
			}
			r.process(ctx, item)
		}
	}
}

// process runs one item through the delivery processor under the arbiter's generational
// fence, applying the SAME bounded process-local retry, error classification, attempt cap,
// capped context-cancellable backoff, and terminal challenge discard as jobs mode
// (AV3D-4.3). A superseded (replaced) generation is skipped before start; the checkpoint
// the processor receives is fenced AND records the freshly rendered sealed payload in the
// in-memory item so a retry reuses the SAME secret; a transient failure retries after a
// bounded backoff (occupying this worker slot — the pool reschedules nothing) up to the
// attempt cap, then dead-letters; a permanent failure dead-letters immediately; and the
// minted challenge is discarded ONLY after the terminal dead-letter is recorded. There is
// NO claim lease across restart: shutdown drops in-flight and backing-off work (ephemeral).
func (r *InProcessRuntime) process(ctx context.Context, item inProcessItem) {
	if !r.queue.claim(item) {
		// The generation was superseded (or evicted) before it started; it must not run.
		r.log.DebugContext(ctx, "in-process delivery item superseded before start; skipping",
			"execution_id", item.executionID,
		)
		return
	}

	// payload is the in-memory checkpoint: the checkpoint closure the processor calls
	// before a send replaces it with the freshly rendered SEALED envelope (fenced by
	// generation), so a process-local retry of an initialized command reuses the SAME
	// secret rather than re-resolving/re-minting one. It starts as the admitted payload
	// (opaque for an enumeration-safe start, already-rendered otherwise).
	payload := item.payload
	checkpoint := func(_ context.Context, sealed []byte) error {
		if err := r.queue.checkpoint(item); err != nil {
			return err // fenced: a superseded/evicted generation must not send
		}
		payload = sealed
		return nil
	}

	for attempt := item.attempt; ; attempt++ {
		err := r.handleOnce(ctx, item, payload, attempt, checkpoint)
		if err == nil {
			// Completed or a non-failed skip: record the terminal success (fenced).
			r.queue.settle(item, nil)
			return
		}
		if HandleErrorPermanent(err) || attempt >= r.maxAttempts {
			// Permanent failure OR the attempt budget is spent: dead-letter, then discard.
			r.deadLetter(ctx, item, payload, err, attempt)
			return
		}
		// Transient failure under the cap. If a Replace already superseded this generation
		// (e.g. its checkpoint was fenced), stop now — no backoff, no resend, no transition.
		if !r.queue.current(item) {
			r.log.DebugContext(ctx, "in-process delivery superseded; skipping retry",
				"execution_id", item.executionID,
			)
			return
		}
		if !r.backoffWait(ctx, r.backoff(attempt)) {
			// Shutdown interrupted the backoff wait: drop the in-flight retry (ephemeral —
			// this mode loses in-flight work on shutdown) without recording a terminal state.
			r.log.DebugContext(ctx, "in-process delivery retry canceled during backoff; dropping",
				"execution_id", item.executionID, "attempt", attempt,
			)
			return
		}
		if !r.queue.current(item) {
			// A Replace superseded this generation during the backoff wait: it must not
			// start a fresh provider call, and its transition already belongs to the
			// replacement. Stop without sending or recording.
			r.log.DebugContext(ctx, "in-process delivery retry superseded during backoff; skipping",
				"execution_id", item.executionID,
			)
			return
		}
	}
}

// handleOnce runs one processor.Handle attempt inside a NARROW recover boundary around the
// per-job execution (the provider render/send plus the engine step), converting a panic into
// a permanent (terminal) failure so a panicking job dead-letters with lifecycle evidence
// instead of propagating out of the worker goroutine and taking down the entire host — HTTP
// included. It mirrors the generic fenced runner's processOnce recover (sdk/foundation/workers)
// so both delivery modes contain a panicking job rather than crashing the process; the jobs
// mode is already protected by that runner's recover. The boundary is exactly this call, not
// the pool loop: on recovery the worker returns from process and pulls the next item. The
// panic value is sanitized (sanitizePanic) — the evidence names the execution ID and a
// secret-free panic representation, never the decrypted payload, destination, or secret.
func (r *InProcessRuntime) handleOnce(ctx context.Context, item inProcessItem, payload []byte, attempt int, checkpoint func(ctx context.Context, sealed []byte) error) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			r.log.ErrorContext(ctx, "panic recovered in in-process delivery job",
				"execution_id", item.executionID,
				"attempt", attempt,
				"panic", sanitizePanic(rec),
				"stack", string(debug.Stack()),
			)
			// Convert to a terminal (permanent) failure: the worker returns normally and
			// keeps processing subsequent jobs; the job dead-letters via the existing path.
			err = errInProcessDeliveryPanic
		}
	}()
	return r.processor.Handle(ctx, item.executionID, payload, attempt, checkpoint)
}

// sanitizePanic reduces a recovered panic value to a secret-free string for lifecycle
// evidence. A runtime-generated panic (nil dereference, index out of range, ...) carries no
// application data, so its message is surfaced verbatim for debugging. Any OTHER panic value
// may embed a decrypted payload, destination, or secret if a buggy or hostile provider
// panicked with one, so only its Go type is surfaced — never its content.
func sanitizePanic(rec any) string {
	if err, ok := rec.(runtime.Error); ok {
		return err.Error()
	}
	return fmt.Sprintf("%T", rec)
}

// deadLetter records the terminal dead-letter (fenced) and then discards the minted
// challenge best-effort (AV3D-4.3) — ONLY after the terminal is recorded, and never for a
// superseded generation (whose transition belongs to the replacement). A discard failure
// is logged but never resurrects the generation.
func (r *InProcessRuntime) deadLetter(ctx context.Context, item inProcessItem, payload []byte, cause error, attempt int) {
	if !r.queue.deadLetter(item) {
		// Superseded/evicted: the current generation owns status; do not discard.
		r.log.DebugContext(ctx, "in-process delivery dead-letter skipped for superseded generation",
			"execution_id", item.executionID,
		)
		return
	}
	r.log.WarnContext(ctx, "in-process delivery dead-lettered",
		"execution_id", item.executionID,
		"attempt", attempt,
		"permanent", HandleErrorPermanent(cause),
		"reason", cause.Error(),
	)
	if err := r.processor.Discard(ctx, item.executionID, payload); err != nil {
		// Best-effort: the dead-letter is already recorded, so a failed discard leaves the
		// challenge un-voided rather than resurrecting the generation.
		r.log.WarnContext(ctx, "in-process delivery challenge discard failed after dead-letter",
			"execution_id", item.executionID, "reason", err.Error(),
		)
	}
}

// backoffWait sleeps for d on the calling worker, returning true when it elapses and
// false when ctx is canceled first (AV3D-4.3). It uses a single timer — no busy-loop and
// no per-retry goroutine — so a shutdown interrupts the wait promptly and never leaks a
// goroutine. A non-positive d returns immediately (true unless ctx is already canceled).
func (r *InProcessRuntime) backoffWait(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// defaultInProcessBackoff is a capped exponential (base * 2^(attempt-1), ceilinged at the
// cap), mirroring command.Engine's default so the bounded pool retries on the same
// schedule the durable jobs runtime does. attempt is 1-based (the count already spent).
func defaultInProcessBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := defaultInProcessBackoffBase
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= defaultInProcessBackoffCap {
			return defaultInProcessBackoffCap
		}
	}
	if d > defaultInProcessBackoffCap {
		return defaultInProcessBackoffCap
	}
	return d
}

// waitBounded waits for wg with a deadline. It reports true when the group completed
// within d and false when the deadline elapsed first. The helper goroutine that waits
// on wg outlives a false return until the group actually completes; the caller bounds
// how long IT waits, not the work itself.
func waitBounded(wg *sync.WaitGroup, d time.Duration) bool {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}
