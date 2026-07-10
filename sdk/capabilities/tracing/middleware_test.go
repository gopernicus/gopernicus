package tracing

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/foundation/logging"
)

// recSpan records everything the middleware does to a span. It does NOT
// implement SpanIdentity.
type recSpan struct {
	name     string
	attrs    map[string]string
	err      error
	finished bool
}

func (s *recSpan) SetAttributes(attrs ...Attribute) {
	for _, a := range attrs {
		s.attrs[a.Key] = a.Value
	}
}
func (s *recSpan) RecordError(err error) { s.err = err }
func (s *recSpan) Finish()               { s.finished = true }

// recTracer captures the spans it started.
type recTracer struct {
	spans []*recSpan
}

func (t *recTracer) StartSpan(ctx context.Context, name string) (context.Context, SpanFinisher) {
	s := &recSpan{name: name, attrs: map[string]string{}}
	t.spans = append(t.spans, s)
	return ctx, s
}

// idSpan additionally carries stable trace/span identity.
type idSpan struct {
	recSpan
	traceID string
	spanID  string
}

func (s *idSpan) TraceID() string { return s.traceID }
func (s *idSpan) SpanID() string  { return s.spanID }

// idTracer returns spans satisfying SpanIdentity.
type idTracer struct {
	traceID string
	spanID  string
}

func (t *idTracer) StartSpan(ctx context.Context, name string) (context.Context, SpanFinisher) {
	return ctx, &idSpan{
		recSpan: recSpan{name: name, attrs: map[string]string{}},
		traceID: t.traceID,
		spanID:  t.spanID,
	}
}

func TestMiddleware_StartsAndFinishesSpanPerRequest(t *testing.T) {
	tr := &recTracer{}
	mux := http.NewServeMux()
	mux.Handle("GET /posts/{id}", Middleware(tr)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/posts/42", nil)
	mux.ServeHTTP(rec, req)

	if len(tr.spans) != 1 {
		t.Fatalf("started %d spans, want 1", len(tr.spans))
	}
	span := tr.spans[0]
	if !span.finished {
		t.Error("span was not finished")
	}
	if span.name != "GET /posts/{id}" {
		t.Errorf("span name = %q, want route pattern", span.name)
	}
	if got := span.attrs["http.method"]; got != "GET" {
		t.Errorf("http.method = %q, want GET", got)
	}
	if got := span.attrs["http.route"]; got != "GET /posts/{id}" {
		t.Errorf("http.route = %q, want the pattern", got)
	}
	if got := span.attrs["net.peer.ip"]; got != "192.0.2.1" {
		t.Errorf("net.peer.ip = %q, want host parsed from RemoteAddr", got)
	}
}

func TestMiddleware_StaticNameFallbackWhenNoPattern(t *testing.T) {
	tr := &recTracer{}
	// Served directly, not via a mux, so r.Pattern is empty.
	h := Middleware(tr)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/anything", nil))

	if len(tr.spans) != 1 {
		t.Fatalf("started %d spans, want 1", len(tr.spans))
	}
	span := tr.spans[0]
	if span.name != "http.request" {
		t.Errorf("span name = %q, want static fallback http.request", span.name)
	}
	if _, ok := span.attrs["http.route"]; ok {
		t.Error("http.route must be omitted when there is no pattern")
	}
}

func TestMiddleware_StatusCodeAttribute(t *testing.T) {
	tr := &recTracer{}
	h := Middleware(tr)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := tr.spans[0].attrs["http.status_code"]; got != "201" {
		t.Errorf("http.status_code = %q, want 201", got)
	}
}

func TestMiddleware_RecordsServerError(t *testing.T) {
	tr := &recTracer{}
	h := Middleware(tr)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	span := tr.spans[0]
	if span.err == nil {
		t.Fatal("5xx did not record an error on the span")
	}
	if span.err.Error() != "server error: 500" {
		t.Errorf("recorded error = %q, want synthesized server error", span.err.Error())
	}
}

func TestMiddleware_ClientErrorDoesNotRecord(t *testing.T) {
	tr := &recTracer{}
	h := Middleware(tr)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if tr.spans[0].err != nil {
		t.Errorf("4xx must not record an error, got %v", tr.spans[0].err)
	}
}

func TestMiddleware_SpanIdentityStashesTraceAndSpanIDs(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(logging.NewTracingHandler(slog.NewJSONHandler(&buf, nil)))
	tr := &idTracer{traceID: "trace-abc", spanID: "span-123"}

	mux := http.NewServeMux()
	mux.Handle("GET /x", Middleware(tr)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.InfoContext(r.Context(), "hit")
	})))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))

	fields := decodeLogLine(t, buf.Bytes())
	if fields["trace_id"] != "trace-abc" {
		t.Errorf("trace_id = %v, want trace-abc", fields["trace_id"])
	}
	if fields["span_id"] != "span-123" {
		t.Errorf("span_id = %v, want span-123", fields["span_id"])
	}
}

func TestMiddleware_NoopPathCarriesNoIDs(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(logging.NewTracingHandler(slog.NewJSONHandler(&buf, nil)))

	// Nil tracer resolves to Noop, whose finisher does not satisfy SpanIdentity,
	// so no trace/span IDs reach the context.
	h := Middleware(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.InfoContext(r.Context(), "hit")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	fields := decodeLogLine(t, buf.Bytes())
	if _, ok := fields["trace_id"]; ok {
		t.Error("Noop path must not emit trace_id")
	}
	if _, ok := fields["span_id"]; ok {
		t.Error("Noop path must not emit span_id")
	}
}

func decodeLogLine(t *testing.T, b []byte) map[string]any {
	t.Helper()
	fields := map[string]any{}
	if err := json.Unmarshal(bytes.TrimSpace(b), &fields); err != nil {
		t.Fatalf("decoding log line %q: %v", b, err)
	}
	return fields
}
