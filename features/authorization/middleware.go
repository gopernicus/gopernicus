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

// RequirePermission returns web.Middleware gating a route on the context
// Principal holding permission on the resolved resource. It is a thin
// re-export of the internal engine implementation — this root package writes NO
// HTTP; the 401/403/500 responses (FS9 web.Error shape) live in authorizersvc.
//
// PURE Check, no bypass hook: a host wanting platform-admin/self-access composes
// those recipes as its OWN closure around this middleware (auth-cms's
// isPlatformAdmin is the flagship demonstration). D-D: it fails CLOSED (Check
// error → 500), the deliberate opposite of ratelimiter.Middleware's fail-open.
//
// RequirePermission needs the relationship kind wired: it panics if
// Repositories.Relationships was nil. The panic fires at REGISTRATION/BOOT time
// — when the host mounts the builder at route registration, before serving
// traffic — so a roles-only host learns of the misconfiguration loudly instead
// of 500ing every gated request. Mount it at registration, not lazily.
func (s *Service) RequirePermission(permission string, resource ResourceResolver) web.Middleware {
	if s.relationships == nil {
		panic("authorization: RequirePermission requires the relationship kind (Repositories.Relationships is nil); a roles-only host must not mount it")
	}
	return s.relationships.RequirePermission(permission, resource)
}
