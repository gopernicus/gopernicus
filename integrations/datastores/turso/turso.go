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
	"net/url"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/sdk"

	_ "github.com/tursodatabase/libsql-client-go/libsql" // registers the "libsql" driver
)

// Driver is the registered database/sql driver name for libSQL.
const Driver = "libsql"

// Config holds the Turso connection settings.
type Config struct {
	URL string

	// AuthToken overrides any authToken, auth_token or jwt credential in URL.
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
	// lazy ping exactly (today's behavior): boot-time table probes in the pocket
	// stores remain the validator.
	//
	// This governs ONLY the boot connectivity check. No statement is ever
	// auto-retried by the connector — statement retry is store-owned, explicit,
	// and per-call.
	Retry RetryPolicy
}

// dsn validates the URL and sets the separately configured credential.
func (cfg Config) dsn() (string, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil {
		return "", fmt.Errorf("turso: invalid database URL: %w", sdk.ErrInvalidInput)
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || u.Fragment != "" {
		return "", fmt.Errorf("turso: malformed database query or fragment: %w", sdk.ErrInvalidInput)
	}
	if cfg.AuthToken != "" {
		for _, name := range credentialParams {
			q.Del(name)
		}
		q.Set(authTokenParam, cfg.AuthToken)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// Redacted returns the connection target with the userinfo password and all
// supported credential query parameters masked, safe for logs and errors.
func (cfg Config) Redacted() string {
	dsn, err := cfg.dsn()
	if err != nil {
		return redactedDSN
	}
	return RedactDSN(dsn)
}

// Open connects to a remote Turso / libSQL database and verifies it with a ping.
// The context bounds startup only; the caller owns the returned DB's lifetime.
func Open(ctx context.Context, cfg Config) (*DB, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if cfg.URL == "" {
		return nil, fmt.Errorf("turso: empty database URL")
	}
	if cfg.ConnectTimeout == 0 {
		cfg.ConnectTimeout = 10 * time.Second
	}

	dsn, err := cfg.dsn()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	db, err := sql.Open(Driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("opening libsql database: %w", err)
	}

	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)

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
