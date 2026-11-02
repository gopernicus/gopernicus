package pgxdb

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	jackpgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/tracing"
)

const (
	// maxQueryTextBytes caps db.query.text.
	maxQueryTextBytes = 2048
	// spanNameFallback names a statement whose leading keyword is not in
	// spanKeywords.
	spanNameFallback = "db.query"
	// spanNameBatch names one pgx batch.
	spanNameBatch = "db.batch"
)

// spanKeywords is the bounded set of span names a statement can take: its
// leading SQL keyword. Anything else is spanNameFallback, so a span name never
// carries SQL text.
var spanKeywords = map[string]bool{
	"SELECT": true, "INSERT": true, "UPDATE": true, "DELETE": true, "MERGE": true, "WITH": true,
	"VALUES": true, "TABLE": true, "CALL": true, "DO": true, "COPY": true,
	"BEGIN": true, "START": true, "COMMIT": true, "ROLLBACK": true, "SAVEPOINT": true, "RELEASE": true,
	"SET": true, "SHOW": true, "RESET": true, "LOCK": true, "DISCARD": true,
	"CREATE": true, "ALTER": true, "DROP": true, "TRUNCATE": true, "COMMENT": true, "GRANT": true, "REVOKE": true,
	"ANALYZE": true, "VACUUM": true, "REINDEX": true, "CLUSTER": true, "REFRESH": true, "EXPLAIN": true,
	"LISTEN": true, "NOTIFY": true, "UNLISTEN": true,
	"PREPARE": true, "EXECUTE": true, "DEALLOCATE": true, "DECLARE": true, "FETCH": true, "MOVE": true, "CLOSE": true,
}

// activeSpanReporter is the optional question a tracer can answer for the
// child-only rule. integrations/tracing/otel's *Tracer implements it
// (HasActiveSpan, v0.7.0+); without it SpanTracer falls back to a trace id on
// the context (sdk.TraceIDFromContext, set inside a traced HTTP request).
type activeSpanReporter interface {
	HasActiveSpan(ctx context.Context) bool
}

// SpanTracerConfig configures NewSpanTracer.
type SpanTracerConfig struct {
	// OmitQueryText drops db.query.text. Set it when the host builds SQL with
	// literal values inlined; gopernicus stores always bind parameters, whose
	// values SpanTracer never records either way.
	OmitQueryText bool
}

// SpanTracer records one child span per SQL statement (and one per batch)
// through any sdk tracing.Tracer. Install it as Config.Tracer, or compose it
// with LogQueries through NewMultiQueryTracer.
//
// It starts a span only when the context already carries one, so pool pings,
// pollers and background loops make no traces. A span is named by the
// statement's leading keyword (SELECT, INSERT, …) or db.query, never by SQL
// text. It records db.system.name, db.operation.name and db.query.text (the SQL
// with its placeholders, truncated to 2 KiB) and NEVER the bind arguments. A
// failure records only the SQLSTATE (db.response.status_code) and the
// constraint name, and marks the span failed with a fixed "sql error <code>"
// error, never the driver's message.
type SpanTracer struct {
	tracer tracing.Tracer
	cfg    SpanTracerConfig
}

var (
	_ jackpgx.QueryTracer = (*SpanTracer)(nil)
	_ jackpgx.BatchTracer = (*SpanTracer)(nil)
)

// NewSpanTracer returns a SpanTracer over tracer. A nil tracer records nothing.
func NewSpanTracer(tracer tracing.Tracer, cfg SpanTracerConfig) *SpanTracer {
	return &SpanTracer{tracer: tracer, cfg: cfg}
}

// spanKey carries a statement's or batch's open span from start to end.
type spanKey struct{}

// TraceQueryStart starts a statement span under an active parent.
func (t *SpanTracer) TraceQueryStart(ctx context.Context, _ *jackpgx.Conn, data jackpgx.TraceQueryStartData) context.Context {
	if !t.parented(ctx) {
		return ctx
	}
	op := operation(data.SQL)
	name := op
	if name == "" {
		name = spanNameFallback
	}
	ctx, span := t.tracer.StartSpan(ctx, name)
	attrs := []tracing.Attribute{tracing.StringAttribute("db.system.name", "postgresql")}
	if op != "" {
		attrs = append(attrs, tracing.StringAttribute("db.operation.name", op))
	}
	if !t.cfg.OmitQueryText {
		attrs = append(attrs, tracing.StringAttribute("db.query.text", truncateUTF8(data.SQL, maxQueryTextBytes)))
	}
	span.SetAttributes(attrs...)
	return context.WithValue(ctx, spanKey{}, span)
}

