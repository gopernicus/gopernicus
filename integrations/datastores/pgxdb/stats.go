package pgxdb

import "time"

// PoolStats is a point-in-time view of the connection pool. Counts and
// durations under "cumulative" are totals since Open. It is a pgxdb-owned
// value: no pgxpool type crosses this package.
type PoolStats struct {
	// TotalConns is every connection the pool holds: acquired, idle and
	// constructing.
	TotalConns int32
	// AcquiredConns are in use by callers.
	AcquiredConns int32
	// IdleConns are open and available.
	IdleConns int32
	// ConstructingConns are being established.
	ConstructingConns int32
	// MaxConns is the pool's configured ceiling.
	MaxConns int32

	// Cumulative acquires: successful, canceled before a connection was
	// available, and those that had to wait for one (empty).
	AcquireCount         int64
	CanceledAcquireCount int64
	EmptyAcquireCount    int64
	// AcquireDuration is the total time spent acquiring successfully.
	AcquireDuration time.Duration
	// EmptyAcquireWaitTime is the total time spent waiting in empty acquires.
	EmptyAcquireWaitTime time.Duration

	// Cumulative connection churn.
	NewConnsCount           int64
	MaxLifetimeDestroyCount int64
	MaxIdleDestroyCount     int64
}

// Stats returns the pool's current statistics, for a host's pool gauges. It is
// safe for concurrent use and after Close (the pool's final counts); a nil *DB
// returns the zero value.
func (d *DB) Stats() PoolStats {
	if d == nil || d.pool == nil {
		return PoolStats{}
	}
	s := d.pool.Stat()
	return PoolStats{
		TotalConns:              s.TotalConns(),
		AcquiredConns:           s.AcquiredConns(),
		IdleConns:               s.IdleConns(),
		ConstructingConns:       s.ConstructingConns(),
		MaxConns:                s.MaxConns(),
		AcquireCount:            s.AcquireCount(),
		CanceledAcquireCount:    s.CanceledAcquireCount(),
		EmptyAcquireCount:       s.EmptyAcquireCount(),
		AcquireDuration:         s.AcquireDuration(),
		EmptyAcquireWaitTime:    s.EmptyAcquireWaitTime(),
		NewConnsCount:           s.NewConnsCount(),
		MaxLifetimeDestroyCount: s.MaxLifetimeDestroyCount(),
		MaxIdleDestroyCount:     s.MaxIdleDestroyCount(),
	}
}
