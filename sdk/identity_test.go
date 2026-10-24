package sdk

import (
	"context"
	"sync"
	"testing"
)

func TestIdentityConstantValues(t *testing.T) {
	if PrincipalTypeUser != "user" || PrincipalTypeServiceAccount != "service_account" {
		t.Fatal("subject-type conventions changed")
	}
	if AddressKindEmail != "email" || AddressKindPhone != "phone" {
		t.Fatal("address-kind conventions changed")
	}
}

func TestWithPrincipalRoundTrip(t *testing.T) {
	for _, want := range []Principal{
		{Type: PrincipalTypeUser, ID: "u-123"},
		{Type: PrincipalTypeServiceAccount, ID: "sa-123"},
		{Type: "host-defined", ID: "actor-123"},
	} {
		parent := context.Background()
		ctx := WithPrincipal(parent, want)
		if got, ok := PrincipalFromContext(ctx); !ok || got != want {
			t.Errorf("PrincipalFromContext() = %+v, %v; want %+v, true", got, ok, want)
		}
		if got, ok := PrincipalFromContext(parent); ok || got != (Principal{}) {
			t.Fatal("WithPrincipal changed the parent context")
		}
	}
}

func TestPrincipalFromContextAbsentOrIncomplete(t *testing.T) {
	for name, ctx := range map[string]context.Context{
		"absent":       context.Background(),
		"zero":         WithPrincipal(context.Background(), Principal{}),
		"missing type": WithPrincipal(context.Background(), Principal{ID: "u1"}),
		"missing ID":   WithPrincipal(context.Background(), Principal{Type: PrincipalTypeUser}),
	} {
		t.Run(name, func(t *testing.T) {
			if got, ok := PrincipalFromContext(ctx); ok || got != (Principal{}) {
				t.Errorf("PrincipalFromContext() = %+v, %v; want zero, false", got, ok)
			}
		})
	}
}

func TestPrincipalContextCoexistsWithRequestAndTraceIDs(t *testing.T) {
	parentPrincipal := Principal{Type: PrincipalTypeUser, ID: "user-1"}
	childPrincipal := Principal{Type: PrincipalTypeServiceAccount, ID: "service-1"}
	parent := WithPrincipal(context.Background(), parentPrincipal)
	parent = WithRequestID(parent, "request-parent")
	parent = WithTraceID(parent, "trace-1")
	parent = WithSpanID(parent, "span-1")
	child := WithPrincipal(parent, childPrincipal)
	child = WithRequestID(child, "request-child")

	for _, tc := range []struct {
		name      string
		ctx       context.Context
		principal Principal
		requestID string
	}{
		{"parent", parent, parentPrincipal, "request-parent"},
		{"child", child, childPrincipal, "request-child"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, ok := PrincipalFromContext(tc.ctx); !ok || got != tc.principal {
				t.Fatalf("principal = %+v, %v; want %+v", got, ok, tc.principal)
			}
			if got, ok := RequestIDFromContext(tc.ctx); !ok || got != tc.requestID {
				t.Fatalf("request ID = %q, %v; want %q", got, ok, tc.requestID)
			}
			if got, ok := TraceIDFromContext(tc.ctx); !ok || got != "trace-1" {
				t.Fatalf("trace ID = %q, %v; want trace-1", got, ok)
			}
			if got, ok := SpanIDFromContext(tc.ctx); !ok || got != "span-1" {
				t.Fatalf("span ID = %q, %v; want span-1", got, ok)
			}
		})
	}

	for _, value := range []any{"not a principal", &childPrincipal, Principal{ID: "missing-type"}} {
		malformed := context.WithValue(child, principalContextKey{}, value)
		if got, ok := PrincipalFromContext(malformed); ok || got != (Principal{}) {
			t.Fatalf("malformed value %T fell back to a parent principal: %+v, %v", value, got, ok)
		}
		if got, ok := RequestIDFromContext(malformed); !ok || got != "request-child" {
			t.Fatalf("malformed principal changed request ID: %q, %v", got, ok)
		}
		if got, ok := TraceIDFromContext(malformed); !ok || got != "trace-1" {
			t.Fatalf("malformed principal changed trace ID: %q, %v", got, ok)
		}
		if got, ok := SpanIDFromContext(malformed); !ok || got != "span-1" {
			t.Fatalf("malformed principal changed span ID: %q, %v", got, ok)
		}
	}
}

