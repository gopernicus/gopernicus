package storetest

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/sdk"
)

// A global-role read addresses the checked principal, even when the caller's
// command target names another subject. Malformed targets still reject.
func specGlobalRoleIdentity(t *testing.T, newRepos func(*testing.T) authorization.Repositories) {
	r := newRepos(t)
	m := r.Mutations
	ctx := context.Background()
	actual := mutations.Target{Kind: mutations.TargetSubject, Type: "user", ID: "checked"}
	wrong := mutations.Target{Kind: mutations.TargetSubject, Type: "user", ID: "unrelated"}
	mustApply(t, m, mutations.Command{Target: actual, Operation: mutations.OpRoleAssign, Roles: []mutations.RoleRow{{SubjectType: "user", SubjectID: "checked", Role: "editor"}}})
	stop := errors.New("inspection only")
	result, err := m.ApplyGuarded(ctx, grant("d", "owner", "owner"), func(ctx context.Context, v mutations.StoreDecisionView) error {
		got, err := v.HasRole(ctx, wrong, "editor", "user", "checked")
		if err != nil || !got {
			t.Fatalf("global-role subject changed: %v %v", got, err)
		}
		for _, bad := range []mutations.Target{{}, {Kind: mutations.TargetResource, Type: "doc"}, {Kind: "invalid", Type: "user", ID: "checked"}} {
			if _, err := v.HasRole(ctx, bad, "editor", "user", "checked"); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("malformed target accepted: %+v %v", bad, err)
			}
		}
		return stop
	}, nil)
	if result != nil || !errors.Is(err, stop) {
		t.Fatalf("inspection changed state: %+v %v", result, err)
	}
}
