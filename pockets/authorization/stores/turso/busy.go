package turso

import (
	"context"
	"strings"
	"time"
)

// busy-retry discipline: libSQL/SQLite has no row locks, so the atomic mutation
// repository serializes writers with the connector's BEGIN IMMEDIATE transaction
// (the auth v3 precedent) — a contending writer WAITS at the write intent rather
// than losing an update. When serialization times out under load, the residual
// surface is SQLITE_BUSY / "database is locked" rather than a lost write. The store
// must make that surface as WAITING, not a spurious failure — the shared
// ConcurrentReplayStorm / ConcurrentSingleWinner cases assert zero spurious errors,
// so a busy error must never leak where the contract promises an application
// outcome. busy_timeout is set on the connection best-effort in Repositories, and
// the bounded retry loop below is the real defense: because Apply is idempotent by
// MutationID, re-running the whole transaction resolves to a deterministic terminal
// outcome (replay / stale / invariant_blocked). This mirrors the jobs turso store's
// helper of the same shape.
const (
	busyMaxRetries = 200
	busyBaseDelay  = 2 * time.Millisecond
	busyMaxDelay   = 200 * time.Millisecond
)

// isBusy reports whether err is a transient SQLite/libSQL contention error that a
// retry can clear. libsql surfaces SQLite's textual messages, and MapError passes
// them through unchanged (they match none of its constraint sentinels), so the
// substring test survives the connector's error mapping.
func isBusy(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "SQLITE_BUSY") ||
		strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "database table is locked") ||
		strings.Contains(msg, "Server returned status 503")
}

// retryBusy runs fn, retrying on a transient busy/locked error with a bounded,
// backing-off wait so contention surfaces as waiting rather than a failure. It
// stops on the first non-busy result (including success and real errors), on
// exhausting the retry budget, or on ctx cancellation.
func retryBusy(ctx context.Context, fn func() error) error {
	delay := busyBaseDelay
	for attempt := 0; ; attempt++ {
		err := fn()
		if !isBusy(err) || attempt >= busyMaxRetries {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		if delay *= 2; delay > busyMaxDelay {
			delay = busyMaxDelay
		}
	}
}
