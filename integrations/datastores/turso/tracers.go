package turso

import "log/slog"

// loggingQueryTracer logs every query, with its arguments, via slog. database/sql
// exposes no driver-level tracer hook (unlike pgx's ConnConfig.Tracer), so the DB
// and Tx wrappers call it directly from their Exec/Query/QueryRow methods. It is
// wired only when a host opts in through Config.LogQueries.
//
// WARNING: it logs SQL query arguments verbatim, and those can carry secrets or
// PII belonging to app domains this connector knows nothing about. This is
// dev-only tooling: wire it only when a host has explicitly set Config.LogQueries,
// never in production logging.
type loggingQueryTracer struct {
	logger *slog.Logger
}

// newLoggingQueryTracer returns a query tracer that logs via logger. A nil logger
// falls back to slog.Default().
func newLoggingQueryTracer(logger *slog.Logger) *loggingQueryTracer {
	if logger == nil {
		logger = slog.Default()
	}
	return &loggingQueryTracer{logger: logger}
}

// traceQuery logs a query about to run against the connection or a transaction,
// with its bind arguments verbatim.
func (l *loggingQueryTracer) traceQuery(query string, args []any) {
	l.logger.Info("query",
		slog.String("sql", query),
		slog.Any("args", args),
	)
}
