// Package runner is this host's retention driver. A ticker that invokes pocket
// services is a driving adapter — time is the caller — so it is the retention
// host pocket's inbound. Run drives the authentication maintenance loop
// (Components.Maintenance.Run: a first pass within a tenth of its hour, then
// hourly) beside an hourly drain of the authorization operation records past
// their retention (Components.Mutations.ExpireOperations), and Health reads
// the maintenance service's last report for /healthz.
//
// Run one runner per database. Concurrent passes are safe, not cheap, and a
// disk archive sink is per instance.
package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/maintenance"
	"github.com/gopernicus/gopernicus/sdk"
)

// The error kinds Health reports: a closed, text-free set, because /healthz is
// unauthenticated.
const (
	KindNone            = "none"
	KindForbidden       = "forbidden"
	KindBelowFloor      = "below_floor"
	KindArchiveFailed   = "archive_failed"
	KindBudgetExhausted = "budget_exhausted"
	KindOther           = "other"
)

// OperationBatch bounds one ExpireOperations call. A tick drains batches until
// a short one.
const OperationBatch = 500

// errStepPanicked reports an expiry call that panicked and was recovered.
var errStepPanicked = errors.New("retention step panicked")

// Maintainer is authentication's Components.Maintenance: the blocking pass
// loop and the last pass it completed.
type Maintainer interface {
	Run(ctx context.Context) error
	LastReport() (maintenance.Report, bool)
}

// OperationExpirer is authorization's Components.Mutations.
type OperationExpirer interface {
	ExpireOperations(ctx context.Context, limit int) (int, error)
}

// Runner drives retention for one instance.
type Runner struct {
	maintenance Maintainer
	operations  OperationExpirer
	interval    time.Duration
	log         *slog.Logger
}

// Health is the last maintenance pass as /healthz reports it. LastPass is zero
// before the first pass completes. StepsFailed counts the pass's failed steps;
// ErrorKind is the first failed step's kind, else budget_exhausted when the
// pass's budget stopped it with rows left, else none.
type Health struct {
	LastPass    time.Time
	StepsFailed int
	ErrorKind   string
}

// New returns a Runner over the maintenance service and the operation
// expirer, expiring operations every interval. A nil service, a non-positive
// interval or a nil logger is sdk.ErrInvalidInput.
func New(m Maintainer, ops OperationExpirer, interval time.Duration, log *slog.Logger) (*Runner, error) {
	if m == nil || ops == nil || interval <= 0 || log == nil {
		return nil, fmt.Errorf("retention runner: the maintenance service, the operation expirer, a positive interval and a logger are required: %w", sdk.ErrInvalidInput)
	}
	return &Runner{maintenance: m, operations: ops, interval: interval, log: log}, nil
}

// Run blocks until ctx ends: the maintenance loop in its own goroutine, and an
// operation-expiry drain at start and then every interval. It returns once
// both have stopped.
func (r *Runner) Run(ctx context.Context) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := r.maintenance.Run(ctx); err != nil {
			r.log.ErrorContext(ctx, "maintenance loop stopped", "error_kind", kind(err))
		}
	}()
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		r.expireOperations(ctx)
		select {
		case <-ctx.Done():
			<-done
			return
		case <-ticker.C:
		}
	}
}

// Health reports the maintenance service's last pass.
func (r *Runner) Health() Health {
	rep, ok := r.maintenance.LastReport()
	if !ok {
		return Health{ErrorKind: KindNone}
	}
	h := Health{LastPass: rep.FinishedAt, ErrorKind: KindNone}
	for _, st := range rep.Steps {
		if st.Err == nil {
			continue
		}
		h.StepsFailed++
		if h.ErrorKind == KindNone {
			h.ErrorKind = kind(st.Err)
		}
	}
	if h.ErrorKind == KindNone && rep.BudgetExhausted {
		h.ErrorKind = KindBudgetExhausted
	}
	return h
}

// Detail is h as /healthz detail fields: maintenance_last_pass (RFC 3339, null
// before the first pass), maintenance_steps_failed and
// maintenance_last_error_kind.
func (h Health) Detail() map[string]any {
	var last any
	if !h.LastPass.IsZero() {
		last = h.LastPass.UTC().Format(time.RFC3339)
	}
	return map[string]any{
		"maintenance_last_pass":       last,
		"maintenance_steps_failed":    h.StepsFailed,
		"maintenance_last_error_kind": h.ErrorKind,
	}
}

// expireOperations drains the operation records past their retention in
// OperationBatch calls until a short batch, logging one line with the total.
// A failure is logged by kind and the next tick retries.
func (r *Runner) expireOperations(ctx context.Context) {
	total := 0
	for {
		n, err := r.expireBatch(ctx)
		total += n
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			r.log.ErrorContext(ctx, "retention step failed; continuing", "step", "operations", "count", total, "error_kind", kind(err))
			return
		}
		if n < OperationBatch {
			break
		}
	}
	r.log.InfoContext(ctx, "retention step", "step", "operations", "count", total)
}

// expireBatch makes one ExpireOperations call, recovering a panic into an
// error.
func (r *Runner) expireBatch(ctx context.Context) (n int, err error) {
	defer func() {
		if p := recover(); p != nil {
			r.log.ErrorContext(ctx, "retention step panicked", "step", "operations", "panic", fmt.Sprint(p), "stack", string(debug.Stack()))
			n, err = 0, errStepPanicked
		}
	}()
	return r.operations.ExpireOperations(ctx, OperationBatch)
}

// kind reduces err to one of the closed error kinds.
func kind(err error) string {
	switch {
	case errors.Is(err, maintenance.ErrArchiveFailed):
		return KindArchiveFailed
	case errors.Is(err, maintenance.ErrBelowRetentionFloor):
		return KindBelowFloor
	case errors.Is(err, sdk.ErrForbidden):
		return KindForbidden
	default:
		return KindOther
	}
}
