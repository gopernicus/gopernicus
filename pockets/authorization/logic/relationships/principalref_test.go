package relationships

import (
	"context"
	"errors"
	"testing"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/sdk"
)

// TestCheckRequestInvalidRejectedAtBoundary proves a malformed decision request
// is rejected at the Check boundary (fail closed) rather than silently denied.
func TestCheckRequestInvalidRejectedAtBoundary(t *testing.T) {
	svc := newTestService(t, &fakeStore{})

	bad := []authmodel.CheckRequest{
		{Principal: authmodel.PrincipalRef{}, Permission: "view", Resource: authmodel.Resource{Type: "post", ID: "p1"}},
		{Principal: authmodel.PrincipalRef{Type: "user", ID: "u1"}, Permission: "", Resource: authmodel.Resource{Type: "post", ID: "p1"}},
		{Principal: authmodel.PrincipalRef{Type: "user", ID: "u1"}, Permission: "view", Resource: authmodel.Resource{Type: "post", ID: ""}},
		{Principal: authmodel.PrincipalRef{Type: "user", ID: "u1"}, Permission: "view", Resource: authmodel.Resource{Type: "po\x00st", ID: "p1"}},
	}
	for i, req := range bad {
		if _, err := svc.Check(context.Background(), req); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("Check(bad %d): want sdk.ErrInvalidInput, got %v", i, err)
		}
	}

	// A well-formed request does not error at the boundary (deny is a result,
	// not an error).
	if _, err := svc.Check(context.Background(), authmodel.CheckRequest{
		Principal: authmodel.PrincipalRef{Type: "user", ID: "u1"}, Permission: "delete", Resource: authmodel.Resource{Type: "post", ID: "p1"},
	}); err != nil {
		t.Fatalf("well-formed Check errored: %v", err)
	}
}
