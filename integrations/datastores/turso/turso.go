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
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/sdk/errs"

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
}

// Open connects to a remote Turso / libSQL database and verifies it with a ping.
func Open(cfg Config) (*DB, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("turso: empty database URL")
	}
	if cfg.ConnectTimeout == 0 {
		cfg.ConnectTimeout = 10 * time.Second
	}

	dsn := cfg.URL
	if cfg.AuthToken != "" {
		sep := "?"
		if strings.Contains(dsn, "?") {
			sep = "&"
		}
		dsn += sep + "authToken=" + cfg.AuthToken
	}

	db, err := sql.Open(Driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("opening libsql database: %w", err)
	}

	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)

	ctx, cancel := context.WithTimeout(context.Background(), cfg.ConnectTimeout)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("pinging libsql database: %w", err)
	}

	return &DB{db: db}, nil
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
		return errs.ErrNotFound
	}

	msg := err.Error()
	switch {
	case strings.Contains(msg, "UNIQUE constraint failed"):
		return errs.ErrAlreadyExists
	case strings.Contains(msg, "FOREIGN KEY constraint failed"):
		return errs.ErrInvalidReference
	case strings.Contains(msg, "CHECK constraint failed"):
		return errs.ErrInvalidInput
	case strings.Contains(msg, "NOT NULL constraint failed"):
		return errs.ErrInvalidInput
	}
	return err
}
