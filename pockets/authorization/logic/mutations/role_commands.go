package mutations

import authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"

// Actor is the concrete principal on whose behalf a guarded write is attempted.
// Trusted writes use the separately held SystemMutator, never a privileged Actor.
type Actor struct{ authmodel.PrincipalRef }

func (a Actor) Validate() error { return a.PrincipalRef.Validate() }

// AssignRoleCommand grants a concrete principal an opaque role, either globally
// (both resource fields empty) or scoped to a resource (both set). A duplicate exact
// assignment is a no_change result.
type AssignRoleCommand struct {
	Subject      authmodel.PrincipalRef // concrete principal — a userset subject is structurally impossible
	Role         string
	ResourceType string // "" with empty ResourceID ⇒ a global assignment
	ResourceID   string
}

// UnassignRoleCommand removes a concrete principal's exact opaque-role assignment at
// the given scope. Unassigning an absent assignment is a committed not_found no-op,
// not an error.
type UnassignRoleCommand struct {
	Subject      authmodel.PrincipalRef
	Role         string
	ResourceType string
	ResourceID   string
}

// UnassignRoleResult describes the outcome and the same-role global fallback.
// SameRoleGrantRemains is computed inside the transaction, including no-op calls.
// It is true only for a scoped unassign with a surviving global grant of that role;
// it does not describe permissions granted by other roles or relationships.
type UnassignRoleResult struct {
	Outcome              Outcome
	SameRoleGrantRemains bool
}