func TestPrincipalCaptureRecordsDerivedWrites(t *testing.T) {
	user := Principal{Type: PrincipalTypeUser, ID: "u-1"}
	ctx, capture := WithPrincipalCapture(context.Background())
	if got, ok := capture.Principal(); ok || got != (Principal{}) {
		t.Fatalf("fresh capture = %+v, %v; want none", got, ok)
	}
	derived := WithPrincipal(context.WithValue(ctx, struct{}{}, "derived"), user)
	if got, ok := capture.Principal(); !ok || got != user {
		t.Fatalf("capture = %+v, %v; want %+v", got, ok, user)
	}
	if _, ok := PrincipalFromContext(ctx); ok {
		t.Fatal("WithPrincipal changed the captured context's own values")
	}
	if got, _ := PrincipalFromContext(derived); got != user {
		t.Fatalf("derived principal = %+v", got)
	}
}

func TestPrincipalCaptureLastWriteWinsIncludingClear(t *testing.T) {
	first := Principal{Type: PrincipalTypeUser, ID: "u-1"}
	second := Principal{Type: PrincipalTypeServiceAccount, ID: "sa-1"}
	ctx, capture := WithPrincipalCapture(context.Background())
	ctx = WithPrincipal(ctx, first)
	ctx = WithPrincipal(ctx, second)
	if got, ok := capture.Principal(); !ok || got != second {
		t.Fatalf("capture = %+v, %v; want the last write %+v", got, ok, second)
	}
	WithPrincipal(ctx, Principal{})
	if got, ok := capture.Principal(); ok || got != (Principal{}) {
		t.Fatalf("capture after a clear = %+v, %v; want none", got, ok)
	}
	WithPrincipal(ctx, Principal{Type: PrincipalTypeUser})
	if _, ok := capture.Principal(); ok {
		t.Fatal("an incomplete principal is reported")
	}
}

func TestPrincipalCaptureFreshPerCallAndChained(t *testing.T) {
	user := Principal{Type: PrincipalTypeUser, ID: "u-1"}
	outerCtx, outer := WithPrincipalCapture(context.Background())
	innerCtx, inner := WithPrincipalCapture(outerCtx)
	if inner == outer {
		t.Fatal("WithPrincipalCapture reused the enclosing capture")
	}
	WithPrincipal(innerCtx, user)
	for name, c := range map[string]*PrincipalCapture{"inner": inner, "outer": outer} {
		if got, ok := c.Principal(); !ok || got != user {
			t.Errorf("%s capture = %+v, %v; want write-through %+v", name, got, ok, user)
		}
	}
	WithPrincipal(innerCtx, Principal{})
	if _, ok := outer.Principal(); ok {
		t.Error("a clear did not write through to the enclosing capture")
	}

	// A write on the outer context reaches the outer capture only.
	sibling := Principal{Type: PrincipalTypeUser, ID: "u-2"}
	WithPrincipal(outerCtx, sibling)
	if got, _ := outer.Principal(); got != sibling {
		t.Errorf("outer = %+v, want %+v", got, sibling)
	}
	if _, ok := inner.Principal(); ok {
		t.Error("a write on the enclosing context reached the inner capture")
	}
}

func TestPrincipalCaptureStartsFromTheContextPrincipal(t *testing.T) {
	outerAttached := Principal{Type: PrincipalTypeUser, ID: "u-outer"}
	ctx := WithPrincipal(context.Background(), outerAttached)
	ctx, capture := WithPrincipalCapture(ctx)
	if got, ok := capture.Principal(); !ok || got != outerAttached {
		t.Fatalf("capture = %+v, %v; want the principal the context already carried", got, ok)
	}
	WithPrincipal(ctx, Principal{})
	if _, ok := capture.Principal(); ok {
		t.Fatal("a clear after the seed is not the last write")
	}
}

func TestPrincipalCaptureNoBleedAcrossUnitsOnOneBase(t *testing.T) {
	base, long := WithPrincipalCapture(context.Background())
	a := Principal{Type: PrincipalTypeUser, ID: "a"}
	b := Principal{Type: PrincipalTypeUser, ID: "b"}
	ctxA, captureA := WithPrincipalCapture(base)
	ctxB, captureB := WithPrincipalCapture(base)
	WithPrincipal(ctxA, a)
	WithPrincipal(ctxB, b)
	if got, _ := captureA.Principal(); got != a {
		t.Errorf("unit A = %+v, want %+v", got, a)
	}
	if got, _ := captureB.Principal(); got != b {
		t.Errorf("unit B = %+v, want %+v", got, b)
	}
	if got, _ := long.Principal(); got != b {
		t.Errorf("long-lived base capture = %+v; it records every unit, last write %+v", got, b)
	}
}

func TestPrincipalCaptureConcurrentWrites(t *testing.T) {
	ctx, capture := WithPrincipalCapture(context.Background())
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Go(func() {
			WithPrincipal(ctx, Principal{Type: PrincipalTypeUser, ID: string(rune('a' + i))})
			capture.Principal()
		})
	}
	wg.Wait()
	if _, ok := capture.Principal(); !ok {
		t.Fatal("no principal after concurrent writes")
	}
}
