package authorization

import (
	"context"
	"errors"

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
