// Package turso is the datastore connector for Turso / libSQL: it bridges the
// libsql driver to a small database/sql wrapper (connection, tx, migrations).
// It is a reusable connector — it owns "how to talk to Turso," not any app's
// queries. App-specific repositories live in the app's providers/ and consume
// this package's *DB.
//
// It is its own module (github.com/gopernicus/gopernicus/integrations/datastores/turso), depending
// only on sdk (for the errs sentinels MapError targets) and libsql.
package turso

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/sdk"

	_ "github.com/tursodatabase/libsql-client-go/libsql" // registers the "libsql" driver
)

// Driver is the registered database/sql driver name for libSQL.
const Driver = "libsql"

// Config holds the Turso connection settings.
type Config struct {
	URL             string
	AuthToken       string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnectTimeout  time.Duration

	// LogQueries installs a query logger that logs every Exec/Query/QueryRow —
	// on both the DB connection and its transactions — via Logger. It logs SQL
	// args verbatim, so this is dev-only tooling: leave it false in production.
	LogQueries bool

	// Logger is used only when LogQueries is true. If nil, slog.Default() is
	// used.
	Logger *slog.Logger

	// Retry, when its Attempts is > 1, makes Open perform EAGER boot validation:
	// it runs a real round-trip (StatusCheck: Ping + SELECT 1) retried under a
	// full-jitter exponential backoff, targeting the orchestration race where the
	// database is not yet reachable. Opting into Retry therefore opts into eager
	// boot validation — the remote libSQL driver's Ping is lazy, so retrying a
	// plain ping that cannot fail would be vacuous. The zero value keeps Open's
	// lazy ping exactly (today's behavior): boot-time table probes in the feature
	// stores remain the validator.
	//
	// This governs ONLY the boot connectivity check. No statement is ever
	// auto-retried by the connector — statement retry is store-owned, explicit,
	// and per-call.
	Retry RetryPolicy
}

// dsn builds the libSQL connection string, appending the auth token as the
// authToken query parameter when Config.AuthToken is set.
func (cfg Config) dsn() string {
	dsn := cfg.URL
	if cfg.AuthToken != "" {
		sep := "?"
		if strings.Contains(dsn, "?") {
			sep = "&"
		}
		dsn += sep + authTokenParam + "=" + cfg.AuthToken
	}
	return dsn
}

// Redacted returns the connection target with the userinfo password and the
// authToken query parameter masked, safe to place in logs and error messages.
func (cfg Config) Redacted() string {
	return RedactDSN(cfg.dsn())
}

// Open connects to a remote Turso / libSQL database and verifies it with a ping.
func Open(cfg Config) (*DB, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("turso: empty database URL")
	}
	if cfg.ConnectTimeout == 0 {
		cfg.ConnectTimeout = 10 * time.Second
	}

	db, err := sql.Open(Driver, cfg.dsn())
	if err != nil {
		return nil, fmt.Errorf("opening libsql database: %w", err)
	}

	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)

	ctx, cancel := context.WithTimeout(context.Background(), cfg.ConnectTimeout)
	defer cancel()

	wrapped := &DB{db: db}
	if cfg.LogQueries {
		wrapped.tracer = newLoggingQueryTracer(cfg.Logger)
	}

	// Opting into Retry opts into eager boot validation: a real round-trip
	// (StatusCheck: Ping + SELECT 1) retried per the policy, resolving the lazy
	// ping's inability to detect a dead DB at boot. The zero value keeps the lazy
	// ping exactly.
	if cfg.Retry.Attempts > 1 {
		if err := retry(ctx, cfg.Retry, func(ctx context.Context) error {
			return StatusCheck(ctx, wrapped)
		}); err != nil {
			db.Close()
			return nil, fmt.Errorf("verifying libsql database: %w", err)
		}
		return wrapped, nil
	}

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("pinging libsql database: %w", err)
	}
	return wrapped, nil
}

// MapError converts a libSQL / SQLite driver error into an sdk/errs sentinel.
// Detection is by substring because the libSQL client surfaces SQLite's textual
// messages. Unrecognized errors pass through unchanged. Callers map both query
// errors and Scan errors (sql.ErrNoRows → ErrNotFound) through this.
func MapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return sdk.ErrNotFound
	}

	msg := err.Error()
	switch {
	case strings.Contains(msg, "UNIQUE constraint failed"):
		return sdk.ErrAlreadyExists
	case strings.Contains(msg, "FOREIGN KEY constraint failed"):
		return sdk.ErrInvalidReference
	case strings.Contains(msg, "CHECK constraint failed"):
		return sdk.ErrInvalidInput
	case strings.Contains(msg, "NOT NULL constraint failed"):
		return sdk.ErrInvalidInput
	}
	return err
}
