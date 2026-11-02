package pgxdb

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	jackpgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/tracing"
)

// recSpan records what a SpanTracer did to one span.
type recSpan struct {
	name     string
	attrs    map[string]string
	errs     []string
	finished bool
}

func (s *recSpan) SetAttributes(attrs ...tracing.Attribute) {
	for _, a := range attrs {
		s.attrs[a.Key] = a.Value
	}
}
func (s *recSpan) RecordError(err error) { s.errs = append(s.errs, err.Error()) }
func (s *recSpan) Finish()               { s.finished = true }

// recTracer is a tracing.Tracer that records spans; active, when set, makes it
// an activeSpanReporter.
type recTracer struct {
	mu    sync.Mutex
	spans []*recSpan
}

func (r *recTracer) StartSpan(ctx context.Context, name string) (context.Context, tracing.SpanFinisher) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := &recSpan{name: name, attrs: map[string]string{}}
	r.spans = append(r.spans, s)
	return ctx, s
}

type activeKey struct{}

// reportingTracer answers HasActiveSpan from a context flag.
type reportingTracer struct{ recTracer }

func (r *reportingTracer) HasActiveSpan(ctx context.Context) bool {
	return ctx.Value(activeKey{}) != nil
}

func runQuery(t *SpanTracer, ctx context.Context, sql string, args []any, err error) {
	ctx = t.TraceQueryStart(ctx, nil, jackpgx.TraceQueryStartData{SQL: sql, Args: args})
	t.TraceQueryEnd(ctx, nil, jackpgx.TraceQueryEndData{Err: err})
}

func TestSpanTracerChildOnly(t *testing.T) {
	rt := &reportingTracer{}
	st := NewSpanTracer(rt, SpanTracerConfig{})
	runQuery(st, context.Background(), "SELECT 1", nil, nil)
	if len(rt.spans) != 0 {
		t.Fatalf("no parent: %d spans, want 0", len(rt.spans))
	}
	runQuery(st, context.WithValue(context.Background(), activeKey{}, true), "SELECT 1", nil, nil)
	if len(rt.spans) != 1 || !rt.spans[0].finished {
		t.Fatalf("parent via HasActiveSpan: %+v", rt.spans)
	}

	// A tracer without HasActiveSpan falls back to a trace id on the context.
	plain := &recTracer{}
	st = NewSpanTracer(plain, SpanTracerConfig{})
	runQuery(st, context.Background(), "SELECT 1", nil, nil)
	runQuery(st, sdk.WithTraceID(context.Background(), "4bf92f3577b34da6a3ce929d0e0e4736"), "SELECT 1", nil, nil)
	if len(plain.spans) != 1 {
		t.Fatalf("trace-id fallback: %d spans, want 1", len(plain.spans))
	}

	// Noop and nil tracers record nothing and never panic.
	for _, tr := range []tracing.Tracer{tracing.Noop{}, nil} {
		runQuery(NewSpanTracer(tr, SpanTracerConfig{}), sdk.WithTraceID(context.Background(), "x"), "SELECT 1", nil, nil)
	}
	var nilTracer *SpanTracer
	if nilTracer.parented(context.Background()) {
		t.Fatal("nil SpanTracer parented")
	}
}

func TestSpanTracerNamesAndText(t *testing.T) {
	parent := sdk.WithTraceID(context.Background(), "t")
	for sql, want := range map[string]string{
		"SELECT * FROM iam_tuples WHERE id = $1":       "SELECT",
		"  \n insert into t (a) values ($1)":           "INSERT",
		"-- note\n/* hint */ UPDATE t SET a = $1":      "UPDATE",
		"(SELECT 1) UNION (SELECT 2)":                  "SELECT",
		"WITH x AS (SELECT 1) DELETE FROM t":           "WITH",
		"frobnicate everything":                        "db.query",
		"":                                             "db.query",
		"SELECTED_COLUMN_THAT_IS_NOT_A_KEYWORD FROM t": "db.query",
	} {
		rt := &recTracer{}
		runQuery(NewSpanTracer(rt, SpanTracerConfig{}), parent, sql, nil, nil)
		s := rt.spans[0]
		if s.name != want || s.attrs["db.system.name"] != "postgresql" || s.attrs["db.query.text"] != sql {
			t.Errorf("%q: name %q attrs %v, want name %q and the SQL as text", sql, s.name, s.attrs, want)
		}
		if want == "db.query" && s.attrs["db.operation.name"] != "" {
			t.Errorf("%q: unbounded operation name %q", sql, s.attrs["db.operation.name"])
		}
	}

	rt := &recTracer{}
	long := "SELECT '" + strings.Repeat("é", 3000) + "'"
	runQuery(NewSpanTracer(rt, SpanTracerConfig{}), parent, long, nil, nil)
	if got := rt.spans[0].attrs["db.query.text"]; len(got) > maxQueryTextBytes || !strings.HasPrefix(long, got) || strings.ContainsRune(got, '�') {
		t.Fatalf("truncation: %d bytes, valid prefix %v", len(got), strings.HasPrefix(long, got))
	}

	rt = &recTracer{}
	runQuery(NewSpanTracer(rt, SpanTracerConfig{OmitQueryText: true}), parent, "SELECT 1", nil, nil)
	if _, ok := rt.spans[0].attrs["db.query.text"]; ok {
		t.Fatal("OmitQueryText still recorded the SQL")
	}
}

