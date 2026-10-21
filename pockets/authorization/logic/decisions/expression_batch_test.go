package decisions

import (
	"errors"
	"testing"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

func TestEvaluateBatchIsOneSnapshotInRequestOrder(t *testing.T) {
	resource := authmodel.Resource{Type: "doc", ID: "one"}
	s, f := expressionService(t, exact(tuples.Global(), "editor"), exact(tuples.On("doc", "one"), "viewer"))
	got, err := s.EvaluateBatch(t.Context(), []ExpressionCheck{
		{Principal: expressionPrincipal, Expression: Role("editor")},
		{Principal: expressionPrincipal, Expression: RoleIn("viewer", resource)},
		{Principal: expressionPrincipal, Expression: Role("viewer")},
		{Principal: expressionPrincipal, Expression: Role("editor")},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []bool{true, true, false, true}
	if len(got) != len(want) {
		t.Fatalf("EvaluateBatch returned %d decisions", len(got))
	}
	for i := range want {
		if got[i].Allowed != want[i] {
			t.Fatalf("decision %d = %v, want %v", i, got[i].Allowed, want[i])
		}
	}
	if f.snapshotCalls != 1 {
		t.Fatalf("snapshots = %d, want one for the whole batch", f.snapshotCalls)
	}
}

func TestEvaluateBatchIsAllOrNothing(t *testing.T) {
	s, f := expressionService(t, exact(tuples.Global(), "editor"))
	got, err := s.EvaluateBatch(t.Context(), []ExpressionCheck{
		{Principal: expressionPrincipal, Expression: Role("editor")},
		{Principal: expressionPrincipal, Expression: Any()},
	})
	if !errors.Is(err, sdk.ErrInvalidInput) || got != nil {
		t.Fatalf("invalid item = %v, %v", got, err)
	}
	if f.snapshotCalls != 0 {
		t.Fatal("an invalid batch read the authority")
	}
	slot := BindResource("doc", "k", Direct("viewer"))
	if _, err := s.EvaluateBatch(t.Context(), []ExpressionCheck{{Principal: expressionPrincipal, Expression: slot}}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("slot accepted: %v", err)
	}
	f.fail = errors.New("store down")
	if got, err := s.EvaluateBatch(t.Context(), []ExpressionCheck{{Principal: expressionPrincipal, Expression: Role("editor")}}); err == nil || got != nil {
		t.Fatalf("store failure = %v, %v", got, err)
	}
}
