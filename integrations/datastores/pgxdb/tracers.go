package pgxdb

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	jackpgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// LoggingQueryTracerConfig configures NewLoggingQueryTracerWithConfig.
type LoggingQueryTracerConfig struct {
	// Logger receives the query lines; nil falls back to slog.Default().
	Logger *slog.Logger

	// LogArgs adds each query's bind arguments, and a failed query's error
	// text, verbatim. Those carry secrets and PII: development only.
	LogArgs bool
}

// LoggingQueryTracer logs every query start and end via slog, with the query's
// context so a context-aware handler adds request_id. The start line carries
// sql, arg_count and arg_types (each argument's Go type), never a value; a
// failed query's end line carries the sqlstate and constraint of a
// *pgconn.PgError, or error_type otherwise, never the error text, because pgx
// encode errors and some server messages repeat the argument.
//
// WARNING: with LogArgs the tracer also logs bind arguments and error text
// verbatim, and those can carry secrets or PII belonging to app domains this
// connector knows nothing about. Either way it is dev-only tooling: wire it
// only when a host has explicitly opted in by setting Config.LogQueries or
// Config.Tracer; a production Config.RuntimeMode refuses both.
type LoggingQueryTracer struct {
	logger  *slog.Logger
	logArgs bool
}

// NewLoggingQueryTracer returns a query tracer that logs argument counts and
// types, never values, via logger. A nil logger falls back to slog.Default().
func NewLoggingQueryTracer(logger *slog.Logger) *LoggingQueryTracer {
	return NewLoggingQueryTracerWithConfig(LoggingQueryTracerConfig{Logger: logger})
}

// NewLoggingQueryTracerWithConfig returns a query tracer configured by cfg.
func NewLoggingQueryTracerWithConfig(cfg LoggingQueryTracerConfig) *LoggingQueryTracer {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &LoggingQueryTracer{logger: logger, logArgs: cfg.LogArgs}
}

func (l *LoggingQueryTracer) TraceQueryStart(ctx context.Context, conn *jackpgx.Conn, data jackpgx.TraceQueryStartData) context.Context {
	attrs := []slog.Attr{
		slog.String("sql", PrettyPrintSQL(data.SQL)),
		slog.Int("arg_count", len(data.Args)),
		slog.Any("arg_types", argTypes(data.Args)),
	}
	if l.logArgs {
		attrs = append(attrs, slog.Any("args", data.Args))
	}
	l.logger.LogAttrs(ctx, slog.LevelInfo, "query start", attrs...)
	return ctx
}

func (l *LoggingQueryTracer) TraceQueryEnd(ctx context.Context, conn *jackpgx.Conn, data jackpgx.TraceQueryEndData) {
	attrs := []slog.Attr{slog.String("command_tag", data.CommandTag.String())}
	if data.Err == nil {
		l.logger.LogAttrs(ctx, slog.LevelInfo, "query end", attrs...)
		return
	}

	var pgErr *pgconn.PgError
	if errors.As(data.Err, &pgErr) {
		attrs = append(attrs, slog.String("sqlstate", pgErr.Code), slog.String("constraint", pgErr.ConstraintName))
	} else {
		attrs = append(attrs, slog.String("error_type", fmt.Sprintf("%T", data.Err)))
	}
	if l.logArgs {
		attrs = append(attrs, slog.String("error", data.Err.Error()))
	}
	l.logger.LogAttrs(ctx, slog.LevelError, "query end", attrs...)
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
