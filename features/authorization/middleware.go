package authorization

import (
	"github.com/gopernicus/gopernicus/features/authorization/internal/logic/authorizersvc"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// ResourceResolver extracts the resource a RequirePermission gate authorizes
// against. Hosts that need a request-derived resource write their own; the
// bundled FixedResource covers the fixed-resource case.
type ResourceResolver = authorizersvc.ResourceResolver

// FixedResource always resolves the same resource, ignoring the request.
var FixedResource = authorizersvc.FixedResource

// PathResource resolves resourceType:<value of the named path parameter>; an
// empty value fails the gate closed (500).
var PathResource = authorizersvc.PathResource

// RequirePermission returns web.Middleware gating a route on the context
// Principal holding permission on the resolved resource. It is a thin delegation
// to the composite decision surface — this root package writes NO HTTP; the
// 401/403/500/503 responses (FS9 web.Error shape) live in the one shared gate
// body in authorizersvc.
//
// PURE Check, no bypass hook: a host wanting platform-admin/self-access composes
// those recipes as its OWN closure around this middleware (auth-cms's
// isPlatformAdmin is the flagship demonstration). D-D: it fails CLOSED (Check
// error → 500), the deliberate opposite of ratelimiter.Middleware's fail-open.
//
// RequirePermission needs a MODEL-BEARING kind — Config.RelationshipModel for the
// relationship kind or Config.RoleModel for the roles kind — and panics when
// neither is wired. The panic fires at REGISTRATION/BOOT time — when the host
// mounts the builder at route registration, before serving traffic — so a
// modelless host learns of the misconfiguration loudly instead of 500ing every
// gated request. Mount it at registration, not lazily.
func (s *Service) RequirePermission(permission string, resource ResourceResolver) web.Middleware {
	if s.decider == nil {
		panic("authorization: RequirePermission requires a decision-capable kind (Config.RelationshipModel or Config.RoleModel); a roles-only host without a role model must not mount it")
	}
	return s.decider.RequirePermission(permission, resource)
}

// RequirePermissionOn is RequirePermission in coordinates — (resourceType,
// permission, pathParam) — so a route line reads as its own authorization
// question. The pair is checked against BOTH compiled models at REGISTRATION: a
// pair neither declares, or an empty parameter name, panics when the route is
// mounted. Same model-bearing-kind precondition as RequirePermission.
func (s *Service) RequirePermissionOn(resourceType, permission, pathParam string) web.Middleware {
	if s.decider == nil {
		panic("authorization: RequirePermissionOn requires a decision-capable kind (Config.RelationshipModel or Config.RoleModel); a roles-only host without a role model must not mount it")
	}
	return s.decider.RequirePermissionOn(resourceType, permission, pathParam)
}

// RequirePermissionFixed is the coordinate form over one named resource, with
// the same registration-time legality check as RequirePermissionOn.
func (s *Service) RequirePermissionFixed(resourceType, permission, resourceID string) web.Middleware {
	if s.decider == nil {
		panic("authorization: RequirePermissionFixed requires a decision-capable kind (Config.RelationshipModel or Config.RoleModel); a roles-only host without a role model must not mount it")
	}
	return s.decider.RequirePermissionFixed(resourceType, permission, resourceID)
}