// TestSpanTracerNeverRecordsArgsOrErrorText: bind arguments and the driver's
// error message reach no attribute and no recorded error.
func TestSpanTracerNeverRecordsArgsOrErrorText(t *testing.T) {
	const secret = "s3cr3t-token-value"
	parent := sdk.WithTraceID(context.Background(), "t")
	pgErr := &pgconn.PgError{Code: "23505", ConstraintName: "iam_tuples_pkey", Message: "duplicate key value violates unique constraint", Detail: "Key (id)=(" + secret + ") already exists."}
	cases := []struct {
		name    string
		err     error
		wantErr string
		attrs   map[string]string
	}{
		{"success", nil, "", nil},
		{"pg error", pgErr, "sql error 23505", map[string]string{"db.response.status_code": "23505", "db.postgresql.constraint": "iam_tuples_pkey"}},
		{"wrapped pg error", errors.Join(errors.New("outer "+secret), pgErr), "sql error 23505", map[string]string{"db.response.status_code": "23505"}},
		{"canceled", context.Canceled, "sql canceled", nil},
		{"other", errors.New("conn reset " + secret), "sql error", nil},
	}
	for _, tc := range cases {
		rt := &recTracer{}
		runQuery(NewSpanTracer(rt, SpanTracerConfig{}), parent, "INSERT INTO t (id) VALUES ($1)", []any{secret}, tc.err)
		s := rt.spans[0]
		for k, v := range tc.attrs {
			if s.attrs[k] != v {
				t.Errorf("%s: %s = %q, want %q", tc.name, k, s.attrs[k], v)
			}
		}
		if (tc.wantErr == "") != (len(s.errs) == 0) || (tc.wantErr != "" && s.errs[0] != tc.wantErr) {
			t.Errorf("%s: errors %v, want %q", tc.name, s.errs, tc.wantErr)
		}
		for k, v := range s.attrs {
			if strings.Contains(v, secret) || strings.Contains(v, "duplicate key") {
				t.Errorf("%s: attribute %s leaks %q", tc.name, k, v)
			}
		}
		for _, e := range s.errs {
			if strings.Contains(e, secret) || strings.Contains(e, "duplicate key") {
				t.Errorf("%s: recorded error leaks %q", tc.name, e)
			}
		}
	}
}

func TestSpanTracerBatch(t *testing.T) {
	rt := &recTracer{}
	st := NewSpanTracer(rt, SpanTracerConfig{})
	b := &jackpgx.Batch{}
	b.Queue("SELECT 1")
	b.Queue("INSERT INTO t VALUES ($1)", "secret")
	ctx := st.TraceBatchStart(sdk.WithTraceID(context.Background(), "t"), nil, jackpgx.TraceBatchStartData{Batch: b})
	st.TraceBatchQuery(ctx, nil, jackpgx.TraceBatchQueryData{SQL: "SELECT 1"})
	st.TraceBatchQuery(ctx, nil, jackpgx.TraceBatchQueryData{SQL: "INSERT INTO t VALUES ($1)", Args: []any{"secret"}, Err: &pgconn.PgError{Code: "23505"}})
	st.TraceBatchEnd(ctx, nil, jackpgx.TraceBatchEndData{})
	if len(rt.spans) != 1 {
		t.Fatalf("batch made %d spans, want 1", len(rt.spans))
	}
	s := rt.spans[0]
	if s.name != "db.batch" || s.attrs["db.batch.size"] != "2" || s.attrs["db.response.status_code"] != "23505" || !s.finished {
		t.Fatalf("batch span = %+v", s)
	}

	// MultiQueryTracer forwards batches to the SpanTracer it holds.
	rt = &recTracer{}
	multi := NewMultiQueryTracer(NewLoggingQueryTracer(nil), NewSpanTracer(rt, SpanTracerConfig{}))
	ctx = multi.TraceBatchStart(sdk.WithTraceID(context.Background(), "t"), nil, jackpgx.TraceBatchStartData{Batch: b})
	multi.TraceBatchEnd(ctx, nil, jackpgx.TraceBatchEndData{})
	if len(rt.spans) != 1 || !rt.spans[0].finished {
		t.Fatalf("multi-tracer batch forwarding: %+v", rt.spans)
	}
}

func TestStatsNilAndUnopened(t *testing.T) {
	var db *DB
	if db.Stats() != (PoolStats{}) {
		t.Fatal("nil *DB Stats not zero")
	}
	if (&DB{}).Stats() != (PoolStats{}) {
		t.Fatal("pool-less DB Stats not zero")
	}
}
