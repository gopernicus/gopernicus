package sdk

import (
	"context"
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
