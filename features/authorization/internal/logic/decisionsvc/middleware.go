package decisionsvc

import (
	"github.com/gopernicus/gopernicus/features/authorization/internal/logic/authorizersvc"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// gates is the composite's gate family: it checks through the dispatching
// decision surface and declares against BOTH models. It is the shared
// authorizersvc.Gates body, so a composite-mounted gate walks the identical
// 401/403/500/503 ladder the relationship engine's own gates do — there is no
// second gate implementation to drift from.
func (c *Composite) gates() authorizersvc.Gates { return authorizersvc.NewGates(c, c) }

// RequirePermission gates a route on the composite's Check — the pair's owning
// model decides. See authorizersvc.Gates.RequirePermission for the full HTTP
// ladder (fail closed, FS9 bodies, no bypass hook).
func (c *Composite) RequirePermission(permission string, resource authorizersvc.ResourceResolver) web.Middleware {
	return c.gates().RequirePermission(permission, resource)
}

// RequirePermissionOn is RequirePermission in coordinates, with the pair checked
// at REGISTRATION against both models: a pair neither the relationship Schema nor
// the RoleModel declares panics when the route is mounted.
func (c *Composite) RequirePermissionOn(resourceType, permission, pathParam string) web.Middleware {
	return c.gates().RequirePermissionOn(resourceType, permission, pathParam)
}

// RequirePermissionFixed is the coordinate form over one named resource, with the
// same registration-time legality check as RequirePermissionOn.
func (c *Composite) RequirePermissionFixed(resourceType, permission, resourceID string) web.Middleware {
	return c.gates().RequirePermissionFixed(resourceType, permission, resourceID)
}
