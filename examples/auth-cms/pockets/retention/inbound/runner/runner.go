// Package runner is this host's retention driver. A ticker that invokes pocket
// services is a driving adapter — time is the caller — so it is the retention
// host pocket's inbound. Each tick runs every configured step in order: cmd
// runs one Runner with the authentication maintenance pass and the
// authorization operation expiry, and one with the cms inquiry purge on its
// own interval, beside the delivery terminal purge cmd schedules. A failing or
// panicking step is logged and the next step and the next tick still run.
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

// errStepPanicked reports a step that panicked and was recovered.
var errStepPanicked = errors.New("retention step panicked")

// Passer runs one bounded retention pass — authentication's
// Components.Maintenance.
type Passer interface {
	Pass(ctx context.Context) (maintenance.Report, error)
}

// OperationExpirer expires authorization operation records past their
// retention — authorization's Components.Mutations.
type OperationExpirer interface {
	ExpireOperations(ctx context.Context, limit int) (int, error)
}

// Step is one retention task a tick runs: its name in the log line and the call
// that reports how many rows it disposed of.
type Step struct {
	Name string
	Run  func(context.Context) (int, error)
}

// Runner ticks its steps on a fixed interval.
type Runner struct {
	steps    []Step
	interval time.Duration
	log      *slog.Logger
}

// MaintenanceStep runs the authentication maintenance pass as one step. Its
// count is every row the pass disposed of; its error is the pass's (nil unless
// every step of the pass failed — the report logs each step itself).
func MaintenanceStep(p Passer) Step {
	return Step{Name: "maintenance", Run: func(ctx context.Context) (int, error) {
		rep, err := p.Pass(ctx)
		total := 0
		for _, st := range rep.Steps {
			total += st.Count
		}
		return total, err
	}}
}

// OperationsStep expires at most batch operation records per tick as the step
// named operations.
func OperationsStep(e OperationExpirer, batch int) Step {
	return Step{Name: "operations", Run: func(ctx context.Context) (int, error) {
		return e.ExpireOperations(ctx, batch)
	}}
}

// New returns a Runner over steps every interval. A non-positive interval, a
// nil logger or a step without a name or call is sdk.ErrInvalidInput.
func New(interval time.Duration, log *slog.Logger, steps ...Step) (*Runner, error) {
	if interval <= 0 || log == nil {
		return nil, fmt.Errorf("retention runner: a positive interval and a logger are required: %w", sdk.ErrInvalidInput)
	}
	for _, st := range steps {
		if st.Name == "" || st.Run == nil {
			return nil, fmt.Errorf("retention runner: a step needs a name and a call: %w", sdk.ErrInvalidInput)
		}
	}
	return &Runner{steps: append([]Step(nil), steps...), interval: interval, log: log}, nil
}

// Tick runs every step once, in order, logging one line per step.
func (r *Runner) Tick(ctx context.Context) {
	for _, st := range r.steps {
		n, err := r.run(ctx, st)
		if err != nil {
			r.log.ErrorContext(ctx, "retention step failed; continuing", "step", st.Name, "count", n, "error_kind", errorKind(err))
			continue
		}
		r.log.InfoContext(ctx, "retention step", "step", st.Name, "count", n)
	}
}

// Run ticks once at start and then every interval until ctx ends.
func (r *Runner) Run(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		r.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// run calls one step, recovering a panic into an error.
func (r *Runner) run(ctx context.Context, st Step) (n int, err error) {
	defer func() {
		if p := recover(); p != nil {
			r.log.ErrorContext(ctx, "retention step panicked", "step", st.Name, "panic", fmt.Sprint(p), "stack", string(debug.Stack()))
			n, err = 0, errStepPanicked
		}
	}()
	return st.Run(ctx)
}

// errorKind is a stable, text-free label for the log line.
func errorKind(err error) string {
	switch {
	case errors.Is(err, errStepPanicked):
		return "panic"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "canceled"
	case errors.Is(err, maintenance.ErrArchiveFailed):
		return "archive_failed"
	case errors.Is(err, sdk.ErrForbidden):
		return "forbidden"
	default:
		return "other"
	}
}
