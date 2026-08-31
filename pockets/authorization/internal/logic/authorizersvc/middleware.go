package authorizersvc

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// ErrAlternativeNotApplicable is the ONE resolver error RequireAnyPermission
// does not fail the request on: a ResourceResolver returning an error that
// errors.Is it declares "this alternative does not apply to this request" (the
// row names no organization, the path carries no tenant), and evaluation skips
// to the next alternative exactly as a deny does. All alternatives denied or
// inapplicable is the ordinary 403.
//
// It is deliberately narrow. ANY other resolver error — and every Check error —
// still fails the WHOLE request closed: the sentinel must never let a store
// outage or a resolver bug be swallowed into a later alternative's allow. It is
// also not a decision outcome, so it carries no sdk taxonomy kind and takes no
// part in ReasonFor/RespondError; the root authorization package re-exports this
// exact sentinel for hosts to wrap.
var ErrAlternativeNotApplicable = errors.New("authorization: gate alternative not applicable to this request")

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

// GateSpec is ONE alternative of a RequireAnyPermission disjunction: the
// (ResourceType, Permission) coordinates checked at REGISTRATION against the
// Declarer, plus the resolver that names the resource at request time. Resource
// is required — a nil resolver panics at mount rather than resolving a
// zero-value resource.
type GateSpec struct {
	ResourceType string
	Permission   string
	Resource     ResourceResolver
}

// Gates is the ONE implementation of the RequirePermission family, built over a
// Checker and a Declarer so every decision surface — the relationship engine
// here, the composite decider in decisionsvc — mounts the SAME HTTP ladder
// (401/403/500/503, fail closed, FS9 bodies) and the same registration-time
// legality check. There is deliberately no second gate body to drift from.
type Gates struct {
	checker         Checker
	declarer        Declarer
	maxAlternatives int
}

// NewGates builds the gate family over the decision surface that answers its
// checks and the model that declares its coordinates. They are usually the same
// value (a *Service, a *decisionsvc.Composite); they are separate parameters
// because the two roles are separate contracts.
//
// maxAlternatives caps one RequireAnyPermission route line and must be the
// RESOLVED EvaluationLimits.MaxBatchSize: a disjunction is a sequential N-Check
// loop, so an uncapped route line multiplies per-request store work without
// bound. Both shipped call sites pass the resolved budget they already hold.
func NewGates(checker Checker, declarer Declarer, maxAlternatives int) Gates {
	return Gates{checker: checker, declarer: declarer, maxAlternatives: maxAlternatives}
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

// RequireAnyPermission returns web.Middleware admitting a route when ANY of the
// alternatives allows — the disjunction on the route line, through this one
// shared gate body instead of a hand-rolled OR in the handler:
//
//	r.GET("/orgs/{orgID}/projects/{projectID}", h.show, svc.RequireAnyPermission(
//		authorization.GateSpec{ResourceType: "project", Permission: "view", Resource: authorization.PathResource("project", "projectID")},
//		authorization.GateSpec{ResourceType: "org", Permission: "admin", Resource: authorization.PathResource("org", "orgID")},
//	))
//
// REGISTRATION (all panic at mount, each message naming the alternative's index
// and pair): zero alternatives; a nil Resource resolver; a (ResourceType,
// Permission) pair no model declares — for the composite that means neither the
// relationship Schema nor the RoleModel; more alternatives than
// EvaluationLimits.MaxBatchSize, the cap that keeps one route line from
// multiplying per-request store work without bound.
//
// REQUEST TIME, the same 401/403/500/503 ladder RequirePermission walks:
//   - no Principal on the context → 401.
//   - alternatives are evaluated STRICTLY IN ORDER, resolve-then-Check, and the
//     first allow SHORT-CIRCUITS to next — later alternatives are not consulted.
//   - a resolver returning ErrAlternativeNotApplicable SKIPS that alternative
//     exactly as a deny does (see the sentinel).
//   - any other resolver error, a resolved Resource.Type disagreeing with the
//     alternative's declared ResourceType, or a Check error fails the WHOLE
//     request closed — 503 for ErrEvaluationLimit, 500 otherwise — EVEN IF a
//     later alternative would have allowed. The type-agreement check is a
//     programming fault, not inapplicability: mustDeclare validated the declared
//     pair, so a disagreeing resolver would evaluate a pair no model declares and
//     deny forever behind a green mount.
//   - every alternative denied or inapplicable → 403.
//
// ORDER IS OUTCOME-AFFECTING, not a cost knob: under whole-request fail-closed,
// an erroring alternative 1 means alternative 2's allow is never reached. Order
// by which alternative should decide, not by which is cheapest. Duplicate
// alternatives (the same pair with different resolvers) are legal and evaluated
// independently. Each alternative costs one budget-bounded Check, so a hot route
// pays N of them.
//
// It is PURE Check with no bypass hook, and there is no nesting: an AND of ORs is
// stacked middleware, as it is today.
func (g Gates) RequireAnyPermission(alternatives ...GateSpec) web.Middleware {
	if len(alternatives) == 0 {
		panic("authorization: RequireAnyPermission needs at least one alternative")
	}
	if len(alternatives) > g.maxAlternatives {
		panic(fmt.Sprintf("authorization: RequireAnyPermission accepts at most %d alternatives (EvaluationLimits.MaxBatchSize), got %d", g.maxAlternatives, len(alternatives)))
	}
	total := len(alternatives)
	for i, alt := range alternatives {
		if alt.Resource == nil {
			panic(fmt.Sprintf("authorization: RequireAnyPermission alternative %d of %d (%q, %q) needs a Resource resolver", i+1, total, alt.ResourceType, alt.Permission))
		}
		if !g.declarer.DeclaresPermission(alt.ResourceType, alt.Permission) {
			panic(fmt.Sprintf("authorization: RequireAnyPermission alternative %d of %d: the model declares no permission %q on resource type %q — fix the gate or the schema", i+1, total, alt.Permission, alt.ResourceType))
		}
	}

	specs := append([]GateSpec(nil), alternatives...)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok := identity.FromContext(r.Context())
			if !ok {
				web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
				return
			}
			for _, alt := range specs {
				res, err := alt.Resource(r)
				if err != nil {
					if errors.Is(err, ErrAlternativeNotApplicable) {
						continue
					}
					web.RespondJSONError(w, web.ErrInternal("internal error"))
					return
				}
				if res.Type != alt.ResourceType {
					web.RespondJSONError(w, web.ErrInternal("internal error"))
					return
				}
				result, err := g.checker.Check(r.Context(), CheckRequest{
					Principal:  PrincipalRef{Type: principal.Type, ID: principal.ID},
					Permission: alt.Permission,
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
				if result.Allowed {
					next.ServeHTTP(w, r)
					return
				}
			}
			web.RespondJSONError(w, web.ErrForbidden("permission denied"))
		})
	}
}

func (g Gates) mustDeclare(resourceType, permission string) {
	if !g.declarer.DeclaresPermission(resourceType, permission) {
		panic(fmt.Sprintf("authorization: the model declares no permission %q on resource type %q — fix the gate or the schema", permission, resourceType))
	}
}

// gates is the engine's own gate family: it checks and declares against the
// compiled relationship schema.
func (s *Service) gates() Gates { return NewGates(s, s, s.limits.MaxBatchSize) }

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

// RequireAnyPermission admits a route when any alternative allows, every pair
// checked at registration against the compiled schema — see
// Gates.RequireAnyPermission for the full ladder and the alternatives cap.
func (s *Service) RequireAnyPermission(alternatives ...GateSpec) web.Middleware {
	return s.gates().RequireAnyPermission(alternatives...)
}
