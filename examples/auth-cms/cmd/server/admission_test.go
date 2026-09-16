package main

import (
	"context"

	access "github.com/gopernicus/gopernicus/examples/auth-cms/pockets/access/inbound"
	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

// These helpers exercise the host's inbound admission followed by its data call.
// Production bundled role handlers perform the same sequence in inbound/http.
func admittedContext(ctx context.Context, c authorization.Components, p sdk.Principal, scope tuples.Scope) (context.Context, error) {
	if err := access.New(c.Decisions).Manage(ctx, p, scope); err != nil {
		return nil, err
	}
	return audit.WithSource(ctx, audit.Source{ActorType: p.Type, ActorID: p.ID}), nil
}
func admitGrantRelationship(ctx context.Context, c authorization.Components, p sdk.Principal, cmd mutations.GrantRelationshipCommand) (*mutations.Result, error) {
	ctx, err := admittedContext(ctx, c, p, tuples.On(cmd.ResourceType, cmd.ResourceID))
	if err != nil {
		return nil, err
	}
	return c.Mutations.GrantRelationship(ctx, cmd)
}
func admitRevokeRelationship(ctx context.Context, c authorization.Components, p sdk.Principal, cmd mutations.RevokeRelationshipCommand) (*mutations.Result, error) {
	ctx, err := admittedContext(ctx, c, p, tuples.On(cmd.ResourceType, cmd.ResourceID))
	if err != nil {
		return nil, err
	}
	return c.Mutations.RevokeRelationship(ctx, cmd)
}
func admitAssignRole(ctx context.Context, c authorization.Components, p sdk.Principal, cmd mutations.AssignRoleCommand) (*mutations.Result, error) {
	ctx, err := admittedContext(ctx, c, p, cmd.Scope)
	if err != nil {
		return nil, err
	}
	return c.Mutations.AssignRole(ctx, cmd)
}
func admitUnassignRole(ctx context.Context, c authorization.Components, p sdk.Principal, cmd mutations.UnassignRoleCommand) (mutations.UnassignRoleResult, error) {
	ctx, err := admittedContext(ctx, c, p, cmd.Scope)
	if err != nil {
		return mutations.UnassignRoleResult{}, err
	}
	return c.Mutations.UnassignRole(ctx, cmd)
}
