package delivery

import (
	"context"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

// Claim is one claimed delivery job a jobs-mode transport hands the
// authentication processor (AV3D-3.1). It is stdlib-typed so a composition adapter
// builds it from a generic job without the authentication core importing the jobs
// pocket. Payload is the sealed command envelope; Attempt is the number of process
// attempts already spent; Checkpoint persists a freshly rendered sealed payload under
// the current claim fence before any provider send.
type Claim struct {
	// ExecutionID is the opaque unit-of-work ID (never a recipient or the logical key),
	// used only for the best-effort lifecycle observation the transport emits.
	ExecutionID string
	Payload     []byte
	Attempt     int
	Checkpoint  func(ctx context.Context, sealed []byte) error
}

// JobRuntime is the narrow, stdlib-typed seam a composition adapter registers
// on the generic jobs runtime for DeliveryMode "jobs" (AV3D-3.1). Kind is the job kind
// (JobKind); Handle runs the delivery processor over one claim (returning nil
// for a completed/skipped outcome, a plain secret-free error for a transient retry, and
// a permanent-classified error — DeliveryErrorPermanent reports true — for an immediate
// dead-letter); Discard is the per-kind terminal hook the runtime invokes AFTER it
// records a dead-letter, voiding the undeliverable challenge. Purged is the batch hook
// a host calls after driving the generic terminal purge, so the optional lifecycle
// observer emits a purged event. Runtime.JobRuntime returns it only when the
// jobs-mode processor is wired.
type JobRuntime struct {
	Kind    string
	Handle  func(ctx context.Context, claim Claim) error
	Discard func(ctx context.Context, executionID string, payload []byte) error
	Purged  func(ctx context.Context, count int)
}

// InProcessConfig tunes the bounded, EPHEMERAL in-process delivery runtime
// (DeliveryMode "in_process", authv3-delivery-refactor AV3D-4.5). Every knob is
// NIL-SAFE: a ZERO value selects the package default, so the zero InProcessConfig
// is a valid, fully-defaulted configuration. A NEGATIVE bound (or a StatusMaxEntries
// smaller than QueueCapacity) fails LOUDLY at construction with a typed error wrapping
// sdk.ErrInvalidInput — an invalid bound is never silently coerced to a default. The
// whole struct is meaningful ONLY for DeliveryMode "in_process".
//
// MULTI-INSTANCE WARNING: these bounds are PER PROCESS. The in-process runtime keeps its
// queue, its fixed worker pool, its submit-once de-duplication, its replace/generation
// arbiter, and its latest-by-key status entirely in one process's memory. Two instances
// of the host do NOT share any of it: the same logical delivery admitted on two
// instances is de-duplicated on NEITHER, so BOTH can render and BOTH can send — a user
// may receive two messages. There is no cross-instance coordination and no durability;
// accepted, in-flight work is lost on a crash or restart. For a multi-instance
// deployment use DeliveryMode "jobs" (durable, cross-instance de-duplicated).
type InProcessConfig struct {
	// Workers is the FIXED worker-pool size (never one goroutine per request); 0 selects
	// the package default. Negative → ErrInProcessWorkersInvalid.
	Workers int
	// QueueCapacity is the FINITE admission-queue depth; 0 selects the package default.
	// Negative → ErrInProcessCapacityInvalid.
	QueueCapacity int
	// AdmissionDeadline bounds how long an enqueue waits for a free slot before returning
	// a typed capacity error; 0 selects the package default. Negative →
	// ErrInProcessAdmissionDeadlineInvalid.
	AdmissionDeadline time.Duration
	// ShutdownDeadline bounds how long Runtime.Run waits for in-flight workers to drain
	// after the host cancels its context; 0 selects the package default. Negative →
	// ErrInProcessShutdownDeadlineInvalid.
	ShutdownDeadline time.Duration
	// MaxAttempts caps process-local delivery attempts before a transient failure becomes
	// a terminal dead-letter; 0 selects the package default. Negative →
	// ErrInProcessMaxAttemptsInvalid.
	MaxAttempts int
	// StatusMaxEntries bounds the latest-by-key status map so retention never grows with
	// process lifetime; 0 selects the package default. It must be at least QueueCapacity
	// (a queued generation is never evicted): a smaller value →
	// ErrInProcessStatusRetentionTooSmall. Negative → ErrInProcessStatusMaxEntriesInvalid.
	StatusMaxEntries int
	// StatusTTL bounds how long a terminal latest-by-key status is retained before it
	// reads as unknown; 0 selects the package default. Negative →
	// ErrInProcessStatusTTLInvalid.
	StatusTTL time.Duration
}

// Runtime.Run runs the bounded, EPHEMERAL in-process delivery runtime until ctx is
// canceled (DeliveryMode "in_process", authv3-delivery-refactor AV3D-4.1). The host
// owns the lifecycle: call it in a goroutine and cancel ctx to stop. It launches a
// FIXED worker pool over a FINITE admission queue — never one goroutine per request —
// and, on cancellation, stops admission, lets in-flight provider calls observe
// cancellation, and drains within a bounded shutdown window. When the in-process
// runtime is not wired (any other DeliveryMode) it is a no-op (returns nil
// immediately), so a host may call it unconditionally.
//
// This mode is process-local and EPHEMERAL: accepted, in-flight delivery work is LOST
// on a crash or restart, there is no cross-instance coordination, and its process-local
// de-duplication (submit-once/replace by logical key), generation fencing, and bounded
// latest-by-key status are all lost on restart (AV3D-4.2). Running the host as MULTIPLE
// instances gives each its OWN queue, de-duplication, and status, so the same logical
// delivery admitted on two instances is de-duplicated on neither — both can render and
// both can send (a user may receive two messages). The durable, cross-instance
// de-duplicated posture is DeliveryMode "jobs".
func (s *Runtime) Run(ctx context.Context) error {
	if s.inProcessRuntime == nil {
		return nil
	}
	return s.inProcessRuntime.Run(ctx)
}

// InProcessQueueDepth reports the bounded in-process delivery queue's current depth and
// capacity for host operational health (DeliveryMode "in_process", AV3D-5.3). ok is true
// only in in_process mode; in every other mode it returns 0, 0, false — the backlog of
// the durable "jobs" mode lives in the generic jobs store, not process-local, so it is not
// observable here. The two counts are bounded and secret-free: they carry no recipient,
// payload, or logical key. A queued count at capacity indicates a saturated, backlogged
// queue.
func (s *Runtime) QueueDepth() (queued, capacity int, ok bool) {
	if s.inProcessQueue == nil {
		return 0, 0, false
	}
	queued, capacity = s.inProcessQueue.Depth()
	return queued, capacity, true
}

// JobRuntime returns the narrow, stdlib-typed jobs-mode delivery seam a
// composition adapter registers on the generic jobs runtime (authv3-delivery-refactor
// AV3D-3.1). ok is true only when DeliveryMode is "jobs" and Config.DeliveryDispatcher
// is wired — the processor is fully built (its collaborators, including this Service's
// account resolver, are attached) BEFORE this returns, so a handler can never run
// against a half-built service. In every other mode ok is false and the host wires no
// handler. It starts no goroutine: the host owns the jobs runtime lifecycle.
func (s *Runtime) JobRuntime() (JobRuntime, bool) {
	if s.jobsProcessor == nil {
		return JobRuntime{}, false
	}
	p := s.jobsProcessor
	return JobRuntime{
		Kind: JobKind,
		Handle: func(ctx context.Context, claim Claim) error {
			return p.Handle(ctx, claim.ExecutionID, claim.Payload, claim.Attempt, claim.Checkpoint)
		},
		Discard: func(ctx context.Context, executionID string, payload []byte) error {
			return p.Discard(ctx, executionID, payload)
		},
		Purged: func(ctx context.Context, count int) {
			p.ObservePurge(ctx, count)
		},
	}, true
}

// Status returns the current delivery status for a receipt key (design
// §6.1.1). A session-gated caller polls it to learn that delivery failed without
// holding the start request open; the caller's handler must enforce the live
// session. An unknown key is sdk.ErrNotFound; the outbox being off is a wrapped
// sdk.ErrForbidden.
func (s *Runtime) Status(ctx context.Context, receiptKey string) (Status, error) {
	if s.status == nil {
		return Status{}, sdk.ErrNotFound
	}
	return s.status.Status(ctx, receiptKey)
}

// Validate checks admission, retention and runtime bounds without starting work.
func (c InProcessConfig) Validate() error {
	queue := inProcessQueueConfig{Capacity: c.QueueCapacity, AdmissionDeadline: c.AdmissionDeadline, StatusMaxEntries: c.StatusMaxEntries, StatusTTL: c.StatusTTL}
	if err := queue.Validate(); err != nil {
		return err
	}
	return (inProcessRuntimeConfig{Workers: c.Workers, ShutdownDeadline: c.ShutdownDeadline, MaxAttempts: c.MaxAttempts}).Validate()
}

// Runtime owns the configured delivery execution mode. The host starts it or
// registers the explicitly returned job handler; construction starts no worker.
type Runtime struct {
	jobsProcessor    *JobsProcessor
	inProcessRuntime *InProcessRuntime
	inProcessQueue   *InProcessQueue
	status           interface {
		Status(context.Context, string) (Status, error)
	}
}

func NewRuntime(processor *JobsProcessor, runtime *InProcessRuntime, queue *InProcessQueue, status interface {
	Status(context.Context, string) (Status, error)
}) (*Runtime, error) {
	if processor != nil && runtime != nil {
		return nil, fmt.Errorf("delivery: choose jobs or in-process runtime: %w", sdk.ErrInvalidInput)
	}
	if (runtime == nil) != (queue == nil) {
		return nil, fmt.Errorf("delivery: in-process queue and runtime must be supplied together: %w", sdk.ErrInvalidInput)
	}
	if runtime != nil && runtime.queue != queue {
		return nil, fmt.Errorf("delivery: runtime must drain the supplied queue: %w", sdk.ErrInvalidInput)
	}
	if nilDependency(status) {
		status = nil
	}
	return &Runtime{jobsProcessor: processor, inProcessRuntime: runtime, inProcessQueue: queue, status: status}, nil
}
