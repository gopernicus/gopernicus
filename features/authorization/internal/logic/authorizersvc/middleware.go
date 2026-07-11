package authorizersvc

import (
	"net/http"

	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// ResourceResolver extracts the resource to authorize from the request. A
// resolver error fails the request CLOSED (a 500), never falling through to a
// Check on a zero-value resource.
type ResourceResolver func(r *http.Request) (Resource, error)

// FixedResource always resolves the same resource, ignoring the request — the
// auth-cms demo case where the gated route protects one known resource.
func FixedResource(resourceType, resourceID string) ResourceResolver {
	res := Resource{Type: resourceType, ID: resourceID}
	return func(*http.Request) (Resource, error) { return res, nil }
}

// RequirePermission returns web.Middleware gating next on the context Principal
// holding permission on the resolved resource. It is PURE Check — the engine
// evaluates the schema and nothing else: there is no bypass hook, so a host
// wanting platform-admin/self-access composes those recipes in its own closure
// around this middleware (the f9397ac posture).
//
// Semantics, all written through sdk/foundation/web (FS9 body shape):
//   - no Principal on the context (identity.FromContext !ok) → 401.
//   - resolver error → 500, fail closed.
//   - Check error → 500, fail closed (D-D: RequirePermission fails CLOSED,
//     the deliberate opposite of ratelimiter.Middleware's fail-open posture —
//     do not "harmonize" them).
//   - Check denies (!Allowed) → 403.
//   - otherwise next runs against the original request.
func (s *Service) RequirePermission(permission string, resource ResourceResolver) web.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok := identity.FromContext(r.Context())
			if !ok {
				web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
				return
			}
			res, err := resource(r)
			if err != nil {
				web.RespondJSONError(w, web.ErrInternal("internal error"))
				return
			}
			result, err := s.Check(r.Context(), CheckRequest{
				Subject:    Subject{Type: principal.Type, ID: principal.ID},
				Permission: permission,
				Resource:   res,
			})
			if err != nil {
				web.RespondJSONError(w, web.ErrInternal("internal error"))
				return
			}
			if !result.Allowed {
				web.RespondJSONError(w, web.ErrForbidden("permission denied"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
