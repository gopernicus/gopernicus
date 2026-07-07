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
