// Package access owns this host's principal admission policies.
package access

import (
	"context"
	"fmt"

	authenticationhttp "github.com/gopernicus/gopernicus/pockets/authentication/inbound/http"
	authorizationhttp "github.com/gopernicus/gopernicus/pockets/authorization/inbound/http"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	model "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

// Policy borrows the decision service. Every decision is made before a data call.
type Policy struct{ decisions *decisions.Service }

func New(decision *decisions.Service) *Policy { return &Policy{decisions: decision} }

// Manage admits a platform administrator or a direct/expanded owner of this scope.
// Global writes require platform administration. Both alternatives use one snapshot.
func (p *Policy) Manage(ctx context.Context, principal sdk.Principal, scope tuples.Scope) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	expr := decisions.On(model.Resource{Type: "platform", ID: "main"}, decisions.Direct("admin"))
	if scope.Kind == tuples.ResourceScope {
		expr = decisions.Any(expr, decisions.On(model.Resource{Type: scope.Type, ID: scope.ID}, decisions.Direct("owner")))
	}
	result, err := p.decisions.EvaluateResolved(ctx, model.PrincipalFrom(principal), expr, nil)
	if err != nil {
		return err
	}
	if !result.Allowed {
		return fmt.Errorf("auth-cms: access management requires platform admin or resource owner: %w", sdk.ErrForbidden)
	}
	return nil
}
func (p *Policy) RoleWrite(ctx context.Context, request authorizationhttp.RoleWriteRequest) error {
	return p.Manage(ctx, request.Principal, request.Scope)
}
func (p *Policy) PlatformAdmin(ctx context.Context, principal sdk.Principal) (bool, error) {
	return p.Can(ctx, principal, "admin", "platform", "main")
}
func (p *Policy) Can(ctx context.Context, principal sdk.Principal, permission, resourceType, resourceID string) (bool, error) {
	result, err := p.decisions.Check(ctx, model.CheckRequest{Principal: model.PrincipalFrom(principal), Permission: permission, Resource: model.Resource{Type: resourceType, ID: resourceID}})
	return result.Allowed, err
}
func (p *Policy) Invite(ctx context.Context, request authenticationhttp.InviteCheckRequest) error {
	expr := decisions.On(model.Resource{Type: "platform", ID: "main"}, decisions.Direct("admin"))
	if request.Action != authenticationhttp.InviteCreate || request.Relation != "owner" {
		expr = decisions.Any(expr, decisions.On(model.Resource{Type: request.ResourceType, ID: request.ResourceID}, decisions.Permission("manage_access")))
	}
	result, err := p.decisions.EvaluateResolved(ctx, model.PrincipalFrom(request.Principal), expr, nil)
	if err != nil {
		return err
	}
	if !result.Allowed {
		return fmt.Errorf("auth-cms: invitation management denied: %w", sdk.ErrForbidden)
	}
	return nil
}
