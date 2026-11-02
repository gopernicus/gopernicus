package pgxdb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"

	jackpgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/gopernicus/gopernicus/sdk"
)

// unavailableSQLStates are server responses that mean the database cannot
// serve right now, not that the statement is wrong: class 08 (connection
// exception) is matched by prefix; these are the rest.
var unavailableSQLStates = map[string]bool{
	"57P01": true, // admin_shutdown
	"57P02": true, // crash_shutdown
	"57P03": true, // cannot_connect_now
	"53300": true, // too_many_connections
}

// classifyUnavailable adds sdk.ErrUnavailable to a connection-level failure,
// keeping the original error in the chain, so a caller (authorization's
// Require, a health check) can answer 503 for an outage instead of 500. It
// returns err unchanged otherwise. Caller cancellation and deadlines are never
// an outage, and neither is a statement_timeout (57014) or a constraint
// violation; pgx.ErrNoRows is untouched.
func classifyUnavailable(err error) error {
	if err == nil || errors.Is(err, sdk.ErrUnavailable) {
		return err
	}
	// Checked first: pgx wraps an already-done context in an error that
	// reports SafeToRetry, which must not read as an outage.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, jackpgx.ErrNoRows) {
		return err
	}
	if isUnavailable(err) {
		return fmt.Errorf("%w: %w", err, sdk.ErrUnavailable)
	}
	return err
}

func isUnavailable(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return strings.HasPrefix(pgErr.Code, "08") || unavailableSQLStates[pgErr.Code]
	}
	var connectErr *pgconn.ConnectError
	if errors.As(err, &connectErr) {
		return true
	}
	if pgconn.SafeToRetry(err) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return true
	}
	// pgxpool reports a closed pool with a plain error.
	return strings.Contains(err.Error(), "closed pool")
}

// classifiedRow classifies a Scan error as classifyUnavailable does, leaving
// every other error (pgx.ErrNoRows included) exactly as pgx returned it.
type classifiedRow struct{ row jackpgx.Row }

func (r classifiedRow) Scan(dest ...any) error { return classifyUnavailable(r.row.Scan(dest...)) }
