package workers

import "sync/atomic"

// Stats is a point-in-time snapshot of a pool's counters.
type Stats struct {
	ActiveWorkers int64 // worker goroutines currently running
	Iterations    int64 // total WorkFunc calls (success, error, and no-work)
	Errors        int64 // WorkFunc calls that returned a non-nil, non-ErrNoWork error
	Panics        int64 // panics recovered inside a WorkFunc call
}

// stats holds the live counters behind atomics.
type stats struct {
	activeWorkers atomic.Int64
	iterations    atomic.Int64
	errors        atomic.Int64
	panics        atomic.Int64
}

func (s *stats) snapshot() Stats {
	return Stats{
		ActiveWorkers: s.activeWorkers.Load(),
		Iterations:    s.iterations.Load(),
		Errors:        s.errors.Load(),
		Panics:        s.panics.Load(),
	}
}
