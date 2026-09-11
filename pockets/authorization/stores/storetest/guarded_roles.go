package storetest

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/sdk"
)

type rolePermissionGuard struct {
	scope *mutations.Target
}

func (g *rolePermissionGuard) AuthorizeMutation(ctx context.Context, attempt mutations.MutationAttempt, view mutations.DecisionView) error {
	scope := attempt.Target
	if g.scope != nil {
		scope = *g.scope
	}
	result, err := view.Check(ctx, authmodel.CheckRequest{Principal: attempt.Actor.PrincipalRef, Permission: "manage", Resource: authmodel.Resource{Type: scope.Type, ID: scope.ID}})
	ok := result.Allowed
	if err != nil {
		return err
	}
	if !ok {
		return sdk.ErrForbidden
	}
	return nil
}

// A role revoke commits, and a racing guarded write either precedes it or
// aborts cleanly under the store's atomic serialization boundary.
func specGuardedRoleRevokeRace(t *testing.T, newRepos func(*testing.T) authorization.Repositories, global bool) {
	repos := newRepos(t)
	ctx := context.Background()
	permissionScope := mutations.Target{Kind: mutations.TargetResource, Type: "doc", ID: "authority"}
	guard := &rolePermissionGuard{scope: &permissionScope}
	components, err := authorization.New(authorization.Repositories{Roles: repos.Roles, Mutations: repos.Mutations}, authorization.WithGuard(guard), authorization.WithRoleModel(authmodel.RoleModel{ResourceTypes: map[string]authmodel.RoleTypeDef{
		"doc": {Roles: []string{"editor", "viewer"}, Permissions: map[string][]string{"manage": {"editor"}, "view": {"viewer"}}},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 8; round++ {
		principal := authmodel.PrincipalRef{Type: "user", ID: "actor-" + strconv.Itoa(round)}
		authority := permissionScope
		if global {
			authority = mutations.Target{Kind: mutations.TargetSubject, Type: principal.Type, ID: principal.ID}
		}
		seed := mutations.Command{Target: authority, Operation: mutations.OpRoleAssign,
			Roles: []mutations.RoleRow{{SubjectType: principal.Type, SubjectID: principal.ID, Role: "editor"}}}
		mustApply(t, repos.Mutations, seed)
		revoke := seed
		revoke.Operation = mutations.OpRoleUnassign
		command := mutations.AssignRoleCommand{ResourceType: "doc", ResourceID: "target-" + strconv.Itoa(round),
			Role: "viewer", Subject: authmodel.PrincipalRef{Type: "user", ID: "recipient"}}
		var guarded, revoked *mutations.Result
		var guardedErr, revokeErr error
		var wg sync.WaitGroup
		start := make(chan struct{})
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			guarded, guardedErr = components.Mutations.AssignRole(ctx, mutations.Actor{PrincipalRef: principal}, command)
		}()
		go func() {
			defer wg.Done()
			<-start
			revoked, revokeErr = repos.Mutations.Apply(ctx, revoke, nil)
		}()
		close(start)
		wg.Wait()
		if revokeErr != nil || revoked == nil || revoked.Outcome != mutations.OutcomeApplied {
			t.Fatalf("role revoke: %+v %v", revoked, revokeErr)
		}
		written, err := repos.Roles.HasExactRole(ctx, "user", "recipient", "viewer", command.ResourceType, command.ResourceID)
		if err != nil {
			t.Fatal(err)
		}
		if guardedErr == nil {
			if guarded == nil || guarded.Outcome != mutations.OutcomeApplied || !written {
				t.Fatalf("successful guard did not commit: %+v written=%v", guarded, written)
			}
		} else if (!errors.Is(guardedErr, sdk.ErrConflict) && !errors.Is(guardedErr, sdk.ErrForbidden)) || guarded != nil || written {
			t.Fatalf("guard did not abort cleanly: %+v %v written=%v", guarded, guardedErr, written)
		}
		// After revocation, the same permission is denied when the same command is submitted again.
		if _, err := components.Mutations.AssignRole(ctx, mutations.Actor{PrincipalRef: principal}, command); !errors.Is(err, sdk.ErrForbidden) {
			t.Fatalf("revoked actor retained guarded authority: %v", err)
		}
	}
}

func specGuardedRolePermission(t *testing.T, newRepos func(*testing.T) authorization.Repositories, mode string) {
	repos := newRepos(t)
	ctx := context.Background()
	scope := mutations.Target{Kind: mutations.TargetResource, Type: "doc", ID: "role-guard"}
	global := mutations.Target{Kind: mutations.TargetSubject, Type: "user", ID: "actor"}
	seedRole := func(scope mutations.Target, name string) {
		mustApply(t, repos.Mutations, mutations.Command{Target: scope,
			Operation: mutations.OpRoleAssign, Roles: []mutations.RoleRow{{SubjectType: "user", SubjectID: "actor", Role: name}}})
	}
	if mode == "scoped" || mode == "both" {
		seedRole(scope, "editor")
	}
	if mode == "global" || mode == "both" {
		seedRole(global, "editor")
	}
	if mode == "removed_from_model" {
		seedRole(scope, "legacy-admin")
		seedRole(global, "legacy-admin")
	}
	guard := &rolePermissionGuard{}
	components, err := authorization.New(authorization.Repositories{Roles: repos.Roles, Mutations: repos.Mutations}, authorization.WithGuard(guard), authorization.WithRoleModel(authmodel.RoleModel{ResourceTypes: map[string]authmodel.RoleTypeDef{
		"doc": {Roles: []string{"editor", "viewer"}, Permissions: map[string][]string{"manage": {"editor"}, "view": {"viewer"}}},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	principal := authmodel.PrincipalRef{Type: "user", ID: "actor"}
	want, err := components.Decisions.Check(ctx, authmodel.CheckRequest{Principal: principal, Permission: "manage", Resource: authmodel.Resource{Type: scope.Type, ID: scope.ID}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = components.Mutations.AssignRole(ctx, mutations.Actor{PrincipalRef: principal}, mutations.AssignRoleCommand{
		ResourceType: scope.Type, ResourceID: scope.ID, Role: "viewer", Subject: authmodel.PrincipalRef{Type: "user", ID: "recipient"},
	})
	if want.Allowed {
		if err != nil {
			t.Fatalf("read-side allow disagreed with guarded permission: %v", err)
		}
	} else if !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("read-side deny disagreed with guarded permission: %v", err)
	}
	written, err := repos.Roles.HasExactRole(ctx, "user", "recipient", "viewer", scope.Type, scope.ID)
	if err != nil || written != want.Allowed {
		t.Fatalf("guarded write did not match decision: written=%v err=%v", written, err)
	}
}