// TraceQueryEnd finishes the statement span, recording a failure safely.
func (t *SpanTracer) TraceQueryEnd(ctx context.Context, _ *jackpgx.Conn, data jackpgx.TraceQueryEndData) {
	finish(ctx, data.Err)
}

// TraceBatchStart starts one span for a whole batch under an active parent.
func (t *SpanTracer) TraceBatchStart(ctx context.Context, _ *jackpgx.Conn, data jackpgx.TraceBatchStartData) context.Context {
	if !t.parented(ctx) {
		return ctx
	}
	ctx, span := t.tracer.StartSpan(ctx, spanNameBatch)
	size := 0
	if data.Batch != nil {
		size = data.Batch.Len()
	}
	span.SetAttributes(tracing.StringAttribute("db.system.name", "postgresql"), tracing.StringAttribute("db.batch.size", strconv.Itoa(size)))
	return context.WithValue(ctx, spanKey{}, span)
}

// TraceBatchQuery records the first failing statement of a batch on its span.
func (t *SpanTracer) TraceBatchQuery(ctx context.Context, _ *jackpgx.Conn, data jackpgx.TraceBatchQueryData) {
	if data.Err == nil {
		return
	}
	if span, ok := ctx.Value(spanKey{}).(tracing.SpanFinisher); ok {
		recordSQLError(span, data.Err)
	}
}

// TraceBatchEnd finishes the batch span.
func (t *SpanTracer) TraceBatchEnd(ctx context.Context, _ *jackpgx.Conn, data jackpgx.TraceBatchEndData) {
	finish(ctx, data.Err)
}

// parented reports whether ctx already carries a span this tracer may parent
// under.
func (t *SpanTracer) parented(ctx context.Context) bool {
	if t == nil || t.tracer == nil {
		return false
	}
	if _, noop := t.tracer.(tracing.Noop); noop {
		return false
	}
	if r, ok := t.tracer.(activeSpanReporter); ok {
		return r.HasActiveSpan(ctx)
	}
	id, ok := sdk.TraceIDFromContext(ctx)
	return ok && id != ""
}

func finish(ctx context.Context, err error) {
	span, ok := ctx.Value(spanKey{}).(tracing.SpanFinisher)
	if !ok {
		return
	}
	if err != nil {
		recordSQLError(span, err)
	}
	span.Finish()
}

// recordSQLError records only the SQLSTATE and constraint name, and a fixed
// error, never the driver's message, which can carry values.
func recordSQLError(span tracing.SpanFinisher, err error) {
	var pgErr *pgconn.PgError
	switch {
	case errors.As(err, &pgErr):
		attrs := []tracing.Attribute{tracing.StringAttribute("db.response.status_code", pgErr.Code)}
		if pgErr.ConstraintName != "" {
			attrs = append(attrs, tracing.StringAttribute("db.postgresql.constraint", pgErr.ConstraintName))
		}
		span.SetAttributes(attrs...)
		span.RecordError(errors.New("sql error " + pgErr.Code))
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		span.RecordError(errors.New("sql canceled"))
	default:
		span.RecordError(errors.New("sql error"))
	}
}

// operation returns the statement's leading keyword when it is in
// spanKeywords, skipping whitespace and leading comments; "" otherwise.
func operation(sql string) string {
	s := sql
	for {
		s = strings.TrimLeft(s, " \t\r\n(")
		switch {
		case strings.HasPrefix(s, "--"):
			if i := strings.IndexByte(s, '\n'); i >= 0 {
				s = s[i+1:]
				continue
			}
			return ""
		case strings.HasPrefix(s, "/*"):
			if i := strings.Index(s, "*/"); i >= 0 {
				s = s[i+2:]
				continue
			}
			return ""
		}
		break
	}
	end := strings.IndexFunc(s, func(r rune) bool { return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') })
	if end < 0 {
		end = len(s)
	}
	word := strings.ToUpper(s[:end])
	if spanKeywords[word] {
		return word
	}
	return ""
}

// truncateUTF8 cuts s to at most n bytes on a rune boundary.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
