package pgxdb

import (
	"context"
	"log/slog"

	jackpgx "github.com/jackc/pgx/v5"
)

// LoggingQueryTracer logs every query start and end via slog.
//
// WARNING: TraceQueryStart logs SQL query arguments verbatim, and those can
// carry secrets or PII belonging to app domains this connector knows nothing
// about. This tracer is dev-only tooling: wire it only when a host has
// explicitly opted in by setting Config.LogQueries or Config.Tracer, never in
// production logging.
type LoggingQueryTracer struct {
	logger *slog.Logger
}

// NewLoggingQueryTracer returns a query tracer that logs via logger. A nil
// logger falls back to slog.Default().
func NewLoggingQueryTracer(logger *slog.Logger) *LoggingQueryTracer {
	if logger == nil {
		logger = slog.Default()
	}
	return &LoggingQueryTracer{logger: logger}
}

func (l *LoggingQueryTracer) TraceQueryStart(ctx context.Context, conn *jackpgx.Conn, data jackpgx.TraceQueryStartData) context.Context {
	l.logger.Info("query start",
		slog.String("sql", PrettyPrintSQL(data.SQL)),
		slog.Any("args", data.Args),
	)
	return ctx
}

func (l *LoggingQueryTracer) TraceQueryEnd(ctx context.Context, conn *jackpgx.Conn, data jackpgx.TraceQueryEndData) {
	if data.Err != nil {
		l.logger.Error("query end",
			slog.String("error", data.Err.Error()),
			slog.String("command_tag", data.CommandTag.String()),
		)
		return
	}

	l.logger.Info("query end", slog.String("command_tag", data.CommandTag.String()))
}

// MultiQueryTracer composes multiple jackpgx.QueryTracer implementations so all of
// them fire for every query — e.g. folding LoggingQueryTracer and a future
// OpenTelemetry tracer into the single Config.Tracer field this connector
// forwards to pgxpool.ConnConfig.Tracer (pgx only accepts one tracer per
// connection).
type MultiQueryTracer struct {
	Tracers []jackpgx.QueryTracer
}

// NewMultiQueryTracer returns a tracer that delegates to all of tracers, in
// order.
func NewMultiQueryTracer(tracers ...jackpgx.QueryTracer) *MultiQueryTracer {
	return &MultiQueryTracer{Tracers: tracers}
}

func (m *MultiQueryTracer) TraceQueryStart(ctx context.Context, conn *jackpgx.Conn, data jackpgx.TraceQueryStartData) context.Context {
	for _, t := range m.Tracers {
		ctx = t.TraceQueryStart(ctx, conn, data)
	}
	return ctx
}

func (m *MultiQueryTracer) TraceQueryEnd(ctx context.Context, conn *jackpgx.Conn, data jackpgx.TraceQueryEndData) {
	for _, t := range m.Tracers {
		t.TraceQueryEnd(ctx, conn, data)
	}
}
