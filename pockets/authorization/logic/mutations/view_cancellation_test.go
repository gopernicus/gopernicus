package mutations

import (
	"context"
	"errors"
	"testing"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
)

type cancelingFactView struct {
	StoreDecisionView
	cancel context.CancelFunc
	held   bool
	calls  int
}

func (v *cancelingFactView) HasRole(context.Context, Target, string, string, string) (bool, error) {
	v.calls++
	v.cancel()
	return v.held, nil
}

func (v *cancelingFactView) CheckRelationBounded(context.Context, Target, string, string, string, int) (bool, error) {
	v.calls++
	v.cancel()
	return v.held, nil
}

func TestDecisionViewRawFactsObserveSuccessfulReadCancellation(t *testing.T) {
	limits, err := (authmodel.EvaluationLimits{}).Resolve()
	if err != nil {
		t.Fatal(err)
	}
	principal := authmodel.PrincipalRef{Type: "user", ID: "u"}
	resource := authmodel.Resource{Type: "doc", ID: "d"}
	methods := []struct {
		name string
		read func(context.Context, DecisionView) (bool, error)
	}{
		{"global role", func(ctx context.Context, view DecisionView) (bool, error) {
			return view.HasGlobalRole(ctx, principal, "owner")
		}},
		{"resource role", func(ctx context.Context, view DecisionView) (bool, error) {
			return view.HasRole(ctx, principal, "owner", resource)
		}},
		{"relationship", func(ctx context.Context, view DecisionView) (bool, error) {
			return view.CheckRelation(ctx, principal, "owner", resource)
		}},
	}
	for _, method := range methods {
		for _, held := range []bool{false, true} {
			state := "absent"
			if held {
				state = "held"
			}
			t.Run(method.name+"/"+state, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				store := &cancelingFactView{cancel: cancel, held: held}
				view := permissionView{store: store, limits: limits}
				result, err := method.read(ctx, view)
				if result || !errors.Is(err, context.Canceled) || store.calls != 1 {
					t.Fatalf("canceled successful primitive became a fact: result=%t err=%v reads=%d", result, err, store.calls)
				}
				// Once canceled, the same API must not start another primitive read.
				if result, err := method.read(ctx, view); result || !errors.Is(err, context.Canceled) || store.calls != 1 {
					t.Fatalf("read after cancellation: result=%t err=%v reads=%d", result, err, store.calls)
				}
			})
		}
	}
}
