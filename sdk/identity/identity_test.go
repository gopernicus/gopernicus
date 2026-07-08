package identity

import (
	"context"
	"testing"
)

// The constants are a wire-adjacent convention (they match the ReBAC Subject
// vocabulary an authorizer reads unadapted), so their literal values are locked.
func TestConstantValues(t *testing.T) {
	if User != "user" {
		t.Errorf("User = %q, want %q", User, "user")
	}
	if ServiceAccount != "service_account" {
		t.Errorf("ServiceAccount = %q, want %q", ServiceAccount, "service_account")
	}
}

func TestWithPrincipal_RoundTrip(t *testing.T) {
	want := Principal{Type: User, ID: "u-123"}
	ctx := WithPrincipal(context.Background(), want)

	got, ok := FromContext(ctx)
	if !ok {
		t.Fatal("FromContext() ok = false, want true for a stashed principal")
	}
	if got != want {
		t.Errorf("FromContext() = %+v, want %+v", got, want)
	}
}

// A zero-valued (empty-ID) Principal reports false even when explicitly stashed:
// absence must fail closed regardless of how the empty value arrived.
func TestFromContext_ZeroValuePrincipalReportsFalse(t *testing.T) {
	ctx := WithPrincipal(context.Background(), Principal{})

	if _, ok := FromContext(ctx); ok {
		t.Error("FromContext() ok = true for a zero-value principal, want false")
	}
}

// A context that never carried an identity fails closed.
func TestFromContext_AbsentReportsFalse(t *testing.T) {
	if _, ok := FromContext(context.Background()); ok {
		t.Error("FromContext() ok = true for an absent principal, want false")
	}
}
