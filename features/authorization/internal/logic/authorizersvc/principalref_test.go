package authorizersvc

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
)

// TestPrincipalRefInvalid proves empty and malformed decision callers are
// rejected and well-formed ones accepted. A PrincipalRef structurally cannot
// carry a userset relation — there is no field for one.
func TestPrincipalRefInvalid(t *testing.T) {
	cases := []struct {
		name    string
		ref     PrincipalRef
		wantErr bool
	}{
		{"ok", PrincipalRef{Type: "user", ID: "u1"}, false},
		{"empty type", PrincipalRef{ID: "u1"}, true},
		{"empty id", PrincipalRef{Type: "user"}, true},
		{"both empty", PrincipalRef{}, true},
		{"control char", PrincipalRef{Type: "user", ID: "u\x01"}, true},
		{"invalid utf8", PrincipalRef{Type: "\xff", ID: "u1"}, true},
		{"over long", PrincipalRef{Type: "user", ID: strings.Repeat("x", 300)}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.ref.Validate()
			if tc.wantErr {
				if !errors.Is(err, sdk.ErrInvalidInput) {
					t.Fatalf("Validate(%+v) = %v, want sdk.ErrInvalidInput", tc.ref, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate(%+v) = %v, want nil", tc.ref, err)
			}
		})
	}
}

// TestCheckRequestInvalidRejectedAtBoundary proves a malformed decision request
// is rejected at the Check boundary (fail closed) rather than silently denied.
func TestCheckRequestInvalidRejectedAtBoundary(t *testing.T) {
	svc := newTestService(t, &fakeStore{}, cryptids.IDGenerator{})

	bad := []CheckRequest{
		{Principal: PrincipalRef{}, Permission: "view", Resource: Resource{Type: "post", ID: "p1"}},
		{Principal: PrincipalRef{Type: "user", ID: "u1"}, Permission: "", Resource: Resource{Type: "post", ID: "p1"}},
		{Principal: PrincipalRef{Type: "user", ID: "u1"}, Permission: "view", Resource: Resource{Type: "post", ID: ""}},
		{Principal: PrincipalRef{Type: "user", ID: "u1"}, Permission: "view", Resource: Resource{Type: "po\x00st", ID: "p1"}},
	}
	for i, req := range bad {
		if _, err := svc.Check(context.Background(), req); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("Check(bad %d): want sdk.ErrInvalidInput, got %v", i, err)
		}
	}

	// A well-formed request does not error at the boundary (deny is a result,
	// not an error).
	if _, err := svc.Check(context.Background(), CheckRequest{
		Principal: PrincipalRef{Type: "user", ID: "u1"}, Permission: "delete", Resource: Resource{Type: "post", ID: "p1"},
	}); err != nil {
		t.Fatalf("well-formed Check errored: %v", err)
	}
}
