package authorizationhttp

import "github.com/gopernicus/gopernicus/sdk/pkg/web"

// RequirePermission returns web.Middleware gating a route on the context
// Principal holding permission on the resolved resource. It builds the inbound
// gates over the composite decision surface. The shared inbound implementation
// writes the 401/403/500/503 responses through sdk/pkg/web.
//
// PURE Check, no bypass hook: a host wanting platform-admin/self-access composes
// those recipes as its OWN closure around this middleware (auth-cms's
// isPlatformAdmin is the flagship demonstration). D-D: it fails CLOSED (Check
// error → 500), the deliberate opposite of ratelimiter.Middleware's fail-open.
//
// RequirePermission needs a MODEL-BEARING kind — WithRelationshipModel for the
// relationship kind or WithRoleModel for the roles kind — and panics when
// neither is wired. The panic fires at REGISTRATION/BOOT time — when the host
// mounts the builder at route registration, before serving traffic — so a
// modelless host learns of the misconfiguration loudly instead of 500ing every
// gated request. Mount it at registration, not lazily.
func (s *Adapter) RequirePermission(permission string, resource ResourceResolver) web.Middleware {
	if s.decisions == nil {
		panic("authorization: RequirePermission requires a decision-capable kind (WithRelationshipModel or WithRoleModel); a roles-only host without a role model must not mount it")
	}
	return s.gates().RequirePermission(permission, resource)
}

// RequirePermissionOn is RequirePermission in coordinates — (resourceType,
// permission, pathParam) — so a route line reads as its own authorization
// question. The pair is checked against BOTH compiled models at REGISTRATION: a
// pair neither declares, or an empty parameter name, panics when the route is
// mounted. Same model-bearing-kind precondition as RequirePermission.
func (s *Adapter) RequirePermissionOn(resourceType, permission, pathParam string) web.Middleware {
	if s.decisions == nil {
		panic("authorization: RequirePermissionOn requires a decision-capable kind (WithRelationshipModel or WithRoleModel); a roles-only host without a role model must not mount it")
	}
	return s.gates().RequirePermissionOn(resourceType, permission, pathParam)
}

// RequirePermissionFixed is the coordinate form over one named resource, with
// the same registration-time legality check as RequirePermissionOn.
func (s *Adapter) RequirePermissionFixed(resourceType, permission, resourceID string) web.Middleware {
	if s.decisions == nil {
		panic("authorization: RequirePermissionFixed requires a decision-capable kind (WithRelationshipModel or WithRoleModel); a roles-only host without a role model must not mount it")
	}
	return s.gates().RequirePermissionFixed(resourceType, permission, resourceID)
}

// RequireAnyPermission returns web.Middleware admitting a route when ANY of the
// alternatives allows — the disjunction on the route line instead of a
// hand-rolled OR in the handler:
//
//	r.GET("/orgs/{orgID}/projects/{projectID}", h.show, authorizer.RequireAnyPermission(
//		authorization.GateSpec{ResourceType: "project", Permission: "view", Resource: authorization.PathResource("project", "projectID")},
//		authorization.GateSpec{ResourceType: "org", Permission: "admin", Resource: authorization.PathResource("org", "orgID")},
//	))
//
// Each alternative is decided by the model that OWNS its pair, so one route line
// may disjoin a relationship-owned pair with a role-owned one. Registration
// panics — at mount, before traffic — on zero alternatives, a nil Resource
// resolver, a pair NEITHER model declares, or more alternatives than
// EvaluationLimits.MaxBatchSize; each message names the alternative's index and
// pair. Same model-bearing-kind precondition as RequirePermission.
//
// Request time: alternatives are evaluated STRICTLY IN ORDER and the first allow
// short-circuits; a resolver returning ErrAlternativeNotApplicable skips that
// alternative as a deny does; any OTHER resolver error, a resolved Resource.Type
// disagreeing with the alternative's ResourceType, or a Check error fails the
// WHOLE request closed (503 for ErrEvaluationLimit, else 500) even when a later
// alternative would have allowed; every alternative denied or inapplicable is
// 403. Order is therefore OUTCOME-AFFECTING, not a cost knob. This root package
// writes NO HTTP — the ladder lives in the one shared gate body.
func (s *Adapter) RequireAnyPermission(alternatives ...GateSpec) web.Middleware {
	if s.decisions == nil {
		panic("authorization: RequireAnyPermission requires a decision-capable kind (WithRelationshipModel or WithRoleModel); a roles-only host without a role model must not mount it")
	}
	return s.gates().RequireAnyPermission(alternatives...)
}

func (s *Adapter) gates() Gates {
	return NewGates(s.decisions, s.decisions, s.decisions.Limits().MaxBatchSize)
}
