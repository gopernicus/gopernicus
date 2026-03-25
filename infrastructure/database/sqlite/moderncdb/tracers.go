package moderncdb

import (
	"context"
	"strings"

	"github.com/gopernicus/gopernicus/telemetry"
)

// OTelTracer creates OpenTelemetry spans for SQLite queries.
// Each query becomes a child span of the current trace context.
//
// Usage:
//
//	tracer := moderncdb.NewOTelTracer(provider.Tracer())
//	db, err := moderncdb.New(cfg, moderncdb.WithTracer(tracer))
type OTelTracer struct {
	tracer telemetry.Tracer
}

// NewOTelTracer creates a new SQLite query tracer for OpenTelemetry.
func NewOTelTracer(tracer telemetry.Tracer) *OTelTracer {
	return &OTelTracer{tracer: tracer}
}

// spanHandle wraps a telemetry.Span with convenience methods for DB tracing.
type spanHandle struct {
	span telemetry.Span
}

func (s spanHandle) End() {
	s.span.End()
}

func (s spanHandle) RecordError(err error) {
	telemetry.RecordError(s.span, err)
}

func (s spanHandle) SetRowsAffected(n int64) {
	telemetry.AddInt64Attribute(s.span, "db.rows_affected", n)
}

// startSpan creates a new client span for a SQLite operation.
func (t *OTelTracer) startSpan(ctx context.Context, operation, query string) (context.Context, spanHandle) {
	ctx, span := telemetry.StartClientSpan(ctx, t.tracer, "sqlite."+operation)

	telemetry.AddAttribute(span, "db.system", "sqlite")
	telemetry.AddAttribute(span, "db.operation", operation)
	telemetry.AddAttribute(span, "db.statement", query)

	return ctx, spanHandle{span: span}
}

// extractSQLOperation extracts the SQL operation (SELECT, INSERT, UPDATE, DELETE, etc.)
// from the first word of the SQL statement.
func extractSQLOperation(sql string) string {
	sql = strings.TrimSpace(sql)
	if sql == "" {
		return "QUERY"
	}

	// Handle CTEs: WITH ... SELECT/INSERT/UPDATE/DELETE
	upper := strings.ToUpper(sql)
	if strings.HasPrefix(upper, "WITH") {
		for _, op := range []string{"SELECT", "INSERT", "UPDATE", "DELETE"} {
			if strings.Contains(upper, op) {
				return op
			}
		}
		return "WITH"
	}

	// Handle PRAGMA statements (common in SQLite)
	if strings.HasPrefix(upper, "PRAGMA") {
		return "PRAGMA"
	}

	// Handle CREATE/DROP/ALTER statements
	if strings.HasPrefix(upper, "CREATE") {
		return "CREATE"
	}
	if strings.HasPrefix(upper, "DROP") {
		return "DROP"
	}
	if strings.HasPrefix(upper, "ALTER") {
		return "ALTER"
	}

	// Handle BEGIN/COMMIT/ROLLBACK
	if strings.HasPrefix(upper, "BEGIN") {
		return "BEGIN"
	}
	if strings.HasPrefix(upper, "COMMIT") {
		return "COMMIT"
	}
	if strings.HasPrefix(upper, "ROLLBACK") {
		return "ROLLBACK"
	}

	// Extract first word for standard operations
	firstSpace := strings.IndexAny(sql, " \t\n")
	if firstSpace == -1 {
		return strings.ToUpper(sql)
	}
	return strings.ToUpper(sql[:firstSpace])
}
