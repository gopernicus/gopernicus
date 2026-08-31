package authorization

import (
	"context"
	"errors"

	"github.com/gopernicus/gopernicus/pockets/authorization/domain/role"
	inbound "github.com/gopernicus/gopernicus/pockets/authorization/internal/inbound/authorization"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// The BUNDLED ROLE-ADMINISTRATION surface (issue #20): the pocket ships the HTTP
// wire for the guarded role-mutation lifecycle it already owns, mounted by
// Register only when the host names a Config.RoleRoutesGate. Absent gate = no
// routes, zero behaviour change — the MachineRoutesGate precedent.
//
// This file holds the root half of the surface: the host-facing AssignmentPolicy
// vocabulary, the construction sentinels that make a contradictory wiring loud,
// and (with the adapter that lands beside them) the SINGLE conversion site
// between the internal transport's request structs and this package's command
// vocabulary.

// Construction sentinels for the bundled role-administration routes. Like
// ErrGuardWithoutMutations these are BOOT-time failures — a misconfigured host
// fails at NewService (or, for the router, at Register) rather than at a later
// request — so they wrap no sdk kind: a construction fault is not a
// client-actionable request error.
var (
	// ErrRoleRoutesGateWithoutRoles is returned by NewService when
	// Config.RoleRoutesGate is set while Repositories.Roles is nil. The bundled
	// routes administer the ROLES kind; with that kind off they could never serve
	// anything, so a gate over them is an authorization policy that can never be
	// consulted — the contradictory wiring fails construction instead of giving
	// false confidence that role administration is protected.
	ErrRoleRoutesGateWithoutRoles = errors.New("authorization: Config.RoleRoutesGate set but Repositories.Roles is nil (no roles kind to administer)")

	// ErrRoleRoutesGateWithoutGuard is returned by NewService when
	// Config.RoleRoutesGate is set while Config.Guard is nil. A nil Guard is the
	// read-only posture, so every bundled WRITE would deterministically fail with
	// ErrMutationsNotConfigured (400) — a deployment the operator must fix, not a
	// request anyone can correct. Config.Guard already implies
	// Repositories.Mutations (ErrGuardWithoutMutations), so this one check covers
	// the whole write path.
	ErrRoleRoutesGateWithoutGuard = errors.New("authorization: Config.RoleRoutesGate requires Config.Guard (the bundled role writes are guarded; without a guard every one of them fails closed)")

	// ErrAssignmentPolicyWithoutRoutes is returned by NewService when
	// Config.AssignmentPolicy is set without Config.RoleRoutesGate. The policy is
	// consulted ONLY on the bundled assign route; with the routes unmounted it is a
	// legality rule that never runs — a silent security no-op, so it fails
	// construction (the ErrAuditWithoutGuard reasoning).
	ErrAssignmentPolicyWithoutRoutes = errors.New("authorization: Config.AssignmentPolicy requires Config.RoleRoutesGate (a policy consulted only by the bundled assign route would never run)")

	// ErrInvalidListStrategy is returned by NewService when Config.ListStrategy is
	// neither the zero value nor crud.StrategyCursor/crud.StrategyOffset. An
	// invalid value is invalid even when orphaned by an unmounted route surface: a
	// bad enum is a typo, never a posture.
	ErrInvalidListStrategy = errors.New(`authorization: Config.ListStrategy must be "cursor" or "offset"`)

	// ErrRoleRoutesWithoutRouter is returned by Register when
	// Config.RoleRoutesGate is set but Mount.Router is nil. Register otherwise
	// tolerates a zero Mount; once routes are PROMISED, a mount with nowhere to
	// register them is loud rather than silently route-free.
	ErrRoleRoutesWithoutRouter = errors.New("authorization: Config.RoleRoutesGate is set but Mount.Router is nil (the bundled role-administration routes have nowhere to mount)")
)

// AssignmentPolicy is the host's optional LEGALITY pre-check over a bundled
// role assignment. It is consulted by the bundled POST /authorization/roles
// route only, immediately before Service.AssignRole, and returns nil to allow
// the command or an error to refuse it.
//
// It is NOT the authorization seam. Three properties define its contract:
//
//   - LEGALITY ONLY, over the command SHAPE — unknown role names, global-only
//     rules, closed scope registries, machine subjects barred. A policy that
//     needs to read authorization STATE belongs in Config.Guard, which runs
//     inside the atomic mutation boundary with a revision-tracked DecisionView;
//     a route-level state read would reinstate the detached check-then-write
//     race (AZ3-0.5) this pocket eliminated.
//   - ASSIGN ONLY — there is deliberately no symmetric unassignment hook. This
//     mirrors Config.RoleModel, which governs assignment legality while
//     unassignment and every read path stay opaque. Revocation AUTHORIZATION
//     already flows through Config.Guard, which sees OpRoleUnassign and the
//     scope kind atomically.
//   - BUNDLED ROUTES ONLY, and NOT audited — a host driving Service.AssignRole
//     directly is unaffected, and a policy refusal is never observed by
//     Config.Audit (the sink records guard outcomes; a refusal here never
//     reaches the guard).
//
// Error mapping: a refusal should wrap an sdk sentinel (sdk.ErrForbidden or
// sdk.ErrInvalidInput) or be a web.NewSafeDomainError carrying a custom safe
// sentence, because the handler answers through web.RespondJSONDomainError. An
// unwrapped bare error therefore lands 500 BY DESIGN — the same contract
// MutationGuard documents.
type AssignmentPolicy func(ctx context.Context, cmd AssignRoleCommand) error

// validateListStrategy accepts the zero value (which resolves to
// crud.StrategyCursor at the transport) plus the two named strategies, and
// rejects anything else with ErrInvalidListStrategy.
func validateListStrategy(s crud.Strategy) error {
	switch s {
	case "", crud.StrategyCursor, crud.StrategyOffset:
		return nil
	}
	return ErrInvalidListStrategy
}

// roleRouteAdapter is the SINGLE conversion site between the bundled transport's
// request structs and this package's command vocabulary. Actor,
// AssignRoleCommand, UnassignRoleCommand, and UnassignRoleResult are root types
// and the root imports the transport to mount it, so the transport can never
// import them back; it declares a narrow port over the domain packages instead
// and this adapter is the one place the two shapes meet. Keeping it a single
// site is what makes the duplicated command shape safe: it is unit-tested
// field for field.
//
// It also owns the two things the transport cannot: minting or validating the
// MutationID, and consulting the host AssignmentPolicy before the guarded
// assign.
type roleRouteAdapter struct {
	svc    *Service
	policy AssignmentPolicy
}

var _ inbound.RoleAdminService = roleRouteAdapter{}

// AssignRole converts, runs the optional legality policy, then drives the
// guarded Service.AssignRole. The policy runs AFTER conversion (so it sees the
// exact command that would be applied, resolved MutationID included) and BEFORE
// the service, so a refusal never reaches the guard, the store, or the audit
// sink.
func (a roleRouteAdapter) AssignRole(ctx context.Context, req inbound.AssignRoleRequest) (*Receipt, error) {
	cmd, err := a.assignCommand(req)
	if err != nil {
		return nil, err
	}
	if a.policy != nil {
		if err := a.policy(ctx, cmd); err != nil {
			return nil, err
		}
	}
	return a.svc.AssignRole(ctx, actorFrom(req.ActorType, req.ActorID), cmd)
}

// UnassignRole converts and drives the guarded Service.UnassignRole, splitting
// the op-specific result into the receipt and the same_role_grant_remains
// annotation the transport reports top-level. The AssignmentPolicy is
// deliberately NOT consulted here (see [AssignmentPolicy]): revocation
// authorization belongs to Config.Guard, inside the atomic boundary.
func (a roleRouteAdapter) UnassignRole(ctx context.Context, req inbound.UnassignRoleRequest) (*Receipt, bool, error) {
	cmd, err := a.unassignCommand(req)
	if err != nil {
		return nil, false, err
	}
	result, err := a.svc.UnassignRole(ctx, actorFrom(req.ActorType, req.ActorID), cmd)
	if err != nil {
		return nil, false, err
	}
	return result.Receipt, result.SameRoleGrantRemains, nil
}

// ListBySubject pages a subject's role assignments.
func (a roleRouteAdapter) ListBySubject(ctx context.Context, subjectType, subjectID string, req crud.ListRequest) (crud.Page[role.Assignment], error) {
	return a.svc.ListRoleAssignmentsBySubject(ctx, PrincipalRef{Type: subjectType, ID: subjectID}, req)
}

// ListByResource pages the RAW direct-scope assignments stored at a resource.
func (a roleRouteAdapter) ListByResource(ctx context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[role.Assignment], error) {
	return a.svc.ListRoleAssignmentsByResource(ctx, resourceType, resourceID, req)
}

// ListEffectiveByResource pages the EFFECTIVE role grants on a resource.
func (a roleRouteAdapter) ListEffectiveByResource(ctx context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[role.EffectiveGrant], error) {
	return a.svc.ListEffectiveRoleGrantsByResource(ctx, resourceType, resourceID, req)
}

// assignCommand builds the AssignRoleCommand a bundled request describes.
func (a roleRouteAdapter) assignCommand(req inbound.AssignRoleRequest) (AssignRoleCommand, error) {
	id, err := resolveMutationID(req.MutationID)
	if err != nil {
		return AssignRoleCommand{}, err
	}
	return AssignRoleCommand{
		MutationID:       id,
		Subject:          PrincipalRef{Type: req.SubjectType, ID: req.SubjectID},
		Role:             req.Role,
		ResourceType:     req.ResourceType,
		ResourceID:       req.ResourceID,
		ExpectedRevision: revisionFrom(req.ExpectedRevision),
	}, nil
}

// unassignCommand builds the UnassignRoleCommand a bundled request describes.
func (a roleRouteAdapter) unassignCommand(req inbound.UnassignRoleRequest) (UnassignRoleCommand, error) {
	id, err := resolveMutationID(req.MutationID)
	if err != nil {
		return UnassignRoleCommand{}, err
	}
	return UnassignRoleCommand{
		MutationID:       id,
		Subject:          PrincipalRef{Type: req.SubjectType, ID: req.SubjectID},
		Role:             req.Role,
		ResourceType:     req.ResourceType,
		ResourceID:       req.ResourceID,
		ExpectedRevision: revisionFrom(req.ExpectedRevision),
	}, nil
}

// actorFrom builds the untrusted Actor from the principal the host gate's
// authenticating layer stashed. The transport already refused (401) when there
// was none, so this never fabricates one.
func actorFrom(principalType, principalID string) Actor {
	return Actor{PrincipalRef: PrincipalRef{Type: principalType, ID: principalID}}
}

// resolveMutationID mints an unguessable id when the client supplied none, and
// otherwise validates the client's own key.
//
// Client-supplied ids are KEPT because retry idempotency is the point of the
// receipts rail: a client that retries with its own id dedups against the stored
// receipt instead of double-granting. The squat surface is bounded and
// documented: the population behind the gate is the host's administrators, an id
// must clear MutationID.Validate's strength floor, and a squatted id yields a
// payload-mismatch conflict, never a silent overwrite.
func resolveMutationID(raw string) (MutationID, error) {
	if raw == "" {
		return NewMutationID()
	}
	id := MutationID(raw)
	if err := id.Validate(); err != nil {
		return "", err
	}
	return id, nil
}

// revisionFrom converts the transport's optional compare-and-set anchor into the
// domain Revision pointer. Nil stays nil — no expectation.
func revisionFrom(v *uint64) *Revision {
	if v == nil {
		return nil
	}
	rev := Revision(*v)
	return &rev
}
