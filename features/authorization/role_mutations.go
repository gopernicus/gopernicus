package authorization

import (
	"context"
	"fmt"

	"github.com/gopernicus/gopernicus/sdk"
)

// The actor-facing, policy-guarded ROLE mutation lifecycle (AZ3-3.3). Each typed
// command builds one atomic mutation.Command and drives it through the guarded
// applyMutation seam — the host MutationGuard is evaluated INSIDE the repository's
// dependency-tracking ApplyGuarded boundary, so a denial never reaches Apply and no
// detached Check→Apply exists. There is no raw AssignRole/UnassignRole on Service:
// AZ3-3.4 removed the legacy unguarded pair and these guarded methods took the plain
// verbs (the trusted, actor-free counterparts live on SystemMutator).
//
// Roles stay OPAQUE (default #5): the core validates only structural subject, role,
// and scope shape — there is no known-role or allowed-assignment catalog here; that
// belongs to host policy or a future admin adapter. The subject is a concrete
// [PrincipalRef] (Type, ID); a userset subject needs a relation, which neither the
// command nor the underlying [RoleRow] can express, so userset role subjects are
// rejected STRUCTURALLY by the type, not by a runtime check.

// ErrHalfScopedRoleScope is returned when exactly one of ResourceType/ResourceID is
// set on a role command: a role assignment is either fully scoped (both set) or
// global (both empty). It wraps sdk.ErrInvalidInput and mirrors the roles service's
// own global-or-fully-scoped rule so the guarded path rejects a half-scoped pair
// before any write.
var ErrHalfScopedRoleScope = fmt.Errorf("authorization role scope: a role is global (both resource fields empty) or fully scoped (both set): %w", sdk.ErrInvalidInput)

// AssignRoleCommand grants a concrete principal an opaque role, either globally
// (both resource fields empty) or scoped to a resource (both set). A duplicate exact
// assignment is an idempotent no_change replay.
type AssignRoleCommand struct {
	MutationID       MutationID
	Subject          PrincipalRef // concrete principal — a userset subject is structurally impossible
	Role             string
	ResourceType     string // "" with empty ResourceID ⇒ a global assignment
	ResourceID       string
	ExpectedRevision *Revision
}

// UnassignRoleCommand removes a concrete principal's exact opaque-role assignment at
// the given scope. Unassigning an absent assignment is a committed not_found no-op,
// not an error.
type UnassignRoleCommand struct {
	MutationID       MutationID
	Subject          PrincipalRef
	Role             string
	ResourceType     string
	ResourceID       string
	ExpectedRevision *Revision
}

// UnassignRoleResult is the op-specific result of a guarded role unassign: the
// mutation Receipt plus SameRoleGrantRemains, the honest same_role_grant_remains
// annotation computed inside the repository's atomic critical section. On a SCOPED
// unassign, SameRoleGrantRemains is true iff a GLOBAL assignment for the same exact
// role still satisfies the scoped HasRole fallback — so a caller cannot mistake
// removal of one scoped row for removal of effective access to that role. It is a
// statement about THIS exact role grant via the global fallback only; it does not
// claim generic access remains, which a host may compose from other role/ReBAC
// rules. It is false for a global unassign and on replay.
type UnassignRoleResult struct {
	Receipt              *Receipt
	SameRoleGrantRemains bool
}

// AssignRole runs a guarded role assignment on behalf of actor. Its guard
// distinguishes a global assignment from a scoped one by the MutationAttempt's Scope
// (Kind ScopeSubject for global, ScopeResource for scoped) even though both share
// Operation OpRoleAssign — so a host can require broader authority for a global
// assignment, whose blast radius is larger than one resource, without a new
// Operation code.
func (s *Service) AssignRole(ctx context.Context, actor Actor, cmd AssignRoleCommand) (*Receipt, error) {
	if s.roles == nil {
		return nil, ErrRolesNotConfigured
	}
	command, err := assignRoleCommand(cmd)
	if err != nil {
		return nil, err
	}
	return s.applyMutation(ctx, actor, command)
}

// UnassignRole runs a guarded role unassignment on behalf of actor and returns the
// receipt together with the same_role_grant_remains annotation (see
// [UnassignRoleResult]). Like AssignRole the guard can distinguish a global unassign
// (ScopeSubject) from a scoped one (ScopeResource) by the attempt's Scope.
func (s *Service) UnassignRole(ctx context.Context, actor Actor, cmd UnassignRoleCommand) (UnassignRoleResult, error) {
	if s.roles == nil {
		return UnassignRoleResult{}, ErrRolesNotConfigured
	}
	command, err := unassignRoleCommand(cmd)
	if err != nil {
		return UnassignRoleResult{}, err
	}
	rcpt, err := s.applyMutation(ctx, actor, command)
	if err != nil {
		return UnassignRoleResult{}, err
	}
	return UnassignRoleResult{Receipt: rcpt, SameRoleGrantRemains: rcpt.SameRoleGrantRemains}, nil
}

// assignRoleCommand builds the actor-independent OpRoleAssign command. Shared by the
// guarded Service.AssignRole and the trusted SystemMutator.AssignRole. A half-scoped
// resource pair is rejected before any write.
func assignRoleCommand(cmd AssignRoleCommand) (Command, error) {
	scope, err := roleCommandScope(cmd.Subject, cmd.ResourceType, cmd.ResourceID)
	if err != nil {
		return Command{}, err
	}
	return Command{
		MutationID:       cmd.MutationID,
		Scope:            scope,
		ExpectedRevision: cmd.ExpectedRevision,
		Operation:        OpRoleAssign,
		Roles:            []RoleRow{{SubjectType: cmd.Subject.Type, SubjectID: cmd.Subject.ID, Role: cmd.Role}},
	}, nil
}

// unassignRoleCommand builds the actor-independent OpRoleUnassign command. Shared by
// the guarded Service.UnassignRole and the trusted SystemMutator.UnassignRole.
func unassignRoleCommand(cmd UnassignRoleCommand) (Command, error) {
	scope, err := roleCommandScope(cmd.Subject, cmd.ResourceType, cmd.ResourceID)
	if err != nil {
		return Command{}, err
	}
	return Command{
		MutationID:       cmd.MutationID,
		Scope:            scope,
		ExpectedRevision: cmd.ExpectedRevision,
		Operation:        OpRoleUnassign,
		Roles:            []RoleRow{{SubjectType: cmd.Subject.Type, SubjectID: cmd.Subject.ID, Role: cmd.Role}},
	}, nil
}

// roleCommandScope resolves a role command's revision anchor scope (default #3):
// resource scope for a scoped assignment, subject scope for a global one. A
// half-scoped resource pair (exactly one of type/id set) is rejected before any
// write; the empty pair is global. Command.Validate then enforces the rest of the
// structural shape (non-empty subject/role, and — for a subject scope — the row
// subject equalling the scope subject).
func roleCommandScope(subject PrincipalRef, resourceType, resourceID string) (ScopeKey, error) {
	if (resourceType == "") != (resourceID == "") {
		return ScopeKey{}, ErrHalfScopedRoleScope
	}
	if resourceType == "" {
		return ScopeKey{Kind: ScopeSubject, Type: subject.Type, ID: subject.ID}, nil
	}
	return ScopeKey{Kind: ScopeResource, Type: resourceType, ID: resourceID}, nil
}
