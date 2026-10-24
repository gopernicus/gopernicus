package turso

import (
	"context"
	"fmt"
	"log/slog"
)

// loggingQueryTracer logs every query via slog, with the query's context so a
// context-aware handler adds request_id. database/sql exposes no driver-level
// tracer hook (unlike pgx's ConnConfig.Tracer), so the DB and Tx wrappers call
// it directly from their Exec/Query/QueryRow methods. It is wired only when a
// host opts in through Config.LogQueries. Each line carries sql, arg_count and
// arg_types (each argument's Go type), never a value.
//
// WARNING: with Config.LogQueryArgs it also logs SQL query arguments verbatim,
// and those can carry secrets or PII belonging to app domains this connector
// knows nothing about. Either way it is dev-only tooling; a production
// Config.RuntimeMode refuses it.
type loggingQueryTracer struct {
	logger  *slog.Logger
	logArgs bool
}

// newLoggingQueryTracer returns a query tracer that logs via cfg.Logger, with
// arguments only under cfg.LogQueryArgs. A nil logger falls back to
// slog.Default().
func newLoggingQueryTracer(cfg Config) *loggingQueryTracer {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &loggingQueryTracer{logger: logger, logArgs: cfg.LogQueryArgs}
}

// traceQuery logs a query about to run against the connection or a
// transaction.
func (l *loggingQueryTracer) traceQuery(ctx context.Context, query string, args []any) {
	attrs := []slog.Attr{
		slog.String("sql", query),
		slog.Int("arg_count", len(args)),
		slog.Any("arg_types", argTypes(args)),
	}
	if l.logArgs {
		attrs = append(attrs, slog.Any("args", args))
	}
	l.logger.LogAttrs(ctx, slog.LevelInfo, "query", attrs...)
}

// argTypes lists each argument's Go type, the value-free shape of a query's
// arguments.
func argTypes(args []any) []string {
	types := make([]string, len(args))
	for i, arg := range args {
		types[i] = fmt.Sprintf("%T", arg)
	}
	return types
}
