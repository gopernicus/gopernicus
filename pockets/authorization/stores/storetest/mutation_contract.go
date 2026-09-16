package storetest

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/sdk"
)

// A global-role read addresses the checked principal, even when the caller's
// command target names another subject. Malformed targets still reject.
func specGlobalRoleIdentity(t *testing.T, newRepos func(*testing.T) Repositories) {
	r := newRepos(t)
	m := r.Mutations
	ctx := context.Background()
	actual := mutations.Target{Kind: mutations.TargetSubject, Type: "user", ID: "checked"}

	mustApply(t, m, mutations.Command{Target: actual, Operation: mutations.OpRoleAssign, Roles: []mutations.RoleRow{{SubjectType: "user", SubjectID: "checked", Role: "editor"}}})
	stop := errors.New("inspection only")
	result, err := m.ApplyGuarded(ctx, grant("d", "owner", "owner"), func(ctx context.Context, v mutations.StoreDecisionView) error {
		got, err := v.Contains(ctx, roleFact("user", "checked", "editor", "", ""))
		if err != nil || !got {
			t.Fatalf("global-role subject changed: %v %v", got, err)
		}
		if _, err := v.Contains(ctx, tuples.Tuple{}); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("invalid canonical probe accepted: %v", err)
		}
		return stop
	}, nil)
	if result != nil || !errors.Is(err, stop) {
		t.Fatalf("inspection changed state: %+v %v", result, err)
	}
}
