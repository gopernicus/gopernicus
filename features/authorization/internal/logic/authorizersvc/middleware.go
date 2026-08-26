package authorizersvc

import (
	"errors"
	"fmt"
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

// PathResource resolves resourceType:<value of the named path parameter>. An
// empty value is a resolver error — the gate fails closed (500), which is the
// honest answer to a parameter name that does not match the route pattern.
func PathResource(resourceType, param string) ResourceResolver {
	return func(r *http.Request) (Resource, error) {
		id := web.Param(r, param)
		if id == "" {
			return Resource{}, fmt.Errorf("authorization: path parameter %q is empty (does the route pattern name it?)", param)
		}
		return Resource{Type: resourceType, ID: id}, nil
	}
}

// RequirePermissionOn is RequirePermission in COORDINATES — the route line
// reads as its own authorization question:
//
//	r.GET("/orgs/{orgID}/people", h.people, svc.RequirePermissionOn("org", "view", "orgID"))
//
// The coordinates are load-bearing and checked at REGISTRATION against the
// compiled model: a (resourceType, permission) pair the model does not declare,
// or an empty parameter name, panics when the route is mounted — never a gate
// that quietly checks something the model never grants. Request semantics are
// RequirePermission's over PathResource.
func (s *Service) RequirePermissionOn(resourceType, permission, pathParam string) web.Middleware {
	s.mustDeclare(resourceType, permission)
	if pathParam == "" {
		panic(fmt.Sprintf("authorization: RequirePermissionOn(%q, %q) needs a path parameter name", resourceType, permission))
	}
	return s.RequirePermission(permission, PathResource(resourceType, pathParam))
}

// RequirePermissionFixed is the coordinate form over one named resource —
// e.g. RequirePermissionFixed("platform", "admin", "main") — with the same
// registration-time legality check as RequirePermissionOn.
func (s *Service) RequirePermissionFixed(resourceType, permission, resourceID string) web.Middleware {
	s.mustDeclare(resourceType, permission)
	if resourceID == "" {
		panic(fmt.Sprintf("authorization: RequirePermissionFixed(%q, %q) needs a resource id", resourceType, permission))
	}
	return s.RequirePermission(permission, FixedResource(resourceType, resourceID))
}

func (s *Service) mustDeclare(resourceType, permission string) {
	if !s.compiled.declaresPermission(resourceType, permission) {
		panic(fmt.Sprintf("authorization: the model declares no permission %q on resource type %q — fix the gate or the schema", permission, resourceType))
	}
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
//   - Check hit the evaluation budget (ErrEvaluationLimit / sdk.ErrUnavailable) →
//     503, fail closed — the indeterminate/limit taxonomy (default #9) surfaced
//     as a retryable "temporarily unavailable" rather than a flat 500.
//   - any other Check error → 500, fail closed (D-D: RequirePermission fails
//     CLOSED, the deliberate opposite of ratelimiter.Middleware's fail-open
//     posture — do not "harmonize" them).
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
				Principal:  PrincipalRef{Type: principal.Type, ID: principal.ID},
				Permission: permission,
				Resource:   res,
			})
			if err != nil {
				if errors.Is(err, ErrEvaluationLimit) {
					web.RespondJSONError(w, web.NewError(http.StatusServiceUnavailable, "authorization temporarily unavailable"))
					return
				}
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
