package storetest

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
)

func specCallbackCancellation(t *testing.T, newRepos func(*testing.T) authorization.Repositories, inGuard bool) {
	repos := newRepos(t)
	m := repos.Mutations
	mustApply(t, m, grant("d1", "owner", "u1"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := grant("d1", "viewer", "u2")
	var guard mutations.Guard
	var validate mutations.SemanticValidator
	if inGuard {
		guard = func(context.Context, mutations.StoreDecisionView) error { cancel(); return nil }
	} else {
		validate = func(mutations.Command) error { cancel(); return nil }
	}
	result, err := m.ApplyGuarded(ctx, cmd, guard, validate)
	if !errors.Is(err, context.Canceled) || result != nil {
		t.Fatalf("callback cancellation: want nil result and context.Canceled; result=%+v err=%v", result, err)
	}
	if ok, err := repos.Relationships.CheckRelationExists(context.Background(), "doc", "d1", "viewer", "user", "u2"); err != nil || ok {
		t.Fatalf("canceled write became visible: %v, %v", ok, err)
	}
	result = mustApply(t, m, cmd)
	if result.Outcome != mutations.OutcomeApplied {
		t.Fatalf("canceled write was not safely retriable: %+v", result)
	}
}
