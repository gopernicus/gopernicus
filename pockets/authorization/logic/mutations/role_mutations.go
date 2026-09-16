package mutations

import (
	"context"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
)

func (s *Service) AssignRole(ctx context.Context, cmd AssignRoleCommand) (*Result, error) {
	c, err := assignRoleCommand(cmd)
	if err != nil {
		return nil, err
	}
	return s.Apply(ctx, c)
}
func (s *Service) UnassignRole(ctx context.Context, cmd UnassignRoleCommand) (UnassignRoleResult, error) {
	c, err := unassignRoleCommand(cmd)
	if err != nil {
		return UnassignRoleResult{}, err
	}
	r, err := s.Apply(ctx, c)
	if err != nil {
		return UnassignRoleResult{}, err
	}
	return UnassignRoleResult{Outcome: r.Outcome}, nil
}
func assignRoleCommand(cmd AssignRoleCommand) (Command, error) {
	target, err := roleCommandTarget(cmd.Subject, cmd.Scope)
	if err != nil {
		return Command{}, err
	}
	return Command{Target: target, Operation: OpRoleAssign, Roles: []RoleRow{{SubjectType: cmd.Subject.Type, SubjectID: cmd.Subject.ID, Role: cmd.Role}}}, nil
}
func unassignRoleCommand(cmd UnassignRoleCommand) (Command, error) {
	target, err := roleCommandTarget(cmd.Subject, cmd.Scope)
	if err != nil {
		return Command{}, err
	}
	return Command{Target: target, Operation: OpRoleUnassign, Roles: []RoleRow{{SubjectType: cmd.Subject.Type, SubjectID: cmd.Subject.ID, Role: cmd.Role}}}, nil
}
func roleCommandTarget(p authmodel.PrincipalRef, scope tuples.Scope) (Target, error) {
	if err := scope.Validate(); err != nil {
		return Target{}, err
	}
	if err := p.Validate(); err != nil {
		return Target{}, err
	}
	if scope.Kind == tuples.GlobalScope {
		return Target{Kind: TargetSubject, Type: p.Type, ID: p.ID}, nil
	}
	return resourceTarget(scope.Type, scope.ID), nil
}
