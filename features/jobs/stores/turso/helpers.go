package turso

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"strings"
	"time"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
)

// MigrationsFS holds the embedded schema (app-owned). cmd wires it into the
// connector's RunMigrations runner.
//
//go:embed migrations/*.sql
var MigrationsFS embed.FS

// MigrationsDir is the directory within MigrationsFS holding the .sql files.
const MigrationsDir = "migrations"

// busy-retry discipline: SQLite has no row locks, so under concurrent writers
// (and Turso remote's serialized writes) contention surfaces as SQLITE_BUSY /
// "database is locked" rather than a lost update. The store must make that
// surface as WAITING, not a failed operation — the conformance suite's
// ConcurrentClaim case asserts zero spurious errors. busy_timeout is set on the
// connection at construction (best effort; libsql remote may ignore it), and the
// bounded retry loop below is the real defense.
const (
	busyMaxRetries = 200
	busyBaseDelay  = 2 * time.Millisecond
	busyMaxDelay   = 200 * time.Millisecond
)

// scanner abstracts *sql.Row and *sql.Rows for shared scan helpers.
type scanner = tursodb.Scanner

// payloadValue returns a non-empty JSON text for storage: the raw payload, or
// "{}" when it is empty (the column is NOT NULL).
func payloadValue(p []byte) string {
	if len(p) == 0 {
		return "{}"
	}
	return string(p)
}

// newID returns a random, collision-free identifier with the given prefix.
func newID(prefix string) string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return prefix + "_" + hex.EncodeToString(b[:])
}

// isBusy reports whether err is a transient SQLite/libSQL contention error that
// a retry can clear. libsql surfaces SQLite's textual messages, and MapError
// passes them through unchanged (they match none of its constraint sentinels).
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
