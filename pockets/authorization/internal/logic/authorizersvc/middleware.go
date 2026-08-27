package authorizersvc

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// Checker answers ONE authorization decision — the request-time half of a gate.
// *Service satisfies it with the relationship engine; the composite decider
// satisfies it across every model-bearing kind.
type Checker interface {
	Check(ctx context.Context, req CheckRequest) (CheckResult, error)
}

// Declarer reports whether a (resourceType, permission) pair is declared by a
// model — the registration-time half of a gate, which is what makes an
// undeclared coordinate pair panic at mount instead of denying every request.
type Declarer interface {
	DeclaresPermission(resourceType, permission string) bool
}

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

// Gates is the ONE implementation of the RequirePermission family, built over a
// Checker and a Declarer so every decision surface — the relationship engine
// here, the composite decider in decisionsvc — mounts the SAME HTTP ladder
// (401/403/500/503, fail closed, FS9 bodies) and the same registration-time
// legality check. There is deliberately no second gate body to drift from.
type Gates struct {
	checker  Checker
	declarer Declarer
}

// NewGates builds the gate family over the decision surface that answers its
// checks and the model that declares its coordinates. They are usually the same
// value (a *Service, a *decisionsvc.Composite); they are separate parameters
// because the two roles are separate contracts.
func NewGates(checker Checker, declarer Declarer) Gates {
	return Gates{checker: checker, declarer: declarer}
}

// RequirePermission returns web.Middleware gating next on the context Principal
// holding permission on the resolved resource. It is PURE Check — the decision
// surface evaluates the model and nothing else: there is no bypass hook, so a
// host wanting platform-admin/self-access composes those recipes in its own
// closure around this middleware (the f9397ac posture).
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
func (g Gates) RequirePermission(permission string, resource ResourceResolver) web.Middleware {
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
			result, err := g.checker.Check(r.Context(), CheckRequest{
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

// RequirePermissionOn is RequirePermission in COORDINATES — the route line
// reads as its own authorization question:
//
//	r.GET("/orgs/{orgID}/people", h.people, svc.RequirePermissionOn("org", "view", "orgID"))
//
// The coordinates are load-bearing and checked at REGISTRATION against the
// Declarer: a (resourceType, permission) pair no model declares, or an empty
// parameter name, panics when the route is mounted — never a gate that quietly
// checks something no model grants. Request semantics are RequirePermission's
// over PathResource.
func (g Gates) RequirePermissionOn(resourceType, permission, pathParam string) web.Middleware {
	g.mustDeclare(resourceType, permission)
	if pathParam == "" {
		panic(fmt.Sprintf("authorization: RequirePermissionOn(%q, %q) needs a path parameter name", resourceType, permission))
	}
	return g.RequirePermission(permission, PathResource(resourceType, pathParam))
}

// RequirePermissionFixed is the coordinate form over one named resource —
// e.g. RequirePermissionFixed("platform", "admin", "main") — with the same
// registration-time legality check as RequirePermissionOn.
func (g Gates) RequirePermissionFixed(resourceType, permission, resourceID string) web.Middleware {
	g.mustDeclare(resourceType, permission)
	if resourceID == "" {
		panic(fmt.Sprintf("authorization: RequirePermissionFixed(%q, %q) needs a resource id", resourceType, permission))
	}
	return g.RequirePermission(permission, FixedResource(resourceType, resourceID))
}

func (g Gates) mustDeclare(resourceType, permission string) {
	if !g.declarer.DeclaresPermission(resourceType, permission) {
		panic(fmt.Sprintf("authorization: the model declares no permission %q on resource type %q — fix the gate or the schema", permission, resourceType))
	}
}

// gates is the engine's own gate family: it checks and declares against the
// compiled relationship schema.
func (s *Service) gates() Gates { return NewGates(s, s) }

// RequirePermission gates a route on the relationship engine's Check. It is the
// shared Gates body — see Gates.RequirePermission for the full HTTP ladder.
func (s *Service) RequirePermission(permission string, resource ResourceResolver) web.Middleware {
	return s.gates().RequirePermission(permission, resource)
}

// RequirePermissionOn is RequirePermission in coordinates, with the pair checked
// at registration against the compiled schema — see Gates.RequirePermissionOn.
func (s *Service) RequirePermissionOn(resourceType, permission, pathParam string) web.Middleware {
	return s.gates().RequirePermissionOn(resourceType, permission, pathParam)
}

// RequirePermissionFixed is the coordinate form over one named resource — see
// Gates.RequirePermissionFixed.
func (s *Service) RequirePermissionFixed(resourceType, permission, resourceID string) web.Middleware {
	return s.gates().RequirePermissionFixed(resourceType, permission, resourceID)
}
