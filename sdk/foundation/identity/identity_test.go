package identity

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
)

// fakeResolver is a tiny in-test Resolver: it returns the Info registered for a
// Principal and the errs not-found class for anything else — the fail-closed
// contract a real Resolver honors.
type fakeResolver struct {
	infos map[Principal]Info
}

func (f fakeResolver) Resolve(_ context.Context, p Principal) (Info, error) {
	info, ok := f.infos[p]
	if !ok {
		return Info{}, fmt.Errorf("identity: resolve %s/%s: %w", p.Type, p.ID, sdk.ErrNotFound)
	}
	return info, nil
}

// Compile-time proof the fake satisfies the port.
var _ Resolver = fakeResolver{}

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

// ResolveAll returns Infos positionally aligned with the input principals.
func TestResolveAll_PositionalAlignment(t *testing.T) {
	alice := Principal{Type: User, ID: "u-alice"}
	bot := Principal{Type: ServiceAccount, ID: "sa-bot"}
	r := fakeResolver{infos: map[Principal]Info{
		alice: {Principal: alice, DisplayName: "Alice", Addresses: []Address{{Kind: KindEmail, Value: "alice@example.com"}}},
		bot:   {Principal: bot, DisplayName: "bot"},
	}}

	ps := []Principal{alice, bot}
	got, err := ResolveAll(context.Background(), r, ps)
	if err != nil {
		t.Fatalf("ResolveAll() err = %v, want nil", err)
	}
	if len(got) != len(ps) {
		t.Fatalf("ResolveAll() len = %d, want %d", len(got), len(ps))
	}
	for i, p := range ps {
		if got[i].Principal != p {
			t.Errorf("ResolveAll()[%d].Principal = %+v, want %+v", i, got[i].Principal, p)
		}
	}
	if got[0].DisplayName != "Alice" || got[1].DisplayName != "bot" {
		t.Errorf("ResolveAll() display names = %q, %q; want %q, %q",
			got[0].DisplayName, got[1].DisplayName, "Alice", "bot")
	}
}

// ResolveAll is STRICT: the first Resolve error aborts and is returned, and the
// returned slice is nil — no partial results leak through.
func TestResolveAll_StrictAbortOnFirstError(t *testing.T) {
	alice := Principal{Type: User, ID: "u-alice"}
	missing := Principal{Type: User, ID: "u-ghost"}
	present := Principal{Type: User, ID: "u-carol"}
	r := fakeResolver{infos: map[Principal]Info{
		alice:   {Principal: alice, DisplayName: "Alice"},
		present: {Principal: present, DisplayName: "Carol"},
	}}

	got, err := ResolveAll(context.Background(), r, []Principal{alice, missing, present})
	if err == nil {
		t.Fatal("ResolveAll() err = nil, want the not-found class")
	}
	if !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("ResolveAll() err = %v, want errors.Is sdk.ErrNotFound", err)
	}
	if got != nil {
		t.Errorf("ResolveAll() = %+v, want nil on strict abort", got)
	}
}

// An empty or nil input resolves to an empty slice and a nil error.
func TestResolveAll_EmptySlice(t *testing.T) {
	r := fakeResolver{}

	for _, ps := range [][]Principal{nil, {}} {
		got, err := ResolveAll(context.Background(), r, ps)
		if err != nil {
			t.Errorf("ResolveAll(%v) err = %v, want nil", ps, err)
		}
		if len(got) != 0 {
			t.Errorf("ResolveAll(%v) len = %d, want 0", ps, len(got))
		}
	}
}
