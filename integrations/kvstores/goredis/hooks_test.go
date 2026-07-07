// These tests are hermetic: they invoke the go-redis hook wrappers directly with
// a fake `next`, so no Redis connection is needed to prove logging and tracing
// behavior.
package goredis

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/gopernicus/gopernicus/sdk/tracing"
)

// bufLogger returns a logger writing text records to buf at debug level so the
// tests can assert on emitted (or absent) log lines.
func bufLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestLoggingHookLogsCommandErrors(t *testing.T) {
	var buf bytes.Buffer
	h := &loggingHook{log: bufLogger(&buf)}

	next := func(ctx context.Context, cmd redis.Cmder) error { return errors.New("boom") }
	wrapped := h.ProcessHook(next)

	err := wrapped(context.Background(), redis.NewCmd(context.Background(), "get", "k"))
	if err == nil || err.Error() != "boom" {
		t.Fatalf("wrapped() error = %v, want boom (hook must not alter the error)", err)
	}

	out := buf.String()
	if !strings.Contains(out, "redis command failed") {
		t.Errorf("log = %q, want it to contain %q", out, "redis command failed")
	}
	if !strings.Contains(out, "boom") {
		t.Errorf("log = %q, want it to contain the error text", out)
	}
	if !strings.Contains(out, "command=get") {
		t.Errorf("log = %q, want it to name the command", out)
	}
}

func TestLoggingHookIgnoresRedisNil(t *testing.T) {
	var buf bytes.Buffer
	h := &loggingHook{log: bufLogger(&buf)}

	next := func(ctx context.Context, cmd redis.Cmder) error { return redis.Nil }
	wrapped := h.ProcessHook(next)

	if err := wrapped(context.Background(), redis.NewCmd(context.Background(), "get", "k")); !errors.Is(err, redis.Nil) {
		t.Fatalf("wrapped() error = %v, want redis.Nil passthrough", err)
	}
	if buf.Len() != 0 {
		t.Errorf("log = %q, want empty (redis.Nil is a cache miss, not an error)", buf.String())
	}
}

