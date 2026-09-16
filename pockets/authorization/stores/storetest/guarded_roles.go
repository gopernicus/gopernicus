package storetest

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"

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
func specGuardedRoleRevokeRace(t *testing.T, newRepos func(*testing.T) Repositories, global bool) {
	repos := newRepos(t)
	ctx := context.Background()
	permissionScope := mutations.Target{Kind: mutations.TargetResource, Type: "doc", ID: "authority"}
	mustApply(t, repos.Mutations, grant(permissionScope.ID, "owner", "anchor"))
	guard := &rolePermissionGuard{scope: &permissionScope}
	components, err := authorization.New(authorization.Repositories{Tuples: repos.Tuples, Mutations: repos.Mutations}, authorization.WithGuard(guard), authorization.WithModel(guardedRolePolicyModel()))
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
		command := mutations.AssignRoleCommand{
			Role: "viewer", Subject: authmodel.PrincipalRef{Type: "user", ID: "recipient"}, Scope: fixtureScope("doc", "target-"+strconv.Itoa(round)),
		}
		mustApply(t, repos.Mutations, grant(command.Scope.ID, "owner", "anchor"))
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
		written, err := repos.Tuples.Contains(ctx, roleFact("user", "recipient", "viewer", command.Scope.Type, command.Scope.ID))
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

func specGuardedRolePermission(t *testing.T, newRepos func(*testing.T) Repositories, mode string) {
	repos := newRepos(t)
	ctx := context.Background()
	scope := mutations.Target{Kind: mutations.TargetResource, Type: "doc", ID: "role-guard"}
	global := mutations.Target{Kind: mutations.TargetSubject, Type: "user", ID: "actor"}
	mustApply(t, repos.Mutations, grant(scope.ID, "owner", "anchor"))
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
	components, err := authorization.New(authorization.Repositories{Tuples: repos.Tuples, Mutations: repos.Mutations}, authorization.WithGuard(guard), authorization.WithModel(guardedRolePolicyModel()))
	if err != nil {
		t.Fatal(err)
	}
	principal := authmodel.PrincipalRef{Type: "user", ID: "actor"}
	want, err := components.Decisions.Check(ctx, authmodel.CheckRequest{Principal: principal, Permission: "manage", Resource: authmodel.Resource{Type: scope.Type, ID: scope.ID}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = components.Mutations.AssignRole(ctx, mutations.Actor{PrincipalRef: principal}, mutations.AssignRoleCommand{
		Role: "viewer", Subject: authmodel.PrincipalRef{Type: "user", ID: "recipient"}, Scope: fixtureScope(scope.Type, scope.ID),
	})
	if want.Allowed {
		if err != nil {
			t.Fatalf("read-side allow disagreed with guarded permission: %v", err)
		}
	} else if !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("read-side deny disagreed with guarded permission: %v", err)
	}
	written, err := repos.Tuples.Contains(ctx, roleFact("user", "recipient", "viewer", scope.Type, scope.ID))
	if err != nil || written != want.Allowed {
		t.Fatalf("guarded write did not match decision: written=%v err=%v", written, err)
	}
}

// Global applicability is a declared branch, not an implicit role fallback.
func guardedRolePolicyModel() decisions.Model {
	return decisions.Model{ResourceTypes: map[string]decisions.ResourceTypeDef{"doc": {Permissions: map[string]decisions.Expression{"manage": decisions.Any(decisions.Role("editor"), decisions.RoleIn("editor")), "view": decisions.RoleIn("viewer")}}}}
}

func specCrossFacadeGuardian(t *testing.T, factory func(*testing.T) Repositories) {
	r := factory(t)
	owner := mutations.Command{Target: resTarget("d"), Operation: mutations.OpRoleAssign, Roles: []mutations.RoleRow{{SubjectType: "user", SubjectID: "u", Role: "owner"}}}
	mustApply(t, r.Mutations, owner)
	if result := mustApply(t, r.Mutations, grant("d", "owner", "u")); result.Outcome != mutations.OutcomeNoChange {
		t.Fatalf("second facade created a second owner: %+v", result)
	}
	if n, err := r.Relationships.CountByResourceAndRelation(t.Context(), "doc", "d", "owner"); err != nil || n != 1 {
		t.Fatalf("canonical guardian count: %d/%v", n, err)
	}
	owner.Operation = mutations.OpRoleUnassign
	mustReject(t, r.Mutations, owner, mutations.ErrInvariantBlocked)
	mustReject(t, r.Mutations, revoke("d", "owner", "u"), mutations.ErrInvariantBlocked)
	mustReject(t, r.Mutations, swap("d", "owner", "viewer", "u"), mutations.ErrInvariantBlocked)
	second := owner
	second.Operation = mutations.OpRoleAssign
	second.Roles = []mutations.RoleRow{{SubjectType: "user", SubjectID: "other", Role: "owner"}}
	mustApply(t, r.Mutations, second)
	mustApply(t, r.Mutations, swap("d", "owner", "viewer", "u"))
	if !hasExact(t, r.Tuples, "user", "u", "viewer", "doc", "d") || hasExact(t, r.Tuples, "user", "u", "owner", "doc", "d") {
		t.Fatal("atomic swap did not publish its exact delta")
	}
	second.Operation = mutations.OpRoleUnassign
	mustReject(t, r.Mutations, second, mutations.ErrInvariantBlocked)
}
