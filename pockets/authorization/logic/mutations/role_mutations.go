package mutations

import (
	"context"
	"fmt"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/sdk"
)

// Role additions use the current RoleModel when configured. Removals stay
// available after a role is removed from the model. Subjects are concrete principals.

// ErrHalfScopedRoleScope is returned when exactly one of ResourceType/ResourceID is
// set on a role command: a role assignment is either fully scoped (both set) or
// global (both empty). It wraps sdk.ErrInvalidInput and mirrors the roles service's
// own global-or-fully-scoped rule so the guarded path rejects a half-scoped pair
// before any write.
var ErrHalfScopedRoleScope = fmt.Errorf("authorization role scope: a role is global (both resource fields empty) or fully scoped (both set): %w", sdk.ErrInvalidInput)

// AssignRole runs a guarded role assignment on behalf of actor. Its guard
// distinguishes a global assignment from a scoped one by the MutationAttempt's Target
// (Kind TargetSubject for global, TargetResource for scoped) even though both share
// Operation OpRoleAssign — so a host can require broader authority for a global
// assignment, whose blast radius is larger than one resource, without a new
// Operation code.
func (s *Service) AssignRole(ctx context.Context, actor Actor, cmd AssignRoleCommand) (*Result, error) {
	if s.roles == nil {
		return nil, roles.ErrRolesNotConfigured
	}
	command, err := assignRoleCommand(cmd)
	if err != nil {
		return nil, err
	}
	return s.applyMutation(ctx, actor, command)
}

// UnassignRole runs a guarded role unassignment on behalf of actor and returns the
// outcome together with the same_role_grant_remains annotation (see
// [UnassignRoleResult]). Like AssignRole the guard can distinguish a global unassign
// (TargetSubject) from a scoped one (TargetResource) by the attempt's Target.
func (s *Service) UnassignRole(ctx context.Context, actor Actor, cmd UnassignRoleCommand) (UnassignRoleResult, error) {
	if s.roles == nil {
		return UnassignRoleResult{}, roles.ErrRolesNotConfigured
	}
	command, err := unassignRoleCommand(cmd)
	if err != nil {
		return UnassignRoleResult{}, err
	}
	result, err := s.applyMutation(ctx, actor, command)
	if err != nil {
		return UnassignRoleResult{}, err
	}
	return UnassignRoleResult{Outcome: result.Outcome, SameRoleGrantRemains: result.SameRoleGrantRemains}, nil
}

// assignRoleCommand builds the actor-independent OpRoleAssign command. Shared by the
// guarded Service.AssignRole and the trusted SystemMutator.AssignRole. A half-scoped
// resource pair is rejected before any write.
func assignRoleCommand(cmd AssignRoleCommand) (Command, error) {
	scope, err := roleCommandTarget(cmd.Subject, cmd.ResourceType, cmd.ResourceID)
	if err != nil {
		return Command{}, err
	}
	return Command{

		Target: scope,

		Operation: OpRoleAssign,
		Roles:     []RoleRow{{SubjectType: cmd.Subject.Type, SubjectID: cmd.Subject.ID, Role: cmd.Role}},
	}, nil
}

// unassignRoleCommand builds the actor-independent OpRoleUnassign command. Shared by
// the guarded Service.UnassignRole and the trusted SystemMutator.UnassignRole.
func unassignRoleCommand(cmd UnassignRoleCommand) (Command, error) {
	scope, err := roleCommandTarget(cmd.Subject, cmd.ResourceType, cmd.ResourceID)
	if err != nil {
		return Command{}, err
	}
	return Command{

		Target: scope,

		Operation: OpRoleUnassign,
		Roles:     []RoleRow{{SubjectType: cmd.Subject.Type, SubjectID: cmd.Subject.ID, Role: cmd.Role}},
	}, nil
}

// roleCommandTarget resolves a role command's target:
// resource scope for a scoped assignment, subject scope for a global one. A
// half-scoped resource pair (exactly one of type/id set) is rejected before any
// write; the empty pair is global. Command.Validate then enforces the rest of the
// structural shape (non-empty subject/role, and — for a subject scope — the row
// subject equalling the scope subject).
func roleCommandTarget(subject authmodel.PrincipalRef, resourceType, resourceID string) (Target, error) {
	if (resourceType == "") != (resourceID == "") {
		return Target{}, ErrHalfScopedRoleScope
	}
	if resourceType == "" {
		return Target{Kind: TargetSubject, Type: subject.Type, ID: subject.ID}, nil
	}
	return Target{Kind: TargetResource, Type: resourceType, ID: resourceID}, nil
}
