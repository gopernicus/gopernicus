package turso

import (
	"context"
	"strings"
	"time"
)

// BEGIN IMMEDIATE contention is retried before any application callback runs.
// Transport errors are not proof of an aborted transaction and never retry.
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
		strings.Contains(msg, "database table is locked")
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
