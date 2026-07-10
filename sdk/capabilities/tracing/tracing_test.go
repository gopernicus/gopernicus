package tracing

import (
	"context"
	"errors"
	"testing"
)

func TestStringAttribute(t *testing.T) {
	attr := StringAttribute("key", "value")
	if attr.Key != "key" || attr.Value != "value" {
		t.Fatalf("StringAttribute = %+v, want {Key:key Value:value}", attr)
	}
}

func TestNoopSatisfiesTracer(t *testing.T) {
	var _ Tracer = Noop{}
}

func TestNoopStartSpanReturnsSameContext(t *testing.T) {
	type ctxKey struct{}
	ctx := context.WithValue(context.Background(), ctxKey{}, "v")

	got, finisher := Noop{}.StartSpan(ctx, "op")
	if got != ctx {
		t.Fatal("StartSpan returned a different context")
	}
	if finisher == nil {
		t.Fatal("StartSpan returned a nil SpanFinisher")
	}
}

func TestNoopFinisherDoesNothing(t *testing.T) {
	_, finisher := Noop{}.StartSpan(context.Background(), "op")

	// None of these must panic, and RecordError(nil) is tolerated.
	finisher.SetAttributes(StringAttribute("k", "v"))
	finisher.SetAttributes()
	finisher.RecordError(errors.New("boom"))
	finisher.RecordError(nil)
	finisher.Finish()
}

func TestNoopFinisherDoesNotSatisfySpanIdentity(t *testing.T) {
	_, finisher := Noop{}.StartSpan(context.Background(), "op")
	if _, ok := finisher.(SpanIdentity); ok {
		t.Fatal("Noop finisher must not satisfy SpanIdentity")
	}
}

// identityFinisher is a finisher carrying stable span identity — the shape an
// integration (e.g. otel) exposes so trace/span IDs can reach the logs.
type identityFinisher struct {
	noopSpanFinisher
	traceID string
	spanID  string
}

func (f identityFinisher) TraceID() string { return f.traceID }
func (f identityFinisher) SpanID() string  { return f.spanID }

var _ SpanIdentity = identityFinisher{}

func TestSpanIdentityExposesIDs(t *testing.T) {
	var id SpanIdentity = identityFinisher{traceID: "trace-abc", spanID: "span-123"}
	if id.TraceID() != "trace-abc" || id.SpanID() != "span-123" {
		t.Fatalf("SpanIdentity IDs = %q/%q, want trace-abc/span-123", id.TraceID(), id.SpanID())
	}
}
