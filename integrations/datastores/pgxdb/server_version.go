package pgxdb

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// MinServerVersionNum is the lowest server_version_num RunMigrations accepts:
// PostgreSQL 18, the first release with a built-in uuidv7().
const MinServerVersionNum = 180000

// serverVersionQuery reads both forms of the version in one round-trip on the
// connection it is given.
const serverVersionQuery = "SELECT current_setting('server_version_num')::int, current_setting('server_version')"

// ErrServerVersion reports a server below MinServerVersionNum. RunMigrations
// returns it before touching the ledger; Open only logs the advisory.
var ErrServerVersion = errors.New("pgxdb: PostgreSQL 18 or later is required")

// readServerVersion probes q for server_version_num and server_version.
func readServerVersion(ctx context.Context, q Querier) (int, string, error) {
	var (
		num     int
		version string
	)
	if err := q.QueryRow(ctx, serverVersionQuery).Scan(&num, &version); err != nil {
		return 0, "", err
	}
	return num, version, nil
}

// checkServerVersion refuses a server below the floor with the operator text:
// what the server reports, why the floor exists, and the two ways out. It names
// no host, user, database or DSN.
func checkServerVersion(num int, version string) error {
	if num >= MinServerVersionNum {
		return nil
	}
	return fmt.Errorf("%w: server reports %q (server_version_num %d < %d); the canonical pgx migrations use uuidv7() (PostgreSQL 18). "+
		"Upgrade the server, or hold pgxdb at v0.16.x and the pgx stores at their previous tags (RELEASING \"PostgreSQL 18 floor\")",
		ErrServerVersion, version, num, MinServerVersionNum)
}

// adviseServerVersion logs the Open-time version advisory: one WARN when the
// server is below the floor, or one WARN when the probe itself fails. It never
// fails Open — a PG-compatible backend or a statement timeout must not stop a
// runtime that never migrates. The error text passes through redactText, so a
// driver message that echoes the connection string or password stays masked.
func adviseServerVersion(ctx context.Context, q Querier, logger *slog.Logger, dsn, password string) {
	if logger == nil {
		logger = slog.Default()
	}
	num, version, err := readServerVersion(ctx, q)
	if err != nil {
		logger.WarnContext(ctx, "pgxdb: could not read the server version; RunMigrations requires PostgreSQL 18",
			slog.Int("required", MinServerVersionNum), slog.String("error", redactText(err.Error(), dsn, password)))
		return
	}
	if num < MinServerVersionNum {
		logger.WarnContext(ctx, "pgxdb: server below the PostgreSQL 18 floor; RunMigrations will refuse",
			slog.Int("server_version_num", num), slog.Int("required", MinServerVersionNum), slog.String("server_version", version))
	}
}
