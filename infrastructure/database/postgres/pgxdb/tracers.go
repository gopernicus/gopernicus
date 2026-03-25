package pgxdb

import (
	"context"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/telemetry"
)

// MultiQueryTracer composes multiple pgx.QueryTracer implementations so they
// all fire for every query. Useful for combining logging + OTel tracing.
//
// See: https://github.com/jackc/pgx/discussions/1677#discussioncomment-8815982
type MultiQueryTracer struct {
	Tracers []pgx.QueryTracer
}

// NewMultiQueryTracer creates a tracer that delegates to all provided tracers.
func NewMultiQueryTracer(tracers ...pgx.QueryTracer) *MultiQueryTracer {
	return &MultiQueryTracer{Tracers: tracers}
}

func (m *MultiQueryTracer) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	for _, t := range m.Tracers {
		ctx = t.TraceQueryStart(ctx, conn, data)
	}
	return ctx
}

func (m *MultiQueryTracer) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryEndData) {
	for _, t := range m.Tracers {
		t.TraceQueryEnd(ctx, conn, data)
	}
}

// LoggingQueryTracer logs every query start and end via slog.
//
// See: https://github.com/jackc/pgx/issues/1061#issuecomment-1186250809
type LoggingQueryTracer struct {
	logger *slog.Logger
}

// NewLoggingQueryTracer creates a new slog-based query tracer.
func NewLoggingQueryTracer(logger *slog.Logger) *LoggingQueryTracer {
	return &LoggingQueryTracer{logger: logger}
}

func (l *LoggingQueryTracer) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	l.logger.Info("query start",
		slog.String("sql", PrettyPrintSQL(data.SQL)),
		slog.Any("args", data.Args),
	)
	return ctx
}

func (l *LoggingQueryTracer) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryEndData) {
	if data.Err != nil {
		l.logger.Error("query end",
			slog.String("error", data.Err.Error()),
			slog.String("command_tag", data.CommandTag.String()),
		)
		return
	}

	l.logger.Info("query end",
		slog.String("command_tag", data.CommandTag.String()),
	)
}

// OTelQueryTracer creates OpenTelemetry spans for database queries.
// Each query becomes a child span of the current trace context.
type OTelQueryTracer struct {
	tracer telemetry.Tracer
}

// NewOTelQueryTracer creates a new pgx query tracer for OpenTelemetry.
func NewOTelQueryTracer(tracer telemetry.Tracer) *OTelQueryTracer {
	return &OTelQueryTracer{tracer: tracer}
}

func (t *OTelQueryTracer) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if t.tracer == nil {
		return ctx
	}

	operation := extractSQLOperation(data.SQL)
	ctx, span := telemetry.StartClientSpan(ctx, t.tracer, "pgx."+operation)

	telemetry.AddAttribute(span, "db.system", "postgresql")
	telemetry.AddAttribute(span, "db.operation", operation)
	telemetry.AddAttribute(span, "db.statement", PrettyPrintSQL(data.SQL))

	return ctx
}

func (t *OTelQueryTracer) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryEndData) {
	span := telemetry.SpanFromContext(ctx)
	if span == nil || !span.IsRecording() {
		return
	}

	if rowsAffected := data.CommandTag.RowsAffected(); rowsAffected > 0 {
		telemetry.AddInt64Attribute(span, "db.rows_affected", rowsAffected)
	}

	if data.Err != nil {
		telemetry.RecordError(span, data.Err)
	}

	span.End()
}

// extractSQLOperation returns the SQL operation (SELECT, INSERT, UPDATE, DELETE, etc.)
// from the first word of the statement. Handles WITH (CTE) queries.
func extractSQLOperation(sql string) string {
	sql = strings.TrimSpace(sql)
	if sql == "" {
		return "QUERY"
	}

	upper := strings.ToUpper(sql)
	if strings.HasPrefix(upper, "WITH") {
		for _, op := range []string{"SELECT", "INSERT", "UPDATE", "DELETE"} {
			if strings.Contains(upper, op) {
				return op
			}
		}
		return "WITH"
	}

	firstSpace := strings.IndexAny(sql, " \t\n")
	if firstSpace == -1 {
		return strings.ToUpper(sql)
	}
	return strings.ToUpper(sql[:firstSpace])
}
