// Package turso is the datastore connector for Turso / libSQL: it bridges the
// libsql driver to a small database/sql wrapper (connection, tx, migrations).
// It is a reusable connector — it owns "how to talk to Turso," not any app's
// queries. App-specific repositories live in the app's providers/ and consume
// this package's *DB.
//
// It is its own module (github.com/gopernicus/gopernicus/integrations/datastores/turso), depending
// only on sdk (for the errs sentinels MapError targets) and libsql. Local
// "file:" databases additionally need a registered SQLite driver, which the
// opt-in sibling package localfile provides (see Open).
package turso

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/sdk"

	_ "github.com/tursodatabase/libsql-client-go/libsql" // registers the "libsql" driver
)

// Driver is the registered database/sql driver name for libSQL.
const Driver = "libsql"

const (
	// localFileScheme marks a local SQLite database ("file:" URL). The pinned
	// libsql driver has no SQLite engine of its own: it hands such a URL to a
	// database/sql driver registered as "sqlite" or "sqlite3", which the
	// localfile package registers.
	localFileScheme = "file"

	// localDriverPackage is the import a host adds for local databases.
	localDriverPackage = "github.com/gopernicus/gopernicus/integrations/datastores/turso/localfile"

	// defaultBusyTimeout backs a zero Config.BusyTimeout on a local file. A
	// zero SQLite busy timeout produces SQLITE_BUSY under ordinary concurrent
	// use, so the connector never applies one.
	defaultBusyTimeout = 5 * time.Second

	pragmaParam = "_pragma"
)

// Config holds the Turso connection settings. The env tags mirror the pgxdb
// connector's DB_* names; a host reads them with
// environment.ParseEnvTags(namespace, &cfg), and a non-empty namespace prefixes
// every key (namespace "AUTH" reads AUTH_DB_URL). The connector itself knows
// no namespace.
type Config struct {
	// URL is required: "libsql://", "https://", "wss://" for a hosted database,
	// or "file:<path>" for a local SQLite database (see Open).
	URL string `env:"DB_URL"`

	// AuthToken overrides any authToken, auth_token or jwt credential in URL.
	AuthToken       string        `env:"DB_AUTH_TOKEN"`
	MaxOpenConns    int           `env:"DB_MAX_CONNS"`
	MaxIdleConns    int           `env:"DB_MAX_IDLE_CONNS"`
	ConnMaxLifetime time.Duration `env:"DB_MAX_CONN_LIFETIME"`
	ConnectTimeout  time.Duration `env:"DB_CONNECT_TIMEOUT"`

	// BusyTimeout is the SQLite busy timeout applied to every pooled
	// connection of a local "file:" database; zero means defaultBusyTimeout.
	// A hosted database ignores it: Turso governs its own busy behavior.
	BusyTimeout time.Duration `env:"DB_BUSY_TIMEOUT"`

	// LogQueries installs a query logger that logs every Exec/Query/QueryRow —
	// on both the DB connection and its transactions — via Logger. It logs SQL
	// args verbatim, so this is dev-only tooling: leave it false in production.
	LogQueries bool `env:"DB_LOG_QUERIES" default:"false"`

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

// Open connects to a Turso / libSQL database and verifies it with a ping.
// The context bounds startup only; the caller owns the returned DB's lifetime.
//
// A "file:" URL opens a local SQLite database through a registered "sqlite" or
// "sqlite3" driver — import the localfile package for the pure-Go one. Open
// then applies the local file profile: the parent directory is created if
// missing, and unless the URL already names a _pragma (an explicit opt-out),
// three are appended so every pooled connection gets them —
// busy_timeout(BusyTimeout), foreign_keys(1) and journal_mode(WAL). A single
// PRAGMA on one connection would not do: the driver applies "_pragma=" DSN
// parameters on each new connection, which is what makes the setting hold on
// every connection of the pool. Hosted URLs are untouched.
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
	if isLocalFile(cfg.URL) {
		if dsn, err = localFileDSN(dsn, cfg.BusyTimeout); err != nil {
			return nil, err
		}
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
			return nil, fmt.Errorf("verifying libsql database: %w", localDriverHint(cfg.URL, err))
		}
		return wrapped, nil
	}

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("pinging libsql database: %w", localDriverHint(cfg.URL, err))
	}
	return wrapped, nil
}

// isLocalFile reports whether rawURL names a local SQLite database.
func isLocalFile(rawURL string) bool {
	return strings.HasPrefix(rawURL, localFileScheme+":")
}

// localFileDSN applies the local file profile to an already-normalized "file:"
// DSN: the parent directory is created if missing, and the three pragmas are
// appended unless the URL names a _pragma of its own. It never runs for a
// hosted URL and is kept out of dsn() so Redacted stays free of side effects.
func localFileDSN(dsn string, busyTimeout time.Duration) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("turso: invalid database URL: %w", sdk.ErrInvalidInput)
	}
	path := u.Opaque
	if path == "" {
		path = u.Path
	}
	if path != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", fmt.Errorf("turso: creating the database directory: %w", err)
		}
	}
	if busyTimeout <= 0 {
		busyTimeout = defaultBusyTimeout
	}
	q := u.Query()
	if !q.Has(pragmaParam) {
		q.Set(pragmaParam, fmt.Sprintf("busy_timeout(%d)", busyTimeout.Milliseconds()))
		q.Add(pragmaParam, "foreign_keys(1)")
		q.Add(pragmaParam, "journal_mode(WAL)")
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

// localDriverHint names the missing import when a local database cannot be
// opened because no SQLite driver is registered; every other error passes
// through unchanged.
func localDriverHint(rawURL string, err error) error {
	if isLocalFile(rawURL) && strings.Contains(err.Error(), "no sqlite driver present") {
		return fmt.Errorf("%w (a file: URL needs a registered sqlite driver: import %s)", err, localDriverPackage)
	}
	return err
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
