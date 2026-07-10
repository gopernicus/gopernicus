package sdk

import (
	"context"
	"testing"
)

func TestRequestIDFromContext(t *testing.T) {
	if _, ok := RequestIDFromContext(context.Background()); ok {
		t.Error("empty context should report no request id")
	}
	ctx := WithRequestID(context.Background(), "r1")
	if v, ok := RequestIDFromContext(ctx); !ok || v != "r1" {
		t.Errorf("RequestIDFromContext = %q,%v, want r1,true", v, ok)
	}
}

func TestTraceIDFromContext(t *testing.T) {
	if _, ok := TraceIDFromContext(context.Background()); ok {
		t.Error("empty context should report no trace id")
	}
	ctx := WithTraceID(context.Background(), "t1")
	if v, ok := TraceIDFromContext(ctx); !ok || v != "t1" {
		t.Errorf("TraceIDFromContext = %q,%v, want t1,true", v, ok)
	}
}

func TestSpanIDFromContext(t *testing.T) {
	if _, ok := SpanIDFromContext(context.Background()); ok {
		t.Error("empty context should report no span id")
	}
	ctx := WithSpanID(context.Background(), "s1")
	if v, ok := SpanIDFromContext(ctx); !ok || v != "s1" {
		t.Errorf("SpanIDFromContext = %q,%v, want s1,true", v, ok)
	}
}

func TestIDFromContext_EmptyStringReportsAbsent(t *testing.T) {
	ctx := WithRequestID(context.Background(), "")
	if _, ok := RequestIDFromContext(ctx); ok {
		t.Error("empty request id string should report absent")
	}
	ctx = WithTraceID(context.Background(), "")
	if _, ok := TraceIDFromContext(ctx); ok {
		t.Error("empty trace id string should report absent")
	}
	ctx = WithSpanID(context.Background(), "")
	if _, ok := SpanIDFromContext(ctx); ok {
		t.Error("empty span id string should report absent")
	}
}