func TestLoggingHookLogsSlowCommands(t *testing.T) {
	var buf bytes.Buffer
	h := &loggingHook{log: bufLogger(&buf), slowThreshold: time.Millisecond}

	next := func(ctx context.Context, cmd redis.Cmder) error {
		time.Sleep(5 * time.Millisecond)
		return nil
	}
	wrapped := h.ProcessHook(next)

	if err := wrapped(context.Background(), redis.NewCmd(context.Background(), "set", "k", "v")); err != nil {
		t.Fatalf("wrapped() error = %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "redis slow command") {
		t.Errorf("log = %q, want it to contain %q", out, "redis slow command")
	}
	if !strings.Contains(out, "command=set") {
		t.Errorf("log = %q, want it to name the command", out)
	}
}

func TestLoggingHookQuietOnFastSuccess(t *testing.T) {
	var buf bytes.Buffer
	h := &loggingHook{log: bufLogger(&buf)} // no threshold: slow logging disabled

	next := func(ctx context.Context, cmd redis.Cmder) error { return nil }
	wrapped := h.ProcessHook(next)

	if err := wrapped(context.Background(), redis.NewCmd(context.Background(), "get", "k")); err != nil {
		t.Fatalf("wrapped() error = %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("log = %q, want empty for a fast successful command", buf.String())
	}
}

func TestLoggingHookLogsSlowPipeline(t *testing.T) {
	var buf bytes.Buffer
	h := &loggingHook{log: bufLogger(&buf), slowThreshold: time.Millisecond}

	next := func(ctx context.Context, cmds []redis.Cmder) error {
		time.Sleep(5 * time.Millisecond)
		return nil
	}
	wrapped := h.ProcessPipelineHook(next)

	cmds := []redis.Cmder{
		redis.NewCmd(context.Background(), "get", "a"),
		redis.NewCmd(context.Background(), "get", "b"),
	}
	if err := wrapped(context.Background(), cmds); err != nil {
		t.Fatalf("wrapped() error = %v", err)
	}
	if !strings.Contains(buf.String(), "command=pipeline(2)") {
		t.Errorf("log = %q, want it to name the pipeline with its length", buf.String())
	}
}

func TestLoggingHookNilLoggerFallsBack(t *testing.T) {
	if LoggingHook(nil) == nil {
		t.Fatal("LoggingHook(nil) = nil, want a hook backed by slog.Default()")
	}
}

// --- tracing ---

type recordingSpan struct {
	attrs    []tracing.Attribute
	errs     []error
	finished int
}

func (s *recordingSpan) SetAttributes(attrs ...tracing.Attribute) {
	s.attrs = append(s.attrs, attrs...)
}
func (s *recordingSpan) RecordError(err error) { s.errs = append(s.errs, err) }
func (s *recordingSpan) Finish()               { s.finished++ }

func (s *recordingSpan) attr(key string) (string, bool) {
	for _, a := range s.attrs {
		if a.Key == key {
			return a.Value, true
		}
	}
	return "", false
}

type recordingTracer struct {
	spans []*recordingSpan
	names []string
}

func (tr *recordingTracer) StartSpan(ctx context.Context, name string) (context.Context, tracing.SpanFinisher) {
	s := &recordingSpan{}
	tr.spans = append(tr.spans, s)
	tr.names = append(tr.names, name)
	return ctx, s
}

func TestTracingHookSpansCommand(t *testing.T) {
	tr := &recordingTracer{}
	h := &tracingHook{tracer: tr}

	next := func(ctx context.Context, cmd redis.Cmder) error { return nil }
	wrapped := h.ProcessHook(next)

	if err := wrapped(context.Background(), redis.NewCmd(context.Background(), "get", "k")); err != nil {
		t.Fatalf("wrapped() error = %v", err)
	}

	if len(tr.spans) != 1 {
		t.Fatalf("spans = %d, want 1", len(tr.spans))
	}
	if tr.names[0] != "redis.get" {
		t.Errorf("span name = %q, want redis.get", tr.names[0])
	}
	span := tr.spans[0]
	if span.finished != 1 {
		t.Errorf("finished = %d, want 1", span.finished)
	}
	if len(span.errs) != 0 {
		t.Errorf("recorded errors = %v, want none on success", span.errs)
	}
	if v, ok := span.attr("db.system"); !ok || v != "redis" {
		t.Errorf("db.system attr = %q,%v, want redis,true", v, ok)
	}
	if v, ok := span.attr("db.operation"); !ok || v != "get" {
		t.Errorf("db.operation attr = %q,%v, want get,true", v, ok)
	}
}

func TestTracingHookRecordsError(t *testing.T) {
	tr := &recordingTracer{}
	h := &tracingHook{tracer: tr}

	boom := errors.New("boom")
	next := func(ctx context.Context, cmd redis.Cmder) error { return boom }
	wrapped := h.ProcessHook(next)

	if err := wrapped(context.Background(), redis.NewCmd(context.Background(), "get", "k")); !errors.Is(err, boom) {
		t.Fatalf("wrapped() error = %v, want boom", err)
	}
	span := tr.spans[0]
	if len(span.errs) != 1 || !errors.Is(span.errs[0], boom) {
		t.Errorf("recorded errors = %v, want [boom]", span.errs)
	}
	if span.finished != 1 {
		t.Errorf("finished = %d, want 1", span.finished)
	}
}

func TestTracingHookIgnoresRedisNil(t *testing.T) {
	tr := &recordingTracer{}
	h := &tracingHook{tracer: tr}

	next := func(ctx context.Context, cmd redis.Cmder) error { return redis.Nil }
	wrapped := h.ProcessHook(next)

	if err := wrapped(context.Background(), redis.NewCmd(context.Background(), "get", "k")); !errors.Is(err, redis.Nil) {
		t.Fatalf("wrapped() error = %v, want redis.Nil", err)
	}
	span := tr.spans[0]
	if len(span.errs) != 0 {
		t.Errorf("recorded errors = %v, want none (redis.Nil is a cache miss)", span.errs)
	}
	if span.finished != 1 {
		t.Errorf("finished = %d, want 1", span.finished)
	}
}

func TestTracingHookSpansPipeline(t *testing.T) {
	tr := &recordingTracer{}
	h := &tracingHook{tracer: tr}

	next := func(ctx context.Context, cmds []redis.Cmder) error { return nil }
	wrapped := h.ProcessPipelineHook(next)

	cmds := []redis.Cmder{
		redis.NewCmd(context.Background(), "get", "a"),
		redis.NewCmd(context.Background(), "get", "b"),
	}
	if err := wrapped(context.Background(), cmds); err != nil {
		t.Fatalf("wrapped() error = %v", err)
	}

	if tr.names[0] != "redis.pipeline" {
		t.Errorf("span name = %q, want redis.pipeline", tr.names[0])
	}
	if v, ok := tr.spans[0].attr("db.redis.pipeline_length"); !ok || v != "2" {
		t.Errorf("pipeline_length attr = %q,%v, want 2,true", v, ok)
	}
}

func TestTracingHookNilTracerUsesNoop(t *testing.T) {
	h, ok := TracingHook(nil).(*tracingHook)
	if !ok {
		t.Fatalf("TracingHook(nil) type = %T, want *tracingHook", TracingHook(nil))
	}
	if _, ok := h.tracer.(tracing.Noop); !ok {
		t.Errorf("tracer = %T, want tracing.Noop", h.tracer)
	}
}
