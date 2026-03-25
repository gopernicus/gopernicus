package workers

import "sync/atomic"

// Stats holds a point-in-time snapshot of pool statistics.
type Stats struct {
	ActiveWorkers int64 // Currently running worker goroutines
	Iterations    int64 // Total work function calls (success + error + no-work)
	Errors        int64 // Work function calls that returned a non-nil, non-ErrNoWork error
	Panics        int64 // Recovered panics
}

// stats tracks internal counters using atomic operations.
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
