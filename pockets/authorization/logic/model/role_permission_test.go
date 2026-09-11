package model

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestRolePermissionOrderedShortCircuit(t *testing.T) {
	model, err := CompileRoleModel(RoleModel{ResourceTypes: map[string]RoleTypeDef{
		"doc": {Roles: []string{"z-admin", "a-editor"}, Permissions: map[string][]string{"edit": {"z-admin", "a-editor"}}},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	req := CheckRequest{Principal: PrincipalRef{Type: "user", ID: "u"}, Resource: Resource{Type: "doc", ID: "d"}, Permission: "edit"}
	failure := errors.New("role store unavailable")
	for _, granted := range []bool{true, false} {
		var calls []string
		result, err := EvaluateRolePermission(context.Background(), model, req, resolvedLimits(t, EvaluationLimits{}), func(_ context.Context, role string) (bool, error) {
			calls = append(calls, role)
			if role == "a-editor" {
				return granted, nil
			}
			return false, failure
		})
		if granted {
			if err != nil || !result.Allowed || !slices.Equal(calls, []string{"a-editor"}) {
				t.Fatalf("grant read an unnecessary failing branch: %+v %v calls=%v", result, err, calls)
			}
		} else if !errors.Is(err, failure) || result.Allowed || !slices.Equal(calls, []string{"a-editor", "z-admin"}) {
			t.Fatalf("required role read lost failure: %+v %v calls=%v", result, err, calls)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := EvaluateRolePermission(ctx, model, req, resolvedLimits(t, EvaluationLimits{}), func(context.Context, string) (bool, error) {
		t.Fatal("canceled role evaluation began a read")
		return true, nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func resolvedLimits(t *testing.T, in EvaluationLimits) EvaluationLimits {
	t.Helper()
	out, err := in.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	return out
}
